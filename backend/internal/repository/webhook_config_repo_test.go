package repository

import (
	"context"
	"crypto/sha256"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestNewWebhookConfigRepository(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	settingRepo := newMockSettingRepository()

	repo := NewWebhookConfigRepository(rdb, settingRepo)
	impl, ok := repo.(*webhookConfigRepository)
	require.True(t, ok)
	require.Equal(t, rdb, impl.redis)
	require.Equal(t, settingRepo, impl.settingRepo)
}

func TestWebhookConfig_NewWebhookConfigRepository_Coverage(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	settingRepo := newMockSettingRepository()
	repo := NewWebhookConfigRepository(rdb, settingRepo)
	require.NotNil(t, repo)
}

func TestWebhookConfigRepository_Get_ReturnsRedisValue(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()

	config := sampleWebhookConfig()
	data, err := json.Marshal(config)
	require.NoError(t, err)
	redisMock.store[webhookConfigKey] = string(data)

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	got, err := repo.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, config.Enabled, got.Enabled)
	require.Equal(t, config.Platforms, got.Platforms)
}

func TestWebhookConfigRepository_Get_FallbacksToSettings(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()

	config := sampleWebhookConfig()
	config.Platforms[0].Name = "测试✨platform"
	data, err := json.Marshal(config)
	require.NoError(t, err)
	settingMock.store[webhookConfigSettingKey] = string(data)

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	got, err := repo.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, config.Platforms[0].Name, got.Platforms[0].Name)
	require.Equal(t, settingMock.store[webhookConfigSettingKey], redisMock.store[webhookConfigKey], "should backfill redis cache after db fallback")
}

func TestWebhookConfigRepository_Get_UsesSettingsWhenRedisMalformed(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()

	redisMock.store[webhookConfigKey] = "not-a-json"
	config := sampleWebhookConfig()
	data, err := json.Marshal(config)
	require.NoError(t, err)
	settingMock.store[webhookConfigSettingKey] = string(data)

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	got, err := repo.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, config.Platforms[0].ID, got.Platforms[0].ID)
}

func TestWebhookConfigRepository_Get_ReturnsSettingErrorWhenRedisNil(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	settingMock.getErr = service.ErrSettingNotFound

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	_, err := repo.Get(ctx)
	require.ErrorIs(t, err, service.ErrSettingNotFound)
}

func TestWebhookConfigRepository_Get_ReturnsErrorWhenBothSourcesFail(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()

	redisMock.getErr = errors.New("redis unreachable")
	settingMock.getErr = service.ErrSettingNotFound

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	_, err := repo.Get(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, service.ErrSettingNotFound)
	require.ErrorContains(t, err, "redis unreachable")
}

func TestWebhookConfigRepository_Save_DoubleWrites(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()

	config := sampleWebhookConfig()
	config.CreatedAt = "2024-01-01T00:00:00Z"

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	err := repo.Save(ctx, config)
	require.NoError(t, err)

	require.NotEmpty(t, config.UpdatedAt)
	require.Equal(t, "2024-01-01T00:00:00Z", config.CreatedAt, "createdAt should be preserved when provided")

	redisCfg := decodeStoredConfig(t, redisMock.store[webhookConfigKey])
	settingCfg := decodeStoredConfig(t, settingMock.store[webhookConfigSettingKey])
	require.Equal(t, redisCfg, settingCfg)
}

func TestWebhookConfigRepository_Save_EncryptsSecretsInStorage_AndGetDecrypts(t *testing.T) {
	t.Setenv(webhookConfigEncryptionEnvKey, "unit-test-key")

	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	cfg := &model.WebhookConfig{
		Enabled: true,
		Platforms: []model.PlatformConfig{
			{
				ID:         "p1",
				Type:       model.PlatformDingtalk,
				Name:       "Dingtalk",
				Enabled:    true,
				URL:        "https://example.com/webhook",
				EnableSign: true,
				Secret:     "raw-secret",
				Token:      "raw-token",
				BotToken:   "raw-bot-token",
				SMTPPass:   "raw-smtp-pass",
			},
		},
		NotificationTypes: map[model.NotificationType]bool{
			model.NotificationSystemError: true,
		},
		RetrySettings: model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1},
		CreatedAt:     "2024-01-01T00:00:00Z",
		UpdatedAt:     "2024-01-01T00:00:00Z",
	}

	err := repo.Save(ctx, cfg)
	require.NoError(t, err)

	redisRaw := redisMock.store[webhookConfigKey]
	settingRaw := settingMock.store[webhookConfigSettingKey]

	require.NotEmpty(t, redisRaw)
	require.Equal(t, redisRaw, settingRaw)
	require.Contains(t, redisRaw, webhookConfigEncryptedPrefixV1)
	require.NotContains(t, redisRaw, "raw-secret")
	require.NotContains(t, redisRaw, "raw-token")
	require.NotContains(t, redisRaw, "raw-bot-token")
	require.NotContains(t, redisRaw, "raw-smtp-pass")

	got, err := repo.Get(ctx)
	require.NoError(t, err)
	require.Len(t, got.Platforms, 1)
	require.Equal(t, "raw-secret", got.Platforms[0].Secret)
	require.Equal(t, "raw-token", got.Platforms[0].Token)
	require.Equal(t, "raw-bot-token", got.Platforms[0].BotToken)
	require.Equal(t, "raw-smtp-pass", got.Platforms[0].SMTPPass)
}

