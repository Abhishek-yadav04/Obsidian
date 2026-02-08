package store

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/database"
	"github.com/corazawaf/coraza/v3/internal/app/model"
)

// Store handles persistence of WAF data
// Uses PostgreSQL via database.Manager for authentication and audit logs
type Store struct {
	mu             sync.RWMutex
	filePath       string
	state          model.SystemState
	dbManager      *database.Manager // Database manager for PostgreSQL/Redis
	rulesFile      string
	crsPath        string
	crsOn          bool
	crsVersion     string
	crsLoadedAt    time.Time
	crsSource      string
	crsFingerprint string

	customRules []model.Rule
	crsRules    []model.Rule
}

// StoreOption configures a Store instance
type StoreOption func(*Store)

// WithDatabaseManager sets the database manager for PostgreSQL operations
func WithDatabaseManager(db *database.Manager) StoreOption {
	return func(s *Store) {
		s.dbManager = db
	}
}

// WithRulesFile configures an external rules file to be loaded into the Rules UI.
func WithRulesFile(path string) StoreOption {
	return func(s *Store) {
		s.rulesFile = path
	}
}

// WithCRSPath configures the external CRS path for read-only rules in the UI.
func WithCRSPath(path string) StoreOption {
	return func(s *Store) {
		s.crsPath = path
	}
}

// WithCRSEnabled controls whether CRS rules are loaded for UI visibility.
func WithCRSEnabled(enabled bool) StoreOption {
	return func(s *Store) {
		s.crsOn = enabled
	}
}

// WithCRSVersion configures the CRS version string for status reporting.
func WithCRSVersion(version string) StoreOption {
	return func(s *Store) {
		s.crsVersion = version
	}
}

func NewStore(path string, opts ...StoreOption) *Store {
	s := &Store{
		filePath: path,
		state: model.SystemState{
			Logs:  []model.LogEntry{},
			Rules: defaultRules(), // Initialize with some defaults
			Stats: model.Stats{ActiveRulesCount: 5},
			Users: []model.User{}, // Users are now in PostgreSQL
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(s)
	}

	s.load()

	s.normalizeCustomRules()
	if err := s.mergeRulesFromFile(); err != nil {
		fmt.Printf("[Store] Warning: failed to load rules file: %v\n", err)
	}

	// Initialize default users in memory if not loaded from file and DB not available
	if len(s.state.Users) == 0 {
		s.initDefaultUsers()
	}

	return s
}

func (s *Store) mergeRulesFromFile() error {
	customRules, err := s.loadCustomRulesFromFile()
	if err != nil {
		return err
	}
	crsRules, err := s.loadCRSRules()
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.customRules = customRules
	s.crsRules = crsRules

	// Merge order: CRS -> custom file -> in-memory custom (state.Rules)
	rulesByID := make(map[int]model.Rule, len(customRules)+len(crsRules)+len(s.state.Rules))
	for _, r := range crsRules {
		rulesByID[r.ID] = r
	}
	for _, r := range customRules {
		rulesByID[r.ID] = r
	}
	for _, r := range s.state.Rules {
		rulesByID[r.ID] = r
	}

	merged := make([]model.Rule, 0, len(rulesByID))
	for _, r := range rulesByID {
		merged = append(merged, r)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })

	s.state.Rules = merged
	s.state.Stats.ActiveRulesCount = len(s.state.Rules)
	return nil
}

func (s *Store) loadCustomRulesFromFile() ([]model.Rule, error) {
	if s.rulesFile == "" {
		return nil, nil
	}
	path := resolvePath(s.rulesFile)
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return parseRulesFile(file, "custom")
}

func (s *Store) loadCRSRules() ([]model.Rule, error) {
	if !s.crsOn || s.crsPath == "" {
		s.setCRSStatus("disabled", time.Time{}, "")
		return nil, nil
	}
	root := resolvePath(s.crsPath)
	pattern := filepath.Join(root, "rules", "*.conf")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		s.setCRSStatus("filesystem", time.Time{}, "")
		return nil, nil
	}

	fingerprint := computeCRSFingerprint(files)
	loadedAt := time.Now().UTC()

	all := make([]model.Rule, 0)
	for _, filePath := range files {
		f, err := os.Open(filePath)
		if err != nil {
			continue
		}
		parsed, err := parseRulesFile(f, "crs")
		f.Close()
		if err != nil {
			continue
		}
		all = append(all, parsed...)
	}

	s.setCRSStatus("filesystem", loadedAt, fingerprint)
	return all, nil
}

func (s *Store) setCRSStatus(source string, loadedAt time.Time, fingerprint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crsSource = source
	s.crsLoadedAt = loadedAt
	s.crsFingerprint = fingerprint
}

func computeCRSFingerprint(files []string) string {
	if len(files) == 0 {
		return ""
	}
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	h := sha256.New()
	for _, filePath := range sorted {
		b, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		_, _ = h.Write([]byte(filePath))
		_, _ = h.Write(b)
	}

	sum := hex.EncodeToString(h.Sum(nil))
	if sum == "" {
		return ""
	}
	return "sha256:" + sum
}

// CRSStatus returns CRS visibility information.
func (s *Store) CRSStatus() model.CRSStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.crsRules)
	active := 0
	for i := range s.crsRules {
		if s.crsRules[i].Enabled {
			active++
		}
	}

	version := s.crsVersion
	if version == "" {
		version = "unknown"
	}

	return model.CRSStatus{
		Enabled:      s.crsOn,
		Version:      version,
		Fingerprint:  s.crsFingerprint,
		TotalRules:   total,
		ActiveRules:  active,
		LastLoadedAt: s.crsLoadedAt,
		Source:       s.crsSource,
	}
}

// EnableCRS enables CRS loading for UI visibility and reloads rules.
func (s *Store) EnableCRS() error {
	s.mu.Lock()
	s.crsOn = true
	s.mu.Unlock()
	return s.mergeRulesFromFile()
}

