package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jung-kurt/gofpdf"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	coraza "github.com/corazawaf/coraza/v3"
	txhttp "github.com/corazawaf/coraza/v3/http"
	"github.com/corazawaf/coraza/v3/types"

	h "github.com/corazawaf/coraza/v3/internal/handler"

	// Security feature imports
	"github.com/corazawaf/coraza/v3/internal/app/apikeys"
	"github.com/corazawaf/coraza/v3/internal/app/graphql"
	"github.com/corazawaf/coraza/v3/internal/app/hibp"
	"github.com/corazawaf/coraza/v3/internal/app/ipallow"
	"github.com/corazawaf/coraza/v3/internal/app/respbody"
	"github.com/corazawaf/coraza/v3/internal/app/secrets"
)

type Config struct {
	Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DBName   string `yaml:"dbname"`
	} `yaml:"database"`
	Logging struct {
		Level string `yaml:"level"`
	} `yaml:"logging"`
	Security struct {
		JWTSecret string `yaml:"jwt_secret"`
	} `yaml:"security"`
	Observability struct {
		PrometheusPort int    `yaml:"prometheus_port"`
		OtelEndpoint   string `yaml:"otel_endpoint"`
	} `yaml:"observability"`
}

// AttackLog represents a security event
type AttackLog = h.AttackLog

// Global variables
var (
	attackLogs []AttackLog
	clients    = make(map[*websocket.Conn]bool)
	upgrader   = websocket.Upgrader{}
	logger     *zap.Logger
	db         *pgxpool.Pool
	config     Config

	// Security services
	apiKeyManager     *apikeys.Manager
	ipAllowlist       *ipallow.Manager
	hibpChecker       *hibp.Checker
	secretsManager    *secrets.Manager
	graphqlAnalyzer   *graphql.Analyzer
	respBodyInspector *respbody.Inspector

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "obsidian_requests_total",
			Help: "Total number of requests",
		},
		[]string{"method", "status"},
	)
	blockedRequests = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "obsidian_blocked_requests_total",
			Help: "Total number of blocked requests",
		},
	)
)

func init() {
	prometheus.MustRegister(requestsTotal)
	prometheus.MustRegister(blockedRequests)
}

func main() {
	// Load configuration
	loadConfig()

	// Initialize logger
	initLogger()

	// Initialize database
	initDatabase()

	// Initialize observability
	initTracing()

	// Initialize security services
	initSecurityServices()

	// Create WAF
	waf := createWAF()

	// Serve dashboard at root (public files, data protected by API auth)
	http.Handle("/", http.FileServer(http.Dir("./ui")))

	/*
		// Old auth wrapper for UI - removed to allow loading the dashboard shell
		// Client-side JS will handle redirection if no token is present,
		// and API calls will fail without valid token.
	*/

	// Protected example app at /api/hello
	http.Handle("/api/hello", txhttp.WrapHandler(waf, http.HandlerFunc(exampleHandler)))

	// API endpoints (Protected)
	http.HandleFunc("/api/logs", authMiddleware(h.GetLogsHandler(logger, db, &attackLogs)))
	http.HandleFunc("/api/stats", authMiddleware(h.GetStatsHandler(logger, db, &attackLogs)))
	http.HandleFunc("/api/export", authMiddleware(exportHandler))

	// Authentication endpoints
	http.HandleFunc("/api/login", loginHandler)
	http.HandleFunc("/api/login/mfa", mfaVerifyHandler)

	// OAuth endpoints (Supabase integration)
	http.HandleFunc("/api/auth/google", oauthGoogleHandler)
	http.HandleFunc("/api/auth/github", oauthGitHubHandler)
	http.HandleFunc("/api/auth/callback", oauthCallbackHandler)

	// ==================== NEW SECURITY API ENDPOINTS ====================

	// API Key Management
	http.HandleFunc("/api/security/apikeys", authMiddleware(apiKeyHandler))
	http.HandleFunc("/api/security/apikeys/create", authMiddleware(apiKeyCreateHandler))
	http.HandleFunc("/api/security/apikeys/revoke", authMiddleware(apiKeyRevokeHandler))

	// IP Allowlist Management
	http.HandleFunc("/api/security/ipallowlist", authMiddleware(ipAllowlistHandler))
	http.HandleFunc("/api/security/ipallowlist/add", authMiddleware(ipAllowlistAddHandler))
	http.HandleFunc("/api/security/ipallowlist/remove", authMiddleware(ipAllowlistRemoveHandler))
	http.HandleFunc("/api/security/ipallowlist/check", authMiddleware(ipAllowlistCheckHandler))

	// Password Breach Check
	http.HandleFunc("/api/security/password/check", authMiddleware(passwordCheckHandler))

	// Secrets Management
	http.HandleFunc("/api/security/secrets/reload", authMiddleware(secretsReloadHandler))
	http.HandleFunc("/api/security/secrets/rotate-jwt", authMiddleware(secretsRotateJWTHandler))

	// GraphQL Security
	http.HandleFunc("/api/security/graphql/analyze", authMiddleware(graphqlAnalyzeHandler))
	http.HandleFunc("/api/security/graphql/config", authMiddleware(graphqlConfigHandler))

	// Response Body Inspection
	http.HandleFunc("/api/security/respbody/config", authMiddleware(respBodyConfigHandler))
	http.HandleFunc("/api/security/respbody/test", authMiddleware(respBodyTestHandler))

	// Security Overview
	http.HandleFunc("/api/security/overview", authMiddleware(securityOverviewHandler))

	// WebSocket for live updates
	http.HandleFunc("/ws", authMiddleware(wsHandler))

	// Metrics endpoint
	http.Handle("/metrics", promhttp.Handler())

	logger.Info("Obsidian Sentinel API starting", zap.String("port", "8090"))
	log.Fatal(http.ListenAndServe(":8090", nil))
}

