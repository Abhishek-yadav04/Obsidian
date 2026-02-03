package threat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	contentTypeHeader = "Content-Type"
	contentTypeJSON   = "application/json"
)

// ThreatIntel manages threat intelligence feeds and IP reputation
type ThreatIntel struct {
	mu           sync.RWMutex
	blockedIPs   map[string]*ThreatEntry
	cidrBlocks   []*net.IPNet
	feeds        []ThreatFeed
	lastUpdate   time.Time
	updateTicker *time.Ticker
	persistPath  string
	httpClient   *http.Client
}

// ThreatEntry represents a known malicious IP
type ThreatEntry struct {
	IP        string    `json:"ip"`
	RiskLevel string    `json:"risk_level"` // HIGH, MEDIUM, LOW
	Category  string    `json:"category"`   // Scanner, Botnet, Proxy, Spam, etc.
	Source    string    `json:"source"`     // Feed name
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	HitCount  int       `json:"hit_count"`
}

// ThreatFeed represents an external threat intelligence feed
type ThreatFeed struct {
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Enabled    bool      `json:"enabled"`
	LastUpdate time.Time `json:"last_update"`
	EntryCount int       `json:"entry_count"`
	Type       string    `json:"type"` // "ip_list", "cidr_list", "json"
}

// Option is a functional option for ThreatIntel configuration
type Option func(*ThreatIntel)

// WithPersistPath sets the file path for persistent storage
func WithPersistPath(path string) Option {
	return func(ti *ThreatIntel) {
		ti.persistPath = path
	}
}

// WithHTTPClient sets a custom HTTP client for feed fetching
func WithHTTPClient(client *http.Client) Option {
	return func(ti *ThreatIntel) {
		ti.httpClient = client
	}
}

// NewThreatIntel creates a new threat intelligence manager
func NewThreatIntel(opts ...Option) *ThreatIntel {
	ti := &ThreatIntel{
		blockedIPs: make(map[string]*ThreatEntry),
		cidrBlocks: make([]*net.IPNet, 0),
		feeds: []ThreatFeed{
			{Name: "Spamhaus DROP", URL: "https://www.spamhaus.org/drop/drop.txt", Enabled: true, Type: "cidr_list"},
			{Name: "Spamhaus EDROP", URL: "https://www.spamhaus.org/drop/edrop.txt", Enabled: true, Type: "cidr_list"},
			{Name: "Emerging Threats", URL: "https://rules.emergingthreats.net/blockrules/compromised-ips.txt", Enabled: true, Type: "ip_list"},
			{Name: "Firehol Level 1", URL: "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset", Enabled: false, Type: "cidr_list"},
			{Name: "Custom Blocklist", URL: "", Enabled: true, Type: "ip_list"},
		},
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Apply functional options
	for _, opt := range opts {
		opt(ti)
	}

	// Load persisted data if available
	if ti.persistPath != "" {
		ti.loadFromDisk()
	}

	// PRODUCTION: No sample data - threat intel is loaded from actual feeds or database
	// Sample data has been removed for production release

	// Start background update for threat feeds
	ti.startBackgroundUpdates()

	return ti
}

// loadFromDisk loads threat data from persistent storage
func (ti *ThreatIntel) loadFromDisk() error {
	data, err := os.ReadFile(ti.persistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No persisted data yet
		}
		return fmt.Errorf("failed to read threat data: %w", err)
	}

	var state struct {
		BlockedIPs map[string]*ThreatEntry `json:"blocked_ips"`
		LastUpdate time.Time               `json:"last_update"`
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to parse threat data: %w", err)
	}

	ti.mu.Lock()
	ti.blockedIPs = state.BlockedIPs
	ti.lastUpdate = state.LastUpdate
	ti.mu.Unlock()

	return nil
}

