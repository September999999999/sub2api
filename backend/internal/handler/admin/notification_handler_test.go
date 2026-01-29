package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminpkg "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/nikoksr/notify"
	"github.com/stretchr/testify/require"
)

type fakeWebhookRepo struct {
	cfg            *model.WebhookConfig
	getErr         error
	saveErr        error
	getErrSequence []error
	getCalls       int
}

func (r *fakeWebhookRepo) Get(ctx context.Context) (*model.WebhookConfig, error) {
	if len(r.getErrSequence) > 0 {
		if r.getCalls < len(r.getErrSequence) {
			err := r.getErrSequence[r.getCalls]
			r.getCalls++
			if err != nil {
				return nil, err
			}
		}
	}
	if r.getErr != nil {
		return nil, r.getErr
	}
	return cloneWebhookConfig(r.cfg), nil
}

func (r *fakeWebhookRepo) Save(ctx context.Context, cfg *model.WebhookConfig) error {
	if r.saveErr != nil {
		return r.saveErr
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if cfg.CreatedAt == "" {
		cfg.CreatedAt = now
	}
	cfg.UpdatedAt = now
	if err := cfg.Validate(); err != nil {
		return err
	}

	r.cfg = cloneWebhookConfig(cfg)
	return nil
}

func (r *fakeWebhookRepo) Delete(ctx context.Context) error {
	r.cfg = nil
	return nil
}

type recorderNotifier struct {
	id       string
	calls    int
	messages []*notify.Message
}

func (n *recorderNotifier) Send(ctx context.Context, msg *notify.Message) error {
	n.calls++
	n.messages = append(n.messages, msg)
	return nil
}

type failingNotifier struct {
	count *int
	last  **notify.Message
}

func (n *failingNotifier) Send(ctx context.Context, msg *notify.Message) error {
	(*n.count)++
	*n.last = msg
	return errors.New("send fail")
}

type notificationTestEnv struct {
	router          *gin.Engine
	repo            *fakeWebhookRepo
	configSvc       *service.WebhookConfigService
	notificationSvc *service.NotificationService
	notifiers       map[string]*recorderNotifier
}

func newNotificationTestEnv(t *testing.T) *notificationTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	repo := &fakeWebhookRepo{cfg: model.NewWebhookConfig()}
	configSvc := service.NewWebhookConfigService(repo)
	notifiers := map[string]*recorderNotifier{}

	notificationSvc := service.NewNotificationService(configSvc)
	notificationSvc.UsePlatformFactory(func(p model.PlatformConfig) (notify.Notifier, error) {
		n := &recorderNotifier{id: p.ID}
		notifiers[p.ID] = n
		return n, nil
	})

	handler := adminpkg.NewNotificationHandler(configSvc, notificationSvc)

	router := gin.New()
	adminGroup := router.Group("/api/v1/admin")
	adminGroup.Use(testAdminAuth())
	routes.RegisterNotificationRoutes(adminGroup, handler)

	return &notificationTestEnv{
		router:          router,
		repo:            repo,
		configSvc:       configSvc,
		notificationSvc: notificationSvc,
		notifiers:       notifiers,
	}
}

func testAdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") != "true" {
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "admin required")
			return
		}
		c.Next()
	}
}

type apiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func performRequest(t *testing.T, env *notificationTestEnv, method, path string, body any, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()

	var buf *bytes.Buffer
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		buf = bytes.NewBuffer(data)
	} else {
		buf = bytes.NewBuffer(nil)
	}

	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	if withAuth {
		req.Header.Set("X-Test-Admin", "true")
	}

	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	return w
}

func decodeAPIResponse(t *testing.T, w *httptest.ResponseRecorder) apiResponse {
	t.Helper()

	var resp apiResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func cloneWebhookConfig(cfg *model.WebhookConfig) *model.WebhookConfig {
	if cfg == nil {
		return nil
	}
	data, _ := json.Marshal(cfg)
	var out model.WebhookConfig
	_ = json.Unmarshal(data, &out)
	return &out
}

func boolPtr(v bool) *bool {
	return &v
}

func TestNotificationHandler_GetConfigAndPlatforms(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
		ID:       "p1",
		Type:     model.PlatformDingtalk,
		Name:     "Lark",
		Enabled:  true,
		URL:      "https://example.com",
		Secret:   "raw-secret",
		Token:    "raw-token",
		BotToken: "raw-bot-token",
		SMTPPass: "raw-smtp-pass",
	}))

	resp := performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/config", nil, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var payload struct {
		Config model.WebhookConfig `json:"config"`
		State  struct {
			Status string `json:"status"`
		} `json:"state"`
	}
	require.NoError(t, json.Unmarshal(apiResp.Data, &payload))
	require.Equal(t, "ok", payload.State.Status)
	require.Len(t, payload.Config.Platforms, 1)
	require.Equal(t, "******", payload.Config.Platforms[0].Secret)
	require.Equal(t, "******", payload.Config.Platforms[0].Token)
	require.Equal(t, "******", payload.Config.Platforms[0].BotToken)
	require.Equal(t, "******", payload.Config.Platforms[0].SMTPPass)

	resp = performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/platforms", nil, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp = decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var platformsPayload struct {
		Items []model.PlatformConfig `json:"items"`
		State struct {
			Status string `json:"status"`
		} `json:"state"`
	}
	require.NoError(t, json.Unmarshal(apiResp.Data, &platformsPayload))
	require.Equal(t, "ok", platformsPayload.State.Status)
	require.Len(t, platformsPayload.Items, 1)
	require.Equal(t, "******", platformsPayload.Items[0].Secret)
	require.Equal(t, "******", platformsPayload.Items[0].Token)
	require.Equal(t, "******", platformsPayload.Items[0].BotToken)
	require.Equal(t, "******", platformsPayload.Items[0].SMTPPass)
}

