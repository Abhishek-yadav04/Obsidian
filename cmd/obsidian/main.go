// Package main provides the entry point for Obsidian Sentinel WAF.
// This is an enterprise-grade Web Application Firewall with advanced security features.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/internal/app/alerts"
	"github.com/corazawaf/coraza/v3/internal/app/api"
	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/cache"
	"github.com/corazawaf/coraza/v3/internal/app/database"
	"github.com/corazawaf/coraza/v3/internal/app/geoip"
	"github.com/corazawaf/coraza/v3/internal/app/logging"
	"github.com/corazawaf/coraza/v3/internal/app/metrics"
	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/corazawaf/coraza/v3/internal/app/ratelimit"
	"github.com/corazawaf/coraza/v3/internal/app/report"
	"github.com/corazawaf/coraza/v3/internal/app/requestid"
	"github.com/corazawaf/coraza/v3/internal/app/security"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/corazawaf/coraza/v3/internal/app/threat"
	"github.com/corazawaf/coraza/v3/internal/app/waf"
	"github.com/corazawaf/coraza/v3/types"
)

// Application version
const (
	AppName    = "Obsidian Sentinel WAF"
	AppVersion = "2.1.0" // Enterprise Edition
)

// Metrics for observability
var (
	totalRequests   int64
	blockedRequests int64
	startTime       = time.Now()
)

