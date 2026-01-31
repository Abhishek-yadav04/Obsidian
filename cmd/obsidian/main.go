package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/internal/app/api"
	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/corazawaf/coraza/v3/internal/app/ratelimit"
	"github.com/corazawaf/coraza/v3/internal/app/report"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/corazawaf/coraza/v3/internal/app/threat"
	"github.com/corazawaf/coraza/v3/internal/app/waf"
	"github.com/corazawaf/coraza/v3/types"
)

// Application version
const (
	AppName    = "Obsidian Sentinel WAF"
	AppVersion = "2.0.0"
)

// Metrics for observability
var (
	totalRequests   int64
	blockedRequests int64
	startTime       = time.Now()
)

// Global instances for enterprise features
var (
	threatIntel *threat.ThreatIntel
	rateLimiter *ratelimit.RateLimiter
	reportGen   *report.Generator
)

// UserClaims for JWT authentication context
type UserClaims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
}

type contextKey string

const userContextKey contextKey = "user"

//go:embed ui/*
var uiAssets embed.FS

func main() {
	port := flag.Int("port", 8082, "Port to run the server on")
	dev := flag.Bool("dev", false, "Run in dev mode")
	flag.Parse()

	// Validate required environment variables
	if os.Getenv("OBSIDIAN_JWT_SECRET") == "" {
		log.Println("WARNING: OBSIDIAN_JWT_SECRET not set. Using default (insecure for production)")
	}

	// Initialize Store
	s := store.NewStore("data.json")
	if *dev {
		fmt.Println("Running in Dev Mode")
	}

	// Initialize Enterprise Features
	threatIntel = threat.NewThreatIntel(
		threat.WithPersistPath("threats.json"),
	)
	rateLimiter = ratelimit.NewRateLimiter(ratelimit.DefaultConfig())
	reportGen = report.NewGenerator()

	// Initialize WAF
	wafEngine, err := waf.NewWAF(s)
	if err != nil {
		log.Fatal(err)
	}

	// Initialize API
	apiHandler := api.NewAPI(s)

	// Router
	mux := http.NewServeMux()

	// API Routes - Public (no auth required)
	mux.HandleFunc("/api/login", apiHandler.HandleLogin)
	mux.HandleFunc("/api/health", handleHealth)

	// API Routes - Protected (auth required)
	mux.HandleFunc("/api/stats", authMiddleware(apiHandler.HandleStats))
	mux.HandleFunc("/api/logs", authMiddleware(apiHandler.HandleLogs))
	mux.HandleFunc("/api/rules", authMiddleware(apiHandler.HandleRules))
	mux.HandleFunc("/api/rules/create", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleCreateRule)))
	mux.HandleFunc("/api/rules/update", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleUpdateRule)))
	mux.HandleFunc("/api/rules/delete", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleDeleteRule)))
	mux.HandleFunc("/api/rules/test", authMiddleware(apiHandler.HandleTestRule))
	mux.HandleFunc("/api/ws", apiHandler.HandleWS)
	mux.HandleFunc("/api/export", authMiddleware(handleExport(s)))
	mux.HandleFunc("/api/metrics", authMiddleware(handleMetrics))
	mux.HandleFunc("/api/admin/users", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleUsers)))
	mux.HandleFunc("/api/admin/audit", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleAuditLogs)))

	// Threat Intelligence Routes
	mux.HandleFunc("/api/threats", authMiddleware(threatIntel.HandleThreats))
	mux.HandleFunc("/api/threats/block", authMiddleware(rbacMiddleware("Admin", threatIntel.HandleBlockIP)))
	mux.HandleFunc("/api/threats/stats", authMiddleware(threatIntel.HandleStats))

	// Rate Limiter Reset (Admin only) - useful for testing
	mux.HandleFunc("/api/admin/ratelimit/reset", authMiddleware(rbacMiddleware("Admin", handleRateLimitReset)))

	// Static Files (UI)
	uiFS, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(uiFS))
	mux.Handle("/", fileServer)

	// Middleware Stack: Security Headers -> Rate Limit -> Logging -> WAF -> Router
	finalHandler := securityHeadersMiddleware(rateLimiter.Middleware(loggingMiddleware(wafMiddleware(wafEngine, s, mux))))

	// Create server with timeouts
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      finalHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down gracefully...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		threatIntel.Stop()
		_ = s.Save()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Shutdown error: %v", err)
		}
	}()

	fmt.Printf("%s v%s is running on http://localhost:%d\n", AppName, AppVersion, *port)
	fmt.Println("Enterprise Features: Rate Limiting ✓ | Threat Intel ✓ | PDF Reports ✓")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// securityHeadersMiddleware adds security headers to all responses
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data:; font-src 'self' https://cdn.jsdelivr.net; connect-src 'self' ws: wss:")

		next.ServeHTTP(w, r)
	})
}

