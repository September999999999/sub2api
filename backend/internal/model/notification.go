package model

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type NotificationType string

const (
	NotificationAccountAnomaly    NotificationType = "accountAnomaly"
	NotificationSystemError       NotificationType = "systemError"
	NotificationSecurityAlert     NotificationType = "securityAlert"
	NotificationRateLimitRecovery NotificationType = "rateLimitRecovery"
	NotificationTest              NotificationType = "test"
)

type PlatformType string

const (
	PlatformLark     PlatformType = "lark"
	PlatformWechat   PlatformType = "wechat"
	PlatformDiscord  PlatformType = "discord"
	PlatformSlack    PlatformType = "slack"
	PlatformBark     PlatformType = "bark"
	PlatformCustom   PlatformType = "custom"
	PlatformTelegram PlatformType = "telegram"
	PlatformSMTP     PlatformType = "smtp"
	PlatformDingtalk PlatformType = "dingtalk"
)

type WebhookConfig struct {
	Enabled           bool                      `json:"enabled"`
	Platforms         []PlatformConfig          `json:"platforms"`
	NotificationTypes map[NotificationType]bool `json:"notificationTypes,omitempty"`
	RetrySettings     RetrySettings             `json:"retrySettings"`
	CreatedAt         string                    `json:"createdAt"`
	UpdatedAt         string                    `json:"updatedAt"`
}

type PlatformConfig struct {
	ID         string       `json:"id"`
	Type       PlatformType `json:"type"`
	Name       string       `json:"name"`
	Enabled    bool         `json:"enabled"`
	EnableSign bool         `json:"enableSign,omitempty"`
	URL        string       `json:"url,omitempty"`
	Secret     string       `json:"secret,omitempty"`
	Token      string       `json:"token,omitempty"`
	ChannelID  string       `json:"channelId,omitempty"`
	BotToken   string       `json:"botToken,omitempty"`
	ChatID     string       `json:"chatId,omitempty"`
	APIBaseURL string       `json:"apiBaseUrl,omitempty"`
	ProxyURL   string       `json:"proxyUrl,omitempty"`

	SMTPHost      string `json:"smtpHost,omitempty"`
	SMTPPort      int    `json:"smtpPort,omitempty"`
	SMTPUser      string `json:"smtpUser,omitempty"`
	SMTPPass      string `json:"smtpPass,omitempty"`
	SMTPFrom      string `json:"smtpFrom,omitempty"`
	SMTPTo        string `json:"smtpTo,omitempty"`
	SMTPSecure    bool   `json:"smtpSecure,omitempty"`
	SMTPIgnoreTLS bool   `json:"smtpIgnoreTLS,omitempty"`
}

const maskedSecretValue = "******"

func maskedOrEmpty(raw string) string {
	if strings.TrimSpace(raw) != "" {
		return maskedSecretValue
	}
	return ""
}

// MaskSecrets returns a copy of the config with secret fields redacted for API responses.
// When the secret is not configured, the field is kept empty to avoid confusing the caller.
func (p PlatformConfig) MaskSecrets() PlatformConfig {
	out := p
	out.Secret = maskedOrEmpty(out.Secret)
	out.Token = maskedOrEmpty(out.Token)
	out.BotToken = maskedOrEmpty(out.BotToken)
	out.SMTPPass = maskedOrEmpty(out.SMTPPass)
	return out
}

type RetrySettings struct {
	MaxRetries int `json:"maxRetries"`
	RetryDelay int `json:"retryDelay"` // milliseconds
	Timeout    int `json:"timeout"`    // milliseconds
}

