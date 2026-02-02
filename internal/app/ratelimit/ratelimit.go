// Package ratelimit provides a high-performance, sharded rate limiter for Obsidian WAF.
// This implementation addresses the critical audit findings regarding global lock contention
// and supports per-endpoint rate limiting for enhanced security.
// ratelimit.go v2.o.
package ratelimit

import (
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// NumShards is the number of lock shards to reduce contention
	NumShards = 256

	// DefaultRequestsPerMinute is the default rate limit
	DefaultRequestsPerMinute = 200

	// DefaultBurstSize is the default burst allowance
	DefaultBurstSize = 300

	// DefaultBlockDuration is how long to block after exceeding limits
	DefaultBlockDuration = 1 * time.Minute

	// LoginRequestsPerMinute is stricter rate limit for login endpoint
	LoginRequestsPerMinute = 5

	// LoginBurstSize is the burst allowance for login
	LoginBurstSize = 10

	// LoginBlockDuration is how long to block after exceeding login limits
	LoginBlockDuration = 15 * time.Minute

	localHostIP = "127.0.0.1"
)

// EndpointConfig holds rate limit configuration for a specific endpoint
type EndpointConfig struct {
	RequestsPerMinute int
	BurstSize         int
	BlockDuration     time.Duration
}

// Config for rate limiter
type Config struct {
	// Global rate limits
	RequestsPerMinute int
	BurstSize         int
	BlockDuration     time.Duration

	// Whitelist of IPs that bypass rate limiting
	Whitelist []string

	// Endpoint-specific rate limits (path prefix -> config)
	EndpointLimits map[string]EndpointConfig

	// CleanupInterval for removing stale entries
	CleanupInterval time.Duration
}

// DefaultConfig returns sensible defaults with security best practices
func DefaultConfig() Config {
	return Config{
		RequestsPerMinute: DefaultRequestsPerMinute,
		BurstSize:         DefaultBurstSize,
		BlockDuration:     DefaultBlockDuration,
		Whitelist:         []string{localHostIP, "::1", "[::1]", "localhost"},
		CleanupInterval:   5 * time.Minute,
		EndpointLimits: map[string]EndpointConfig{
			"/api/login": {
				RequestsPerMinute: LoginRequestsPerMinute,
				BurstSize:         LoginBurstSize,
				BlockDuration:     LoginBlockDuration,
			},
			"/api/admin": {
				RequestsPerMinute: 30,
				BurstSize:         50,
				BlockDuration:     5 * time.Minute,
			},
		},
	}
}

// visitor tracks request timestamps for a single IP
type visitor struct {
	timestamps []time.Time
	blocked    atomic.Bool
	blockUntil atomic.Int64 // Unix timestamp
}

// shard holds visitors for a subset of IPs
type shard struct {
	mu       sync.RWMutex
	visitors map[string]*visitor
}

// RateLimiter implements a high-performance sharded sliding window rate limiter
type RateLimiter struct {
	shards    [NumShards]*shard
	config    Config
	whitelist sync.Map // map[string]bool
	blacklist sync.Map // map[string]bool

	// Metrics
	totalAllowed  atomic.Int64
	totalBlocked  atomic.Int64
	totalVisitors atomic.Int64

	// Lifecycle
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewRateLimiter creates a new high-performance rate limiter
func NewRateLimiter(cfg Config) *RateLimiter {
	rl := &RateLimiter{
		config: cfg,
		stopCh: make(chan struct{}),
	}

	// Initialize shards
	for i := range rl.shards {
		rl.shards[i] = &shard{
			visitors: make(map[string]*visitor),
		}
	}

	// Add whitelisted IPs
	for _, ip := range cfg.Whitelist {
		rl.whitelist.Store(ip, true)
	}

	// Start cleanup goroutine with proper shutdown support
	go rl.cleanupLoop()

	return rl
}

// Stop gracefully shuts down the rate limiter
func (rl *RateLimiter) Stop() {
	rl.stopOnce.Do(func() {
		close(rl.stopCh)
	})
}

// getShard returns the shard for a given IP using consistent hashing
func (rl *RateLimiter) getShard(ip string) *shard {
	h := fnv.New32a()
	h.Write([]byte(ip))
	return rl.shards[h.Sum32()%NumShards]
}

// cleanupLoop removes stale visitor records periodically
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.cleanup()
		}
	}
}

