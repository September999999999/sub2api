package slack

import (
	"context"

	"github.com/nikoksr/notify"
)

// Service 模拟 Slack 通知服务。
type Service struct {
	Token     string
	receivers []string
	SendFunc  func(ctx context.Context, msg *notify.Message) error
}

// New 创建 Slack 服务实例。
func New(token string) *Service {
	return &Service{
		Token:     token,
		receivers: make([]string, 0),
	}
}

// AddReceivers 添加接收频道。
func (s *Service) AddReceivers(channelIDs ...string) {
	s.receivers = append(s.receivers, channelIDs...)
}

// Send 发送消息。
func (s *Service) Send(ctx context.Context, msg *notify.Message) error {
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}
	return nil
}
