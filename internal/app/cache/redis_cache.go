// Package cache provides Redis-backed caching for Obsidian WAF.
// This integrates with Redis Cloud for distributed caching, session storage,
// and rate limiting state sharing across multiple WAF instances.
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache provides Redis-backed caching operations
type Cache struct {
	client *redis.Client
	prefix string
}

// New creates a new Redis cache with the given client
func New(client *redis.Client) *Cache {
	return &Cache{
		client: client,
		prefix: "obsidian:",
	}
}

// key prefixes the given key with the cache prefix
func (c *Cache) key(k string) string {
	return c.prefix + k
}

// Set stores a value with optional expiration
func (c *Cache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}
	return c.client.Set(ctx, c.key(key), data, expiration).Err()
}

// Get retrieves a value and unmarshals it into the target
func (c *Cache) Get(ctx context.Context, key string, target interface{}) error {
	data, err := c.client.Get(ctx, c.key(key)).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// Delete removes a key from the cache
func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.key(key)).Err()
}

// Exists checks if a key exists
func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, c.key(key)).Result()
	return n > 0, err
}

// Increment atomically increments a counter
func (c *Cache) Increment(ctx context.Context, key string) (int64, error) {
	return c.client.Incr(ctx, c.key(key)).Result()
}

// IncrementWithExpiry increments and sets expiry if key is new
func (c *Cache) IncrementWithExpiry(ctx context.Context, key string, expiry time.Duration) (int64, error) {
	pipe := c.client.Pipeline()
	incr := pipe.Incr(ctx, c.key(key))
	pipe.Expire(ctx, c.key(key), expiry)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

// RateLimitKey generates a rate limit key for an IP and endpoint
func RateLimitKey(ip, endpoint string) string {
	return fmt.Sprintf("ratelimit:%s:%s", ip, endpoint)
}

// BlockedIPKey generates a blocked IP key
func BlockedIPKey(ip string) string {
	return fmt.Sprintf("blocked:%s", ip)
}

// SessionKey generates a session key
func SessionKey(sessionID string) string {
	return fmt.Sprintf("session:%s", sessionID)
}

// =====================================================
// Rate Limiting Operations
// =====================================================

// CheckRateLimit checks if an IP has exceeded the rate limit
// Returns: (allowed bool, remaining int64, resetTime time.Time, err error)
func (c *Cache) CheckRateLimit(ctx context.Context, ip, endpoint string, limit int64, window time.Duration) (bool, int64, time.Time, error) {
	key := c.key(RateLimitKey(ip, endpoint))
	now := time.Now()
	windowStart := now.Add(-window)

	// Use sorted set with timestamp scores for sliding window
	pipe := c.client.Pipeline()

	// Remove old entries outside the window
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart.UnixNano()))

	// Count current entries
	countCmd := pipe.ZCard(ctx, key)

	// Add current request
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.UnixNano()), Member: now.UnixNano()})

	// Set expiry on the key
	pipe.Expire(ctx, key, window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, 0, time.Time{}, fmt.Errorf("rate limit check failed: %w", err)
	}

	count := countCmd.Val()
	remaining := limit - count - 1 // -1 for current request
	if remaining < 0 {
		remaining = 0
	}

	resetTime := now.Add(window)

	// Check if limit exceeded
	if count >= limit {
		return false, remaining, resetTime, nil
	}

	return true, remaining, resetTime, nil
}

// BlockIP blocks an IP for a duration
func (c *Cache) BlockIP(ctx context.Context, ip string, duration time.Duration, reason string) error {
	key := c.key(BlockedIPKey(ip))
	data := map[string]interface{}{
		"blocked_at": time.Now().UTC().Format(time.RFC3339),
		"duration":   duration.String(),
		"reason":     reason,
	}
	return c.Set(ctx, key, data, duration)
}

// IsIPBlocked checks if an IP is blocked
func (c *Cache) IsIPBlocked(ctx context.Context, ip string) (bool, error) {
	key := c.key(BlockedIPKey(ip))
	exists, err := c.client.Exists(ctx, key).Result()
	return exists > 0, err
}

// UnblockIP removes an IP from the blocklist
func (c *Cache) UnblockIP(ctx context.Context, ip string) error {
	return c.client.Del(ctx, c.key(BlockedIPKey(ip))).Err()
}

// =====================================================
// Session Operations
// =====================================================

