// Package ratelimit provides a Redis-backed distributed rate limiter for Obsidian WAF.
// This implementation uses Redis for state sharing across multiple WAF instances,
// enabling horizontal scaling while maintaining consistent rate limiting.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors for Redis rate limiter
var (
	ErrRedisConnection = errors.New("failed to connect to Redis")
	ErrRedisCommand    = errors.New("Redis command failed")
)

// RedisClient interface for Redis operations
// This allows for easy mocking in tests and supports different Redis clients
type RedisClient interface {
	// INCR atomically increments a key
	Incr(ctx context.Context, key string) (int64, error)
	// EXPIRE sets key expiration
	Expire(ctx context.Context, key string, expiration time.Duration) error
	// GET retrieves a value
	Get(ctx context.Context, key string) (string, error)
	// SET sets a value with optional expiration
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	// DEL deletes keys
	Del(ctx context.Context, keys ...string) error
	// EXISTS checks if key exists
	Exists(ctx context.Context, keys ...string) (int64, error)
	// EVAL runs a Lua script
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error)
	// TTL gets remaining TTL
	TTL(ctx context.Context, key string) (time.Duration, error)
	// SCAN iterates keys
	Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error)
	// Ping checks connectivity
	Ping(ctx context.Context) error
	// Close closes the connection
	Close() error
}

// RedisConfig holds Redis connection configuration
type RedisConfig struct {
	// Addresses for Redis (single or cluster)
	Addresses []string

	// Password for Redis authentication
	Password string

	// Database number (0-15 for single instance)
	Database int

	// TLS enables TLS connections
	TLS bool

	// PoolSize is the connection pool size
	PoolSize int

	// MinIdleConns is minimum idle connections
	MinIdleConns int

	// DialTimeout for new connections
	DialTimeout time.Duration

	// ReadTimeout for read operations
	ReadTimeout time.Duration

	// WriteTimeout for write operations
	WriteTimeout time.Duration

	// KeyPrefix for all rate limit keys
	KeyPrefix string
}

// DefaultRedisConfig returns sensible defaults
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		Addresses:    []string{"localhost:6379"},
		Password:     "",
		Database:     0,
		TLS:          false,
		PoolSize:     10,
		MinIdleConns: 2,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		KeyPrefix:    "obsidian:rl:",
	}
}

// RedisRateLimiterConfig for distributed rate limiting
type RedisRateLimiterConfig struct {
	// Redis configuration
	Redis RedisConfig

	// Rate limit settings
	RequestsPerMinute int
	BurstSize         int
	BlockDuration     time.Duration

	// Whitelist of IPs that bypass rate limiting
	Whitelist []string

	// Endpoint-specific rate limits
	EndpointLimits map[string]EndpointConfig

	// EnableFallback uses local rate limiter if Redis is unavailable
	EnableFallback bool
}

// DefaultRedisRateLimiterConfig returns sensible defaults
func DefaultRedisRateLimiterConfig() RedisRateLimiterConfig {
	return RedisRateLimiterConfig{
		Redis:             DefaultRedisConfig(),
		RequestsPerMinute: DefaultRequestsPerMinute,
		BurstSize:         DefaultBurstSize,
		BlockDuration:     DefaultBlockDuration,
		Whitelist:         []string{localHostIP, "::1"},
		EndpointLimits: map[string]EndpointConfig{
			"/api/login": {
				RequestsPerMinute: LoginRequestsPerMinute,
				BurstSize:         LoginBurstSize,
				BlockDuration:     LoginBlockDuration,
			},
		},
		EnableFallback: true,
	}
}

// RedisRateLimiter implements distributed rate limiting using Redis
type RedisRateLimiter struct {
	mu        sync.RWMutex
	client    RedisClient
	config    RedisRateLimiterConfig
	whitelist map[string]bool
	fallback  *RateLimiter // Local fallback
}

