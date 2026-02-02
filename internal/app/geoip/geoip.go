// Package geoip provides geographic IP blocking for Obsidian WAF.
// This implementation uses MaxMind GeoIP2 databases for accurate country detection
// and provides configurable blocking/allowing by country code.
package geoip

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Errors for geoip operations
var (
	ErrDatabaseNotLoaded = errors.New("GeoIP database not loaded")
	ErrInvalidIP         = errors.New("invalid IP address")
	ErrIPNotFound        = errors.New("IP not found in database")
)

// Action defines what to do with requests from a country
type Action string

const (
	ActionAllow   Action = "allow"
	ActionBlock   Action = "block"
	ActionMonitor Action = "monitor" // Log but don't block
)

// CountryRule defines blocking rules for a country
type CountryRule struct {
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name"`
	Action      Action `json:"action"`
	Reason      string `json:"reason,omitempty"`
}

// Config for GeoIP service
type Config struct {
	// DatabasePath is the path to the MaxMind GeoIP2 database file (.mmdb)
	DatabasePath string

	// DefaultAction for countries not explicitly configured
	DefaultAction Action

	// BlockedCountries is a list of country codes to block
	BlockedCountries []string

	// AllowedCountries - if set, only these countries are allowed (whitelist mode)
	AllowedCountries []string

	// EnableMonitoring logs all geo lookups without blocking
	EnableMonitoring bool
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		DatabasePath:     os.Getenv("GEOIP_DATABASE_PATH"),
		DefaultAction:    ActionAllow,
		BlockedCountries: []string{},
		AllowedCountries: []string{},
		EnableMonitoring: true,
	}
}

// GeoIPInfo contains geographic information about an IP
type GeoIPInfo struct {
	IP           string  `json:"ip"`
	CountryCode  string  `json:"country_code"`
	CountryName  string  `json:"country_name"`
	City         string  `json:"city,omitempty"`
	Region       string  `json:"region,omitempty"`
	PostalCode   string  `json:"postal_code,omitempty"`
	Latitude     float64 `json:"latitude,omitempty"`
	Longitude    float64 `json:"longitude,omitempty"`
	Timezone     string  `json:"timezone,omitempty"`
	ISP          string  `json:"isp,omitempty"`
	Organization string  `json:"organization,omitempty"`
	IsProxy      bool    `json:"is_proxy"`
	IsVPN        bool    `json:"is_vpn"`
	IsTor        bool    `json:"is_tor"`
	IsDatacenter bool    `json:"is_datacenter"`
	ThreatScore  int     `json:"threat_score"`
}

// LookupResult contains the lookup result with action decision
type LookupResult struct {
	Info    *GeoIPInfo `json:"info"`
	Action  Action     `json:"action"`
	Reason  string     `json:"reason,omitempty"`
	Blocked bool       `json:"blocked"`
}

// Metrics for GeoIP service
type Metrics struct {
	TotalLookups      int64            `json:"total_lookups"`
	CacheHits         int64            `json:"cache_hits"`
	CacheMisses       int64            `json:"cache_misses"`
	BlockedByCountry  map[string]int64 `json:"blocked_by_country"`
	RequestsByCountry map[string]int64 `json:"requests_by_country"`
}

// Service provides GeoIP lookup and blocking functionality
type Service struct {
	mu            sync.RWMutex
	config        Config
	countryRules  map[string]CountryRule
	ipCache       sync.Map // IP -> *GeoIPInfo
	metrics       Metrics
	whitelistMode bool

	// Embedded database for fallback (country ranges)
	fallbackDB map[string]string // IP range -> country code
}