// DisableCRS disables CRS loading for UI visibility and reloads rules.
func (s *Store) DisableCRS() error {
	s.mu.Lock()
	s.crsOn = false
	s.mu.Unlock()
	return s.mergeRulesFromFile()
}

func parseRulesFile(r *os.File, source string) ([]model.Rule, error) {
	scanner := bufio.NewScanner(r)
	// Increase scanner buffer for large CRS rules with many tags
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	rules := make([]model.Rule, 0)
	var lineBuf strings.Builder

	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)

		// Skip comments and blank lines (only when not accumulating a multi-line rule)
		if lineBuf.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}

		// Handle line continuation (trailing backslash)
		if strings.HasSuffix(trimmed, "\\") {
			// Strip the trailing backslash and accumulate
			lineBuf.WriteString(strings.TrimSuffix(trimmed, "\\"))
			lineBuf.WriteByte(' ')
			continue
		}

		// Final line of a (possibly multi-line) directive
		lineBuf.WriteString(trimmed)
		fullLine := lineBuf.String()
		lineBuf.Reset()

		if !strings.HasPrefix(fullLine, "SecRule ") {
			continue
		}

		rule, err := parseSecRule(fullLine, source)
		if err != nil {
			continue
		}
		rules = append(rules, *rule)
	}

	// Flush any remaining buffered content
	if lineBuf.Len() > 0 {
		fullLine := lineBuf.String()
		if strings.HasPrefix(fullLine, "SecRule ") {
			if rule, err := parseSecRule(fullLine, source); err == nil {
				rules = append(rules, *rule)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func parseSecRule(line string, source string) (*model.Rule, error) {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid SecRule line")
	}
	target := parts[1]

	op, rest, ok := extractQuoted(line)
	if !ok {
		return nil, fmt.Errorf("missing operator")
	}
	actions, _, ok := extractQuoted(rest)
	if !ok {
		return nil, fmt.Errorf("missing actions")
	}

	rule := model.Rule{
		TargetField: target,
		Pattern:     op,
		Enabled:     true,
		Action:      "log",
		Category:    "General",
		Source:      source,
		ReadOnly:    true,
	}

	// Use a smarter action splitter that respects quoted values
	for _, token := range splitActions(actions) {
		lower := strings.ToLower(token)
		switch {
		case lower == "deny" || lower == "drop" || lower == "pass" || lower == "log":
			rule.Action = lower
		case lower == "block":
			// CRS uses "block" which means deny/drop depending on SecDefaultAction
			rule.Action = "deny"
		case strings.HasPrefix(lower, "id:"):
			idStr := strings.TrimPrefix(token, "id:")
			idStr = strings.TrimPrefix(idStr, "ID:")
			idStr = trimQuoted(idStr)
			if id, err := strconv.Atoi(idStr); err == nil {
				rule.ID = id
			}
		case strings.HasPrefix(lower, "status:"):
			codeStr := trimQuoted(strings.TrimPrefix(token, "status:"))
			if code, err := strconv.Atoi(codeStr); err == nil {
				rule.BlockStatus = code
			}
		case strings.HasPrefix(lower, "severity:"):
			sev := trimQuoted(strings.TrimPrefix(token, "severity:"))
			sev = strings.TrimPrefix(sev, "severity:")
			rule.Severity = strings.ToUpper(sev)
		case strings.HasPrefix(lower, "msg:"):
			rule.Description = trimQuoted(strings.TrimPrefix(token, "msg:"))
		case strings.HasPrefix(lower, "tag:"):
			tag := trimQuoted(strings.TrimPrefix(token, "tag:"))
			// Use the first meaningful tag as category; skip OWASP_CRS, paranoia, capec, PCI
			tagLower := strings.ToLower(tag)
			if !strings.HasPrefix(tagLower, "owasp_crs") &&
				!strings.HasPrefix(tagLower, "paranoia") &&
				!strings.HasPrefix(tagLower, "capec") &&
				!strings.HasPrefix(tagLower, "pci") {
				// Prefer "attack-*" or "application-*" tags as category
				if strings.HasPrefix(tagLower, "attack-") || strings.HasPrefix(tagLower, "application-") {
					rule.Category = tag
				} else if rule.Category == "General" {
					rule.Category = tag
				}
			}
		case strings.HasPrefix(lower, "ver:"):
			// Ignore version tag — extracted at file level
		}
	}

	if rule.ID == 0 {
		return nil, fmt.Errorf("missing id")
	}
	// Only validate ID range for user-created custom rules, not for CRS file imports
	if source == "custom" {
		if err := validateRuleIDRange(source, rule.ID); err != nil {
			return nil, err
		}
	}
	if rule.Description == "" {
		rule.Description = fmt.Sprintf("Rule %d", rule.ID)
	}
	if rule.Severity == "" {
		rule.Severity = "NOTICE"
	}
	if (rule.Action == "deny" || rule.Action == "block") && rule.BlockStatus == 0 {
		rule.BlockStatus = 403
	}

	return &rule, nil
}

// splitActions splits a comma-separated action string while respecting quoted values.
// e.g. "id:123,msg:'hello, world',deny" → ["id:123", "msg:'hello, world'", "deny"]
func splitActions(actions string) []string {
	out := make([]string, 0, 16)
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(actions); i++ {
		ch := actions[i]
		switch {
		case inQuote:
			cur.WriteByte(ch)
			if ch == quoteChar {
				inQuote = false
			}
		case ch == '\'' || ch == '"':
			inQuote = true
			quoteChar = ch
			cur.WriteByte(ch)
		case ch == ',':
			t := strings.TrimSpace(cur.String())
			if t != "" {
				out = append(out, t)
			}
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out
}

func (s *Store) normalizeCustomRules() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := 0; i < len(s.state.Rules); i++ {
		if s.state.Rules[i].Source == "" {
			s.state.Rules[i].Source = "custom"
		}
		// NOTE: ID range validation is NOT applied to built-in default rules.
		// It is enforced only on user-created rules in CreateRule/UpdateRule.
	}
}

func validateRuleIDRange(source string, id int) error {
	switch source {
	case "custom":
		if id < 900000 || id > 909999 {
			return fmt.Errorf("custom rule id %d outside allowed range 900000-909999", id)
		}
	case "crs":
		// OWASP CRS uses IDs across 900000-999999 (e.g. 911xxx, 920xxx, 941xxx, 949xxx)
		if id < 900000 || id > 999999 {
			return fmt.Errorf("crs rule id %d outside allowed range 900000-999999", id)
		}
	}
	return nil
}

func trimQuoted(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func extractQuoted(s string) (string, string, bool) {
	start := strings.IndexAny(s, "\"'")
	if start == -1 {
		return "", s, false
	}
	quote := s[start]
	rest := s[start+1:]
	end := strings.IndexByte(rest, quote)
	if end == -1 {
		return "", s, false
	}
	value := rest[:end]
	return value, rest[end+1:], true
}

func resolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if wd, err := os.Getwd(); err == nil {
		cwdPath := filepath.Join(wd, path)
		if _, err := os.Stat(cwdPath); err == nil {
			return cwdPath
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return path
	}
	return filepath.Join(filepath.Dir(exe), path)
}

// initDefaultUsers creates default users in memory for when DB is not available
func (s *Store) initDefaultUsers() {
	// Hash default passwords
	adminHash, _ := auth.HashPassword("ObsidianAdmin#2024")
	analystHash, _ := auth.HashPassword("ObsidianAnalyst#2024")
	viewerHash, _ := auth.HashPassword("ObsidianViewer#2024")

	s.state.Users = []model.User{
		{
			ID:           1,
			Username:     "admin",
			PasswordHash: adminHash,
			Email:        "admin@obsidian.local",
			Role:         "Admin",
			Enabled:      true,
		},
		{
			ID:           2,
			Username:     "analyst",
			PasswordHash: analystHash,
			Email:        "analyst@obsidian.local",
			Role:         "Analyst",
			Enabled:      true,
		},
		{
			ID:           3,
			Username:     "viewer",
			PasswordHash: viewerHash,
			Email:        "viewer@obsidian.local",
			Role:         "Viewer",
			Enabled:      true,
		},
	}
}

func (s *Store) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		// File doesn't exist or can't be read - start with empty state
		return
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		// JSON parsing failed - log and start with empty state
		// This prevents data corruption from propagating
		fmt.Printf("[Store] Warning: failed to parse state file %s: %v\n", s.filePath, err)
	}

	// Re-inject any missing default rules.
	// data.json may have been saved by a previous version that dropped rules;
	// defaults must always be present (user edits are preserved via ID match).
	existing := make(map[int]struct{}, len(s.state.Rules))
	for _, r := range s.state.Rules {
		existing[r.ID] = struct{}{}
	}
	for _, d := range defaultRules() {
		if _, ok := existing[d.ID]; !ok {
			s.state.Rules = append(s.state.Rules, d)
		}
	}
	sort.Slice(s.state.Rules, func(i, j int) bool { return s.state.Rules[i].ID < s.state.Rules[j].ID })
}

func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveUnlocked()
}

// saveUnlocked persists state to disk. Caller must hold at least an RLock on s.mu.
func (s *Store) saveUnlocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
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

	// Update per-rule match stats
	if entry.RuleID != 0 {
		now := time.Now().Format(time.RFC3339)
		for i := range s.state.Rules {
			if s.state.Rules[i].ID == entry.RuleID {
				s.state.Rules[i].MatchCount++
				s.state.Rules[i].LastMatch = now
				break
			}
		}
		for i := range s.customRules {
			if s.customRules[i].ID == entry.RuleID {
				s.customRules[i].MatchCount++
				s.customRules[i].LastMatch = now
				break
			}
		}
		for i := range s.crsRules {
			if s.crsRules[i].ID == entry.RuleID {
				s.crsRules[i].MatchCount++
				s.crsRules[i].LastMatch = now
				break
			}
		}
	}

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
	// Compute active rules count without mutating shared state (avoids write-under-RLock)
	stats := s.state.Stats
	stats.ActiveRulesCount = len(s.state.Rules)
	// Compute average response time from accumulators
	if stats.ResponseCount > 0 {
		stats.AvgResponseTimeMs = float64(stats.TotalResponseTimeNs) / float64(stats.ResponseCount) / 1e6
	}
	return stats
}