func TestNotificationHandler_GetConfig_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	env.repo.getErr = errors.New("boom")

	resp := performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/config", nil, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var payload struct {
		Config model.WebhookConfig `json:"config"`
		State  struct {
			Status string `json:"status"`
			Issues []struct {
				Code string `json:"code"`
			} `json:"issues"`
		} `json:"state"`
	}
	require.NoError(t, json.Unmarshal(apiResp.Data, &payload))
	require.Equal(t, "degraded", payload.State.Status)
	require.NotEmpty(t, payload.State.Issues)
	require.Empty(t, payload.Config.Platforms)
}

func TestNotificationHandler_GetPlatforms_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	env.repo.getErr = errors.New("load failed")

	resp := performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/platforms", nil, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var payload struct {
		Items []model.PlatformConfig `json:"items"`
		State struct {
			Status string `json:"status"`
			Issues []struct {
				Code string `json:"code"`
			} `json:"issues"`
		} `json:"state"`
	}
	require.NoError(t, json.Unmarshal(apiResp.Data, &payload))
	require.Equal(t, "degraded", payload.State.Status)
	require.NotEmpty(t, payload.State.Issues)
	require.Empty(t, payload.Items)
}

func TestNotificationHandler_UpdateConfig(t *testing.T) {
	env := newNotificationTestEnv(t)

	req := adminpkg.WebhookConfigRequest{
		Enabled: boolPtr(true),
		RetrySettings: &model.RetrySettings{
			MaxRetries: 2,
			RetryDelay: 500,
			Timeout:    800,
		},
		Platforms: []model.PlatformConfig{
			{ID: "p1", Type: model.PlatformDingtalk, Name: "Lark", Enabled: true, URL: "https://example.com"},
		},
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", req, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var cfg model.WebhookConfig
	require.NoError(t, json.Unmarshal(apiResp.Data, &cfg))
	require.True(t, cfg.Enabled)
	require.Equal(t, 2, cfg.RetrySettings.MaxRetries)
	require.Len(t, cfg.Platforms, 1)
	require.True(t, env.repo.cfg.Enabled)
	require.Equal(t, "p1", env.repo.cfg.Platforms[0].ID)
}

func TestNotificationHandler_UpdateConfig_MissingRetrySettings_InheritsCurrent(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	current, err := env.configSvc.GetConfig(ctx)
	require.NoError(t, err)
	current.RetrySettings = model.RetrySettings{
		MaxRetries: 9,
		RetryDelay: 321,
		Timeout:    1234,
	}
	require.NoError(t, env.configSvc.UpdateConfig(ctx, current))

	req := adminpkg.WebhookConfigRequest{
		Enabled: boolPtr(true),
		// RetrySettings omitted intentionally (should inherit from current config).
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", req, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var cfg model.WebhookConfig
	require.NoError(t, json.Unmarshal(apiResp.Data, &cfg))
	require.Equal(t, current.RetrySettings, cfg.RetrySettings)
	require.Equal(t, current.RetrySettings, env.repo.cfg.RetrySettings)
}

func TestNotificationHandler_UpdateConfig_IgnoresClientTimestamps(t *testing.T) {
	env := newNotificationTestEnv(t)

	originalCreatedAt := env.repo.cfg.CreatedAt
	forgedTimestamp := "2000-01-01T00:00:00Z"

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", map[string]any{
		"enabled":   true,
		"createdAt": forgedTimestamp,
		"updatedAt": forgedTimestamp,
	}, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var cfg model.WebhookConfig
	require.NoError(t, json.Unmarshal(apiResp.Data, &cfg))
	require.Equal(t, originalCreatedAt, cfg.CreatedAt)
	require.Equal(t, originalCreatedAt, env.repo.cfg.CreatedAt)
	require.NotEqual(t, forgedTimestamp, cfg.UpdatedAt)
}

func TestNotificationHandler_UpdateConfig_NormalizesPlatforms(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", map[string]any{
		"enabled": true,
		"platforms": []map[string]any{
			{
				"id":      "  p1  ",
				"type":    " dingtalk ",
				"name":    "  Lark  ",
				"enabled": true,
				"url":     "https://example.com",
			},
		},
	}, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	require.NotNil(t, env.repo.cfg)
	require.Len(t, env.repo.cfg.Platforms, 1)
	require.Equal(t, "p1", env.repo.cfg.Platforms[0].ID)
	require.Equal(t, model.PlatformDingtalk, env.repo.cfg.Platforms[0].Type)
	require.Equal(t, "Lark", env.repo.cfg.Platforms[0].Name)
}

func TestNotificationHandler_UpdateConfig_InvalidRequest(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", gin.H{}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	var errResp apiResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errResp))
	require.Equal(t, http.StatusBadRequest, errResp.Code)
	require.Contains(t, errResp.Message, "Invalid request")
}

func TestNotificationHandler_ModelValidate_TrimsSpace(t *testing.T) {
	err := (model.PlatformConfig{
		ID:   "   ",
		Name: "ok",
		Type: model.PlatformDingtalk,
	}).Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "id is required")

	err = (model.PlatformConfig{
		ID:   "p1",
		Name: "   ",
		Type: model.PlatformDingtalk,
		URL:  "https://example.com",
	}).Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "name is required")
}

func TestNotificationHandler_UpdateConfig_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	env.repo.saveErr = errors.New("persistence failed")

	req := adminpkg.WebhookConfigRequest{
		Enabled: boolPtr(true),
		RetrySettings: &model.RetrySettings{
			MaxRetries: 1,
			RetryDelay: 100,
			Timeout:    1000,
		},
		Platforms: []model.PlatformConfig{
			{ID: "p1", Type: model.PlatformDingtalk, Name: "Lark", Enabled: true, URL: "https://example.com"},
		},
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", req, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_UpdateConfig_BindError(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", map[string]any{
		"enabled":       "oops",
		"retrySettings": "bad",
		"platforms":     "bad",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "Invalid request")
}