// Global instances for enterprise features
var (
	threatIntel  *threat.ThreatIntel
	rateLimiter  *ratelimit.RateLimiter
	reportGen    *report.Generator
	geoIPService *geoip.Service
	alertService *alerts.Service
	securityMgr  *security.Manager
	logger       *logging.Logger
	metricsInst  *metrics.Metrics
	dbManager    *database.Manager // Database connection manager
	redisCache   *cache.Cache      // Redis cache for rate limiting & sessions
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
	// Parse flags
	port := flag.Int("port", 8082, "Port to run the server on")
	dev := flag.Bool("dev", false, "Run in development mode")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "json", "Log format (json, console)")
	flag.Parse()

	// Set authentication development mode EARLY - before any JWT operations
	auth.SetDevelopmentMode(*dev)

	// Initialize structured logging first
	logCfg := logging.DefaultConfig()
	logCfg.Level = *logLevel
	logCfg.Format = *logFormat
	if *dev {
		logCfg.Format = "console"
		logCfg.Level = "debug"
	}
	if err := logging.Init(logCfg); err != nil {
		fmt.Printf("Failed to initialize logging: %v\n", err)
		os.Exit(1)
	}
	logger = logging.Get()
	defer logger.Sync()

	// Initialize security manager
	secCfg := security.DefaultConfig()
	secCfg.RequireSecureSecret = !*dev // Require secret in production
	var err error
	securityMgr, err = security.NewManager(secCfg)
	if err != nil {
		logger.Error("Failed to initialize security manager") // Using zap fields - this would need adjustment for actual zap import

		os.Exit(1)
	}

	// Initialize Database Connections (Supabase PostgreSQL + Redis Cloud)
	ctx := context.Background()
	dbManager, err = database.New(ctx, database.DefaultConfig())
	if err != nil {
		logger.Warn("Database connection failed - using in-memory storage")
		logger.Warn(fmt.Sprintf("Database error: %v", err))
	} else {
		// Log connection status for each database
		if dbManager.HasPostgres() {
			if dbManager.IsLocalPostgres() {
				logger.Info("Connected to PostgreSQL (Local)")
			} else {
				logger.Info("Connected to PostgreSQL (Supabase)")
			}
			if err := dbManager.RunMigrations(ctx); err != nil {
				logger.Warn(fmt.Sprintf("Migration warning: %v", err))
			} else {
				logger.Info("Database migrations completed")
			}
			// Insert default users
			if err := dbManager.InsertDefaultUsers(ctx); err != nil {
				logger.Warn(fmt.Sprintf("Default users warning: %v", err))
			}
		} else {
			logger.Warn("PostgreSQL not connected - check DATABASE_URL")
		}
		if dbManager.HasRedis() {
			logger.Info("Connected to Redis (Redis Cloud)")
			// Initialize Redis cache for rate limiting, sessions, and caching
			redisCache = cache.New(dbManager.RedisClient())
			logger.Info("Redis cache initialized for rate limiting and sessions")
		} else {
			logger.Warn("Redis not connected - check REDIS_URL")
		}
	}

	// Initialize Store with database manager
	storeOpts := []store.StoreOption{}
	if dbManager != nil && dbManager.HasPostgres() {
		storeOpts = append(storeOpts, store.WithDatabaseManager(dbManager))
		logger.Info("Store configured with PostgreSQL backend")
	} else {
		logger.Warn("Store running without PostgreSQL - authentication will fail!")
	}
	s := store.NewStore("data.json", storeOpts...)
	if *dev {
		logger.Info("Running in Development Mode")
	}

	// Initialize Metrics
	metricsInst = metrics.New()

	// Initialize Enterprise Features
	threatIntel = threat.NewThreatIntel(
		threat.WithPersistPath("threats.json"),
	)

	rateLimiter = ratelimit.NewRateLimiter(ratelimit.DefaultConfig())

	reportGen = report.NewGenerator()

	// Initialize GeoIP service
	geoIPService, err = geoip.NewService(geoip.DefaultConfig())
	if err != nil {
		logger.Warn("GeoIP service not available (database not configured)")
	}

	// Initialize Alert service
	alertCfg := alerts.DefaultConfig()
	// Configure webhooks from environment
	if slackURL := os.Getenv("OBSIDIAN_SLACK_WEBHOOK"); slackURL != "" {
		alertCfg.Webhooks = append(alertCfg.Webhooks, alerts.WebhookConfig{
			Name:        "slack",
			Type:        alerts.WebhookSlack,
			URL:         slackURL,
			Enabled:     true,
			MinSeverity: alerts.SeverityMedium,
		})
	}
	if teamsURL := os.Getenv("OBSIDIAN_TEAMS_WEBHOOK"); teamsURL != "" {
		alertCfg.Webhooks = append(alertCfg.Webhooks, alerts.WebhookConfig{
			Name:        "teams",
			Type:        alerts.WebhookTeams,
			URL:         teamsURL,
			Enabled:     true,
			MinSeverity: alerts.SeverityMedium,
		})
	}
	alertService = alerts.NewService(alertCfg)

	// Initialize WAF
	wafEngine, err := waf.NewWAF(s)
	if err != nil {
		logger.Error("Failed to initialize WAF engine")
		os.Exit(1)
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
	mux.HandleFunc("/api/admin/users", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleUsers)))
	mux.HandleFunc("/api/admin/audit", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleAuditLogs)))

	// Prometheus Metrics endpoint
	mux.Handle("/metrics", metricsInst.Handler())

	// Internal metrics (JSON format)
	mux.HandleFunc("/api/metrics", authMiddleware(handleMetrics))

	// Threat Intelligence Routes
	mux.HandleFunc("/api/threats", authMiddleware(threatIntel.HandleThreats))
	mux.HandleFunc("/api/threats/block", authMiddleware(rbacMiddleware("Admin", threatIntel.HandleBlockIP)))
	mux.HandleFunc("/api/threats/stats", authMiddleware(threatIntel.HandleStats))

	// GeoIP Routes
	if geoIPService != nil {
		mux.HandleFunc("/api/geoip/lookup", authMiddleware(geoIPService.HandleLookup))
		mux.HandleFunc("/api/geoip/blocked", authMiddleware(rbacMiddleware("Admin", geoIPService.HandleBlockedCountries)))
		mux.HandleFunc("/api/geoip/metrics", authMiddleware(geoIPService.HandleMetrics))
	}

	// Alert Routes
	mux.HandleFunc("/api/alerts/webhooks", authMiddleware(rbacMiddleware("Admin", alertService.HandleWebhooks)))
	mux.HandleFunc("/api/alerts/webhooks/test", authMiddleware(rbacMiddleware("Admin", alertService.HandleTest)))
	mux.HandleFunc("/api/alerts/metrics", authMiddleware(alertService.HandleMetrics))

	// Rate Limiter Routes
	mux.HandleFunc("/api/ratelimit/blacklist", authMiddleware(rbacMiddleware("Admin", handleRateLimitBlacklist)))
	mux.HandleFunc("/api/ratelimit/whitelist", authMiddleware(rbacMiddleware("Admin", handleRateLimitWhitelist)))
	mux.HandleFunc("/api/admin/ratelimit/reset", authMiddleware(rbacMiddleware("Admin", handleRateLimitReset)))

	// Cache/Redis Routes
	mux.HandleFunc("/api/cache/stats", authMiddleware(handleCacheStats))
	mux.HandleFunc("/api/cache/health", authMiddleware(handleCacheHealth))

	// Static Files (UI)
	uiFS, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		logger.Error("Failed to load UI assets")
		os.Exit(1)
	}
	fileServer := http.FileServer(http.FS(uiFS))
	mux.Handle("/", fileServer)

	// Build middleware stack
	// Order: Request ID -> Security Headers -> Metrics -> Rate Limit -> GeoIP -> Logging -> WAF -> Router
	var finalHandler http.Handler = mux

	// WAF middleware
	finalHandler = wafMiddleware(wafEngine, s, finalHandler)

	// Logging middleware
	finalHandler = loggingMiddleware(finalHandler)

	// GeoIP middleware (if available)
	if geoIPService != nil {
		finalHandler = geoIPService.Middleware(finalHandler)
	}

	// Rate limiting middleware
	finalHandler = rateLimiter.Middleware(finalHandler)

	// Metrics middleware
	finalHandler = metricsInst.Middleware(finalHandler)

	// Security headers middleware
	finalHandler = securityHeadersMiddleware(finalHandler)

	// Request ID middleware
	reqIDCfg := requestid.DefaultConfig()
	finalHandler = requestid.Middleware(reqIDCfg)(finalHandler)

	// Create server with timeouts
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", *port),
		Handler:           finalHandler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		logger.Info("Shutting down gracefully...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Stop services
		threatIntel.Stop()
		rateLimiter.Stop()
		alertService.Stop()
		_ = s.Save()

		// Close database connections
		if dbManager != nil {
			if err := dbManager.Close(); err != nil {
				logger.Warn(fmt.Sprintf("Database close error: %v", err))
			} else {
				logger.Info("Database connections closed")
			}
		}

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("Shutdown error")
		}
	}()

	// Start uptime counter for metrics
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			metricsInst.IncrementUptime()
			// Update system metrics
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			metricsInst.SetSystemMetrics(runtime.NumGoroutine(), m.Alloc, m.Sys)
		}
	}()

	// Send startup alert
	_ = alertService.AlertSystemEvent(alerts.SeverityInfo,
		"Obsidian WAF Started",
		fmt.Sprintf("Obsidian Sentinel WAF v%s started on port %d", AppVersion, *port))

	// Print startup banner
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║           OBSIDIAN SENTINEL WAF - ENTERPRISE EDITION         ║")
	fmt.Printf("║                        Version %s                          ║\n", AppVersion)
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Server:      http://localhost:%d                            ║\n", *port)
	fmt.Printf("║  Metrics:     http://localhost:%d/metrics                    ║\n", *port)
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Features:                                                   ║")
	fmt.Println("║    ✓ Coraza WAF Engine (OWASP CRS Compatible)               ║")
	fmt.Println("║    ✓ Sharded Rate Limiting (256 shards)                     ║")
	fmt.Println("║    ✓ Threat Intelligence                                    ║")
	if geoIPService != nil {
		fmt.Println("║    ✓ GeoIP Blocking                                         ║")
	} else {
		fmt.Println("║    ○ GeoIP Blocking (database not configured)               ║")
	}
	fmt.Println("║    ✓ Webhook Alerts (Slack/Teams/Discord)                   ║")
	fmt.Println("║    ✓ Prometheus Metrics                                     ║")
	fmt.Println("║    ✓ Structured Logging (JSON)                              ║")
	fmt.Println("║    ✓ Request ID Tracing                                     ║")
	fmt.Println("║    ✓ PDF Report Generation                                  ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Database Connections:                                       ║")
	if dbManager != nil && dbManager.HasPostgres() {
		if dbManager.IsLocalPostgres() {
			fmt.Println("║    ✓ PostgreSQL (Local) - Connected                         ║")
		} else {
			fmt.Println("║    ✓ PostgreSQL (Supabase) - Connected                      ║")
		}
	} else {
		fmt.Println("║    ○ PostgreSQL - Not connected (using in-memory)           ║")
	}
	if dbManager != nil && dbManager.HasRedis() {
		fmt.Println("║    ✓ Redis (Redis Cloud) - Connected                        ║")
	} else {
		fmt.Println("║    ○ Redis - Not connected (using local cache)              ║")
	}
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("Server failed to start")
		os.Exit(1)
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
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data:; font-src 'self' https://cdn.jsdelivr.net; connect-src 'self' ws: wss:")

		// HSTS (only enable in production with HTTPS)
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

