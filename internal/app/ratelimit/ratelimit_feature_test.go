package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeRedisClient is a minimal in-memory RedisClient for feature tests.
type fakeRedisClient struct {
	mu   sync.Mutex
	data map[string]string
}

func newFakeRedisClient() *fakeRedisClient {
	return &fakeRedisClient{data: make(map[string]string)}
}

func (f *fakeRedisClient) Incr(ctx context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return 1, nil
}

func (f *fakeRedisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return nil
}

func (f *fakeRedisClient) Get(ctx context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (f *fakeRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = fmt.Sprintf("%v", value)
	return nil
}

func (f *fakeRedisClient) Del(ctx context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.data, k)
	}
	return nil
}

func (f *fakeRedisClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, k := range keys {
		if _, ok := f.data[k]; ok {
			count++
		}
	}
	return count, nil
}

func (f *fakeRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	return []interface{}{int64(1)}, nil
}

func (f *fakeRedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	return 0, nil
}

func (f *fakeRedisClient) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	return []string{}, 0, nil
}

func (f *fakeRedisClient) Ping(ctx context.Context) error { return nil }
func (f *fakeRedisClient) Close() error                   { return nil }

func TestRateLimiter_BlacklistBlocks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Whitelist = nil
	rl := NewRateLimiter(cfg)

	ip := "1.2.3.4"
	rl.Blacklist(ip)

	if rl.AllowEndpoint(ip, "/api/health") {
		t.Fatal("expected blacklisted IP to be blocked")
	}
}

func TestRateLimiter_WhitelistAllows(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Whitelist = nil
	rl := NewRateLimiter(cfg)

	ip := "1.2.3.4"
	rl.Whitelist(ip)

	if !rl.AllowEndpoint(ip, "/api/health") {
		t.Fatal("expected whitelisted IP to be allowed")
	}
}

func TestRateLimiter_RateLimitTriggers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Whitelist = nil
	cfg.RequestsPerMinute = 1
	rl := NewRateLimiter(cfg)

	ip := "1.2.3.4"
	if !rl.AllowEndpoint(ip, "/api/health") {
		t.Fatal("first request should be allowed")
	}
	if rl.AllowEndpoint(ip, "/api/health") {
		t.Fatal("second request should be blocked by rate limit")
	}
}

func TestRateLimiter_RedisBlacklistBlocks(t *testing.T) {
	fake := newFakeRedisClient()
	rcfg := DefaultRedisRateLimiterConfig()
	rcfg.Whitelist = nil
	redisRL, err := NewRedisRateLimiter(fake, rcfg)
	if err != nil {
		t.Fatalf("failed to init redis rate limiter: %v", err)
	}

	ip := "1.2.3.4"
	if err := redisRL.Blacklist(ip); err != nil {
		t.Fatalf("failed to blacklist ip: %v", err)
	}

	rl := NewRateLimiter(DefaultConfig())
	rl.SetRedisBackend(redisRL)

	if rl.AllowEndpoint(ip, "/api/health") {
		t.Fatal("expected redis-blacklisted IP to be blocked")
	}
}
