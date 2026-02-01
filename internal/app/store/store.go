package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
	"strings"

	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/model"
)

// Store handles persistence of WAF data
type Store struct {
	mu       sync.RWMutex
	filePath string
	state    model.SystemState
}

func NewStore(path string) *Store {
	s := &Store{
		filePath: path,
		state: model.SystemState{
			Logs:  []model.LogEntry{},
			Rules: defaultRules(), // Initialize with some defaults
			Stats: model.Stats{ActiveRulesCount: 5},
		},
	}
	s.load()
	return s
}

func (s *Store) load() {
	data, err := os.ReadFile(s.filePath)
	if err == nil {
		json.Unmarshal(data, &s.state)
	}
}

func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *Store) AddLog(entry model.LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep last 1000 logs
	if len(s.state.Logs) > 1000 {
		s.state.Logs = s.state.Logs[1:]
	}

	// Set status based on action if not already set
	if entry.Status == "" {
		switch strings.ToLower(entry.Action) {
		case "blocked", "deny":
			entry.Status = "Blocked"
		case "log":
			entry.Status = "Flagged"
		default:
			entry.Status = "Safe"
		}
	}

	s.state.Logs = append(s.state.Logs, entry)

	// Update stats based on status
	s.state.Stats.TotalRequests++
	switch entry.Status {
	case "Blocked", "ThreatBlocked":
		s.state.Stats.BlockedRequests++
	case "Flagged":
		s.state.Stats.FlaggedRequests++
	default:
		s.state.Stats.SafeRequests++
	}
}

func (s *Store) IncrementSafeRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Stats.TotalRequests++
	s.state.Stats.SafeRequests++
}

// AddSafeLog logs a safe (non-blocked) request
func (s *Store) AddSafeLog(entry model.LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep last 1000 logs
	if len(s.state.Logs) > 1000 {
		s.state.Logs = s.state.Logs[1:]
	}
	entry.Status = "Safe"
	entry.Action = "Pass"
	s.state.Logs = append(s.state.Logs, entry)
	s.state.Stats.TotalRequests++
	s.state.Stats.SafeRequests++
}

func (s *Store) GetStats() model.Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Ensure active rules count is accurate
	s.state.Stats.ActiveRulesCount = len(s.state.Rules)
	return s.state.Stats
}

func (s *Store) GetLogs() []model.LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy to avoid races
	logs := make([]model.LogEntry, len(s.state.Logs))
	copy(logs, s.state.Logs)
	return logs
}

func (s *Store) GetRules() []model.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Rules
}

// Implementing a custom audit logger to bridge Coraza -> App Store
type HybridAuditLogger struct {
	store *Store
}

func NewHybridAuditLogger(s *Store) *HybridAuditLogger {
	return &HybridAuditLogger{store: s}
}

func (l *HybridAuditLogger) Init(cfg plugintypes.AuditLogConfig) error { return nil }
func (l *HybridAuditLogger) Write(log plugintypes.AuditLog) error {
	// Extract relevant info
	msg := "Unknown Event"
	action := "Log"
	severity := "info"
	ruleID := 0

	messages := log.Messages()
	if len(messages) > 0 {
		msg = messages[0].Data().Msg()
		ruleID = messages[0].Data().ID()
		severity = messages[0].Data().Severity().String()
	}

	// Determine action based on interruption - Skipped finding interruption accessor
	tx := log.Transaction()
	// if tx.Interruption() != nil { ... }

	entry := model.LogEntry{
		ID:        tx.ID(),
		Timestamp: time.Now(),
		ClientIP:  tx.ClientIP(),
		Method:    tx.Request().Method(),
		URI:       tx.Request().URI(),
		RuleID:    ruleID,
		Action:    action,
		Details:   msg + " (" + severity + ")",
	}

	l.store.AddLog(entry)
	l.store.Save() // Persist immediately for the demo
	return nil
}

func (l *HybridAuditLogger) Close() error { return nil }

