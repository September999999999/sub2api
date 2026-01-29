package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/stretchr/testify/require"
)

func TestWebhookConfigService_GetConfig_DefaultOnNotFound(t *testing.T) {
	repo := &mockWebhookRepo{getErr: ErrSettingNotFound}
	svc := NewWebhookConfigService(repo)

	cfg, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.NotEmpty(t, cfg.CreatedAt)
	require.Empty(t, cfg.NotificationTypes)
}

func TestWebhookConfigService_PlatformCRUD(t *testing.T) {
	cfg := model.NewWebhookConfig()
	cfg.Enabled = true
	repo := &mockWebhookRepo{cfg: cfg}
	svc := NewWebhookConfigService(repo)
	ctx := context.Background()

	platform := model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformLark,
		Name:    "Lark",
		Enabled: true,
		URL:     "https://example.com/hook",
	}

	require.NoError(t, svc.AddPlatform(ctx, platform))
	require.Len(t, repo.cfg.Platforms, 1)
	require.Equal(t, 1, repo.saveCalls)

	update := model.PlatformConfig{
		Type:    model.PlatformLark,
		Name:    "Updated",
		Enabled: false,
		URL:     "https://example.com/hook2",
	}
	require.NoError(t, svc.UpdatePlatform(ctx, "p1", update))
	require.Equal(t, "Updated", repo.cfg.Platforms[0].Name)

	require.NoError(t, svc.TogglePlatform(ctx, "p1"))
	require.True(t, repo.cfg.Platforms[0].Enabled)

	require.NoError(t, svc.DeletePlatform(ctx, "p1"))
	require.Empty(t, repo.cfg.Platforms)
	require.Equal(t, 4, repo.saveCalls, "add+update+toggle+delete should call save")
}

func TestWebhookConfigService_UpdatePlatform_NotFound(t *testing.T) {
	repo := &mockWebhookRepo{cfg: model.NewWebhookConfig()}
	svc := NewWebhookConfigService(repo)

	err := svc.UpdatePlatform(context.Background(), "missing", model.PlatformConfig{Type: model.PlatformLark, Name: "x", URL: "https://example.com"})
	require.Error(t, err)
}

func TestWebhookConfigService_ValidateConfig(t *testing.T) {
	svc := NewWebhookConfigService(&mockWebhookRepo{})

	cfg := model.NewWebhookConfig()
	cfg.Platforms = []model.PlatformConfig{
		{ID: "dup", Type: model.PlatformLark, Name: "a", Enabled: true, URL: "https://example.com/a"},
		{ID: "dup", Type: model.PlatformLark, Name: "b", Enabled: true, URL: "https://example.com/b"},
	}
	err := svc.ValidateConfig(cfg)
	require.ErrorContains(t, err, "duplicate platform id")

	cfg.Platforms = []model.PlatformConfig{
		{ID: "ok", Type: model.PlatformSlack, Name: "Slack", Enabled: true, Token: "token", ChannelID: "chan"},
	}
	require.NoError(t, svc.ValidateConfig(cfg))

	require.Error(t, svc.UpdateConfig(context.Background(), nil))
	repo := &mockWebhookRepo{saveErr: errors.New("boom")}
	svc = NewWebhookConfigService(repo)
	require.ErrorContains(t, svc.UpdateConfig(context.Background(), model.NewWebhookConfig()), "boom")
}
