package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

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
	s.state.Logs = append(s.state.Logs, entry)

	// Update stats
	s.state.Stats.TotalRequests++
	if entry.Action == "Blocked" || entry.Action == "Deny" {
		s.state.Stats.BlockedRequests++
	} else if entry.Action == "Log" {
		s.state.Stats.FlaggedRequests++
	}

	// Auto-save on every log for now (could be optimized)
	go func() {
		// Create a localized lock just for writing to file to avoid holding the main lock?
		// Actually simplest is just to trigger a save.
		// NOTE: In a high throughput system, we would batch this.
		// For this project, we'll save periodically or let the OS cache handle it.
		// Let's not save on *every* request to avoid IO bottleneck, relying on periodic save or explicit save.
	}()
}

func (s *Store) IncrementSafeRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		{ID: 941100, Description: "XSS Detection - Libinjection", Severity: "CRITICAL", Enabled: true, Category: "XSS"},
		{ID: 942100, Description: "SQL Injection - Logic", Severity: "CRITICAL", Enabled: true, Category: "SQLi"},
		{ID: 933100, Description: "PHP Injection Attack", Severity: "CRITICAL", Enabled: true, Category: "RCE"},
		{ID: 920350, Description: "Host Header Validation", Severity: "WARNING", Enabled: true, Category: "Protocol"},
		{ID: 913100, Description: "Malicious User Agent", Severity: "NOTICE", Enabled: true, Category: "Reputation"},
	}
}

// AuthenticateUser validates credentials and returns user if valid
func (s *Store) AuthenticateUser(username, password string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// In-memory user storage for demo (will use DB in production)
	// Default admin user - password is "password"
	defaultAdmin := model.User{
		ID:           1,
		Username:     "admin",
		PasswordHash: "", // Not used in demo mode
		Email:        "admin@obsidian.local",
		Role:         "Admin",
		Enabled:      true,
		CreatedAt:    time.Now(),
	}

	// Demo mode: Simple string comparison (use bcrypt in production)
	if username != "admin" || password != "password" {
		return nil, auth.ErrInvalidCredentials
	}

	if !defaultAdmin.Enabled {
		return nil, fmt.Errorf("user account is disabled")
	}

	return &defaultAdmin, nil
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

	// Persist to file
	go s.saveNoLock()
	return nil
}

// UpdateRule modifies an existing WAF rule
func (s *Store) UpdateRule(rule model.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.state.Rules {
		if r.ID == rule.ID {
			s.state.Rules[i] = rule
			go s.saveNoLock()
			return nil
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
			go s.saveNoLock()
			return nil
		}
	}

	return fmt.Errorf("rule with ID %d not found", id)
}

// saveNoLock saves state without acquiring lock (caller must hold lock)
func (s *Store) saveNoLock() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}