func TestWebhookConfigEncryption_NilGuards(t *testing.T) {
	require.Error(t, encryptWebhookConfigSecrets(nil))
	require.Error(t, decryptWebhookConfigSecrets(nil))
	require.Error(t, encryptPlatformSecrets(nil))
	require.Error(t, decryptPlatformSecrets(nil))

	_, err := encodeWebhookConfigForStorage(nil)
	require.Error(t, err)
}

func TestWebhookConfigEncryption_Save_RejectsSecretsWhenKeyMissing(t *testing.T) {
	t.Setenv(webhookConfigEncryptionEnvKey, "")
	prev := viper.GetString("jwt.secret")
	viper.Set("jwt.secret", "")
	t.Cleanup(func() { viper.Set("jwt.secret", prev) })

	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	cfg := &model.WebhookConfig{
		Enabled: true,
		Platforms: []model.PlatformConfig{
			{
				ID:         "p1",
				Type:       model.PlatformDingtalk,
				Name:       "Dingtalk",
				Enabled:    true,
				URL:        "https://example.com/webhook",
				EnableSign: true,
				Secret:     "raw-secret",
			},
		},
		NotificationTypes: map[model.NotificationType]bool{model.NotificationSystemError: true},
		RetrySettings:     model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1},
		CreatedAt:         "2024-01-01T00:00:00Z",
		UpdatedAt:         "2024-01-01T00:00:00Z",
	}

	err := repo.Save(ctx, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "encryption key")
}

func TestWebhookConfigEncryption_Get_FailsToDecryptWhenKeyMissing(t *testing.T) {
	t.Setenv(webhookConfigEncryptionEnvKey, "unit-test-key")

	key, ok := getWebhookConfigEncryptionKey()
	require.True(t, ok)

	encryptedSecret, err := encryptStringV1(key, "raw-secret")
	require.NoError(t, err)

	cfg := &model.WebhookConfig{
		Enabled: true,
		Platforms: []model.PlatformConfig{
			{
				ID:         "p1",
				Type:       model.PlatformDingtalk,
				Name:       "Dingtalk",
				Enabled:    true,
				URL:        "https://example.com/webhook",
				EnableSign: true,
				Secret:     encryptedSecret,
			},
		},
		NotificationTypes: map[model.NotificationType]bool{model.NotificationSystemError: true},
		RetrySettings:     model.RetrySettings{MaxRetries: 0, RetryDelay: 0, Timeout: 1},
		CreatedAt:         "2024-01-01T00:00:00Z",
		UpdatedAt:         "2024-01-01T00:00:00Z",
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	t.Setenv(webhookConfigEncryptionEnvKey, "")
	prev := viper.GetString("jwt.secret")
	viper.Set("jwt.secret", "")
	t.Cleanup(func() { viper.Set("jwt.secret", prev) })

	ctx := context.Background()
	redisMock := newMockRedisClient()
	redisMock.store[webhookConfigKey] = string(raw)
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: newMockSettingRepository()}

	_, getErr := repo.Get(ctx)
	require.Error(t, getErr)
	require.ErrorContains(t, getErr, "encryption key")
}

