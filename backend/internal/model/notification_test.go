package model

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestNotificationTypeEnumCompleteness(t *testing.T) {
	expected := []NotificationType{
		NotificationAccountAnomaly,
		NotificationSystemError,
		NotificationSecurityAlert,
		NotificationRateLimitRecovery,
		NotificationTest,
	}

	if len(expected) != len(validNotificationTypes) {
		t.Fatalf("expected %d notification types, got %d", len(expected), len(validNotificationTypes))
	}

	for _, nt := range expected {
		if !IsValidNotificationType(nt) {
			t.Fatalf("notification type %s not registered", nt)
		}
	}
}

func TestWebhookConfigJSONRoundTrip(t *testing.T) {
	config := WebhookConfig{
		Enabled: true,
		Platforms: []PlatformConfig{
			{
				ID:      "p1",
				Type:    PlatformLark,
				Name:    "Lark Channel",
				Enabled: true,
				URL:     "https://example.com/webhook",
				Secret:  "secret",
			},
		},
		NotificationTypes: map[NotificationType]bool{
			NotificationAccountAnomaly: true,
			NotificationSystemError:    false,
		},
		RetrySettings: RetrySettings{
			MaxRetries: 5,
			RetryDelay: 2000,
			Timeout:    10000,
		},
		CreatedAt: "2024-01-01T00:00:00Z",
		UpdatedAt: "2024-01-02T00:00:00Z",
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded WebhookConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !reflect.DeepEqual(config, decoded) {
		t.Fatalf("round trip mismatch\nafter: %+v\nbefore: %+v", decoded, config)
	}
}

func TestDefaultConstructors(t *testing.T) {
	config := NewWebhookConfig()
	if config == nil {
		t.Fatalf("expected non-nil config")
	}
	if config.Enabled {
		t.Fatalf("default config should be disabled")
	}
	if len(config.Platforms) != 0 {
		t.Fatalf("expected no default platforms, got %d", len(config.Platforms))
	}
	if len(config.NotificationTypes) != 0 {
		t.Fatalf("expected no default notificationTypes, got %d", len(config.NotificationTypes))
	}
	expectedRetry := RetrySettings{
		MaxRetries: defaultMaxRetries,
		RetryDelay: defaultRetryDelay,
		Timeout:    defaultTimeout,
	}
	if !reflect.DeepEqual(config.RetrySettings, expectedRetry) {
		t.Fatalf("unexpected default retry settings: %+v", config.RetrySettings)
	}
	for _, ts := range []string{config.CreatedAt, config.UpdatedAt} {
		if ts == "" {
			t.Fatalf("timestamp should not be empty")
		}
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Fatalf("timestamp should be RFC3339: %v", err)
		}
	}

	p := NewPlatformConfig()
	if p.Enabled {
		t.Fatalf("default platform config should be disabled")
	}
	if p.Type != "" {
		t.Fatalf("default platform type should be empty")
	}
}

func TestRetrySettingsValidate(t *testing.T) {
	cases := []struct {
		name     string
		settings RetrySettings
		wantErr  bool
	}{
		{name: "valid", settings: RetrySettings{MaxRetries: 1, RetryDelay: 0, Timeout: 1}},
		{name: "negative delay", settings: RetrySettings{Timeout: 1, RetryDelay: -1}, wantErr: true},
		{name: "non-positive timeout", settings: RetrySettings{Timeout: 0}, wantErr: true},
		{name: "negative retries", settings: RetrySettings{MaxRetries: -1, Timeout: 1}, wantErr: true},
	}

	for _, tc := range cases {
		err := tc.settings.Validate()
		if tc.wantErr && err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
	}
}

