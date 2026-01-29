package admin

import (
	"context"
	"errors"
	"log"
	"maps"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// NotificationHandler 管理通知配置的处理器
type NotificationHandler struct {
	configSvc       *service.WebhookConfigService
	notificationSvc *service.NotificationService
}

func (h *NotificationHandler) getMaskedPlatforms(ctx context.Context) ([]model.PlatformConfig, error) {
	cfg, err := h.configSvc.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	return maskPlatforms(cfg.Platforms), nil
}

type webhookConfigIssue struct {
	Level       string `json:"level"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type webhookConfigState struct {
	Status string               `json:"status"`
	Source string               `json:"source"`
	Issues []webhookConfigIssue `json:"issues"`
}

type webhookConfigGetResponse struct {
	Config *model.WebhookConfig `json:"config"`
	State  webhookConfigState   `json:"state"`
}

type webhookPlatformsGetResponse struct {
	Items []model.PlatformConfig `json:"items"`
	State webhookConfigState     `json:"state"`
}

func okWebhookState() webhookConfigState {
	return webhookConfigState{
		Status: "ok",
		Source: "settings",
		Issues: []webhookConfigIssue{},
	}
}

func degradedWebhookState(err error) webhookConfigState {
	issue := webhookConfigIssue{
		Level:   "error",
		Code:    "WEBHOOK_CONFIG_ERROR",
		Message: "Failed to load webhook config",
	}
	if err != nil {
		msg := strings.TrimSpace(err.Error())
		if msg != "" {
			issue.Message = msg
		}

		msgLower := strings.ToLower(msg)
		if strings.Contains(msgLower, "encryption key") {
			issue.Code = "WEBHOOK_CONFIG_ENCRYPTION_KEY_MISSING"
			issue.Remediation = "Set SUB2API_WEBHOOK_CONFIG_ENCRYPTION_KEY (or configure jwt.secret) and restart the service"
		} else if strings.Contains(msgLower, "decrypt") {
			issue.Code = "WEBHOOK_CONFIG_DECRYPT_FAILED"
			issue.Remediation = "Verify the webhook config encryption key matches the one used to encrypt stored secrets"
		} else if strings.Contains(msgLower, "unmarshal") || strings.Contains(msgLower, "invalid character") {
			issue.Code = "WEBHOOK_CONFIG_INVALID_JSON"
			issue.Remediation = "Re-save webhook notification settings to rewrite a valid config"
		} else if strings.Contains(msgLower, "validate") || strings.Contains(msgLower, "invalid") {
			issue.Code = "WEBHOOK_CONFIG_INVALID"
			issue.Remediation = "Re-save webhook notification settings to fix invalid fields"
		}
	}
	return webhookConfigState{
		Status: "degraded",
		Source: "default",
		Issues: []webhookConfigIssue{issue},
	}
}

// NewNotificationHandler 创建通知处理器
func NewNotificationHandler(configSvc *service.WebhookConfigService, notificationSvc *service.NotificationService) *NotificationHandler {
	return &NotificationHandler{
		configSvc:       configSvc,
		notificationSvc: notificationSvc,
	}
}

// WebhookConfigRequest 配置更新/校验请求
type WebhookConfigRequest struct {
	Enabled           *bool                           `json:"enabled,omitempty"`
	Platforms         []model.PlatformConfig          `json:"platforms"`
	NotificationTypes map[model.NotificationType]bool `json:"notificationTypes"`
	RetrySettings     *model.RetrySettings            `json:"retrySettings,omitempty"`
}

// AddPlatformRequest 新增平台请求
type AddPlatformRequest struct {
	Type      model.PlatformType `json:"type" binding:"required,oneof=bark custom dingtalk discord telegram smtp"`
	Name      string             `json:"name" binding:"required"`
	Enabled   bool               `json:"enabled"`
	URL       string             `json:"url"`
	Secret    string             `json:"secret"`
	Token     string             `json:"token"`
	ChannelID string             `json:"channelId"`

	// Dingtalk
	EnableSign bool `json:"enableSign"`

	// Telegram
	BotToken   string `json:"botToken"`
	ChatID     string `json:"chatId"`
	APIBaseURL string `json:"apiBaseUrl"`
	ProxyURL   string `json:"proxyUrl"`

	// SMTP
	SMTPHost      string `json:"smtpHost"`
	SMTPPort      int    `json:"smtpPort"`
	SMTPUser      string `json:"smtpUser"`
	SMTPPass      string `json:"smtpPass"`
	SMTPFrom      string `json:"smtpFrom"`
	SMTPTo        string `json:"smtpTo"`
	SMTPSecure    bool   `json:"smtpSecure"`
	SMTPIgnoreTLS bool   `json:"smtpIgnoreTLS"`
}

// UpdatePlatformRequest 更新平台请求
type UpdatePlatformRequest AddPlatformRequest

// TestNotificationRequest 发送测试通知请求
type TestNotificationRequest struct {
	Type     model.NotificationType `json:"type"`
	Title    string                 `json:"title"`
	Content  string                 `json:"content"`
	Severity string                 `json:"severity" binding:"omitempty,oneof=info warning error critical"`
	Metadata map[string]any         `json:"metadata"`
}

// SendNotificationRequest 外部系统发送通知请求
type SendNotificationRequest struct {
	Type     model.NotificationType `json:"type" binding:"required"`
	Title    string                 `json:"title" binding:"required"`
	Content  string                 `json:"content" binding:"required"`
	Severity string                 `json:"severity" binding:"omitempty,oneof=info warning error critical"`
	Metadata map[string]any         `json:"metadata"`
}

// GetConfig 获取通知配置
// GET /api/v1/admin/webhook/config
func (h *NotificationHandler) GetConfig(c *gin.Context) {
	cfg, err := h.configSvc.GetConfig(c.Request.Context())
	if err != nil {
		fallback := maskWebhookConfig(model.NewWebhookConfig())
		response.Success(c, webhookConfigGetResponse{
			Config: fallback,
			State:  degradedWebhookState(err),
		})
		return
	}

	response.Success(c, webhookConfigGetResponse{
		Config: maskWebhookConfig(cfg),
		State:  okWebhookState(),
	})
}

// UpdateConfig 更新通知配置
// PUT /api/v1/admin/webhook/config
func (h *NotificationHandler) UpdateConfig(c *gin.Context) {
	var req WebhookConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	current, err := h.configSvc.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if isEmptyWebhookConfigRequest(req) {
		response.BadRequest(c, "Invalid request")
		return
	}

	cfg := h.buildWebhookConfig(req, current)

	if err := h.configSvc.ValidateConfig(cfg); err != nil {
		response.BadRequest(c, "Invalid config: "+err.Error())
		return
	}

	if err := h.configSvc.UpdateConfig(c.Request.Context(), cfg); err != nil {
		h.auditLog(c, "notification.update_config", "result=error err=%v", err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.update_config", "result=success enabled=%v platforms=%d", cfg.Enabled, len(cfg.Platforms))
	response.Success(c, maskWebhookConfig(cfg))
}

// GetPlatforms 获取所有通知平台
// GET /api/v1/admin/webhook/platforms
func (h *NotificationHandler) GetPlatforms(c *gin.Context) {
	items, err := h.getMaskedPlatforms(c.Request.Context())
	if err != nil {
		response.Success(c, webhookPlatformsGetResponse{
			Items: []model.PlatformConfig{},
			State: degradedWebhookState(err),
		})
		return
	}

	response.Success(c, webhookPlatformsGetResponse{
		Items: items,
		State: okWebhookState(),
	})
}

// AddPlatform 新增通知平台
// POST /api/v1/admin/webhook/platforms
func (h *NotificationHandler) AddPlatform(c *gin.Context) {
	var req AddPlatformRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	platform := h.normalizePlatform(reqToPlatform(uuid.New().String(), req))
	if err := platform.Validate(); err != nil {
		response.BadRequest(c, "Invalid platform: "+err.Error())
		return
	}

	if err := h.configSvc.AddPlatform(c.Request.Context(), platform); err != nil {
		h.auditLog(c, "notification.add_platform", "result=error platform_id=%s platform_type=%s err=%v", platform.ID, platform.Type, err)
		response.ErrorFrom(c, err)
		return
	}

	items, err := h.getMaskedPlatforms(c.Request.Context())
	if err != nil {
		h.auditLog(c, "notification.add_platform", "result=error platform_id=%s platform_type=%s err=%v", platform.ID, platform.Type, err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.add_platform", "result=success platform_id=%s platform_type=%s platform_name=%q", platform.ID, platform.Type, platform.Name)
	response.Success(c, items)
}

// UpdatePlatform 更新通知平台
// PUT /api/v1/admin/webhook/platforms/:id
func (h *NotificationHandler) UpdatePlatform(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.BadRequest(c, "Invalid platform id")
		return
	}

	var req UpdatePlatformRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	platform := h.normalizePlatform(reqToPlatform(id, AddPlatformRequest(req)))
	if err := platform.Validate(); err != nil {
		response.BadRequest(c, "Invalid platform: "+err.Error())
		return
	}

	if err := h.configSvc.UpdatePlatform(c.Request.Context(), id, platform); err != nil {
		h.auditLog(c, "notification.update_platform", "result=error platform_id=%s platform_type=%s err=%v", id, platform.Type, err)
		response.ErrorFrom(c, err)
		return
	}

	items, err := h.getMaskedPlatforms(c.Request.Context())
	if err != nil {
		h.auditLog(c, "notification.update_platform", "result=error platform_id=%s platform_type=%s err=%v", id, platform.Type, err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.update_platform", "result=success platform_id=%s platform_type=%s platform_name=%q", id, platform.Type, platform.Name)
	response.Success(c, items)
}

// DeletePlatform 删除通知平台
// DELETE /api/v1/admin/webhook/platforms/:id
func (h *NotificationHandler) DeletePlatform(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.BadRequest(c, "Invalid platform id")
		return
	}

	if err := h.configSvc.DeletePlatform(c.Request.Context(), id); err != nil {
		h.auditLog(c, "notification.delete_platform", "result=error platform_id=%s err=%v", id, err)
		response.ErrorFrom(c, err)
		return
	}

	items, err := h.getMaskedPlatforms(c.Request.Context())
	if err != nil {
		h.auditLog(c, "notification.delete_platform", "result=error platform_id=%s err=%v", id, err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.delete_platform", "result=success platform_id=%s", id)
	response.Success(c, items)
}

// TogglePlatform 切换平台启用状态
// POST /api/v1/admin/webhook/platforms/:id/toggle
func (h *NotificationHandler) TogglePlatform(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.BadRequest(c, "Invalid platform id")
		return
	}

	if err := h.configSvc.TogglePlatform(c.Request.Context(), id); err != nil {
		h.auditLog(c, "notification.toggle_platform", "result=error platform_id=%s err=%v", id, err)
		response.ErrorFrom(c, err)
		return
	}

	items, err := h.getMaskedPlatforms(c.Request.Context())
	if err != nil {
		h.auditLog(c, "notification.toggle_platform", "result=error platform_id=%s err=%v", id, err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.toggle_platform", "result=success platform_id=%s", id)
	response.Success(c, items)
}

// TestPlatform 测试单个平台连通性
// POST /api/v1/admin/webhook/platforms/:id/test
func (h *NotificationHandler) TestPlatform(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.BadRequest(c, "Invalid platform id")
		return
	}

	if err := h.notificationSvc.TestPlatform(c.Request.Context(), id); err != nil {
		if errors.Is(err, service.ErrPlatformNotFound) {
			h.auditLog(c, "notification.test_platform", "result=not_found platform_id=%s", id)
			response.NotFound(c, "platform not found")
			return
		}
		h.auditLog(c, "notification.test_platform", "result=error platform_id=%s err=%v", id, err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.test_platform", "result=success platform_id=%s", id)
	response.Success(c, gin.H{"message": "Test notification sent"})
}

// ValidateConfig 校验配置合法性
// POST /api/v1/admin/webhook/test
func (h *NotificationHandler) ValidateConfig(c *gin.Context) {
	var req WebhookConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	current, err := h.configSvc.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if isEmptyWebhookConfigRequest(req) {
		response.BadRequest(c, "Invalid request")
		return
	}

	cfg := h.buildWebhookConfig(req, current)

	if err := h.configSvc.ValidateConfig(cfg); err != nil {
		response.BadRequest(c, "Invalid config: "+err.Error())
		return
	}

	response.Success(c, gin.H{"valid": true})
}

// SendTestNotification 发送测试通知
// POST /api/v1/admin/webhook/test-notification
func (h *NotificationHandler) SendTestNotification(c *gin.Context) {
	var req TestNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	msg := model.NotificationMessage{
		Type:     req.Type,
		Title:    strings.TrimSpace(req.Title),
		Content:  strings.TrimSpace(req.Content),
		Severity: strings.TrimSpace(req.Severity),
		Metadata: req.Metadata,
	}

	if err := h.notificationSvc.TestNotification(c.Request.Context(), msg); err != nil {
		h.auditLog(c, "notification.send_test", "result=error type=%s severity=%q err=%v", msg.Type, msg.Severity, err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.send_test", "result=success type=%s severity=%q title_len=%d content_len=%d", msg.Type, msg.Severity, len(msg.Title), len(msg.Content))
	response.Success(c, gin.H{"message": "Test notification sent"})
}

// SendNotification 发送通知（供外部运维系统调用）。
// 权限模型：该端点位于 /api/v1/admin 下，必须通过 AdminAuthMiddleware 鉴权：
//   - x-api-key: <admin-api-key>（管理员 API Key），或
//   - Authorization: Bearer <admin-jwt>（需要管理员角色）。
//
// 建议仅在内网/受控环境暴露，并配合审计日志追踪调用。
// POST /api/v1/admin/webhook/send
func (h *NotificationHandler) SendNotification(c *gin.Context) {
	var req SendNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		response.BadRequest(c, "title is required")
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		response.BadRequest(c, "content is required")
		return
	}

	severity := strings.TrimSpace(req.Severity)
	if severity == "" {
		severity = "info"
	}

	msg := model.NotificationMessage{
		Type:     req.Type,
		Title:    title,
		Content:  content,
		Severity: severity,
		Metadata: req.Metadata,
	}

	if err := h.notificationSvc.Send(c.Request.Context(), msg); err != nil {
		h.auditLog(c, "notification.send", "result=error type=%s severity=%q err=%v", msg.Type, msg.Severity, err)
		response.ErrorFrom(c, err)
		return
	}

	h.auditLog(c, "notification.send", "result=success type=%s severity=%q title_len=%d content_len=%d", msg.Type, msg.Severity, len(msg.Title), len(msg.Content))
	response.Success(c, gin.H{"message": "Notification sent"})
}

func (h *NotificationHandler) auditLog(c *gin.Context, action string, format string, args ...any) {
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	role, _ := middleware.GetUserRoleFromContext(c)
	authMethod := strings.TrimSpace(c.GetString("auth_method"))
	clientIP := strings.TrimSpace(c.ClientIP())
	log.Printf("AUDIT: action=%s user_id=%d role=%s auth_method=%s client_ip=%s "+format,
		append([]any{action, subject.UserID, role, authMethod, clientIP}, args...)...,
	)
}

func isEmptyWebhookConfigRequest(req WebhookConfigRequest) bool {
	return req.Enabled == nil &&
		req.RetrySettings == nil &&
		len(req.Platforms) == 0 &&
		len(req.NotificationTypes) == 0
}

func (h *NotificationHandler) buildWebhookConfig(req WebhookConfigRequest, current *model.WebhookConfig) *model.WebhookConfig {
	retrySettings := model.NewWebhookConfig().RetrySettings
	if current != nil {
		retrySettings = current.RetrySettings
	}
	if req.RetrySettings != nil {
		retrySettings = *req.RetrySettings
	}

	enabled := false
	if current != nil {
		enabled = current.Enabled
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	cfg := &model.WebhookConfig{
		Enabled:           enabled,
		NotificationTypes: cloneNotificationTypes(req.NotificationTypes),
		RetrySettings:     retrySettings,
	}

	// Preserve existing platforms if not provided in request
	// This prevents accidental overwrite when only updating other settings
	if len(req.Platforms) > 0 {
		cfg.Platforms = clonePlatforms(req.Platforms)
		for i := range cfg.Platforms {
			cfg.Platforms[i] = h.normalizePlatform(cfg.Platforms[i])
		}
	} else if current != nil {
		cfg.Platforms = current.Platforms
	}

	if current != nil {
		cfg.CreatedAt = current.CreatedAt
		cfg.UpdatedAt = current.UpdatedAt
	}
	return cfg
}

func (h *NotificationHandler) normalizePlatform(p model.PlatformConfig) model.PlatformConfig {
	p.ID = strings.TrimSpace(p.ID)
	p.Type = model.PlatformType(strings.TrimSpace(string(p.Type)))
	p.Name = strings.TrimSpace(p.Name)
	p.URL = strings.TrimSpace(p.URL)
	p.Secret = strings.TrimSpace(p.Secret)
	p.Token = strings.TrimSpace(p.Token)
	p.ChannelID = strings.TrimSpace(p.ChannelID)
	p.BotToken = strings.TrimSpace(p.BotToken)
	p.ChatID = strings.TrimSpace(p.ChatID)
	p.APIBaseURL = strings.TrimSpace(p.APIBaseURL)
	p.ProxyURL = strings.TrimSpace(p.ProxyURL)
	p.SMTPHost = strings.TrimSpace(p.SMTPHost)
	p.SMTPUser = strings.TrimSpace(p.SMTPUser)
	p.SMTPPass = strings.TrimSpace(p.SMTPPass)
	p.SMTPFrom = strings.TrimSpace(p.SMTPFrom)
	p.SMTPTo = strings.TrimSpace(p.SMTPTo)
	return p
}

func reqToPlatform(id string, req AddPlatformRequest) model.PlatformConfig {
	return model.PlatformConfig{
		ID:        id,
		Type:      req.Type,
		Name:      req.Name,
		Enabled:   req.Enabled,
		URL:       req.URL,
		Secret:    req.Secret,
		Token:     req.Token,
		ChannelID: req.ChannelID,

		EnableSign: req.EnableSign,

		BotToken:   req.BotToken,
		ChatID:     req.ChatID,
		APIBaseURL: req.APIBaseURL,
		ProxyURL:   req.ProxyURL,

		SMTPHost:      req.SMTPHost,
		SMTPPort:      req.SMTPPort,
		SMTPUser:      req.SMTPUser,
		SMTPPass:      req.SMTPPass,
		SMTPFrom:      req.SMTPFrom,
		SMTPTo:        req.SMTPTo,
		SMTPSecure:    req.SMTPSecure,
		SMTPIgnoreTLS: req.SMTPIgnoreTLS,
	}
}

func cloneNotificationTypes(src map[model.NotificationType]bool) map[model.NotificationType]bool {
	if len(src) == 0 {
		return map[model.NotificationType]bool{}
	}
	dst := make(map[model.NotificationType]bool, len(src))
	maps.Copy(dst, src)
	return dst
}

func clonePlatforms(src []model.PlatformConfig) []model.PlatformConfig {
	if len(src) == 0 {
		return []model.PlatformConfig{}
	}
	dst := make([]model.PlatformConfig, len(src))
	copy(dst, src)
	return dst
}

func maskPlatforms(src []model.PlatformConfig) []model.PlatformConfig {
	if len(src) == 0 {
		return []model.PlatformConfig{}
	}
	dst := make([]model.PlatformConfig, len(src))
	for i := range src {
		dst[i] = src[i].MaskSecrets()
	}
	return dst
}

func maskWebhookConfig(cfg *model.WebhookConfig) *model.WebhookConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Platforms = maskPlatforms(cfg.Platforms)
	out.NotificationTypes = nil
	return &out
}
