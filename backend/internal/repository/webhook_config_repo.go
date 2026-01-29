package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

type redisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

type webhookConfigRepository struct {
	redis       redisClient
	settingRepo service.SettingRepository
}

const webhookConfigKey = "webhook_config:default"
const webhookConfigSettingKey = "webhook_config"

const webhookConfigEncryptionEnvKey = "SUB2API_WEBHOOK_CONFIG_ENCRYPTION_KEY"
const webhookConfigEncryptedPrefixV1 = "enc:v1:"

func NewWebhookConfigRepository(rdb *redis.Client, settingRepo service.SettingRepository) service.WebhookConfigRepository {
	return &webhookConfigRepository{
		redis:       rdb,
		settingRepo: settingRepo,
	}
}

func (r *webhookConfigRepository) Get(ctx context.Context) (*model.WebhookConfig, error) {
	config, err := r.getFromRedis(ctx)
	if err == nil {
		return config, nil
	}

	config, settingErr := r.getFromSettings(ctx)
	if settingErr == nil {
		r.cacheRedis(ctx, config)
		return config, nil
	}

	if errors.Is(err, redis.Nil) {
		return nil, settingErr
	}
	return nil, errors.Join(err, settingErr)
}

func (r *webhookConfigRepository) Save(ctx context.Context, config *model.WebhookConfig) error {
	if config == nil {
		return errors.New("webhook config is nil")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if config.CreatedAt == "" {
		config.CreatedAt = now
	}
	config.UpdatedAt = now

	if err := config.Validate(); err != nil {
		return err
	}

	data, err := encodeWebhookConfigForStorage(config)
	if err != nil {
		return err
	}

	redisErr := r.redis.Set(ctx, webhookConfigKey, data, 0).Err()
	settingErr := r.settingRepo.Set(ctx, webhookConfigSettingKey, string(data))
	if redisErr != nil || settingErr != nil {
		return errors.Join(redisErr, settingErr)
	}
	return nil
}

func (r *webhookConfigRepository) Delete(ctx context.Context) error {
	redisErr := r.redis.Del(ctx, webhookConfigKey).Err()
	settingErr := r.settingRepo.Delete(ctx, webhookConfigSettingKey)
	if redisErr != nil || settingErr != nil {
		return errors.Join(redisErr, settingErr)
	}
	return nil
}

func (r *webhookConfigRepository) getFromRedis(ctx context.Context) (*model.WebhookConfig, error) {
	data, err := r.redis.Get(ctx, webhookConfigKey).Bytes()
	if err != nil {
		return nil, err
	}
	return decodeWebhookConfig(data)
}

func (r *webhookConfigRepository) getFromSettings(ctx context.Context) (*model.WebhookConfig, error) {
	value, err := r.settingRepo.GetValue(ctx, webhookConfigSettingKey)
	if err != nil {
		return nil, err
	}
	return decodeWebhookConfig([]byte(value))
}

func (r *webhookConfigRepository) cacheRedis(ctx context.Context, cfg *model.WebhookConfig) {
	data, err := encodeWebhookConfigForStorage(cfg)
	if err != nil {
		return
	}
	_ = r.redis.Set(ctx, webhookConfigKey, data, 0).Err()
}

func decodeWebhookConfig(data []byte) (*model.WebhookConfig, error) {
	if len(data) == 0 {
		return nil, errors.New("webhook config is empty")
	}

	var cfg model.WebhookConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := decryptWebhookConfigSecrets(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func encodeWebhookConfigForStorage(cfg *model.WebhookConfig) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("webhook config is nil")
	}

	out := *cfg
	out.Platforms = make([]model.PlatformConfig, len(cfg.Platforms))
	copy(out.Platforms, cfg.Platforms)

	if err := encryptWebhookConfigSecrets(&out); err != nil {
		return nil, err
	}
	return json.Marshal(&out)
}

func encryptWebhookConfigSecrets(cfg *model.WebhookConfig) error {
	if cfg == nil {
		return errors.New("webhook config is nil")
	}
	for i := range cfg.Platforms {
		if err := encryptPlatformSecrets(&cfg.Platforms[i]); err != nil {
			return err
		}
	}
	return nil
}

func decryptWebhookConfigSecrets(cfg *model.WebhookConfig) error {
	if cfg == nil {
		return errors.New("webhook config is nil")
	}
	for i := range cfg.Platforms {
		if err := decryptPlatformSecrets(&cfg.Platforms[i]); err != nil {
			return err
		}
	}
	return nil
}

func encryptPlatformSecrets(p *model.PlatformConfig) error {
	if p == nil {
		return errors.New("platform config is nil")
	}

	key, keyConfigured := getWebhookConfigEncryptionKey()
	maybeEncrypt := func(value string) (string, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", nil
		}
		if strings.HasPrefix(value, webhookConfigEncryptedPrefixV1) {
			return value, nil
		}
		if !keyConfigured {
			return "", errors.New("webhook config encryption key is not configured")
		}
		return encryptStringV1(key, value)
	}

	var err error
	if p.Secret != "" {
		p.Secret, err = maybeEncrypt(p.Secret)
		if err != nil {
			return err
		}
	}
	if p.Token != "" {
		p.Token, err = maybeEncrypt(p.Token)
		if err != nil {
			return err
		}
	}
	if p.BotToken != "" {
		p.BotToken, err = maybeEncrypt(p.BotToken)
		if err != nil {
			return err
		}
	}
	if p.SMTPPass != "" {
		p.SMTPPass, err = maybeEncrypt(p.SMTPPass)
		if err != nil {
			return err
		}
	}
	return nil
}

func decryptPlatformSecrets(p *model.PlatformConfig) error {
	if p == nil {
		return errors.New("platform config is nil")
	}

	key, keyConfigured := getWebhookConfigEncryptionKey()
	maybeDecrypt := func(value string) (string, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", nil
		}
		if !strings.HasPrefix(value, webhookConfigEncryptedPrefixV1) {
			return value, nil
		}
		if !keyConfigured {
			return "", errors.New("webhook config encryption key is not configured")
		}
		return decryptStringV1(key, value)
	}

	var err error
	p.Secret, err = maybeDecrypt(p.Secret)
	if err != nil {
		return err
	}
	p.Token, err = maybeDecrypt(p.Token)
	if err != nil {
		return err
	}
	p.BotToken, err = maybeDecrypt(p.BotToken)
	if err != nil {
		return err
	}
	p.SMTPPass, err = maybeDecrypt(p.SMTPPass)
	if err != nil {
		return err
	}
	return nil
}

func getWebhookConfigEncryptionKey() ([]byte, bool) {
	raw := strings.TrimSpace(os.Getenv(webhookConfigEncryptionEnvKey))
	if raw == "" {
		raw = strings.TrimSpace(viper.GetString("jwt.secret"))
	}
	if raw == "" {
		return nil, false
	}

	sum := sha256.Sum256([]byte(raw))
	return sum[:], true
}

func encryptStringV1(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return webhookConfigEncryptedPrefixV1 + base64.StdEncoding.EncodeToString(payload), nil
}

func decryptStringV1(key []byte, value string) (string, error) {
	if !strings.HasPrefix(value, webhookConfigEncryptedPrefixV1) {
		return value, nil
	}
	encoded := strings.TrimPrefix(value, webhookConfigEncryptedPrefixV1)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext is too short")
	}

	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

var _ redisClient = (*redis.Client)(nil)
