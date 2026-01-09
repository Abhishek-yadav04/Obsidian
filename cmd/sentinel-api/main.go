package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	"gopkg.in/yaml.v3"

	coraza "github.com/corazawaf/coraza/v3"
	txhttp "github.com/corazawaf/coraza/v3/http"
	"github.com/corazawaf/coraza/v3/types"

	h "github.com/Abhishek-yadav04/Obsidian/internal/handler"
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
	attackLogs    []AttackLog
	clients       = make(map[*websocket.Conn]bool)
	upgrader      = websocket.Upgrader{}
	logger        *zap.Logger
	db            *pgxpool.Pool
	config        Config
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
	http.HandleFunc("/api/login", loginHandler)

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

	// Simple authentication (replace with proper user store)
	if creds.Username == "admin" && creds.Password == "password" {
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

		json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
		return
	}

	http.Error(w, "Invalid credentials", http.StatusUnauthorized)
}

// exportHandler generates PDF reports
func exportHandler(w http.ResponseWriter, r *http.Request) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
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
