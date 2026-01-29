package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/model"
)

// WebhookConfigService 提供 webhook 配置的读写与校验能力。
type WebhookConfigService struct {
	repo WebhookConfigRepository
}

// NewWebhookConfigService 创建配置服务。
func NewWebhookConfigService(repo WebhookConfigRepository) *WebhookConfigService {
	return &WebhookConfigService{repo: repo}
}

// GetConfig 获取当前配置，缺省时返回默认配置。
func (s *WebhookConfigService) GetConfig(ctx context.Context) (*model.WebhookConfig, error) {
	cfg, err := s.repo.Get(ctx)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return model.NewWebhookConfig(), nil
		}
		return nil, err
	}
	if cfg == nil {
		return model.NewWebhookConfig(), nil
	}
	return cfg, nil
}

// UpdateConfig 覆盖更新配置。
func (s *WebhookConfigService) UpdateConfig(ctx context.Context, cfg *model.WebhookConfig) error {
	if err := s.ValidateConfig(cfg); err != nil {
		return err
	}
	return s.repo.Save(ctx, cfg)
}

// AddPlatform 新增渠道。
func (s *WebhookConfigService) AddPlatform(ctx context.Context, platform model.PlatformConfig) error {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	for _, p := range cfg.Platforms {
		if p.ID == platform.ID {
			return fmt.Errorf("platform %s already exists", platform.ID)
		}
	}
	cfg.Platforms = append(cfg.Platforms, platform)
	return s.UpdateConfig(ctx, cfg)
}

// UpdatePlatform 更新指定渠道。
func (s *WebhookConfigService) UpdatePlatform(ctx context.Context, id string, platform model.PlatformConfig) error {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	updated := false
	for i := range cfg.Platforms {
		if cfg.Platforms[i].ID == id {
			platform.ID = id
			cfg.Platforms[i] = platform
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("platform %s not found", id)
	}
	return s.UpdateConfig(ctx, cfg)
}

// DeletePlatform 删除渠道。
func (s *WebhookConfigService) DeletePlatform(ctx context.Context, id string) error {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	for i := range cfg.Platforms {
		if cfg.Platforms[i].ID == id {
			cfg.Platforms = append(cfg.Platforms[:i], cfg.Platforms[i+1:]...)
			return s.UpdateConfig(ctx, cfg)
		}
	}
	return fmt.Errorf("platform %s not found", id)
}

// TogglePlatform 切换渠道启用状态。
func (s *WebhookConfigService) TogglePlatform(ctx context.Context, id string) error {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	for i := range cfg.Platforms {
		if cfg.Platforms[i].ID == id {
			cfg.Platforms[i].Enabled = !cfg.Platforms[i].Enabled
			return s.UpdateConfig(ctx, cfg)
		}
	}
	return fmt.Errorf("platform %s not found", id)
}

// ValidateConfig 校验配置合法性。
func (s *WebhookConfigService) ValidateConfig(cfg *model.WebhookConfig) error {
	if cfg == nil {
		return errors.New("webhook config is nil")
	}
	idSet := make(map[string]struct{}, len(cfg.Platforms))
	for _, p := range cfg.Platforms {
		if p.ID == "" {
			return errors.New("platform id is required")
		}
		if _, ok := idSet[p.ID]; ok {
			return fmt.Errorf("duplicate platform id: %s", p.ID)
		}
		idSet[p.ID] = struct{}{}
	}
	return cfg.Validate()
}
