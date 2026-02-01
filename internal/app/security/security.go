// Package security provides security utilities and configurations for Obsidian WAF.
// This package centralizes security-related logic including IP validation, secret management,
// and common password detection to address critical audit findings.
package security

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

// Errors for security operations
var (
	ErrMissingSecret       = errors.New("OBSIDIAN_JWT_SECRET environment variable is required in production")
	ErrInsecureSecret      = errors.New("JWT secret must be at least 32 characters")
	ErrIPSpoofingDetected  = errors.New("potential IP spoofing detected")
	ErrCommonPassword      = errors.New("password is too common and easily guessable")
	ErrPasswordTooShort    = errors.New("password must be at least 12 characters")
	ErrPasswordNoUpper     = errors.New("password must contain uppercase letters")
	ErrPasswordNoLower     = errors.New("password must contain lowercase letters")
	ErrPasswordNoNumber    = errors.New("password must contain numbers")
	ErrPasswordNoSpecial   = errors.New("password must contain special characters")
	ErrPasswordCompromised = errors.New("password appears in known data breaches")
)

// Config holds security configuration
type Config struct {
	// RequireSecureSecret fails startup if JWT secret is not set (for production)
	RequireSecureSecret bool

	// TrustedProxies is a list of IP addresses/CIDRs that are trusted to set X-Forwarded-For
	TrustedProxies []string

	// MinPasswordLength is the minimum required password length
	MinPasswordLength int

	// RequireAllComplexity requires all complexity groups (upper, lower, number, special)
	RequireAllComplexity bool
}

// DefaultConfig returns secure defaults
func DefaultConfig() Config {
	return Config{
		RequireSecureSecret:  os.Getenv("OBSIDIAN_ENV") == "production",
		TrustedProxies:       []string{"127.0.0.1", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		MinPasswordLength:    12,
		RequireAllComplexity: true,
	}
}

// Manager handles security operations
type Manager struct {
	mu              sync.RWMutex
	config          Config
	trustedNets     []*net.IPNet
	trustedIPs      map[string]bool
	jwtSecret       string
	commonPasswords map[string]bool
}

// NewManager creates a new security manager
func NewManager(cfg Config) (*Manager, error) {
	m := &Manager{
		config:          cfg,
		trustedIPs:      make(map[string]bool),
		commonPasswords: loadCommonPasswords(),
	}

	// Parse trusted proxies
	for _, proxy := range cfg.TrustedProxies {
		if strings.Contains(proxy, "/") {
			_, ipNet, err := net.ParseCIDR(proxy)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %s: %w", proxy, err)
			}
			m.trustedNets = append(m.trustedNets, ipNet)
		} else {
			m.trustedIPs[proxy] = true
		}
	}

	// Initialize JWT secret
	if err := m.initJWTSecret(); err != nil {
		return nil, err
	}

	return m, nil
}

// initJWTSecret initializes the JWT secret from environment or generates one
func (m *Manager) initJWTSecret() error {
	secret := os.Getenv("OBSIDIAN_JWT_SECRET")

	if secret == "" {
		if m.config.RequireSecureSecret {
			return ErrMissingSecret
		}
		// Generate a secure random secret for development
		generated, err := generateSecureSecret(32)
		if err != nil {
			return fmt.Errorf("failed to generate JWT secret: %w", err)
		}
		m.jwtSecret = generated
		fmt.Println("⚠️  WARNING: Generated temporary JWT secret. Set OBSIDIAN_JWT_SECRET for production!")
		fmt.Println("⚠️  Tokens will be invalidated on restart.")
		return nil
	}

	if len(secret) < 32 {
		return ErrInsecureSecret
	}

	m.jwtSecret = secret
	return nil
}

// GetJWTSecret returns the JWT signing secret
func (m *Manager) GetJWTSecret() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jwtSecret
}

// ExtractClientIP extracts the real client IP, validating against trusted proxies
func (m *Manager) ExtractClientIP(remoteAddr string, xForwardedFor string, xRealIP string) (string, error) {
	// Parse the direct connection IP
	directIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// remoteAddr might not have a port
		directIP = remoteAddr
	}

	// Handle IPv6 brackets
	directIP = strings.TrimPrefix(directIP, "[")
	directIP = strings.TrimSuffix(directIP, "]")

	// If no proxy headers, return direct IP
	if xForwardedFor == "" && xRealIP == "" {
		return directIP, nil
	}

	// Check if the direct connection is from a trusted proxy
	if !m.isTrustedProxy(directIP) {
		// Not from a trusted proxy - ignore proxy headers to prevent spoofing
		return directIP, nil
	}

	// Process X-Forwarded-For from right to left, finding the first untrusted IP
	if xForwardedFor != "" {
		ips := strings.Split(xForwardedFor, ",")
		// Traverse from right to left (closest to server first)
		for i := len(ips) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(ips[i])
			if ip == "" {
				continue
			}
			// If this IP is not trusted, it's the real client IP
			if !m.isTrustedProxy(ip) {
				return ip, nil
			}
		}
	}

	// Check X-Real-IP as fallback
	if xRealIP != "" {
		xRealIP = strings.TrimSpace(xRealIP)
		if !m.isTrustedProxy(xRealIP) {
			return xRealIP, nil
		}
	}

	// All IPs in chain are trusted, return the direct IP
	return directIP, nil
}

