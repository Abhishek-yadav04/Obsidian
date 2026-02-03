// Package secrets provides hot-reload support for credentials and configuration.
// Supports manual reload via API and automatic reload on SIGHUP.
package secrets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrSecretNotFound   = errors.New("secret not found")
	ErrInvalidSecret    = errors.New("invalid secret value")
	ErrReloadInProgress = errors.New("reload already in progress")
)

// SecretType identifies different types of secrets
type SecretType string

const (
	SecretJWT     SecretType = "jwt_secret"
	SecretDB      SecretType = "database_url"
	SecretRedis   SecretType = "redis_url"
	SecretAPIKey  SecretType = "api_key"
	SecretWebhook SecretType = "webhook_secret"
	SecretEncrypt SecretType = "encryption_key"
)

// Secret represents a managed secret
type Secret struct {
	Type     SecretType `json:"type"`
	Value    string     `json:"-"` // Never serialize
	Version  int        `json:"version"`
	LoadedAt time.Time  `json:"loaded_at"`
	Source   string     `json:"source"` // "env", "file", "vault"
	EnvVar   string     `json:"env_var"`
	Masked   string     `json:"masked"` // First 4 + last 4 chars
}

// ReloadEvent is emitted when secrets are reloaded
type ReloadEvent struct {
	Type       SecretType
	OldVersion int
	NewVersion int
	Timestamp  time.Time
}

// ReloadCallback is called when a secret is reloaded
type ReloadCallback func(event ReloadEvent)

// Manager handles secret storage and hot-reloading
type Manager struct {
	mu          sync.RWMutex
	secrets     map[SecretType]*Secret
	callbacks   map[SecretType][]ReloadCallback
	reloading   atomic.Bool
	reloadCount atomic.Int64
	lastReload  time.Time
}

// NewManager creates a new secrets manager
func NewManager() *Manager {
	return &Manager{
		secrets:   make(map[SecretType]*Secret),
		callbacks: make(map[SecretType][]ReloadCallback),
	}
}

// LoadFromEnv loads a secret from an environment variable
func (m *Manager) LoadFromEnv(secretType SecretType, envVar string) error {
	value := os.Getenv(envVar)
	if value == "" {
		return fmt.Errorf("%w: env var %s not set", ErrSecretNotFound, envVar)
	}

	return m.SetSecret(secretType, value, "env", envVar)
}

// LoadFromEnvWithDefault loads a secret from env or uses default
func (m *Manager) LoadFromEnvWithDefault(secretType SecretType, envVar, defaultValue string) error {
	value := os.Getenv(envVar)
	if value == "" {
		value = defaultValue
	}

	return m.SetSecret(secretType, value, "env", envVar)
}

// SetSecret sets or updates a secret
func (m *Manager) SetSecret(secretType SecretType, value, source, envVar string) error {
	if value == "" {
		return ErrInvalidSecret
	}

	m.mu.Lock()

	oldVersion := 0
	if existing, exists := m.secrets[secretType]; exists {
		oldVersion = existing.Version
	}

	newVersion := oldVersion + 1

	m.secrets[secretType] = &Secret{
		Type:     secretType,
		Value:    value,
		Version:  newVersion,
		LoadedAt: time.Now(),
		Source:   source,
		EnvVar:   envVar,
		Masked:   maskSecret(value),
	}

	callbacks := m.callbacks[secretType]
	m.mu.Unlock()

	// Notify callbacks (outside lock)
	if oldVersion > 0 {
		event := ReloadEvent{
			Type:       secretType,
			OldVersion: oldVersion,
			NewVersion: newVersion,
			Timestamp:  time.Now(),
		}
		for _, cb := range callbacks {
			go cb(event)
		}
	}

	return nil
}

// GetSecret retrieves a secret value
func (m *Manager) GetSecret(secretType SecretType) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	secret, exists := m.secrets[secretType]
	if !exists {
		return "", ErrSecretNotFound
	}

	return secret.Value, nil
}