// cleanup removes stale entries from all shards
func (rl *RateLimiter) cleanup() {
	now := time.Now()
	windowStart := now.Add(-time.Minute)

	for _, sh := range rl.shards {
		sh.mu.Lock()
		for ip, v := range sh.visitors {
			// Unblock if block duration expired
			if v.blocked.Load() {
				blockUntil := time.Unix(v.blockUntil.Load(), 0)
				if now.After(blockUntil) {
					v.blocked.Store(false)
				}
			}

			// Remove stale timestamps
			validTimestamps := v.timestamps[:0]
			for _, ts := range v.timestamps {
				if ts.After(windowStart) {
					validTimestamps = append(validTimestamps, ts)
				}
			}
			v.timestamps = validTimestamps

			// Remove visitor if no recent requests and not blocked
			if len(v.timestamps) == 0 && !v.blocked.Load() {
				delete(sh.visitors, ip)
				rl.totalVisitors.Add(-1)
			}
		}
		sh.mu.Unlock()
	}
}

// Allow checks if a request from IP should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	return rl.AllowEndpoint(ip, "")
}

// AllowEndpoint checks if a request from IP to a specific endpoint should be allowed
func (rl *RateLimiter) AllowEndpoint(ip, endpoint string) bool {
	// Normalize localhost variants
	normalizedIP := normalizeIP(ip)

	// Check whitelist
	if _, ok := rl.whitelist.Load(ip); ok {
		rl.totalAllowed.Add(1)
		return true
	}
	if _, ok := rl.whitelist.Load(normalizedIP); ok {
		rl.totalAllowed.Add(1)
		return true
	}

	// Check blacklist
	if _, ok := rl.blacklist.Load(ip); ok {
		rl.totalBlocked.Add(1)
		return false
	}

	// Determine rate limit config for this endpoint
	cfg := rl.getEndpointConfig(endpoint)

	// Get the appropriate shard
	sh := rl.getShard(ip)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-time.Minute)

	v, exists := sh.visitors[ip]
	if !exists {
		v = &visitor{
			timestamps: []time.Time{now},
		}
		sh.visitors[ip] = v
		rl.totalVisitors.Add(1)
		rl.totalAllowed.Add(1)
		return true
	}

	// Check if blocked
	if v.blocked.Load() {
		blockUntil := time.Unix(v.blockUntil.Load(), 0)
		if now.After(blockUntil) {
			v.blocked.Store(false)
		} else {
			rl.totalBlocked.Add(1)
			return false
		}
	}

	// Remove old timestamps outside window
	validTimestamps := v.timestamps[:0]
	for _, ts := range v.timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}
	v.timestamps = validTimestamps

	// Check rate
	if len(v.timestamps) >= cfg.RequestsPerMinute {
		v.blocked.Store(true)
		v.blockUntil.Store(now.Add(cfg.BlockDuration).Unix())
		rl.totalBlocked.Add(1)
		return false
	}

	// Add current request
	v.timestamps = append(v.timestamps, now)
	rl.totalAllowed.Add(1)
	return true
}

// getEndpointConfig returns the rate limit config for an endpoint
// Uses longest-prefix matching to avoid security bypass issues
func (rl *RateLimiter) getEndpointConfig(endpoint string) EndpointConfig {
	// Get all prefixes and sort by length (longest first) for proper matching
	prefixes := make([]string, 0, len(rl.config.EndpointLimits))
	for prefix := range rl.config.EndpointLimits {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return len(prefixes[i]) > len(prefixes[j])
	})

	// Check for endpoint-specific config using longest-prefix match
	for _, prefix := range prefixes {
		if strings.HasPrefix(endpoint, prefix) {
			return rl.config.EndpointLimits[prefix]
		}
	}

	// Return global config
	return EndpointConfig{
		RequestsPerMinute: rl.config.RequestsPerMinute,
		BurstSize:         rl.config.BurstSize,
		BlockDuration:     rl.config.BlockDuration,
	}
}

// normalizeIP normalizes localhost variants to a consistent format
func normalizeIP(ip string) string {
	switch ip {
	case "[::1]", "::1", "localhost":
		return localHostIP
	default:
		return ip
	}
}

// Blacklist adds an IP to the permanent blacklist
func (rl *RateLimiter) Blacklist(ip string) {
	rl.blacklist.Store(ip, true)
	rl.whitelist.Delete(ip)
}