func wafMiddleware(engine coraza.WAF, s *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Panic recovery
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("WAF Panic recovered")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		requestStart := time.Now()
		ip := extractClientIP(r)
		userAgent := r.UserAgent()
		requestID := requestid.FromContext(r.Context())

		// Check threat intelligence first
		if entry, blocked := threatIntel.CheckIP(ip); blocked {
			atomic.AddInt64(&blockedRequests, 1)
			threatIntel.RecordHit(ip)

			// Record metrics
			metricsInst.RecordThreatBlock(entry.Category)

			// Send alert for threat intel block
			_ = alertService.AlertThreatIntel(ip, entry.Category, entry.Source)

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
				Details:    fmt.Sprintf("[%s] IP blocked by threat intelligence: %s", requestID, entry.Category),
				StatusCode: http.StatusForbidden,
				UserAgent:  userAgent,
			})

			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"blocked":    true,
				"reason":     fmt.Sprintf("IP blocked by threat intelligence: %s", entry.Category),
				"request_id": requestID,
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

		// Record WAF evaluation start
		evalStart := time.Now()

		// 1. Process Request Headers
		tx.ProcessConnection(r.RemoteAddr, 0, "", 0)
		tx.ProcessURI(r.URL.String(), r.Method, r.Proto)
		for k, vr := range r.Header {
			for _, v := range vr {
				tx.AddRequestHeader(k, v)
			}
		}

		metricsInst.RecordWAFRuleEvaluation("request_headers", time.Since(evalStart))

		if it := tx.ProcessRequestHeaders(); it != nil {
			processInterruption(w, it, s, r, requestID)
			return
		}

		// 2. Process Request Body
		evalStart = time.Now()
		if it, _ := tx.ProcessRequestBody(); it != nil {
			processInterruption(w, it, s, r, requestID)
			return
		}
		metricsInst.RecordWAFRuleEvaluation("request_body", time.Since(evalStart))

		// 3. Pass to application
		rec := &responseRecorder{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rec, r)

		// 4. Process Response
		evalStart = time.Now()
		for k, vr := range rec.Header() {
			for _, v := range vr {
				tx.AddResponseHeader(k, v)
			}
		}
		if it := tx.ProcessResponseHeaders(rec.statusCode, "HTTP/1.1"); it != nil {
			// Response phase interruption - log it
		}
		metricsInst.RecordWAFRuleEvaluation("response", time.Since(evalStart))

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
				Details:    fmt.Sprintf("[%s] Request processed successfully in %v", requestID, time.Since(requestStart)),
				StatusCode: rec.statusCode,
				UserAgent:  userAgent,
			})
		} else if !tx.IsInterrupted() && isStaticAsset {
			// Just increment counter for static assets without logging
			s.IncrementSafeRequest()
		}
	})
}