// loadConfig loads configuration from YAML file
func loadConfig() {
	file, err := os.Open("configs/config.yaml")
	if err != nil {
		log.Fatal("Failed to open config file:", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		log.Fatal("Failed to decode config:", err)
	}
}

// initLogger initializes structured logging
func initLogger() {
	var zapConfig zap.Config
	switch config.Logging.Level {
	case "debug":
		zapConfig = zap.NewDevelopmentConfig()
	default:
		zapConfig = zap.NewProductionConfig()
	}
	var err error
	logger, err = zapConfig.Build()
	if err != nil {
		log.Fatal("Failed to initialize logger:", err)
	}
}

// initDatabase initializes PostgreSQL connection (optional)
func initDatabase() {
	if config.Database.Host == "" {
		logger.Info("Database not configured, skipping")
		return
	}
	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		config.Database.User, config.Database.Password, config.Database.Host, config.Database.Port, config.Database.DBName)

	var err error
	db, err = pgxpool.New(context.Background(), connString)
	if err != nil {
		logger.Warn("Failed to connect to database, continuing without persistence", zap.Error(err))
		return
	}

	// Run migrations
	runMigrations()
}

// runMigrations runs database migrations
func runMigrations() {
	// Placeholder for migration logic using golang-migrate
	logger.Info("Running database migrations")
}

// initTracing initializes OpenTelemetry tracing (optional)
func initTracing() {
	if config.Observability.OtelEndpoint == "" {
		logger.Info("OpenTelemetry endpoint not configured, skipping tracing")
		return
	}
	exporter, err := otlptracegrpc.New(context.Background(), otlptracegrpc.WithEndpoint(config.Observability.OtelEndpoint))
	if err != nil {
		logger.Warn("Failed to create OTLP exporter, continuing without tracing", zap.Error(err))
		return
	}

	tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
	otel.SetTracerProvider(tp)
	logger.Info("OpenTelemetry tracing initialized")
}

// createWAF creates and configures the WAF instance
func createWAF() coraza.WAF {
	directivesFile := "./default.conf"
	if s := os.Getenv("DIRECTIVES_FILE"); s != "" {
		directivesFile = s
	}

	waf, err := coraza.NewWAF(
		coraza.NewWAFConfig().
			WithErrorCallback(logError).
			WithDirectivesFromFile(directivesFile),
	)
	if err != nil {
		logger.Fatal("Failed to create WAF", zap.Error(err))
	}
	return waf
}

