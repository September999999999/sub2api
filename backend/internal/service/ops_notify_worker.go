package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/redis/go-redis/v9"
)

type OpsNotifyWorkerConfig struct {
	StreamKey string
	DLQKey    string
	Group     string
	Consumer  string

	DryRun      bool
	Concurrency int

	// Streams pending reclaim.
	ClaimMinIdle   time.Duration
	ClaimBatchSize int64

	// Attempts persist across worker restarts so persistent failures can
	// eventually go to DLQ instead of retrying forever.
	AttemptTTL  time.Duration
	MaxAttempts int
	Backoffs    []time.Duration

	// Alert policy (worker-level safeguards).
	AlertDedupTTL     time.Duration
	AlertRateLimitPer int64
}

type OpsNotifyWorker struct {
	rdb             *redis.Client
	notificationSvc *NotificationService
	cfg             OpsNotifyWorkerConfig
}

func NewOpsNotifyWorker(rdb *redis.Client, notificationSvc *NotificationService, cfg OpsNotifyWorkerConfig) *OpsNotifyWorker {
	if cfg.StreamKey == "" {
		cfg.StreamKey = OpsNotifyStreamKeyDefault
	}
	if cfg.DLQKey == "" {
		cfg.DLQKey = OpsNotifyDLQStreamKeyDefault
	}
	if cfg.Group == "" {
		cfg.Group = OpsNotifyConsumerGroupDefault
	}
	if cfg.Consumer == "" {
		cfg.Consumer = "ops-notify-consumer"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.ClaimMinIdle <= 0 {
		// Ensure we don't reclaim messages that are being retried with backoff.
		cfg.ClaimMinIdle = 2 * time.Minute
	}
	if cfg.ClaimBatchSize <= 0 {
		cfg.ClaimBatchSize = 32
	}
	if cfg.AttemptTTL <= 0 {
		cfg.AttemptTTL = 24 * time.Hour
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if len(cfg.Backoffs) == 0 {
		cfg.Backoffs = []time.Duration{10 * time.Second, 60 * time.Second, 300 * time.Second}
	}
	if cfg.AlertDedupTTL <= 0 {
		cfg.AlertDedupTTL = 5 * time.Minute
	}
	if cfg.AlertRateLimitPer <= 0 {
		cfg.AlertRateLimitPer = 30
	}

	return &OpsNotifyWorker{rdb: rdb, notificationSvc: notificationSvc, cfg: cfg}
}

func (w *OpsNotifyWorker) Run(ctx context.Context) error {
	if w == nil || w.rdb == nil {
		return errors.New("ops notify worker redis is nil")
	}
	if w.notificationSvc == nil {
		return errors.New("ops notify worker notification service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if err := w.ensureGroup(ctx); err != nil {
		return err
	}

	msgCh := make(chan redis.XMessage, w.cfg.Concurrency*16)
	for i := 0; i < w.cfg.Concurrency; i++ {
		go w.workerLoop(ctx, msgCh)
	}

	log.Printf("[OpsNotifyWorker] started stream=%s group=%s consumer=%s concurrency=%d dry_run=%v", w.cfg.StreamKey, w.cfg.Group, w.cfg.Consumer, w.cfg.Concurrency, w.cfg.DryRun)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		w.reclaimStale(ctx, msgCh)

		streams, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    w.cfg.Group,
			Consumer: w.cfg.Consumer,
			Streams:  []string{w.cfg.StreamKey, ">"},
			Count:    32,
			Block:    2 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			log.Printf("[OpsNotifyWorker] XREADGROUP failed: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if len(streams) == 0 {
			continue
		}

		for _, s := range streams {
			for _, msg := range s.Messages {
				select {
				case msgCh <- msg:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

func (w *OpsNotifyWorker) workerLoop(ctx context.Context, msgCh <-chan redis.XMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-msgCh:
			w.handleMessage(ctx, msg)
		}
	}
}

func (w *OpsNotifyWorker) ensureGroup(ctx context.Context) error {
	err := w.rdb.XGroupCreateMkStream(ctx, w.cfg.StreamKey, w.cfg.Group, "$").Err()
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "busygroup") {
		return nil
	}
	return fmt.Errorf("create consumer group: %w", err)
}

func (w *OpsNotifyWorker) reclaimStale(ctx context.Context, msgCh chan<- redis.XMessage) {
	if ctx.Err() != nil {
		return
	}

	msgs, _, err := w.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   w.cfg.StreamKey,
		Group:    w.cfg.Group,
		Consumer: w.cfg.Consumer,
		MinIdle:  w.cfg.ClaimMinIdle,
		Start:    "0-0",
		Count:    w.cfg.ClaimBatchSize,
	}).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			log.Printf("[OpsNotifyWorker] XAUTOCLAIM failed: %v", err)
		}
		return
	}
	for _, m := range msgs {
		select {
		case msgCh <- m:
		case <-ctx.Done():
			return
		}
	}
}