// Whitelist adds an IP to the whitelist
func (rl *RateLimiter) Whitelist(ip string) {
	rl.whitelist.Store(ip, true)
	rl.blacklist.Delete(ip)
}

// RemoveFromBlacklist removes an IP from the blacklist
func (rl *RateLimiter) RemoveFromBlacklist(ip string) {
	rl.blacklist.Delete(ip)
}

// RemoveFromWhitelist removes an IP from the whitelist
func (rl *RateLimiter) RemoveFromWhitelist(ip string) {
	rl.whitelist.Delete(ip)
}

// Reset clears rate limit state for an IP or all IPs if empty string
func (rl *RateLimiter) Reset(ip string) {
	if ip == "" {
		// Reset all shards
		for _, sh := range rl.shards {
			sh.mu.Lock()
			sh.visitors = make(map[string]*visitor)
			sh.mu.Unlock()
		}
		rl.totalVisitors.Store(0)
		return
	}

	sh := rl.getShard(ip)
	sh.mu.Lock()
	if _, exists := sh.visitors[ip]; exists {
		delete(sh.visitors, ip)
		rl.totalVisitors.Add(-1)
	}
	sh.mu.Unlock()
}

// Unblock removes block status for an IP
func (rl *RateLimiter) Unblock(ip string) {
	sh := rl.getShard(ip)
	sh.mu.Lock()
	if v, ok := sh.visitors[ip]; ok {
		v.blocked.Store(false)
		v.timestamps = nil
	}
	sh.mu.Unlock()
}

// GetStats returns rate limiter statistics
func (rl *RateLimiter) GetStats() map[string]interface{} {
	var whitelist []string
	rl.whitelist.Range(func(key, _ interface{}) bool {
		if ip, ok := key.(string); ok {
			whitelist = append(whitelist, ip)
		}
		return true
	})

	var blacklist []string
	rl.blacklist.Range(func(key, _ interface{}) bool {
		if ip, ok := key.(string); ok {
			blacklist = append(blacklist, ip)
		}
		return true
	})

	blockedCount := int64(0)
	var rateLimitedIPs []string
	for _, sh := range rl.shards {
		sh.mu.RLock()
		for ip, v := range sh.visitors {
			if v.blocked.Load() {
				blockedCount++
				rateLimitedIPs = append(rateLimitedIPs, ip)
			}
		}
		sh.mu.RUnlock()
	}

	return map[string]interface{}{
		"active_visitors":     rl.totalVisitors.Load(),
		"blocked_ips":         blockedCount,
		"rate_limited_ips":    len(rateLimitedIPs),
		"whitelist_count":     len(whitelist),
		"blacklist_count":     len(blacklist),
		"whitelisted_count":   len(whitelist),
		"blacklisted_count":   len(blacklist),
		"whitelist":           whitelist,
		"blacklist":           blacklist,
		"rate_limit":          rl.config.RequestsPerMinute,
		"requests_per_minute": rl.config.RequestsPerMinute,
		"window_seconds":      60,
		"total_allowed":       rl.totalAllowed.Load(),
		"total_blocked":       rl.totalBlocked.Load(),
		"shards":              NumShards,
	}
}

// Middleware returns HTTP middleware for rate limiting with endpoint awareness
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract IP from request - use security manager in production
		ip := extractIP(r)
		endpoint := r.URL.Path

		if !rl.AllowEndpoint(ip, endpoint) {
			cfg := rl.getEndpointConfig(endpoint)
			retryAfter := int(cfg.BlockDuration.Seconds())

			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.RequestsPerMinute))
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", time.Now().Add(cfg.BlockDuration).Format(time.RFC3339))
			http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
			return
		}

		// Add rate limit headers
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.config.RequestsPerMinute))
		w.Header().Set("X-RateLimit-Window", "60s")

		next.ServeHTTP(w, r)
	})
}

// extractIP gets the client IP from request
// Note: In production, use security.Manager.ExtractClientIP for proper validation
func extractIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxied requests)
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// Take first IP in chain
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr - strip port if present
	ip := r.RemoteAddr
	for i := len(ip) - 1; i >= 0; i-- {
		if ip[i] == ':' {
			return ip[:i]
		}
		if ip[i] == ']' {
			// IPv6 address
			return ip
		}
	}
	return ip
}