// GetSecretInfo retrieves secret metadata (without value)
func (m *Manager) GetSecretInfo(secretType SecretType) (*Secret, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	secret, exists := m.secrets[secretType]
	if !exists {
		return nil, ErrSecretNotFound
	}

	// Return copy without actual value
	return &Secret{
		Type:     secret.Type,
		Version:  secret.Version,
		LoadedAt: secret.LoadedAt,
		Source:   secret.Source,
		EnvVar:   secret.EnvVar,
		Masked:   secret.Masked,
	}, nil
}

// OnReload registers a callback for secret reloads
func (m *Manager) OnReload(secretType SecretType, callback ReloadCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callbacks[secretType] = append(m.callbacks[secretType], callback)
}

// ReloadFromEnv reloads a secret from its environment variable
func (m *Manager) ReloadFromEnv(ctx context.Context, secretType SecretType) error {
	m.mu.RLock()
	secret, exists := m.secrets[secretType]
	if !exists {
		m.mu.RUnlock()
		return ErrSecretNotFound
	}
	envVar := secret.EnvVar
	m.mu.RUnlock()

	// Re-read from environment
	value := os.Getenv(envVar)
	if value == "" {
		return fmt.Errorf("%w: env var %s not set", ErrSecretNotFound, envVar)
	}

	return m.SetSecret(secretType, value, "env", envVar)
}

// ReloadAll reloads all secrets from their sources
func (m *Manager) ReloadAll(ctx context.Context) error {
	if !m.reloading.CompareAndSwap(false, true) {
		return ErrReloadInProgress
	}
	defer m.reloading.Store(false)

	m.mu.RLock()
	secretsCopy := make(map[SecretType]*Secret, len(m.secrets))
	for k, v := range m.secrets {
		secretsCopy[k] = v
	}
	m.mu.RUnlock()

	var errs []error
	for secretType, secret := range secretsCopy {
		if secret.Source == "env" && secret.EnvVar != "" {
			if err := m.ReloadFromEnv(ctx, secretType); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", secretType, err))
			}
		}
	}

	m.reloadCount.Add(1)
	m.mu.Lock()
	m.lastReload = time.Now()
	m.mu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("some secrets failed to reload: %v", errs)
	}

	return nil
}

// RotateJWTSecret generates a new JWT secret and returns the old one
// The old secret should be kept for a grace period to validate existing tokens
func (m *Manager) RotateJWTSecret() (newSecret, oldSecret string, err error) {
	// Get old secret
	oldSecret, _ = m.GetSecret(SecretJWT)

	// Generate new secret (32 bytes = 256 bits)
	rawSecret := make([]byte, 32)
	if _, err := rand.Read(rawSecret); err != nil {
		return "", "", fmt.Errorf("failed to generate secret: %w", err)
	}

	newSecret = hex.EncodeToString(rawSecret)

	// Store new secret
	if err := m.SetSecret(SecretJWT, newSecret, "generated", ""); err != nil {
		return "", "", err
	}

	return newSecret, oldSecret, nil
}

// ListSecrets returns info about all managed secrets (values masked)
func (m *Manager) ListSecrets() []*Secret {
	m.mu.RLock()
	defer m.mu.RUnlock()

	secrets := make([]*Secret, 0, len(m.secrets))
	for _, s := range m.secrets {
		secrets = append(secrets, &Secret{
			Type:     s.Type,
			Version:  s.Version,
			LoadedAt: s.LoadedAt,
			Source:   s.Source,
			EnvVar:   s.EnvVar,
			Masked:   s.Masked,
		})
	}
	return secrets
}

// Stats returns reload statistics
type Stats struct {
	TotalSecrets int       `json:"total_secrets"`
	ReloadCount  int64     `json:"reload_count"`
	LastReload   time.Time `json:"last_reload"`
	IsReloading  bool      `json:"is_reloading"`
}

// GetStats returns current stats
func (m *Manager) GetStats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return Stats{
		TotalSecrets: len(m.secrets),
		ReloadCount:  m.reloadCount.Load(),
		LastReload:   m.lastReload,
		IsReloading:  m.reloading.Load(),
	}
}

// maskSecret returns a masked version of the secret
func maskSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// ValidateJWTSecret ensures JWT secret meets minimum requirements
func ValidateJWTSecret(secret string) error {
	if len(secret) < 32 {
		return fmt.Errorf("JWT secret must be at least 32 characters")
	}
	return nil
}
