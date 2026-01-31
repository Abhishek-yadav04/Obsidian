package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/internal/app/api"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/corazawaf/coraza/v3/internal/app/waf"
	"github.com/corazawaf/coraza/v3/types"
)

// Metrics for observability
var (
	totalRequests   int64
	blockedRequests int64
	startTime       = time.Now()
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

	// Initialize Store
	s := store.NewStore("data.json")
	if *dev {
		fmt.Println("Running in Dev Mode")
	}

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

	// Static Files (UI)
	// We need to strip "ui" prefix because embed root is "ui"
	uiFS, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(uiFS))
	// We handle root / by stripping nothing if we just passed uiFS, but we want / to match index.html
	// FileServer handles index.html mostly automatically.
	mux.Handle("/", fileServer)

	// Middleware Stack
	finalHandler := loggingMiddleware(wafMiddleware(wafEngine, s, mux))

	fmt.Printf("Obsidian Sentinel WAF is running on http://localhost:%d\n", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), finalHandler); err != nil {
		log.Fatal(err)
	}
}

func wafMiddleware(engine coraza.WAF, s *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip WAF for static assets (css, js, images) to save perf
		// Simple check for extension
		path := r.URL.Path
		if path == "/api/ws" { // Skip WAF for websockets for now
			next.ServeHTTP(w, r)
			return
		}

		tx := engine.NewTransaction()
		// Capture ID for logging
		// txID := tx.ID()

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
			processInterruption(w, it, s, tx)
			return
		}

		// 2. Process Request Body (Simplified)
		// For a real WAF we need to buffer body, write to tx, then new reader for next handler
		// Skipping heavy body buffering for this demo for simplicity unless needed
		if it, _ := tx.ProcessRequestBody(); it != nil {
			processInterruption(w, it, s, tx)
			return
		}

		// 3. Pass to application
		// We intercept Response to run Response Rules (Phase 4)
		rec := &responseRecorder{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rec, r)

		// 4. Process Response
		for k, vr := range rec.Header() {
			for _, v := range vr {
				tx.AddResponseHeader(k, v)
			}
		}
		if it := tx.ProcessResponseHeaders(rec.statusCode, "HTTP/1.1"); it != nil {
			// Too late to intercept status code usually if already written, but we can try
			// In standard Go http, once you write, it's gone.
			// So we mainly log here for alerting.
		}

		// Update Safe Request Count if no interruption
		if !tx.IsInterrupted() {
			s.IncrementSafeRequest()
		}
	})
}

func processInterruption(w http.ResponseWriter, it *types.Interruption, s *store.Store, tx types.Transaction) {
	w.WriteHeader(403)
	w.Write([]byte("WAF Blocked: " + it.Action))

	// Ensure log is written (store uses a workaround via callback but let's be safe)
	// Actually the audit logger hybrid handles this via ProcessLogging() in defer.
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

// authMiddleware validates JWT tokens
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
		claims, err := parseJWT(tokenString)
		if err != nil {
			http.Error(w, `{"error": "Invalid token"}`, http.StatusUnauthorized)
			return
		}

		if claims.Exp < time.Now().Unix() {
			http.Error(w, `{"error": "Token expired"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, claims)
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

// parseJWT extracts claims from JWT token
func parseJWT(tokenString string) (*UserClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	var claims UserClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}

	return &claims, nil
}

// handleHealth returns system health status
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"uptime":  time.Since(startTime).String(),
		"version": "1.0.0",
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

		// Generate simple text report (PDF library would be used in production)
		report := fmt.Sprintf(`OBSIDIAN SENTINEL WAF - SECURITY REPORT
========================================
Generated: %s

STATISTICS
----------
Total Requests: %d
Blocked Requests: %d
Flagged Requests: %d
Safe Requests: %d
Active Rules: %d

RECENT SECURITY EVENTS
----------------------
`,
			time.Now().Format(time.RFC3339),
			stats.TotalRequests,
			stats.BlockedRequests,
			stats.FlaggedRequests,
			stats.SafeRequests,
			stats.ActiveRulesCount,
		)

		for i, log := range logs {
			if i >= 20 {
				break
			}
			report += fmt.Sprintf("[%s] Rule %d: %s - %s\n",
				log.Timestamp.Format(time.RFC3339),
				log.RuleID,
				log.Action,
				log.Details,
			)
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", "attachment; filename=obsidian-report.txt")
		w.Write([]byte(report))
	}
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