// Session represents a user session
type Session struct {
	UserID    int       `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	IPAddress string    `json:"ip_address"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// StoreSession stores a session in Redis
func (c *Cache) StoreSession(ctx context.Context, sessionID string, session *Session) error {
	ttl := time.Until(session.ExpiresAt)
	if ttl <= 0 {
		ttl = time.Hour // Default 1 hour if expiry is invalid
	}
	return c.Set(ctx, SessionKey(sessionID), session, ttl)
}

// GetSession retrieves a session from Redis
func (c *Cache) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var session Session
	err := c.Get(ctx, SessionKey(sessionID), &session)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// DeleteSession removes a session
func (c *Cache) DeleteSession(ctx context.Context, sessionID string) error {
	return c.Delete(ctx, SessionKey(sessionID))
}

// =====================================================
// Threat Intelligence Caching
// =====================================================

// ThreatInfo represents cached threat information
type ThreatInfo struct {
	IP          string    `json:"ip"`
	ThreatLevel string    `json:"threat_level"`
	Source      string    `json:"source"`
	Reason      string    `json:"reason"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	HitCount    int       `json:"hit_count"`
}

// CacheThreatInfo caches threat information for an IP
func (c *Cache) CacheThreatInfo(ctx context.Context, info *ThreatInfo, ttl time.Duration) error {
	key := fmt.Sprintf("threat:%s", info.IP)
	return c.Set(ctx, key, info, ttl)
}

// GetThreatInfo retrieves cached threat information
func (c *Cache) GetThreatInfo(ctx context.Context, ip string) (*ThreatInfo, error) {
	key := fmt.Sprintf("threat:%s", ip)
	var info ThreatInfo
	err := c.Get(ctx, key, &info)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// =====================================================
// GeoIP Caching
// =====================================================

// GeoInfo represents cached GeoIP information
type GeoInfo struct {
	IP          string    `json:"ip"`
	Country     string    `json:"country"`
	CountryCode string    `json:"country_code"`
	Region      string    `json:"region"`
	City        string    `json:"city"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	ISP         string    `json:"isp"`
	CachedAt    time.Time `json:"cached_at"`
}

// CacheGeoInfo caches GeoIP information
func (c *Cache) CacheGeoInfo(ctx context.Context, info *GeoInfo, ttl time.Duration) error {
	key := fmt.Sprintf("geoip:%s", info.IP)
	info.CachedAt = time.Now()
	return c.Set(ctx, key, info, ttl)
}

// GetGeoInfo retrieves cached GeoIP information
func (c *Cache) GetGeoInfo(ctx context.Context, ip string) (*GeoInfo, error) {
	key := fmt.Sprintf("geoip:%s", ip)
	var info GeoInfo
	err := c.Get(ctx, key, &info)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// =====================================================
// Statistics & Metrics
// =====================================================

// IncrementStat increments a statistic counter
func (c *Cache) IncrementStat(ctx context.Context, stat string, delta int64) (int64, error) {
	key := fmt.Sprintf("stats:%s", stat)
	return c.client.IncrBy(ctx, c.key(key), delta).Result()
}

// GetStat retrieves a statistic value
func (c *Cache) GetStat(ctx context.Context, stat string) (int64, error) {
	key := fmt.Sprintf("stats:%s", stat)
	val, err := c.client.Get(ctx, c.key(key)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

// GetAllStats retrieves all statistics
func (c *Cache) GetAllStats(ctx context.Context) (map[string]int64, error) {
	pattern := c.key("stats:*")
	keys, err := c.client.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	stats := make(map[string]int64)
	for _, key := range keys {
		val, err := c.client.Get(ctx, key).Int64()
		if err == nil {
			// Remove prefix to get stat name
			statName := key[len(c.key("stats:")):]
			stats[statName] = val
		}
	}
	return stats, nil
}

// =====================================================
// Pub/Sub for Real-time Updates
// =====================================================

// Publish publishes a message to a channel
func (c *Cache) Publish(ctx context.Context, channel string, message interface{}) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	return c.client.Publish(ctx, c.key(channel), data).Err()
}

// Subscribe subscribes to a channel and returns a channel for messages
func (c *Cache) Subscribe(ctx context.Context, channel string) (<-chan string, func()) {
	pubsub := c.client.Subscribe(ctx, c.key(channel))
	ch := make(chan string, 100)

	go func() {
		defer close(ch)
		for msg := range pubsub.Channel() {
			select {
			case ch <- msg.Payload:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, func() { pubsub.Close() }
}

// =====================================================
// Health Check
// =====================================================

// Ping checks Redis connectivity
func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Info returns Redis server info
func (c *Cache) Info(ctx context.Context) (string, error) {
	return c.client.Info(ctx).Result()
}
