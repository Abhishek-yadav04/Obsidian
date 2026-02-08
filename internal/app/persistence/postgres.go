// Package persistence provides database-backed storage for Obsidian WAF.
// This implementation addresses the critical audit finding of in-memory only
// persistence and provides proper PostgreSQL integration.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors for persistence operations
var (
	ErrUserNotFound     = errors.New("user not found")
	ErrUserDisabled     = errors.New("user account is disabled")
	ErrDuplicateUser    = errors.New("user already exists")
	ErrDuplicateRule    = errors.New("rule with this ID already exists")
	ErrRuleNotFound     = errors.New("rule not found")
	ErrConnectionFailed = errors.New("database connection failed")
)

// Store defines the interface for all persistence operations
// This allows for multiple implementations (PostgreSQL, SQLite, in-memory)
type Store interface {
	// User operations
	GetUser(ctx context.Context, username string) (*model.User, error)
	CreateUser(ctx context.Context, user *model.User) error
	UpdateUser(ctx context.Context, user *model.User) error
	DeleteUser(ctx context.Context, id int) error
	ListUsers(ctx context.Context) ([]model.User, error)

	// Log operations
	AddLog(ctx context.Context, entry *model.LogEntry) error
	GetLogs(ctx context.Context, limit, offset int) ([]model.LogEntry, error)
	GetLogsFiltered(ctx context.Context, filter LogFilter) ([]model.LogEntry, error)
	GetLogCount(ctx context.Context) (int64, error)

	// Rule operations
	CreateRule(ctx context.Context, rule *model.Rule) error
	UpdateRule(ctx context.Context, rule *model.Rule) error
	DeleteRule(ctx context.Context, id int) error
	GetRule(ctx context.Context, id int) (*model.Rule, error)
	ListRules(ctx context.Context) ([]model.Rule, error)

	// Stats operations
	GetStats(ctx context.Context) (*model.Stats, error)
	IncrementStat(ctx context.Context, stat string, delta int64) error

	// Audit log operations
	AddAuditLog(ctx context.Context, log *model.AuditLog) error
	GetAuditLogs(ctx context.Context, limit, offset int) ([]model.AuditLog, error)

	// Session operations
	CreateSession(ctx context.Context, session *model.Session) error
	GetSession(ctx context.Context, id string) (*model.Session, error)
	UpdateSession(ctx context.Context, session *model.Session) error
	DeleteSession(ctx context.Context, id string) error
	DeleteUserSessions(ctx context.Context, userID int) error

	// User lookup by ID
	GetUserByID(ctx context.Context, id int) (*model.User, error)

	// Health check
	Ping(ctx context.Context) error

	// Cleanup
	Close() error
}

// LogFilter for querying logs with various criteria
type LogFilter struct {
	StartTime  *time.Time
	EndTime    *time.Time
	ClientIP   string
	Status     string
	RuleID     *int
	Method     string
	Limit      int
	Offset     int
	OrderBy    string
	Descending bool
}

// PostgresStore implements Store using PostgreSQL
type PostgresStore struct {
	pool *pgxpool.Pool
	mu   sync.RWMutex
}

// PostgresConfig holds PostgreSQL connection settings
type PostgresConfig struct {
	Host        string
	Port        int
	User        string
	Password    string
	Database    string
	SSLMode     string
	MaxConns    int32
	MinConns    int32
	MaxConnLife time.Duration
	MaxConnIdle time.Duration
	HealthCheck time.Duration
	ConnTimeout time.Duration
}

// DefaultPostgresConfig returns sensible defaults
func DefaultPostgresConfig() PostgresConfig {
	return PostgresConfig{
		Host:        "localhost",
		Port:        5432,
		User:        "obsidian",
		Password:    "",
		Database:    "obsidian_waf",
		SSLMode:     "prefer",
		MaxConns:    25,
		MinConns:    5,
		MaxConnLife: time.Hour,
		MaxConnIdle: 30 * time.Minute,
		HealthCheck: time.Minute,
		ConnTimeout: 5 * time.Second,
	}
}