// NewRedisRateLimiter creates a new Redis-backed rate limiter
func NewRedisRateLimiter(client RedisClient, cfg RedisRateLimiterConfig) (*RedisRateLimiter, error) {
	// Verify Redis connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		if !cfg.EnableFallback {
			return nil, fmt.Errorf("%w: %v", ErrRedisConnection, err)
		}
		// Log warning but continue with fallback
		fmt.Println("WARNING: Redis unavailable, using local rate limiter fallback")
	}

	rl := &RedisRateLimiter{
		client:    client,
		config:    cfg,
		whitelist: make(map[string]bool),
	}

	// Build whitelist
	for _, ip := range cfg.Whitelist {
		rl.whitelist[ip] = true
	}

	// Create fallback limiter
	if cfg.EnableFallback {
		fallbackCfg := Config{
			RequestsPerMinute: cfg.RequestsPerMinute,
			BurstSize:         cfg.BurstSize,
			BlockDuration:     cfg.BlockDuration,
			Whitelist:         cfg.Whitelist,
			EndpointLimits:    cfg.EndpointLimits,
			CleanupInterval:   5 * time.Minute,
		}
		rl.fallback = NewRateLimiter(fallbackCfg)
	}

	return rl, nil
}

// Lua script for atomic rate limiting with sliding window
// This script atomically:
// 1. Removes old entries outside the window
// 2. Counts remaining entries
// 3. Adds new entry if under limit
// 4. Returns (allowed, current_count, ttl)
const rateLimitScript = `
local key = KEYS[1]
local block_key = KEYS[2]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local block_duration = tonumber(ARGV[4])

-- Check if IP is blocked
local blocked_until = redis.call('GET', block_key)
if blocked_until then
    if tonumber(blocked_until) > now then
        return {0, -1, tonumber(blocked_until) - now}
    else
        redis.call('DEL', block_key)
    end
end

-- Remove old entries outside the sliding window
local window_start = now - window
redis.call('ZREMRANGEBYSCORE', key, '-inf', window_start)

-- Count current entries
local count = redis.call('ZCARD', key)

-- Check if we're at the limit
if count >= limit then
    -- Block the IP
    local block_until = now + block_duration
    redis.call('SET', block_key, block_until, 'EX', block_duration)
    return {0, count, block_duration}
end

-- Add current request timestamp
redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))

-- Set key expiration
redis.call('EXPIRE', key, window)

return {1, count + 1, 0}
`

// Allow checks if a request from IP should be allowed
func (rl *RedisRateLimiter) Allow(ip string) bool {
	return rl.AllowEndpoint(ip, "")
}

// AllowEndpoint checks if a request from IP to a specific endpoint should be allowed
func (rl *RedisRateLimiter) AllowEndpoint(ip, endpoint string) bool {
	// Normalize IP
	normalizedIP := normalizeIP(ip)

	// Check whitelist (protected by RLock)
	rl.mu.RLock()
	whitelisted := rl.whitelist[ip] || rl.whitelist[normalizedIP]
	rl.mu.RUnlock()
	if whitelisted {
		return true
	}
	// Check blacklist in Redis (hard block)
	if rl.IsBlacklisted(normalizedIP) || rl.IsBlacklisted(ip) {
		return false
	}

	// Get endpoint config
	cfg := rl.getEndpointConfig(endpoint)

	// Build keys
	keyPrefix := rl.config.Redis.KeyPrefix
	key := fmt.Sprintf("%s%s:%s", keyPrefix, normalizedIP, endpoint)
	blockKey := fmt.Sprintf("%sblock:%s:%s", keyPrefix, normalizedIP, endpoint)

	// Try Redis
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.ReadTimeout)
	defer cancel()

	now := time.Now().Unix()
	window := int64(60) // 1 minute sliding window

	result, err := rl.client.Eval(ctx, rateLimitScript, []string{key, blockKey},
		now, window, cfg.RequestsPerMinute, int64(cfg.BlockDuration.Seconds()))

	if err != nil {
		// Fall back to local rate limiter if Redis fails
		if rl.fallback != nil {
			return rl.fallback.AllowEndpoint(ip, endpoint)
		}
		// If no fallback and Redis fails, allow (fail open)
		return true
	}

	// Parse result
	results, ok := result.([]interface{})
	if !ok || len(results) < 1 {
		return true
	}

	allowed, _ := results[0].(int64)
	return allowed == 1
}

// getEndpointConfig returns the rate limit config for an endpoint
func (rl *RedisRateLimiter) getEndpointConfig(endpoint string) EndpointConfig {
	for prefix, cfg := range rl.config.EndpointLimits {
		if strings.HasPrefix(endpoint, prefix) {
			return cfg
		}
	}

	return EndpointConfig{
		RequestsPerMinute: rl.config.RequestsPerMinute,
		BurstSize:         rl.config.BurstSize,
		BlockDuration:     rl.config.BlockDuration,
	}
}