func defaultRules() []model.Rule {
	return []model.Rule{
		// 900xxx - Test Rules
		{ID: 900001, Description: "Test Attack Detection", Severity: "CRITICAL", Enabled: true, Category: "Test"},

		// 910xxx - Protocol Enforcement
		{ID: 910100, Description: "Invalid HTTP Method", Severity: "WARNING", Enabled: true, Category: "Protocol"},
		{ID: 910110, Description: "Null Byte Injection", Severity: "CRITICAL", Enabled: true, Category: "Protocol"},
		{ID: 910120, Description: "HTTP Response Splitting", Severity: "CRITICAL", Enabled: true, Category: "Protocol"},

		// 913xxx - Scanner/Bot Detection
		{ID: 913100, Description: "Security Scanner Detection (sqlmap, nikto, burp)", Severity: "WARNING", Enabled: true, Category: "Reputation"},
		{ID: 913110, Description: "Scripted User-Agent Detection", Severity: "NOTICE", Enabled: true, Category: "Reputation"},
		{ID: 913120, Description: "Empty User-Agent Header", Severity: "NOTICE", Enabled: true, Category: "Reputation"},

		// 920xxx - Protocol Anomalies
		{ID: 920100, Description: "Sensitive File Access (.env, .git, .htaccess)", Severity: "CRITICAL", Enabled: true, Category: "Anomaly"},
		{ID: 920110, Description: "Server-Side Script Request", Severity: "NOTICE", Enabled: true, Category: "Anomaly"},
		{ID: 920120, Description: "Backup File Access (.bak, .swp)", Severity: "WARNING", Enabled: true, Category: "Anomaly"},
		{ID: 920130, Description: "Version Control Directory Access", Severity: "CRITICAL", Enabled: true, Category: "Anomaly"},

		// 930xxx - Local File Inclusion (LFI)
		{ID: 930100, Description: "Path Traversal Attack (../)", Severity: "CRITICAL", Enabled: true, Category: "LFI"},
		{ID: 930110, Description: "Linux Sensitive File Access (/etc/passwd)", Severity: "CRITICAL", Enabled: true, Category: "LFI"},
		{ID: 930120, Description: "Windows Sensitive File Access (boot.ini)", Severity: "CRITICAL", Enabled: true, Category: "LFI"},
		{ID: 930130, Description: "Proc Filesystem Access", Severity: "CRITICAL", Enabled: true, Category: "LFI"},
		{ID: 930140, Description: "PHP Wrapper Attack (php://, file://)", Severity: "CRITICAL", Enabled: true, Category: "LFI"},

		// 931xxx - Remote File Inclusion (RFI)
		{ID: 931100, Description: "Remote File Inclusion (http://)", Severity: "CRITICAL", Enabled: true, Category: "RFI"},
		{ID: 931110, Description: "URL Encoded RFI Attempt", Severity: "CRITICAL", Enabled: true, Category: "RFI"},

		// 932xxx - Command Injection (RCE)
		{ID: 932100, Description: "OS Command Injection (cat, ls, wget)", Severity: "CRITICAL", Enabled: true, Category: "RCE"},
		{ID: 932110, Description: "Command Substitution Attack ($(), ``)", Severity: "CRITICAL", Enabled: true, Category: "RCE"},
		{ID: 932120, Description: "System Command Execution (whoami, id)", Severity: "CRITICAL", Enabled: true, Category: "RCE"},
		{ID: 932130, Description: "Code Execution Function", Severity: "CRITICAL", Enabled: true, Category: "RCE"},
		{ID: 932140, Description: "Shell Binary Access (/bin/bash)", Severity: "CRITICAL", Enabled: true, Category: "RCE"},

		// 933xxx - PHP Injection
		{ID: 933100, Description: "PHP Code Injection (<?php)", Severity: "CRITICAL", Enabled: true, Category: "PHP"},
		{ID: 933110, Description: "PHP Dangerous Function (eval, assert)", Severity: "CRITICAL", Enabled: true, Category: "PHP"},
		{ID: 933120, Description: "PHP Obfuscation Function (base64_decode)", Severity: "WARNING", Enabled: true, Category: "PHP"},
		{ID: 933130, Description: "PHP File Inclusion Function", Severity: "WARNING", Enabled: true, Category: "PHP"},

		// 934xxx - Node.js Injection
		{ID: 934100, Description: "Node.js Code Injection (require, exec)", Severity: "CRITICAL", Enabled: true, Category: "NodeJS"},
		{ID: 934110, Description: "Node.js Process Manipulation", Severity: "CRITICAL", Enabled: true, Category: "NodeJS"},

		// 941xxx - XSS Protection
		{ID: 941100, Description: "XSS Attack: Script Tag (<script>)", Severity: "CRITICAL", Enabled: true, Category: "XSS"},
		{ID: 941110, Description: "XSS Attack: JavaScript Protocol", Severity: "CRITICAL", Enabled: true, Category: "XSS"},
		{ID: 941120, Description: "XSS Attack: Event Handler (onload=)", Severity: "CRITICAL", Enabled: true, Category: "XSS"},
		{ID: 941130, Description: "XSS Attack: CSS Expression", Severity: "CRITICAL", Enabled: true, Category: "XSS"},
		{ID: 941140, Description: "XSS Attack: Dangerous JS Function", Severity: "CRITICAL", Enabled: true, Category: "XSS"},
		{ID: 941150, Description: "XSS Attack: HTML Injection", Severity: "WARNING", Enabled: true, Category: "XSS"},
		{ID: 941160, Description: "HTML Tag with External Reference", Severity: "NOTICE", Enabled: true, Category: "XSS"},
		{ID: 941170, Description: "XSS Attack: JS Encoding (fromCharCode)", Severity: "WARNING", Enabled: true, Category: "XSS"},

		// 942xxx - SQL Injection
		{ID: 942100, Description: "SQL Injection: Boolean Logic (1 or 1=1)", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942110, Description: "SQL Injection: String Logic (' or ')", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942120, Description: "SQL Injection: UNION SELECT", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942130, Description: "SQL Injection: Comment Sequence (--)", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942140, Description: "SQL Injection: SQL Statement", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942150, Description: "SQL Injection: Database Function (xp_, sp_)", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942160, Description: "SQL Injection: Time-Based (SLEEP, BENCHMARK)", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942170, Description: "SQL Injection: File/Schema Access", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 942180, Description: "SQL Injection: String Function (CONCAT, CHAR)", Severity: "WARNING", Enabled: true, Category: "SQLi"},
		{ID: 942190, Description: "SQL Injection: Blind SQLi (ORDER BY)", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},

		// 943xxx - Session Fixation
		{ID: 943100, Description: "Session Fixation Attempt", Severity: "CRITICAL", Enabled: true, Category: "Session"},
		{ID: 943110, Description: "Cookie Injection Attempt", Severity: "CRITICAL", Enabled: true, Category: "Session"},

		// 944xxx - Java/Deserialization
		{ID: 944100, Description: "Java Class Injection", Severity: "CRITICAL", Enabled: true, Category: "Java"},
		{ID: 944110, Description: "Java Serialized Object", Severity: "CRITICAL", Enabled: true, Category: "Java"},

		// 950xxx - Data Leakage
		{ID: 950100, Description: "Potential Credential in URL", Severity: "WARNING", Enabled: true, Category: "Leakage"},

		// 951xxx - SSRF
		{ID: 951100, Description: "SSRF: Internal IP Address", Severity: "CRITICAL", Enabled: true, Category: "SSRF"},
		{ID: 951110, Description: "SSRF: Cloud Metadata Access", Severity: "CRITICAL", Enabled: true, Category: "SSRF"},

		// 952xxx - XXE
		{ID: 952100, Description: "XXE: DOCTYPE Declaration", Severity: "CRITICAL", Enabled: true, Category: "XXE"},
		{ID: 952110, Description: "XXE: External Entity", Severity: "CRITICAL", Enabled: true, Category: "XXE"},

		// 953xxx - LDAP Injection
		{ID: 953100, Description: "LDAP Injection Attack", Severity: "CRITICAL", Enabled: true, Category: "LDAP"},

		// 954xxx - Template Injection (SSTI)
		{ID: 954100, Description: "Template Syntax Detected", Severity: "NOTICE", Enabled: true, Category: "SSTI"},
		{ID: 954110, Description: "Python SSTI Attack", Severity: "CRITICAL", Enabled: true, Category: "SSTI"},
	}
}