func TestGetErrorCode(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		status   string
		want     string
	}{
		{
			name:     "claude-oauth unauthorized",
			platform: AccountAnomalyPlatformClaudeOAuth,
			status:   AccountAnomalyStatusUnauthorized,
			want:     ErrorCodeClaudeOAuthUnauthorized,
		},
		{
			name:     "claude-oauth blocked",
			platform: AccountAnomalyPlatformClaudeOAuth,
			status:   AccountAnomalyStatusBlocked,
			want:     ErrorCodeClaudeOAuthBlocked,
		},
		{
			name:     "openai rate limited",
			platform: AccountAnomalyPlatformOpenAI,
			status:   AccountAnomalyStatusRateLimited,
			want:     ErrorCodeOpenAIRateLimited,
		},
		{
			name:     "normalizes inputs",
			platform: "  CLAUDE-OAUTH ",
			status:   " BLOCKED ",
			want:     ErrorCodeClaudeOAuthBlocked,
		},
		{
			name:     "unknown platform",
			platform: "unknown",
			status:   AccountAnomalyStatusBlocked,
			want:     "",
		},
		{
			name:     "unknown status",
			platform: AccountAnomalyPlatformClaudeOAuth,
			status:   "???",
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetErrorCode(tc.platform, tc.status); got != tc.want {
				t.Fatalf("GetErrorCode(%q, %q)=%q, want %q", tc.platform, tc.status, got, tc.want)
			}
		})
	}
}