// Blacklist adds an IP to the permanent blacklist
func (rl *RedisRateLimiter) Blacklist(ip string) error {
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.WriteTimeout)
	defer cancel()

	key := fmt.Sprintf("%sblacklist:%s", rl.config.Redis.KeyPrefix, ip)
	return rl.client.Set(ctx, key, "1", 0) // No expiration
}

// Whitelist adds an IP to the whitelist
func (rl *RedisRateLimiter) Whitelist(ip string) {
	rl.mu.Lock()
	rl.whitelist[ip] = true
	rl.mu.Unlock()

	// Remove from blacklist if present
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.WriteTimeout)
	defer cancel()

	key := fmt.Sprintf("%sblacklist:%s", rl.config.Redis.KeyPrefix, ip)
	_ = rl.client.Del(ctx, key)
}

// RemoveFromBlacklist removes an IP from the blacklist
func (rl *RedisRateLimiter) RemoveFromBlacklist(ip string) error {
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.WriteTimeout)
	defer cancel()

	key := fmt.Sprintf("%sblacklist:%s", rl.config.Redis.KeyPrefix, ip)
	return rl.client.Del(ctx, key)
}

// IsBlacklisted checks if an IP is blacklisted in Redis.
func (rl *RedisRateLimiter) IsBlacklisted(ip string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.ReadTimeout)
	defer cancel()

	key := fmt.Sprintf("%sblacklist:%s", rl.config.Redis.KeyPrefix, ip)
	exists, err := rl.client.Exists(ctx, key)
	if err != nil {
		return false
	}
	return exists > 0
}

// Reset clears rate limit state for an IP or all IPs
func (rl *RedisRateLimiter) Reset(ip string) error {
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.WriteTimeout)
	defer cancel()

	if ip == "" {
		// Reset all - scan and delete matching keys
		var cursor uint64 = 0
		for {
			keys, nextCursor, err := rl.client.Scan(ctx, cursor, rl.config.Redis.KeyPrefix+"*", 100)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrRedisCommand, err)
			}

			if len(keys) > 0 {
				if err := rl.client.Del(ctx, keys...); err != nil {
					return fmt.Errorf("%w: %v", ErrRedisCommand, err)
				}
			}

			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
		return nil
	}

	// Reset specific IP - delete all related keys
	prefix := rl.config.Redis.KeyPrefix
	keys := []string{
		fmt.Sprintf("%s%s:*", prefix, ip),
		fmt.Sprintf("%sblock:%s:*", prefix, ip),
	}

	for _, pattern := range keys {
		var cursor uint64 = 0
		for {
			matchedKeys, nextCursor, err := rl.client.Scan(ctx, cursor, pattern, 100)
			if err != nil {
				break
			}

			if len(matchedKeys) > 0 {
				_ = rl.client.Del(ctx, matchedKeys...)
			}

			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
	}

	return nil
}