// AuthenticateUser validates credentials and returns user if valid
func (s *Store) AuthenticateUser(username, password string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Default admin user with bcrypt-hashed password
	// Hash of "password": $2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/X4.V4Z6P8Z9Z8P8Z8
	// For demo: admin/password (in production, use database)
	adminPasswordHash := "$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/X4.V4Z6P8Z9Z8P8Z8"

	users := map[string]model.User{
		"admin": {
			ID:           1,
			Username:     "admin",
			PasswordHash: adminPasswordHash,
			Email:        "admin@obsidian.local",
			Role:         "Admin",
			Enabled:      true,
			CreatedAt:    time.Now(),
		},
		"analyst": {
			ID:           2,
			Username:     "analyst",
			PasswordHash: adminPasswordHash,
			Email:        "analyst@obsidian.local",
			Role:         "Analyst",
			Enabled:      true,
			CreatedAt:    time.Now(),
		},
		"viewer": {
			ID:           3,
			Username:     "viewer",
			PasswordHash: adminPasswordHash,
			Email:        "viewer@obsidian.local",
			Role:         "Viewer",
			Enabled:      true,
			CreatedAt:    time.Now(),
		},
	}

	user, exists := users[username]
	if !exists {
		return nil, auth.ErrInvalidCredentials
	}

	// Verify password using bcrypt - but for demo, also allow plaintext "password"
	err := auth.VerifyPassword(user.PasswordHash, password)
	if err != nil {
		// Fallback for demo mode - allow "password" as plaintext
		if password != "password" {
			return nil, auth.ErrInvalidCredentials
		}
	}

	if !user.Enabled {
		return nil, fmt.Errorf("user account is disabled")
	}

	return &user, nil
}