func TestWebhookConfigEncryption_EncryptDecrypt_EdgeCases(t *testing.T) {
	key32 := sha256.Sum256([]byte("unit-test-key"))

	_, err := encryptStringV1([]byte("short"), "x")
	require.Error(t, err)

	_, err = decryptStringV1([]byte("short"), webhookConfigEncryptedPrefixV1+"AA==")
	require.Error(t, err)

	_, err = decryptStringV1(key32[:], webhookConfigEncryptedPrefixV1+"not-base64!!")
	require.Error(t, err)

	tooShort := webhookConfigEncryptedPrefixV1 + "AA=="
	_, err = decryptStringV1(key32[:], tooShort)
	require.Error(t, err)

	unchanged, err := decryptStringV1(key32[:], "plain-text")
	require.NoError(t, err)
	require.Equal(t, "plain-text", unchanged)

	enc, err := encryptStringV1(key32[:], "hello")
	require.NoError(t, err)
	otherKey := sha256.Sum256([]byte("other-key"))
	_, err = decryptStringV1(otherKey[:], enc)
	require.Error(t, err)
}

func TestWebhookConfigEncryption_EncryptStringV1_ReadNonceError(t *testing.T) {
	key32 := sha256.Sum256([]byte("unit-test-key"))
	prev := rand.Reader
	rand.Reader = &failingReader{err: errors.New("nonce read fail")}
	t.Cleanup(func() { rand.Reader = prev })

	_, err := encryptStringV1(key32[:], "hello")
	require.Error(t, err)
}

func TestWebhookConfigEncryption_EncryptPlatformSecrets_SkipsEmptyAndKeepsEncrypted(t *testing.T) {
	t.Setenv(webhookConfigEncryptionEnvKey, "")
	prev := viper.GetString("jwt.secret")
	viper.Set("jwt.secret", "")
	t.Cleanup(func() { viper.Set("jwt.secret", prev) })

	p := &model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Dingtalk",
		Enabled: true,
		URL:     "https://example.com/webhook",
	}
	require.NoError(t, encryptPlatformSecrets(p))

	p.Secret = webhookConfigEncryptedPrefixV1 + "AA=="
	require.NoError(t, encryptPlatformSecrets(p))
	require.Equal(t, webhookConfigEncryptedPrefixV1+"AA==", p.Secret)
}

func TestWebhookConfigEncryption_DecryptPlatformSecrets_AllowsPlaintextWithoutKey(t *testing.T) {
	t.Setenv(webhookConfigEncryptionEnvKey, "")
	prev := viper.GetString("jwt.secret")
	viper.Set("jwt.secret", "")
	t.Cleanup(func() { viper.Set("jwt.secret", prev) })

	p := &model.PlatformConfig{
		ID:         "p1",
		Type:       model.PlatformDingtalk,
		Name:       "Dingtalk",
		Enabled:    true,
		URL:        "https://example.com/webhook",
		EnableSign: true,
		Secret:     "raw-secret",
	}
	require.NoError(t, decryptPlatformSecrets(p))
	require.Equal(t, "raw-secret", p.Secret)
}

func TestWebhookConfigEncryption_EncryptPlatformSecrets_ErrorsPerFieldWhenKeyMissing(t *testing.T) {
	t.Setenv(webhookConfigEncryptionEnvKey, "")
	prev := viper.GetString("jwt.secret")
	viper.Set("jwt.secret", "")
	t.Cleanup(func() { viper.Set("jwt.secret", prev) })

	p := &model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Dingtalk",
		Enabled: true,
		URL:     "https://example.com/webhook",
	}

	p.Token = "raw-token"
	require.ErrorContains(t, encryptPlatformSecrets(p), "encryption key")
	p.Token = ""

	p.BotToken = "raw-bot-token"
	require.ErrorContains(t, encryptPlatformSecrets(p), "encryption key")
	p.BotToken = ""

	p.SMTPPass = "raw-smtp-pass"
	require.ErrorContains(t, encryptPlatformSecrets(p), "encryption key")
}

func TestWebhookConfigEncryption_DecryptPlatformSecrets_ErrorsPerFieldWhenKeyMissing(t *testing.T) {
	t.Setenv(webhookConfigEncryptionEnvKey, "unit-test-key")
	key, ok := getWebhookConfigEncryptionKey()
	require.True(t, ok)

	encToken, err := encryptStringV1(key, "raw-token")
	require.NoError(t, err)
	encBotToken, err := encryptStringV1(key, "raw-bot-token")
	require.NoError(t, err)
	encSMTPPass, err := encryptStringV1(key, "raw-smtp-pass")
	require.NoError(t, err)

	t.Setenv(webhookConfigEncryptionEnvKey, "")
	prev := viper.GetString("jwt.secret")
	viper.Set("jwt.secret", "")
	t.Cleanup(func() { viper.Set("jwt.secret", prev) })

	base := model.PlatformConfig{
		ID:      "p1",
		Type:    model.PlatformDingtalk,
		Name:    "Dingtalk",
		Enabled: true,
		URL:     "https://example.com/webhook",
	}

	p1 := base
	p1.Token = encToken
	require.ErrorContains(t, decryptPlatformSecrets(&p1), "encryption key")

	p2 := base
	p2.BotToken = encBotToken
	require.ErrorContains(t, decryptPlatformSecrets(&p2), "encryption key")

	p3 := base
	p3.SMTPPass = encSMTPPass
	require.ErrorContains(t, decryptPlatformSecrets(&p3), "encryption key")
}

