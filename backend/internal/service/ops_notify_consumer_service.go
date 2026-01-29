package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	opsNotifyConsumerJobName = "ops_notify_consumer"

	opsNotifyConsumerLeaderLockKey = "ops:notify:consumer:leader"
	opsNotifyConsumerLeaderLockTTL = 90 * time.Second

	opsNotifyConsumerHeartbeatInterval = 60 * time.Second
)

var opsNotifyConsumerReleaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

var opsNotifyConsumerRefreshScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

type OpsNotifyConsumerService struct {
	opsService *OpsService
	opsRepo    OpsRepository

	rdb            *redis.Client
	notificationSv *NotificationService
	cfg            *config.Config

	instanceID string

	startOnce sync.Once
	stopOnce  sync.Once
	stopCtx   context.Context
	stop      context.CancelFunc
	wg        sync.WaitGroup

	skipLogMu sync.Mutex
	skipLogAt time.Time
}

func NewOpsNotifyConsumerService(
	opsService *OpsService,
	opsRepo OpsRepository,
	redisClient *redis.Client,
	notificationSvc *NotificationService,
	cfg *config.Config,
) *OpsNotifyConsumerService {
	return &OpsNotifyConsumerService{
		opsService:     opsService,
		opsRepo:        opsRepo,
		rdb:            redisClient,
		notificationSv: notificationSvc,
		cfg:            cfg,
		instanceID:     uuid.NewString(),
		startOnce:      sync.Once{},
		stopOnce:       sync.Once{},
		stopCtx:        nil,
		stop:           nil,
		wg:             sync.WaitGroup{},
		skipLogMu:      sync.Mutex{},
		skipLogAt:      time.Time{},
	}
}

func (s *OpsNotifyConsumerService) Start() {
	s.StartWithContext(context.Background())
}

func (s *OpsNotifyConsumerService) StartWithContext(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return
	}
	if s.rdb == nil {
		return
	}
	if s.notificationSv == nil {
		return
	}

	s.startOnce.Do(func() {
		s.stopCtx, s.stop = context.WithCancel(ctx)
		s.wg.Add(1)
		go s.run()
	})
}

func (s *OpsNotifyConsumerService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.stop != nil {
			s.stop()
		}
	})
	s.wg.Wait()
}

func (s *OpsNotifyConsumerService) run() {
	defer s.wg.Done()

	for {
		if s.stopCtx.Err() != nil {
			return
		}
		if s.opsService != nil && !s.opsService.IsMonitoringEnabled(s.stopCtx) {
			s.sleepOrStop(2 * time.Second)
			continue
		}

		release, ok := s.tryAcquireLeaderLock(s.stopCtx)
		if !ok {
			s.sleepOrStop(2 * time.Second)
			continue
		}

		s.runAsLeader(release)
	}
}

func (s *OpsNotifyConsumerService) runAsLeader(release func()) {
	if s == nil {
		return
	}
	if release != nil {
		defer release()
	}

	runCtx, cancel := context.WithCancel(s.stopCtx)
	defer cancel()

	worker := NewOpsNotifyWorker(s.rdb, s.notificationSv, OpsNotifyWorkerConfig{
		Consumer: "ops-notify-consumer:" + s.instanceID,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(runCtx)
	}()

	leaseTicker := time.NewTicker(opsNotifyConsumerLeaderLockTTL / 3)
	hbTicker := time.NewTicker(opsNotifyConsumerHeartbeatInterval)
	defer leaseTicker.Stop()
	defer hbTicker.Stop()

	s.recordHeartbeatSuccess(time.Now().UTC(), 0)

	for {
		select {
		case <-runCtx.Done():
			return
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				s.recordHeartbeatError(time.Now().UTC(), 0, err)
				log.Printf("[OpsNotifyConsumer] worker stopped: %v", err)
			}
			return
		case <-hbTicker.C:
			s.recordHeartbeatSuccess(time.Now().UTC(), 0)
		case <-leaseTicker.C:
			if s.stopCtx.Err() != nil {
				return
			}
			if s.opsService != nil && !s.opsService.IsMonitoringEnabled(s.stopCtx) {
				return
			}
			if ok := s.refreshLeaderLock(s.stopCtx); !ok {
				s.maybeLogSkip()
				return
			}
		}
	}
}

func (s *OpsNotifyConsumerService) tryAcquireLeaderLock(ctx context.Context) (func(), bool) {
	if s == nil || s.rdb == nil {
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ok, err := s.rdb.SetNX(ctx, opsNotifyConsumerLeaderLockKey, s.instanceID, opsNotifyConsumerLeaderLockTTL).Result()
	if err != nil {
		log.Printf("[OpsNotifyConsumer] leader lock SetNX failed; skipping: %v", err)
		return nil, false
	}
	if !ok {
		s.maybeLogSkip()
		return nil, false
	}

	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = opsNotifyConsumerReleaseScript.Run(ctx, s.rdb, []string{opsNotifyConsumerLeaderLockKey}, s.instanceID).Result()
	}
	return release, true
}

func (s *OpsNotifyConsumerService) refreshLeaderLock(ctx context.Context) bool {
	if s == nil || s.rdb == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ttlMs := int64(opsNotifyConsumerLeaderLockTTL / time.Millisecond)
	res, err := opsNotifyConsumerRefreshScript.Run(ctx, s.rdb, []string{opsNotifyConsumerLeaderLockKey}, s.instanceID, ttlMs).Int64()
	if err != nil {
		log.Printf("[OpsNotifyConsumer] leader lock refresh failed: %v", err)
		return false
	}
	return res == 1
}

func (s *OpsNotifyConsumerService) recordHeartbeatSuccess(runAt time.Time, duration time.Duration) {
	if s == nil || s.opsRepo == nil {
		return
	}
	now := time.Now().UTC()
	durMs := duration.Milliseconds()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsNotifyConsumerJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &now,
		LastDurationMs: &durMs,
	})
}

func (s *OpsNotifyConsumerService) recordHeartbeatError(runAt time.Time, duration time.Duration, err error) {
	if s == nil || s.opsRepo == nil || err == nil {
		return
	}
	now := time.Now().UTC()
	durMs := duration.Milliseconds()
	msg := truncateString(err.Error(), 2048)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsNotifyConsumerJobName,
		LastRunAt:      &runAt,
		LastErrorAt:    &now,
		LastError:      &msg,
		LastDurationMs: &durMs,
	})
}

func (s *OpsNotifyConsumerService) maybeLogSkip() {
	s.skipLogMu.Lock()
	defer s.skipLogMu.Unlock()

	now := time.Now()
	if !s.skipLogAt.IsZero() && now.Sub(s.skipLogAt) < time.Minute {
		return
	}
	s.skipLogAt = now
	log.Printf("[OpsNotifyConsumer] leader lock held by another instance; skipping")
}

func (s *OpsNotifyConsumerService) sleepOrStop(d time.Duration) {
	if d <= 0 {
		d = time.Second
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.stopCtx.Done():
		return
	case <-t.C:
		return
	}
}