func processInterruption(w http.ResponseWriter, it *types.Interruption, s *store.Store, r *http.Request, requestID string) {
	atomic.AddInt64(&blockedRequests, 1)

	ip := extractClientIP(r)

	// Record metrics
	metricsInst.RecordWAFBlock(it.Action)
	metricsInst.RecordWAFRuleMatch(it.RuleID, "medium", "waf")

	// Send alert for high-severity blocks
	_ = alertService.AlertWAFBlock(it.RuleID, ip, r.URL.Path, it.Action)

	// Log blocked request
	s.AddLog(model.LogEntry{
		ID:         fmt.Sprintf("block-%d", time.Now().UnixNano()),
		Timestamp:  time.Now(),
		ClientIP:   ip,
		Method:     r.Method,
		URI:        r.URL.Path,
		RuleID:     it.RuleID,
		Action:     it.Action,
		Status:     "Blocked",
		Details:    fmt.Sprintf("[%s] WAF Rule %d triggered: %s", requestID, it.RuleID, it.Action),
		StatusCode: http.StatusForbidden,
		UserAgent:  r.UserAgent(),
	})

	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"blocked":    true,
		"rule_id":    it.RuleID,
		"action":     it.Action,
		"request_id": requestID,
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		atomic.AddInt64(&totalRequests, 1)

		next.ServeHTTP(w, r)

		requestID := requestid.FromContext(r.Context())
		logger.LogRequest(requestID, r.Method, r.URL.Path, extractClientIP(r), r.UserAgent(), 200, time.Since(start))
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
			metricsInst.RecordAuthAttempt(false, "invalid_token")
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
			metricsInst.RecordAuthAttempt(false, "token_expired")
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

	// Check database health
	dbStatus := map[string]interface{}{
		"postgres": "not_configured",
		"redis":    "not_configured",
	}
	if dbManager != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if dbManager.HasPostgres() {
			if err := dbManager.PostgresPool().Ping(ctx); err != nil {
				dbStatus["postgres"] = "error: " + err.Error()
			} else {
				dbStatus["postgres"] = "connected"
			}
		}
		if dbManager.HasRedis() {
			if err := dbManager.RedisClient().Ping(ctx).Err(); err != nil {
				dbStatus["redis"] = "error: " + err.Error()
			} else {
				dbStatus["redis"] = "connected"
			}
		}

		// Add connection stats
		stats := dbManager.GetStats()
		if stats.Postgres != nil {
			dbStatus["postgres_stats"] = stats.Postgres
		}
		if stats.Redis != nil {
			dbStatus["redis_stats"] = stats.Redis
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"uptime":    time.Since(startTime).String(),
		"version":   AppVersion,
		"name":      AppName,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"features": map[string]bool{
			"waf":                true,
			"rate_limiting":      true,
			"threat_intel":       true,
			"geoip":              geoIPService != nil,
			"alerts":             len(alertService.GetWebhooks()) > 0,
			"prometheus":         true,
			"structured_logging": true,
		},
		"databases": dbStatus,
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
	rateLimiter.Reset(req.IP)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rate limits reset successfully",
		"ip":      req.IP,
	})
}

