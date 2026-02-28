// Package apikeys provides API key generation, validation, and management for Obsidian WAF.
// Implements secure key generation with scopes and rate limiting per key.
package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrKeyNotFound       = errors.New("API key not found")
	ErrKeyExpired        = errors.New("API key has expired")
	ErrKeyDisabled       = errors.New("API key is disabled")
	ErrInsufficientScope = errors.New("API key lacks required scope")
	ErrRateLimitExceeded = errors.New("API key rate limit exceeded")
)

// Scope defines what an API key can access
type Scope string

const (
	ScopeRead    Scope = "read"    // Read-only access to logs, stats, rules
	ScopeWrite   Scope = "write"   // Can modify rules, settings
	ScopeAdmin   Scope = "admin"   // Full administrative access
	ScopeThreat  Scope = "threat"  // Access to threat intelligence
	ScopeExport  Scope = "export"  // Can export data and reports
	ScopeWebhook Scope = "webhook" // Can manage webhooks
)

// AllScopes returns all available scopes
func AllScopes() []Scope {
	return []Scope{ScopeRead, ScopeWrite, ScopeAdmin, ScopeThreat, ScopeExport, ScopeWebhook}
}

// APIKey represents an API key with its metadata
type APIKey struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	KeyHash     string     `json:"-"`          // SHA256 hash of the key (never expose)
	KeyPrefix   string     `json:"key_prefix"` // First 8 chars for identification
	Scopes      []Scope    `json:"scopes"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"` // nil = never expires
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP  string     `json:"last_used_ip,omitempty"`
	Enabled     bool       `json:"enabled"`
	RateLimit   int        `json:"rate_limit"` // Requests per minute (0 = unlimited)
	Description string     `json:"description,omitempty"`
	CreatedBy   string     `json:"created_by"`
}

// KeyUsage tracks API key usage for rate limiting
type KeyUsage struct {
	Count       int
	WindowStart time.Time
}

// Manager handles API key operations
type Manager struct {
	mu       sync.RWMutex
	keys     map[string]*APIKey   // keyHash -> APIKey
	prefixes map[string]string    // keyPrefix -> keyHash (for lookup)
	usage    map[string]*KeyUsage // keyHash -> usage
	store    KeyStore
	logf     func(format string, args ...any)
}

// KeyStore interface for persistence
type KeyStore interface {
	SaveKey(ctx context.Context, key *APIKey) error
	GetKey(ctx context.Context, keyHash string) (*APIKey, error)
	GetAllKeys(ctx context.Context) ([]*APIKey, error)
	DeleteKey(ctx context.Context, keyHash string) error
	UpdateKeyUsage(ctx context.Context, keyHash string, lastUsed time.Time, lastIP string) error
}

// NewManager creates a new API key manager
func NewManager(store KeyStore) *Manager {
	m := &Manager{
		keys:     make(map[string]*APIKey),
		prefixes: make(map[string]string),
		usage:    make(map[string]*KeyUsage),
		store:    store,
		logf:     func(string, ...any) {},
	}
	return m
}

// SetLogf sets an optional logger callback used for non-fatal async errors.
func (m *Manager) SetLogf(logf func(format string, args ...any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if logf == nil {
		m.logf = func(string, ...any) {}
		return
	}
	m.logf = logf
}

// LoadKeys loads all keys from the store into memory
func (m *Manager) LoadKeys(ctx context.Context) error {
	if m.store == nil {
		return nil
	}

	keys, err := m.store.GetAllKeys(ctx)
	if err != nil {
		return fmt.Errorf("failed to load API keys: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range keys {
		m.keys[key.KeyHash] = key
		m.prefixes[key.KeyPrefix] = key.KeyHash
	}

	return nil
}

// GenerateKey creates a new API key with the given parameters
// Returns the plaintext key (only shown once) and the APIKey metadata
func (m *Manager) GenerateKey(ctx context.Context, name string, scopes []Scope, expiresIn *time.Duration, rateLimit int, description, createdBy string) (string, *APIKey, error) {
	// Generate 32 random bytes (256 bits)
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		return "", nil, fmt.Errorf("failed to generate random key: %w", err)
	}

	// Create the plaintext key with prefix for identification
	// Format: obs_<random_hex>
	plaintext := "obs_" + hex.EncodeToString(rawKey)

	// Hash the key for storage (never store plaintext)
	hash := sha256.Sum256([]byte(plaintext))
	keyHash := hex.EncodeToString(hash[:])

	// Create key prefix for display (first 8 chars after obs_)
	keyPrefix := plaintext[:12] // "obs_" + first 8 hex chars

	now := time.Now()
	var expiresAt *time.Time
	if expiresIn != nil {
		exp := now.Add(*expiresIn)
		expiresAt = &exp
	}

	// Generate unique ID
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate API key ID: %w", err)
	}

	apiKey := &APIKey{
		ID:          hex.EncodeToString(idBytes),
		Name:        name,
		KeyHash:     keyHash,
		KeyPrefix:   keyPrefix,
		Scopes:      scopes,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		Enabled:     true,
		RateLimit:   rateLimit,
		Description: description,
		CreatedBy:   createdBy,
	}

	// Store in memory
	m.mu.Lock()
	m.keys[keyHash] = apiKey
	m.prefixes[keyPrefix] = keyHash
	m.mu.Unlock()

	// Persist to store
	if m.store != nil {
		if err := m.store.SaveKey(ctx, apiKey); err != nil {
			// Rollback memory storage
			m.mu.Lock()
			delete(m.keys, keyHash)
			delete(m.prefixes, keyPrefix)
			m.mu.Unlock()
			return "", nil, fmt.Errorf("failed to persist API key: %w", err)
		}
	}

	return plaintext, apiKey, nil
}

