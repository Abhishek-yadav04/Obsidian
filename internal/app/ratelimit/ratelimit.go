package ratelimit

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter implements a sliding window rate limiter
type RateLimiter struct {
	mu        sync.RWMutex
	visitors  map[string]*visitor
	rate      int           // requests per window
	window    time.Duration // window size
	cleanup   *time.Ticker
	whitelist map[string]bool
	blacklist map[string]bool
}

type visitor struct {
	timestamps []time.Time
	blocked    bool
	blockUntil time.Time
}

// Config for rate limiter
type Config struct {
	RequestsPerMinute int
	BurstSize         int
	BlockDuration     time.Duration
	Whitelist         []string
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		RequestsPerMinute: 60,
		BurstSize:         100,
		BlockDuration:     15 * time.Minute,
		Whitelist:         []string{"127.0.0.1", "::1"},
	}
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(cfg Config) *RateLimiter {
	rl := &RateLimiter{
		visitors:  make(map[string]*visitor),
		rate:      cfg.RequestsPerMinute,
		window:    time.Minute,
		whitelist: make(map[string]bool),
		blacklist: make(map[string]bool),
	}

	// Add whitelisted IPs
	for _, ip := range cfg.Whitelist {
		rl.whitelist[ip] = true
	}

	// Start cleanup goroutine
	rl.cleanup = time.NewTicker(5 * time.Minute)
	go rl.cleanupLoop()

	return rl
}

// cleanupLoop removes old visitor records
func (rl *RateLimiter) cleanupLoop() {
	for range rl.cleanup.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, v := range rl.visitors {
			// Remove if no recent requests and not blocked
			if len(v.timestamps) == 0 && !v.blocked {
				delete(rl.visitors, ip)
				continue
			}
			// Unblock if block duration expired
			if v.blocked && now.After(v.blockUntil) {
				v.blocked = false
			}
		}
		rl.mu.Unlock()
	}
}

// Allow checks if a request from IP should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	// Check whitelist
	if rl.whitelist[ip] {
		return true
	}

	// Check blacklist
	if rl.blacklist[ip] {
		return false
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	v, exists := rl.visitors[ip]
	if !exists {
		v = &visitor{
			timestamps: []time.Time{now},
		}
		rl.visitors[ip] = v
		return true
	}

	// Check if blocked
	if v.blocked {
		if now.After(v.blockUntil) {
			v.blocked = false
		} else {
			return false
		}
	}

	// Remove old timestamps outside window
	validTimestamps := make([]time.Time, 0, len(v.timestamps))
	for _, ts := range v.timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}
	v.timestamps = validTimestamps

	// Check rate
	if len(v.timestamps) >= rl.rate {
		v.blocked = true
		v.blockUntil = now.Add(15 * time.Minute)
		return false
	}

	// Add current request
	v.timestamps = append(v.timestamps, now)
	return true
}

// Blacklist adds an IP to the permanent blacklist
func (rl *RateLimiter) Blacklist(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.blacklist[ip] = true
}

// Whitelist adds an IP to the whitelist
func (rl *RateLimiter) Whitelist(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.whitelist[ip] = true
	delete(rl.blacklist, ip)
}

// GetStats returns rate limiter statistics
func (rl *RateLimiter) GetStats() map[string]interface{} {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	blocked := 0
	for _, v := range rl.visitors {
		if v.blocked {
			blocked++
		}
	}

	return map[string]interface{}{
		"active_visitors": len(rl.visitors),
		"blocked_ips":     blocked,
		"whitelist_count": len(rl.whitelist),
		"blacklist_count": len(rl.blacklist),
		"rate_limit":      rl.rate,
		"window_seconds":  int(rl.window.Seconds()),
	}
}

// Middleware returns HTTP middleware for rate limiting
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract IP from request
		ip := extractIP(r)

		if !rl.Allow(ip) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("X-RateLimit-Limit", "60")
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
			return
		}

		// Add rate limit headers
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Window", "60s")

		next.ServeHTTP(w, r)
	})
}

// extractIP gets the client IP from request
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

	// Fall back to RemoteAddr
	// Strip port if present
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
