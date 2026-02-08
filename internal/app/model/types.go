package model

import "time"

// User represents an authenticated user in the system
type User struct {
	ID           int        `json:"id" db:"id"`
	Username     string     `json:"username" db:"username"`
	PasswordHash string     `json:"-" db:"password_hash"` // Never serialize password
	Email        string     `json:"email" db:"email"`
	Role         string     `json:"role" db:"role"` // Admin, Analyst, Viewer
	Enabled      bool       `json:"enabled" db:"enabled"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	LastLogin    *time.Time `json:"last_login,omitempty" db:"last_login"`
}

// Session represents an active user session
type Session struct {
	ID           string    `json:"id" db:"id"`
	UserID       int       `json:"user_id" db:"user_id"`
	Token        string    `json:"-"`                 // JWT refresh token (never persisted)
	TokenHash    string    `json:"-" db:"token_hash"` // Hashed token for DB storage
	ExpiresAt    time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	LastActivity time.Time `json:"last_activity" db:"last_activity"`
	IPAddress    string    `json:"ip_address" db:"ip_address"`
	UserAgent    string    `json:"user_agent" db:"user_agent"`
}

// AuditLog represents a system audit trail entry
type AuditLog struct {
	ID        int       `json:"id" db:"id"`
	UserID    *int      `json:"user_id,omitempty" db:"user_id"`
	Action    string    `json:"action" db:"action"`
	Resource  string    `json:"resource" db:"resource"`
	Details   string    `json:"details" db:"details"`
	IPAddress string    `json:"ip_address" db:"ip_address"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
}

// RuleAuditLog represents an audit trail entry for custom rule changes
type RuleAuditLog struct {
	ID        int       `json:"id" db:"id"`
	RuleID    int       `json:"rule_id" db:"rule_id"`
	Action    string    `json:"action" db:"action"`
	OldValue  string    `json:"old_value,omitempty" db:"old_value"`
	NewValue  string    `json:"new_value,omitempty" db:"new_value"`
	Actor     string    `json:"actor" db:"actor"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
}

// CRSStatus represents read-only CRS visibility information
type CRSStatus struct {
	Enabled      bool      `json:"enabled"`
	Version      string    `json:"version"`
	Fingerprint  string    `json:"fingerprint,omitempty"`
	TotalRules   int       `json:"total_rules"`
	ActiveRules  int       `json:"active_rules"`
	LastLoadedAt time.Time `json:"last_loaded_at"`
	Source       string    `json:"source"`
}

// LogEntry represents a request processed by the WAF
type LogEntry struct {
	ID             string    `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	ClientIP       string    `json:"client_ip"`
	Method         string    `json:"method"`
	URI            string    `json:"uri"`
	RuleID         int       `json:"rule_id"`
	Action         string    `json:"action"` // Blocked, Logged, Pass, etc.
	Status         string    `json:"status"` // Safe, Blocked, Flagged, ThreatBlocked
	Details        string    `json:"details"`
	StatusCode     int       `json:"status_code"` // HTTP response code
	UserAgent      string    `json:"user_agent"`
	ResponseTimeMs float64   `json:"response_time_ms"` // Request processing time in milliseconds
}

// Stats represents aggregated traffic data
type Stats struct {
	TotalRequests     int64   `json:"total_requests"`
	BlockedRequests   int64   `json:"blocked_requests"`
	FlaggedRequests   int64   `json:"flagged_requests"`
	SafeRequests      int64   `json:"safe_requests"`
	ActiveRulesCount  int     `json:"active_rules_count"`
	AvgResponseTimeMs float64 `json:"avg_response_time_ms"`
	// Internal accumulators (not serialized to JSON for display but used for computation)
	TotalResponseTimeNs int64 `json:"-"`
	ResponseCount       int64 `json:"-"`
}

// Rule represents a WAF rule with full configuration
type Rule struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
	Severity    string `json:"severity"` // CRITICAL, HIGH, MEDIUM, LOW, NOTICE
	Enabled     bool   `json:"enabled"`
	Category    string `json:"category"`
	Source      string `json:"source,omitempty"`    // custom, crs
	ReadOnly    bool   `json:"read_only,omitempty"` // true for file-based rules

	// Rule Configuration (what to block and when)
	Pattern     string `json:"pattern,omitempty"`      // Regex pattern to match
	TargetField string `json:"target_field,omitempty"` // REQUEST_URI, QUERY_STRING, REQUEST_HEADERS, etc.
	Action      string `json:"action,omitempty"`       // deny, log, pass, drop
	BlockStatus int    `json:"block_status,omitempty"` // HTTP status code when blocking (403, 400, etc.)
	Threshold   int    `json:"threshold,omitempty"`    // Number of matches before action
	TimeWindow  int    `json:"time_window,omitempty"`  // Time window in seconds for threshold

	// Statistics
	MatchCount int64  `json:"match_count"`          // Total times this rule matched
	LastMatch  string `json:"last_match,omitempty"` // Timestamp of last match
}

// SystemState is the root object for persistence
type SystemState struct {
	Logs  []LogEntry `json:"logs"`
	Stats Stats      `json:"stats"`
	Rules []Rule     `json:"rules"`
	Users []User     `json:"users"`
}

// LoginRequest represents authentication request
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse represents authentication response
type LoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	User         User   `json:"user"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}

// TokenClaims represents JWT token claims
type TokenClaims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
	Iat      int64  `json:"iat"`
}