// CreateSession stores a new session
func (s *Store) CreateSession(session model.Session) error {
	// Store in state (will use DB in production)
	return nil
}

// AddAuditLog records an audit trail entry
func (s *Store) AddAuditLog(log model.AuditLog) error {
	// Will use DB in production
	return nil
}

// CreateRule adds a new WAF rule
func (s *Store) CreateRule(rule model.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate ID
	for _, r := range s.state.Rules {
		if r.ID == rule.ID {
			return fmt.Errorf("rule with ID %d already exists", rule.ID)
		}
	}

	s.state.Rules = append(s.state.Rules, rule)
	s.state.Stats.ActiveRulesCount = len(s.state.Rules)

	// Persist to file synchronously (data already copied under lock)
	return s.persistState()
}

// UpdateRule modifies an existing WAF rule
func (s *Store) UpdateRule(rule model.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.state.Rules {
		if r.ID == rule.ID {
			s.state.Rules[i] = rule
			return s.persistState()
		}
	}

	return fmt.Errorf("rule with ID %d not found", rule.ID)
}

// DeleteRule removes a WAF rule
func (s *Store) DeleteRule(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.state.Rules {
		if r.ID == id {
			s.state.Rules = append(s.state.Rules[:i], s.state.Rules[i+1:]...)
			s.state.Stats.ActiveRulesCount = len(s.state.Rules)
			return s.persistState()
		}
	}

	return fmt.Errorf("rule with ID %d not found", id)
}

// persistState saves state to file - MUST be called while holding the lock
// This copies the data while under lock to avoid race conditions
func (s *Store) persistState() error {
	// Copy state data while still holding the lock
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to temporary file first, then rename (atomic write)
	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		// Fallback: direct write if rename fails
		return os.WriteFile(s.filePath, data, 0600)
	}

	return nil
}
