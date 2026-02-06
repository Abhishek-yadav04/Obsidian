// Package respbody provides response body inspection for data leakage prevention.
// Scans outgoing responses for sensitive data patterns like SSN, credit cards, API keys.
package respbody

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrDataLeakageDetected = errors.New("potential data leakage detected in response")
	ErrResponseTooLarge    = errors.New("response body exceeds inspection limit")
)

// LeakageType identifies the type of sensitive data detected
type LeakageType string

const (
	LeakageSSN         LeakageType = "ssn"
	LeakageCreditCard  LeakageType = "credit_card"
	LeakageAPIKey      LeakageType = "api_key"
	LeakageAWSKey      LeakageType = "aws_key"
	LeakagePrivateKey  LeakageType = "private_key"
	LeakageJWT         LeakageType = "jwt_token"
	LeakagePassword    LeakageType = "password"
	LeakageEmail       LeakageType = "email_bulk"
	LeakageIPAddress   LeakageType = "ip_bulk"
	LeakagePhoneNumber LeakageType = "phone_number"
	LeakageCustom      LeakageType = "custom"
)

// Detection represents a detected data leakage
type Detection struct {
	Type     LeakageType `json:"type"`
	Pattern  string      `json:"pattern"`
	Count    int         `json:"count"`
	Severity string      `json:"severity"`           // low, medium, high, critical
	Redacted string      `json:"redacted,omitempty"` // Redacted sample
}

// Config for response body inspection
type Config struct {
	// Enabled turns inspection on/off
	Enabled bool `json:"enabled"`

	// MaxBodySize maximum response size to inspect (default: 1MB)
	MaxBodySize int64 `json:"max_body_size"`

	// DetectSSN looks for US Social Security Numbers
	DetectSSN bool `json:"detect_ssn"`

	// DetectCreditCard looks for credit card numbers
	DetectCreditCard bool `json:"detect_credit_card"`

	// DetectAPIKeys looks for common API key patterns
	DetectAPIKeys bool `json:"detect_api_keys"`

	// DetectAWSKeys looks for AWS access keys
	DetectAWSKeys bool `json:"detect_aws_keys"`

	// DetectPrivateKeys looks for PEM-encoded private keys
	DetectPrivateKeys bool `json:"detect_private_keys"`

	// DetectJWT looks for JWT tokens
	DetectJWT bool `json:"detect_jwt"`

	// DetectPasswordFields looks for password-like field values
	DetectPasswordFields bool `json:"detect_password_fields"`

	// DetectBulkEmail detects bulk email exposure (>5 emails)
	DetectBulkEmail bool `json:"detect_bulk_email"`

	// DetectBulkIP detects bulk IP exposure (>10 IPs)
	DetectBulkIP bool `json:"detect_bulk_ip"`

	// BlockOnDetection blocks the response if leakage is detected
	BlockOnDetection bool `json:"block_on_detection"`

	// MinSeverityToBlock minimum severity to trigger blocking
	MinSeverityToBlock string `json:"min_severity_to_block"` // low, medium, high, critical

	// CustomPatterns additional regex patterns to detect
	CustomPatterns map[string]string `json:"custom_patterns"`

	// ExcludePaths paths to skip inspection
	ExcludePaths []string `json:"exclude_paths"`

	// ExcludeContentTypes content types to skip
	ExcludeContentTypes []string `json:"exclude_content_types"`
}

// DefaultConfig returns production-safe defaults
func DefaultConfig() *Config {
	return &Config{
		Enabled:              true,
		MaxBodySize:          1 * 1024 * 1024, // 1MB
		DetectSSN:            true,
		DetectCreditCard:     true,
		DetectAPIKeys:        true,
		DetectAWSKeys:        true,
		DetectPrivateKeys:    true,
		DetectJWT:            true,
		DetectPasswordFields: true,
		DetectBulkEmail:      true,
		DetectBulkIP:         false, // Can be noisy
		BlockOnDetection:     false, // Log only by default
		MinSeverityToBlock:   "high",
		ExcludePaths:         []string{"/health", "/metrics", "/favicon.ico"},
		ExcludeContentTypes:  []string{"image/", "video/", "audio/", "application/octet-stream"},
	}
}

