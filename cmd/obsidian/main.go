// Package main provides the entry point for Obsidian Sentinel WAF.
// This is an enterprise-grade Web Application Firewall with advanced security features.
package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/internal/app/alerts"
	"github.com/corazawaf/coraza/v3/internal/app/api"
	"github.com/corazawaf/coraza/v3/internal/app/apikeys"
	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/cache"
	"github.com/corazawaf/coraza/v3/internal/app/database"
	"github.com/corazawaf/coraza/v3/internal/app/geoip"
	"github.com/corazawaf/coraza/v3/internal/app/graphql"
	"github.com/corazawaf/coraza/v3/internal/app/hibp"
	"github.com/corazawaf/coraza/v3/internal/app/ipallow"
	"github.com/corazawaf/coraza/v3/internal/app/logging"
	"github.com/corazawaf/coraza/v3/internal/app/metrics"
	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/corazawaf/coraza/v3/internal/app/ratelimit"
	"github.com/corazawaf/coraza/v3/internal/app/report"
	"github.com/corazawaf/coraza/v3/internal/app/requestid"
	"github.com/corazawaf/coraza/v3/internal/app/respbody"
	"github.com/corazawaf/coraza/v3/internal/app/secrets"
	"github.com/corazawaf/coraza/v3/internal/app/security"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/corazawaf/coraza/v3/internal/app/threat"
	"github.com/corazawaf/coraza/v3/internal/app/waf"
	"github.com/corazawaf/coraza/v3/types"
	"go.uber.org/zap"
)

// Application version
const (
	AppName    = "Obsidian Sentinel WAF"
	AppVersion = "2.2.4" // Enterprise Edition - UI/UX Modernization & Advanced Security Analysis
)

