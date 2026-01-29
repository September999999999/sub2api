package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/model"
)

// WebhookConfigRepository 提供 webhook 配置的持久化接口
type WebhookConfigRepository interface {
	Get(ctx context.Context) (*model.WebhookConfig, error)
	Save(ctx context.Context, config *model.WebhookConfig) error
	Delete(ctx context.Context) error
}

// WebhookConfigProvider 提供 webhook 配置的读取能力
type WebhookConfigProvider interface {
	GetConfig(ctx context.Context) (*model.WebhookConfig, error)
}