// isTrustedProxy checks if an IP is in the trusted proxy list
func (m *Manager) isTrustedProxy(ipStr string) bool {
	// Handle IPv6 brackets
	ipStr = strings.TrimPrefix(ipStr, "[")
	ipStr = strings.TrimSuffix(ipStr, "]")

	// Check direct IP match
	if m.trustedIPs[ipStr] {
		return true
	}

	// Parse IP for CIDR checking
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	// Check CIDR ranges
	for _, ipNet := range m.trustedNets {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// AddTrustedProxy adds an IP or CIDR to the trusted proxy list
func (m *Manager) AddTrustedProxy(proxy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.Contains(proxy, "/") {
		_, ipNet, err := net.ParseCIDR(proxy)
		if err != nil {
			return fmt.Errorf("invalid CIDR: %w", err)
		}
		m.trustedNets = append(m.trustedNets, ipNet)
	} else {
		m.trustedIPs[proxy] = true
	}
	return nil
}

// ValidatePassword checks password against security policy
func (m *Manager) ValidatePassword(password string) error {
	// Check length
	if len(password) < m.config.MinPasswordLength {
		return fmt.Errorf("%w: minimum %d characters required", ErrPasswordTooShort, m.config.MinPasswordLength)
	}

	// Check for common passwords
	if m.isCommonPassword(password) {
		return ErrCommonPassword
	}

	// Check complexity
	var hasUpper, hasLower, hasNumber, hasSpecial bool
	for _, c := range password {
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= '0' && c <= '9':
			hasNumber = true
		case strings.ContainsRune("!@#$%^&*()_+-=[]{}|;':\",./<>?`~", c):
			hasSpecial = true
		}
	}

	if m.config.RequireAllComplexity {
		if !hasUpper {
			return ErrPasswordNoUpper
		}
		if !hasLower {
			return ErrPasswordNoLower
		}
		if !hasNumber {
			return ErrPasswordNoNumber
		}
		if !hasSpecial {
			return ErrPasswordNoSpecial
		}
	} else {
		// Require at least 3 of 4 complexity groups
		count := 0
		if hasUpper {
			count++
		}
		if hasLower {
			count++
		}
		if hasNumber {
			count++
		}
		if hasSpecial {
			count++
		}
		if count < 3 {
			return errors.New("password must contain at least 3 of: uppercase, lowercase, numbers, special characters")
		}
	}

	return nil
}

// isCommonPassword checks if password is in the common passwords list
func (m *Manager) isCommonPassword(password string) bool {
	// Check lowercase version
	lower := strings.ToLower(password)
	return m.commonPasswords[lower]
}

// generateSecureSecret generates a cryptographically secure random secret
func generateSecureSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// loadCommonPasswords returns a set of common passwords that should be rejected
func loadCommonPasswords() map[string]bool {
	// Top 100 most common passwords - these should NEVER be accepted
	common := []string{
		"password", "123456", "12345678", "qwerty", "abc123", "monkey", "1234567",
		"letmein", "trustno1", "dragon", "baseball", "iloveyou", "master", "sunshine",
		"ashley", "bailey", "passw0rd", "shadow", "123123", "654321", "superman",
		"qazwsx", "michael", "football", "password1", "password123", "welcome",
		"welcome1", "admin", "admin123", "root", "toor", "pass", "test", "guest",
		"master123", "changeme", "12345", "123456789", "1234567890", "0987654321",
		"qwerty123", "qwertyuiop", "1q2w3e4r", "1qaz2wsx", "zaq12wsx", "!qaz2wsx",
		"password!", "p@ssw0rd", "p@ssword", "passw0rd!", "pa$$word", "pa$$w0rd",
		"secret", "secret123", "letmein123", "access", "login", "administrator",
		"default", "hello", "hello123", "winter", "spring", "summer", "autumn",
		"january", "february", "march", "april", "may2024", "june2024", "july2024",
		"company", "company123", "corporate", "business", "enterprise", "network",
		"server", "database", "mysql", "oracle", "postgres", "mongo", "redis",
		"security", "secure", "private", "public", "internal", "external",
		"obsidian", "sentinel", "waf", "firewall", "proxy", "gateway", "router",
	}

	passwords := make(map[string]bool, len(common))
	for _, p := range common {
		passwords[p] = true
	}
	return passwords
}

// SanitizeLogField removes sensitive data from log fields
func SanitizeLogField(fieldName, value string) string {
	sensitiveFields := map[string]bool{
		"password":      true,
		"passwd":        true,
		"secret":        true,
		"token":         true,
		"authorization": true,
		"auth":          true,
		"key":           true,
		"apikey":        true,
		"api_key":       true,
		"private":       true,
		"credential":    true,
		"credentials":   true,
		"session":       true,
		"cookie":        true,
		"jwt":           true,
	}

	lower := strings.ToLower(fieldName)
	if sensitiveFields[lower] {
		return "[REDACTED]"
	}
	return value
}
