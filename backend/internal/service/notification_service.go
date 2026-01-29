package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/nikoksr/notify"
	"github.com/nikoksr/notify/service/bark"
	"github.com/nikoksr/notify/service/custom"
	"github.com/nikoksr/notify/service/dingtalk"
	"github.com/nikoksr/notify/service/discord"
	"github.com/nikoksr/notify/service/smtp"
	"github.com/nikoksr/notify/service/telegram"
)

type platformFactoryFunc func(model.PlatformConfig) (notify.Notifier, error)

// ErrPlatformNotFound 在配置中找不到指定平台时返回，用于让 handler 层映射为 404。
var ErrPlatformNotFound = errors.New("platform not found")

// NotificationService 基于 notify 封装的通知服务。
type NotificationService struct {
	configSvc       WebhookConfigProvider
	platformFactory platformFactoryFunc
}

// NewNotificationService 创建通知服务。
func NewNotificationService(configSvc WebhookConfigProvider) *NotificationService {
	return &NotificationService{
		configSvc: configSvc,
	}
}

// UsePlatformFactory 设置自定义渠道工厂，用于测试或自定义扩展。
func (s *NotificationService) UsePlatformFactory(factory func(model.PlatformConfig) (notify.Notifier, error)) {
	s.platformFactory = factory
}

type sendOptions struct {
	ignoreConfigEnabled bool
}

// Send 根据配置和事件类型发送通知。
func (s *NotificationService) Send(ctx context.Context, msg model.NotificationMessage) error {
	config, err := s.configSvc.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("get webhook config: %w", err)
	}
	return s.sendWithConfig(ctx, msg, config, sendOptions{})
}

func (s *NotificationService) SendToPlatform(ctx context.Context, msg model.NotificationMessage, platformID string) error {
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return errors.New("platformID is required")
	}

	config, err := s.configSvc.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("get webhook config: %w", err)
	}
	if config == nil {
		return nil
	}

	var target *model.PlatformConfig
	for i := range config.Platforms {
		if config.Platforms[i].ID == platformID {
			target = &config.Platforms[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("%w: %s", ErrPlatformNotFound, platformID)
	}
	if !target.Enabled {
		return fmt.Errorf("platform disabled: %s", platformID)
	}
	if model.IsLegacyPlatformType(target.Type) {
		return fmt.Errorf("platform %s(%s) is no longer supported", platformID, target.Type)
	}

	scoped := *config
	scoped.Platforms = []model.PlatformConfig{*target}
	return s.sendWithConfig(ctx, msg, &scoped, sendOptions{})
}

func isOpsNotificationType(t model.NotificationType) bool {
	v := strings.TrimSpace(string(t))
	return v == "ops" || strings.HasPrefix(v, "ops.")
}

func (s *NotificationService) sendWithConfig(ctx context.Context, msg model.NotificationMessage, config *model.WebhookConfig, opts sendOptions) error {
	msg.Timestamp = s.ensureTimestamp(msg.Timestamp)

	if config == nil {
		return nil
	}
	if !opts.ignoreConfigEnabled && !config.Enabled {
		if isOpsNotificationType(msg.Type) {
			return nil
		}
	}

	notifier, buildErr := s.buildNotifier(config)
	if notifier == nil {
		if buildErr != nil {
			return fmt.Errorf("build notifier: %w", buildErr)
		}
		return nil
	}

	retrySettings := config.RetrySettings

	maxRetries := retrySettings.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	retryDelay := time.Duration(retrySettings.RetryDelay) * time.Millisecond
	if retryDelay < 0 {
		retryDelay = 0
	}

	singleTimeout := time.Duration(retrySettings.Timeout) * time.Millisecond
	if singleTimeout <= 0 {
		singleTimeout = 5 * time.Second
	}

	totalAttempts := maxRetries + 1
	var lastSendErr error

	for attempt := 0; attempt < totalAttempts; attempt++ {
		if ctx.Err() != nil {
			return errors.Join(buildErr, ctx.Err())
		}

		sendCtx, cancel := context.WithTimeout(ctx, singleTimeout)
		sendErr := notifier.Send(sendCtx, msg.Title, s.formatContent(msg), msg.Metadata)
		cancel()

		if sendErr == nil {
			if buildErr != nil {
				log.Printf("[NotificationService] Warning: notifier built with partial failures: %v", buildErr)
			}
			return nil
		}
		lastSendErr = sendErr

		if attempt == totalAttempts-1 {
			break
		}

		if retryDelay <= 0 {
			continue
		}

		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(buildErr, ctx.Err())
		case <-timer.C:
		}
	}

	return errors.Join(buildErr, lastSendErr)
}

// SendToType 直接指定事件类型发送。
func (s *NotificationService) SendToType(ctx context.Context, notificationType, title, content string, metadata map[string]interface{}) error {
	msg := model.NotificationMessage{
		Type:      model.NotificationType(notificationType),
		Title:     title,
		Content:   content,
		Severity:  "info",
		Metadata:  metadata,
		Timestamp: time.Now().UTC(),
	}
	return s.Send(ctx, msg)
}