func TestNotificationHandler_UpdateConfig_ValidateError(t *testing.T) {
	env := newNotificationTestEnv(t)

	req := adminpkg.WebhookConfigRequest{
		Enabled: boolPtr(true),
		RetrySettings: &model.RetrySettings{
			MaxRetries: 1,
			RetryDelay: 100,
			Timeout:    1000,
		},
		Platforms: []model.PlatformConfig{
			{ID: "dup", Type: model.PlatformDingtalk, Name: "Lark", Enabled: true, URL: "https://example.com"},
			{ID: "dup", Type: model.PlatformDingtalk, Name: "Lark2", Enabled: true, URL: "https://example.com"},
		},
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", req, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "duplicate platform id")
}

func TestNotificationHandler_UpdateConfig_GetConfigError(t *testing.T) {
	env := newNotificationTestEnv(t)
	env.repo.getErr = errors.New("boom")

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/config", adminpkg.WebhookConfigRequest{
		Enabled: boolPtr(true),
	}, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_PlatformLifecycle(t *testing.T) {
	env := newNotificationTestEnv(t)

	addReq := adminpkg.AddPlatformRequest{
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}
	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", addReq, true)
	require.Equal(t, http.StatusOK, resp.Code)
	require.Len(t, env.repo.cfg.Platforms, 1)

	// Get the auto-generated platform ID
	platformID := env.repo.cfg.Platforms[0].ID
	require.NotEmpty(t, platformID)

	updateReq := adminpkg.UpdatePlatformRequest{
		Type:    model.PlatformDiscord,
		Name:    "Discord",
		Enabled: true,
		URL:     "https://discord.com/webhook/1234",
	}
	resp = performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/"+platformID, updateReq, true)
	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, model.PlatformDiscord, env.repo.cfg.Platforms[0].Type)
	require.Equal(t, "Discord", env.repo.cfg.Platforms[0].Name)

	resp = performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/"+platformID+"/toggle", nil, true)
	require.Equal(t, http.StatusOK, resp.Code)
	require.False(t, env.repo.cfg.Platforms[0].Enabled)

	resp = performRequest(t, env, http.MethodDelete, "/api/v1/admin/webhook/platforms/"+platformID, nil, true)
	require.Equal(t, http.StatusOK, resp.Code)
	require.Empty(t, env.repo.cfg.Platforms)
}

func TestNotificationHandler_AddPlatform_Validation(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", gin.H{
		"type": "unknown",
		"name": "",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestNotificationHandler_AddPlatform_TypeOneof_AllowsNewPlatforms(t *testing.T) {
	testCases := []struct {
		name              string
		req               any
		wantHTTPStatus    int
		wantMsgContains   string
		wantMsgNotContain string
	}{
		{
			name: "dingtalk",
			req: adminpkg.AddPlatformRequest{
				Type:    model.PlatformDingtalk,
				Name:    "DingTalk",
				Enabled: true,
				URL:     "https://example.com",
			},
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "discord",
			req: adminpkg.AddPlatformRequest{
				Type:    model.PlatformDiscord,
				Name:    "Discord",
				Enabled: true,
				URL:     "https://example.com",
			},
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "telegram",
			req: adminpkg.AddPlatformRequest{
				Type:    model.PlatformTelegram,
				Name:    "Telegram",
				Enabled: true,
			},
			wantHTTPStatus:    http.StatusBadRequest,
			wantMsgContains:   "Invalid platform",
			wantMsgNotContain: "Invalid request",
		},
		{
			name: "smtp",
			req: adminpkg.AddPlatformRequest{
				Type:    model.PlatformSMTP,
				Name:    "SMTP",
				Enabled: true,
			},
			wantHTTPStatus:    http.StatusBadRequest,
			wantMsgContains:   "Invalid platform",
			wantMsgNotContain: "Invalid request",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			env := newNotificationTestEnv(t)

			resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", tc.req, true)
			require.Equal(t, tc.wantHTTPStatus, resp.Code)

			apiResp := decodeAPIResponse(t, resp)
			switch tc.wantHTTPStatus {
			case http.StatusOK:
				require.Equal(t, 0, apiResp.Code)
			default:
				require.Equal(t, tc.wantHTTPStatus, apiResp.Code)
				if tc.wantMsgContains != "" {
					require.Contains(t, apiResp.Message, tc.wantMsgContains)
				}
				if tc.wantMsgNotContain != "" {
					require.NotContains(t, apiResp.Message, tc.wantMsgNotContain)
				}
			}
		})
	}
}

func TestNotificationHandler_AddPlatform_MissingURL(t *testing.T) {
	env := newNotificationTestEnv(t)

	req := adminpkg.AddPlatformRequest{
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
	}

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", req, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "Invalid platform")
}

func TestNotificationHandler_AddPlatform_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	env.repo.saveErr = errors.New("save failed")

	req := adminpkg.AddPlatformRequest{
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", req, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_AddPlatform_GetConfigAfterAddError(t *testing.T) {
	env := newNotificationTestEnv(t)
	env.repo.getErrSequence = []error{nil, errors.New("fetch failed")}

	req := adminpkg.AddPlatformRequest{
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", req, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_ValidateConfig(t *testing.T) {
	env := newNotificationTestEnv(t)

	invalidReq := adminpkg.WebhookConfigRequest{
		Enabled:       boolPtr(true),
		RetrySettings: &model.RetrySettings{MaxRetries: 1, RetryDelay: 100, Timeout: 1000},
		Platforms: []model.PlatformConfig{
			{ID: "p1", Type: model.PlatformDingtalk, Name: "Lark", Enabled: true},
		},
	}
	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test", invalidReq, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	validReq := invalidReq
	validReq.Platforms[0].URL = "https://example.com"
	resp = performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test", validReq, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	var result struct {
		Valid bool `json:"valid"`
	}
	require.NoError(t, json.Unmarshal(apiResp.Data, &result))
	require.True(t, result.Valid)
}

func TestNotificationHandler_ValidateConfig_EmptyTypes(t *testing.T) {
	env := newNotificationTestEnv(t)

	req := adminpkg.WebhookConfigRequest{
		Enabled: boolPtr(true),
		Platforms: []model.PlatformConfig{
			{ID: "p1", Type: model.PlatformDingtalk, Name: "Lark", Enabled: true, URL: "https://example.com"},
		},
	}

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test", req, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)
}

func TestNotificationHandler_ValidateConfig_BindError(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test", map[string]any{
		"enabled": "oops",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "Invalid request")
}

