package service

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/nikoksr/notify"
	"github.com/nikoksr/notify/service/discord"
	"github.com/stretchr/testify/require"
)

type sequenceNotifier struct {
	errs      []error
	calls     int
	callTimes []time.Time
}

func (n *sequenceNotifier) Send(ctx context.Context, msg *notify.Message) error {
	n.calls++
	n.callTimes = append(n.callTimes, time.Now())
	if n.calls-1 < len(n.errs) {
		return n.errs[n.calls-1]
	}
	return nil
}

type alwaysErrNotifier struct {
	err   error
	calls int
}

func (n *alwaysErrNotifier) Send(ctx context.Context, msg *notify.Message) error {
	n.calls++
	return n.err
}

type waitForContextNotifier struct {
	calls     int
	durations []time.Duration
}

func (n *waitForContextNotifier) Send(ctx context.Context, msg *notify.Message) error {
	start := time.Now()
	<-ctx.Done()
	n.calls++
	n.durations = append(n.durations, time.Since(start))
	return ctx.Err()
}

type cancelingNotifier struct {
	cancel context.CancelFunc
	err    error
	calls  int
}

func (n *cancelingNotifier) Send(ctx context.Context, msg *notify.Message) error {
	n.calls++
	if n.cancel != nil {
		n.cancel()
	}
	return n.err
}

func TestNotificationService_CreatePlatformService(t *testing.T) {
	repo := &mockWebhookRepo{cfg: model.NewWebhookConfig()}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	_, err := svc.createPlatformService(model.PlatformConfig{Type: model.PlatformLark, URL: "https://example.com"})
	require.Error(t, err)
	require.ErrorContains(t, err, "platform lark is no longer supported")

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformSlack, Token: "t", ChannelID: "c"})
	require.Error(t, err)
	require.ErrorContains(t, err, "platform slack is no longer supported")

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformWechat, URL: "https://example.com"})
	require.Error(t, err)
	require.ErrorContains(t, err, "platform wechat is no longer supported")

	discordSvc, err := svc.createPlatformService(model.PlatformConfig{Type: model.PlatformDiscord, URL: "https://example.com"})
	require.NoError(t, err)
	require.IsType(t, &discord.DiscordNotifier{}, discordSvc)

	_, err = svc.createPlatformService(model.PlatformConfig{Type: "unknown"})
	require.Error(t, err)
}

func TestLegacyPlatform_Enabled_Skipped(t *testing.T) {
	tests := []struct {
		name     string
		platform model.PlatformConfig
	}{
		{
			name:     "lark",
			platform: model.PlatformConfig{ID: "p1", Type: model.PlatformLark, Name: "lark", Enabled: true, URL: "https://example.com"},
		},
		{
			name:     "wechat",
			platform: model.PlatformConfig{ID: "p1", Type: model.PlatformWechat, Name: "wechat", Enabled: true, URL: "https://example.com"},
		},
		{
			name:     "slack",
			platform: model.PlatformConfig{ID: "p1", Type: model.PlatformSlack, Name: "slack", Enabled: true, Token: "t", ChannelID: "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := model.NewWebhookConfig()
			config.Enabled = true
			config.Platforms = []model.PlatformConfig{tt.platform}

			repo := &mockWebhookRepo{cfg: config}
			svc := NewNotificationService(NewWebhookConfigService(repo))
			svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
				t.Fatalf("legacy platform %s should be skipped and must not call factory", p.Type)
				return nil, nil
			}

			err := svc.Send(context.Background(), model.NotificationMessage{
				Type:      model.NotificationSystemError,
				Title:     "title",
				Content:   "content",
				Timestamp: time.Now(),
				Severity:  "critical",
			})
			require.NoError(t, err)
		})
	}
}

func TestNotificationService_Send_ErrorIsolation(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	// 该用例关注“失败不影响其它渠道被调用”，避免默认重试导致重复发送与测试变慢。
	config.RetrySettings = model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "fail", Type: model.PlatformDiscord, Name: "fail", Enabled: true, URL: "https://example.com"},
		{ID: "ok", Type: model.PlatformCustom, Name: "ok", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	notifiers := map[string]*mockNotifier{}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		n := &mockNotifier{id: p.ID}
		if p.ID == "fail" {
			n.err = errors.New("channel boom")
		}
		notifiers[p.ID] = n
		return n, nil
	}

	err := svc.Send(context.Background(), model.NotificationMessage{
		Type:      model.NotificationAccountAnomaly,
		Title:     "title",
		Content:   "content",
		Timestamp: time.Now(),
		Severity:  "critical",
	})
	require.Error(t, err)
	require.Equal(t, 1, notifiers["fail"].calls)
	require.Equal(t, 1, notifiers["ok"].calls)
}

