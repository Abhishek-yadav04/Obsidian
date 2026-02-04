// Package database provides unified database connection management for Obsidian WAF.
// Supports PostgreSQL (Supabase) and Redis (Redis Cloud) with automatic connection
// validation and health monitoring.
package database

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// Errors for database operations
var (
	ErrNoPostgresURL     = errors.New("DATABASE_URL environment variable not set")
	ErrNoRedisURL        = errors.New("REDIS_URL environment variable not set")
	ErrPostgresConnect   = errors.New("failed to connect to PostgreSQL")
	ErrRedisConnect      = errors.New("failed to connect to Redis")
	ErrHealthCheckFailed = errors.New("database health check failed")
)

// Manager handles connections to PostgreSQL and Redis
type Manager struct {
	pgPool          *pgxpool.Pool
	redisClient     *redis.Client
	config          *Config
	isLocalPostgres bool // true if using local PostgreSQL (fallback)
}

// Config holds database configuration
type Config struct {
	// PostgreSQL settings
	PostgresURL      string
	PostgresURLLocal string // Fallback local PostgreSQL
	PostgresMaxConns int32
	PostgresMinConns int32
	PostgresTimeout  time.Duration

	// Redis settings
	RedisURL      string
	RedisPoolSize int
	RedisTimeout  time.Duration

	// General
	HealthCheckInterval time.Duration
	AutoMigrate         bool
}

// DefaultConfig returns sensible defaults for database connections
func DefaultConfig() *Config {
	return &Config{
		PostgresMaxConns:    25,
		PostgresMinConns:    5,
		PostgresTimeout:     10 * time.Second,
		RedisPoolSize:       10,
		RedisTimeout:        5 * time.Second,
		HealthCheckInterval: 30 * time.Second,
		AutoMigrate:         true,
	}
}

// LoadEnv loads environment variables from .env file
func LoadEnv() error {
	// Try to load .env from current directory
	if err := godotenv.Load(); err != nil {
		// Try parent directories
		for _, path := range []string{".env", "../.env", "../../.env"} {
			if err := godotenv.Load(path); err == nil {
				return nil
			}
		}
		// Not finding .env is OK - might be using system env vars
		return nil
	}
	return nil
}

// New creates a new database manager with connections to PostgreSQL and Redis
// It tries to connect to each database independently - one failing won't prevent the other from connecting
func New(ctx context.Context, cfg *Config) (*Manager, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Load environment variables
	if err := LoadEnv(); err != nil {
		return nil, fmt.Errorf("failed to load .env: %w", err)
	}

	// Get URLs from environment if not provided in config
	if cfg.PostgresURL == "" {
		cfg.PostgresURL = os.Getenv("DATABASE_URL")
	}
	if cfg.PostgresURLLocal == "" {
		cfg.PostgresURLLocal = os.Getenv("DATABASE_URL_LOCAL")
	}
	if cfg.RedisURL == "" {
		cfg.RedisURL = os.Getenv("REDIS_URL")
	}

	m := &Manager{config: cfg}
	var connectionErrors []string

	// Connect to PostgreSQL - try primary (Supabase) first, then fallback to local
	if cfg.PostgresURL != "" {
		fmt.Printf("[Database] Attempting PostgreSQL connection (Supabase)...\n")
		pool, err := connectPostgres(ctx, cfg.PostgresURL, cfg)
		if err != nil {
			fmt.Printf("[Database] Supabase PostgreSQL FAILED: %v\n", err)
			// Try local fallback
			if cfg.PostgresURLLocal != "" {
				fmt.Printf("[Database] Attempting PostgreSQL connection (Local fallback)...\n")
				pool, err = connectPostgres(ctx, cfg.PostgresURLLocal, cfg)
				if err != nil {
					fmt.Printf("[Database] Local PostgreSQL FAILED: %v\n", err)
					connectionErrors = append(connectionErrors, fmt.Sprintf("PostgreSQL (both Supabase and Local failed): %v", err))
				} else {
					fmt.Printf("[Database] Local PostgreSQL connection SUCCESS\n")
					m.pgPool = pool
					m.isLocalPostgres = true
				}
			} else {
				connectionErrors = append(connectionErrors, fmt.Sprintf("PostgreSQL: %v", err))
			}
		} else {
			fmt.Printf("[Database] Supabase PostgreSQL connection SUCCESS\n")
			m.pgPool = pool
		}
	} else if cfg.PostgresURLLocal != "" {
		// No Supabase URL, try local directly
		fmt.Printf("[Database] Attempting PostgreSQL connection (Local)...\n")
		pool, err := connectPostgres(ctx, cfg.PostgresURLLocal, cfg)
		if err != nil {
			fmt.Printf("[Database] Local PostgreSQL FAILED: %v\n", err)
			connectionErrors = append(connectionErrors, fmt.Sprintf("PostgreSQL: %v", err))
		} else {
			fmt.Printf("[Database] Local PostgreSQL connection SUCCESS\n")
			m.pgPool = pool
			m.isLocalPostgres = true
		}
	} else {
		fmt.Printf("[Database] DATABASE_URL not set, skipping PostgreSQL\n")
	}

	// Connect to Redis (Redis Cloud) - non-blocking
	if cfg.RedisURL != "" {
		fmt.Printf("[Database] Attempting Redis connection...\n")
		client, err := connectRedis(ctx, cfg)
		if err != nil {
			fmt.Printf("[Database] Redis connection FAILED: %v\n", err)
			connectionErrors = append(connectionErrors, fmt.Sprintf("Redis: %v", err))
		} else {
			fmt.Printf("[Database] Redis connection SUCCESS\n")
			m.redisClient = client
		}
	} else {
		fmt.Printf("[Database] REDIS_URL not set, skipping Redis\n")
	}

	// If neither database connected, return error
	if m.pgPool == nil && m.redisClient == nil && len(connectionErrors) > 0 {
		return nil, fmt.Errorf("all database connections failed: %s", strings.Join(connectionErrors, "; "))
	}

	return m, nil
}