// handleRateLimitBlacklist manages IP blacklist
func handleRateLimitBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		IP string `json:"ip"`
	}

	switch r.Method {
	case http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
			http.Error(w, `{"error": "Invalid IP"}`, http.StatusBadRequest)
			return
		}
		rateLimiter.Blacklist(req.IP)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "IP blacklisted",
			"ip":      req.IP,
		})

	case http.MethodDelete:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
			http.Error(w, `{"error": "Invalid IP"}`, http.StatusBadRequest)
			return
		}
		rateLimiter.RemoveFromBlacklist(req.IP)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "IP removed from blacklist",
			"ip":      req.IP,
		})

	default:
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleRateLimitWhitelist manages IP whitelist
func handleRateLimitWhitelist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		IP string `json:"ip"`
	}

	switch r.Method {
	case http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
			http.Error(w, `{"error": "Invalid IP"}`, http.StatusBadRequest)
			return
		}
		rateLimiter.Whitelist(req.IP)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "IP whitelisted",
			"ip":      req.IP,
		})

	case http.MethodDelete:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
			http.Error(w, `{"error": "Invalid IP"}`, http.StatusBadRequest)
			return
		}
		rateLimiter.RemoveFromWhitelist(req.IP)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "IP removed from whitelist",
			"ip":      req.IP,
		})

	default:
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleMetrics returns JSON metrics
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
		"rate_limiter":     rateLimiter.GetStats(),
		"threat_intel":     threatIntel.GetStats(),
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

// extractClientIP extracts the client IP using security manager's validation
func extractClientIP(r *http.Request) string {
	if securityMgr != nil {
		ip, _ := securityMgr.ExtractClientIP(
			r.RemoteAddr,
			r.Header.Get("X-Forwarded-For"),
			r.Header.Get("X-Real-IP"),
		)
		return ip
	}

	// Fallback to basic extraction
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	ip = strings.TrimPrefix(ip, "[")
	ip = strings.TrimSuffix(ip, "]")
	return ip
}

// Helper for response interception
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *responseRecorder) WriteHeader(code int) {
	rec.statusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

// handleCacheStats returns Redis cache statistics
func handleCacheStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := map[string]interface{}{
		"redis_connected": redisCache != nil,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}

	if redisCache != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Get stats from Redis
		stats, err := redisCache.GetAllStats(ctx)
		if err == nil {
			response["stats"] = stats
		}

		// Get connection pool stats from database manager
		if dbManager != nil {
			dbStats := dbManager.GetStats()
			if dbStats.Redis != nil {
				response["pool"] = dbStats.Redis
			}
		}
	}

	json.NewEncoder(w).Encode(response)
}

// handleCacheHealth checks Redis cache health
func handleCacheHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := map[string]interface{}{
		"status":    "unknown",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	if redisCache == nil {
		response["status"] = "not_configured"
		response["message"] = "Redis cache is not configured"
		json.NewEncoder(w).Encode(response)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := redisCache.Ping(ctx); err != nil {
		response["status"] = "error"
		response["error"] = err.Error()
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		response["status"] = "healthy"

		// Get Redis info
		if info, err := redisCache.Info(ctx); err == nil {
			// Parse some basic info
			lines := strings.Split(info, "\r\n")
			infoMap := make(map[string]string)
			for _, line := range lines {
				if strings.Contains(line, ":") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						infoMap[parts[0]] = parts[1]
					}
				}
			}
			response["redis_version"] = infoMap["redis_version"]
			response["connected_clients"] = infoMap["connected_clients"]
			response["used_memory_human"] = infoMap["used_memory_human"]
			response["uptime_in_days"] = infoMap["uptime_in_days"]
		}
	}

	json.NewEncoder(w).Encode(response)
}