// ValidateKey validates an API key and returns its metadata
func (m *Manager) ValidateKey(ctx context.Context, plaintext string, requiredScope Scope, clientIP string) (*APIKey, error) {
	// Hash the provided key
	hash := sha256.Sum256([]byte(plaintext))
	keyHash := hex.EncodeToString(hash[:])

	m.mu.RLock()
	apiKey, exists := m.keys[keyHash]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrKeyNotFound
	}

	// Check if enabled
	if !apiKey.Enabled {
		return nil, ErrKeyDisabled
	}

	// Check expiration
	if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
		return nil, ErrKeyExpired
	}

	// Check scope
	if requiredScope != "" && !m.hasScope(apiKey, requiredScope) {
		return nil, ErrInsufficientScope
	}

	// Check rate limit
	if apiKey.RateLimit > 0 {
		if err := m.checkRateLimit(keyHash, apiKey.RateLimit); err != nil {
			return nil, err
		}
	}

	// Update usage statistics
	now := time.Now()
	m.mu.Lock()
	apiKey.LastUsedAt = &now
	apiKey.LastUsedIP = clientIP
	apiKeyCopy := cloneKey(apiKey)
	logf := m.logf
	m.mu.Unlock()

	// Async persist usage update
	if m.store != nil {
		go func() {
			updateCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := m.store.UpdateKeyUsage(updateCtx, keyHash, now, clientIP); err != nil {
				logf("apikey usage update failed for key %s: %v", keyHash, err)
			}
		}()
	}

	return apiKeyCopy, nil
}

// hasScope checks if the key has the required scope
func (m *Manager) hasScope(key *APIKey, required Scope) bool {
	for _, s := range key.Scopes {
		if s == required || s == ScopeAdmin {
			return true
		}
	}
	return false
}

// checkRateLimit checks and updates the rate limit counter
func (m *Manager) checkRateLimit(keyHash string, limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	usage, exists := m.usage[keyHash]

	if !exists || now.Sub(usage.WindowStart) >= time.Minute {
		// New window
		m.usage[keyHash] = &KeyUsage{
			Count:       1,
			WindowStart: now,
		}
		return nil
	}

	if usage.Count >= limit {
		return ErrRateLimitExceeded
	}

	usage.Count++
	return nil
}

// RevokeKey disables an API key
func (m *Manager) RevokeKey(ctx context.Context, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range m.keys {
		if key.ID == keyID {
			key.Enabled = false
			if m.store != nil {
				return m.store.SaveKey(ctx, key)
			}
			return nil
		}
	}

	return ErrKeyNotFound
}

// DeleteKey permanently removes an API key
func (m *Manager) DeleteKey(ctx context.Context, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for hash, key := range m.keys {
		if key.ID == keyID {
			delete(m.keys, hash)
			delete(m.prefixes, key.KeyPrefix)
			delete(m.usage, hash)
			if m.store != nil {
				return m.store.DeleteKey(ctx, hash)
			}
			return nil
		}
	}

	return ErrKeyNotFound
}

// ListKeys returns all API keys (without sensitive data)
func (m *Manager) ListKeys() []*APIKey {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]*APIKey, 0, len(m.keys))
	for _, key := range m.keys {
		keys = append(keys, cloneKey(key))
	}
	return keys
}

// builtinCopy is a type-safe copy wrapper for Scope slices
func builtinCopy(dst, src []Scope) {
	for i, s := range src {
		dst[i] = s
	}
}

// GetKeyByID returns a specific API key by ID
func (m *Manager) GetKeyByID(keyID string) (*APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, key := range m.keys {
		if key.ID == keyID {
			return cloneKey(key), nil
		}
	}
	return nil, ErrKeyNotFound
}

func cloneKey(key *APIKey) *APIKey {
	if key == nil {
		return nil
	}
	copy := *key
	copy.Scopes = make([]Scope, len(key.Scopes))
	builtinCopy(copy.Scopes, key.Scopes)
	return &copy
}

// ExtractKeyFromHeader extracts API key from Authorization header
// Supports: "Bearer obs_xxx" or "ApiKey obs_xxx"
func ExtractKeyFromHeader(authHeader string) string {
	if authHeader == "" {
		return ""
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return ""
	}

	scheme := strings.ToLower(parts[0])
	if scheme == "bearer" || scheme == "apikey" {
		token := strings.TrimSpace(parts[1])
		if strings.HasPrefix(token, "obs_") {
			return token
		}
	}

	return ""
}