// Metrics for observability
var (
	startTime = time.Now()
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
	appStore     *store.Store

	// Security Services (Enterprise Features)
	apiKeyMgr         *apikeys.Manager    // API Key management with scopes
	ipAllowMgr        *ipallow.Manager    // IP allowlist management
	hibpChecker       *hibp.Checker       // Password breach checking (HIBP)
	secretsManager    *secrets.Manager    // Secret hot-reload management
	graphqlAnalyzer   *graphql.Analyzer   // GraphQL security analysis
	respBodyInspector *respbody.Inspector // Response body DLP
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
const apiKeyContextKey contextKey = "api_key"

//go:embed ui/*
var uiAssets embed.FS

func envString(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func main() {
	// Parse flags
	port := flag.Int("port", 8082, "Port to run the server on")
	dev := flag.Bool("dev", false, "Run in development mode")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "json", "Log format (json, console)")
	flag.Parse()

	// CRITICAL: Load .env file FIRST - before any component checks environment variables
	// This ensures OBSIDIAN_JWT_SECRET and other env vars are available
	if err := database.LoadEnv(); err != nil {
		fmt.Printf("Warning: Failed to load .env file: %v\n", err)
	}

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

	// Initialize security manager (now .env is already loaded)
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
	storeOpts = append(storeOpts,
		store.WithRulesFile(envString("OBSIDIAN_WAF_CUSTOM_RULES", "rules/obsidian-custom.conf")),
		store.WithCRSPath(envString("OBSIDIAN_CRS_PATH", "")),
		store.WithCRSEnabled(envBool("OBSIDIAN_CRS_ENABLED", false)),
		store.WithCRSVersion(envString("OBSIDIAN_CRS_VERSION", "unknown")),
	)
	s := store.NewStore("data.json", storeOpts...)
	appStore = s

	// Sync WAF rules to PostgreSQL on startup
	if dbManager != nil && dbManager.HasPostgres() {
		if err := s.SyncRulesToDB(); err != nil {
			logger.Warn("Failed to sync WAF rules to PostgreSQL: " + err.Error())
		}
	}

	if *dev {
		logger.Info("Running in Development Mode")
	}

	// Initialize Metrics
	metricsInst = metrics.New()

	// Initialize Enterprise Features
	threatIntel = threat.NewThreatIntel(
		threat.WithPersistPath("threats.json"),
	)

	// Initialize Rate Limiter - use Redis if available, fallback to in-memory
	if dbManager != nil && dbManager.HasRedis() {
		redisAdapter := dbManager.NewRedisRateLimitAdapter()
		if redisAdapter != nil {
			redisRateLimiter, err := ratelimit.NewRedisRateLimiter(redisAdapter, ratelimit.DefaultRedisRateLimiterConfig())
			if err != nil {
				logger.Warn("Failed to create Redis rate limiter, falling back to in-memory: " + err.Error())
				rateLimiter = ratelimit.NewRateLimiter(ratelimit.DefaultConfig())
			} else {
				// Use Redis rate limiter - create wrapper that implements same interface
				rateLimiter = ratelimit.NewRateLimiter(ratelimit.DefaultConfig())
				rateLimiter.SetRedisBackend(redisRateLimiter)
				logger.Info("Rate limiter configured with Redis backend")
			}
		} else {
			rateLimiter = ratelimit.NewRateLimiter(ratelimit.DefaultConfig())
		}
	} else {
		rateLimiter = ratelimit.NewRateLimiter(ratelimit.DefaultConfig())
	}

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
	if discordURL := os.Getenv("OBSIDIAN_DISCORD_WEBHOOK"); discordURL != "" {
		alertCfg.Webhooks = append(alertCfg.Webhooks, alerts.WebhookConfig{
			Name:        "discord",
			Type:        alerts.WebhookDiscord,
			URL:         discordURL,
			Enabled:     true,
			MinSeverity: alerts.SeverityMedium,
		})
	}
	// Add built-in console logger webhook for alerts (always active)
	alertCfg.Webhooks = append(alertCfg.Webhooks, alerts.WebhookConfig{
		Name:        "console",
		Type:        alerts.WebhookGeneric,
		URL:         "internal://console",
		Enabled:     true,
		MinSeverity: alerts.SeverityHigh,
	})
	alertService = alerts.NewService(alertCfg)

	// Initialize Security Services (Enterprise Features)
	initSecurityServices()

	// Initialize WAF
	wafConfig := waf.Config{
		CRSEnabled:      envBool("OBSIDIAN_CRS_ENABLED", false),
		CRSPath:         envString("OBSIDIAN_CRS_PATH", ""),
		CRSMode:         envString("OBSIDIAN_CRS_MODE", "DetectionOnly"),
		CustomRulesPath: envString("OBSIDIAN_WAF_CUSTOM_RULES", "rules/obsidian-custom.conf"),
	}

	wafEngine, err := waf.NewWAF(s, wafConfig)
	if err != nil {
		logger.Warn(fmt.Sprintf("WAF engine initialization failed: %v — starting in degraded mode (no WAF protection)", err))
		// Create a minimal WAF with just SecRuleEngine On so the app starts
		wafEngine, err = coraza.NewWAF(coraza.NewWAFConfig().WithDirectives("SecRuleEngine On"))
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to create fallback WAF engine: %v", err))
			os.Exit(1)
		}
	}

	// Initialize API
	apiHandler := api.NewAPI(s)

	// Wire up HIBP checker to API handler if initialized
	if hibpChecker != nil {
		apiHandler.SetHIBPChecker(&hibpCheckerAdapter{checker: hibpChecker})
	}

	// Router
	mux := http.NewServeMux()

	// API Routes - Public (no auth required)
	mux.HandleFunc("/api/login", apiHandler.HandleLogin)
	mux.HandleFunc("/api/health", handleHealth)

	// OAuth Routes (Supabase integration)
	mux.HandleFunc("/api/auth/google", handleOAuthGoogle)
	mux.HandleFunc("/api/auth/github", handleOAuthGitHub)
	mux.HandleFunc("/api/auth/callback", handleOAuthCallback)

	// API Routes - Protected (supports both JWT and API key auth)
	// Read-only endpoints support API keys with "read" scope
	mux.HandleFunc("/api/stats", authOrAPIKeyMiddleware(apikeys.ScopeRead, apiHandler.HandleStats))
	mux.HandleFunc("/api/logs", authOrAPIKeyMiddleware(apikeys.ScopeRead, apiHandler.HandleLogs))
	mux.HandleFunc("/api/rules", authOrAPIKeyMiddleware(apikeys.ScopeRead, apiHandler.HandleRules))
	mux.HandleFunc("/api/rules/create", authOrAPIKeyMiddleware(apikeys.ScopeWrite, rbacMiddleware("Admin", apiHandler.HandleCreateRule)))
	mux.HandleFunc("/api/rules/update", authOrAPIKeyMiddleware(apikeys.ScopeWrite, rbacMiddleware("Admin", apiHandler.HandleUpdateRule)))
	mux.HandleFunc("/api/rules/delete", authOrAPIKeyMiddleware(apikeys.ScopeWrite, rbacMiddleware("Admin", apiHandler.HandleDeleteRule)))
	mux.HandleFunc("/api/rules/test", authOrAPIKeyMiddleware(apikeys.ScopeRead, apiHandler.HandleTestRule))
	mux.HandleFunc("/api/ws", apiHandler.HandleWS)
	mux.HandleFunc("/api/export", authOrAPIKeyMiddleware(apikeys.ScopeExport, handleExport(s)))
	mux.HandleFunc("/api/admin/users", authMiddleware(rbacMiddleware("Admin", apiHandler.HandleUsers)))
	mux.HandleFunc("/api/admin/audit", authOrAPIKeyMiddleware(apikeys.ScopeAdmin, rbacMiddleware("Admin", apiHandler.HandleAuditLogs)))
	mux.HandleFunc("/api/admin/rules/audit", authOrAPIKeyMiddleware(apikeys.ScopeAdmin, rbacMiddleware("Admin", apiHandler.HandleRuleAuditLogs)))
	mux.HandleFunc("/api/admin/crs/status", authOrAPIKeyMiddleware(apikeys.ScopeAdmin, rbacMiddleware("Admin", apiHandler.HandleCRSStatus)))
	mux.HandleFunc("/api/admin/crs/enable", authOrAPIKeyMiddleware(apikeys.ScopeAdmin, rbacMiddleware("Admin", apiHandler.HandleCRSEnable)))
	mux.HandleFunc("/api/admin/crs/disable", authOrAPIKeyMiddleware(apikeys.ScopeAdmin, rbacMiddleware("Admin", apiHandler.HandleCRSDisable)))

	// Prometheus Metrics endpoint
	mux.Handle("/metrics", metricsInst.Handler())

	// Internal metrics (JSON format) - supports API key with read scope
	mux.HandleFunc("/api/metrics", authOrAPIKeyMiddleware(apikeys.ScopeRead, handleMetrics))

	// Threat Intelligence Routes - read endpoints support API keys
	mux.HandleFunc("/api/threats", authOrAPIKeyMiddleware(apikeys.ScopeThreat, threatIntel.HandleThreats))
	mux.HandleFunc("/api/threats/block", authMiddleware(rbacMiddleware("Admin", threatIntel.HandleBlockIP)))
	mux.HandleFunc("/api/threats/stats", authOrAPIKeyMiddleware(apikeys.ScopeThreat, threatIntel.HandleStats))

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

	// Security Settings Routes
	mux.HandleFunc("/api/security/overview", authMiddleware(handleSecurityOverview))
	mux.HandleFunc("/api/security/password/check", authMiddleware(handlePasswordBreachCheck))
	mux.HandleFunc("/api/security/secrets/reload", authMiddleware(rbacMiddleware("Admin", handleSecretsReload)))
	mux.HandleFunc("/api/security/secrets/rotate-jwt", authMiddleware(rbacMiddleware("Admin", handleSecretsRotateJWT)))

	// API Key Management
	mux.HandleFunc("/api/security/apikeys", authMiddleware(handleAPIKeysList))
	mux.HandleFunc("/api/security/apikeys/create", authMiddleware(rbacMiddleware("Admin", handleAPIKeyCreate)))
	mux.HandleFunc("/api/security/apikeys/revoke", authMiddleware(rbacMiddleware("Admin", handleAPIKeyRevoke)))

	// IP Allowlist Management
	mux.HandleFunc("/api/security/ipallowlist", authMiddleware(handleIPAllowlist))
	mux.HandleFunc("/api/security/ipallowlist/add", authMiddleware(rbacMiddleware("Admin", handleIPAllowlistAdd)))
	mux.HandleFunc("/api/security/ipallowlist/remove", authMiddleware(rbacMiddleware("Admin", handleIPAllowlistRemove)))
	mux.HandleFunc("/api/security/ipallowlist/check", authMiddleware(handleIPAllowlistCheck))

	// GraphQL Security Routes (Enterprise)
	mux.HandleFunc("/api/security/graphql/analyze", authMiddleware(handleGraphQLAnalyze))
	mux.HandleFunc("/api/security/graphql/config", authMiddleware(rbacMiddleware("Admin", handleGraphQLConfig)))

	// Response Body Inspection Routes (Enterprise DLP)
	mux.HandleFunc("/api/security/respbody/config", authMiddleware(rbacMiddleware("Admin", handleRespBodyConfig)))
	mux.HandleFunc("/api/security/respbody/test", authMiddleware(rbacMiddleware("Admin", handleRespBodyTest)))

	// Static Files (UI)
	uiFS, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		logger.Error("Failed to load UI assets")
		os.Exit(1)
	}
	fileServer := http.FileServer(http.FS(uiFS))
	mux.Handle("/", fileServer)

	// Build middleware stack
	// Order: Request ID -> Security Headers -> IP Allowlist -> Metrics -> Rate Limit -> GeoIP -> Logging -> WAF -> Router
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

	// IP Allowlist middleware (enforces admin endpoint restrictions)
	finalHandler = ipAllowlistMiddleware(finalHandler)

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
		logger.Error("Server failed to start", zap.Error(err))
		os.Exit(1)
	}
}