// TestPlatform 单独测试某个平台的连通性。
func (s *NotificationService) TestPlatform(ctx context.Context, platformID string) error {
	config, err := s.configSvc.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("get webhook config: %w", err)
	}

	var target *model.PlatformConfig
	for i := range config.Platforms {
		if config.Platforms[i].ID == platformID {
			target = &config.Platforms[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("%w: %s", ErrPlatformNotFound, platformID)
	}

	notifier := notify.New()
	svc, err := s.createPlatformService(*target)
	if err != nil {
		return err
	}
	notifier.UseServices(svc)

	msg := model.NotificationMessage{
		Type:      model.NotificationTest,
		Title:     "🧪 测试通知",
		Content:   "Sub2API webhook测试",
		Severity:  "info",
		Metadata:  map[string]any{"platformId": platformID},
		Timestamp: time.Now().UTC(),
	}
	return notifier.Send(ctx, msg.Title, s.formatContent(msg), msg.Metadata)
}

// TestNotification 使用当前配置发送测试通知。
func (s *NotificationService) TestNotification(ctx context.Context, msg model.NotificationMessage) error {
	if msg.Type == "" {
		msg.Type = model.NotificationTest
	}
	if msg.Title == "" {
		msg.Title = "🧪 测试通知"
	}
	if msg.Content == "" {
		msg.Content = "Sub2API webhook测试"
	}
	if msg.Severity == "" {
		msg.Severity = "info"
	}

	config, err := s.configSvc.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("get webhook config: %w", err)
	}

	// 强制发送：忽略全局 Enabled 与通知类型订阅过滤，仅使用“平台 Enabled”过滤。
	return s.sendWithConfig(ctx, msg, config, sendOptions{
		ignoreConfigEnabled: true,
	})
}

// buildNotifier 按配置构建 notify 聚合器。
func (s *NotificationService) buildNotifier(config *model.WebhookConfig) (*notify.Notify, error) {
	n := notify.New()
	var errs []error
	enabledCount := 0

	for _, platform := range config.Platforms {
		if !platform.Enabled {
			continue
		}
		if model.IsLegacyPlatformType(platform.Type) {
			continue
		}

		svc, err := s.createPlatformService(platform)
		if err != nil {
			errs = append(errs, fmt.Errorf("platform %s: %w", platform.ID, err))
			continue
		}
		n.UseServices(svc)
		enabledCount++
	}

	if enabledCount == 0 {
		return nil, errors.Join(errs...)
	}

	return n, errors.Join(errs...)
}

// createPlatformService 创建具体渠道实例。
func (s *NotificationService) createPlatformService(platform model.PlatformConfig) (notify.Notifier, error) {
	if s.platformFactory != nil {
		return s.platformFactory(platform)
	}

	switch platform.Type {
	case model.PlatformLark:
		return nil, fmt.Errorf("platform %s is no longer supported", platform.Type)
	case model.PlatformSlack:
		return nil, fmt.Errorf("platform %s is no longer supported", platform.Type)
	case model.PlatformWechat:
		return nil, fmt.Errorf("platform %s is no longer supported", platform.Type)
	case model.PlatformDiscord:
		return discord.NewWebhookService(platform.URL), nil
	case model.PlatformBark:
		// Bark: Token 是 Device Key，URL 是可选的自建服务器地址
		if platform.URL != "" {
			return bark.NewWithServers(platform.Token, platform.URL), nil
		}
		return bark.New(platform.Token), nil
	case model.PlatformDingtalk:
		if platform.EnableSign {
			if platform.Secret == "" {
				return nil, errors.New("secret is required for dingtalk when enableSign is true")
			}
			return dingtalk.NewWebhookServiceWithSign(platform.URL, platform.Secret), nil
		}
		return dingtalk.NewWebhookService(platform.URL), nil
	case model.PlatformTelegram:
		return telegram.NewWithSettings(platform.BotToken, platform.ChatID, platform.APIBaseURL, platform.ProxyURL)
	case model.PlatformSMTP:
		return smtp.New(smtp.Config{
			Host:      platform.SMTPHost,
			Port:      platform.SMTPPort,
			User:      platform.SMTPUser,
			Pass:      platform.SMTPPass,
			From:      platform.SMTPFrom,
			To:        smtp.SplitRecipients(platform.SMTPTo),
			Secure:    platform.SMTPSecure,
			IgnoreTLS: platform.SMTPIgnoreTLS,
		}), nil
	case model.PlatformCustom:
		// Custom Webhook: URL 必填，Secret 可选（用于签名验证）
		if platform.Secret != "" {
			return custom.NewWithSecret(platform.URL, platform.Secret), nil
		}
		return custom.New(platform.URL), nil
	default:
		return nil, fmt.Errorf("unsupported platform type: %s", platform.Type)
	}
}

func (s *NotificationService) ensureTimestamp(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now().UTC()
	}
	return ts
}

func (s *NotificationService) formatContent(msg model.NotificationMessage) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("[%s] %s\n", msg.Type, msg.Content))
	builder.WriteString(fmt.Sprintf("Severity: %s\n", msg.Severity))
	builder.WriteString(fmt.Sprintf("Time: %s", msg.Timestamp.Format(time.RFC3339)))

	if len(msg.Metadata) > 0 {
		keys := make([]string, 0, len(msg.Metadata))
		for k := range msg.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		builder.WriteString("\nMetadata:")
		for _, k := range keys {
			builder.WriteString(fmt.Sprintf(" %s=%v;", k, msg.Metadata[k]))
		}
	}
	return strings.TrimSpace(builder.String())
}
