// Package ipallow provides admin IP allowlisting for Obsidian WAF.
// Restricts administrative endpoints to trusted IP addresses.
package ipallow

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

var (
	ErrIPNotAllowed  = errors.New("IP address not in allowlist")
	ErrInvalidIP     = errors.New("invalid IP address format")
	ErrInvalidCIDR   = errors.New("invalid CIDR notation")
	ErrAlreadyExists = errors.New("IP/CIDR already in allowlist")
	ErrNotFound      = errors.New("IP/CIDR not found in allowlist")
)

// AllowlistEntry represents an allowed IP or CIDR range
type AllowlistEntry struct {
	ID          string     `json:"id"`
	IP          string     `json:"ip"` // Single IP or CIDR notation
	CIDR        *net.IPNet `json:"-"`  // Parsed CIDR (nil for single IP)
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	LastHit     *time.Time `json:"last_hit,omitempty"`
	HitCount    int64      `json:"hit_count"`
	Enabled     bool       `json:"enabled"`
}

// Config for IP allowlisting
type Config struct {
	// Enabled turns allowlisting on/off
	Enabled bool `json:"enabled"`

	// EnforceForAPI enforces allowlist for /api/admin/* endpoints
	EnforceForAPI bool `json:"enforce_for_api"`

	// EnforceForDashboard enforces allowlist for dashboard access
	EnforceForDashboard bool `json:"enforce_for_dashboard"`

	// AllowLocalhost always allows 127.0.0.1 and ::1
	AllowLocalhost bool `json:"allow_localhost"`

	// BypassHeader allows bypass if this header matches secret
	BypassHeader string `json:"bypass_header,omitempty"`
	BypassSecret string `json:"-"` // Never expose
}

// DefaultConfig returns safe defaults
func DefaultConfig() *Config {
	return &Config{
		Enabled:             false, // Off by default for development
		EnforceForAPI:       true,
		EnforceForDashboard: true,
		AllowLocalhost:      true,
	}
}

// Manager handles IP allowlisting
type Manager struct {
	mu      sync.RWMutex
	entries map[string]*AllowlistEntry // IP/CIDR string -> entry
	cidrs   []*AllowlistEntry          // CIDR entries for range matching
	config  *Config
	store   AllowlistStore
}

// AllowlistStore interface for persistence
type AllowlistStore interface {
	SaveEntry(ctx context.Context, entry *AllowlistEntry) error
	GetAllEntries(ctx context.Context) ([]*AllowlistEntry, error)
	DeleteEntry(ctx context.Context, id string) error
	UpdateHit(ctx context.Context, id string, hitTime time.Time) error
}

// NewManager creates a new IP allowlist manager
func NewManager(config *Config, store AllowlistStore) *Manager {
	if config == nil {
		config = DefaultConfig()
	}

	return &Manager{
		entries: make(map[string]*AllowlistEntry),
		cidrs:   make([]*AllowlistEntry, 0),
		config:  config,
		store:   store,
	}
}