func TestWebhookConfigRepository_Save_SetsTimestampsWhenMissing(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	config := sampleWebhookConfig()
	config.CreatedAt = ""
	config.UpdatedAt = ""

	err := repo.Save(ctx, config)
	require.NoError(t, err)
	require.NotEmpty(t, config.CreatedAt)
	require.NotEmpty(t, config.UpdatedAt)
	require.Equal(t, config.CreatedAt, config.UpdatedAt, "timestamps should be set consistently when missing")
}

func TestWebhookConfigRepository_Save_RejectsNilConfig(t *testing.T) {
	ctx := context.Background()
	repo := &webhookConfigRepository{redis: newMockRedisClient(), settingRepo: newMockSettingRepository()}

	err := repo.Save(ctx, nil)
	require.Error(t, err)
}

func TestWebhookConfigRepository_Save_ReturnsValidationError(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	err := repo.Save(ctx, &model.WebhookConfig{})
	require.Error(t, err)
	require.Empty(t, redisMock.store)
	require.Empty(t, settingMock.store)
}

func TestWebhookConfigRepository_Save_ReportsRedisFailure(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	config := sampleWebhookConfig()
	redisMock.setErr = errors.New("set failed")

	err := repo.Save(ctx, config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "set failed")
	require.Empty(t, redisMock.store, "redis write should fail")
	require.NotEmpty(t, settingMock.store, "settings write should still execute")
}

func TestWebhookConfigRepository_Save_ReportsSettingFailure(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	settingMock.setErr = errors.New("setting write failed")
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	config := sampleWebhookConfig()

	err := repo.Save(ctx, config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "setting write failed")
	require.NotEmpty(t, redisMock.store, "redis write should still occur")
	require.Empty(t, settingMock.store, "setting write should fail")
}

func TestWebhookConfigRepository_Save_ConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()
	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			cfg := sampleWebhookConfig()
			cfg.CreatedAt = ""
			cfg.UpdatedAt = ""
			cfg.Platforms[0].Name = fmt.Sprintf("platform-%d", idx)
			_ = repo.Save(ctx, cfg)
		}()
	}
	wg.Wait()

	require.NotEmpty(t, redisMock.store[webhookConfigKey])
	require.NotEmpty(t, settingMock.store[webhookConfigSettingKey])

	latest := decodeStoredConfig(t, redisMock.store[webhookConfigKey])
	require.NotEmpty(t, latest.CreatedAt)
	require.NotEmpty(t, latest.UpdatedAt)
}

func TestWebhookConfigRepository_Delete_RemovesStores(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	settingMock := newMockSettingRepository()

	config := sampleWebhookConfig()
	data, err := json.Marshal(config)
	require.NoError(t, err)
	redisMock.store[webhookConfigKey] = string(data)
	settingMock.store[webhookConfigSettingKey] = string(data)

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	err = repo.Delete(ctx)
	require.NoError(t, err)
	require.Empty(t, redisMock.store)
	require.Empty(t, settingMock.store)
}

func TestWebhookConfigRepository_Delete_PropagatesErrors(t *testing.T) {
	ctx := context.Background()
	redisMock := newMockRedisClient()
	redisMock.delErr = errors.New("del failed")
	settingMock := newMockSettingRepository()
	settingMock.store[webhookConfigSettingKey] = "{}"
	redisMock.store[webhookConfigKey] = "{}"

	repo := &webhookConfigRepository{redis: redisMock, settingRepo: settingMock}

	err := repo.Delete(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "del failed")
	require.Empty(t, settingMock.store, "setting delete should still run")
}