func wafMiddleware(engine coraza.WAF, s *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Panic recovery
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("WAF Panic recovered: %v", rec)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		requestStart := time.Now()
		ip := extractClientIP(r)
		userAgent := r.UserAgent()

		// Check threat intelligence first
		if entry, blocked := threatIntel.CheckIP(ip); blocked {
			atomic.AddInt64(&blockedRequests, 1)
			threatIntel.RecordHit(ip)

			// Log threat-blocked request
			s.AddLog(model.LogEntry{
				ID:         fmt.Sprintf("threat-%d", time.Now().UnixNano()),
				Timestamp:  requestStart,
				ClientIP:   ip,
				Method:     r.Method,
				URI:        r.URL.Path,
				RuleID:     0,
				Action:     "Deny",
				Status:     "ThreatBlocked",
				Details:    fmt.Sprintf("IP blocked by threat intelligence: %s", entry.Category),
				StatusCode: http.StatusForbidden,
				UserAgent:  userAgent,
			})

			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"blocked": true,
				"reason":  fmt.Sprintf("IP blocked by threat intelligence: %s", entry.Category),
			})
			return
		}

		// Skip WAF for static assets and websockets
		path := r.URL.Path
		if path == "/api/ws" {
			next.ServeHTTP(w, r)
			return
		}

		// Skip logging for static assets (css, js, images, fonts)
		isStaticAsset := strings.HasPrefix(path, "/css/") ||
			strings.HasPrefix(path, "/js/") ||
			strings.HasPrefix(path, "/assets/") ||
			strings.HasSuffix(path, ".css") ||
			strings.HasSuffix(path, ".js") ||
			strings.HasSuffix(path, ".png") ||
			strings.HasSuffix(path, ".jpg") ||
			strings.HasSuffix(path, ".svg") ||
			strings.HasSuffix(path, ".ico") ||
			strings.HasSuffix(path, ".woff") ||
			strings.HasSuffix(path, ".woff2")

		tx := engine.NewTransaction()

		// Ensure cleanup
		defer func() {
			tx.ProcessLogging()
			tx.Close()
		}()

		// 1. Process Request Headers
		tx.ProcessConnection(r.RemoteAddr, 0, "", 0)
		tx.ProcessURI(r.URL.String(), r.Method, r.Proto)
		for k, vr := range r.Header {
			for _, v := range vr {
				tx.AddRequestHeader(k, v)
			}
		}
		if it := tx.ProcessRequestHeaders(); it != nil {
			processInterruption(w, it, s, tx, r)
			return
		}

		// 2. Process Request Body
		if it, _ := tx.ProcessRequestBody(); it != nil {
			processInterruption(w, it, s, tx, r)
			return
		}

		// 3. Pass to application
		rec := &responseRecorder{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rec, r)

		// 4. Process Response
		for k, vr := range rec.Header() {
			for _, v := range vr {
				tx.AddResponseHeader(k, v)
			}
		}
		if it := tx.ProcessResponseHeaders(rec.statusCode, "HTTP/1.1"); it != nil {
			// Response phase interruption - log it
		}

		// Log safe request (skip static assets to reduce noise)
		if !tx.IsInterrupted() && !isStaticAsset {
			s.AddSafeLog(model.LogEntry{
				ID:         fmt.Sprintf("req-%d", time.Now().UnixNano()),
				Timestamp:  requestStart,
				ClientIP:   ip,
				Method:     r.Method,
				URI:        r.URL.Path,
				RuleID:     0,
				Action:     "Pass",
				Status:     "Safe",
				Details:    fmt.Sprintf("Request processed successfully in %v", time.Since(requestStart)),
				StatusCode: rec.statusCode,
				UserAgent:  userAgent,
			})
		} else if !tx.IsInterrupted() && isStaticAsset {
			// Just increment counter for static assets without logging
			s.IncrementSafeRequest()
		}
	})
}