// LoadEntries loads allowlist from store
func (m *Manager) LoadEntries(ctx context.Context) error {
	if m.store == nil {
		return nil
	}

	entries, err := m.store.GetAllEntries(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range entries {
		m.entries[entry.IP] = entry
		if entry.CIDR != nil {
			m.cidrs = append(m.cidrs, entry)
		}
	}

	return nil
}

// AddIP adds a single IP address to the allowlist
func (m *Manager) AddIP(ctx context.Context, ip, description, createdBy string) (*AllowlistEntry, error) {
	// Validate IP
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return nil, ErrInvalidIP
	}

	// Normalize IP string
	ip = parsedIP.String()

	m.mu.Lock()
	if _, exists := m.entries[ip]; exists {
		m.mu.Unlock()
		return nil, ErrAlreadyExists
	}
	m.mu.Unlock()

	entry := &AllowlistEntry{
		ID:          generateID(),
		IP:          ip,
		Description: description,
		CreatedAt:   time.Now(),
		CreatedBy:   createdBy,
		Enabled:     true,
	}

	// Persist
	if m.store != nil {
		if err := m.store.SaveEntry(ctx, entry); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	m.entries[ip] = entry
	m.mu.Unlock()

	return entry, nil
}

// AddCIDR adds a CIDR range to the allowlist
func (m *Manager) AddCIDR(ctx context.Context, cidr, description, createdBy string) (*AllowlistEntry, error) {
	// Parse CIDR
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, ErrInvalidCIDR
	}

	// Normalize CIDR string
	cidr = ipNet.String()

	m.mu.Lock()
	if _, exists := m.entries[cidr]; exists {
		m.mu.Unlock()
		return nil, ErrAlreadyExists
	}
	m.mu.Unlock()

	entry := &AllowlistEntry{
		ID:          generateID(),
		IP:          cidr,
		CIDR:        ipNet,
		Description: description,
		CreatedAt:   time.Now(),
		CreatedBy:   createdBy,
		Enabled:     true,
	}

	// Persist
	if m.store != nil {
		if err := m.store.SaveEntry(ctx, entry); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	m.entries[cidr] = entry
	m.cidrs = append(m.cidrs, entry)
	m.mu.Unlock()

	return entry, nil
}

// RemoveEntry removes an IP or CIDR from the allowlist
func (m *Manager) RemoveEntry(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find by ID
	var found *AllowlistEntry
	for _, entry := range m.entries {
		if entry.ID == id {
			found = entry
			break
		}
	}

	if found == nil {
		return ErrNotFound
	}

	// Remove from maps
	delete(m.entries, found.IP)

	// Remove from CIDR list if applicable
	if found.CIDR != nil {
		newCidrs := make([]*AllowlistEntry, 0, len(m.cidrs)-1)
		for _, e := range m.cidrs {
			if e.ID != id {
				newCidrs = append(newCidrs, e)
			}
		}
		m.cidrs = newCidrs
	}

	// Persist deletion
	if m.store != nil {
		return m.store.DeleteEntry(ctx, id)
	}

	return nil
}

// IsAllowed checks if an IP is in the allowlist
func (m *Manager) IsAllowed(ip string) bool {
	m.mu.RLock()
	enabled := m.config.Enabled
	allowLocalhost := m.config.AllowLocalhost
	m.mu.RUnlock()

	// If allowlisting is disabled, allow all
	if !enabled {
		return true
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	// Check localhost bypass
	if allowLocalhost {
		if parsedIP.IsLoopback() {
			return true
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check exact IP match
	normalizedIP := parsedIP.String()
	if entry, exists := m.entries[normalizedIP]; exists && entry.Enabled {
		// Update hit stats (async)
		go m.recordHit(entry)
		return true
	}

	// Check CIDR ranges
	for _, entry := range m.cidrs {
		if entry.Enabled && entry.CIDR != nil && entry.CIDR.Contains(parsedIP) {
			go m.recordHit(entry)
			return true
		}
	}

	return false
}

// CheckBypassHeader checks if bypass header is valid
func (m *Manager) CheckBypassHeader(headerValue string) bool {
	if m.config.BypassHeader == "" || m.config.BypassSecret == "" {
		return false
	}
	return headerValue == m.config.BypassSecret
}

// recordHit updates hit statistics
func (m *Manager) recordHit(entry *AllowlistEntry) {
	m.mu.Lock()
	now := time.Now()
	entry.LastHit = &now
	entry.HitCount++
	m.mu.Unlock()

	if m.store != nil {
		m.store.UpdateHit(context.Background(), entry.ID, now)
	}
}

// ListEntries returns all allowlist entries
func (m *Manager) ListEntries() []*AllowlistEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := make([]*AllowlistEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	return entries
}

// SetEnabled enables/disables an entry
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range m.entries {
		if entry.ID == id {
			entry.Enabled = enabled
			if m.store != nil {
				return m.store.SaveEntry(ctx, entry)
			}
			return nil
		}
	}

	return ErrNotFound
}

// GetConfig returns a copy of the current configuration
func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy to prevent external modification
	return &Config{
		Enabled:             m.config.Enabled,
		EnforceForAPI:       m.config.EnforceForAPI,
		EnforceForDashboard: m.config.EnforceForDashboard,
		AllowLocalhost:      m.config.AllowLocalhost,
		BypassHeader:        m.config.BypassHeader,
		BypassSecret:        m.config.BypassSecret,
	}
}

// SetConfig updates the configuration
func (m *Manager) SetConfig(config *Config) {
	if config == nil {
		return
	}
	m.mu.Lock()
	m.config = config
	m.mu.Unlock()
}

// IsEnabled returns whether allowlisting is enabled
func (m *Manager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Enabled
}

// Enable turns on IP allowlisting
func (m *Manager) Enable() {
	m.mu.Lock()
	m.config.Enabled = true
	m.mu.Unlock()
}

// Disable turns off IP allowlisting
func (m *Manager) Disable() {
	m.mu.Lock()
	m.config.Enabled = false
	m.mu.Unlock()
}

// generateID creates a unique ID for entries
func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}
