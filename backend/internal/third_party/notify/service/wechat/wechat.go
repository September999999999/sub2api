package wechat

import (
	"context"

	"github.com/nikoksr/notify"
)

// WebhookService 模拟企业微信 webhook 渠道。
type WebhookService struct {
	WebhookURL string
	SendFunc   func(ctx context.Context, msg *notify.Message) error
}

// NewWebhookService 创建企业微信 webhook 服务。
func NewWebhookService(url string) *WebhookService {
	return &WebhookService{WebhookURL: url}
}

// Send 发送消息。
func (s *WebhookService) Send(ctx context.Context, msg *notify.Message) error {
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}
	return nil
}