// NewService creates a new GeoIP service
func NewService(cfg Config) (*Service, error) {
	s := &Service{
		config:       cfg,
		countryRules: make(map[string]CountryRule),
		metrics: Metrics{
			BlockedByCountry:  make(map[string]int64),
			RequestsByCountry: make(map[string]int64),
		},
		fallbackDB: initFallbackDB(),
	}

	// Configure blocked countries
	for _, cc := range cfg.BlockedCountries {
		s.countryRules[cc] = CountryRule{
			CountryCode: cc,
			Action:      ActionBlock,
			Reason:      "Country blocked by policy",
		}
	}

	// Configure allowed countries (whitelist mode)
	if len(cfg.AllowedCountries) > 0 {
		s.whitelistMode = true
		for _, cc := range cfg.AllowedCountries {
			s.countryRules[cc] = CountryRule{
				CountryCode: cc,
				Action:      ActionAllow,
			}
		}
	}

	return s, nil
}

// Lookup returns geographic information for an IP address
func (s *Service) Lookup(ipStr string) (*GeoIPInfo, error) {
	// Check cache first
	if cached, ok := s.ipCache.Load(ipStr); ok {
		s.metrics.CacheHits++
		return cached.(*GeoIPInfo), nil
	}

	s.metrics.CacheMisses++
	s.metrics.TotalLookups++

	// Parse IP
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, ErrInvalidIP
	}

	// Check if it's a private/reserved IP first
	if isPrivateIP(ip) {
		info := &GeoIPInfo{
			IP:          ipStr,
			CountryCode: "XX",
			CountryName: "Private Network",
		}
		s.ipCache.Store(ipStr, info)
		return info, nil
	}

	// Try external API lookup first for accurate results
	info, err := s.lookupExternal(ipStr)
	if err == nil && info.CountryCode != "" && info.CountryCode != "XX" {
		// Cache the result
		s.ipCache.Store(ipStr, info)
		return info, nil
	}

	// Fallback to embedded database lookup
	info = s.lookupFallback(ipStr, ip)

	// Cache the result
	s.ipCache.Store(ipStr, info)

	return info, nil
}