// connectPostgres establishes a PostgreSQL connection pool
func connectPostgres(ctx context.Context, connURL string, cfg *Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(connURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DATABASE_URL: %w", err)
	}

	// Apply pool settings
	poolConfig.MaxConns = cfg.PostgresMaxConns
	poolConfig.MinConns = cfg.PostgresMinConns
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute
	poolConfig.HealthCheckPeriod = cfg.HealthCheckInterval
	poolConfig.ConnConfig.ConnectTimeout = cfg.PostgresTimeout

	// Create connection pool
	ctxTimeout, cancel := context.WithTimeout(ctx, cfg.PostgresTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctxTimeout, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctxTimeout); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	return pool, nil
}

// connectRedis establishes a Redis connection
func connectRedis(ctx context.Context, cfg *Config) (*redis.Client, error) {
	// Use the built-in ParseURL which handles rediss:// TLS automatically
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse REDIS_URL: %w", err)
	}

	opts.PoolSize = cfg.RedisPoolSize
	opts.DialTimeout = cfg.RedisTimeout
	opts.ReadTimeout = cfg.RedisTimeout
	opts.WriteTimeout = cfg.RedisTimeout

	client := redis.NewClient(opts)

	// Verify connection
	ctxTimeout, cancel := context.WithTimeout(ctx, cfg.RedisTimeout)
	defer cancel()

	if err := client.Ping(ctxTimeout).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	return client, nil
}