// RecordResponseTime records a request's response time for average calculation.
func (s *Store) RecordResponseTime(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Stats.TotalResponseTimeNs += d.Nanoseconds()
	s.state.Stats.ResponseCount++
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
	rules := make([]model.Rule, len(s.state.Rules))
	copy(rules, s.state.Rules)
	return rules
}

func (s *Store) GetRulesBySource(source string) []model.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Filter from the canonical s.state.Rules slice which contains
	// all rules (built-in defaults + file-loaded custom + CRS).
	var filtered []model.Rule
	for _, r := range s.state.Rules {
		switch source {
		case "custom":
			if r.Source != "crs" {
				filtered = append(filtered, r)
			}
		case "crs":
			if r.Source == "crs" {
				filtered = append(filtered, r)
			}
		default:
			filtered = append(filtered, r)
		}
	}
	if filtered == nil {
		filtered = []model.Rule{}
	}
	return filtered
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
	rules := []model.Rule{
		// 900xxx - Test Rules
		{ID: 900001, Description: "Test Attack Detection", Severity: "CRITICAL", Enabled: true, Category: "Test", Pattern: "@rx attack=test", TargetField: "QUERY_STRING", Action: "deny", BlockStatus: 403},

		// 910xxx - Protocol Enforcement
		{ID: 910100, Description: "Invalid HTTP Method", Severity: "WARNING", Enabled: true, Category: "Protocol", Pattern: "!@rx ^(GET|HEAD|POST|PUT|DELETE|OPTIONS|PATCH)$", TargetField: "REQUEST_METHOD", Action: "deny", BlockStatus: 405},
		{ID: 910110, Description: "Null Byte Injection", Severity: "CRITICAL", Enabled: true, Category: "Protocol", Pattern: "@rx %00", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 910120, Description: "HTTP Response Splitting", Severity: "CRITICAL", Enabled: true, Category: "Protocol", Pattern: "@rx %0[aAdD]", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 913xxx - Scanner/Bot Detection
		{ID: 913100, Description: "Security Scanner Detection (sqlmap, nikto, burp)", Severity: "WARNING", Enabled: true, Category: "Reputation", Pattern: "@rx (?i)(nikto|sqlmap|nmap|masscan|burp|owasp|acunetix)", TargetField: "REQUEST_HEADERS:User-Agent", Action: "deny", BlockStatus: 403},
		{ID: 913110, Description: "Scripted User-Agent Detection", Severity: "NOTICE", Enabled: true, Category: "Reputation", Pattern: "@rx (?i)(python-requests|python-urllib|perl|ruby|libwww)", TargetField: "REQUEST_HEADERS:User-Agent", Action: "log", BlockStatus: 0},
		{ID: 913120, Description: "Empty User-Agent Header", Severity: "NOTICE", Enabled: true, Category: "Reputation", Pattern: "@rx ^$", TargetField: "REQUEST_HEADERS:User-Agent", Action: "log", BlockStatus: 0},

		// 920xxx - Protocol Anomalies
		{ID: 920100, Description: "Sensitive File Access (.env, .git, .htaccess)", Severity: "CRITICAL", Enabled: true, Category: "Anomaly", Pattern: "@rx (?i)\\.(htaccess|htpasswd|git|svn|env|DS_Store|config|bak|sql|db|log)$", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 920110, Description: "Server-Side Script Request", Severity: "NOTICE", Enabled: true, Category: "Anomaly", Pattern: "@rx (?i)\\.(php|asp|aspx|jsp|cgi)$", TargetField: "REQUEST_URI", Action: "log", BlockStatus: 0},
		{ID: 920120, Description: "Backup File Access (.bak, .swp)", Severity: "WARNING", Enabled: true, Category: "Anomaly", Pattern: "@rx ~$|\\.swp$|\\.bak$|\\.orig$|\\.old$", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 920130, Description: "Version Control Directory Access", Severity: "CRITICAL", Enabled: true, Category: "Anomaly", Pattern: "@rx (?i)/\\.(git|svn|hg|bzr)/", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 930xxx - Local File Inclusion (LFI)
		{ID: 930100, Description: "Path Traversal Attack (../)", Severity: "CRITICAL", Enabled: true, Category: "LFI", Pattern: "@rx (\\.\\./|\\.\\.\\ %2f|%2e%2e%2f)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 930110, Description: "Linux Sensitive File Access (/etc/passwd)", Severity: "CRITICAL", Enabled: true, Category: "LFI", Pattern: "@rx (?i)(/etc/passwd|/etc/shadow|/etc/hosts)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 930120, Description: "Windows Sensitive File Access (boot.ini)", Severity: "CRITICAL", Enabled: true, Category: "LFI", Pattern: "@rx (?i)(boot\\.ini|windows/system32|winnt)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 930130, Description: "Proc Filesystem Access", Severity: "CRITICAL", Enabled: true, Category: "LFI", Pattern: "@rx (?i)/proc/(self|version|cmdline)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 930140, Description: "PHP Wrapper Attack (php://, file://)", Severity: "CRITICAL", Enabled: true, Category: "LFI", Pattern: "@rx (?i)(file://|php://|data://|expect://|zip://|phar://)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 931xxx - Remote File Inclusion (RFI)
		{ID: 931100, Description: "Remote File Inclusion (http://)", Severity: "CRITICAL", Enabled: true, Category: "RFI", Pattern: "@rx (?i)(https?|ftp)://", TargetField: "QUERY_STRING", Action: "deny", BlockStatus: 403},
		{ID: 931110, Description: "URL Encoded RFI Attempt", Severity: "CRITICAL", Enabled: true, Category: "RFI", Pattern: "@rx (?i)=(https?|ftp)%3a%2f%2f", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 932xxx - Command Injection (RCE)
		{ID: 932100, Description: "OS Command Injection (cat, ls, wget)", Severity: "CRITICAL", Enabled: true, Category: "RCE", Pattern: "@rx [;&|](cat|ls|dir|wget|curl|nc|bash|sh|cmd|powershell)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 932110, Description: "Command Substitution Attack ($(), ``)", Severity: "CRITICAL", Enabled: true, Category: "RCE", Pattern: "@rx (%24%28|%60)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 932120, Description: "System Command Execution (whoami, id)", Severity: "CRITICAL", Enabled: true, Category: "RCE", Pattern: "@rx (?i)(;|\\|)(whoami|id|uname|hostname|pwd|ifconfig)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 932130, Description: "Code Execution Function", Severity: "CRITICAL", Enabled: true, Category: "RCE", Pattern: "@rx (?i)(shell_exec|system|exec|passthru|popen|proc_open)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 932140, Description: "Shell Binary Access (/bin/bash)", Severity: "CRITICAL", Enabled: true, Category: "RCE", Pattern: "@rx (?i)(/bin/bash|/bin/sh|cmd\\.exe|powershell\\.exe)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 933xxx - PHP Injection
		{ID: 933100, Description: "PHP Code Injection (<?php)", Severity: "CRITICAL", Enabled: true, Category: "PHP", Pattern: "@rx (?i)(<\\?php|<\\?=|<%php)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 933110, Description: "PHP Dangerous Function (eval, assert)", Severity: "CRITICAL", Enabled: true, Category: "PHP", Pattern: "@rx (?i)(eval\\s*\\(|assert\\s*\\()", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 933120, Description: "PHP Obfuscation Function (base64_decode)", Severity: "WARNING", Enabled: true, Category: "PHP", Pattern: "@rx (?i)(base64_decode|gzinflate|gzuncompress|str_rot13)\\s*\\(", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 933130, Description: "PHP File Inclusion Function", Severity: "WARNING", Enabled: true, Category: "PHP", Pattern: "@rx (?i)(include|require|include_once|require_once)\\s*\\(", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 934xxx - Node.js Injection
		{ID: 934100, Description: "Node.js Code Injection (require, exec)", Severity: "CRITICAL", Enabled: true, Category: "NodeJS", Pattern: "@rx (?i)(require\\s*\\(|child_process|\\.exec\\s*\\(|\\.spawn\\s*\\()", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 934110, Description: "Node.js Process Manipulation", Severity: "CRITICAL", Enabled: true, Category: "NodeJS", Pattern: "@rx (?i)(process\\.env|process\\.exit|process\\.kill)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 941xxx - XSS Protection
		{ID: 941100, Description: "XSS Attack: Script Tag (<script>)", Severity: "CRITICAL", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(<script|%3Cscript|%3c%73%63%72%69%70%74)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 941110, Description: "XSS Attack: JavaScript Protocol", Severity: "CRITICAL", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(javascript:|vbscript:|data:text/html)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 941120, Description: "XSS Attack: Event Handler (onload=)", Severity: "CRITICAL", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(on(error|load|click|mouse|focus|blur|change|submit|reset|select)\\s*=)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 941130, Description: "XSS Attack: CSS Expression", Severity: "CRITICAL", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(expression\\s*\\(|@import|behavior:)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 941140, Description: "XSS Attack: Dangerous JS Function", Severity: "CRITICAL", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(alert|confirm|prompt|document\\.cookie|document\\.write|\\.innerHTML)\\s*[\\(\\=]", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 941150, Description: "XSS Attack: HTML Injection", Severity: "WARNING", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(<iframe|<object|<embed|<applet|<form|<input|<button)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 941160, Description: "HTML Tag with External Reference", Severity: "NOTICE", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(src\\s*=|href\\s*=)\\s*[\"']?https?://", TargetField: "REQUEST_URI", Action: "log", BlockStatus: 0},
		{ID: 941170, Description: "XSS Attack: JS Encoding (fromCharCode)", Severity: "WARNING", Enabled: true, Category: "XSS", Pattern: "@rx (?i)(fromCharCode|String\\.fromCharCode)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 942xxx - SQL Injection
		{ID: 942100, Description: "SQL Injection: Boolean Logic (1 or 1=1)", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)(\\d[\\s\\+]+or[\\s\\+]+\\d|1[\\s\\+]*=[\\s\\+]*1|\\d[\\s\\+]+and[\\s\\+]+\\d)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942110, Description: "SQL Injection: String Logic (' or ')", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)('[\\s\\+]*(or|and)[\\s\\+]*')", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942120, Description: "SQL Injection: UNION SELECT", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)(union[\\s\\+]*(all[\\s\\+]*)?select)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942130, Description: "SQL Injection: Comment Sequence (--)", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (--|#|%23|%2d%2d)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942140, Description: "SQL Injection: SQL Statement", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)(select[\\s\\+]+.*(from|into)|insert[\\s\\+]+into|update[\\s\\+]+.+set|delete[\\s\\+]+from|drop[\\s\\+]+(table|database))", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942150, Description: "SQL Injection: Database Function (xp_, sp_)", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)(exec[\\s\\+]+(xp_|sp_)|execute[\\s\\+]+immediate|dbms_|utl_)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942160, Description: "SQL Injection: Time-Based (SLEEP, BENCHMARK)", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)(benchmark\\s*\\(|sleep\\s*\\(|waitfor[\\s\\+]+delay|pg_sleep)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942170, Description: "SQL Injection: File/Schema Access", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)(load_file|into[\\s\\+]+(out|dump)file|information_schema)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 942180, Description: "SQL Injection: String Function (CONCAT, CHAR)", Severity: "WARNING", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)(concat\\s*\\(|char\\s*\\(|chr\\s*\\(|ascii\\s*\\(|ord\\s*\\(|hex\\s*\\(|unhex\\s*\\()", TargetField: "REQUEST_URI", Action: "log", BlockStatus: 0},
		{ID: 942190, Description: "SQL Injection: Blind SQLi (ORDER BY)", Severity: "CRITICAL", Enabled: true, Category: "SQLi", Pattern: "@rx (?i)((group[\\s\\+]+by|order[\\s\\+]+by)[\\s\\+]+\\d+|having[\\s\\+]+\\d)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 943xxx - Session Fixation
		{ID: 943100, Description: "Session Fixation Attempt", Severity: "CRITICAL", Enabled: true, Category: "Session", Pattern: "@rx (?i)(PHPSESSID|JSESSIONID|ASPSESSIONID|session_id)=", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 943110, Description: "Cookie Injection Attempt", Severity: "CRITICAL", Enabled: true, Category: "Session", Pattern: "@rx (?i)(set-cookie:|cookie:).*session", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 944xxx - Java/Deserialization
		{ID: 944100, Description: "Java Class Injection", Severity: "CRITICAL", Enabled: true, Category: "Java", Pattern: "@rx (?i)(java\\.(lang|io|util|net|security)\\.|Runtime\\.getRuntime)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 944110, Description: "Java Serialized Object", Severity: "CRITICAL", Enabled: true, Category: "Java", Pattern: "@rx (\\xac\\xed\\x00\\x05|rO0AB)", TargetField: "REQUEST_BODY", Action: "deny", BlockStatus: 403},

		// 950xxx - Data Leakage
		{ID: 950100, Description: "Potential Credential in URL", Severity: "WARNING", Enabled: true, Category: "Leakage", Pattern: "@rx (?i)(password|passwd|pwd|secret|token|api[_-]?key)\\s*=", TargetField: "QUERY_STRING", Action: "log", BlockStatus: 0},

		// 951xxx - SSRF
		{ID: 951100, Description: "SSRF: Internal IP Address", Severity: "CRITICAL", Enabled: true, Category: "SSRF", Pattern: "@rx (?i)(127\\.0\\.0\\.|10\\.|192\\.168\\.|172\\.(1[6-9]|2[0-9]|3[01])\\.|\\.local|localhost)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 951110, Description: "SSRF: Cloud Metadata Access", Severity: "CRITICAL", Enabled: true, Category: "SSRF", Pattern: "@rx (?i)(169\\.254\\.169\\.254|metadata\\.google|metadata\\.azure)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 952xxx - XXE
		{ID: 952100, Description: "XXE: DOCTYPE Declaration", Severity: "CRITICAL", Enabled: true, Category: "XXE", Pattern: "@rx (?i)(<!DOCTYPE|<!ENTITY)", TargetField: "REQUEST_BODY", Action: "deny", BlockStatus: 403},
		{ID: 952110, Description: "XXE: External Entity", Severity: "CRITICAL", Enabled: true, Category: "XXE", Pattern: "@rx (?i)(SYSTEM\\s+[\"']file://|SYSTEM\\s+[\"']http)", TargetField: "REQUEST_BODY", Action: "deny", BlockStatus: 403},

		// 953xxx - LDAP Injection
		{ID: 953100, Description: "LDAP Injection Attack", Severity: "CRITICAL", Enabled: true, Category: "LDAP", Pattern: "@rx (?i)(\\(\\||\\|\\)|\\*\\)|\\(\\*|\\(cn=|\\(uid=|\\(objectClass=)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 954xxx - Template Injection (SSTI)
		{ID: 954100, Description: "Template Syntax Detected", Severity: "NOTICE", Enabled: true, Category: "SSTI", Pattern: "@rx (\\{\\{|\\{%|\\$\\{|<%=|#\\{)", TargetField: "REQUEST_URI", Action: "log", BlockStatus: 0},
		{ID: 954110, Description: "Python SSTI Attack", Severity: "CRITICAL", Enabled: true, Category: "SSTI", Pattern: "@rx (?i)(__class__|__mro__|__subclasses__|__builtins__|__import__|__globals__)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 955xxx - GraphQL Attacks (Enterprise)
		{ID: 955100, Description: "GraphQL Introspection Attack", Severity: "WARNING", Enabled: true, Category: "GraphQL", Pattern: "@rx (?i)(__schema|__type|introspectionQuery)", TargetField: "REQUEST_BODY", Action: "log", BlockStatus: 0},
		{ID: 955110, Description: "GraphQL Deep Query Attack", Severity: "CRITICAL", Enabled: true, Category: "GraphQL", Pattern: "@rx (\\{\\s*[a-zA-Z]+\\s*\\{){5,}", TargetField: "REQUEST_BODY", Action: "deny", BlockStatus: 403},

		// 956xxx - API Security (Enterprise)
		{ID: 956100, Description: "JWT Token in URL", Severity: "WARNING", Enabled: true, Category: "API", Pattern: "@rx (?i)(token|jwt|bearer)=[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+", TargetField: "QUERY_STRING", Action: "log", BlockStatus: 0},
		{ID: 956110, Description: "API Version Enumeration", Severity: "NOTICE", Enabled: true, Category: "API", Pattern: "@rx /api/v(\\d+)/", TargetField: "REQUEST_URI", Action: "log", BlockStatus: 0},

		// 957xxx - Log4j/Log4Shell (Enterprise)
		{ID: 957100, Description: "Log4j JNDI Injection", Severity: "CRITICAL", Enabled: true, Category: "Log4j", Pattern: "@rx (?i)(\\$\\{jndi:|\\$\\{env:|\\$\\{lower:|\\$\\{upper:)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},
		{ID: 957110, Description: "Log4j Obfuscated Attack", Severity: "CRITICAL", Enabled: true, Category: "Log4j", Pattern: "@rx (?i)(\\$\\{j\\$\\{|\\$\\{\\$\\{lower:|%24%7b)", TargetField: "REQUEST_URI", Action: "deny", BlockStatus: 403},

		// 958xxx - Rate Limiting Bypass (Enterprise)
		{ID: 958100, Description: "X-Forwarded-For Spoofing", Severity: "WARNING", Enabled: true, Category: "RateLimit", Pattern: "@rx ^(127\\.|10\\.|192\\.168\\.|172\\.)", TargetField: "REQUEST_HEADERS:X-Forwarded-For", Action: "log", BlockStatus: 0, Threshold: 10, TimeWindow: 60},
	}
	for i := range rules {
		if rules[i].Source == "" {
			rules[i].Source = "custom"
		}
		rules[i].ReadOnly = true
	}
	return rules
}

// AuthenticateUser validates credentials against PostgreSQL database or in-memory fallback
// SECURITY: Uses bcrypt for password verification
func (s *Store) AuthenticateUser(username, password string) (*model.User, error) {
	// Try PostgreSQL database first if available
	if s.dbManager != nil && s.dbManager.HasPostgres() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Query user from PostgreSQL
		dbUser, err := s.dbManager.GetUserByUsername(ctx, username)
		if err == nil {
			// Verify password using bcrypt
			if err := auth.VerifyPassword(dbUser.PasswordHash, password); err != nil {
				return nil, auth.ErrInvalidCredentials
			}

			if !dbUser.Enabled {
				return nil, fmt.Errorf("user account is disabled")
			}

			// Convert database.User to model.User
			user := &model.User{
				ID:           dbUser.ID,
				Username:     dbUser.Username,
				Email:        dbUser.Email,
				PasswordHash: dbUser.PasswordHash,
				Role:         dbUser.Role,
				Enabled:      dbUser.Enabled,
				CreatedAt:    dbUser.CreatedAt,
			}

			return user, nil
		}
	}

	// In-memory fallback when database is not connected
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, u := range s.state.Users {
		if u.Username == username {
			// Verify password using bcrypt
			if err := auth.VerifyPassword(u.PasswordHash, password); err != nil {
				return nil, auth.ErrInvalidCredentials
			}

			if !u.Enabled {
				return nil, fmt.Errorf("user account is disabled")
			}

			// Return a copy
			user := &model.User{
				ID:           u.ID,
				Username:     u.Username,
				Email:        u.Email,
				PasswordHash: u.PasswordHash,
				Role:         u.Role,
				Enabled:      u.Enabled,
				CreatedAt:    u.CreatedAt,
			}

			return user, nil
		}
	}

	return nil, auth.ErrInvalidCredentials
}

// GetUsers returns all users from PostgreSQL or in-memory fallback
func (s *Store) GetUsers() []model.User {
	// Try database first if available
	if s.dbManager != nil && s.dbManager.HasPostgres() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		dbUsers, err := s.dbManager.GetAllUsers(ctx)
		if err == nil && len(dbUsers) > 0 {
			users := make([]model.User, 0, len(dbUsers))
			for _, u := range dbUsers {
				users = append(users, model.User{
					ID:        u.ID,
					Username:  u.Username,
					Email:     u.Email,
					Role:      u.Role,
					Enabled:   u.Enabled,
					CreatedAt: u.CreatedAt,
				})
			}
			return users
		}
	}

	// In-memory fallback
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return copy without password hashes
	users := make([]model.User, 0, len(s.state.Users))
	for _, u := range s.state.Users {
		users = append(users, model.User{
			ID:        u.ID,
			Username:  u.Username,
			Email:     u.Email,
			Role:      u.Role,
			Enabled:   u.Enabled,
			CreatedAt: u.CreatedAt,
		})
	}
	return users
}

// UpdateUser updates a user in PostgreSQL or in-memory fallback
func (s *Store) UpdateUser(username, role string, enabled bool, newPassword string) error {
	// Try database first if available
	if s.dbManager != nil && s.dbManager.HasPostgres() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Update role and enabled status
		if err := s.dbManager.UpdateUser(ctx, username, role, enabled); err != nil {
			return fmt.Errorf("failed to update user: %w", err)
		}

		// Update password if provided
		if newPassword != "" {
			passwordHash, err := auth.HashPassword(newPassword)
			if err != nil {
				return fmt.Errorf("failed to hash password: %w", err)
			}
			if err := s.dbManager.UpdateUserPassword(ctx, username, passwordHash); err != nil {
				return fmt.Errorf("failed to update password: %w", err)
			}
		}

		return nil
	}

	// In-memory fallback when database is not connected
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, u := range s.state.Users {
		if u.Username == username {
			s.state.Users[i].Role = role
			s.state.Users[i].Enabled = enabled
			if newPassword != "" {
				passwordHash, err := auth.HashPassword(newPassword)
				if err != nil {
					return fmt.Errorf("failed to hash password: %w", err)
				}
				s.state.Users[i].PasswordHash = passwordHash
			}
			// Persist changes synchronously to prevent data race
			if err := s.saveUnlocked(); err != nil {
				fmt.Printf("[Store] Warning: failed to persist state: %v\n", err)
			}
			return nil
		}
	}

	return fmt.Errorf("user not found: %s", username)
}

// CreateSession stores a new session in the database
func (s *Store) CreateSession(session model.Session) error {
	if s.dbManager == nil || s.dbManager.PostgresPool() == nil {
		// In-memory fallback - sessions not persisted
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Hash the token for secure storage
	tokenHash := session.TokenHash
	if tokenHash == "" && session.Token != "" {
		tokenHash = auth.HashToken(session.Token)
	}

	_, err := s.dbManager.PostgresPool().Exec(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, expires_at, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, session.ID, session.UserID, tokenHash, session.ExpiresAt, session.IPAddress, session.UserAgent)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	return nil
}

// AddAuditLog records an audit trail entry in the database
func (s *Store) AddAuditLog(log model.AuditLog) error {
	if s.dbManager == nil || s.dbManager.PostgresPool() == nil {
		// In-memory fallback - audit logs not persisted
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Insert into audit_logs table using the model.AuditLog structure
	_, err := s.dbManager.PostgresPool().Exec(ctx, `
		INSERT INTO audit_logs (action, resource, details, client_ip, user_id, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, log.Action, log.Resource, log.Details, log.IPAddress, log.UserID, log.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	return nil
}

// GetAuditLogs retrieves audit logs from the database
func (s *Store) GetAuditLogs(limit, offset int) ([]map[string]interface{}, error) {
	if s.dbManager == nil {
		return nil, fmt.Errorf("database not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.dbManager.GetAuditLogs(ctx, limit, offset)
}

// GetRuleAuditLogs retrieves rule audit logs from the database
func (s *Store) GetRuleAuditLogs(limit, offset int) ([]model.RuleAuditLog, error) {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return nil, fmt.Errorf("database not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.dbManager.GetRuleAuditLogs(ctx, limit, offset)
}

func (s *Store) recordRuleAudit(action string, oldRule *model.Rule, newRule *model.Rule, actor string) {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return
	}
	if actor == "" {
		actor = "unknown"
	}

	ruleID := 0
	if newRule != nil {
		ruleID = newRule.ID
	} else if oldRule != nil {
		ruleID = oldRule.ID
	}

	oldValue := ""
	if oldRule != nil {
		if b, err := json.Marshal(oldRule); err == nil {
			oldValue = string(b)
		}
	}

	newValue := ""
	if newRule != nil {
		if b, err := json.Marshal(newRule); err == nil {
			newValue = string(b)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log := model.RuleAuditLog{
		RuleID:    ruleID,
		Action:    action,
		OldValue:  oldValue,
		NewValue:  newValue,
		Actor:     actor,
		Timestamp: time.Now().UTC(),
	}
	if err := s.dbManager.InsertRuleAuditLog(ctx, log); err != nil {
		fmt.Printf("[Store] Warning: failed to insert rule audit log for rule %d: %v\n", ruleID, err)
	}
}

// CreateRule adds a new WAF rule and persists to PostgreSQL
func (s *Store) CreateRule(rule model.Rule, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rule.Source == "" {
		rule.Source = "custom"
	}
	rule.ReadOnly = false
	if err := validateRuleIDRange(rule.Source, rule.ID); err != nil {
		return err
	}

	// Check for duplicate ID
	for _, r := range s.state.Rules {
		if r.ID == rule.ID {
			return fmt.Errorf("rule with ID %d already exists", rule.ID)
		}
	}

	s.state.Rules = append(s.state.Rules, rule)
	s.state.Stats.ActiveRulesCount = len(s.state.Rules)
	s.customRules = append(s.customRules, rule)

	// Persist to PostgreSQL if available
	if s.dbManager != nil && s.dbManager.HasPostgres() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.dbManager.SaveWAFRule(ctx, rule.ID, rule.Description, rule.Category, rule.Pattern, 1, rule.Severity, rule.Enabled); err != nil {
			fmt.Printf("[Store] Warning: failed to persist rule %d to DB: %v\n", rule.ID, err)
		}
	}

	if rule.Source == "custom" {
		s.recordRuleAudit("create", nil, &rule, actor)
	}

	// Persist to file synchronously (data already copied under lock)
	return s.persistState()
}

// UpdateRule modifies an existing WAF rule and persists to PostgreSQL
func (s *Store) UpdateRule(rule model.Rule, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.state.Rules {
		if r.ID == rule.ID {
			if r.ReadOnly || r.Source == "crs" {
				return fmt.Errorf("rule %d is read-only", rule.ID)
			}
			if rule.Source == "" {
				rule.Source = r.Source
			}
			rule.ReadOnly = false
			if err := validateRuleIDRange(rule.Source, rule.ID); err != nil {
				return err
			}

			oldRule := r
			newRule := rule
			s.state.Rules[i] = rule
			for j := range s.customRules {
				if s.customRules[j].ID == rule.ID {
					s.customRules[j] = rule
					break
				}
			}

			// Persist to PostgreSQL if available
			if s.dbManager != nil && s.dbManager.HasPostgres() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.dbManager.SaveWAFRule(ctx, rule.ID, rule.Description, rule.Category, rule.Pattern, 1, rule.Severity, rule.Enabled); err != nil {
					fmt.Printf("[Store] Warning: failed to update rule %d in DB: %v\n", rule.ID, err)
				}
			}

			if rule.Source == "custom" {
				s.recordRuleAudit("update", &oldRule, &newRule, actor)
			}

			return s.persistState()
		}
	}

	return fmt.Errorf("rule with ID %d not found", rule.ID)
}

// DeleteRule removes a WAF rule and deletes from PostgreSQL
func (s *Store) DeleteRule(id int, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.state.Rules {
		if r.ID == id {
			if r.ReadOnly || r.Source == "crs" {
				return fmt.Errorf("rule %d is read-only", id)
			}
			s.state.Rules = append(s.state.Rules[:i], s.state.Rules[i+1:]...)
			s.state.Stats.ActiveRulesCount = len(s.state.Rules)
			for j := range s.customRules {
				if s.customRules[j].ID == id {
					s.customRules = append(s.customRules[:j], s.customRules[j+1:]...)
					break
				}
			}

			// Delete from PostgreSQL if available
			if s.dbManager != nil && s.dbManager.HasPostgres() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.dbManager.DeleteWAFRule(ctx, id); err != nil {
					fmt.Printf("[Store] Warning: failed to delete rule %d from DB: %v\n", id, err)
				}
			}

			if r.Source == "custom" {
				oldRule := r
				s.recordRuleAudit("delete", &oldRule, nil, actor)
			}

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

// =============================================================================
// Threat Intelligence Methods - Persist to PostgreSQL
// =============================================================================

// AddThreat records a threat in the database
func (s *Store) AddThreat(ipAddress, threatLevel, source, reason string, blocked bool) error {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return nil // Silently skip if no database
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.dbManager.SaveThreatIntel(ctx, ipAddress, threatLevel, source, reason, blocked)
}

// GetThreats retrieves all threat intel entries from the database
func (s *Store) GetThreats() ([]map[string]interface{}, error) {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return []map[string]interface{}{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.dbManager.GetThreatIntel(ctx)
}

// GetBlockedIPs retrieves all blocked IP addresses from the database
func (s *Store) GetBlockedIPs() ([]string, error) {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return []string{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.dbManager.GetBlockedIPs(ctx)
}

// =============================================================================
// Attack Log Methods - Persist to PostgreSQL
// =============================================================================

// AddAttackLog records an attack in the database
func (s *Store) AddAttackLog(clientIP, method, uri string, ruleID int, ruleMsg, severity, action string, responseStatus int) error {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return nil // Silently skip if no database
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.dbManager.InsertAttackLog(ctx, clientIP, method, uri, ruleID, ruleMsg, severity, action, responseStatus)
}

// GetAttackLogs retrieves attack logs from the database
func (s *Store) GetAttackLogs(limit, offset int) ([]map[string]interface{}, error) {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return []map[string]interface{}{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.dbManager.GetAttackLogs(ctx, limit, offset)
}

// =============================================================================
// Statistics Methods - Persist to PostgreSQL
// =============================================================================

// IncrementDBStat increments a stat counter in the database
func (s *Store) IncrementDBStat(statName string, delta int64) {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return // Silently skip if no database
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.dbManager.IncrementStat(ctx, statName, delta); err != nil {
		fmt.Printf("[Store] Warning: failed to increment stat %s: %v\n", statName, err)
	}
}

// GetDBStats retrieves all stats from the database
func (s *Store) GetDBStats() (map[string]int64, error) {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return map[string]int64{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.dbManager.GetAllStats(ctx)
}

// SyncRulesToDB syncs all in-memory rules to the database
func (s *Store) SyncRulesToDB() error {
	if s.dbManager == nil || !s.dbManager.HasPostgres() {
		return nil
	}

	s.mu.RLock()
	rules := make([]model.Rule, len(s.state.Rules))
	copy(rules, s.state.Rules)
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, rule := range rules {
		if err := s.dbManager.SaveWAFRule(ctx, rule.ID, rule.Description, rule.Category, rule.Pattern, 1, rule.Severity, rule.Enabled); err != nil {
			fmt.Printf("[Store] Warning: failed to sync rule %d to DB: %v\n", rule.ID, err)
		}
	}

	fmt.Printf("[Store] Synced %d rules to PostgreSQL\n", len(rules))
	return nil
}