// Compiled regex patterns
var (
	// SSN: XXX-XX-XXXX or XXXXXXXXX
	ssnPattern = regexp.MustCompile(`\b\d{3}[-\s]?\d{2}[-\s]?\d{4}\b`)

	// Credit card: 13-19 digits with optional separators
	creditCardPattern = regexp.MustCompile(`\b(?:\d{4}[-\s]?){3,4}\d{1,4}\b`)

	// Generic API key patterns
	apiKeyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(api[_-]?key|apikey)["\s:=]+["']?([a-zA-Z0-9_\-]{20,})["']?`),
		regexp.MustCompile(`(?i)(secret[_-]?key|secretkey)["\s:=]+["']?([a-zA-Z0-9_\-]{20,})["']?`),
		regexp.MustCompile(`(?i)(access[_-]?token)["\s:=]+["']?([a-zA-Z0-9_\-]{20,})["']?`),
	}

	// AWS Access Key ID: AKIA followed by 16 characters
	awsKeyPattern = regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`)

	// AWS Secret Key: 40 character base64-ish
	awsSecretPattern = regexp.MustCompile(`(?i)(aws[_-]?secret|secret[_-]?access[_-]?key)["\s:=]+["']?([A-Za-z0-9/+=]{40})["']?`)

	// PEM Private Key
	privateKeyPattern = regexp.MustCompile(`-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`)

	// JWT Token: 3 base64url segments
	jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

	// Password fields in JSON
	passwordFieldPattern = regexp.MustCompile(`(?i)["']?(password|passwd|pwd|secret|credential)["']?\s*[":=]\s*["']([^"']{1,})["']`)

	// Email addresses
	emailPattern = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`)

	// IP addresses (IPv4)
	ipPattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

	// Phone numbers (various formats)
	phonePattern = regexp.MustCompile(`\b(?:\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`)

	// Non-digit pattern for credit card validation
	nonDigitPattern = regexp.MustCompile(`\D`)
)

// Inspector performs response body inspection
type Inspector struct {
	mu       sync.RWMutex
	config   *Config
	customRe map[string]*regexp.Regexp
}

// NewInspector creates a new response body inspector
func NewInspector(config *Config) *Inspector {
	if config == nil {
		config = DefaultConfig()
	}

	i := &Inspector{
		config:   config,
		customRe: make(map[string]*regexp.Regexp),
	}

	// Compile custom patterns
	for name, pattern := range config.CustomPatterns {
		if re, err := regexp.Compile(pattern); err == nil {
			i.customRe[name] = re
		}
	}

	return i
}

// InspectionResult contains the result of body inspection
type InspectionResult struct {
	Inspected       bool         `json:"inspected"`
	Detections      []*Detection `json:"detections,omitempty"`
	ShouldBlock     bool         `json:"should_block"`
	HighestSeverity string       `json:"highest_severity"`
	TotalCount      int          `json:"total_count"`
	BodySize        int64        `json:"body_size"`
	Truncated       bool         `json:"truncated"`
}

// Inspect analyzes a response body for sensitive data
func (i *Inspector) Inspect(body []byte, contentType, path string) *InspectionResult {
	result := &InspectionResult{
		Detections: make([]*Detection, 0),
	}

	// Snapshot config under read lock to avoid race with SetConfig
	i.mu.RLock()
	cfg := i.config
	// Copy custom regex map under lock
	customRe := make(map[string]*regexp.Regexp, len(i.customRe))
	for k, v := range i.customRe {
		customRe[k] = v
	}
	i.mu.RUnlock()

	if !cfg.Enabled {
		return result
	}

	// Check exclusions
	if i.shouldExcludeCfg(cfg, contentType, path) {
		return result
	}

	result.Inspected = true
	result.BodySize = int64(len(body))

	// Truncate if too large
	if result.BodySize > cfg.MaxBodySize {
		body = body[:cfg.MaxBodySize]
		result.Truncated = true
	}

	// Convert to string for pattern matching
	content := string(body)

	// Run detectors
	if cfg.DetectSSN {
		i.detectPattern(content, ssnPattern, LeakageSSN, "critical", result)
	}

	if cfg.DetectCreditCard {
		i.detectCreditCard(content, result)
	}

	if cfg.DetectAPIKeys {
		for _, pattern := range apiKeyPatterns {
			i.detectPattern(content, pattern, LeakageAPIKey, "high", result)
		}
	}

	if cfg.DetectAWSKeys {
		i.detectPattern(content, awsKeyPattern, LeakageAWSKey, "critical", result)
		i.detectPattern(content, awsSecretPattern, LeakageAWSKey, "critical", result)
	}

	if cfg.DetectPrivateKeys {
		i.detectPattern(content, privateKeyPattern, LeakagePrivateKey, "critical", result)
	}

	if cfg.DetectJWT {
		i.detectPattern(content, jwtPattern, LeakageJWT, "medium", result)
	}

	if cfg.DetectPasswordFields {
		i.detectPattern(content, passwordFieldPattern, LeakagePassword, "high", result)
	}

	if cfg.DetectBulkEmail {
		i.detectBulk(content, emailPattern, LeakageEmail, 5, "medium", result)
	}

	if cfg.DetectBulkIP {
		i.detectBulk(content, ipPattern, LeakageIPAddress, 10, "low", result)
	}

	// Custom patterns
	for name, re := range customRe {
		det := &Detection{
			Type:     LeakageCustom,
			Pattern:  name,
			Severity: "medium",
		}
		matches := re.FindAllString(content, -1)
		if len(matches) > 0 {
			det.Count = len(matches)
			if len(matches) > 0 {
				det.Redacted = redact(matches[0])
			}
			result.Detections = append(result.Detections, det)
		}
	}

	// Calculate totals and severity
	for _, det := range result.Detections {
		result.TotalCount += det.Count
		if severityLevel(det.Severity) > severityLevel(result.HighestSeverity) {
			result.HighestSeverity = det.Severity
		}
	}

	// Determine if we should block
	if cfg.BlockOnDetection && len(result.Detections) > 0 {
		if severityLevel(result.HighestSeverity) >= severityLevel(cfg.MinSeverityToBlock) {
			result.ShouldBlock = true
		}
	}

	return result
}

// detectPattern runs a single pattern and records detections
func (i *Inspector) detectPattern(content string, pattern *regexp.Regexp, leakType LeakageType, severity string, result *InspectionResult) {
	matches := pattern.FindAllString(content, -1)
	if len(matches) > 0 {
		det := &Detection{
			Type:     leakType,
			Pattern:  pattern.String()[:min(50, len(pattern.String()))],
			Count:    len(matches),
			Severity: severity,
			Redacted: redact(matches[0]),
		}
		result.Detections = append(result.Detections, det)
	}
}

// detectCreditCard detects and validates credit card numbers
func (i *Inspector) detectCreditCard(content string, result *InspectionResult) {
	matches := creditCardPattern.FindAllString(content, -1)
	validCount := 0
	var sample string

	for _, match := range matches {
		// Remove separators
		digits := nonDigitPattern.ReplaceAllString(match, "")
		if len(digits) >= 13 && len(digits) <= 19 && luhnCheck(digits) {
			validCount++
			if sample == "" {
				sample = match
			}
		}
	}

	if validCount > 0 {
		det := &Detection{
			Type:     LeakageCreditCard,
			Pattern:  "credit_card",
			Count:    validCount,
			Severity: "critical",
			Redacted: redact(sample),
		}
		result.Detections = append(result.Detections, det)
	}
}

// detectBulk detects bulk exposure of data
func (i *Inspector) detectBulk(content string, pattern *regexp.Regexp, leakType LeakageType, threshold int, severity string, result *InspectionResult) {
	matches := pattern.FindAllString(content, -1)
	if len(matches) >= threshold {
		det := &Detection{
			Type:     leakType,
			Pattern:  "bulk_exposure",
			Count:    len(matches),
			Severity: severity,
			Redacted: redact(matches[0]) + fmt.Sprintf(" (and %d more)", len(matches)-1),
		}
		result.Detections = append(result.Detections, det)
	}
}

// shouldExcludeCfg checks if the request should be excluded from inspection using the given config.
func (i *Inspector) shouldExcludeCfg(cfg *Config, contentType, path string) bool {
	// Check content type
	for _, ct := range cfg.ExcludeContentTypes {
		if strings.HasPrefix(contentType, ct) {
			return true
		}
	}

	// Check path
	for _, p := range cfg.ExcludePaths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	return false
}

// luhnCheck validates a credit card number using Luhn algorithm
func luhnCheck(number string) bool {
	sum := 0
	isSecond := false

	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')

		if isSecond {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}

		sum += d
		isSecond = !isSecond
	}

	return sum%10 == 0
}

// severityLevel converts severity string to numeric level
func severityLevel(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// redact masks sensitive data for safe logging
func redact(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// Helper
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ResponseWriter wraps http.ResponseWriter to capture and inspect response
type ResponseWriter struct {
	io.Writer
	buf         *bytes.Buffer
	statusCode  int
	wroteHeader bool
}

// GetConfig returns the current configuration
func (i *Inspector) GetConfig() *Config {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.config
}

// SetConfig updates the configuration
func (i *Inspector) SetConfig(config *Config) {
	i.mu.Lock()
	i.config = config

	// Recompile custom patterns
	i.customRe = make(map[string]*regexp.Regexp)
	for name, pattern := range config.CustomPatterns {
		if re, err := regexp.Compile(pattern); err == nil {
			i.customRe[name] = re
		}
	}
	i.mu.Unlock()
}