// ipAllowlistMiddleware enforces IP allowlist for admin endpoints
func ipAllowlistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only enforce for admin endpoints when IP allowlist is enabled
		if ipAllowMgr != nil && ipAllowMgr.IsEnabled() {
			// Check if this is an admin endpoint
			path := r.URL.Path
			if strings.HasPrefix(path, "/api/admin/") ||
				strings.HasPrefix(path, "/api/security/") ||
				strings.HasPrefix(path, "/api/apikeys") {
				// Extract client IP
				clientIP := ipallow.ExtractIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"))

				// Check if IP is allowed
				if !ipAllowMgr.IsAllowed(clientIP) {
					// Check bypass header
					if bypassHeader := r.Header.Get("X-IP-Bypass"); bypassHeader != "" {
						if ipAllowMgr.CheckBypassHeader(bypassHeader) {
							next.ServeHTTP(w, r)
							return
						}
					}

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":   "IP address not in allowlist",
						"ip":      clientIP,
						"message": "Contact administrator to add your IP to the allowlist",
					})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
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
			threatIntel.RecordHit(ip)

			// Record metrics
			metricsInst.RecordThreatBlock(entry.Category)

			// Send alert for threat intel block
			_ = alertService.AlertThreatIntel(ip, entry.Category, entry.Source)

			// Log threat-blocked request
			s.AddLog(model.LogEntry{
				ID:             fmt.Sprintf("threat-%d", time.Now().UnixNano()),
				Timestamp:      requestStart,
				ClientIP:       ip,
				Method:         r.Method,
				URI:            r.URL.Path,
				RuleID:         0,
				Action:         "Deny",
				Status:         "ThreatBlocked",
				Details:        fmt.Sprintf("[%s] IP blocked by threat intelligence: %s", requestID, entry.Category),
				StatusCode:     http.StatusForbidden,
				UserAgent:      userAgent,
				ResponseTimeMs: float64(time.Since(requestStart).Microseconds()) / 1000.0,
			})

			// Persist attack log to PostgreSQL
			_ = s.AddAttackLog(ip, r.Method, r.URL.Path, 0, fmt.Sprintf("Threat Intelligence: %s", entry.Category), "high", "deny", http.StatusForbidden)

			// Persist threat to database
			_ = s.AddThreat(ip, "high", entry.Source, entry.Category, true)

			// Increment blocked stats in database
			s.IncrementDBStat("blocked_requests", 1)
			s.IncrementDBStat("threats_detected", 1)

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
			processInterruption(w, it, tx, s, r, requestID)
			return
		}

		// 2. Process Request Body
		evalStart = time.Now()
		if it, _ := tx.ProcessRequestBody(); it != nil {
			processInterruption(w, it, tx, s, r, requestID)
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
			elapsed := time.Since(requestStart)
			s.AddSafeLog(model.LogEntry{
				ID:             fmt.Sprintf("req-%d", time.Now().UnixNano()),
				Timestamp:      requestStart,
				ClientIP:       ip,
				Method:         r.Method,
				URI:            r.URL.Path,
				RuleID:         0,
				Action:         "Pass",
				Status:         "Safe",
				Details:        fmt.Sprintf("[%s] Request processed successfully in %v", requestID, elapsed),
				StatusCode:     rec.statusCode,
				UserAgent:      userAgent,
				ResponseTimeMs: float64(elapsed.Microseconds()) / 1000.0,
			})
			// Increment total requests in database
			s.IncrementDBStat("total_requests", 1)
		} else if !tx.IsInterrupted() && isStaticAsset {
			// Just increment counter for static assets without logging
			s.IncrementSafeRequest()
			// Still count static assets in database stats
			s.IncrementDBStat("total_requests", 1)
		}
	})
}

func processInterruption(w http.ResponseWriter, it *types.Interruption, tx types.Transaction, s *store.Store, r *http.Request, requestID string) {
	ip := extractClientIP(r)

	// Determine the real attack rule ID. CRS evaluation rules like 949110
	// (Inbound Anomaly Score Exceeded) or 959100 just do the final block;
	// the actual attack-specific rules (941xxx=XSS, 942xxx=SQLi, etc.)
	// are found in MatchedRules. Use the first attack-category rule for
	// classification so the analytics chart shows the real attack type.
	realRuleID := resolveAttackRuleID(it.RuleID, tx)

	// Record metrics
	metricsInst.RecordWAFBlock(it.Action)
	metricsInst.RecordWAFRuleMatch(realRuleID, "medium", "waf")

	// Send alert for high-severity blocks
	_ = alertService.AlertWAFBlock(realRuleID, ip, r.URL.Path, it.Action)

	// Log blocked request to in-memory store
	attackCategory := classifyRuleID(realRuleID)
	s.AddLog(model.LogEntry{
		ID:         fmt.Sprintf("block-%d", time.Now().UnixNano()),
		Timestamp:  time.Now(),
		ClientIP:   ip,
		Method:     r.Method,
		URI:        r.URL.Path,
		RuleID:     realRuleID,
		Action:     it.Action,
		Status:     "Blocked",
		Details:    fmt.Sprintf("[%s] %s - WAF Rule %d triggered: %s", requestID, attackCategory, realRuleID, it.Action),
		StatusCode: http.StatusForbidden,
		UserAgent:  r.UserAgent(),
	})

	// Persist attack log to PostgreSQL
	_ = s.AddAttackLog(ip, r.Method, r.URL.Path, realRuleID, fmt.Sprintf("%s (Rule %d)", attackCategory, realRuleID), "medium", it.Action, http.StatusForbidden)

	// Increment blocked stats in database
	s.IncrementDBStat("blocked_requests", 1)
	s.IncrementDBStat("threats_detected", 1)

	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"blocked":    true,
		"rule_id":    it.RuleID,
		"action":     it.Action,
		"request_id": requestID,
	})
}