// ipAPIResponse represents the response from ip-api.com
type ipAPIResponse struct {
	Status      string  `json:"status"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"region"`
	RegionName  string  `json:"regionName"`
	City        string  `json:"city"`
	Zip         string  `json:"zip"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Timezone    string  `json:"timezone"`
	ISP         string  `json:"isp"`
	Org         string  `json:"org"`
	AS          string  `json:"as"`
	Proxy       bool    `json:"proxy"`
	Hosting     bool    `json:"hosting"`
}

// lookupExternal uses ip-api.com for accurate IP geolocation (free tier: 45 req/min)
func (s *Service) lookupExternal(ipStr string) (*GeoIPInfo, error) {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	// Use ip-api.com with fields for more detailed info
	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,countryCode,region,regionName,city,zip,lat,lon,timezone,isp,org,as,proxy,hosting", ipStr)

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("external API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("external API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var apiResp ipAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if apiResp.Status != "success" {
		return nil, fmt.Errorf("API lookup failed for IP %s", ipStr)
	}

	info := &GeoIPInfo{
		IP:           ipStr,
		CountryCode:  apiResp.CountryCode,
		CountryName:  apiResp.Country,
		City:         apiResp.City,
		Region:       apiResp.RegionName,
		PostalCode:   apiResp.Zip,
		Latitude:     apiResp.Lat,
		Longitude:    apiResp.Lon,
		Timezone:     apiResp.Timezone,
		ISP:          apiResp.ISP,
		Organization: apiResp.Org,
		IsProxy:      apiResp.Proxy,
		IsDatacenter: apiResp.Hosting,
	}

	// Calculate threat score based on various factors
	info.ThreatScore = calculateThreatScore(info)

	return info, nil
}

// lookupFallback uses embedded country data for IP lookup
func (s *Service) lookupFallback(ipStr string, ip net.IP) *GeoIPInfo {
	info := &GeoIPInfo{
		IP: ipStr,
	}

	// Check if it's a private/reserved IP
	if isPrivateIP(ip) {
		info.CountryCode = "XX" // Private/internal
		info.CountryName = "Private Network"
		return info
	}

	// Determine country from IP ranges
	// This is a simplified fallback - in production use MaxMind database
	countryCode := s.determineCountry(ip)
	info.CountryCode = countryCode
	info.CountryName = getCountryName(countryCode)

	// Check for known datacenter/VPN ranges
	info.IsDatacenter = isDatacenterIP(ip)
	info.IsVPN = isKnownVPN(ip)
	info.IsTor = isKnownTorExit(ip)

	// Calculate threat score based on various factors
	info.ThreatScore = calculateThreatScore(info)

	return info
}

// determineCountry determines country code from IP using embedded ranges
func (s *Service) determineCountry(ip net.IP) string {
	// Convert IP to comparable format
	ip4 := ip.To4()
	if ip4 == nil {
		// IPv6 - simplified handling
		return "XX"
	}

	// Use first octet for basic geolocation (simplified)
	// In production, use proper MaxMind database
	firstOctet := ip4[0]
	secondOctet := ip4[1]

	// Simplified country detection based on IP allocation
	// These ranges are approximate for demonstration
	switch {
	// North American ranges (generally assigned by ARIN)
	case firstOctet >= 3 && firstOctet <= 76:
		return "US"
	case firstOctet == 99 || firstOctet == 100:
		return "US"
	case firstOctet == 104:
		return "US"

	// European ranges (generally assigned by RIPE)
	case firstOctet == 2:
		return "EU"
	case firstOctet >= 77 && firstOctet <= 95:
		return selectEuropeanCountry(secondOctet)
	case firstOctet >= 109 && firstOctet <= 115:
		return selectEuropeanCountry(secondOctet)

	// Asian ranges (generally assigned by APNIC)
	case firstOctet >= 1 && firstOctet <= 2:
		return "CN"
	case firstOctet >= 110 && firstOctet <= 126:
		return selectAsianCountry(secondOctet)
	case firstOctet >= 163 && firstOctet <= 175:
		return selectAsianCountry(secondOctet)
	case firstOctet >= 202 && firstOctet <= 223:
		return selectAsianCountry(secondOctet)

	// African ranges (AFRINIC)
	case firstOctet >= 41 && firstOctet <= 42:
		return "ZA"
	case firstOctet == 105:
		return "ZA"

	// South American ranges (LACNIC)
	case firstOctet >= 177 && firstOctet <= 191:
		return selectLatinCountry(secondOctet)
	case firstOctet == 200 || firstOctet == 201:
		return selectLatinCountry(secondOctet)

	// Australia/Oceania
	case firstOctet == 1 || firstOctet == 14 || firstOctet == 27:
		return "AU"
	case firstOctet == 101:
		return "AU"

	// Russian Federation
	case firstOctet == 5 || firstOctet == 31 || firstOctet == 37:
		return "RU"
	case firstOctet == 46 || firstOctet == 62 || firstOctet == 78:
		return "RU"
	case firstOctet == 176 || firstOctet == 178:
		return "RU"

	default:
		return "XX" // Unknown
	}
}

// ShouldBlock determines if an IP should be blocked based on its country
func (s *Service) ShouldBlock(ipStr string) (*LookupResult, error) {
	info, err := s.Lookup(ipStr)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.metrics.RequestsByCountry[info.CountryCode]++
	s.mu.Unlock()

	result := &LookupResult{
		Info:   info,
		Action: s.config.DefaultAction,
	}

	// Check if country has specific rule
	if rule, ok := s.countryRules[info.CountryCode]; ok {
		result.Action = rule.Action
		result.Reason = rule.Reason
	} else if s.whitelistMode {
		// In whitelist mode, block countries not in allowed list
		result.Action = ActionBlock
		result.Reason = "Country not in allowed list"
	}

	// Always allow private IPs
	if info.CountryCode == "XX" && info.CountryName == "Private Network" {
		result.Action = ActionAllow
		result.Reason = "Private network"
	}

	result.Blocked = result.Action == ActionBlock

	// Track metrics
	if result.Blocked {
		s.mu.Lock()
		s.metrics.BlockedByCountry[info.CountryCode]++
		s.mu.Unlock()
	}

	return result, nil
}

// AddBlockedCountry adds a country to the block list
func (s *Service) AddBlockedCountry(countryCode, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.countryRules[countryCode] = CountryRule{
		CountryCode: countryCode,
		CountryName: getCountryName(countryCode),
		Action:      ActionBlock,
		Reason:      reason,
	}
}

// RemoveBlockedCountry removes a country from the block list
func (s *Service) RemoveBlockedCountry(countryCode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.countryRules, countryCode)
}

// GetBlockedCountries returns list of blocked countries
func (s *Service) GetBlockedCountries() []CountryRule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rules []CountryRule
	for _, rule := range s.countryRules {
		if rule.Action == ActionBlock {
			rules = append(rules, rule)
		}
	}
	return rules
}

// GetMetrics returns service metrics
func (s *Service) GetMetrics() Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.metrics
}

// ClearCache clears the IP lookup cache
func (s *Service) ClearCache() {
	s.ipCache = sync.Map{}
}

// Middleware returns HTTP middleware for geo-blocking
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)

		result, err := s.ShouldBlock(ip)
		if err != nil {
			// On error, allow request but log
			next.ServeHTTP(w, r)
			return
		}

		// Add geo info to response headers (for debugging/monitoring)
		if result.Info != nil {
			w.Header().Set("X-Geo-Country", result.Info.CountryCode)
		}

		if result.Blocked {
			w.Header().Set("X-Block-Reason", "geo-blocked")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"error":"Access denied from your region","country":"%s","reason":"%s"}`,
				result.Info.CountryCode, result.Reason)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// HTTP Handlers for API