func TestNotificationService_MetadataPassthrough(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	notifiers := map[string]*mockNotifier{}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		n := &mockNotifier{id: p.ID}
		notifiers[p.ID] = n
		return n, nil
	}

	expected := map[string]any{
		"traceId": "t-123",
		"count":   7,
		"nested": map[string]any{
			"k": "v",
		},
	}

	err := svc.Send(context.Background(), model.NotificationMessage{
		Type:     model.NotificationSystemError,
		Title:    "title",
		Content:  "content",
		Severity: "info",
		Metadata: expected,
	})
	require.NoError(t, err)

	require.NotNil(t, notifiers["p1"])
	require.Equal(t, 1, notifiers["p1"].calls)
	require.Equal(t, expected, notifiers["p1"].messages[0].Metadata)
}

func TestNotificationService_TestPlatform_TargetOnly(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformSlack, Name: "a", Enabled: false, Token: "t", ChannelID: "c"},
		{ID: "p2", Type: model.PlatformLark, Name: "b", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	notifiers := map[string]*mockNotifier{}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		n := &mockNotifier{id: p.ID}
		notifiers[p.ID] = n
		return n, nil
	}

	require.NoError(t, svc.TestPlatform(context.Background(), "p1"))
	require.NotNil(t, notifiers["p1"])
	require.Equal(t, 1, notifiers["p1"].calls)
	require.Nil(t, notifiers["p2"])

	require.Error(t, svc.TestPlatform(context.Background(), "missing"))
}

func TestNotificationService_TestPlatform_GetConfigError(t *testing.T) {
	svc := NewNotificationService(staticWebhookConfigProvider{cfg: nil, err: errors.New("repo down")})
	require.Error(t, svc.TestPlatform(context.Background(), "p1"))
}

func TestNotificationService_TestPlatform_CreateServiceError(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformType("unsupported"), Name: "bad", Enabled: true},
	}
	svc := NewNotificationService(staticWebhookConfigProvider{cfg: config, err: nil})

	err := svc.TestPlatform(context.Background(), "p1")
	require.Error(t, err)
	require.ErrorContains(t, err, "unsupported platform type")
}

func TestNotificationService_buildNotifier_PartialFailure(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.Platforms = []model.PlatformConfig{
		{ID: "bad", Type: model.PlatformType("unsupported"), Name: "bad", Enabled: true, URL: "https://example.com"},
		{ID: "good", Type: model.PlatformDiscord, Name: "good", Enabled: true, URL: "https://example.com"},
	}

	svc := NewNotificationService(NewWebhookConfigService(&mockWebhookRepo{cfg: config}))
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		if p.ID == "bad" {
			return nil, errors.New("boom")
		}
		return &mockNotifier{id: p.ID}, nil
	}

	notifier, err := svc.buildNotifier(config)
	require.Error(t, err)
	require.NotNil(t, notifier)
	require.NoError(t, notifier.Send(context.Background(), "t", "b", nil))
}

func TestNotificationService_Send_Retry_SucceedsAfterRetries(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 2, RetryDelay: 40, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	seq := &sequenceNotifier{
		errs: []error{errors.New("attempt-1"), errors.New("attempt-2")},
	}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return seq, nil
	}

	start := time.Now()
	err := svc.Send(context.Background(), model.NotificationMessage{
		Type:     model.NotificationSystemError,
		Title:    "t",
		Content:  "c",
		Severity: "error",
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, 3, seq.calls)
	require.GreaterOrEqual(t, elapsed, 2*time.Duration(config.RetrySettings.RetryDelay)*time.Millisecond)
}

func TestNotificationService_Send_Retry_MaxRetriesZero_NoRetry(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 0, RetryDelay: 1000, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	alwaysErr := &alwaysErrNotifier{err: errors.New("boom")}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return alwaysErr, nil
	}

	err := svc.Send(context.Background(), model.NotificationMessage{
		Type:     model.NotificationSystemError,
		Title:    "t",
		Content:  "c",
		Severity: "error",
	})
	require.Error(t, err)
	require.Equal(t, 1, alwaysErr.calls)
}

func TestNotificationService_Send_Retry_AllFail_ReturnsLastError(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 1, RetryDelay: 0, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	seq := &sequenceNotifier{
		errs: []error{errors.New("err-1"), errors.New("err-2")},
	}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return seq, nil
	}

	err := svc.Send(context.Background(), model.NotificationMessage{
		Type:     model.NotificationSystemError,
		Title:    "t",
		Content:  "c",
		Severity: "error",
	})
	require.Error(t, err)
	require.Equal(t, 2, seq.calls)
	require.Contains(t, err.Error(), "err-2")
}

