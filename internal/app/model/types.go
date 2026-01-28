package model

import "time"

// LogEntry represents a security event detected by the WAF
type LogEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	ClientIP  string    `json:"client_ip"`
	Method    string    `json:"method"`
	URI       string    `json:"uri"`
	RuleID    int       `json:"rule_id"`
	Action    string    `json:"action"` // Blocked, Logged, etc.
	Details   string    `json:"details"`
}

// Stats represents aggregated traffic data
type Stats struct {
	TotalRequests    int64 `json:"total_requests"`
	BlockedRequests  int64 `json:"blocked_requests"`
	FlaggedRequests  int64 `json:"flagged_requests"`
	SafeRequests     int64 `json:"safe_requests"`
	ActiveRulesCount int   `json:"active_rules_count"`
}

// Rule represents a WAF rule (simplified for UI)
type Rule struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Enabled     bool   `json:"enabled"`
	Category    string `json:"category"`
}

// SystemState is the root object for persistence
type SystemState struct {
	Logs  []LogEntry `json:"logs"`
	Stats Stats      `json:"stats"`
	Rules []Rule     `json:"rules"`
}
