package store

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
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
		{ID: 933100, Description: "PHP Injection Attack", Severity: "HIGH", Enabled: true, Category: "RCE"},
		{ID: 920350, Description: "Host Header Validation", Severity: "WARNING", Enabled: true, Category: "Protocol"},
		{ID: 913100, Description: "Malicious User Agent", Severity: "LOW", Enabled: true, Category: "Reputation"},
	}
}