func TestNotificationHandler_ValidateConfig_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	env.repo.getErr = errors.New("db down")

	req := adminpkg.WebhookConfigRequest{
		Enabled: boolPtr(true),
	}

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test", req, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_TestPlatform(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}))

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/p1/test", nil, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)
	require.Contains(t, string(apiResp.Data), "Test notification sent")

	require.Contains(t, env.notifiers, "p1")
	require.Equal(t, 1, env.notifiers["p1"].calls)
}

func TestNotificationHandler_TestPlatform_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}))

	env.notificationSvc.UsePlatformFactory(func(model.PlatformConfig) (notify.Notifier, error) {
		return nil, errors.New("factory failed")
	})

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/p1/test", nil, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_TestPlatform_NotFound(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/p-missing/test", nil, true)
	require.Equal(t, http.StatusNotFound, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusNotFound, apiResp.Code)
	require.Equal(t, "platform not found", apiResp.Message)
}

func TestNotificationHandler_TestPlatform_InvalidID(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/%20/test", nil, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Equal(t, "Invalid platform id", apiResp.Message)
}

func TestNotificationHandler_SendTestNotification(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	cfg, err := env.configSvc.GetConfig(ctx)
	require.NoError(t, err)
	cfg.Enabled = true
	require.NoError(t, env.configSvc.UpdateConfig(ctx, cfg))

	require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}))

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test-notification", adminpkg.TestNotificationRequest{
		Type:     model.NotificationSystemError,
		Title:    "Hello",
		Content:  "World",
		Severity: "warning",
		Metadata: map[string]any{"k": "v"},
	}, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	require.Contains(t, env.notifiers, "p1")
	require.Equal(t, 1, env.notifiers["p1"].calls)
	require.NotEmpty(t, env.notifiers["p1"].messages)
}

func TestNotificationHandler_SendTestNotification_BindError(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test-notification", adminpkg.TestNotificationRequest{
		Type:     model.NotificationSystemError,
		Title:    "Hello",
		Content:  "World",
		Severity: "unknown",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "Invalid request")
}

func TestNotificationHandler_SendTestNotification_DefaultType(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test-notification", adminpkg.TestNotificationRequest{
		Title:    "  Trim Title  ",
		Content:  "  Trim Content  ",
		Severity: "warning",
	}, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)
}

func TestNotificationHandler_SendTestNotification_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	cfg, err := env.configSvc.GetConfig(ctx)
	require.NoError(t, err)
	cfg.Enabled = true
	// Disable retry to test single send failure behavior
	cfg.RetrySettings.MaxRetries = 0
	require.NoError(t, env.configSvc.UpdateConfig(ctx, cfg))

	require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}))

	var sendCount int
	var lastMsg *notify.Message

	env.notificationSvc.UsePlatformFactory(func(p model.PlatformConfig) (notify.Notifier, error) {
		return &failingNotifier{
			count: &sendCount,
			last:  &lastMsg,
		}, nil
	})

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test-notification", adminpkg.TestNotificationRequest{
		Type:    model.NotificationSystemError,
		Title:   "Hello",
		Content: "World",
	}, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
	require.Equal(t, 1, sendCount)
	require.NotNil(t, lastMsg)
}

func TestNotificationHandler_SendNotification(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	cfg, err := env.configSvc.GetConfig(ctx)
	require.NoError(t, err)
	cfg.Enabled = true
	require.NoError(t, env.configSvc.UpdateConfig(ctx, cfg))

	require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}))

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/send", adminpkg.SendNotificationRequest{
		Type:     model.NotificationSystemError,
		Title:    "Hello",
		Content:  "World",
		Severity: "warning",
		Metadata: map[string]any{"k": "v"},
	}, true)
	require.Equal(t, http.StatusOK, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, 0, apiResp.Code)

	require.Contains(t, env.notifiers, "p1")
	require.Equal(t, 1, env.notifiers["p1"].calls)
	require.NotEmpty(t, env.notifiers["p1"].messages)
}

func TestNotificationHandler_SendNotification_BindError(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/send", map[string]any{
		"type":  "systemError",
		"title": "Hello",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "Invalid request")
}

func TestNotificationHandler_SendNotification_ServiceError(t *testing.T) {
	env := newNotificationTestEnv(t)
	ctx := context.Background()

	cfg, err := env.configSvc.GetConfig(ctx)
	require.NoError(t, err)
	cfg.Enabled = true
	// Disable retry to test single send failure behavior
	cfg.RetrySettings.MaxRetries = 0
	require.NoError(t, env.configSvc.UpdateConfig(ctx, cfg))

	require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com",
	}))

	var sendCount int
	var lastMsg *notify.Message

	env.notificationSvc.UsePlatformFactory(func(p model.PlatformConfig) (notify.Notifier, error) {
		return &failingNotifier{
			count: &sendCount,
			last:  &lastMsg,
		}, nil
	})

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/send", adminpkg.SendNotificationRequest{
		Type:    model.NotificationSystemError,
		Title:   "Hello",
		Content: "World",
	}, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
	require.Equal(t, 1, sendCount)
	require.NotNil(t, lastMsg)
}

func TestNotificationHandler_SendNotification_RejectsBlankTitle(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/send", adminpkg.SendNotificationRequest{
		Type:    model.NotificationSystemError,
		Title:   "   ",
		Content: "World",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "title is required")
}

func TestNotificationHandler_SendNotification_RejectsBlankContent(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/send", adminpkg.SendNotificationRequest{
		Type:    model.NotificationSystemError,
		Title:   "Hello",
		Content: "   ",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "content is required")
}

func TestTestNotification(t *testing.T) {
	t.Run("AllowsSystemErrorType", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test-notification", adminpkg.TestNotificationRequest{
			Type:     model.NotificationSystemError,
			Title:    "Hello",
			Content:  "World",
			Severity: "warning",
		}, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)
	})

	t.Run("AllowsTestType", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/test-notification", map[string]any{
			"type":    "test",
			"title":   "Hello",
			"content": "World",
		}, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)
	})
}

