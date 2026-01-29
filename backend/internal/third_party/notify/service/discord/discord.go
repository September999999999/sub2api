package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nikoksr/notify"
)

const (
	defaultUsername  = "Claude Relay Service"
	defaultTimeout   = 10 * time.Second
	defaultUserAgent = "Sub2API-Notifier/1.0"
)

// DiscordNotifier Discord Webhook 通知服务（Embed 格式）。
type DiscordNotifier struct {
	webhookURL string
	client     *http.Client

	// nowFunc 用于测试注入。
	nowFunc func() time.Time

	// SendFunc 便于测试注入自定义行为。
	SendFunc func(ctx context.Context, msg *notify.Message) error
}

type webhookPayload struct {
	Username string         `json:"username,omitempty"`
	Embeds   []discordEmbed `json:"embeds,omitempty"`
}

type discordEmbed struct {
	Title     string       `json:"title,omitempty"`
	Color     int          `json:"color,omitempty"`
	Fields    []embedField `json:"fields,omitempty"`
	Timestamp string       `json:"timestamp,omitempty"`
	Footer    *embedFooter `json:"footer,omitempty"`
}

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type embedFooter struct {
	Text string `json:"text"`
}

// NewWebhookService 创建 Discord Webhook 服务（默认 10s 超时）。
func NewWebhookService(webhookURL string) *DiscordNotifier {
	return &DiscordNotifier{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: defaultTimeout,
		},
		nowFunc: time.Now,
	}
}

// Send 发送 Embed 消息到 Discord Webhook。
func (s *DiscordNotifier) Send(ctx context.Context, msg *notify.Message) error {
	if s == nil {
		return errors.New("discord: notifier is nil")
	}
	if s.SendFunc != nil {
		return s.SendFunc(ctx, msg)
	}
	if msg == nil {
		return errors.New("discord: message is nil")
	}

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	nowFunc := s.nowFunc
	if nowFunc == nil {
		nowFunc = time.Now
	}

	notificationType := extractNotificationType(msg.Body)
	embed := discordEmbed{
		Title:     msg.Subject,
		Color:     getDiscordColor(notificationType),
		Fields:    formatEmbedFields(msg.Body),
		Timestamp: nowFunc().UTC().Format(time.RFC3339),
		Footer:    &embedFooter{Text: defaultUsername},
	}

	payload := webhookPayload{
		Username: defaultUsername,
		Embeds:   []discordEmbed{embed},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("discord: marshal payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("discord: create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord: unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func extractNotificationType(body string) string {
	firstLine := strings.TrimSpace(firstLineOf(body))
	if firstLine == "" || !strings.HasPrefix(firstLine, "[") {
		return ""
	}
	end := strings.Index(firstLine, "]")
	if end <= 1 {
		return ""
	}
	return firstLine[1:end]
}

func firstLineOf(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if idx := strings.IndexByte(body, '\n'); idx >= 0 {
		return body[:idx]
	}
	return body
}

func getDiscordColor(notificationType string) int {
	switch notificationType {
	case "accountAnomaly":
		return 0xff9800 // 橙色
	case "systemError":
		return 0xf44336 // 红色
	case "securityAlert":
		return 0xf44336 // 红色
	case "rateLimitRecovery":
		return 0x4caf50 // 绿色
	case "test":
		return 0x2196f3 // 蓝色
	}
	return 0x9e9e9e
}

func formatEmbedFields(body string) []embedField {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" {
		return nil
	}

	lines := strings.Split(body, "\n")
	fields := make([]embedField, 0, len(lines))

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if i == 0 {
			fields = append(fields, embedField{
				Name:  "Content",
				Value: contentFromFirstLine(line),
			})
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if ok {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				fields = append(fields, embedField{Name: key, Value: value})
				continue
			}
		}

		fields = append(fields, embedField{Name: "Message", Value: line})
	}

	if len(fields) == 0 {
		return nil
	}
	return fields
}

func contentFromFirstLine(line string) string {
	if strings.HasPrefix(line, "[") {
		if end := strings.Index(line, "]"); end >= 0 {
			content := strings.TrimSpace(line[end+1:])
			if content != "" {
				return content
			}
		}
	}
	return line
}