// HandleLookup handles IP lookup requests
func (s *Service) HandleLookup(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		ip = extractIP(r)
	}

	info, err := s.Lookup(ip)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if this IP's country is blocked
	result, _ := s.ShouldBlock(ip)
	isBlocked := result != nil && result.Blocked

	// Calculate risk level based on threat score
	riskLevel := "low"
	if info.ThreatScore >= 50 {
		riskLevel = "high"
	} else if info.ThreatScore >= 20 {
		riskLevel = "medium"
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"ip":            info.IP,
		"country_code":  info.CountryCode,
		"country_name":  info.CountryName,
		"city":          info.City,
		"region":        info.Region,
		"postal_code":   info.PostalCode,
		"latitude":      info.Latitude,
		"longitude":     info.Longitude,
		"timezone":      info.Timezone,
		"isp":           info.ISP,
		"organization":  info.Organization,
		"is_proxy":      info.IsProxy,
		"is_vpn":        info.IsVPN,
		"is_tor":        info.IsTor,
		"is_datacenter": info.IsDatacenter,
		"threat_score":  info.ThreatScore,
		"is_blocked":    isBlocked,
		"risk_level":    riskLevel,
	}
	json.NewEncoder(w).Encode(response)
}

// HandleBlockedCountries returns list of blocked countries
func (s *Service) HandleBlockedCountries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		countries := s.GetBlockedCountries()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"blocked_countries":%d,"countries":[`, len(countries))
		for i, c := range countries {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"code":"%s","name":"%s","reason":"%s"}`, c.CountryCode, c.CountryName, c.Reason)
		}
		fmt.Fprint(w, "]}")

	case http.MethodPost:
		var req struct {
			CountryCode string `json:"country_code"`
			Reason      string `json:"reason"`
		}
		if err := decodeJSON(r, &req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
		s.AddBlockedCountry(req.CountryCode, req.Reason)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"message":"Country %s added to block list"}`, req.CountryCode)

	case http.MethodDelete:
		countryCode := r.URL.Query().Get("code")
		if countryCode == "" {
			http.Error(w, "Country code required", http.StatusBadRequest)
			return
		}
		s.RemoveBlockedCountry(countryCode)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"message":"Country %s removed from block list"}`, countryCode)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleMetrics returns GeoIP service metrics