// NewPostgresStore creates a new PostgreSQL-backed store
func NewPostgresStore(ctx context.Context, cfg PostgresConfig) (*PostgresStore, error) {
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database, cfg.SSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLife
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdle
	poolConfig.HealthCheckPeriod = cfg.HealthCheck
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	store := &PostgresStore{pool: pool}

	// Run migrations
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return store, nil
}

// migrate runs database migrations
func (s *PostgresStore) migrate(ctx context.Context) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			email VARCHAR(255) UNIQUE NOT NULL,
			role VARCHAR(50) NOT NULL DEFAULT 'Viewer',
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			last_login TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE TABLE IF NOT EXISTS waf_logs (
			id VARCHAR(255) PRIMARY KEY,
			timestamp TIMESTAMP WITH TIME ZONE NOT NULL,
			client_ip VARCHAR(45) NOT NULL,
			method VARCHAR(10) NOT NULL,
			uri TEXT NOT NULL,
			rule_id INTEGER,
			action VARCHAR(50) NOT NULL,
			status VARCHAR(50) NOT NULL,
			details TEXT,
			status_code INTEGER NOT NULL,
			user_agent TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_waf_logs_timestamp ON waf_logs(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_waf_logs_client_ip ON waf_logs(client_ip)`,
		`CREATE INDEX IF NOT EXISTS idx_waf_logs_status ON waf_logs(status)`,
		`CREATE TABLE IF NOT EXISTS waf_rules (
			id INTEGER PRIMARY KEY,
			description TEXT NOT NULL,
			severity VARCHAR(50) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT true,
			category VARCHAR(100) NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS waf_stats (
			id SERIAL PRIMARY KEY,
			stat_name VARCHAR(100) UNIQUE NOT NULL,
			stat_value BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO waf_stats (stat_name, stat_value) VALUES 
			('total_requests', 0),
			('blocked_requests', 0),
			('flagged_requests', 0),
			('safe_requests', 0)
		ON CONFLICT (stat_name) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id SERIAL PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			action VARCHAR(100) NOT NULL,
			resource VARCHAR(255) NOT NULL,
			details TEXT,
			ip_address VARCHAR(45),
			timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp ON audit_logs(timestamp DESC)`,
		`CREATE TABLE IF NOT EXISTS rule_audit_log (
			id SERIAL PRIMARY KEY,
			rule_id INTEGER NOT NULL,
			action VARCHAR(10) NOT NULL,
			old_value TEXT,
			new_value TEXT,
			actor VARCHAR(255) NOT NULL,
			timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rule_audit_log_timestamp ON rule_audit_log(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_rule_audit_log_rule_id ON rule_audit_log(rule_id)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id VARCHAR(255) PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token TEXT NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			last_activity TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			ip_address VARCHAR(45),
			user_agent TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
	}

	for _, migration := range migrations {
		if _, err := s.pool.Exec(ctx, migration); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

// Ping checks database connectivity
func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close closes the database connection pool
func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

// GetUser retrieves a user by username
func (s *PostgresStore) GetUser(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	var lastLogin sql.NullTime

	err := s.pool.QueryRow(ctx,
		`SELECT id, username, password_hash, email, role, enabled, created_at, last_login 
		FROM users WHERE username = $1`,
		username,
	).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.Email,
		&user.Role, &user.Enabled, &user.CreatedAt, &lastLogin,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if lastLogin.Valid {
		user.LastLogin = &lastLogin.Time
	}

	return &user, nil
}

// CreateUser creates a new user
func (s *PostgresStore) CreateUser(ctx context.Context, user *model.User) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, email, role, enabled, created_at) 
		VALUES ($1, $2, $3, $4, $5, $6)`,
		user.Username, user.PasswordHash, user.Email, user.Role, user.Enabled, time.Now(),
	)

	if err != nil {
		// Check for unique constraint violation
		if isPgDuplicateError(err) {
			return ErrDuplicateUser
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// UpdateUser updates an existing user
func (s *PostgresStore) UpdateUser(ctx context.Context, user *model.User) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE users SET email = $1, role = $2, enabled = $3, last_login = $4 
		WHERE id = $5`,
		user.Email, user.Role, user.Enabled, user.LastLogin, user.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrUserNotFound
	}

	return nil
}

// DeleteUser deletes a user
func (s *PostgresStore) DeleteUser(ctx context.Context, id int) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrUserNotFound
	}

	return nil
}

// ListUsers returns all users
func (s *PostgresStore) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, username, password_hash, email, role, enabled, created_at, last_login 
		FROM users ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var user model.User
		var lastLogin sql.NullTime
		if err := rows.Scan(
			&user.ID, &user.Username, &user.PasswordHash, &user.Email,
			&user.Role, &user.Enabled, &user.CreatedAt, &lastLogin,
		); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		if lastLogin.Valid {
			user.LastLogin = &lastLogin.Time
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating users: %w", err)
	}

	return users, nil
}