// logError handles WAF error logging with database persistence
func logError(err types.MatchedRule) {
	msg := err.ErrorLog()

	ruleID := err.Rule().ID()
	logEntry := AttackLog{
		Time:     time.Now().Format(time.RFC3339),
		RuleID:   fmt.Sprintf("%d", ruleID),
		Severity: err.Rule().Severity().String(),
		Message:  msg,
		Type: func() string {
			// Prefer explicit mapping by rule ID, fall back to heuristic checks
			switch ruleID {
			case 1000:
				return "SQLI"
			case 1001:
				return "XSS"
			default:
				lower := strings.ToLower(msg)
				if strings.Contains(lower, "<script") {
					return "XSS"
				}
				if strings.Contains(lower, "select") || strings.Contains(lower, "union") || strings.Contains(lower, "' or '") {
					return "SQLI"
				}
				if strings.Contains(lower, "password") {
					return "BODY"
				}
				return "OTHER"
			}
		}(),
	}

	// Store in memory for backward compatibility
	attackLogs = append(attackLogs, logEntry)

	// Store in database
	storeLogInDB(logEntry)

	// Update metrics
	blockedRequests.Inc()

	// Broadcast to WebSocket clients
	broadcastWS(logEntry)

	logger.Info("WAF blocked request",
		zap.String("rule_id", logEntry.RuleID),
		zap.String("severity", logEntry.Severity),
		zap.String("type", logEntry.Type))
}

// storeLogInDB persists log entry to PostgreSQL (if available)
func storeLogInDB(logEntry AttackLog) {
	if db == nil {
		return
	}
	_, err := db.Exec(context.Background(),
		"INSERT INTO audit_logs (time, rule_id, severity, message, type) VALUES ($1, $2, $3, $4, $5)",
		logEntry.Time, logEntry.RuleID, logEntry.Severity, logEntry.Message, logEntry.Type)
	if err != nil {
		logger.Error("Failed to store log in database", zap.Error(err))
	}
}

// authMiddleware provides JWT-based authentication
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow login page without auth
		if strings.HasSuffix(r.URL.Path, "login.html") || r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}

		// Check header
		tokenString := r.Header.Get("Authorization")

		// If header is missing, check query param (useful for WebSockets)
		if tokenString == "" {
			tokenString = r.URL.Query().Get("token")
		}

		if tokenString == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Remove "Bearer " prefix
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return []byte(config.Security.JWTSecret), nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// loginHandler handles user authentication
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Try database authentication first
	if db != nil {
		var (
			storedHash string
			role       string
			mfaEnabled bool
			mfaSecret  string
		)

		err := db.QueryRow(context.Background(),
			"SELECT password_hash, role, COALESCE(mfa_enabled, false), COALESCE(mfa_secret, '') FROM users WHERE username = $1",
			creds.Username).Scan(&storedHash, &role, &mfaEnabled, &mfaSecret)

		if err == nil {
			// User found in database - verify password using bcrypt
			if bcryptErr := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(creds.Password)); bcryptErr == nil {
				// Password correct
				if mfaEnabled && mfaSecret != "" {
					// MFA required - return temporary token
					tempToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
						"username": creds.Username,
						"role":     role,
						"mfa":      "pending",
						"exp":      time.Now().Add(time.Minute * 5).Unix(),
					})
					tempTokenString, _ := tempToken.SignedString([]byte(config.Security.JWTSecret))

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]interface{}{
						"requires_mfa": true,
						"temp_token":   tempTokenString,
					})
					return
				}

				// No MFA - issue full token
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"username": creds.Username,
					"role":     role,
					"exp":      time.Now().Add(time.Hour * 24).Unix(),
				})
				tokenString, err := token.SignedString([]byte(config.Security.JWTSecret))
				if err != nil {
					http.Error(w, "Internal server error", http.StatusInternalServerError)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"token":    tokenString,
					"username": creds.Username,
					"role":     role,
				})
				return
			}
		}
	}

	// Fallback to default admin authentication (for initial setup)
	if creds.Username == "admin" && creds.Password == "admin123" {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"username": creds.Username,
			"role":     "admin",
			"exp":      time.Now().Add(time.Hour * 24).Unix(),
		})

		tokenString, err := token.SignedString([]byte(config.Security.JWTSecret))
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token":    tokenString,
			"username": creds.Username,
			"role":     "admin",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
}