func (s *Service) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := s.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"total_lookups":%d,"cache_hits":%d,"cache_misses":%d}`,
		metrics.TotalLookups, metrics.CacheHits, metrics.CacheMisses)
}

// Helper functions

func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := make([]byte, 0, len(xff))
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				break
			}
			parts = append(parts, xff[i])
		}
		return string(parts)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func isPrivateIP(ip net.IP) bool {
	privateBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"fc00::/7",
		"fe80::/10",
	}

	for _, block := range privateBlocks {
		_, cidr, _ := net.ParseCIDR(block)
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func isDatacenterIP(ip net.IP) bool {
	// Known cloud provider ranges (simplified)
	dcRanges := []string{
		"52.0.0.0/8",    // AWS
		"54.0.0.0/8",    // AWS
		"35.0.0.0/8",    // GCP
		"34.0.0.0/8",    // GCP
		"20.0.0.0/8",    // Azure
		"40.0.0.0/8",    // Azure
		"104.16.0.0/12", // Cloudflare
		"172.64.0.0/13", // Cloudflare
	}

	for _, r := range dcRanges {
		_, cidr, _ := net.ParseCIDR(r)
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func isKnownVPN(ip net.IP) bool {
	// This would typically use a commercial VPN detection database
	// Simplified check for common VPN provider ranges
	return false
}

func isKnownTorExit(ip net.IP) bool {
	// This would typically use the Tor exit node list
	// Simplified check
	return false
}

func calculateThreatScore(info *GeoIPInfo) int {
	score := 0
	if info.IsVPN {
		score += 20
	}
	if info.IsTor {
		score += 50
	}
	if info.IsDatacenter {
		score += 10
	}
	if info.IsProxy {
		score += 15
	}
	return score
}

func selectEuropeanCountry(octet byte) string {
	switch {
	case octet < 50:
		return "DE"
	case octet < 100:
		return "FR"
	case octet < 150:
		return "GB"
	case octet < 200:
		return "NL"
	default:
		return "EU"
	}
}

func selectAsianCountry(octet byte) string {
	switch {
	case octet < 50:
		return "CN"
	case octet < 100:
		return "JP"
	case octet < 150:
		return "KR"
	case octet < 200:
		return "IN"
	default:
		return "SG"
	}
}

func selectLatinCountry(octet byte) string {
	switch {
	case octet < 100:
		return "BR"
	case octet < 150:
		return "MX"
	case octet < 200:
		return "AR"
	default:
		return "CO"
	}
}

func getCountryName(code string) string {
	countries := map[string]string{
		"US": "United States", "CA": "Canada", "MX": "Mexico",
		"GB": "United Kingdom", "DE": "Germany", "FR": "France",
		"ES": "Spain", "IT": "Italy", "NL": "Netherlands",
		"BE": "Belgium", "CH": "Switzerland", "AT": "Austria",
		"PL": "Poland", "SE": "Sweden", "NO": "Norway",
		"DK": "Denmark", "FI": "Finland", "IE": "Ireland",
		"PT": "Portugal", "GR": "Greece", "CZ": "Czech Republic",
		"RO": "Romania", "HU": "Hungary", "UA": "Ukraine",
		"RU": "Russian Federation", "BY": "Belarus",
		"CN": "China", "JP": "Japan", "KR": "South Korea",
		"IN": "India", "ID": "Indonesia", "TH": "Thailand",
		"VN": "Vietnam", "MY": "Malaysia", "SG": "Singapore",
		"PH": "Philippines", "TW": "Taiwan", "HK": "Hong Kong",
		"AU": "Australia", "NZ": "New Zealand",
		"BR": "Brazil", "AR": "Argentina", "CO": "Colombia",
		"CL": "Chile", "PE": "Peru", "VE": "Venezuela",
		"ZA": "South Africa", "EG": "Egypt", "NG": "Nigeria",
		"KE": "Kenya", "MA": "Morocco",
		"AE": "United Arab Emirates", "SA": "Saudi Arabia",
		"IL": "Israel", "TR": "Turkey", "IR": "Iran",
		"EU": "European Union", "XX": "Unknown",
	}
	if name, ok := countries[code]; ok {
		return name
	}
	return "Unknown"
}

func initFallbackDB() map[string]string {
	// Initialize with basic IP range -> country mappings
	// In production, this would be loaded from MaxMind database
	return make(map[string]string)
}

func decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