// AddLog adds a new WAF log entry
func (s *PostgresStore) AddLog(ctx context.Context, entry *model.LogEntry) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO waf_logs (id, timestamp, client_ip, method, uri, rule_id, action, status, details, status_code, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		entry.ID, entry.Timestamp, entry.ClientIP, entry.Method, entry.URI,
		entry.RuleID, entry.Action, entry.Status, entry.Details, entry.StatusCode, entry.UserAgent,
	)

	if err != nil {
		return fmt.Errorf("failed to add log: %w", err)
	}

	// Update stats based on status
	statName := "safe_requests"
	switch entry.Status {
	case "Blocked", "ThreatBlocked":
		statName = "blocked_requests"
	case "Flagged":
		statName = "flagged_requests"
	}

	// Increment total and specific stat
	if err := s.IncrementStat(ctx, "total_requests", 1); err != nil {
		return fmt.Errorf("failed to increment total_requests: %w", err)
	}
	if err := s.IncrementStat(ctx, statName, 1); err != nil {
		return fmt.Errorf("failed to increment %s: %w", statName, err)
	}

	return nil
}

// GetLogs retrieves logs with pagination
func (s *PostgresStore) GetLogs(ctx context.Context, limit, offset int) ([]model.LogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, timestamp, client_ip, method, uri, rule_id, action, status, details, status_code, user_agent
		FROM waf_logs ORDER BY timestamp DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs: %w", err)
	}
	defer rows.Close()

	return scanLogs(rows)
}

// GetLogsFiltered retrieves logs with filtering
func (s *PostgresStore) GetLogsFiltered(ctx context.Context, filter LogFilter) ([]model.LogEntry, error) {
	query := `SELECT id, timestamp, client_ip, method, uri, rule_id, action, status, details, status_code, user_agent FROM waf_logs WHERE 1=1`
	args := make([]interface{}, 0)
	argNum := 1

	if filter.StartTime != nil {
		query += fmt.Sprintf(" AND timestamp >= $%d", argNum)
		args = append(args, *filter.StartTime)
		argNum++
	}
	if filter.EndTime != nil {
		query += fmt.Sprintf(" AND timestamp <= $%d", argNum)
		args = append(args, *filter.EndTime)
		argNum++
	}
	if filter.ClientIP != "" {
		query += fmt.Sprintf(" AND client_ip = $%d", argNum)
		args = append(args, filter.ClientIP)
		argNum++
	}
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argNum)
		args = append(args, filter.Status)
		argNum++
	}
	if filter.RuleID != nil {
		query += fmt.Sprintf(" AND rule_id = $%d", argNum)
		args = append(args, *filter.RuleID)
		argNum++
	}
	if filter.Method != "" {
		query += fmt.Sprintf(" AND method = $%d", argNum)
		args = append(args, filter.Method)
		argNum++
	}

	// Order by - whitelist allowed columns to prevent SQL injection
	allowedOrderBy := map[string]bool{
		"timestamp": true, "client_ip": true, "method": true,
		"uri": true, "rule_id": true, "action": true,
		"status": true, "status_code": true,
	}
	orderBy := "timestamp"
	if filter.OrderBy != "" && allowedOrderBy[filter.OrderBy] {
		orderBy = filter.OrderBy
	}
	order := "DESC"
	if !filter.Descending {
		order = "ASC"
	}
	query += fmt.Sprintf(" ORDER BY %s %s", orderBy, order)

	// Pagination
	limit := 100
	if filter.Limit > 0 && filter.Limit <= 1000 {
		limit = filter.Limit
	}
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argNum, argNum+1)
	args = append(args, limit, filter.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get filtered logs: %w", err)
	}
	defer rows.Close()

	return scanLogs(rows)
}