func TestNotificationService_Send_Retry_TimeoutPerAttempt(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 1, RetryDelay: 0, Timeout: 25}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	waiter := &waitForContextNotifier{}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return waiter, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := svc.Send(ctx, model.NotificationMessage{
		Type:     model.NotificationSystemError,
		Title:    "t",
		Content:  "c",
		Severity: "error",
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 2, waiter.calls)
	require.Less(t, elapsed, 200*time.Millisecond)
	for i, d := range waiter.durations {
		require.Greater(t, d, 0*time.Millisecond, "call %d should finish with timeout", i+1)
		require.Less(t, d, 200*time.Millisecond, "call %d should not wait for parent timeout", i+1)
	}
}

type staticWebhookConfigProvider struct {
	cfg *model.WebhookConfig
	err error
}

func (p staticWebhookConfigProvider) GetConfig(ctx context.Context) (*model.WebhookConfig, error) {
	return p.cfg, p.err
}

func TestNotificationService_CreatePlatformService_AllSupported(t *testing.T) {
	repo := &mockWebhookRepo{cfg: model.NewWebhookConfig()}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	_, err := svc.createPlatformService(model.PlatformConfig{Type: model.PlatformBark, Token: "t"})
	require.NoError(t, err)

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformBark, Token: "t", URL: "https://bark.example.com"})
	require.NoError(t, err)

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformCustom, URL: "https://example.com"})
	require.NoError(t, err)

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformCustom, URL: "https://example.com", Secret: "s"})
	require.NoError(t, err)

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformDingtalk, URL: "https://example.com"})
	require.NoError(t, err)

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformDingtalk, URL: "https://example.com", EnableSign: true})
	require.Error(t, err)
	require.ErrorContains(t, err, "secret is required")

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformDingtalk, URL: "https://example.com", EnableSign: true, Secret: "secret"})
	require.NoError(t, err)

	_, err = svc.createPlatformService(model.PlatformConfig{Type: model.PlatformTelegram, BotToken: "bot", ChatID: "chat"})
	require.NoError(t, err)

	_, err = svc.createPlatformService(model.PlatformConfig{
		Type:     model.PlatformSMTP,
		SMTPHost: "smtp.example.com",
		SMTPPort: 587,
		SMTPUser: "u",
		SMTPPass: "p",
		SMTPFrom: "from@example.com",
		SMTPTo:   "to@example.com",
	})
	require.NoError(t, err)
}

func TestNotificationService_UsePlatformFactory(t *testing.T) {
	repo := &mockWebhookRepo{cfg: model.NewWebhookConfig()}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	expected := &mockNotifier{id: "x"}
	svc.UsePlatformFactory(func(p model.PlatformConfig) (notify.Notifier, error) {
		return expected, nil
	})

	got, err := svc.createPlatformService(model.PlatformConfig{Type: model.PlatformLark})
	require.NoError(t, err)
	require.Same(t, expected, got)
}

func TestSendToType_SendsWhenEnabled(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	svc := NewNotificationService(NewWebhookConfigService(&mockWebhookRepo{cfg: config}))

	notifier := &mockNotifier{id: "p1"}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return notifier, nil
	}

	require.NoError(t, svc.SendToType(context.Background(), string(model.NotificationSystemError), "t", "c", map[string]any{"k": "v"}))
	require.Equal(t, 1, notifier.calls)
}

func TestSend_ConfigNil_NoOp(t *testing.T) {
	svc := NewNotificationService(staticWebhookConfigProvider{cfg: nil, err: nil})
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		t.Fatalf("config nil should not build notifier")
		return nil, nil
	}
	require.NoError(t, svc.Send(context.Background(), model.NotificationMessage{Type: model.NotificationSystemError}))
}

func TestSend_NoEnabledPlatforms_NoOp(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.Platforms = nil

	svc := NewNotificationService(staticWebhookConfigProvider{cfg: config, err: nil})
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		t.Fatalf("no platforms should not call factory")
		return nil, nil
	}
	require.NoError(t, svc.Send(context.Background(), model.NotificationMessage{Type: model.NotificationSystemError}))
}

func TestSend_BuildNotifierFails_ReturnsError(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDingtalk, Name: "d", Enabled: true, URL: "https://example.com", EnableSign: true},
	}

	svc := NewNotificationService(staticWebhookConfigProvider{cfg: config, err: nil})
	err := svc.Send(context.Background(), model.NotificationMessage{Type: model.NotificationSystemError})
	require.Error(t, err)
	require.ErrorContains(t, err, "build notifier")
	require.ErrorContains(t, err, "secret is required")
}

func TestSend_GetConfigError_ReturnsWrappedError(t *testing.T) {
	svc := NewNotificationService(staticWebhookConfigProvider{cfg: nil, err: errors.New("db down")})
	err := svc.Send(context.Background(), model.NotificationMessage{Type: model.NotificationSystemError})
	require.Error(t, err)
	require.ErrorContains(t, err, "get webhook config")
	require.ErrorContains(t, err, "db down")
}