// parseRedisURL parses a Redis URL into options
// Supports: redis://, rediss:// (TLS)
func parseRedisURL(redisURL string) (*redis.Options, error) {
	u, err := url.Parse(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URL: %w", err)
	}

	opts := &redis.Options{}

	// Set address
	opts.Addr = u.Host

	// Set password
	if u.User != nil {
		if password, ok := u.User.Password(); ok {
			opts.Password = password
		}
	}

	// Set database number from path
	if u.Path != "" {
		path := strings.TrimPrefix(u.Path, "/")
		if path != "" {
			db, err := strconv.Atoi(path)
			if err == nil {
				opts.DB = db
			}
		}
	}

	// Enable TLS for rediss:// scheme
	if u.Scheme == "rediss" {
		opts.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	return opts, nil
}

// PostgresPool returns the PostgreSQL connection pool
func (m *Manager) PostgresPool() *pgxpool.Pool {
	return m.pgPool
}

// RedisClient returns the Redis client
func (m *Manager) RedisClient() *redis.Client {
	return m.redisClient
}

// HasPostgres returns true if PostgreSQL is connected
func (m *Manager) HasPostgres() bool {
	return m.pgPool != nil
}

// IsLocalPostgres returns true if using local PostgreSQL (fallback)
func (m *Manager) IsLocalPostgres() bool {
	return m.isLocalPostgres
}

// HasRedis returns true if Redis is connected
func (m *Manager) HasRedis() bool {
	return m.redisClient != nil
}

// HealthCheck performs health checks on all connected databases
func (m *Manager) HealthCheck(ctx context.Context) error {
	var errs []string

	if m.pgPool != nil {
		if err := m.pgPool.Ping(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("PostgreSQL: %v", err))
		}
	}

	if m.redisClient != nil {
		if err := m.redisClient.Ping(ctx).Err(); err != nil {
			errs = append(errs, fmt.Sprintf("Redis: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrHealthCheckFailed, strings.Join(errs, "; "))
	}

	return nil
}

// Close closes all database connections gracefully
func (m *Manager) Close() error {
	var errs []string

	if m.pgPool != nil {
		m.pgPool.Close()
	}

	if m.redisClient != nil {
		if err := m.redisClient.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("Redis: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing connections: %s", strings.Join(errs, "; "))
	}

	return nil
}

// Stats returns connection statistics for monitoring
type Stats struct {
	Postgres *PostgresStats `json:"postgres,omitempty"`
	Redis    *RedisStats    `json:"redis,omitempty"`
}

// PostgresStats holds PostgreSQL pool statistics
type PostgresStats struct {
	TotalConns    int32 `json:"total_conns"`
	IdleConns     int32 `json:"idle_conns"`
	AcquiredConns int32 `json:"acquired_conns"`
	MaxConns      int32 `json:"max_conns"`
}

// RedisStats holds Redis connection statistics
type RedisStats struct {
	Hits       uint32 `json:"hits"`
	Misses     uint32 `json:"misses"`
	Timeouts   uint32 `json:"timeouts"`
	TotalConns uint32 `json:"total_conns"`
	IdleConns  uint32 `json:"idle_conns"`
}

// GetStats returns current connection statistics
func (m *Manager) GetStats() *Stats {
	stats := &Stats{}

	if m.pgPool != nil {
		pgStats := m.pgPool.Stat()
		stats.Postgres = &PostgresStats{
			TotalConns:    pgStats.TotalConns(),
			IdleConns:     pgStats.IdleConns(),
			AcquiredConns: pgStats.AcquiredConns(),
			MaxConns:      pgStats.MaxConns(),
		}
	}

	if m.redisClient != nil {
		redisStats := m.redisClient.PoolStats()
		stats.Redis = &RedisStats{
			Hits:       redisStats.Hits,
			Misses:     redisStats.Misses,
			Timeouts:   redisStats.Timeouts,
			TotalConns: redisStats.TotalConns,
			IdleConns:  redisStats.IdleConns,
		}
	}

	return stats
}

// RunMigrations executes database migrations
func (m *Manager) RunMigrations(ctx context.Context) error {
	if m.pgPool == nil {
		return nil // No PostgreSQL connection, skip migrations
	}

	// Create tables if they don't exist
	migrations := []string{
		// Users table
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(50) UNIQUE NOT NULL,
			email VARCHAR(255),
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'Viewer',
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Audit logs table
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id SERIAL PRIMARY KEY,
			time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			rule_id VARCHAR(50),
			severity VARCHAR(20),
			message TEXT,
			type VARCHAR(20),
			client_ip VARCHAR(45),
			request_uri TEXT,
			user_agent TEXT
		)`,

		// Attack logs table
		`CREATE TABLE IF NOT EXISTS attack_logs (
			id SERIAL PRIMARY KEY,
			time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			client_ip VARCHAR(45) NOT NULL,
			method VARCHAR(10),
			uri TEXT,
			rule_id INTEGER,
			rule_msg TEXT,
			severity VARCHAR(20),
			action VARCHAR(20),
			request_headers JSONB,
			response_status INTEGER
		)`,

		// WAF rules table
		`CREATE TABLE IF NOT EXISTS waf_rules (
			id SERIAL PRIMARY KEY,
			rule_id INTEGER UNIQUE NOT NULL,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			pattern TEXT NOT NULL,
			phase INTEGER DEFAULT 1,
			severity VARCHAR(20) DEFAULT 'medium',
			enabled BOOLEAN DEFAULT true,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Sessions table
		`CREATE TABLE IF NOT EXISTS sessions (
			id VARCHAR(255) PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			token_hash VARCHAR(255) NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			ip_address VARCHAR(45),
			user_agent TEXT
		)`,

		// Stats table
		`CREATE TABLE IF NOT EXISTS stats (
			id SERIAL PRIMARY KEY,
			stat_name VARCHAR(100) UNIQUE NOT NULL,
			stat_value BIGINT DEFAULT 0,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Threat intelligence table
		`CREATE TABLE IF NOT EXISTS threat_intel (
			id SERIAL PRIMARY KEY,
			ip_address VARCHAR(45) UNIQUE NOT NULL,
			threat_level VARCHAR(20) NOT NULL,
			source VARCHAR(100),
			reason TEXT,
			blocked BOOLEAN DEFAULT false,
			first_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			hit_count INTEGER DEFAULT 1
		)`,

		// Create indexes
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_time ON audit_logs(time)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_severity ON audit_logs(severity)`,
		`CREATE INDEX IF NOT EXISTS idx_attack_logs_time ON attack_logs(time)`,
		`CREATE INDEX IF NOT EXISTS idx_attack_logs_client_ip ON attack_logs(client_ip)`,
		`CREATE INDEX IF NOT EXISTS idx_threat_intel_ip ON threat_intel(ip_address)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
	}

	for _, migration := range migrations {
		if _, err := m.pgPool.Exec(ctx, migration); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

// InsertDefaultUsers inserts default users if they don't exist
// SECURITY: Each user has a UNIQUE password hash. Change passwords immediately in production!
// Default passwords: admin="ObsidianAdmin#2024", analyst="ObsidianAnalyst#2024", viewer="ObsidianViewer#2024"
func (m *Manager) InsertDefaultUsers(ctx context.Context) error {
	if m.pgPool == nil {
		return nil
	}

	// SECURITY: Each user has a unique bcrypt hash (cost=12)
	// These hashes are for INITIAL SETUP ONLY - users MUST change passwords after first login
	defaultUsers := []struct {
		username     string
		email        string
		passwordHash string // Unique hash per user - bcrypt cost 12
		role         string
	}{
		// Password: ObsidianAdmin#2024
		{"admin", "admin@obsidian.local", "$2a$12$2xXI1FJm/E7ShS.99UBp9OHSznLgbdzTPrjnh6dopp5YS.y7Fovt2", "Admin"},
		// Password: ObsidianAnalyst#2024
		{"analyst", "analyst@obsidian.local", "$2a$12$lV4oLgU..jCLVqFYFqwZ9um0cEyKqf5eUWlehYBLan5JVbEPLxKfu", "Analyst"},
		// Password: ObsidianViewer#2024
		{"viewer", "viewer@obsidian.local", "$2a$12$vWaIVdKEfvkodk9sxY2zne.mFqyjqtoNi914.K.lNk4uFiVR6vYZC", "Viewer"},
	}

	for _, u := range defaultUsers {
		_, err := m.pgPool.Exec(ctx, `
			INSERT INTO users (username, email, password_hash, role, enabled)
			VALUES ($1, $2, $3, $4, true)
			ON CONFLICT (username) DO UPDATE SET 
				password_hash = EXCLUDED.password_hash,
				updated_at = NOW()
			WHERE users.password_hash != EXCLUDED.password_hash
		`, u.username, u.email, u.passwordHash, u.role)
		if err != nil {
			return fmt.Errorf("failed to insert default user %s: %w", u.username, err)
		}
	}

	return nil
}

// User represents a user from the database
type User struct {
	ID           int
	Username     string
	Email        string
	PasswordHash string
	Role         string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// GetUserByUsername retrieves a user by username from PostgreSQL
func (m *Manager) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	var user User
	err := m.pgPool.QueryRow(ctx, `
		SELECT id, username, email, password_hash, role, enabled, created_at, updated_at
		FROM users
		WHERE username = $1
	`, username).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Role,
		&user.Enabled,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	return &user, nil
}

// GetAllUsers retrieves all users from PostgreSQL
func (m *Manager) GetAllUsers(ctx context.Context) ([]User, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	rows, err := m.pgPool.Query(ctx, `
		SELECT id, username, email, password_hash, role, enabled, created_at, updated_at
		FROM users
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Role,
			&user.Enabled,
			&user.CreatedAt,
			&user.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}

	return users, nil
}

// UpdateUserPassword updates a user's password hash
func (m *Manager) UpdateUserPassword(ctx context.Context, username, newPasswordHash string) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	result, err := m.pgPool.Exec(ctx, `
		UPDATE users
		SET password_hash = $1, updated_at = NOW()
		WHERE username = $2
	`, newPasswordHash, username)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	if result.RowsAffected() == 0 {
		return errors.New("user not found")
	}

	return nil
}

// UpdateUser updates a user's role and enabled status
func (m *Manager) UpdateUser(ctx context.Context, username, role string, enabled bool) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	result, err := m.pgPool.Exec(ctx, `
		UPDATE users
		SET role = $1, enabled = $2, updated_at = NOW()
		WHERE username = $3
	`, role, enabled, username)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return errors.New("user not found")
	}

	return nil
}