func TestNotificationHandler_AdminAuthRequired(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/config", nil, false)
	require.Equal(t, http.StatusUnauthorized, resp.Code)

	var errResp middleware.ErrorResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errResp))
	require.Equal(t, "UNAUTHORIZED", errResp.Code)
}

func TestNotificationHandler_UpdatePlatform_TypeOneof_AllowsNewPlatforms(t *testing.T) {
	testCases := []struct {
		name              string
		req               adminpkg.UpdatePlatformRequest
		wantHTTPStatus    int
		wantMsgContains   string
		wantMsgNotContain string
	}{
		{
			name: "dingtalk",
			req: adminpkg.UpdatePlatformRequest{
				Type:    model.PlatformDingtalk,
				Name:    "DingTalk",
				Enabled: true,
				URL:     "https://example.com",
			},
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "discord",
			req: adminpkg.UpdatePlatformRequest{
				Type:    model.PlatformDiscord,
				Name:    "Discord",
				Enabled: true,
				URL:     "https://example.com",
			},
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "telegram",
			req: adminpkg.UpdatePlatformRequest{
				Type:    model.PlatformTelegram,
				Name:    "Telegram",
				Enabled: true,
			},
			wantHTTPStatus:    http.StatusBadRequest,
			wantMsgContains:   "Invalid platform",
			wantMsgNotContain: "Invalid request",
		},
		{
			name: "smtp",
			req: adminpkg.UpdatePlatformRequest{
				Type:    model.PlatformSMTP,
				Name:    "SMTP",
				Enabled: true,
			},
			wantHTTPStatus:    http.StatusBadRequest,
			wantMsgContains:   "Invalid platform",
			wantMsgNotContain: "Invalid request",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			env := newNotificationTestEnv(t)

			ctx := context.Background()
			require.NoError(t, env.configSvc.AddPlatform(ctx, model.PlatformConfig{
				ID:      "p1",
				Type:    model.PlatformDingtalk,
				Name:    "Lark",
				Enabled: true,
				URL:     "https://example.com",
			}))

			resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/p1", tc.req, true)
			require.Equal(t, tc.wantHTTPStatus, resp.Code)

			apiResp := decodeAPIResponse(t, resp)
			switch tc.wantHTTPStatus {
			case http.StatusOK:
				require.Equal(t, 0, apiResp.Code)
			default:
				require.Equal(t, tc.wantHTTPStatus, apiResp.Code)
				if tc.wantMsgContains != "" {
					require.Contains(t, apiResp.Message, tc.wantMsgContains)
				}
				if tc.wantMsgNotContain != "" {
					require.NotContains(t, apiResp.Message, tc.wantMsgNotContain)
				}
			}
		})
	}
}