// GetLogCount returns total number of logs
func (s *PostgresStore) GetLogCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM waf_logs`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get log count: %w", err)
	}
	return count, nil
}

// scanLogs is a helper to scan log rows
func scanLogs(rows pgx.Rows) ([]model.LogEntry, error) {
	var logs []model.LogEntry
	for rows.Next() {
		var log model.LogEntry
		var ruleID sql.NullInt32
		if err := rows.Scan(
			&log.ID, &log.Timestamp, &log.ClientIP, &log.Method, &log.URI,
			&ruleID, &log.Action, &log.Status, &log.Details, &log.StatusCode, &log.UserAgent,
		); err != nil {
			return nil, fmt.Errorf("failed to scan log: %w", err)
		}
		if ruleID.Valid {
			log.RuleID = int(ruleID.Int32)
		}
		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating logs: %w", err)
	}

	return logs, nil
}

// CreateRule creates a new WAF rule
func (s *PostgresStore) CreateRule(ctx context.Context, rule *model.Rule) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO waf_rules (id, description, severity, enabled, category) VALUES ($1, $2, $3, $4, $5)`,
		rule.ID, rule.Description, rule.Severity, rule.Enabled, rule.Category,
	)

	if err != nil {
		if isPgDuplicateError(err) {
			return ErrDuplicateRule
		}
		return fmt.Errorf("failed to create rule: %w", err)
	}

	return nil
}

// UpdateRule updates an existing WAF rule
func (s *PostgresStore) UpdateRule(ctx context.Context, rule *model.Rule) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE waf_rules SET description = $1, severity = $2, enabled = $3, category = $4, updated_at = $5 
		WHERE id = $6`,
		rule.Description, rule.Severity, rule.Enabled, rule.Category, time.Now(), rule.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update rule: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrRuleNotFound
	}

	return nil
}

// DeleteRule deletes a WAF rule
func (s *PostgresStore) DeleteRule(ctx context.Context, id int) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM waf_rules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete rule: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrRuleNotFound
	}

	return nil
}

// GetRule retrieves a single rule by ID
func (s *PostgresStore) GetRule(ctx context.Context, id int) (*model.Rule, error) {
	var rule model.Rule
	err := s.pool.QueryRow(ctx,
		`SELECT id, description, severity, enabled, category FROM waf_rules WHERE id = $1`,
		id,
	).Scan(&rule.ID, &rule.Description, &rule.Severity, &rule.Enabled, &rule.Category)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRuleNotFound
		}
		return nil, fmt.Errorf("failed to get rule: %w", err)
	}

	return &rule, nil
}

// ListRules returns all WAF rules
func (s *PostgresStore) ListRules(ctx context.Context) ([]model.Rule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, description, severity, enabled, category FROM waf_rules ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list rules: %w", err)
	}
	defer rows.Close()

	var rules []model.Rule
	for rows.Next() {
		var rule model.Rule
		if err := rows.Scan(&rule.ID, &rule.Description, &rule.Severity, &rule.Enabled, &rule.Category); err != nil {
			return nil, fmt.Errorf("failed to scan rule: %w", err)
		}
		rules = append(rules, rule)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rules: %w", err)
	}

	return rules, nil
}

// GetStats retrieves current statistics
func (s *PostgresStore) GetStats(ctx context.Context) (*model.Stats, error) {
	rows, err := s.pool.Query(ctx, `SELECT stat_name, stat_value FROM waf_stats`)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}
	defer rows.Close()

	stats := &model.Stats{}
	for rows.Next() {
		var name string
		var value int64
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("failed to scan stat: %w", err)
		}

		switch name {
		case "total_requests":
			stats.TotalRequests = value
		case "blocked_requests":
			stats.BlockedRequests = value
		case "flagged_requests":
			stats.FlaggedRequests = value
		case "safe_requests":
			stats.SafeRequests = value
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stats: %w", err)
	}

	// Get active rules count
	var ruleCount int
	err = s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM waf_rules WHERE enabled = true`).Scan(&ruleCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count rules: %w", err)
	}
	stats.ActiveRulesCount = ruleCount

	return stats, nil
}