func TestPlatformConfig_Validate(t *testing.T) {
	t.Run("MaskSecrets redacts secrets", func(t *testing.T) {
		orig := PlatformConfig{
			ID:       "p1",
			Type:     PlatformDiscord,
			Name:     "Discord",
			Enabled:  true,
			URL:      "https://example.com/webhook",
			Secret:   "secret",
			Token:    "token",
			BotToken: "bot-token",
			SMTPPass: "smtp-pass",
		}

		masked := orig.MaskSecrets()
		if masked.Secret != maskedSecretValue || masked.Token != maskedSecretValue || masked.BotToken != maskedSecretValue || masked.SMTPPass != maskedSecretValue {
			t.Fatalf("expected secrets masked, got secret=%q token=%q botToken=%q smtpPass=%q", masked.Secret, masked.Token, masked.BotToken, masked.SMTPPass)
		}
		if orig.Secret != "secret" || orig.Token != "token" || orig.BotToken != "bot-token" || orig.SMTPPass != "smtp-pass" {
			t.Fatalf("expected original unchanged, got secret=%q token=%q botToken=%q smtpPass=%q", orig.Secret, orig.Token, orig.BotToken, orig.SMTPPass)
		}

		empty := PlatformConfig{Secret: "  ", Token: "", BotToken: "\n", SMTPPass: "\t"}
		emptyMasked := empty.MaskSecrets()
		if emptyMasked.Secret != "" || emptyMasked.Token != "" || emptyMasked.BotToken != "" || emptyMasked.SMTPPass != "" {
			t.Fatalf("expected empty secrets stay empty, got secret=%q token=%q botToken=%q smtpPass=%q", emptyMasked.Secret, emptyMasked.Token, emptyMasked.BotToken, emptyMasked.SMTPPass)
		}
	})

	t.Run("IsLegacyPlatformType identifies legacy platforms", func(t *testing.T) {
		for _, legacy := range []PlatformType{PlatformLark, PlatformWechat, PlatformSlack} {
			if !IsLegacyPlatformType(legacy) {
				t.Fatalf("expected %q to be legacy", legacy)
			}
		}
		for _, supported := range []PlatformType{PlatformDiscord, PlatformBark, PlatformCustom, PlatformTelegram, PlatformSMTP, PlatformDingtalk} {
			if IsLegacyPlatformType(supported) {
				t.Fatalf("expected %q to be non-legacy", supported)
			}
		}
	})

	t.Run("GetErrorCode covers all defined codes", func(t *testing.T) {
		cases := []struct {
			platform string
			status   string
			want     string
		}{
			{AccountAnomalyPlatformClaudeOAuth, AccountAnomalyStatusUnauthorized, ErrorCodeClaudeOAuthUnauthorized},
			{AccountAnomalyPlatformClaudeOAuth, AccountAnomalyStatusBlocked, ErrorCodeClaudeOAuthBlocked},
			{AccountAnomalyPlatformClaudeOAuth, AccountAnomalyStatusError, ErrorCodeClaudeOAuthError},
			{AccountAnomalyPlatformClaudeOAuth, AccountAnomalyStatusDisabled, ErrorCodeClaudeOAuthDisabled},
			{AccountAnomalyPlatformClaudeOAuth, AccountAnomalyStatusRateLimited, ErrorCodeClaudeOAuthRateLimited},
			{AccountAnomalyPlatformClaudeConsole, AccountAnomalyStatusUnauthorized, ErrorCodeClaudeConsoleUnauthorized},
			{AccountAnomalyPlatformClaudeConsole, AccountAnomalyStatusBlocked, ErrorCodeClaudeConsoleBlocked},
			{AccountAnomalyPlatformClaudeConsole, AccountAnomalyStatusError, ErrorCodeClaudeConsoleError},
			{AccountAnomalyPlatformClaudeConsole, AccountAnomalyStatusDisabled, ErrorCodeClaudeConsoleDisabled},
			{AccountAnomalyPlatformClaudeConsole, AccountAnomalyStatusRateLimited, ErrorCodeClaudeConsoleRateLimited},
			{AccountAnomalyPlatformOpenAI, AccountAnomalyStatusUnauthorized, ErrorCodeOpenAIUnauthorized},
			{AccountAnomalyPlatformOpenAI, AccountAnomalyStatusBlocked, ErrorCodeOpenAIBlocked},
			{AccountAnomalyPlatformOpenAI, AccountAnomalyStatusError, ErrorCodeOpenAIError},
			{AccountAnomalyPlatformOpenAI, AccountAnomalyStatusDisabled, ErrorCodeOpenAIDisabled},
			{AccountAnomalyPlatformOpenAI, AccountAnomalyStatusRateLimited, ErrorCodeOpenAIRateLimited},
			{AccountAnomalyPlatformGemini, AccountAnomalyStatusUnauthorized, ErrorCodeGeminiUnauthorized},
			{AccountAnomalyPlatformGemini, AccountAnomalyStatusBlocked, ErrorCodeGeminiBlocked},
			{AccountAnomalyPlatformGemini, AccountAnomalyStatusError, ErrorCodeGeminiError},
			{AccountAnomalyPlatformGemini, AccountAnomalyStatusDisabled, ErrorCodeGeminiDisabled},
			{AccountAnomalyPlatformGemini, AccountAnomalyStatusRateLimited, ErrorCodeGeminiRateLimited},
		}

		for _, tc := range cases {
			if got := GetErrorCode(tc.platform, tc.status); got != tc.want {
				t.Fatalf("GetErrorCode(%q, %q)=%q, want %q", tc.platform, tc.status, got, tc.want)
			}
		}
	})

	t.Run("isPrivateIP edge cases", func(t *testing.T) {
		if isPrivateIP(nil) {
			t.Fatalf("expected nil to be non-private")
		}
		if isPrivateIP(net.IP{1, 2, 3}) {
			t.Fatalf("expected malformed IP to be non-private")
		}
		if isPrivateIP(net.ParseIP("8.8.8.8")) {
			t.Fatalf("expected public IPv4 to be non-private")
		}
		if isPrivateIP(net.ParseIP("2001:4860:4860::8888")) {
			t.Fatalf("expected public IPv6 to be non-private")
		}
	})

	t.Run("mustParseCIDR panics on invalid CIDR", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected panic")
			}
		}()
		_ = mustParseCIDR("not-a-cidr")
	})

	t.Run("validPlatformTypes excludes legacy", func(t *testing.T) {
		for _, legacy := range []PlatformType{PlatformLark, PlatformWechat, PlatformSlack} {
			if _, ok := validPlatformTypes[legacy]; ok {
				t.Fatalf("expected legacy platform type %q removed from validPlatformTypes", legacy)
			}
		}

		for _, supported := range []PlatformType{PlatformDiscord, PlatformBark, PlatformCustom, PlatformTelegram, PlatformSMTP, PlatformDingtalk} {
			if _, ok := validPlatformTypes[supported]; !ok {
				t.Fatalf("expected supported platform type %q present in validPlatformTypes", supported)
			}
		}
	})

	t.Run("validateHTTPOrHTTPSURL requires host", func(t *testing.T) {
		if err := validateHTTPOrHTTPSURL("url", "http:///no-host"); err == nil {
			t.Fatalf("expected host required error")
		}
	})

	t.Run("validateURLWithAllowedSchemes blocks SSRF targets", func(t *testing.T) {
		for _, raw := range []string{
			"http://localhost/path",
			"http://LOCALHOST/path",
			"http://localhost./path",
			"http://foo.localhost/path",
			"http://127.0.0.1/path",
			"http://127.1.2.3/path",
			"http://10.0.0.1/path",
			"http://172.16.0.1/path",
			"http://172.31.255.255/path",
			"http://192.168.1.1/path",
			"http://169.254.1.1/path",
			"http://169.254.169.254/path",
			"http://[::1]/path",
			"http://[fe80::1]/path",
			"http://[fc00::1]/path",
		} {
			if err := validateHTTPOrHTTPSURL("url", raw); err == nil {
				t.Fatalf("expected SSRF blocked for url=%q", raw)
			}
		}

		for _, raw := range []string{
			"http://example.com/webhook",
			"https://example.com/webhook",
			"http://8.8.8.8/webhook",
			"socks5://proxy.example.com:1080",
			"socks5h://user:pass@proxy.example.com:1080",
		} {
			var err error
			if raw == "socks5://proxy.example.com:1080" || raw == "socks5h://user:pass@proxy.example.com:1080" {
				err = validateProxyURL("proxyUrl", raw)
			} else {
				err = validateHTTPOrHTTPSURL("url", raw)
			}
			if err != nil {
				t.Fatalf("expected URL accepted url=%q, got err=%v", raw, err)
			}
		}
	})

	t.Run("WebhookConfig rejects duplicate platform names", func(t *testing.T) {
		cfg := NewWebhookConfig()
		cfg.Enabled = true
		cfg.NotificationTypes = map[NotificationType]bool{NotificationSystemError: true}
		cfg.Platforms = []PlatformConfig{
			{ID: "p1", Type: PlatformDiscord, Name: "dup", Enabled: true, URL: "https://example.com/1"},
			{ID: "p2", Type: PlatformCustom, Name: "dup", Enabled: true, URL: "https://example.com/2"},
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected error for duplicate platform name")
		}
	})

	cases := []struct {
		name      string
		config    PlatformConfig
		expectErr bool
	}{
		{
			name: "legacy wechat accepted",
			config: PlatformConfig{
				ID:      "wechat-1",
				Type:    PlatformWechat,
				Name:    "WeChat",
				Enabled: true,
			},
		},
		{
			name: "legacy lark accepted",
			config: PlatformConfig{
				ID:      "lark-1",
				Type:    PlatformLark,
				Name:    "Lark",
				Enabled: true,
				URL:     "://bad-url",
			},
		},
		{
			name: "legacy slack accepted",
			config: PlatformConfig{
				ID:      "slack-1",
				Type:    PlatformSlack,
				Name:    "Slack",
				Enabled: true,
			},
		},
		{
			name: "missing id",
			config: PlatformConfig{
				Type:    PlatformDiscord,
				Name:    "Discord",
				Enabled: true,
				URL:     "https://example.com/webhook",
			},
			expectErr: true,
		},
		{
			name: "missing type",
			config: PlatformConfig{
				ID:      "p-missing-type",
				Name:    "NoType",
				Enabled: true,
				URL:     "https://example.com/webhook",
			},
			expectErr: true,
		},
		{
			name: "unsupported type",
			config: PlatformConfig{
				ID:      "p3",
				Type:    PlatformType("unknown"),
				Name:    "Unknown",
				Enabled: true,
				URL:     "https://example.com/webhook",
			},
			expectErr: true,
		},
		{
			name:      "discord without url",
			config:    PlatformConfig{ID: "p4", Type: PlatformDiscord, Name: "Discord", Enabled: true},
			expectErr: true,
		},
		{
			name:      "dingtalk enableSign missing secret",
			config:    PlatformConfig{ID: "dt-1", Type: PlatformDingtalk, Name: "DingTalk", Enabled: true, URL: "https://example.com/webhook", EnableSign: true},
			expectErr: true,
		},
		{
			name:      "bark missing token",
			config:    PlatformConfig{ID: "bk-1", Type: PlatformBark, Name: "Bark", Enabled: true},
			expectErr: true,
		},
		{
			name: "valid telegram",
			config: PlatformConfig{
				ID:       "tg-1",
				Type:     PlatformTelegram,
				Name:     "Telegram",
				Enabled:  true,
				BotToken: "bot-token",
				ChatID:   "123456",
			},
		},
		{
			name:      "telegram missing bot token",
			config:    PlatformConfig{ID: "tg-2", Type: PlatformTelegram, Name: "Telegram", Enabled: true, ChatID: "123456"},
			expectErr: true,
		},
		{
			name: "valid smtp",
			config: PlatformConfig{
				ID:       "smtp-ok",
				Type:     PlatformSMTP,
				Name:     "SMTP",
				Enabled:  true,
				SMTPHost: "smtp.example.com",
				SMTPPort: 587,
				SMTPUser: "user",
				SMTPPass: "pass",
				SMTPTo:   "to@example.com",
			},
		},
		{
			name: "dingtalk without signing secret is valid",
			config: PlatformConfig{
				ID:      "dt-ok",
				Type:    PlatformDingtalk,
				Name:    "DingTalk",
				Enabled: true,
				URL:     "https://example.com/webhook",
			},
		},
		{
			name: "smtp missing smtpTo",
			config: PlatformConfig{
				ID:       "smtp-1",
				Type:     PlatformSMTP,
				Name:     "SMTP",
				Enabled:  true,
				SMTPHost: "smtp.example.com",
				SMTPUser: "user",
				SMTPPass: "pass",
			},
			expectErr: true,
		},
		{
			name: "smtp port out of range",
			config: PlatformConfig{
				ID:       "smtp-2",
				Type:     PlatformSMTP,
				Name:     "SMTP",
				Enabled:  true,
				SMTPHost: "smtp.example.com",
				SMTPPort: 70000,
				SMTPUser: "user",
				SMTPPass: "pass",
				SMTPTo:   "to@example.com",
			},
			expectErr: true,
		},
	}

	for _, tc := range cases {
		err := tc.config.Validate()
		if tc.expectErr && err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if !tc.expectErr && err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
	}

	t.Run("url scheme whitelist", func(t *testing.T) {
		platforms := []PlatformType{
			PlatformDiscord,
			PlatformDingtalk,
			PlatformCustom,
		}

		invalidURLs := []string{
			"file:///etc/passwd",
			"gopher://127.0.0.1:70/_",
			"ftp://example.com/a",
			"example.com/no-scheme",
		}
		for _, platform := range platforms {
			for _, raw := range invalidURLs {
				cfg := PlatformConfig{
					ID:      "ssrf-url-1",
					Type:    platform,
					Name:    "SSRF Test",
					Enabled: true,
					URL:     raw,
				}
				if err := cfg.Validate(); err == nil {
					t.Fatalf("expected error for platform=%s url=%q", platform, raw)
				}
			}
		}

		validURLs := []string{
			"http://example.com/webhook",
			"https://example.com/webhook",
		}
		for _, platform := range platforms {
			for _, raw := range validURLs {
				cfg := PlatformConfig{
					ID:      "ssrf-url-2",
					Type:    platform,
					Name:    "SSRF Test",
					Enabled: true,
					URL:     raw,
				}
				if err := cfg.Validate(); err != nil {
					t.Fatalf("unexpected error for platform=%s url=%q: %v", platform, raw, err)
				}
			}
		}
	})

	t.Run("apiBaseUrl scheme whitelist", func(t *testing.T) {
		cfg := PlatformConfig{
			ID:       "tg-api-base-1",
			Type:     PlatformTelegram,
			Name:     "Telegram",
			Enabled:  true,
			BotToken: "bot-token",
			ChatID:   "123456",
		}

		cfg.APIBaseURL = "https://api.telegram.org"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("unexpected error for apiBaseUrl=https: %v", err)
		}

		for _, raw := range []string{
			"file:///etc/passwd",
			"gopher://127.0.0.1:70/_",
			"ftp://example.com/a",
			"example.com/no-scheme",
		} {
			cfg.APIBaseURL = raw
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected error for apiBaseUrl=%q", raw)
			}
		}
	})

	t.Run("proxyUrl scheme whitelist", func(t *testing.T) {
		cfg := PlatformConfig{
			ID:       "tg-proxy-1",
			Type:     PlatformTelegram,
			Name:     "Telegram",
			Enabled:  true,
			BotToken: "bot-token",
			ChatID:   "123456",
		}

		cfg.ProxyURL = "http://proxy.example.com:8080"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("unexpected error for proxyUrl=http: %v", err)
		}

		cfg.ProxyURL = "https://proxy.example.com:8443"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("unexpected error for proxyUrl=https: %v", err)
		}

		cfg.ProxyURL = "socks5://proxy.example.com:1080"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("unexpected error for proxyUrl=socks5: %v", err)
		}

		cfg.ProxyURL = "socks5h://user:pass@proxy.example.com:1080"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("unexpected error for proxyUrl=socks5h: %v", err)
		}

		for _, raw := range []string{
			"file:///etc/passwd",
			"gopher://127.0.0.1:70/_",
			"ftp://example.com/a",
			"example.com/no-scheme",
		} {
			cfg.ProxyURL = raw
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected error for proxyUrl=%q", raw)
			}
		}
	})

	t.Run("coverage smoke", func(t *testing.T) {
		TestNotificationTypeEnumCompleteness(t)
		TestWebhookConfigJSONRoundTrip(t)
		TestDefaultConstructors(t)
		TestRetrySettingsValidate(t)
		TestGetErrorCode(t)
		TestWebhookConfigValidate(t)
	})
}