// classifyRuleID maps an OWASP CRS / custom rule ID to a human-readable
// attack category using the standard CRS rule ID ranges.
func classifyRuleID(ruleID int) string {
	switch {
	case ruleID >= 941000 && ruleID < 942000:
		return "XSS Attack"
	case ruleID >= 942000 && ruleID < 943000:
		return "SQL Injection"
	case ruleID >= 932000 && ruleID < 933000:
		return "Remote Code Execution"
	case ruleID >= 930000 && ruleID < 931000:
		return "Local File Inclusion"
	case ruleID >= 931000 && ruleID < 932000:
		return "Remote File Inclusion"
	case ruleID >= 913000 && ruleID < 914000:
		return "Scanner Detection"
	case ruleID >= 920000 && ruleID < 921000:
		return "Protocol Violation"
	case ruleID >= 933000 && ruleID < 934000:
		return "PHP Injection"
	case ruleID >= 934000 && ruleID < 935000:
		return "Node.js Injection"
	case ruleID >= 943000 && ruleID < 944000:
		return "Session Fixation"
	case ruleID >= 944000 && ruleID < 945000:
		return "Java Attack"
	case ruleID >= 910000 && ruleID < 913000:
		return "Protocol Anomaly"
	default:
		return "WAF Block"
	}
}

// isCRSEvaluationRule returns true for CRS meta/evaluation rules that don't
// represent a specific attack type (e.g., 949110 = "Inbound Anomaly Score
// Exceeded", 959100 = "Outbound Anomaly Score Exceeded", 980xxx = logging,
// 900100-900999 = CRS setup/initialization).
func isCRSEvaluationRule(ruleID int) bool {
	return (ruleID >= 900100 && ruleID < 901000) ||
		(ruleID >= 949000 && ruleID < 950000) ||
		(ruleID >= 959000 && ruleID < 960000) ||
		(ruleID >= 980000 && ruleID < 990000)
}

// resolveAttackRuleID examines the transaction's matched rules to find the
// real attack-category rule when the interrupting rule is a CRS evaluation
// rule like 949110 (Anomaly Score Exceeded). This ensures the analytics
// category chart shows the actual attack type (XSS, SQLi, etc.) instead of
// "Other".
func resolveAttackRuleID(interruptRuleID int, tx types.Transaction) int {
	if !isCRSEvaluationRule(interruptRuleID) {
		return interruptRuleID
	}
	// Scan matched rules for the first one that is an actual attack rule
	for _, mr := range tx.MatchedRules() {
		id := mr.Rule().ID()
		if id != interruptRuleID && !isCRSEvaluationRule(id) && id >= 900000 {
			return id
		}
	}
	return interruptRuleID
}

// hibpCheckerAdapter wraps hibp.Checker to implement api.HIBPPasswordChecker interface
type hibpCheckerAdapter struct {
	checker *hibp.Checker
}

// CheckPassword checks if a password has been breached
func (a *hibpCheckerAdapter) CheckPassword(ctx context.Context, password string) (breached bool, count int, err error) {
	result, err := a.checker.CheckPassword(ctx, password)
	if err != nil {
		return false, 0, err
	}
	return result.IsBreached, result.BreachCount, nil
}

// IsEnabled returns whether breach checking is enabled
func (a *hibpCheckerAdapter) IsEnabled() bool {
	return a.checker.IsEnabled()
}

// statusRecorder wraps http.ResponseWriter to capture the status code
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.written {
		r.status = http.StatusOK
		r.written = true
	}
	return r.ResponseWriter.Write(b)
}

// Hijack implements http.Hijacker interface for WebSocket support
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support Hijack")
}

// Flush implements http.Flusher interface
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Skip wrapping for WebSocket connections to avoid hijack issues
		if r.URL.Path == "/api/ws" {
			next.ServeHTTP(w, r)
			return
		}

		// Wrap response writer to capture status code
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		elapsed := time.Since(start)
		requestID := requestid.FromContext(r.Context())
		logger.LogRequest(requestID, r.Method, r.URL.Path, extractClientIP(r), r.UserAgent(), rec.status, elapsed)

		// Record response time in the store for avg calculation
		if appStore != nil {
			appStore.RecordResponseTime(elapsed)
		}
	})
}

// apiKeyMiddleware validates API keys from X-API-Key header
// This middleware can be used as an alternative or in addition to JWT auth
func apiKeyMiddleware(requiredScope apikeys.Scope, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			// No API key provided, return 401
			http.Error(w, `{"error": "API key required", "hint": "Set X-API-Key header"}`, http.StatusUnauthorized)
			return
		}

		// Validate the API key
		clientIP := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			clientIP = strings.TrimSpace(strings.Split(forwarded, ",")[0])
		} else {
			if host, _, err := net.SplitHostPort(clientIP); err == nil {
				clientIP = host
			}
		}

		keyInfo, err := apiKeyMgr.ValidateKey(r.Context(), apiKey, requiredScope, clientIP)
		if err != nil {
			metricsInst.RecordAuthAttempt(false, "invalid_api_key")
			switch err {
			case apikeys.ErrKeyNotFound:
				http.Error(w, `{"error": "Invalid API key"}`, http.StatusUnauthorized)
			case apikeys.ErrKeyExpired:
				http.Error(w, `{"error": "API key has expired"}`, http.StatusUnauthorized)
			case apikeys.ErrKeyDisabled:
				http.Error(w, `{"error": "API key is disabled"}`, http.StatusForbidden)
			case apikeys.ErrInsufficientScope:
				http.Error(w, fmt.Sprintf(`{"error": "API key lacks required scope: %s"}`, requiredScope), http.StatusForbidden)
			case apikeys.ErrRateLimitExceeded:
				http.Error(w, `{"error": "API key rate limit exceeded"}`, http.StatusTooManyRequests)
			default:
				http.Error(w, `{"error": "API key validation failed"}`, http.StatusUnauthorized)
			}
			return
		}

		metricsInst.RecordAuthAttempt(true, "api_key")
		// Log the successful API key auth (using Info since Debug expects zap.Field)
		logger.Info(fmt.Sprintf("API key auth: %s (scopes: %v)", keyInfo.Name, keyInfo.Scopes))

		// Store key info in context for downstream use
		ctx := context.WithValue(r.Context(), apiKeyContextKey, keyInfo)
		r.Header.Set("X-Actor", "api:"+keyInfo.Name)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// authOrAPIKeyMiddleware allows either JWT token or API key authentication
