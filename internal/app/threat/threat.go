package threat

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// ThreatIntel manages threat intelligence feeds and IP reputation
type ThreatIntel struct {
	mu           sync.RWMutex
	blockedIPs   map[string]*ThreatEntry
	feeds        []ThreatFeed
	lastUpdate   time.Time
	updateTicker *time.Ticker
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
}

// NewThreatIntel creates a new threat intelligence manager
func NewThreatIntel() *ThreatIntel {
	ti := &ThreatIntel{
		blockedIPs: make(map[string]*ThreatEntry),
		feeds: []ThreatFeed{
			{Name: "AbuseIPDB", URL: "https://api.abuseipdb.com/api/v2/blacklist", Enabled: true},
			{Name: "Spamhaus DROP", URL: "https://www.spamhaus.org/drop/drop.txt", Enabled: true},
			{Name: "Emerging Threats", URL: "https://rules.emergingthreats.net/blockrules/compromised-ips.txt", Enabled: true},
			{Name: "Firehol Level 1", URL: "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset", Enabled: true},
			{Name: "Custom Blocklist", URL: "", Enabled: true},
		},
	}

	// Initialize with some known bad actors for demo
	ti.seedDemoData()

	// Start background update (would fetch real feeds in production)
	ti.startBackgroundUpdates()

	return ti
}

// seedDemoData populates initial threat data for demonstration
func (ti *ThreatIntel) seedDemoData() {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	demoThreats := []ThreatEntry{
		{IP: "185.220.101.34", RiskLevel: "HIGH", Category: "Tor Exit Node", Source: "Spamhaus DROP"},
		{IP: "45.155.205.233", RiskLevel: "HIGH", Category: "Scanner", Source: "AbuseIPDB"},
		{IP: "192.241.xxx.xxx", RiskLevel: "MEDIUM", Category: "Botnet C2", Source: "Emerging Threats"},
		{IP: "103.224.182.250", RiskLevel: "HIGH", Category: "Brute Force", Source: "AbuseIPDB"},
		{IP: "91.240.118.172", RiskLevel: "MEDIUM", Category: "Web Spam", Source: "Firehol Level 1"},
		{IP: "178.128.xxx.xxx", RiskLevel: "LOW", Category: "Proxy", Source: "Custom Blocklist"},
		{IP: "167.99.xxx.xxx", RiskLevel: "MEDIUM", Category: "Port Scanner", Source: "AbuseIPDB"},
		{IP: "64.225.xxx.xxx", RiskLevel: "HIGH", Category: "SQL Injection", Source: "Custom Blocklist"},
	}

	now := time.Now()
	for _, t := range demoThreats {
		t.FirstSeen = now.Add(-24 * time.Hour * time.Duration(1+len(t.IP)%7))
		t.LastSeen = now.Add(-time.Duration(len(t.IP)%60) * time.Minute)
		t.HitCount = len(t.IP) % 50
		ti.blockedIPs[t.IP] = &t
	}
}

// startBackgroundUpdates periodically refreshes threat feeds
func (ti *ThreatIntel) startBackgroundUpdates() {
	ti.updateTicker = time.NewTicker(1 * time.Hour)
	go func() {
		for range ti.updateTicker.C {
			ti.refreshFeeds()
		}
	}()
}

// refreshFeeds updates threat intelligence from all enabled feeds
func (ti *ThreatIntel) refreshFeeds() {
	// In production, this would fetch from actual threat feeds
	// For demo, we just update the lastUpdate time
	ti.mu.Lock()
	ti.lastUpdate = time.Now()
	for i := range ti.feeds {
		ti.feeds[i].LastUpdate = time.Now()
		ti.feeds[i].EntryCount = len(ti.blockedIPs) / len(ti.feeds)
	}
	ti.mu.Unlock()
}

// CheckIP returns threat information for an IP address
func (ti *ThreatIntel) CheckIP(ip string) (*ThreatEntry, bool) {
	ti.mu.RLock()
	defer ti.mu.RUnlock()

	// Direct lookup
	if entry, ok := ti.blockedIPs[ip]; ok {
		return entry, true
	}

	// Check CIDR ranges (simplified)
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return nil, false
	}

	// Would check against CIDR blocks in production
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
	w.Header().Set("Content-Type", "application/json")
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
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("IP %s blocked successfully", req.IP),
	})
}

// HandleStats returns threat intelligence statistics
func (ti *ThreatIntel) HandleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ti.GetStats())
}
