// Package requestid provides request ID generation and propagation for Obsidian WAF.
// This implementation generates unique request IDs and propagates them through
// context for distributed tracing and log correlation.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// contextKey type for context values
type contextKey string

const (
	// RequestIDKey is the context key for request ID
	RequestIDKey contextKey = "request_id"

	// HeaderRequestID is the HTTP header for request ID
	HeaderRequestID = "X-Request-ID"

	// HeaderCorrelationID is the HTTP header for correlation ID (from client)
	HeaderCorrelationID = "X-Correlation-ID"

	// HeaderTraceID is the HTTP header for distributed trace ID
	HeaderTraceID = "X-Trace-ID"
)

var (
	// Counter for sequential component of request ID
	counter uint64

	// Instance ID to differentiate between server instances
	instanceID string
)

func init() {
	// Generate a short instance ID on startup
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	instanceID = hex.EncodeToString(b)
}

// Config for request ID middleware
type Config struct {
	// TrustHeader trusts incoming X-Request-ID header from clients
	TrustHeader bool

	// TrustedProxies list of IPs that can set X-Request-ID
	TrustedProxies []string

	// Generator custom ID generator function
	Generator func() string

	// IncludeTimestamp adds timestamp to generated IDs
	IncludeTimestamp bool

	// IncludeInstanceID adds server instance ID to generated IDs
	IncludeInstanceID bool
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		TrustHeader:       false, // Don't trust client headers by default
		TrustedProxies:    []string{"127.0.0.1", "::1"},
		Generator:         nil, // Use default generator
		IncludeTimestamp:  true,
		IncludeInstanceID: true,
	}
}

// Generate creates a new unique request ID
// Format: <timestamp>-<instance>-<counter>-<random>
// Example: 1706745600-a1b2-000042-c3d4e5f6
func Generate() string {
	// Increment counter atomically
	count := atomic.AddUint64(&counter, 1)

	// Generate random suffix
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	random := hex.EncodeToString(b)

	// Get current timestamp (seconds since epoch, base36 for compactness)
	ts := time.Now().Unix()

	return fmt.Sprintf("%x-%s-%06x-%s", ts, instanceID, count, random)
}

// GenerateShort creates a shorter request ID (16 chars)
// Format: <timestamp-hex>-<random>
func GenerateShort() string {
	ts := time.Now().UnixNano()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%s", ts&0xFFFFFFFF, hex.EncodeToString(b))
}

// GenerateUUID creates a UUID v4 style request ID
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	// Set version 4 and variant bits
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// FromContext retrieves the request ID from context
func FromContext(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}

// NewContext returns a new context with request ID
func NewContext(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// Middleware returns HTTP middleware that generates/propagates request IDs
func Middleware(cfg Config) func(http.Handler) http.Handler {
	trustedProxies := make(map[string]bool)
	for _, ip := range cfg.TrustedProxies {
		trustedProxies[ip] = true
	}

	generator := cfg.Generator
	if generator == nil {
		generator = Generate
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var requestID string

			// Check for existing request ID header
			if cfg.TrustHeader {
				// Check if request is from trusted proxy
				clientIP := getClientIP(r)
				if trustedProxies[clientIP] {
					// Trust the header from trusted proxies
					requestID = r.Header.Get(HeaderRequestID)
					if requestID == "" {
						requestID = r.Header.Get(HeaderCorrelationID)
					}
				}
			}

			// Generate new ID if not found
			if requestID == "" {
				requestID = generator()
			}

			// Set response header
			w.Header().Set(HeaderRequestID, requestID)

			// Add to context
			ctx := NewContext(r.Context(), requestID)
			r = r.WithContext(ctx)

			// Continue with request
			next.ServeHTTP(w, r)
		})
	}
}

// MiddlewareWithLogging returns middleware that also logs request start/end
func MiddlewareWithLogging(cfg Config, logFn func(requestID, method, path, clientIP string, duration time.Duration, statusCode int)) func(http.Handler) http.Handler {
	innerMiddleware := Middleware(cfg)

	return func(next http.Handler) http.Handler {
		return innerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			requestID := FromContext(r.Context())
			clientIP := getClientIP(r)

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			// Log the request
			if logFn != nil {
				logFn(requestID, r.Method, r.URL.Path, clientIP, time.Since(start), wrapped.statusCode)
			}
		}))
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// getClientIP extracts client IP from request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
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
	ip := r.RemoteAddr
	for i := len(ip) - 1; i >= 0; i-- {
		if ip[i] == ':' {
			return ip[:i]
		}
		if ip[i] == ']' {
			return ip
		}
	}
	return ip
}

// ExtractFromRequest gets request ID from HTTP request
func ExtractFromRequest(r *http.Request) string {
	// First try context
	if id := FromContext(r.Context()); id != "" {
		return id
	}

	// Then try headers
	if id := r.Header.Get(HeaderRequestID); id != "" {
		return id
	}
	if id := r.Header.Get(HeaderCorrelationID); id != "" {
		return id
	}
	if id := r.Header.Get(HeaderTraceID); id != "" {
		return id
	}

	return ""
}

// SetHeader adds request ID to outgoing HTTP request
func SetHeader(r *http.Request, requestID string) {
	r.Header.Set(HeaderRequestID, requestID)
}

// Handler wraps an http.HandlerFunc with request ID
func Handler(cfg Config, h http.HandlerFunc) http.HandlerFunc {
	middleware := Middleware(cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		middleware(h).ServeHTTP(w, r)
	}
}