func authOrAPIKeyMiddleware(apiScope apikeys.Scope, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check for API key first (X-API-Key header)
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "" {
			// Validate API key
			clientIP := r.RemoteAddr
			if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
				clientIP = strings.Split(forwarded, ",")[0]
			}

			keyInfo, err := apiKeyMgr.ValidateKey(r.Context(), apiKey, apiScope, clientIP)
			if err == nil {
				metricsInst.RecordAuthAttempt(true, "api_key")
				ctx := context.WithValue(r.Context(), apiKeyContextKey, keyInfo)
				// Create a pseudo user context for compatibility
				userClaims := &UserClaims{
					Username: "api:" + keyInfo.Name,
					Role:     scopeToRole(keyInfo.Scopes),
				}
				ctx = context.WithValue(ctx, userContextKey, userClaims)
				r.Header.Set("X-Actor", userClaims.Username)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// If API key validation fails, try JWT
		}

		// Fall back to JWT authentication
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			authHeader = "Bearer " + r.URL.Query().Get("token")
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, `{"error": "Unauthorized", "hint": "Provide Authorization Bearer token or X-API-Key header"}`, http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := auth.VerifyJWT(tokenString)
		if err != nil {
			metricsInst.RecordAuthAttempt(false, "invalid_token")
			http.Error(w, `{"error": "Invalid token"}`, http.StatusUnauthorized)
			return
		}

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
		r.Header.Set("X-Actor", userClaims.Username)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// scopeToRole converts API key scopes to user role for RBAC compatibility
func scopeToRole(scopes []apikeys.Scope) string {
	for _, s := range scopes {
		if s == apikeys.ScopeAdmin {
			return "Admin"
		}
	}
	for _, s := range scopes {
		if s == apikeys.ScopeWrite {
			return "Analyst"
		}
	}
	return "Viewer"
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
		r.Header.Set("X-Actor", userClaims.Username)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body: " + err.Error(),
		})
		return
	}

	if req.IP == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "IP address is required",
		})
		return
	}

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
		// Trim and validate IP address
		ip := strings.TrimSpace(req.IP)
		if net.ParseIP(ip) == nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Invalid IP address format",
			})
			return
		}
		rateLimiter.Blacklist(ip)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "IP blacklisted",
			"ip":      ip,
		})

	case http.MethodDelete:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
			http.Error(w, `{"error": "Invalid IP"}`, http.StatusBadRequest)
			return
		}
		// Trim and validate IP address
		ip := strings.TrimSpace(req.IP)
		if net.ParseIP(ip) == nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Invalid IP address format",
			})
			return
		}
		rateLimiter.RemoveFromBlacklist(ip)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "IP removed from blacklist",
			"ip":      ip,
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

	ruleHits := map[string]int{}
	blockedCount := 0
	alertCount := 0
	topOffenders := []map[string]interface{}{}

	if appStore != nil {
		ipCounts := map[string]int{}
		for _, entry := range appStore.GetLogs() {
			if entry.RuleID != 0 {
				ruleHits[strconv.Itoa(entry.RuleID)]++
			}
			switch entry.Status {
			case "Blocked", "ThreatBlocked":
				blockedCount++
			case "Flagged":
				alertCount++
			}
			if entry.ClientIP != "" {
				ipCounts[entry.ClientIP]++
			}
		}

		type kv struct {
			Key   string
			Count int
		}
		top := make([]kv, 0, len(ipCounts))
		for k, v := range ipCounts {
			top = append(top, kv{Key: k, Count: v})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].Count > top[j].Count })
		for i := 0; i < len(top) && i < 10; i++ {
			topOffenders = append(topOffenders, map[string]interface{}{
				"ip":    top[i].Key,
				"count": top[i].Count,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	storeStats := appStore.GetStats()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_requests":   storeStats.TotalRequests,
		"blocked_requests": storeStats.BlockedRequests,
		"uptime_seconds":   int64(time.Since(startTime).Seconds()),
		"memory_alloc_mb":  m.Alloc / 1024 / 1024,
		"memory_sys_mb":    m.Sys / 1024 / 1024,
		"goroutines":       runtime.NumGoroutine(),
		"rate_limiter":     rateLimiter.GetStats(),
		"threat_intel":     threatIntel.GetStats(),
		"rule_hits":        ruleHits,
		"block_vs_alert": map[string]interface{}{
			"blocked": blockedCount,
			"alerted": alertCount,
		},
		"top_offending_ips": topOffenders,
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
				logger.Error("PDF generation failed: " + err.Error())
				http.Error(w, "Failed to generate PDF: "+err.Error(), http.StatusInternalServerError)
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
		return strings.TrimSpace(xri)
	}
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
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

// Hijack implements http.Hijacker interface for WebSocket support
func (rec *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support Hijack")
}

// Flush implements http.Flusher interface
func (rec *responseRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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

		// Get real Redis statistics from INFO command
		info, err := redisCache.Info(ctx)
		if err == nil {
			// Parse Redis INFO output
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

			// Extract key statistics
			cacheStats := map[string]interface{}{}

			// Hit rate calculation: keyspace_hits / (keyspace_hits + keyspace_misses)
			if hits := infoMap["keyspace_hits"]; hits != "" {
				if misses := infoMap["keyspace_misses"]; misses != "" {
					hitsNum, _ := strconv.ParseFloat(hits, 64)
					missesNum, _ := strconv.ParseFloat(misses, 64)
					total := hitsNum + missesNum
					if total > 0 {
						hitRate := (hitsNum / total) * 100
						cacheStats["hit_rate"] = fmt.Sprintf("%.2f%%", hitRate)
					} else {
						cacheStats["hit_rate"] = "0.00%"
					}
				}
			}

			// Total keys across all databases
			totalKeys := int64(0)
			for key, value := range infoMap {
				if strings.HasPrefix(key, "db") && strings.Contains(value, "keys=") {
					// Parse "keys=123,expires=45,avg_ttl=67890"
					parts := strings.Split(value, ",")
					for _, part := range parts {
						if strings.HasPrefix(part, "keys=") {
							if keyCount, err := strconv.ParseInt(strings.TrimPrefix(part, "keys="), 10, 64); err == nil {
								totalKeys += keyCount
							}
						}
					}
				}
			}
			cacheStats["total_keys"] = totalKeys

			// Memory usage
			if mem := infoMap["used_memory_human"]; mem != "" {
				cacheStats["memory_used"] = mem
			} else if memBytes := infoMap["used_memory"]; memBytes != "" {
				if memNum, err := strconv.ParseInt(memBytes, 10, 64); err == nil {
					// Convert bytes to human readable
					const unit = 1024
					if memNum < unit {
						cacheStats["memory_used"] = fmt.Sprintf("%d B", memNum)
					} else if memNum < unit*unit {
						cacheStats["memory_used"] = fmt.Sprintf("%.2f KB", float64(memNum)/unit)
					} else if memNum < unit*unit*unit {
						cacheStats["memory_used"] = fmt.Sprintf("%.2f MB", float64(memNum)/(unit*unit))
					} else {
						cacheStats["memory_used"] = fmt.Sprintf("%.2f GB", float64(memNum)/(unit*unit*unit))
					}
				}
			}

			// Additional useful stats
			if uptime := infoMap["uptime_in_seconds"]; uptime != "" {
				cacheStats["uptime_seconds"] = uptime
			}
			if connectedClients := infoMap["connected_clients"]; connectedClients != "" {
				cacheStats["connected_clients"] = connectedClients
			}
			if totalCommands := infoMap["total_commands_processed"]; totalCommands != "" {
				cacheStats["total_commands"] = totalCommands
			}

			response["cache_stats"] = cacheStats
		}

		// Get custom stats from Redis (if any)
		stats, err := redisCache.GetAllStats(ctx)
		if err == nil && len(stats) > 0 {
			response["custom_stats"] = stats
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

// ============================================
// Security Settings Handlers (Enterprise)
// ============================================

// initSecurityServices initializes all enterprise security services
func initSecurityServices() {
	// Initialize API Key Manager (nil store = in-memory)
	apiKeyMgr = apikeys.NewManager(nil)
	logger.Info("API Key Manager initialized (in-memory storage)")

	// Initialize IP Allowlist Manager
	ipAllowConfig := ipallow.DefaultConfig()
	ipAllowConfig.AllowLocalhost = true // Allow localhost by default
	ipAllowMgr = ipallow.NewManager(ipAllowConfig, nil)
	logger.Info("IP Allowlist Manager initialized")

	// Initialize HIBP Password Checker
	hibpConfig := hibp.DefaultConfig()
	hibpConfig.Enabled = true
	hibpConfig.BreachThreshold = 1 // Reject passwords seen even once
	hibpChecker = hibp.NewChecker(hibpConfig)
	logger.Info("HIBP Password Breach Checker initialized")

	// Initialize Secrets Manager
	secretsManager = secrets.NewManager()
	// Load secrets from environment (using correct env var name)
	if err := secretsManager.LoadFromEnvWithDefault(secrets.SecretJWT, "OBSIDIAN_JWT_SECRET", ""); err != nil {
		logger.Warn("JWT secret not loaded from environment")
	}
	logger.Info("Secrets Manager initialized")

	// Initialize GraphQL Security Analyzer
	gqlConfig := graphql.DefaultConfig()
	gqlConfig.Enabled = true
	gqlConfig.MaxDepth = 10
	gqlConfig.MaxComplexity = 1000
	gqlConfig.BlockIntrospection = true // Block introspection in production
	graphqlAnalyzer = graphql.NewAnalyzer(gqlConfig)
	logger.Info("GraphQL Security Analyzer initialized")

	// Initialize Response Body Inspector (DLP)
	respConfig := respbody.DefaultConfig()
	respConfig.Enabled = true
	respConfig.DetectSSN = true
	respConfig.DetectCreditCard = true
	respConfig.DetectAPIKeys = true
	respConfig.DetectAWSKeys = true
	respConfig.DetectPrivateKeys = true
	respConfig.DetectJWT = true
	respConfig.BlockOnDetection = false // Log only by default, don't block
	respBodyInspector = respbody.NewInspector(respConfig)
	logger.Info("Response Body DLP Inspector initialized")

	logger.Info("All Enterprise Security Services initialized successfully")
}

// handleSecurityOverview returns comprehensive security configuration overview
func handleSecurityOverview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get HIBP cache stats
	hibpEntries, hibpHitRate := hibpChecker.GetCacheStats()

	// Get secrets stats
	secretStats := secretsManager.GetStats()

	// Get API key count
	apiKeyCount := len(apiKeyMgr.ListKeys())

	// Get IP allowlist count
	ipAllowEntries := ipAllowMgr.ListEntries()

	overview := map[string]interface{}{
		"oauth": map[string]interface{}{
			"google_enabled": os.Getenv("SUPABASE_URL") != "",
			"github_enabled": os.Getenv("SUPABASE_URL") != "",
			"provider":       "supabase",
		},
		"database": map[string]interface{}{
			"postgres_connected": dbManager != nil && dbManager.HasPostgres(),
			"redis_connected":    dbManager != nil && dbManager.HasRedis(),
		},
		"security": map[string]interface{}{
			"jwt_configured":    securityMgr != nil,
			"rate_limit_active": rateLimiter != nil,
			"threat_intel":      threatIntel != nil,
		},
		"api_keys": map[string]interface{}{
			"enabled": apiKeyMgr != nil,
			"count":   apiKeyCount,
		},
		"ip_allowlist": map[string]interface{}{
			"enabled": ipAllowMgr != nil && ipAllowMgr.IsEnabled(),
			"count":   len(ipAllowEntries),
		},
		"hibp": map[string]interface{}{
			"enabled":        hibpChecker != nil && hibpChecker.IsEnabled(),
			"cache_entries":  hibpEntries,
			"cache_hit_rate": hibpHitRate,
		},
		"secrets": map[string]interface{}{
			"reload_count": secretStats.ReloadCount,
			"last_reload":  secretStats.LastReload,
		},
		"graphql": map[string]interface{}{
			"enabled":             graphqlAnalyzer != nil,
			"max_depth":           graphqlAnalyzer.GetConfig().MaxDepth,
			"block_introspection": graphqlAnalyzer.GetConfig().BlockIntrospection,
		},
		"response_inspection": map[string]interface{}{
			"enabled":            respBodyInspector != nil,
			"block_on_detection": respBodyInspector.GetConfig().BlockOnDetection,
		},
		"version": AppVersion,
	}

	json.NewEncoder(w).Encode(overview)
}

// handlePasswordBreachCheck checks if a password has been compromised using HIBP k-anonymity
func handlePasswordBreachCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Password is required",
		})
		return
	}

	// Use the rich HIBP package for breach checking
	result, err := hibpChecker.CheckPassword(r.Context(), req.Password)
	if err != nil {
		logger.Error(fmt.Sprintf("HIBP check failed: %v", err))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Failed to check password against breach database",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"compromised": result.IsBreached,
		"count":       result.BreachCount,
		"checked_at":  result.CheckedAt,
		"message": func() string {
			if result.IsBreached {
				return fmt.Sprintf("Password found %d times in data breaches. Do not use!", result.BreachCount)
			}
			return "Password not found in known data breaches."
		}(),
	})
}