type NotificationMessage struct {
	Type      NotificationType `json:"type"`
	Title     string           `json:"title"`
	Content   string           `json:"content"`
	Severity  string           `json:"severity"` // info, warning, error, critical
	Metadata  map[string]any   `json:"metadata,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
}

// accountAnomaly status values (aligned with claude-relay-service).
const (
	AccountAnomalyStatusUnauthorized = "unauthorized"
	AccountAnomalyStatusBlocked      = "blocked"
	AccountAnomalyStatusError        = "error"
	AccountAnomalyStatusDisabled     = "disabled"
	AccountAnomalyStatusRateLimited  = "rate_limited"
)

// accountAnomaly platform values (aligned with claude-relay-service).
const (
	AccountAnomalyPlatformClaudeOAuth   = "claude-oauth"
	AccountAnomalyPlatformClaudeConsole = "claude-console"
	AccountAnomalyPlatformOpenAI        = "openai"
	AccountAnomalyPlatformGemini        = "gemini"
)

// accountAnomaly errorCode values (aligned with claude-relay-service).
const (
	ErrorCodeClaudeOAuthUnauthorized = "CLAUDE_OAUTH_UNAUTHORIZED"
	ErrorCodeClaudeOAuthBlocked      = "CLAUDE_OAUTH_BLOCKED"
	ErrorCodeClaudeOAuthError        = "CLAUDE_OAUTH_ERROR"
	ErrorCodeClaudeOAuthDisabled     = "CLAUDE_OAUTH_MANUALLY_DISABLED"
	ErrorCodeClaudeOAuthRateLimited  = "CLAUDE_OAUTH_RATE_LIMITED"

	ErrorCodeClaudeConsoleUnauthorized = "CLAUDE_CONSOLE_UNAUTHORIZED"
	ErrorCodeClaudeConsoleBlocked      = "CLAUDE_CONSOLE_BLOCKED"
	ErrorCodeClaudeConsoleError        = "CLAUDE_CONSOLE_ERROR"
	ErrorCodeClaudeConsoleDisabled     = "CLAUDE_CONSOLE_MANUALLY_DISABLED"
	ErrorCodeClaudeConsoleRateLimited  = "CLAUDE_CONSOLE_RATE_LIMITED"

	ErrorCodeOpenAIUnauthorized = "OPENAI_UNAUTHORIZED"
	ErrorCodeOpenAIBlocked      = "OPENAI_BLOCKED"
	ErrorCodeOpenAIError        = "OPENAI_ERROR"
	ErrorCodeOpenAIDisabled     = "OPENAI_MANUALLY_DISABLED"
	ErrorCodeOpenAIRateLimited  = "OPENAI_RATE_LIMITED"

	ErrorCodeGeminiUnauthorized = "GEMINI_UNAUTHORIZED"
	ErrorCodeGeminiBlocked      = "GEMINI_BLOCKED"
	ErrorCodeGeminiError        = "GEMINI_ERROR"
	ErrorCodeGeminiDisabled     = "GEMINI_MANUALLY_DISABLED"
	ErrorCodeGeminiRateLimited  = "GEMINI_RATE_LIMITED"
)

var accountAnomalyErrorCodes = map[string]map[string]string{
	AccountAnomalyPlatformClaudeOAuth: {
		AccountAnomalyStatusUnauthorized: ErrorCodeClaudeOAuthUnauthorized,
		AccountAnomalyStatusBlocked:      ErrorCodeClaudeOAuthBlocked,
		AccountAnomalyStatusError:        ErrorCodeClaudeOAuthError,
		AccountAnomalyStatusDisabled:     ErrorCodeClaudeOAuthDisabled,
		AccountAnomalyStatusRateLimited:  ErrorCodeClaudeOAuthRateLimited,
	},
	AccountAnomalyPlatformClaudeConsole: {
		AccountAnomalyStatusUnauthorized: ErrorCodeClaudeConsoleUnauthorized,
		AccountAnomalyStatusBlocked:      ErrorCodeClaudeConsoleBlocked,
		AccountAnomalyStatusError:        ErrorCodeClaudeConsoleError,
		AccountAnomalyStatusDisabled:     ErrorCodeClaudeConsoleDisabled,
		AccountAnomalyStatusRateLimited:  ErrorCodeClaudeConsoleRateLimited,
	},
	AccountAnomalyPlatformOpenAI: {
		AccountAnomalyStatusUnauthorized: ErrorCodeOpenAIUnauthorized,
		AccountAnomalyStatusBlocked:      ErrorCodeOpenAIBlocked,
		AccountAnomalyStatusError:        ErrorCodeOpenAIError,
		AccountAnomalyStatusDisabled:     ErrorCodeOpenAIDisabled,
		AccountAnomalyStatusRateLimited:  ErrorCodeOpenAIRateLimited,
	},
	AccountAnomalyPlatformGemini: {
		AccountAnomalyStatusUnauthorized: ErrorCodeGeminiUnauthorized,
		AccountAnomalyStatusBlocked:      ErrorCodeGeminiBlocked,
		AccountAnomalyStatusError:        ErrorCodeGeminiError,
		AccountAnomalyStatusDisabled:     ErrorCodeGeminiDisabled,
		AccountAnomalyStatusRateLimited:  ErrorCodeGeminiRateLimited,
	},
}

// GetErrorCode returns accountAnomaly errorCode by platform + status.
// Unknown inputs return "" to keep metadata clean and backward compatible.
func GetErrorCode(platform, status string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	status = strings.ToLower(strings.TrimSpace(status))

	statusMap, ok := accountAnomalyErrorCodes[platform]
	if !ok {
		return ""
	}
	return statusMap[status]
}

var validNotificationTypes = map[NotificationType]struct{}{
	NotificationAccountAnomaly:    {},
	NotificationSystemError:       {},
	NotificationSecurityAlert:     {},
	NotificationRateLimitRecovery: {},
	NotificationTest:              {},
}

var validPlatformTypes = map[PlatformType]struct{}{
	PlatformDiscord:  {},
	PlatformBark:     {},
	PlatformCustom:   {},
	PlatformTelegram: {},
	PlatformSMTP:     {},
	PlatformDingtalk: {},
}

var legacyPlatformTypes = map[PlatformType]struct{}{
	PlatformLark:   {},
	PlatformWechat: {},
	PlatformSlack:  {},
}

func IsLegacyPlatformType(t PlatformType) bool {
	_, ok := legacyPlatformTypes[t]
	return ok
}

const (
	defaultMaxRetries = 3
	defaultRetryDelay = 1000
	defaultTimeout    = 5000
)

func NewWebhookConfig() *WebhookConfig {
	now := time.Now().UTC().Format(time.RFC3339)
	return &WebhookConfig{
		Enabled:   false,
		Platforms: []PlatformConfig{},
		RetrySettings: RetrySettings{
			MaxRetries: defaultMaxRetries,
			RetryDelay: defaultRetryDelay,
			Timeout:    defaultTimeout,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func NewPlatformConfig() PlatformConfig {
	return PlatformConfig{
		Enabled: false,
		Type:    "",
	}
}

func (r RetrySettings) Validate() error {
	if r.MaxRetries < 0 {
		return errors.New("maxRetries cannot be negative")
	}
	if r.RetryDelay < 0 {
		return errors.New("retryDelay cannot be negative")
	}
	if r.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	return nil
}

func mustParseCIDR(cidr string) *net.IPNet {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil || network == nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", cidr, err))
	}
	return network
}

var privateIPv4Nets = []*net.IPNet{
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("169.254.0.0/16"),
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}

	if v4 := ip.To4(); v4 != nil {
		// Explicitly block the cloud metadata endpoint (also covered by 169.254.0.0/16).
		if v4[0] == 169 && v4[1] == 254 && v4[2] == 169 && v4[3] == 254 {
			return true
		}
		for _, network := range privateIPv4Nets {
			if network.Contains(v4) {
				return true
			}
		}
		return false
	}

	// IPv6 hardening: loopback/link-local/ULA are not valid webhook/proxy targets.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return false
	}
	// fc00::/7 unique local addresses.
	return ip16[0]&0xfe == 0xfc
}

func validateURLWithAllowedSchemes(fieldName, raw string, allowedSchemes map[string]struct{}) error {
	raw = strings.TrimSpace(raw)
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", fieldName, err)
	}

	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if _, ok := allowedSchemes[scheme]; !ok {
		return fmt.Errorf("unsupported %s scheme: %s", fieldName, scheme)
	}

	hostname := strings.TrimSuffix(strings.TrimSpace(parsed.Hostname()), ".")
	if hostname == "" {
		return fmt.Errorf("%s host is required", fieldName)
	}
	hostnameLower := strings.ToLower(hostname)
	if hostnameLower == "localhost" || strings.HasSuffix(hostnameLower, ".localhost") {
		return fmt.Errorf("%s host is not allowed", fieldName)
	}
	if ip := net.ParseIP(hostname); ip != nil && isPrivateIP(ip) {
		return fmt.Errorf("%s host is not allowed", fieldName)
	}
	return nil
}

var allowedHTTPOrHTTPSURLSchemes = map[string]struct{}{
	"http":  {},
	"https": {},
}

var allowedProxyURLSchemes = map[string]struct{}{
	"http":    {},
	"https":   {},
	"socks5":  {},
	"socks5h": {},
}

func validateHTTPOrHTTPSURL(fieldName, raw string) error {
	return validateURLWithAllowedSchemes(fieldName, raw, allowedHTTPOrHTTPSURLSchemes)
}

func validateProxyURL(fieldName, raw string) error {
	return validateURLWithAllowedSchemes(fieldName, raw, allowedProxyURLSchemes)
}

func (p PlatformConfig) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if p.Type == "" {
		return errors.New("type is required")
	}
	// Backward compatibility: allow legacy types (slack/lark/wechat) to exist in stored config
	// without blocking config reads/updates. These types are removed from the supported platform list
	// and should not be validated/treated as active platforms.
	if _, ok := legacyPlatformTypes[p.Type]; ok {
		return nil
	}
	if _, ok := validPlatformTypes[p.Type]; !ok {
		return fmt.Errorf("unsupported platform type: %s", p.Type)
	}
	if p.URL != "" {
		if err := validateHTTPOrHTTPSURL("url", p.URL); err != nil {
			return err
		}
	}
	if p.APIBaseURL != "" {
		if err := validateHTTPOrHTTPSURL("apiBaseUrl", p.APIBaseURL); err != nil {
			return err
		}
	}
	if p.ProxyURL != "" {
		if err := validateProxyURL("proxyUrl", p.ProxyURL); err != nil {
			return err
		}
	}
	switch p.Type {
	case PlatformDiscord, PlatformDingtalk:
		if p.URL == "" {
			return errors.New("url is required for webhook platform")
		}
		if p.Type == PlatformDingtalk && p.EnableSign && p.Secret == "" {
			return errors.New("secret is required when enableSign is true for dingtalk")
		}
	case PlatformBark:
		if p.Token == "" {
			return errors.New("token (device key) is required for bark")
		}
	case PlatformCustom:
		if p.URL == "" {
			return errors.New("url is required for custom webhook")
		}
	case PlatformTelegram:
		if strings.TrimSpace(p.BotToken) == "" {
			return errors.New("botToken is required for telegram")
		}
		if strings.TrimSpace(p.ChatID) == "" {
			return errors.New("chatId is required for telegram")
		}
	case PlatformSMTP:
		if strings.TrimSpace(p.SMTPHost) == "" {
			return errors.New("smtpHost is required for smtp")
		}
		if p.SMTPPort != 0 && (p.SMTPPort < 1 || p.SMTPPort > 65535) {
			return errors.New("smtpPort must be between 1 and 65535")
		}
		if strings.TrimSpace(p.SMTPUser) == "" {
			return errors.New("smtpUser is required for smtp")
		}
		if strings.TrimSpace(p.SMTPPass) == "" {
			return errors.New("smtpPass is required for smtp")
		}
		if strings.TrimSpace(p.SMTPTo) == "" {
			return errors.New("smtpTo is required for smtp")
		}
	}
	return nil
}

func (c *WebhookConfig) Validate() error {
	if c == nil {
		return errors.New("webhook config is nil")
	}
	if err := c.RetrySettings.Validate(); err != nil {
		return fmt.Errorf("invalid retry settings: %w", err)
	}
	nameSet := make(map[string]struct{}, len(c.Platforms))
	for i := range c.Platforms {
		if err := c.Platforms[i].Validate(); err != nil {
			return fmt.Errorf("platform %q invalid: %w", c.Platforms[i].ID, err)
		}
		normalizedName := strings.TrimSpace(c.Platforms[i].Name)
		if _, exists := nameSet[normalizedName]; exists {
			return fmt.Errorf("duplicate platform name: %s", normalizedName)
		}
		nameSet[normalizedName] = struct{}{}
	}
	if c.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, c.CreatedAt); err != nil {
			return fmt.Errorf("invalid createdAt: %w", err)
		}
	}
	if c.UpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339, c.UpdatedAt); err != nil {
			return fmt.Errorf("invalid updatedAt: %w", err)
		}
	}
	return nil
}

func IsValidNotificationType(t NotificationType) bool {
	_, ok := validNotificationTypes[t]
	return ok
}
