package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/nikoksr/notify"
)

// DingTalkNotifier 钉钉机器人 Webhook 通知服务（支持 Markdown + 可选签名）。
type DingTalkNotifier struct {
	webhookURL string
	enableSign bool
	secret     string
	client     *http.Client

	// timestampFunc 用于测试注入；单位：毫秒。
	timestampFunc func() int64

	// SendFunc 便于测试注入自定义行为。
	SendFunc func(ctx context.Context, msg *notify.Message) error
}

type dingTalkMarkdown struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type dingTalkPayload struct {
	MsgType  string           `json:"msgtype"`
	Markdown dingTalkMarkdown `json:"markdown"`
}

type dingTalkResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// NewWebhookService 创建一个钉钉 Webhook 通知服务（不启用签名）。
func NewWebhookService(webhookURL string) *DingTalkNotifier {
	return &DingTalkNotifier{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		timestampFunc: func() int64 { return time.Now().UnixMilli() },
	}
}

// NewWebhookServiceWithSign 创建启用签名的钉钉 Webhook 通知服务。
func NewWebhookServiceWithSign(webhookURL, secret string) *DingTalkNotifier {
	svc := NewWebhookService(webhookURL)
	svc.enableSign = true
	svc.secret = secret
	return svc
}

// Send 发送 Markdown 消息到钉钉 Webhook。
func (s *DingTalkNotifier) Send(ctx context.Context, msg *notify.Message) error {
	if s == nil {
		return errors.New("dingtalk: notifier is nil")
	}
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}
	if msg == nil {
		return errors.New("dingtalk: message is nil")
	}

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	timestampFunc := s.timestampFunc
	if timestampFunc == nil {
		timestampFunc = func() int64 { return time.Now().UnixMilli() }
	}

	targetURL := s.webhookURL
	if s.enableSign {
		if s.secret == "" {
			return errors.New("dingtalk: secret is required when enableSign is true")
		}
		timestamp := timestampFunc()
		sign := generateDingTalkSign(s.secret, timestamp)

		signedURL, err := appendSignParams(targetURL, timestamp, sign)
		if err != nil {
			return fmt.Errorf("dingtalk: build signed url failed: %w", err)
		}
		targetURL = signedURL
	}

	payload := dingTalkPayload{
		MsgType: "markdown",
		Markdown: dingTalkMarkdown{
			Title: msg.Subject,
			Text:  msg.Body,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("dingtalk: marshal payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("dingtalk: create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sub2API-Notifier/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dingtalk: send request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dingtalk: unexpected status code: %d", resp.StatusCode)
	}

	var apiResp dingTalkResponse
	if err := json.Unmarshal(body, &apiResp); err == nil && apiResp.ErrCode != 0 {
		return fmt.Errorf("dingtalk: api error errcode=%d errmsg=%s", apiResp.ErrCode, apiResp.ErrMsg)
	}

	return nil
}

func appendSignParams(rawURL string, timestamp int64, sign string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	q := parsed.Query()
	q.Set("timestamp", strconv.FormatInt(timestamp, 10))
	q.Set("sign", sign)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// generateDingTalkSign 使用 HMAC-SHA256 + Base64 的签名算法，与 JS 版本保持一致：
// stringToSign = `${timestamp}\n${secret}`。
func generateDingTalkSign(secret string, timestamp int64) string {
	stringToSign := fmt.Sprintf("%d\n%s", timestamp, secret)
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