// InsertAuditLog records an audit trail entry in PostgreSQL
func (m *Manager) InsertAuditLog(ctx context.Context, ruleID, severity, message, logType, clientIP, requestURI, userAgent string) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	_, err := m.pgPool.Exec(ctx, `
		INSERT INTO audit_logs (rule_id, severity, message, type, client_ip, request_uri, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, ruleID, severity, message, logType, clientIP, requestURI, userAgent)
	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	return nil
}

// GetAuditLogs retrieves audit logs with pagination
func (m *Manager) GetAuditLogs(ctx context.Context, limit, offset int) ([]map[string]interface{}, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000 // Cap to prevent abuse
	}

	rows, err := m.pgPool.Query(ctx, `
		SELECT id, time, rule_id, severity, message, type, client_ip, request_uri, user_agent
		FROM audit_logs
		ORDER BY time DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}
	defer rows.Close()

	var logs []map[string]interface{}
	for rows.Next() {
		var id int
		var logTime time.Time
		var ruleID, severity, message, logType, clientIP, requestURI, userAgent *string

		if err := rows.Scan(&id, &logTime, &ruleID, &severity, &message, &logType, &clientIP, &requestURI, &userAgent); err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}

		log := map[string]interface{}{
			"id":   id,
			"time": logTime,
		}
		if ruleID != nil {
			log["rule_id"] = *ruleID
		}
		if severity != nil {
			log["severity"] = *severity
		}
		if message != nil {
			log["message"] = *message
		}
		if logType != nil {
			log["type"] = *logType
		}
		if clientIP != nil {
			log["client_ip"] = *clientIP
		}
		if requestURI != nil {
			log["request_uri"] = *requestURI
		}
		if userAgent != nil {
			log["user_agent"] = *userAgent
		}
		logs = append(logs, log)
	}

	return logs, nil
}