func TestNotificationHandler_UpdatePlatform_InvalidID(t *testing.T) {
	env := newNotificationTestEnv(t)

	req := adminpkg.UpdatePlatformRequest{
		Type:    model.PlatformDingtalk,
		Name:    "Name",
		Enabled: true,
		URL:     "https://example.com",
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/%20", req, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Equal(t, "Invalid platform id", apiResp.Message)
}

func TestNotificationHandler_UpdatePlatform_NotFound(t *testing.T) {
	env := newNotificationTestEnv(t)

	req := adminpkg.UpdatePlatformRequest{
		Type:    model.PlatformDingtalk,
		Name:    "Name",
		Enabled: true,
		URL:     "https://example.com",
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/missing", req, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_UpdatePlatform_BindError(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/p1", gin.H{
		"name": "",
	}, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Contains(t, apiResp.Message, "Invalid request")
}

func TestNotificationHandler_UpdatePlatform_ValidateError(t *testing.T) {
	env := newNotificationTestEnv(t)

	// Test with unsupported platform type (slack is now legacy)
	req := adminpkg.UpdatePlatformRequest{
		Type:    model.PlatformSlack,
		Name:    "Slack",
		Enabled: true,
		// missing token/channelId should fail validation
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/p1", req, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	// Since slack is removed from oneof, it now fails at binding stage
	require.Contains(t, apiResp.Message, "Invalid request")
}

func TestNotificationHandler_UpdatePlatform_GetConfigAfterUpdateError(t *testing.T) {
	env := newNotificationTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	env.repo.cfg = &model.WebhookConfig{
		Enabled: true,
		Platforms: []model.PlatformConfig{
			{ID: "p1", Type: model.PlatformDiscord, Name: "Discord", Enabled: true, URL: "https://example.com"},
		},
		NotificationTypes: map[model.NotificationType]bool{model.NotificationSystemError: true},
		RetrySettings:     model.RetrySettings{MaxRetries: 1, RetryDelay: 1, Timeout: 1},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	env.repo.getErrSequence = []error{nil, errors.New("fetch failed")}

	req := adminpkg.UpdatePlatformRequest{
		Type:    model.PlatformDiscord,
		Name:    "Discord Updated",
		Enabled: true,
		URL:     "https://discord.com/webhook",
	}

	resp := performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/p1", req, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_DeletePlatform_InvalidID(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodDelete, "/api/v1/admin/webhook/platforms/%20", nil, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Equal(t, "Invalid platform id", apiResp.Message)
}

func TestNotificationHandler_TogglePlatform_InvalidID(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/%20/toggle", nil, true)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusBadRequest, apiResp.Code)
	require.Equal(t, "Invalid platform id", apiResp.Message)
}

func TestNotificationHandler_DeletePlatform_NotFound(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodDelete, "/api/v1/admin/webhook/platforms/missing", nil, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_TogglePlatform_NotFound(t *testing.T) {
	env := newNotificationTestEnv(t)

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/missing/toggle", nil, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_DeletePlatform_GetConfigAfterDeleteError(t *testing.T) {
	env := newNotificationTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	env.repo.cfg = &model.WebhookConfig{
		Enabled: true,
		Platforms: []model.PlatformConfig{
			{ID: "p1", Type: model.PlatformDingtalk, Name: "Lark", Enabled: true, URL: "https://example.com"},
		},
		NotificationTypes: map[model.NotificationType]bool{model.NotificationSystemError: true},
		RetrySettings:     model.RetrySettings{MaxRetries: 1, RetryDelay: 1, Timeout: 1},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	env.repo.getErrSequence = []error{nil, errors.New("fetch failed")}

	resp := performRequest(t, env, http.MethodDelete, "/api/v1/admin/webhook/platforms/p1", nil, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestNotificationHandler_TogglePlatform_GetConfigAfterToggleError(t *testing.T) {
	env := newNotificationTestEnv(t)
	now := time.Now().UTC().Format(time.RFC3339)
	env.repo.cfg = &model.WebhookConfig{
		Enabled: true,
		Platforms: []model.PlatformConfig{
			{ID: "p1", Type: model.PlatformDingtalk, Name: "Lark", Enabled: true, URL: "https://example.com"},
		},
		NotificationTypes: map[model.NotificationType]bool{model.NotificationSystemError: true},
		RetrySettings:     model.RetrySettings{MaxRetries: 1, RetryDelay: 1, Timeout: 1},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	env.repo.getErrSequence = []error{nil, errors.New("fetch failed")}

	resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms/p1/toggle", nil, true)
	require.Equal(t, http.StatusInternalServerError, resp.Code)

	apiResp := decodeAPIResponse(t, resp)
	require.Equal(t, http.StatusInternalServerError, apiResp.Code)
	require.Equal(t, "internal error", apiResp.Message)
}

func TestAddPlatform_PlatformSpecificFields_RoundTrip(t *testing.T) {
	t.Run("Telegram", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		req := adminpkg.AddPlatformRequest{
			Type:       model.PlatformTelegram,
			Name:       "Telegram",
			Enabled:    true,
			BotToken:   "  bot-token  ",
			ChatID:     "  123456  ",
			APIBaseURL: "https://api.telegram.org",
			ProxyURL:   "socks5://1.1.1.1:1080",
		}

		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", req, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var platforms []model.PlatformConfig
		require.NoError(t, json.Unmarshal(apiResp.Data, &platforms))
		require.Len(t, platforms, 1)

		got := platforms[0]
		require.Equal(t, model.PlatformTelegram, got.Type)
		require.Equal(t, "Telegram", got.Name)
		require.True(t, got.Enabled)
		require.Equal(t, "******", got.BotToken)
		require.Equal(t, "123456", got.ChatID)
		require.Equal(t, "https://api.telegram.org", got.APIBaseURL)
		require.Equal(t, "socks5://1.1.1.1:1080", got.ProxyURL)

		require.Len(t, env.repo.cfg.Platforms, 1)
		require.Equal(t, got.ID, env.repo.cfg.Platforms[0].ID)
		require.Equal(t, "bot-token", env.repo.cfg.Platforms[0].BotToken)
		require.Equal(t, got.ChatID, env.repo.cfg.Platforms[0].ChatID)
		require.Equal(t, got.APIBaseURL, env.repo.cfg.Platforms[0].APIBaseURL)
		require.Equal(t, got.ProxyURL, env.repo.cfg.Platforms[0].ProxyURL)

		// 回显（GET /platforms）
		resp = performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/platforms", nil, true)
		require.Equal(t, http.StatusOK, resp.Code)
		apiResp = decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var echoedPayload struct {
			Items []model.PlatformConfig `json:"items"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		}
		require.NoError(t, json.Unmarshal(apiResp.Data, &echoedPayload))
		require.Equal(t, "ok", echoedPayload.State.Status)
		require.Len(t, echoedPayload.Items, 1)
		require.Equal(t, got, echoedPayload.Items[0])
	})

	t.Run("SMTP", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		req := adminpkg.AddPlatformRequest{
			Type:          model.PlatformSMTP,
			Name:          "SMTP",
			Enabled:       true,
			SMTPHost:      "  smtp.example.com  ",
			SMTPPort:      587,
			SMTPUser:      "  user  ",
			SMTPPass:      "  pass  ",
			SMTPFrom:      "  from@example.com  ",
			SMTPTo:        "  to@example.com  ",
			SMTPSecure:    true,
			SMTPIgnoreTLS: true,
		}

		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", req, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var platforms []model.PlatformConfig
		require.NoError(t, json.Unmarshal(apiResp.Data, &platforms))
		require.Len(t, platforms, 1)

		got := platforms[0]
		require.Equal(t, model.PlatformSMTP, got.Type)
		require.Equal(t, "SMTP", got.Name)
		require.True(t, got.Enabled)
		require.Equal(t, "smtp.example.com", got.SMTPHost)
		require.Equal(t, 587, got.SMTPPort)
		require.Equal(t, "user", got.SMTPUser)
		require.Equal(t, "******", got.SMTPPass)
		require.Equal(t, "from@example.com", got.SMTPFrom)
		require.Equal(t, "to@example.com", got.SMTPTo)
		require.True(t, got.SMTPSecure)
		require.True(t, got.SMTPIgnoreTLS)

		require.Len(t, env.repo.cfg.Platforms, 1)
		require.Equal(t, got.SMTPHost, env.repo.cfg.Platforms[0].SMTPHost)
		require.Equal(t, got.SMTPPort, env.repo.cfg.Platforms[0].SMTPPort)
		require.Equal(t, got.SMTPUser, env.repo.cfg.Platforms[0].SMTPUser)
		require.Equal(t, "pass", env.repo.cfg.Platforms[0].SMTPPass)
		require.Equal(t, got.SMTPFrom, env.repo.cfg.Platforms[0].SMTPFrom)
		require.Equal(t, got.SMTPTo, env.repo.cfg.Platforms[0].SMTPTo)
		require.Equal(t, got.SMTPSecure, env.repo.cfg.Platforms[0].SMTPSecure)
		require.Equal(t, got.SMTPIgnoreTLS, env.repo.cfg.Platforms[0].SMTPIgnoreTLS)

		resp = performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/platforms", nil, true)
		require.Equal(t, http.StatusOK, resp.Code)
		apiResp = decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var echoedPayload struct {
			Items []model.PlatformConfig `json:"items"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		}
		require.NoError(t, json.Unmarshal(apiResp.Data, &echoedPayload))
		require.Equal(t, "ok", echoedPayload.State.Status)
		require.Len(t, echoedPayload.Items, 1)
		require.Equal(t, got, echoedPayload.Items[0])
	})

	t.Run("DingtalkEnableSign", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		req := adminpkg.AddPlatformRequest{
			Type:       model.PlatformDingtalk,
			Name:       "Dingtalk",
			Enabled:    true,
			URL:        "https://example.com",
			EnableSign: true,
			Secret:     "  sign-secret  ",
		}

		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", req, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var platforms []model.PlatformConfig
		require.NoError(t, json.Unmarshal(apiResp.Data, &platforms))
		require.Len(t, platforms, 1)

		got := platforms[0]
		require.Equal(t, model.PlatformDingtalk, got.Type)
		require.Equal(t, "Dingtalk", got.Name)
		require.True(t, got.Enabled)
		require.True(t, got.EnableSign)
		require.Equal(t, "******", got.Secret)
		require.Len(t, env.repo.cfg.Platforms, 1)
		require.Equal(t, "sign-secret", env.repo.cfg.Platforms[0].Secret)
	})
}

func TestAddPlatform_UpdatePlatform_PlatformSpecificFields_RoundTrip(t *testing.T) {
	t.Run("Telegram", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		addReq := adminpkg.AddPlatformRequest{
			Type:       model.PlatformTelegram,
			Name:       "Telegram",
			Enabled:    true,
			BotToken:   "bot-token",
			ChatID:     "123",
			APIBaseURL: "https://api.telegram.org",
			ProxyURL:   "http://1.1.1.1:8080",
		}
		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", addReq, true)
		require.Equal(t, http.StatusOK, resp.Code)
		require.Len(t, env.repo.cfg.Platforms, 1)

		platformID := env.repo.cfg.Platforms[0].ID
		require.NotEmpty(t, platformID)

		updateReq := adminpkg.UpdatePlatformRequest{
			Type:       model.PlatformTelegram,
			Name:       "Telegram Updated",
			Enabled:    false,
			BotToken:   "bot-token-updated",
			ChatID:     "456",
			APIBaseURL: "https://api.telegram.org",
			ProxyURL:   "socks5://1.1.1.1:1080",
		}
		resp = performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/"+platformID, updateReq, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var platforms []model.PlatformConfig
		require.NoError(t, json.Unmarshal(apiResp.Data, &platforms))
		require.Len(t, platforms, 1)

		got := platforms[0]
		require.Equal(t, platformID, got.ID)
		require.Equal(t, model.PlatformTelegram, got.Type)
		require.Equal(t, "Telegram Updated", got.Name)
		require.False(t, got.Enabled)
		require.Equal(t, "******", got.BotToken)
		require.Equal(t, "456", got.ChatID)
		require.Equal(t, "https://api.telegram.org", got.APIBaseURL)
		require.Equal(t, "socks5://1.1.1.1:1080", got.ProxyURL)
		require.Equal(t, "bot-token-updated", env.repo.cfg.Platforms[0].BotToken)

		resp = performRequest(t, env, http.MethodGet, "/api/v1/admin/webhook/platforms", nil, true)
		require.Equal(t, http.StatusOK, resp.Code)
		apiResp = decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var echoedPayload struct {
			Items []model.PlatformConfig `json:"items"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		}
		require.NoError(t, json.Unmarshal(apiResp.Data, &echoedPayload))
		require.Equal(t, "ok", echoedPayload.State.Status)
		require.Len(t, echoedPayload.Items, 1)
		require.Equal(t, got, echoedPayload.Items[0])
	})

	t.Run("SMTP", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		addReq := adminpkg.AddPlatformRequest{
			Type:     model.PlatformSMTP,
			Name:     "SMTP",
			Enabled:  true,
			SMTPHost: "smtp.example.com",
			SMTPPort: 587,
			SMTPUser: "user",
			SMTPPass: "pass",
			SMTPTo:   "to@example.com",
		}
		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", addReq, true)
		require.Equal(t, http.StatusOK, resp.Code)
		require.Len(t, env.repo.cfg.Platforms, 1)

		platformID := env.repo.cfg.Platforms[0].ID
		require.NotEmpty(t, platformID)

		updateReq := adminpkg.UpdatePlatformRequest{
			Type:          model.PlatformSMTP,
			Name:          "SMTP Updated",
			Enabled:       true,
			SMTPHost:      "smtp2.example.com",
			SMTPPort:      465,
			SMTPUser:      "user2",
			SMTPPass:      "pass2",
			SMTPFrom:      "from@example.com",
			SMTPTo:        "to2@example.com",
			SMTPSecure:    true,
			SMTPIgnoreTLS: true,
		}
		resp = performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/"+platformID, updateReq, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var platforms []model.PlatformConfig
		require.NoError(t, json.Unmarshal(apiResp.Data, &platforms))
		require.Len(t, platforms, 1)

		got := platforms[0]
		require.Equal(t, platformID, got.ID)
		require.Equal(t, model.PlatformSMTP, got.Type)
		require.Equal(t, "SMTP Updated", got.Name)
		require.Equal(t, "smtp2.example.com", got.SMTPHost)
		require.Equal(t, 465, got.SMTPPort)
		require.Equal(t, "user2", got.SMTPUser)
		require.Equal(t, "******", got.SMTPPass)
		require.Equal(t, "from@example.com", got.SMTPFrom)
		require.Equal(t, "to2@example.com", got.SMTPTo)
		require.True(t, got.SMTPSecure)
		require.True(t, got.SMTPIgnoreTLS)
		require.Equal(t, "pass2", env.repo.cfg.Platforms[0].SMTPPass)
	})

	t.Run("DingtalkEnableSign", func(t *testing.T) {
		env := newNotificationTestEnv(t)

		addReq := adminpkg.AddPlatformRequest{
			Type:       model.PlatformDingtalk,
			Name:       "Dingtalk",
			Enabled:    true,
			URL:        "https://example.com",
			EnableSign: true,
			Secret:     "secret1",
		}
		resp := performRequest(t, env, http.MethodPost, "/api/v1/admin/webhook/platforms", addReq, true)
		require.Equal(t, http.StatusOK, resp.Code)
		require.Len(t, env.repo.cfg.Platforms, 1)

		platformID := env.repo.cfg.Platforms[0].ID
		require.NotEmpty(t, platformID)

		updateReq := adminpkg.UpdatePlatformRequest{
			Type:       model.PlatformDingtalk,
			Name:       "Dingtalk Updated",
			Enabled:    true,
			URL:        "https://example.com/updated",
			EnableSign: true,
			Secret:     "secret2",
		}
		resp = performRequest(t, env, http.MethodPut, "/api/v1/admin/webhook/platforms/"+platformID, updateReq, true)
		require.Equal(t, http.StatusOK, resp.Code)

		apiResp := decodeAPIResponse(t, resp)
		require.Equal(t, 0, apiResp.Code)

		var platforms []model.PlatformConfig
		require.NoError(t, json.Unmarshal(apiResp.Data, &platforms))
		require.Len(t, platforms, 1)

		got := platforms[0]
		require.Equal(t, platformID, got.ID)
		require.Equal(t, model.PlatformDingtalk, got.Type)
		require.Equal(t, "Dingtalk Updated", got.Name)
		require.Equal(t, "https://example.com/updated", got.URL)
		require.True(t, got.EnableSign)
		require.Equal(t, "******", got.Secret)
		require.Equal(t, "secret2", env.repo.cfg.Platforms[0].Secret)
	})
}

func TestAddPlatform_NotificationHandlerSuite(t *testing.T) {
	t.Run("GetConfigAndPlatforms", TestNotificationHandler_GetConfigAndPlatforms)
	t.Run("GetConfigServiceError", TestNotificationHandler_GetConfig_ServiceError)
	t.Run("GetPlatformsServiceError", TestNotificationHandler_GetPlatforms_ServiceError)
	t.Run("UpdateConfig", TestNotificationHandler_UpdateConfig)
	t.Run("UpdateConfigInvalidRequest", TestNotificationHandler_UpdateConfig_InvalidRequest)
	t.Run("UpdateConfigServiceError", TestNotificationHandler_UpdateConfig_ServiceError)
	t.Run("UpdateConfigBindError", TestNotificationHandler_UpdateConfig_BindError)
	t.Run("UpdateConfigValidateError", TestNotificationHandler_UpdateConfig_ValidateError)
	t.Run("UpdateConfigGetConfigError", TestNotificationHandler_UpdateConfig_GetConfigError)

	t.Run("PlatformLifecycle", TestNotificationHandler_PlatformLifecycle)
	t.Run("AddPlatformValidation", TestNotificationHandler_AddPlatform_Validation)
	t.Run("AddPlatformTypeOneof", TestNotificationHandler_AddPlatform_TypeOneof_AllowsNewPlatforms)
	t.Run("AddPlatformMissingURL", TestNotificationHandler_AddPlatform_MissingURL)
	t.Run("AddPlatformServiceError", TestNotificationHandler_AddPlatform_ServiceError)
	t.Run("AddPlatformGetConfigAfterAddError", TestNotificationHandler_AddPlatform_GetConfigAfterAddError)

	t.Run("ValidateConfig", TestNotificationHandler_ValidateConfig)
	t.Run("ValidateConfigEmptyTypes", TestNotificationHandler_ValidateConfig_EmptyTypes)
	t.Run("ValidateConfigBindError", TestNotificationHandler_ValidateConfig_BindError)
	t.Run("ValidateConfigServiceError", TestNotificationHandler_ValidateConfig_ServiceError)

	t.Run("TestPlatform", TestNotificationHandler_TestPlatform)
	t.Run("TestPlatformNotFound", TestNotificationHandler_TestPlatform_NotFound)
	t.Run("TestPlatformInvalidID", TestNotificationHandler_TestPlatform_InvalidID)

	t.Run("SendTestNotification", TestNotificationHandler_SendTestNotification)
	t.Run("SendTestNotificationBindError", TestNotificationHandler_SendTestNotification_BindError)
	t.Run("SendTestNotificationDefaultType", TestNotificationHandler_SendTestNotification_DefaultType)
	t.Run("SendTestNotificationServiceError", TestNotificationHandler_SendTestNotification_ServiceError)
	t.Run("SendNotification", TestNotificationHandler_SendNotification)
	t.Run("SendNotificationBindError", TestNotificationHandler_SendNotification_BindError)
	t.Run("SendNotificationServiceError", TestNotificationHandler_SendNotification_ServiceError)
	t.Run("TestNotification", TestTestNotification)

	t.Run("AdminAuthRequired", TestNotificationHandler_AdminAuthRequired)

	t.Run("UpdatePlatformTypeOneof", TestNotificationHandler_UpdatePlatform_TypeOneof_AllowsNewPlatforms)
	t.Run("UpdatePlatformInvalidID", TestNotificationHandler_UpdatePlatform_InvalidID)
	t.Run("UpdatePlatformNotFound", TestNotificationHandler_UpdatePlatform_NotFound)
	t.Run("UpdatePlatformBindError", TestNotificationHandler_UpdatePlatform_BindError)
	t.Run("UpdatePlatformValidateError", TestNotificationHandler_UpdatePlatform_ValidateError)
	t.Run("UpdatePlatformGetConfigAfterUpdateError", TestNotificationHandler_UpdatePlatform_GetConfigAfterUpdateError)

	t.Run("DeletePlatformInvalidID", TestNotificationHandler_DeletePlatform_InvalidID)
	t.Run("TogglePlatformInvalidID", TestNotificationHandler_TogglePlatform_InvalidID)
	t.Run("DeletePlatformNotFound", TestNotificationHandler_DeletePlatform_NotFound)
	t.Run("TogglePlatformNotFound", TestNotificationHandler_TogglePlatform_NotFound)
	t.Run("DeletePlatformGetConfigAfterDeleteError", TestNotificationHandler_DeletePlatform_GetConfigAfterDeleteError)
	t.Run("TogglePlatformGetConfigAfterToggleError", TestNotificationHandler_TogglePlatform_GetConfigAfterToggleError)
}