// Unblock removes block status for an IP
func (rl *RedisRateLimiter) Unblock(ip string) error {
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.WriteTimeout)
	defer cancel()

	// Delete all block keys for this IP
	var cursor uint64 = 0
	pattern := fmt.Sprintf("%sblock:%s:*", rl.config.Redis.KeyPrefix, ip)

	for {
		keys, nextCursor, err := rl.client.Scan(ctx, cursor, pattern, 100)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRedisCommand, err)
		}

		if len(keys) > 0 {
			if err := rl.client.Del(ctx, keys...); err != nil {
				return fmt.Errorf("%w: %v", ErrRedisCommand, err)
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return nil
}

// RemoveFromWhitelist removes an IP from the whitelist.
func (rl *RedisRateLimiter) RemoveFromWhitelist(ip string) {
	rl.mu.Lock()
	delete(rl.whitelist, ip)
	rl.mu.Unlock()
}

// GetStats returns rate limiter statistics
func (rl *RedisRateLimiter) GetStats() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rl.config.Redis.ReadTimeout)
	defer cancel()

	var activeCount int64 = 0
	var blockedCount int64 = 0
	var cursor uint64 = 0

	// Count rate limit entries
	for {
		keys, nextCursor, err := rl.client.Scan(ctx, cursor, rl.config.Redis.KeyPrefix+"[^b]*", 100)
		if err != nil {
			break
		}
		activeCount += int64(len(keys))
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// Count blocked entries + collect details
	cursor = 0
	limitedList := make([]map[string]interface{}, 0)
	limitedIPs := make(map[string]struct{})
	for {
		keys, nextCursor, err := rl.client.Scan(ctx, cursor, rl.config.Redis.KeyPrefix+"block:*", 100)
		if err != nil {
			break
		}
		blockedCount += int64(len(keys))
		for _, key := range keys {
			trimmed := strings.TrimPrefix(key, rl.config.Redis.KeyPrefix+"block:")
			ip := trimmed
			endpoint := ""
			if idx := strings.IndexByte(trimmed, ':'); idx != -1 {
				ip = trimmed[:idx]
				endpoint = trimmed[idx+1:]
			}
			if ip != "" {
				limitedIPs[ip] = struct{}{}
			}
			retryAfter := int64(0)
			if ttl, err := rl.client.TTL(ctx, key); err == nil && ttl > 0 {
				retryAfter = int64(ttl.Seconds())
			}
			limitedList = append(limitedList, map[string]interface{}{
				"ip":          ip,
				"endpoint":    endpoint,
				"retry_after": retryAfter,
			})
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// Load blacklist from Redis
	cursor = 0
	blacklist := make([]string, 0)
	for {
		keys, nextCursor, err := rl.client.Scan(ctx, cursor, rl.config.Redis.KeyPrefix+"blacklist:*", 100)
		if err != nil {
			break
		}
		for _, key := range keys {
			ip := strings.TrimPrefix(key, rl.config.Redis.KeyPrefix+"blacklist:")
			if ip != "" {
				blacklist = append(blacklist, ip)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// Whitelist (local)
	whitelist := make([]string, 0)
	rl.mu.RLock()
	for ip := range rl.whitelist {
		whitelist = append(whitelist, ip)
	}
	rl.mu.RUnlock()

	limitedIPsList := make([]string, 0, len(limitedIPs))
	for ip := range limitedIPs {
		limitedIPsList = append(limitedIPsList, ip)
	}

	return map[string]interface{}{
		"active_visitors":       activeCount,
		"blocked_ips":           blockedCount,
		"rate_limited_ips":      len(limitedIPsList),
		"rate_limited_list":     limitedList,
		"rate_limited_ips_list": limitedIPsList,
		"whitelist_count":       len(whitelist),
		"blacklist_count":       len(blacklist),
		"whitelisted_count":     len(whitelist),
		"blacklisted_count":     len(blacklist),
		"whitelist":             whitelist,
		"blacklist":             blacklist,
		"rate_limit":            rl.config.RequestsPerMinute,
		"requests_per_minute":   rl.config.RequestsPerMinute,
		"window_seconds":        60,
		"backend":               "redis",
		"redis_addresses":       rl.config.Redis.Addresses,
	}, nil
}

// Middleware returns HTTP middleware for rate limiting
func (rl *RedisRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		endpoint := r.URL.Path

		if !rl.AllowEndpoint(ip, endpoint) {
			cfg := rl.getEndpointConfig(endpoint)
			retryAfter := int(cfg.BlockDuration.Seconds())

			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.RequestsPerMinute))
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", time.Now().Add(cfg.BlockDuration).Format(time.RFC3339))
			w.Header().Set("X-RateLimit-Backend", "redis")
			http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
			return
		}

		// Add rate limit headers
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.config.RequestsPerMinute))
		w.Header().Set("X-RateLimit-Window", "60s")
		w.Header().Set("X-RateLimit-Backend", "redis")

		next.ServeHTTP(w, r)
	})
}

// Stop closes the Redis connection
func (rl *RedisRateLimiter) Stop() {
	if rl.client != nil {
		_ = rl.client.Close()
	}
	if rl.fallback != nil {
		rl.fallback.Stop()
	}
}

// HealthCheck verifies Redis connectivity
func (rl *RedisRateLimiter) HealthCheck(ctx context.Context) error {
	return rl.client.Ping(ctx)
}
