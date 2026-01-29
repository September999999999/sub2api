package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/stretchr/testify/require"
)

func TestNotificationHandler_InternalHelpers(t *testing.T) {
	empty1 := clonePlatforms(nil)
	require.NotNil(t, empty1)
	require.Len(t, empty1, 0)

	empty2 := clonePlatforms([]model.PlatformConfig{})
	require.NotNil(t, empty2)
	require.Len(t, empty2, 0)

	require.Nil(t, maskWebhookConfig(nil))

	cfg := &model.WebhookConfig{
		Platforms: []model.PlatformConfig{
			{Secret: "raw-secret"},
		},
	}
	masked := maskWebhookConfig(cfg)
	require.NotNil(t, masked)
	require.Len(t, masked.Platforms, 1)
	require.Equal(t, "******", masked.Platforms[0].Secret)
}
