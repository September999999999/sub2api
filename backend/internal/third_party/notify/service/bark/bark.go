package bark

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nikoksr/notify"
)

const defaultServer = "https://api.day.app"

// Service Bark 推送服务
type Service struct {
	deviceKey string
	serverURL string
	client    *http.Client
	// SendFunc 用于测试注入
	SendFunc func(ctx context.Context, msg *notify.Message) error
}

// New 创建使用默认服务器的 Bark 服务
func New(deviceKey string) *Service {
	return &Service{
		deviceKey: deviceKey,
		serverURL: defaultServer,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NewWithServers 创建使用自定义服务器的 Bark 服务
func NewWithServers(deviceKey string, serverURL string) *Service {
	// 移除末尾斜杠
	serverURL = strings.TrimSuffix(serverURL, "/")
	return &Service{
		deviceKey: deviceKey,
		serverURL: serverURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Send 发送通知到 Bark
func (s *Service) Send(ctx context.Context, msg *notify.Message) error {
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}

	// 构建 Bark API URL
	// 格式: {server}/{device_key}/{title}/{body}
	apiURL := fmt.Sprintf("%s/%s/%s/%s",
		s.serverURL,
		s.deviceKey,
		url.PathEscape(msg.Subject),
		url.PathEscape(msg.Body),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("bark: create request failed: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("bark: send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bark: unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