// IncrementStat atomically increments a statistic (upserts if not exists)
func (s *PostgresStore) IncrementStat(ctx context.Context, stat string, delta int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO waf_stats (stat_name, stat_value, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (stat_name) DO UPDATE SET stat_value = waf_stats.stat_value + $2, updated_at = $3`,
		stat, delta, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to increment stat %s: %w", stat, err)
	}
	return nil
}

// AddAuditLog records an audit trail entry
func (s *PostgresStore) AddAuditLog(ctx context.Context, log *model.AuditLog) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, resource, details, ip_address, timestamp) 
		VALUES ($1, $2, $3, $4, $5, $6)`,
		log.UserID, log.Action, log.Resource, log.Details, log.IPAddress, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to add audit log: %w", err)
	}
	return nil
}

// GetAuditLogs retrieves audit logs with pagination
func (s *PostgresStore) GetAuditLogs(ctx context.Context, limit, offset int) ([]model.AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, action, resource, details, ip_address, timestamp 
		FROM audit_logs ORDER BY timestamp DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get audit logs: %w", err)
	}
	defer rows.Close()

	var logs []model.AuditLog
	for rows.Next() {
		var log model.AuditLog
		var userID sql.NullInt32
		if err := rows.Scan(&log.ID, &userID, &log.Action, &log.Resource, &log.Details, &log.IPAddress, &log.Timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}
		if userID.Valid {
			id := int(userID.Int32)
			log.UserID = &id
		}
		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating audit logs: %w", err)
	}

	return logs, nil
}

// CreateSession stores a new session
func (s *PostgresStore) CreateSession(ctx context.Context, session *model.Session) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, token, expires_at, ip_address, user_agent) 
		VALUES ($1, $2, $3, $4, $5, $6)`,
		session.ID, session.UserID, session.Token, session.ExpiresAt, session.IPAddress, session.UserAgent,
	)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID
func (s *PostgresStore) GetSession(ctx context.Context, id string) (*model.Session, error) {
	var session model.Session
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, token, expires_at, created_at, last_activity, ip_address, user_agent 
		FROM sessions WHERE id = $1`,
		id,
	).Scan(
		&session.ID, &session.UserID, &session.Token, &session.ExpiresAt,
		&session.CreatedAt, &session.LastActivity, &session.IPAddress, &session.UserAgent,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("session not found")
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return &session, nil
}

// DeleteSession deletes a session
func (s *PostgresStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

// DeleteUserSessions deletes all sessions for a user
func (s *PostgresStore) DeleteUserSessions(ctx context.Context, userID int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// GetUserByID retrieves a user by their numeric ID
func (s *PostgresStore) GetUserByID(ctx context.Context, id int) (*model.User, error) {
	var user model.User
	var lastLogin sql.NullTime

	err := s.pool.QueryRow(ctx,
		`SELECT id, username, password_hash, email, role, enabled, created_at, last_login 
		FROM users WHERE id = $1`,
		id,
	).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.Email,
		&user.Role, &user.Enabled, &user.CreatedAt, &lastLogin,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by ID: %w", err)
	}

	if lastLogin.Valid {
		user.LastLogin = &lastLogin.Time
	}

	return &user, nil
}

// UpdateSession updates an existing session's mutable fields (e.g., last_activity)
func (s *PostgresStore) UpdateSession(ctx context.Context, session *model.Session) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET last_activity = $1 WHERE id = $2`,
		session.LastActivity, session.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}
	return nil
}

// isPgDuplicateError checks if an error is a PostgreSQL unique constraint violation (code 23505)
func isPgDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	// Use proper pgconn type assertion instead of fragile string matching
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	// Fallback for wrapped errors that don't unwrap to PgError
	errMsg := err.Error()
	return strings.Contains(errMsg, "23505")
}