// mfaVerifyHandler verifies MFA TOTP code
func mfaVerifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TempToken string `json:"temp_token"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Parse temp token
	token, err := jwt.Parse(req.TempToken, func(token *jwt.Token) (interface{}, error) {
		return []byte(config.Security.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid or expired token"})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["mfa"] != "pending" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid token type"})
		return
	}

	username := claims["username"].(string)
	role := claims["role"].(string)

	// Get MFA secret from database
	var mfaSecret string
	if db != nil {
		err := db.QueryRow(context.Background(),
			"SELECT mfa_secret FROM users WHERE username = $1 AND mfa_enabled = true",
			username).Scan(&mfaSecret)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "MFA not configured"})
			return
		}
	}

	// Verify TOTP code
	if !verifyTOTP(mfaSecret, req.Code) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid MFA code"})
		return
	}

	// Issue full token
	fullToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": username,
		"role":     role,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, _ := fullToken.SignedString([]byte(config.Security.JWTSecret))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token":    tokenString,
		"username": username,
		"role":     role,
	})
}

// verifyTOTP verifies a TOTP code against a secret
func verifyTOTP(secret, code string) bool {
	// TOTP verification using time-based algorithm
	// For production, use a proper TOTP library like pquerna/otp
	// This is a simplified implementation for demonstration
	if len(code) != 6 {
		return false
	}

	// In production, implement proper TOTP verification
	// For now, allow any 6-digit code if MFA is enabled (demo mode)
	// TODO: Integrate with github.com/pquerna/otp/totp
	return true
}

// oauthGoogleHandler initiates Google OAuth flow
func oauthGoogleHandler(w http.ResponseWriter, r *http.Request) {
	// Redirect to Supabase Google OAuth
	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		http.Redirect(w, r, "/ui/login.html?error=OAuth+not+configured", http.StatusTemporaryRedirect)
		return
	}

	redirectURL := fmt.Sprintf("%s/auth/v1/authorize?provider=google&redirect_to=%s",
		supabaseURL, url.QueryEscape("http://"+r.Host+"/api/auth/callback"))
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// oauthGitHubHandler initiates GitHub OAuth flow
func oauthGitHubHandler(w http.ResponseWriter, r *http.Request) {
	// Redirect to Supabase GitHub OAuth
	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		http.Redirect(w, r, "/ui/login.html?error=OAuth+not+configured", http.StatusTemporaryRedirect)
		return
	}

	redirectURL := fmt.Sprintf("%s/auth/v1/authorize?provider=github&redirect_to=%s",
		supabaseURL, url.QueryEscape("http://"+r.Host+"/api/auth/callback"))
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// oauthCallbackHandler handles OAuth callback from Supabase
func oauthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	// Get access token from Supabase callback
	accessToken := r.URL.Query().Get("access_token")
	if accessToken == "" {
		// Try to get from hash fragment (client-side redirect)
		http.Redirect(w, r, "/ui/login.html?error=OAuth+failed", http.StatusTemporaryRedirect)
		return
	}

	// Verify token with Supabase and get user info
	supabaseURL := os.Getenv("SUPABASE_URL")
	supabaseKey := os.Getenv("SUPABASE_KEY")

	if supabaseURL == "" || supabaseKey == "" {
		http.Redirect(w, r, "/ui/login.html?error=OAuth+not+configured", http.StatusTemporaryRedirect)
		return
	}

	// Get user from Supabase
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", supabaseURL+"/auth/v1/user", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("apikey", supabaseKey)

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		http.Redirect(w, r, "/ui/login.html?error=OAuth+verification+failed", http.StatusTemporaryRedirect)
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

	// Determine role (check if email matches any admin patterns)
	role := "viewer"
	if db != nil {
		var dbRole string
		err := db.QueryRow(context.Background(),
			"SELECT role FROM users WHERE email = $1", supabaseUser.Email).Scan(&dbRole)
		if err == nil {
			role = dbRole
		} else {
			// Insert new OAuth user with default role
			_, _ = db.Exec(context.Background(),
				"INSERT INTO users (username, email, role, password_hash, created_at) VALUES ($1, $2, $3, '', NOW()) ON CONFLICT (email) DO NOTHING",
				supabaseUser.Email, supabaseUser.Email, "viewer")
		}
	}

	// Issue our JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": supabaseUser.Email,
		"email":    supabaseUser.Email,
		"role":     role,
		"provider": supabaseUser.AppMetadata.Provider,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, _ := token.SignedString([]byte(config.Security.JWTSecret))

	// Redirect to frontend with token
	http.Redirect(w, r, "/ui/login.html?token="+tokenString, http.StatusTemporaryRedirect)
}

// exportHandler generates PDF reports
func exportHandler(w http.ResponseWriter, r *http.Request) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	// If logo exists, place it in the header
	if _, err := os.Stat("ui/assets/logo.png"); err == nil {
		// x=10,y=8 width=40
		pdf.ImageOptions("ui/assets/logo.png", 10, 8, 40, 0, false, gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, 0, "")
		pdf.Ln(18)
	}
	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(40, 10, "Obsidian WAF Attack Report")
	pdf.Ln(12)
	pdf.SetFont("Arial", "", 12)

	for _, l := range attackLogs {
		pdf.Cell(0, 8, fmt.Sprintf("[%s] %s | %s | %s", l.Time, l.RuleID, l.Severity, l.Message))
		pdf.Ln(8)
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=obsidian-report.pdf")
	err := pdf.Output(w)
	if err != nil {
		logger.Error("PDF generation error", zap.Error(err))
	}
}

// wsHandler manages WebSocket connections
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("WebSocket upgrade error", zap.Error(err))
		return
	}
	defer conn.Close()
	clients[conn] = true
	for {
		if _, _, err := conn.NextReader(); err != nil {
			delete(clients, conn)
			break
		}
	}
}

// broadcastWS sends log updates to WebSocket clients
func broadcastWS(logEntry AttackLog) {
	for conn := range clients {
		if err := conn.WriteJSON(logEntry); err != nil {
			conn.Close()
			delete(clients, conn)
		}
	}
}

// exampleHandler is a sample protected endpoint
func exampleHandler(w http.ResponseWriter, r *http.Request) {
	requestsTotal.WithLabelValues(r.Method, "200").Inc()
	w.Write([]byte("Hello from Obsidian-protected application!"))
}

// ==================== SECURITY SERVICES INITIALIZATION ====================

// initSecurityServices initializes all security feature services
func initSecurityServices() {
	logger.Info("Initializing security services")

	// Initialize API Key Manager
	apiKeyManager = apikeys.NewManager(apikeys.DefaultConfig(), nil)
	logger.Info("API Key Manager initialized")

	// Initialize IP Allowlist Manager
	ipAllowlist = ipallow.NewManager(ipallow.DefaultConfig(), nil)
	logger.Info("IP Allowlist Manager initialized")

	// Initialize HIBP Checker
	hibpChecker = hibp.NewChecker(hibp.DefaultConfig())
	logger.Info("HIBP Password Checker initialized")

	// Initialize Secrets Manager (load from env)
	secretsManager = secrets.NewManager(secrets.DefaultConfig())
	if err := secretsManager.LoadFromEnv(); err != nil {
		logger.Warn("Failed to load secrets from environment", zap.Error(err))
	}
	logger.Info("Secrets Manager initialized")

	// Initialize GraphQL Analyzer
	graphqlAnalyzer = graphql.NewAnalyzer(graphql.DefaultConfig())
	logger.Info("GraphQL Analyzer initialized")

	// Initialize Response Body Inspector
	respBodyInspector = respbody.NewInspector(respbody.DefaultConfig())
	logger.Info("Response Body Inspector initialized")

	logger.Info("All security services initialized successfully")
}

// ==================== API KEY HANDLERS ====================

func apiKeyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	keys, err := apiKeyManager.ListKeys()
	if err != nil {
		http.Error(w, "Failed to list keys", http.StatusInternalServerError)
		return
	}

	// Sanitize - remove hash from response
	safeKeys := make([]map[string]interface{}, len(keys))
	for i, k := range keys {
		safeKeys[i] = map[string]interface{}{
			"id":         k.ID,
			"name":       k.Name,
			"prefix":     k.Prefix,
			"scopes":     k.Scopes,
			"owner_id":   k.OwnerID,
			"created_at": k.CreatedAt,
			"expires_at": k.ExpiresAt,
			"last_used":  k.LastUsedAt,
			"revoked":    k.Revoked,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(safeKeys)
}

func apiKeyCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name     string   `json:"name"`
		Scopes   []string `json:"scopes"`
		OwnerID  string   `json:"owner_id"`
		ExpireIn string   `json:"expire_in"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	expireIn := 30 * 24 * time.Hour
	if req.ExpireIn != "" {
		if d, err := time.ParseDuration(req.ExpireIn); err == nil {
			expireIn = d
		}
	}

	key, err := apiKeyManager.GenerateKey(req.Name, req.Scopes, req.OwnerID, expireIn)
	if err != nil {
		logger.Error("Failed to generate API key", zap.Error(err))
		http.Error(w, "Failed to generate API key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":     key.RawKey,
		"id":      key.ID,
		"name":    key.Name,
		"scopes":  key.Scopes,
		"created": key.CreatedAt,
		"expires": key.ExpiresAt,
	})
}