// saveToDisk persists threat data
func (ti *ThreatIntel) saveToDisk() error {
	if ti.persistPath == "" {
		return nil
	}

	ti.mu.RLock()
	state := struct {
		BlockedIPs map[string]*ThreatEntry `json:"blocked_ips"`
		LastUpdate time.Time               `json:"last_update"`
	}{
		BlockedIPs: ti.blockedIPs,
		LastUpdate: ti.lastUpdate,
	}
	ti.mu.RUnlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal threat data: %w", err)
	}

	tmpPath := ti.persistPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write threat data: %w", err)
	}

	return os.Rename(tmpPath, ti.persistPath)
}

// startBackgroundUpdates periodically refreshes threat feeds
func (ti *ThreatIntel) startBackgroundUpdates() {
	ti.updateTicker = time.NewTicker(1 * time.Hour)
	go func() {
		// Initial refresh on startup (non-blocking)
		go ti.RefreshFeeds()

		for range ti.updateTicker.C {
			ti.RefreshFeeds()
		}
	}()
}

// Stop gracefully shuts down the threat intelligence manager
func (ti *ThreatIntel) Stop() {
	if ti.updateTicker != nil {
		ti.updateTicker.Stop()
	}
	_ = ti.saveToDisk()
}

// RefreshFeeds updates threat intelligence from all enabled feeds
func (ti *ThreatIntel) RefreshFeeds() {
	for i := range ti.feeds {
		if !ti.feeds[i].Enabled || ti.feeds[i].URL == "" {
			continue
		}

		count, err := ti.fetchFeed(&ti.feeds[i])
		if err != nil {
			// Log error but continue with other feeds
			continue
		}

		ti.mu.Lock()
		ti.feeds[i].LastUpdate = time.Now()
		ti.feeds[i].EntryCount = count
		ti.mu.Unlock()
	}

	ti.mu.Lock()
	ti.lastUpdate = time.Now()
	ti.mu.Unlock()

	// Persist updated data
	_ = ti.saveToDisk()
}

// fetchFeed downloads and parses a single threat feed
func (ti *ThreatIntel) fetchFeed(feed *ThreatFeed) (int, error) {
	resp, err := ti.httpClient.Get(feed.URL)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch feed %s: %w", feed.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("feed %s returned status %d", feed.Name, resp.StatusCode)
	}

	// Limit response size to prevent memory exhaustion
	limitedReader := io.LimitReader(resp.Body, 10*1024*1024) // 10MB max

	return ti.parseFeed(limitedReader, feed)
}

// parseFeed parses threat entries from a feed response
func (ti *ThreatIntel) parseFeed(reader io.Reader, feed *ThreatFeed) (int, error) {
	scanner := bufio.NewScanner(reader)
	count := 0
	now := time.Now()

	ti.mu.Lock()
	defer ti.mu.Unlock()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Extract IP or CIDR from line
		entry := ti.parseEntry(line, feed.Name, feed.Type)
		if entry == nil {
			continue
		}

		// Update existing or add new
		if existing, ok := ti.blockedIPs[entry.IP]; ok {
			existing.LastSeen = now
			existing.HitCount++
		} else {
			entry.FirstSeen = now
			entry.LastSeen = now
			ti.blockedIPs[entry.IP] = entry
		}
		count++
	}

	return count, scanner.Err()
}

// parseEntry parses a single line from a threat feed
func (ti *ThreatIntel) parseEntry(line, source, feedType string) *ThreatEntry {
	// Handle CIDR notation
	if strings.Contains(line, "/") && feedType == "cidr_list" {
		// Extract CIDR, handle trailing comments
		parts := strings.Fields(line)
		if len(parts) == 0 {
			return nil
		}
		cidr := parts[0]

		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil
		}

		// Store CIDR blocks for range checking
		ti.cidrBlocks = append(ti.cidrBlocks, ipnet)

		// Also store the network address as a representative entry
		return &ThreatEntry{
			IP:        cidr,
			RiskLevel: "HIGH",
			Category:  "Malicious Network",
			Source:    source,
		}
	}

	// Handle plain IP
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}
	ip := parts[0]

	// Validate IP
	if net.ParseIP(ip) == nil {
		return nil
	}

	return &ThreatEntry{
		IP:        ip,
		RiskLevel: "HIGH",
		Category:  "Threat Feed",
		Source:    source,
	}
}

