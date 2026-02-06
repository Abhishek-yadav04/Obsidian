// Package hibp provides HaveIBeenPwned password breach checking using k-anonymity.
// This ensures passwords are never sent in full to the HIBP service.
package hibp

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrPasswordBreached   = errors.New("password found in known data breaches")
	ErrServiceUnavailable = errors.New("HIBP service unavailable")
	ErrRateLimited        = errors.New("HIBP rate limit exceeded")
)

const (
	// HIBP API endpoint for range queries (k-anonymity)
	hibpAPIURL = "https://api.pwnedpasswords.com/range/"

	// Default threshold - reject passwords seen more than this many times
	DefaultBreachThreshold = 1

	// Cache duration for HIBP responses
	DefaultCacheDuration = 24 * time.Hour
)

// BreachResult contains the result of a password check
type BreachResult struct {
	IsBreached  bool      `json:"is_breached"`
	BreachCount int       `json:"breach_count"`
	CheckedAt   time.Time `json:"checked_at"`
	Prefix      string    `json:"-"` // SHA1 prefix used (for debugging)
}

// Config for the HIBP checker
type Config struct {
	// Enabled turns breach checking on/off
	Enabled bool `json:"enabled"`

	// BreachThreshold - passwords seen more than this are rejected (default: 1)
	BreachThreshold int `json:"breach_threshold"`

	// CacheDuration - how long to cache HIBP responses
	CacheDuration time.Duration `json:"cache_duration"`

	// Timeout for API requests
	Timeout time.Duration `json:"timeout"`

	// EnforceOnRegistration - check during user registration
	EnforceOnRegistration bool `json:"enforce_on_registration"`

	// EnforceOnPasswordChange - check during password changes
	EnforceOnPasswordChange bool `json:"enforce_on_password_change"`

	// WarnOnly - warn but don't reject breached passwords
	WarnOnly bool `json:"warn_only"`
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Enabled:                 true,
		BreachThreshold:         DefaultBreachThreshold,
		CacheDuration:           DefaultCacheDuration,
		Timeout:                 10 * time.Second,
		EnforceOnRegistration:   true,
		EnforceOnPasswordChange: true,
		WarnOnly:                false,
	}
}

// cacheEntry for storing HIBP responses
type cacheEntry struct {
	suffixes  map[string]int // suffix -> breach count
	fetchedAt time.Time
}

// Checker handles password breach checking
type Checker struct {
	mu     sync.RWMutex
	config *Config
	cache  map[string]*cacheEntry // SHA1 prefix -> cache entry
	client *http.Client
}

// NewChecker creates a new HIBP password checker
func NewChecker(config *Config) *Checker {
	if config == nil {
		config = DefaultConfig()
	}

	return &Checker{
		config: config,
		cache:  make(map[string]*cacheEntry),
		client: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// CheckPassword checks if a password has been exposed in known data breaches.
// Uses k-anonymity: only the first 5 characters of the SHA1 hash are sent to HIBP.
func (c *Checker) CheckPassword(ctx context.Context, password string) (*BreachResult, error) {
	if !c.config.Enabled {
		return &BreachResult{
			IsBreached:  false,
			BreachCount: 0,
			CheckedAt:   time.Now(),
		}, nil
	}

	// Compute SHA1 hash of password
	hash := sha1.Sum([]byte(password))
	hashHex := strings.ToUpper(hex.EncodeToString(hash[:]))

	// Split into prefix (5 chars) and suffix (35 chars)
	prefix := hashHex[:5]
	suffix := hashHex[5:]

	// Check cache first
	if count, found := c.checkCache(prefix, suffix); found {
		return &BreachResult{
			IsBreached:  count >= c.config.BreachThreshold,
			BreachCount: count,
			CheckedAt:   time.Now(),
			Prefix:      prefix,
		}, nil
	}

	// Query HIBP API
	suffixes, err := c.fetchHIBPRange(ctx, prefix)
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.cacheResult(prefix, suffixes)

	// Look up our suffix
	count := suffixes[suffix]

	return &BreachResult{
		IsBreached:  count >= c.config.BreachThreshold,
		BreachCount: count,
		CheckedAt:   time.Now(),
		Prefix:      prefix,
	}, nil
}

// checkCache looks up a prefix/suffix in the cache
func (c *Checker) checkCache(prefix, suffix string) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[prefix]
	if !exists {
		return 0, false
	}

	// Check if cache is still valid
	if time.Since(entry.fetchedAt) > c.config.CacheDuration {
		return 0, false
	}

	count, _ := entry.suffixes[suffix]
	return count, true // Prefix is cached and not expired; count=0 means no breach found
}

// cacheResult stores HIBP response in cache
func (c *Checker) cacheResult(prefix string, suffixes map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[prefix] = &cacheEntry{
		suffixes:  suffixes,
		fetchedAt: time.Now(),
	}
}

// fetchHIBPRange queries the HIBP API for a hash prefix
func (c *Checker) fetchHIBPRange(ctx context.Context, prefix string) (map[string]int, error) {
	url := hibpAPIURL + prefix

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers
	req.Header.Set("User-Agent", "Obsidian-WAF/2.1.0")
	req.Header.Set("Add-Padding", "true") // Adds padding to prevent response length analysis

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Success - parse response
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	default:
		return nil, fmt.Errorf("%w: status %d", ErrServiceUnavailable, resp.StatusCode)
	}

	// Parse response: each line is "SUFFIX:COUNT"
	suffixes := make(map[string]int)
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1024*1024)) // Limit to 1MB

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		suffix := strings.ToUpper(parts[0])
		count, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		suffixes[suffix] = count
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return suffixes, nil
}

// ValidatePassword checks password and returns appropriate error if breached
func (c *Checker) ValidatePassword(ctx context.Context, password string) error {
	result, err := c.CheckPassword(ctx, password)
	if err != nil {
		// If HIBP is unavailable, allow the password (fail open)
		// but log the error
		return nil
	}

	if result.IsBreached && !c.config.WarnOnly {
		return fmt.Errorf("%w: this password has appeared in %d data breaches and should not be used",
			ErrPasswordBreached, result.BreachCount)
	}

	return nil
}

// IsEnabled returns whether breach checking is enabled
func (c *Checker) IsEnabled() bool {
	return c.config.Enabled
}

// SetEnabled enables/disables breach checking
func (c *Checker) SetEnabled(enabled bool) {
	c.mu.Lock()
	c.config.Enabled = enabled
	c.mu.Unlock()
}

// GetConfig returns the current configuration
func (c *Checker) GetConfig() *Config {
	return c.config
}

// SetConfig updates the configuration
func (c *Checker) SetConfig(config *Config) {
	c.mu.Lock()
	c.config = config
	c.client.Timeout = config.Timeout
	c.mu.Unlock()
}

// ClearCache clears the HIBP response cache
func (c *Checker) ClearCache() {
	c.mu.Lock()
	c.cache = make(map[string]*cacheEntry)
	c.mu.Unlock()
}

// GetCacheStats returns cache statistics
func (c *Checker) GetCacheStats() (entries int, hitRate float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entries = len(c.cache)
	// In a production system, you'd track hits/misses
	return entries, 0.0
}