func processInterruption(w http.ResponseWriter, it *types.Interruption, s *store.Store, tx types.Transaction, r *http.Request) {
	atomic.AddInt64(&blockedRequests, 1)

	// Log blocked request
	s.AddLog(model.LogEntry{
		ID:         fmt.Sprintf("block-%d", time.Now().UnixNano()),
		Timestamp:  time.Now(),
		ClientIP:   extractClientIP(r),
		Method:     r.Method,
		URI:        r.URL.Path,
		RuleID:     it.RuleID,
		Action:     it.Action,
		Status:     "Blocked",
		Details:    fmt.Sprintf("WAF Rule %d triggered: %s", it.RuleID, it.Action),
		StatusCode: http.StatusForbidden,
		UserAgent:  r.UserAgent(),
	})

	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte("WAF Blocked: " + it.Action))
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		atomic.AddInt64(&totalRequests, 1)
		next.ServeHTTP(w, r)
		// Access Log
		fmt.Printf("[%s] %s %s %v\n", time.Now().Format(time.RFC3339), r.Method, r.URL.Path, time.Since(start))
	})
}

// authMiddleware validates JWT tokens using cryptographic verification
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			authHeader = "Bearer " + r.URL.Query().Get("token")
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		// Use proper JWT verification from auth package
		claims, err := auth.VerifyJWT(tokenString)
		if err != nil {
			http.Error(w, `{"error": "Invalid token"}`, http.StatusUnauthorized)
			return
		}

		// Convert auth.Claims to local UserClaims
		userClaims := &UserClaims{
			UserID:   claims.UserID,
			Username: claims.Username,
			Role:     claims.Role,
			Exp:      claims.Exp,
		}

		if userClaims.Exp < time.Now().Unix() {
			http.Error(w, `{"error": "Token expired"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, userClaims)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// rbacMiddleware checks user role permissions
func rbacMiddleware(requiredRole string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(userContextKey).(*UserClaims)
		if !ok {
			http.Error(w, `{"error": "Forbidden"}`, http.StatusForbidden)
			return
		}

		// Role hierarchy: Admin > Analyst > Viewer
		allowed := false
		switch requiredRole {
		case "Viewer":
			allowed = true
		case "Analyst":
			allowed = claims.Role == "Admin" || claims.Role == "Analyst"
		case "Admin":
			allowed = claims.Role == "Admin"
		}

		if !allowed {
			http.Error(w, `{"error": "Insufficient permissions"}`, http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// handleHealth returns system health status
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"uptime":    time.Since(startTime).String(),
		"version":   AppVersion,
		"name":      AppName,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleRateLimitReset resets rate limits for testing
func handleRateLimitReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IP string `json:"ip"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// Reset rate limits
	rateLimiter.Reset(req.IP) // Empty string resets all

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rate limits reset successfully",
		"ip":      req.IP,
	})
}

// handleMetrics returns Prometheus-style metrics
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_requests":   atomic.LoadInt64(&totalRequests),
		"blocked_requests": atomic.LoadInt64(&blockedRequests),
		"uptime_seconds":   int64(time.Since(startTime).Seconds()),
		"memory_alloc_mb":  m.Alloc / 1024 / 1024,
		"memory_sys_mb":    m.Sys / 1024 / 1024,
		"goroutines":       runtime.NumGoroutine(),
	})
}

// handleExport generates PDF report
func handleExport(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := s.GetStats()
		logs := s.GetLogs()
		rules := s.GetRules()

		// Check if PDF is requested
		format := r.URL.Query().Get("format")

		if format == "pdf" {
			// Generate PDF report
			pdfData, err := reportGen.GeneratePDF(stats, logs, rules)
			if err != nil {
				http.Error(w, "Failed to generate PDF", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", "attachment; filename=obsidian-security-report.pdf")
			w.Write(pdfData)
			return
		}

		// Generate text report (default)
		textReport := reportGen.GenerateTextReport(stats, logs, rules)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=obsidian-security-report.txt")
		w.Write([]byte(textReport))
	}
}

// extractClientIP extracts the client IP from the request
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Extract from RemoteAddr
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	// Handle IPv6 brackets
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")
	return ip
}

// Helper for response interception (simplified)
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *responseRecorder) WriteHeader(code int) {
	rec.statusCode = code
	rec.ResponseWriter.WriteHeader(code)
}