// InsertAttackLog records an attack event in PostgreSQL
func (m *Manager) InsertAttackLog(ctx context.Context, clientIP, method, uri string, ruleID int, ruleMsg, severity, action string, responseStatus int) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	_, err := m.pgPool.Exec(ctx, `
		INSERT INTO attack_logs (client_ip, method, uri, rule_id, rule_msg, severity, action, response_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, clientIP, method, uri, ruleID, ruleMsg, severity, action, responseStatus)
	if err != nil {
		return fmt.Errorf("failed to insert attack log: %w", err)
	}

	return nil
}

// GetAttackLogs retrieves attack logs with pagination
func (m *Manager) GetAttackLogs(ctx context.Context, limit, offset int) ([]map[string]interface{}, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := m.pgPool.Query(ctx, `
		SELECT id, time, client_ip, method, uri, rule_id, rule_msg, severity, action, response_status
		FROM attack_logs
		ORDER BY time DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query attack logs: %w", err)
	}
	defer rows.Close()

	var logs []map[string]interface{}
	for rows.Next() {
		var id, ruleID, responseStatus int
		var logTime time.Time
		var clientIP, method, uri, ruleMsg, severity, action string

		if err := rows.Scan(&id, &logTime, &clientIP, &method, &uri, &ruleID, &ruleMsg, &severity, &action, &responseStatus); err != nil {
			continue
		}

		logs = append(logs, map[string]interface{}{
			"id":              id,
			"time":            logTime,
			"client_ip":       clientIP,
			"method":          method,
			"uri":             uri,
			"rule_id":         ruleID,
			"rule_msg":        ruleMsg,
			"severity":        severity,
			"action":          action,
			"response_status": responseStatus,
		})
	}

	return logs, nil
}