func (w *OpsNotifyWorker) handleMessage(ctx context.Context, msg redis.XMessage) {
	if ctx.Err() != nil {
		return
	}

	job, raw, err := parseOpsNotifyJob(msg)
	if err != nil {
		log.Printf("[OpsNotifyWorker] invalid job message stream_id=%s err=%v", msg.ID, err)
		_ = w.moveToDLQ(ctx, msg.ID, raw, err, 1)
		_ = w.ackAndDelete(ctx, msg.ID)
		return
	}

	if job.Kind == OpsNotifyJobKindAlert {
		if !w.allowAlertByDedup(ctx, job) {
			log.Printf("[OpsNotifyWorker] alert suppressed by dedup kind=%s type=%s job_id=%s", job.Kind, job.Type, job.ID)
			_ = w.ackAndDelete(ctx, msg.ID)
			return
		}
		if !w.allowAlertByRateLimit(ctx) {
			log.Printf("[OpsNotifyWorker] alert suppressed by rate-limit kind=%s type=%s job_id=%s", job.Kind, job.Type, job.ID)
			_ = w.ackAndDelete(ctx, msg.ID)
			return
		}
	}

	if w.cfg.DryRun {
		log.Printf("[OpsNotifyWorker] dry-run ack kind=%s type=%s job_id=%s", job.Kind, job.Type, job.ID)
		_ = w.ackAndDelete(ctx, msg.ID)
		return
	}

	sendErr := w.deliverWithRetry(ctx, msg.ID, job, raw)
	if sendErr != nil {
		if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) {
			return
		}
		log.Printf("[OpsNotifyWorker] deliver failed kind=%s type=%s job_id=%s stream_id=%s err=%v", job.Kind, job.Type, job.ID, msg.ID, sendErr)
	}

	_ = w.ackAndDelete(ctx, msg.ID)
}

func (w *OpsNotifyWorker) deliverWithRetry(ctx context.Context, streamID string, job OpsNotifyJob, raw string) error {
	attemptKey := w.attemptKey(job, streamID)
	attempted, _ := w.rdb.Get(ctx, attemptKey).Int()
	if attempted < 0 {
		attempted = 0
	}

	msg := model.NotificationMessage{
		Type:     model.NotificationType(strings.TrimSpace(job.Type)),
		Title:    strings.TrimSpace(job.Title),
		Content:  strings.TrimSpace(job.Content),
		Severity: strings.TrimSpace(job.Severity),
		Metadata: job.Metadata,
	}
	if msg.Type == "" {
		msg.Type = model.NotificationType("ops")
	}
	if msg.Title == "" {
		msg.Title = "Ops Notification"
	}
	if msg.Severity == "" {
		msg.Severity = "info"
	}

	platformID := strings.TrimSpace(job.PlatformID)
	if platformID == "" {
		err := errors.New("missing platform_id")
		_ = w.moveToDLQ(ctx, streamID, raw, err, 1)
		return err
	}

	var lastErr error
	for attemptNum := attempted + 1; attemptNum <= w.cfg.MaxAttempts; attemptNum++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		backoff := w.backoffForAttempt(attemptNum)
		if backoff > 0 {
			if err := opsSleepWithContext(ctx, backoff); err != nil {
				return err
			}
		}

		err := w.notificationSvc.SendToPlatform(ctx, msg, platformID)
		if err == nil {
			_ = w.rdb.Del(ctx, attemptKey).Err()
			return nil
		}
		lastErr = err
		_ = w.rdb.Set(ctx, attemptKey, attemptNum, w.cfg.AttemptTTL).Err()
	}

	_ = w.rdb.Del(ctx, attemptKey).Err()
	_ = w.moveToDLQ(ctx, streamID, raw, lastErr, w.cfg.MaxAttempts)
	return lastErr
}

