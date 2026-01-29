package custom

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nikoksr/notify"
)

// WebhookPayload 自定义 Webhook 的 JSON 负载
type WebhookPayload struct {
	Title     string         `json:"title"`
	Content   string         `json:"content"`
	Timestamp int64          `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Service 自定义 Webhook 推送服务
type Service struct {
	webhookURL string
	secret     string // 可选，用于签名验证
	client     *http.Client
	// SendFunc 用于测试注入
	SendFunc func(ctx context.Context, msg *notify.Message) error
}

// New 创建自定义 Webhook 服务
func New(webhookURL string) *Service {
	return &Service{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NewWithSecret 创建带签名的自定义 Webhook 服务
func NewWithSecret(webhookURL, secret string) *Service {
	return &Service{
		webhookURL: webhookURL,
		secret:     secret,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Send 发送通知到自定义 Webhook
func (s *Service) Send(ctx context.Context, msg *notify.Message) error {
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}

	payload := WebhookPayload{
		Title:     msg.Subject,
		Content:   msg.Body,
		Timestamp: time.Now().Unix(),
		Metadata:  msg.Metadata,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("custom webhook: marshal payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("custom webhook: create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sub2API-Notifier/1.0")

	// 如果配置了 secret，添加签名
	if s.secret != "" {
		signature := s.sign(jsonData)
		req.Header.Set("X-Signature", signature)
		req.Header.Set("X-Timestamp", fmt.Sprintf("%d", payload.Timestamp))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("custom webhook: send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("custom webhook: unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// sign 使用 HMAC-SHA256 签名
func (s *Service) sign(data []byte) string {
	h := hmac.New(sha256.New, []byte(s.secret))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