func apiKeyRevokeHandler(w http.ResponseWriter, r *http.Request) {
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

	if err := apiKeyManager.RevokeKey(req.KeyID); err != nil {
		http.Error(w, "Failed to revoke key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked"})
}

// ==================== IP ALLOWLIST HANDLERS ====================

func ipAllowlistHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries := ipAllowlist.ListEntries()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func ipAllowlistAddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IP          string `json:"ip"`
		CIDR        string `json:"cidr"`
		Description string `json:"description"`
		AddedBy     string `json:"added_by"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var err error
	if req.CIDR != "" {
		err = ipAllowlist.AddCIDR(req.CIDR, req.Description, req.AddedBy)
	} else if req.IP != "" {
		err = ipAllowlist.AddIP(req.IP, req.Description, req.AddedBy)
	} else {
		http.Error(w, "Must provide 'ip' or 'cidr'", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}

func ipAllowlistRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := ipAllowlist.RemoveEntry(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}

func ipAllowlistCheckHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		http.Error(w, "Missing 'ip' parameter", http.StatusBadRequest)
		return
	}

	allowed := ipAllowlist.IsAllowed(ip)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed})
}

// ==================== PASSWORD CHECK HANDLER ====================

func passwordCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Password == "" {
		http.Error(w, "Password required", http.StatusBadRequest)
		return
	}

	result, err := hibpChecker.CheckPassword(r.Context(), req.Password)
	if err != nil {
		logger.Error("HIBP check failed", zap.Error(err))
		http.Error(w, "Password check failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"breached": result.IsBreached,
		"count":    result.BreachCount,
		"safe":     !result.IsBreached,
	})
}

// ==================== SECRETS HANDLERS ====================

func secretsReloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := secretsManager.ReloadAll(); err != nil {
		logger.Error("Secrets reload failed", zap.Error(err))
		http.Error(w, "Reload failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "reloaded",
		"version": secretsManager.Version(),
	})
}

func secretsRotateJWTHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	newSecret, err := secretsManager.RotateJWTSecret()
	if err != nil {
		logger.Error("JWT rotation failed", zap.Error(err))
		http.Error(w, "Rotation failed", http.StatusInternalServerError)
		return
	}

	// Update the config's JWT secret
	config.Security.JWTSecret = newSecret

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "rotated",
		"version": secretsManager.Version(),
		"note":    "All existing tokens are now invalid",
	})
}

// ==================== GRAPHQL HANDLERS ====================

func graphqlAnalyzeHandler(w http.ResponseWriter, r *http.Request) {
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

	// Create a proper GraphQL request body
	body := fmt.Sprintf(`{"query":%q}`, req.Query)
	result := graphqlAnalyzer.AnalyzeBody([]byte(body))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func graphqlConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graphqlAnalyzer.GetConfig())
		return
	}

	if r.Method == http.MethodPost {
		var cfg graphql.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "Invalid config", http.StatusBadRequest)
			return
		}
		graphqlAnalyzer.SetConfig(&cfg)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// ==================== RESPONSE BODY HANDLERS ====================

func respBodyConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(respBodyInspector.GetConfig())
		return
	}

	if r.Method == http.MethodPost {
		var cfg respbody.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "Invalid config", http.StatusBadRequest)
			return
		}
		respBodyInspector.SetConfig(&cfg)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func respBodyTestHandler(w http.ResponseWriter, r *http.Request) {
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

	result := respBodyInspector.Inspect([]byte(req.Body), req.ContentType, req.Path)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ==================== SECURITY OVERVIEW HANDLER ====================

func securityOverviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiKeyCount := 0
	if keys, err := apiKeyManager.ListKeys(); err == nil {
		apiKeyCount = len(keys)
	}

	cacheEntries, cacheHitRate := hibpChecker.GetCacheStats()

	overview := map[string]interface{}{
		"api_keys": map[string]interface{}{
			"enabled": true,
			"count":   apiKeyCount,
		},
		"ip_allowlist": map[string]interface{}{
			"enabled": ipAllowlist.IsEnabled(),
			"count":   len(ipAllowlist.ListEntries()),
		},
		"hibp": map[string]interface{}{
			"enabled":        hibpChecker.IsEnabled(),
			"cache_entries":  cacheEntries,
			"cache_hit_rate": cacheHitRate,
		},
		"secrets": map[string]interface{}{
			"version": secretsManager.Version(),
		},
		"graphql": map[string]interface{}{
			"enabled":   graphqlAnalyzer.GetConfig().Enabled,
			"max_depth": graphqlAnalyzer.GetConfig().MaxDepth,
		},
		"response_inspection": map[string]interface{}{
			"enabled":       respBodyInspector.GetConfig().Enabled,
			"max_body_size": respBodyInspector.GetConfig().MaxBodySize,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(overview)
}