// =============================================================================
// WAF Rules CRUD Operations
// =============================================================================

// SaveWAFRule inserts or updates a WAF rule in PostgreSQL
func (m *Manager) SaveWAFRule(ctx context.Context, ruleID int, name, description, pattern string, phase int, severity string, enabled bool) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	_, err := m.pgPool.Exec(ctx, `
		INSERT INTO waf_rules (rule_id, name, description, pattern, phase, severity, enabled, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (rule_id) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			pattern = EXCLUDED.pattern,
			phase = EXCLUDED.phase,
			severity = EXCLUDED.severity,
			enabled = EXCLUDED.enabled,
			updated_at = NOW()
	`, ruleID, name, description, pattern, phase, severity, enabled)
	if err != nil {
		return fmt.Errorf("failed to save WAF rule: %w", err)
	}

	return nil
}

// GetWAFRules retrieves all WAF rules from PostgreSQL
func (m *Manager) GetWAFRules(ctx context.Context) ([]map[string]interface{}, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	rows, err := m.pgPool.Query(ctx, `
		SELECT id, rule_id, name, description, pattern, phase, severity, enabled, created_at, updated_at
		FROM waf_rules
		ORDER BY rule_id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query WAF rules: %w", err)
	}
	defer rows.Close()

	var rules []map[string]interface{}
	for rows.Next() {
		var id, ruleID, phase int
		var name, description, pattern, severity string
		var enabled bool
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&id, &ruleID, &name, &description, &pattern, &phase, &severity, &enabled, &createdAt, &updatedAt); err != nil {
			continue
		}

		rules = append(rules, map[string]interface{}{
			"id":          id,
			"rule_id":     ruleID,
			"name":        name,
			"description": description,
			"pattern":     pattern,
			"phase":       phase,
			"severity":    severity,
			"enabled":     enabled,
			"created_at":  createdAt,
			"updated_at":  updatedAt,
		})
	}

	return rules, nil
}

// DeleteWAFRule removes a WAF rule from PostgreSQL
func (m *Manager) DeleteWAFRule(ctx context.Context, ruleID int) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	_, err := m.pgPool.Exec(ctx, `DELETE FROM waf_rules WHERE rule_id = $1`, ruleID)
	if err != nil {
		return fmt.Errorf("failed to delete WAF rule: %w", err)
	}

	return nil
}

// =============================================================================
// Threat Intelligence CRUD Operations
// =============================================================================

// SaveThreatIntel inserts or updates a threat intel entry
func (m *Manager) SaveThreatIntel(ctx context.Context, ipAddress, threatLevel, source, reason string, blocked bool) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	_, err := m.pgPool.Exec(ctx, `
		INSERT INTO threat_intel (ip_address, threat_level, source, reason, blocked, last_seen, hit_count)
		VALUES ($1, $2, $3, $4, $5, NOW(), 1)
		ON CONFLICT (ip_address) DO UPDATE SET
			threat_level = EXCLUDED.threat_level,
			source = EXCLUDED.source,
			reason = EXCLUDED.reason,
			blocked = EXCLUDED.blocked,
			last_seen = NOW(),
			hit_count = threat_intel.hit_count + 1
	`, ipAddress, threatLevel, source, reason, blocked)
	if err != nil {
		return fmt.Errorf("failed to save threat intel: %w", err)
	}

	return nil
}

// GetThreatIntel retrieves all threat intel entries
func (m *Manager) GetThreatIntel(ctx context.Context) ([]map[string]interface{}, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	rows, err := m.pgPool.Query(ctx, `
		SELECT id, ip_address, threat_level, source, reason, blocked, first_seen, last_seen, hit_count
		FROM threat_intel
		ORDER BY last_seen DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query threat intel: %w", err)
	}
	defer rows.Close()

	var entries []map[string]interface{}
	for rows.Next() {
		var id, hitCount int
		var ipAddress, threatLevel, source, reason string
		var blocked bool
		var firstSeen, lastSeen time.Time

		if err := rows.Scan(&id, &ipAddress, &threatLevel, &source, &reason, &blocked, &firstSeen, &lastSeen, &hitCount); err != nil {
			continue
		}

		entries = append(entries, map[string]interface{}{
			"id":           id,
			"ip_address":   ipAddress,
			"threat_level": threatLevel,
			"source":       source,
			"reason":       reason,
			"blocked":      blocked,
			"first_seen":   firstSeen,
			"last_seen":    lastSeen,
			"hit_count":    hitCount,
		})
	}

	return entries, nil
}