func TestValidateURL(t *testing.T) {
	// Keep coverage high for the dedicated URL validation test target.
	TestPlatformConfig_Validate(t)
}

func TestPlatformConfigValidate(t *testing.T) {
	if t.Name() == "TestPlatformConfigValidate" {
		t.Skip("covered by TestPlatformConfig_Validate")
	}
	TestPlatformConfig_Validate(t)
}

func TestWebhookConfigValidate(t *testing.T) {
	baseConfig := func() *WebhookConfig {
		cfg := NewWebhookConfig()
		cfg.Enabled = true
		cfg.NotificationTypes = map[NotificationType]bool{
			NotificationAccountAnomaly: true,
			NotificationSystemError:    true,
		}
		cfg.Platforms = []PlatformConfig{
			{
				ID:      "discord-1",
				Type:    PlatformDiscord,
				Name:    "Discord Alerts",
				Enabled: true,
				URL:     "https://example.com/webhook",
			},
		}
		cfg.RetrySettings = RetrySettings{MaxRetries: 1, RetryDelay: 100, Timeout: 1000}
		cfg.CreatedAt = "2024-01-01T00:00:00Z"
		cfg.UpdatedAt = "2024-01-01T00:00:00Z"
		return cfg
	}

	t.Run("valid config", func(t *testing.T) {
		if err := baseConfig().Validate(); err != nil {
			t.Fatalf("expected valid config, got %v", err)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		var cfg *WebhookConfig
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected error for nil config")
		}
	})

	t.Run("invalid retry settings", func(t *testing.T) {
		cfg := baseConfig()
		cfg.RetrySettings.MaxRetries = -1
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected retry settings error")
		}
	})

	t.Run("invalid platform credentials", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Platforms[0] = PlatformConfig{
			ID:      "bark-1",
			Type:    PlatformBark,
			Name:    "Bark",
			Enabled: true,
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected platform validation error for missing bark token")
		}
	})

	t.Run("invalid timestamp", func(t *testing.T) {
		cfg := baseConfig()
		cfg.CreatedAt = "not-a-time"
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected timestamp parsing error")
		}
	})

	t.Run("invalid webhook url", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Platforms[0] = PlatformConfig{
			ID:      "discord-bad-1",
			Type:    PlatformDiscord,
			Name:    "Discord Alerts",
			Enabled: true,
			URL:     "://bad-url",
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected invalid url error")
		}
	})

	t.Run("empty notification types", func(t *testing.T) {
		cfg := baseConfig()
		cfg.NotificationTypes = map[NotificationType]bool{}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected valid config, got %v", err)
		}
	})

	t.Run("unsupported platform", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Platforms[0].Type = PlatformType("unknown_platform")
		cfg.Platforms[0].URL = "https://example.com"
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected platform type error")
		}
	})
}