func TestSend_ContextAlreadyCanceled_ReturnsContextError(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 1, RetryDelay: 10, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	svc := NewNotificationService(staticWebhookConfigProvider{cfg: config, err: nil})
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return &mockNotifier{id: p.ID}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.Send(ctx, model.NotificationMessage{Type: model.NotificationSystemError, Title: "t", Content: "c"})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSend_RetryDelay_CanceledWhileWaiting_ReturnsContextError(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 1, RetryDelay: 2000, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	svc := NewNotificationService(staticWebhookConfigProvider{cfg: config, err: nil})

	ctx, cancel := context.WithCancel(context.Background())
	canceler := &cancelingNotifier{cancel: cancel, err: errors.New("send failed")}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return canceler, nil
	}

	err := svc.Send(ctx, model.NotificationMessage{Type: model.NotificationSystemError, Title: "t", Content: "c"})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, canceler.calls)
}

func TestSend_Success_ReturnsNil_AndLogsBuildErrAsWarning(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = true
	config.RetrySettings = model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "bad", Type: model.PlatformDiscord, Name: "bad", Enabled: true, URL: "https://example.com"},
		{ID: "good", Type: model.PlatformCustom, Name: "good", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	notifiers := map[string]*mockNotifier{}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		if p.ID == "bad" {
			return nil, errors.New("boom")
		}
		n := &mockNotifier{id: p.ID}
		notifiers[p.ID] = n
		return n, nil
	}

	var logBuf bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	err := svc.Send(context.Background(), model.NotificationMessage{
		Type:     model.NotificationSystemError,
		Title:    "t",
		Content:  "c",
		Severity: "error",
	})
	require.NoError(t, err)
	require.NotNil(t, notifiers["good"])
	require.Equal(t, 1, notifiers["good"].calls)

	logOut := logBuf.String()
	require.Contains(t, logOut, "Warning:")
	require.Contains(t, logOut, "partial failures")
	require.Contains(t, logOut, "platform bad")
	require.Contains(t, logOut, "boom")
}

func TestNotificationService_TestNotification_BypassesEnabled(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = false
	config.RetrySettings = model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	notifier := &mockNotifier{id: "p1"}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return notifier, nil
	}

	msg := model.NotificationMessage{
		Type:     model.NotificationType("ops.alert"),
		Title:    "title",
		Content:  "content",
		Severity: "warning",
	}

	require.NoError(t, svc.Send(context.Background(), msg))
	require.Equal(t, 0, notifier.calls)

	require.NoError(t, svc.TestNotification(context.Background(), msg))
	require.Equal(t, 1, notifier.calls)
	require.NotEmpty(t, notifier.messages)
	require.Contains(t, notifier.messages[0].Body, "[ops.alert]")
	require.True(t, strings.Contains(notifier.messages[0].Body, "content"))
}

func TestNotificationService_Send_IgnoresEnabledForNonOps(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = false
	config.NotificationTypes = newFullNotificationTypes()
	config.RetrySettings = model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	repo := &mockWebhookRepo{cfg: config}
	svc := NewNotificationService(NewWebhookConfigService(repo))

	notifier := &mockNotifier{id: "p1"}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return notifier, nil
	}

	msg := model.NotificationMessage{
		Type:     model.NotificationSystemError,
		Title:    "title",
		Content:  "content",
		Severity: "warning",
	}

	require.NoError(t, svc.Send(context.Background(), msg))
	require.Equal(t, 1, notifier.calls)
	require.NotEmpty(t, notifier.messages)
	require.Contains(t, notifier.messages[0].Body, "[systemError]")
}

func TestNotificationService_TestNotification_Defaults_ForceSend(t *testing.T) {
	config := model.NewWebhookConfig()
	config.Enabled = false
	config.NotificationTypes = newFullNotificationTypes()
	config.RetrySettings = model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1000}
	config.Platforms = []model.PlatformConfig{
		{ID: "p1", Type: model.PlatformDiscord, Name: "discord", Enabled: true, URL: "https://example.com"},
	}

	svc := NewNotificationService(NewWebhookConfigService(&mockWebhookRepo{cfg: config}))

	notifier := &mockNotifier{id: "p1"}
	svc.platformFactory = func(p model.PlatformConfig) (notify.Notifier, error) {
		return notifier, nil
	}

	require.NoError(t, svc.TestNotification(context.Background(), model.NotificationMessage{}))
	require.Equal(t, 1, notifier.calls)
	require.NotEmpty(t, notifier.messages)
	require.Equal(t, "🧪 测试通知", notifier.messages[0].Subject)
	require.Contains(t, notifier.messages[0].Body, "[test]")
	require.Contains(t, notifier.messages[0].Body, "Severity: info")
}