// GetBlockedIPs returns list of blocked IP addresses
func (m *Manager) GetBlockedIPs(ctx context.Context) ([]string, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	rows, err := m.pgPool.Query(ctx, `SELECT ip_address FROM threat_intel WHERE blocked = true`)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocked IPs: %w", err)
	}
	defer rows.Close()

	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			continue
		}
		ips = append(ips, ip)
	}

	return ips, nil
}

// =============================================================================
// Stats Operations
// =============================================================================

// IncrementStat increments a stat counter in PostgreSQL
func (m *Manager) IncrementStat(ctx context.Context, statName string, delta int64) error {
	if m.pgPool == nil {
		return errors.New("PostgreSQL not connected")
	}

	_, err := m.pgPool.Exec(ctx, `
		INSERT INTO stats (stat_name, stat_value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (stat_name) DO UPDATE SET
			stat_value = stats.stat_value + $2,
			updated_at = NOW()
	`, statName, delta)
	if err != nil {
		return fmt.Errorf("failed to increment stat: %w", err)
	}

	return nil
}

// GetAllStats retrieves all stats from PostgreSQL
func (m *Manager) GetAllStats(ctx context.Context) (map[string]int64, error) {
	if m.pgPool == nil {
		return nil, errors.New("PostgreSQL not connected")
	}

	rows, err := m.pgPool.Query(ctx, `SELECT stat_name, stat_value FROM stats`)
	if err != nil {
		return nil, fmt.Errorf("failed to query stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int64)
	for rows.Next() {
		var name string
		var value int64
		if err := rows.Scan(&name, &value); err != nil {
			continue
		}
		stats[name] = value
	}

	return stats, nil
}

// =============================================================================
// Redis Rate Limiter Adapter
// =============================================================================

// RedisRateLimitAdapter wraps the go-redis client to implement the ratelimit.RedisClient interface
type RedisRateLimitAdapter struct {
	client *redis.Client
}

// NewRedisRateLimitAdapter creates a new adapter for rate limiting
func (m *Manager) NewRedisRateLimitAdapter() *RedisRateLimitAdapter {
	if m.redisClient == nil {
		return nil
	}
	return &RedisRateLimitAdapter{client: m.redisClient}
}

// Incr atomically increments a key
func (a *RedisRateLimitAdapter) Incr(ctx context.Context, key string) (int64, error) {
	return a.client.Incr(ctx, key).Result()
}

// Expire sets key expiration
func (a *RedisRateLimitAdapter) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return a.client.Expire(ctx, key, expiration).Err()
}

// Get retrieves a value
func (a *RedisRateLimitAdapter) Get(ctx context.Context, key string) (string, error) {
	return a.client.Get(ctx, key).Result()
}

// Set sets a value with optional expiration
func (a *RedisRateLimitAdapter) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return a.client.Set(ctx, key, value, expiration).Err()
}

// Del deletes keys
func (a *RedisRateLimitAdapter) Del(ctx context.Context, keys ...string) error {
	return a.client.Del(ctx, keys...).Err()
}

// Exists checks if key exists
func (a *RedisRateLimitAdapter) Exists(ctx context.Context, keys ...string) (int64, error) {
	return a.client.Exists(ctx, keys...).Result()
}

// Eval runs a Lua script
func (a *RedisRateLimitAdapter) Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	return a.client.Eval(ctx, script, keys, args...).Result()
}

// TTL gets remaining TTL
func (a *RedisRateLimitAdapter) TTL(ctx context.Context, key string) (time.Duration, error) {
	return a.client.TTL(ctx, key).Result()
}

// Scan iterates keys
func (a *RedisRateLimitAdapter) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	return a.client.Scan(ctx, cursor, match, count).Result()
}

// Ping checks connectivity
func (a *RedisRateLimitAdapter) Ping(ctx context.Context) error {
	return a.client.Ping(ctx).Err()
}

// Close closes the connection (no-op as the Manager owns the connection)
func (a *RedisRateLimitAdapter) Close() error {
	// Don't close the underlying client - the Manager owns it
	return nil
}