// handleSecretsReload reloads secrets from environment
func handleSecretsReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Use the secrets manager for hot-reload
	if err := secretsManager.ReloadAll(r.Context()); err != nil {
		logger.Error(fmt.Sprintf("Secrets reload failed: %v", err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Secrets reload failed",
		})
		return
	}

	stats := secretsManager.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"message":      "Secrets reloaded successfully",
		"reload_count": stats.ReloadCount,
	})
}

// handleSecretsRotateJWT rotates the JWT secret
func handleSecretsRotateJWT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Use the secrets manager for JWT rotation
	newSecret, version, err := secretsManager.RotateJWTSecret()
	if err != nil {
		logger.Error(fmt.Sprintf("JWT rotation failed: %v", err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "JWT secret rotation failed",
		})
		return
	}

	// Don't expose the actual secret - just confirm rotation
	_ = newSecret

	stats := secretsManager.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"message":         "JWT secret rotated successfully",
		"version":         version,
		"reload_count":    stats.ReloadCount,
		"note":            "All existing tokens are now invalid",
		"action_required": "Users must re-authenticate",
	})
}

// ============================================
// API Key Management Handlers (Enterprise)
// ============================================

func handleAPIKeysList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	keys := apiKeyMgr.ListKeys()

	// Sanitize - remove sensitive data from response
	safeKeys := make([]map[string]interface{}, len(keys))
	for i, k := range keys {
		safeKeys[i] = map[string]interface{}{
			"id":           k.ID,
			"name":         k.Name,
			"key_prefix":   k.KeyPrefix,
			"scopes":       k.Scopes,
			"created_by":   k.CreatedBy,
			"created_at":   k.CreatedAt,
			"expires_at":   k.ExpiresAt,
			"last_used":    k.LastUsedAt,
			"last_used_ip": k.LastUsedIP,
			"enabled":      k.Enabled,
			"rate_limit":   k.RateLimit,
			"description":  k.Description,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"keys":  safeKeys,
		"count": len(safeKeys),
	})
}

func handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name        string   `json:"name"`
		Scopes      []string `json:"scopes"`
		ExpiresIn   string   `json:"expires_in"` // e.g., "720h" for 30 days
		RateLimit   int      `json:"rate_limit"` // Requests per minute
		Description string   `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "API key name is required",
		})
		return
	}

	// Parse expiration duration
	expireIn := 30 * 24 * time.Hour // Default 30 days
	if req.ExpiresIn != "" {
		if d, err := time.ParseDuration(req.ExpiresIn); err == nil {
			expireIn = d
		}
	}

	// Convert string scopes to apikeys.Scope
	scopes := make([]apikeys.Scope, len(req.Scopes))
	for i, s := range req.Scopes {
		scopes[i] = apikeys.Scope(s)
	}

	// Get username from JWT claims
	createdBy := "admin"
	if claims, ok := r.Context().Value(userContextKey).(*UserClaims); ok && claims.Username != "" {
		createdBy = claims.Username
	}

	// Generate the key using the rich apikeys package
	plainKey, key, err := apiKeyMgr.GenerateKey(r.Context(), req.Name, scopes, &expireIn, req.RateLimit, req.Description, createdBy)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to generate API key: %v", err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to generate API key",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"key":     plainKey, // Only returned on creation - NEVER shown again!
		"id":      key.ID,
		"name":    key.Name,
		"prefix":  key.KeyPrefix,
		"scopes":  key.Scopes,
		"created": key.CreatedAt,
		"expires": key.ExpiresAt,
		"warning": "Save this key NOW. It will never be shown again!",
	})
}

func handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		KeyID string `json:"key_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.KeyID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "key_id is required",
		})
		return
	}

	if err := apiKeyMgr.RevokeKey(r.Context(), req.KeyID); err != nil {
		logger.Error(fmt.Sprintf("Failed to revoke API key: %v", err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to revoke API key",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "API key revoked successfully",
	})
}

// ============================================
// IP Allowlist Management Handlers (Enterprise)
// ============================================

func handleIPAllowlist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries := ipAllowMgr.ListEntries()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled": ipAllowMgr.IsEnabled(),
		"entries": entries,
		"count":   len(entries),
	})
}

func handleIPAllowlistAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IP          string `json:"ip"`
		CIDR        string `json:"cidr"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Get username from JWT claims
	addedBy := "admin"
	if claims, ok := r.Context().Value(userContextKey).(*UserClaims); ok && claims.Username != "" {
		addedBy = claims.Username
	}

	var err error
	var entry *ipallow.AllowlistEntry

	if req.CIDR != "" {
		entry, err = ipAllowMgr.AddCIDR(r.Context(), req.CIDR, req.Description, addedBy)
	} else if req.IP != "" {
		entry, err = ipAllowMgr.AddIP(r.Context(), req.IP, req.Description, addedBy)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Must provide 'ip' or 'cidr'",
		})
		return
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"entry":   entry,
	})
}

func handleIPAllowlistRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
		IP string `json:"ip"` // Also support IP for backward compatibility
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Support both ID and IP for removal
	removeID := req.ID
	if removeID == "" {
		removeID = req.IP
	}

	if removeID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "id or ip is required",
		})
		return
	}

	if err := ipAllowMgr.RemoveEntry(r.Context(), removeID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "IP removed from allowlist",
	})
}

func handleIPAllowlistCheck(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		http.Error(w, "Missing 'ip' parameter", http.StatusBadRequest)
		return
	}

	allowed := ipAllowMgr.IsAllowed(ip)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ip":      ip,
		"allowed": allowed,
	})
}

// ============================================
// GraphQL Security Handlers (Enterprise)
// ============================================

func handleGraphQLAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query string `json:"query"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "GraphQL query is required",
		})
		return
	}

	// Build payload safely using json.Marshal to prevent JSON injection
	payload := map[string]string{"query": req.Query}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to marshal GraphQL payload: %v", err))
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	result, err := graphqlAnalyzer.AnalyzeBody(payloadBytes)
	if err != nil {
		logger.Error(fmt.Sprintf("GraphQL analysis failed: %v", err))
		http.Error(w, "Analysis failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleGraphQLConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graphqlAnalyzer.GetConfig())
		return
	}

	if r.Method == http.MethodPost {
		var config graphql.Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, "Invalid config", http.StatusBadRequest)
			return
		}
		graphqlAnalyzer.SetConfig(&config)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "GraphQL config updated",
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// ============================================
// Response Body DLP Handlers (Enterprise)
// ============================================

func handleRespBodyConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(respBodyInspector.GetConfig())
		return
	}

	if r.Method == http.MethodPost {
		var config respbody.Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, "Invalid config", http.StatusBadRequest)
			return
		}
		respBodyInspector.SetConfig(&config)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Response body inspection config updated",
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleRespBodyTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Body        string `json:"body"`
		ContentType string `json:"content_type"`
		Path        string `json:"path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Body == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Body content is required for testing",
		})
		return
	}

	result := respBodyInspector.Inspect([]byte(req.Body), req.ContentType, req.Path)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ============================================
// OAuth Handlers (Supabase Integration)
// ============================================

// handleOAuthGoogle initiates Google OAuth flow via Supabase
func handleOAuthGoogle(w http.ResponseWriter, r *http.Request) {
	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		// Return a proper HTML page explaining OAuth setup
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OAuth Not Configured - Obsidian WAF</title>
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
<style>
body { background: #0d1117; color: #e6edf3; min-height: 100vh; display: flex; align-items: center; justify-content: center; font-family: system-ui; }
.card { background: #161b22; border: 1px solid #30363d; max-width: 500px; }
code { background: #21262d; padding: 2px 6px; border-radius: 4px; color: #f0883e; }
.btn-primary { background: #f43f5e; border-color: #f43f5e; }
.btn-primary:hover { background: #e11d48; border-color: #e11d48; }
</style>
</head>
<body>
<div class="card p-4">
<h4 class="text-warning mb-3"><i class="fas fa-exclamation-triangle me-2"></i>OAuth Not Configured</h4>
<p>Google OAuth requires Supabase configuration. To enable OAuth login:</p>
<ol class="small">
<li class="mb-2">Create a project at <a href="https://supabase.com" target="_blank" class="text-info">supabase.com</a></li>
<li class="mb-2">Enable Google Auth in Authentication → Providers</li>
<li class="mb-2">Set environment variables:
<pre class="mt-1 p-2 rounded" style="background:#21262d">SUPABASE_URL=https://your-project.supabase.co
SUPABASE_KEY=your-anon-public-key</pre>
</li>
<li>Restart Obsidian WAF</li>
</ol>
<hr class="border-secondary">
<p class="small text-muted mb-3">For now, please use username/password login with the default credentials.</p>
<a href="/login.html" class="btn btn-primary w-100"><i class="fas fa-arrow-left me-2"></i>Back to Login</a>
</div>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css">
</body>
</html>`))
		return
	}

	// Build redirect URL
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Validate Host header to prevent open redirect attacks
	host := r.Host
	if allowedHost := os.Getenv("OBSIDIAN_ALLOWED_HOST"); allowedHost != "" {
		host = allowedHost
	}
	redirectTo := fmt.Sprintf("%s://%s/api/auth/callback", scheme, host)

	authURL := fmt.Sprintf("%s/auth/v1/authorize?provider=google&redirect_to=%s",
		supabaseURL, url.QueryEscape(redirectTo))

	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// handleOAuthGitHub initiates GitHub OAuth flow via Supabase
func handleOAuthGitHub(w http.ResponseWriter, r *http.Request) {
	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		// Return a proper HTML page explaining OAuth setup
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OAuth Not Configured - Obsidian WAF</title>
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet">
<style>
body { background: #0d1117; color: #e6edf3; min-height: 100vh; display: flex; align-items: center; justify-content: center; font-family: system-ui; }
.card { background: #161b22; border: 1px solid #30363d; max-width: 500px; }
code { background: #21262d; padding: 2px 6px; border-radius: 4px; color: #f0883e; }
.btn-primary { background: #f43f5e; border-color: #f43f5e; }
.btn-primary:hover { background: #e11d48; border-color: #e11d48; }
</style>
</head>
<body>
<div class="card p-4">
<h4 class="text-warning mb-3"><i class="fas fa-exclamation-triangle me-2"></i>OAuth Not Configured</h4>
<p>GitHub OAuth requires Supabase configuration. To enable OAuth login:</p>
<ol class="small">
<li class="mb-2">Create a project at <a href="https://supabase.com" target="_blank" class="text-info">supabase.com</a></li>
<li class="mb-2">Enable GitHub Auth in Authentication → Providers</li>
<li class="mb-2">Set environment variables:
<pre class="mt-1 p-2 rounded" style="background:#21262d">SUPABASE_URL=https://your-project.supabase.co
SUPABASE_KEY=your-anon-public-key</pre>
</li>
<li>Restart Obsidian WAF</li>
</ol>
<hr class="border-secondary">
<p class="small text-muted mb-3">For now, please use username/password login with the default credentials.</p>
<a href="/login.html" class="btn btn-primary w-100"><i class="fas fa-arrow-left me-2"></i>Back to Login</a>
</div>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css">
</body>
</html>`))
		return
	}

	// Build redirect URL
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Validate Host header to prevent open redirect attacks
	host := r.Host
	if allowedHost := os.Getenv("OBSIDIAN_ALLOWED_HOST"); allowedHost != "" {
		host = allowedHost
	}
	redirectTo := fmt.Sprintf("%s://%s/api/auth/callback", scheme, host)

	authURL := fmt.Sprintf("%s/auth/v1/authorize?provider=github&redirect_to=%s",
		supabaseURL, url.QueryEscape(redirectTo))

	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// handleOAuthCallback handles the OAuth callback from Supabase
func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	// Supabase returns tokens in the URL fragment for implicit flow
	// or as query parameters for code flow
	accessToken := r.URL.Query().Get("access_token")

	if accessToken == "" {
		// Check for error - sanitize before using in redirect
		if errMsg := r.URL.Query().Get("error_description"); errMsg != "" {
			// URL-encode and sanitize the error message to prevent header injection
			// Strip CRLF characters and limit length
			sanitized := strings.Map(func(r rune) rune {
				if r == '\r' || r == '\n' || r < 32 {
					return -1 // Remove control characters
				}
				return r
			}, errMsg)
			if len(sanitized) > 200 {
				sanitized = sanitized[:200]
			}
			http.Redirect(w, r, "/login.html?error="+url.QueryEscape(sanitized), http.StatusTemporaryRedirect)
			return
		}
		// For implicit flow, the token is in the hash fragment
		// We need to render a page that extracts it and redirects
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Processing...</title></head>
<body>
<script>
// Extract token from hash fragment
const hash = window.location.hash.substring(1);
const params = new URLSearchParams(hash);
const token = params.get('access_token');
if (token) {
    window.location.href = '/login.html?token=' + encodeURIComponent(token);
} else {
    window.location.href = '/login.html?error=OAuth+failed';
}
</script>
<p>Processing authentication...</p>
</body>
</html>`))
		return
	}

	// Verify token with Supabase and get user info
	supabaseURL := os.Getenv("SUPABASE_URL")
	supabaseKey := os.Getenv("SUPABASE_KEY")

	if supabaseURL == "" || supabaseKey == "" {
		http.Redirect(w, r, "/login.html?error=OAuth+not+configured", http.StatusTemporaryRedirect)
		return
	}

	// Get user from Supabase
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", supabaseURL+"/auth/v1/user", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("apikey", supabaseKey)

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		http.Redirect(w, r, "/login.html?error=OAuth+verification+failed", http.StatusTemporaryRedirect)
		return
	}
	defer resp.Body.Close()

	var supabaseUser struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		AppMetadata struct {
			Provider string `json:"provider"`
		} `json:"app_metadata"`
	}
	json.NewDecoder(resp.Body).Decode(&supabaseUser)

	// Determine role - default to Viewer for OAuth users
	role := "Viewer"

	// Generate our JWT token
	token, err := auth.GenerateJWT(0, supabaseUser.Email, role, 24*time.Hour)
	if err != nil {
		http.Redirect(w, r, "/login.html?error=Token+generation+failed", http.StatusTemporaryRedirect)
		return
	}

	// Redirect to frontend with token
	http.Redirect(w, r, "/login.html?token="+token, http.StatusTemporaryRedirect)
}