func (w *OpsNotifyWorker) attemptKey(job OpsNotifyJob, streamID string) string {
	id := strings.TrimSpace(job.ID)
	if id == "" {
		id = strings.TrimSpace(streamID)
	}
	return "ops:notify:attempt:" + id
}

func (w *OpsNotifyWorker) backoffForAttempt(attemptNum int) time.Duration {
	if attemptNum <= 1 {
		return 0
	}
	idx := attemptNum - 2
	if idx < 0 {
		idx = 0
	}
	if idx >= len(w.cfg.Backoffs) {
		return w.cfg.Backoffs[len(w.cfg.Backoffs)-1]
	}
	return w.cfg.Backoffs[idx]
}

func opsSleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseOpsNotifyJob(msg redis.XMessage) (OpsNotifyJob, string, error) {
	rawAny, ok := msg.Values["job"]
	if !ok {
		return OpsNotifyJob{}, "", errors.New("missing field: job")
	}

	var raw string
	switch v := rawAny.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		raw = fmt.Sprintf("%v", v)
	}
	if strings.TrimSpace(raw) == "" {
		return OpsNotifyJob{}, raw, errors.New("empty job payload")
	}

	var job OpsNotifyJob
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return OpsNotifyJob{}, raw, err
	}
	return job, raw, nil
}

func (w *OpsNotifyWorker) allowAlertByDedup(ctx context.Context, job OpsNotifyJob) bool {
	key := strings.TrimSpace(job.DedupKey)
	if key == "" {
		return true
	}

	dedupKey := "ops:notify:dedup:" + key
	ok, err := w.rdb.SetNX(ctx, dedupKey, "1", w.cfg.AlertDedupTTL).Result()
	if err != nil {
		log.Printf("[OpsNotifyWorker] dedup SetNX failed key=%s err=%v", dedupKey, err)
		return true
	}
	return ok
}

func (w *OpsNotifyWorker) allowAlertByRateLimit(ctx context.Context) bool {
	if w.cfg.AlertRateLimitPer <= 0 {
		return true
	}
	window := time.Now().UTC().Format("200601021504")
	key := "ops:notify:rl:alert:" + window

	val, err := w.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Printf("[OpsNotifyWorker] rate limit incr failed key=%s err=%v", key, err)
		return true
	}
	_ = w.rdb.Expire(ctx, key, 2*time.Minute).Err()
	return val <= w.cfg.AlertRateLimitPer
}

func (w *OpsNotifyWorker) moveToDLQ(ctx context.Context, streamID, raw string, err error, attempts int) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return w.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: w.cfg.DLQKey,
		Values: map[string]any{
			"source_stream": w.cfg.StreamKey,
			"source_id":     streamID,
			"job":           raw,
			"error":         msg,
			"attempts":      attempts,
			"at":            time.Now().UTC().Format(time.RFC3339),
		},
	}).Err()
}

func (w *OpsNotifyWorker) ackAndDelete(ctx context.Context, streamID string) error {
	if streamID == "" {
		return nil
	}
	if err := w.rdb.XAck(ctx, w.cfg.StreamKey, w.cfg.Group, streamID).Err(); err != nil {
		return err
	}
	_ = w.rdb.XDel(ctx, w.cfg.StreamKey, streamID).Err()
	return nil
}