func sampleWebhookConfig() *model.WebhookConfig {
	now := time.Now().UTC().Format(time.RFC3339)
	return &model.WebhookConfig{
		Enabled: true,
		Platforms: []model.PlatformConfig{
			{
				ID:      "p1",
				Type:    model.PlatformLark,
				Name:    "Lark Hook",
				Enabled: true,
				URL:     "https://example.com/webhook",
			},
		},
		NotificationTypes: map[model.NotificationType]bool{
			model.NotificationAccountAnomaly:    true,
			model.NotificationSystemError:       true,
			model.NotificationSecurityAlert:     true,
			model.NotificationRateLimitRecovery: true,
		},
		RetrySettings: model.RetrySettings{
			MaxRetries: 3,
			RetryDelay: 1000,
			Timeout:    5000,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func decodeStoredConfig(t *testing.T, raw string) *model.WebhookConfig {
	t.Helper()
	var cfg model.WebhookConfig
	err := json.Unmarshal([]byte(raw), &cfg)
	require.NoError(t, err)
	return &cfg
}

func TestWebhookConfigDecode_EmptyData(t *testing.T) {
	_, err := decodeWebhookConfig(nil)
	require.Error(t, err)
}

func TestWebhookConfigDecode_InvalidData(t *testing.T) {
	raw := []byte(`{"enabled":true,"platforms":[{"id":"","type":"","name":"","enabled":true}],"notificationTypes":{},"retrySettings":{"maxRetries":-1,"retryDelay":-1,"timeout":0},"createdAt":"bad","updatedAt":"bad"}`)
	_, err := decodeWebhookConfig(raw)
	require.Error(t, err)
}

func TestWebhookConfigDecode_MalformedJSON(t *testing.T) {
	_, err := decodeWebhookConfig([]byte(`{"enabled":true,"platforms":"oops"`))
	require.Error(t, err)
}

func TestWebhookConfigCacheRedis_NilConfig_NoPanic(t *testing.T) {
	repo := &webhookConfigRepository{redis: newMockRedisClient(), settingRepo: newMockSettingRepository()}
	repo.cacheRedis(context.Background(), nil)
}

type failingReader struct {
	err error
}

func (r *failingReader) Read(p []byte) (int, error) {
	return 0, r.err
}

var _ io.Reader = (*failingReader)(nil)

type mockRedisClient struct {
	mu     sync.Mutex
	store  map[string]string
	getErr error
	setErr error
	delErr error
}

func newMockRedisClient() *mockRedisClient {
	return &mockRedisClient{
		store: make(map[string]string),
	}
}

func (m *mockRedisClient) Get(_ context.Context, key string) *redis.StringCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return redis.NewStringResult("", m.getErr)
	}

	val, ok := m.store[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(val, nil)
}

func (m *mockRedisClient) Set(_ context.Context, key string, value interface{}, _ time.Duration) *redis.StatusCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.setErr != nil {
		return redis.NewStatusResult("", m.setErr)
	}

	var str string
	switch v := value.(type) {
	case []byte:
		str = string(v)
	case string:
		str = v
	default:
		str = fmt.Sprint(v)
	}
	m.store[key] = str
	return redis.NewStatusResult("OK", nil)
}

func (m *mockRedisClient) Del(_ context.Context, keys ...string) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.delErr != nil {
		return redis.NewIntResult(0, m.delErr)
	}

	var deleted int64
	for _, key := range keys {
		if _, ok := m.store[key]; ok {
			delete(m.store, key)
			deleted++
		}
	}
	return redis.NewIntResult(deleted, nil)
}

type mockSettingRepository struct {
	mu        sync.Mutex
	store     map[string]string
	getErr    error
	setErr    error
	deleteErr error
}

func newMockSettingRepository() *mockSettingRepository {
	return &mockSettingRepository{
		store: make(map[string]string),
	}
}

func (m *mockSettingRepository) Get(ctx context.Context, key string) (*service.Setting, error) {
	value, err := m.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (m *mockSettingRepository) GetValue(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return "", m.getErr
	}

	val, ok := m.store[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return val, nil
}

func (m *mockSettingRepository) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.setErr != nil {
		return m.setErr
	}
	m.store[key] = value
	return nil
}

func (m *mockSettingRepository) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return nil, m.getErr
	}

	res := make(map[string]string, len(keys))
	for _, k := range keys {
		if val, ok := m.store[k]; ok {
			res[k] = val
		}
	}
	return res, nil
}

func (m *mockSettingRepository) SetMultiple(_ context.Context, settings map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.setErr != nil {
		return m.setErr
	}
	for k, v := range settings {
		m.store[k] = v
	}
	return nil
}

func (m *mockSettingRepository) GetAll(_ context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return nil, m.getErr
	}

	res := make(map[string]string, len(m.store))
	for k, v := range m.store {
		res[k] = v
	}
	return res, nil
}

func (m *mockSettingRepository) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.store, key)
	return nil
}

var _ redisClient = (*mockRedisClient)(nil)
var _ service.SettingRepository = (*mockSettingRepository)(nil)