// CheckIP returns threat information for an IP address
func (ti *ThreatIntel) CheckIP(ip string) (*ThreatEntry, bool) {
	ti.mu.RLock()
	defer ti.mu.RUnlock()

	// Direct lookup
	if entry, ok := ti.blockedIPs[ip]; ok {
		return entry, true
	}

	// Check CIDR ranges
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return nil, false
	}

	for _, cidr := range ti.cidrBlocks {
		if cidr.Contains(parsedIP) {
			return &ThreatEntry{
				IP:        ip,
				RiskLevel: "HIGH",
				Category:  "Malicious Network Range",
				Source:    "CIDR Block",
			}, true
		}
	}

	return nil, false
}

// BlockIP adds an IP to the custom blocklist
func (ti *ThreatIntel) BlockIP(ip, category string) error {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}

	now := time.Now()
	ti.blockedIPs[ip] = &ThreatEntry{
		IP:        ip,
		RiskLevel: "HIGH",
		Category:  category,
		Source:    "Custom Blocklist",
		FirstSeen: now,
		LastSeen:  now,
		HitCount:  0,
	}

	return nil
}

// UnblockIP removes an IP from the blocklist
func (ti *ThreatIntel) UnblockIP(ip string) error {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	if _, ok := ti.blockedIPs[ip]; !ok {
		return fmt.Errorf("IP not found in blocklist: %s", ip)
	}

	delete(ti.blockedIPs, ip)
	return nil
}

// GetAllThreats returns all blocked IPs
func (ti *ThreatIntel) GetAllThreats() []*ThreatEntry {
	ti.mu.RLock()
	defer ti.mu.RUnlock()

	threats := make([]*ThreatEntry, 0, len(ti.blockedIPs))
	for _, entry := range ti.blockedIPs {
		threats = append(threats, entry)
	}
	return threats
}

// GetFeeds returns all configured threat feeds
func (ti *ThreatIntel) GetFeeds() []ThreatFeed {
	ti.mu.RLock()
	defer ti.mu.RUnlock()

	feeds := make([]ThreatFeed, len(ti.feeds))
	copy(feeds, ti.feeds)
	return feeds
}

// GetStats returns threat intelligence statistics
func (ti *ThreatIntel) GetStats() map[string]interface{} {
	ti.mu.RLock()
	defer ti.mu.RUnlock()

	stats := map[string]interface{}{
		"total_threats":   len(ti.blockedIPs),
		"feeds_active":    0,
		"last_update":     ti.lastUpdate,
		"high_risk_count": 0,
		"blocked_today":   0,
	}

	for _, feed := range ti.feeds {
		if feed.Enabled {
			stats["feeds_active"] = stats["feeds_active"].(int) + 1
		}
	}

	today := time.Now().Truncate(24 * time.Hour)
	for _, entry := range ti.blockedIPs {
		if entry.RiskLevel == "HIGH" {
			stats["high_risk_count"] = stats["high_risk_count"].(int) + 1
		}
		if entry.LastSeen.After(today) {
			stats["blocked_today"] = stats["blocked_today"].(int) + 1
		}
	}

	return stats
}

// RecordHit updates the hit count for a blocked IP
func (ti *ThreatIntel) RecordHit(ip string) {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	if entry, ok := ti.blockedIPs[ip]; ok {
		entry.HitCount++
		entry.LastSeen = time.Now()
	}
}

// HTTPHandler returns an http.Handler for threat intelligence API
func (ti *ThreatIntel) HandleThreats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(ti.GetAllThreats())
}

// HandleBlockIP handles IP blocking requests
func (ti *ThreatIntel) HandleBlockIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IP       string `json:"ip"`
		Category string `json:"category"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Category == "" {
		req.Category = "Manual Block"
	}

	if err := ti.BlockIP(req.IP, req.Category); err != nil {
		w.Header().Set(contentTypeHeader, contentTypeJSON)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("IP %s blocked successfully", req.IP),
	})
}

// HandleStats returns threat intelligence statistics
func (ti *ThreatIntel) HandleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(ti.GetStats())
}
