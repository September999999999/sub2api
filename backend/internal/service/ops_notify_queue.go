package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	OpsNotifyStreamKeyDefault     = "ops:notify:jobs"
	OpsNotifyDLQStreamKeyDefault  = "ops:notify:dlq"
	OpsNotifyConsumerGroupDefault = "ops-notify-workers"
)

type OpsNotifyJobKind string

const (
	OpsNotifyJobKindAlert  OpsNotifyJobKind = "alert"
	OpsNotifyJobKindReport OpsNotifyJobKind = "report"
)

// OpsNotifyJob is an internal message for Ops-triggered notifications.
//
// NOTE: Recipients are intentionally NOT included; they are sourced from
// enabled notification platforms (e.g. SMTP platform's smtpTo).
//
// JSON naming uses snake_case because this payload is stored in Redis and may
// be inspected/debugged by ops.
type OpsNotifyJob struct {
	ID         string           `json:"id"`
	Kind       OpsNotifyJobKind `json:"kind"`
	Type       string           `json:"type"`
	PlatformID string           `json:"platform_id,omitempty"`
	SubType    string           `json:"sub_type,omitempty"`
	Severity   string           `json:"severity"`
	Title      string           `json:"title"`
	Content    string           `json:"content"`
	DedupKey   string           `json:"dedup_key,omitempty"`
	Metadata   map[string]any   `json:"metadata,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
}

func NewOpsNotifyJob(kind OpsNotifyJobKind, typ string) OpsNotifyJob {
	return OpsNotifyJob{
		ID:        uuid.NewString(),
		Kind:      kind,
		Type:      typ,
		Severity:  "info",
		Title:     "",
		Content:   "",
		Metadata:  nil,
		CreatedAt: time.Now().UTC(),
	}
}

type OpsNotifyPublisher interface {
	Publish(ctx context.Context, job OpsNotifyJob) error
}

type RedisOpsNotifyPublisher struct {
	rdb    *redis.Client
	stream string
}

func NewRedisOpsNotifyPublisher(rdb *redis.Client) *RedisOpsNotifyPublisher {
	return &RedisOpsNotifyPublisher{rdb: rdb, stream: OpsNotifyStreamKeyDefault}
}

func (p *RedisOpsNotifyPublisher) WithStream(stream string) *RedisOpsNotifyPublisher {
	if p == nil {
		return nil
	}
	out := *p
	if stream != "" {
		out.stream = stream
	}
	return &out
}

func (p *RedisOpsNotifyPublisher) Publish(ctx context.Context, job OpsNotifyJob) error {
	if p == nil || p.rdb == nil {
		return errors.New("ops notify publisher is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}

	raw, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal ops notify job: %w", err)
	}

	return p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]any{
			"job": string(raw),
		},
	}).Err()
}
