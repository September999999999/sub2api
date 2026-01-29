package service

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/nikoksr/notify"
)

type mockWebhookRepo struct {
	cfg         *model.WebhookConfig
	getErr      error
	saveErr     error
	deleteErr   error
	saveCalls   int
	deleteCalls int
}

func (m *mockWebhookRepo) Get(ctx context.Context) (*model.WebhookConfig, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return cloneConfig(m.cfg), nil
}

func (m *mockWebhookRepo) Save(ctx context.Context, config *model.WebhookConfig) error {
	m.saveCalls++
	if m.saveErr != nil {
		return m.saveErr
	}
	m.cfg = cloneConfig(config)
	return nil
}

func (m *mockWebhookRepo) Delete(ctx context.Context) error {
	m.deleteCalls++
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.cfg = nil
	return nil
}

type mockNotifier struct {
	id       string
	err      error
	calls    int
	messages []*notify.Message
}

func (m *mockNotifier) Send(ctx context.Context, msg *notify.Message) error {
	m.calls++
	m.messages = append(m.messages, msg)
	return m.err
}

func cloneConfig(cfg *model.WebhookConfig) *model.WebhookConfig {
	if cfg == nil {
		return nil
	}
	data, _ := json.Marshal(cfg)
	var out model.WebhookConfig
	_ = json.Unmarshal(data, &out)
	return &out
}

func newFullNotificationTypes() map[model.NotificationType]bool {
	return map[model.NotificationType]bool{
		model.NotificationAccountAnomaly:    true,
		model.NotificationSystemError:       true,
		model.NotificationSecurityAlert:     true,
		model.NotificationRateLimitRecovery: true,
		model.NotificationTest:              true,
	}
}
