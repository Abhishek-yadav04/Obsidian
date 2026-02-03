package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrWeakPassword       = errors.New("password does not meet security requirements")
	ErrTokenExpired       = errors.New("token has expired")
	ErrInvalidToken       = errors.New("invalid token")
)

// cachedSecretKey stores the JWT secret for the lifetime of the application
var cachedSecretKey string

// isDevelopmentMode tracks whether we're running in dev mode
var isDevelopmentMode bool

// SetDevelopmentMode enables/disables development mode for JWT secret handling
// In production, JWT secret MUST be set via OBSIDIAN_JWT_SECRET environment variable
func SetDevelopmentMode(dev bool) {
	isDevelopmentMode = dev
}

// getSecretKey retrieves JWT secret from environment
// SECURITY: In production mode, this will panic if OBSIDIAN_JWT_SECRET is not set
func getSecretKey() string {
	// Return cached key if already set
	if cachedSecretKey != "" {
		return cachedSecretKey
	}

	key := os.Getenv("OBSIDIAN_JWT_SECRET")
	if key == "" {
		if isDevelopmentMode {
			// Development only - use a stable default key
			// This ensures tokens remain valid for the application lifetime
			key = "obsidian-development-secret-key-change-in-production"
			fmt.Println("⚠️  WARNING: Using default JWT secret (DEVELOPMENT MODE ONLY)")
		} else {
			// PRODUCTION: FAIL HARD - no default secrets
			panic("FATAL: OBSIDIAN_JWT_SECRET environment variable not set. This is required in production mode.")
		}
	}

	// Validate minimum key length (256 bits = 32 bytes minimum)
	if len(key) < 32 {
		if !isDevelopmentMode {
			panic("FATAL: OBSIDIAN_JWT_SECRET must be at least 32 characters for production security")
		}
		fmt.Println("⚠️  WARNING: JWT secret is less than 32 characters - NOT SECURE FOR PRODUCTION")
	}

	// Cache the key for consistent signing/verification
	cachedSecretKey = key
	return cachedSecretKey
}

// HashPassword generates a bcrypt hash of the password with cost 12
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword compares a bcrypt hash with a plaintext password
func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// GenerateToken creates a cryptographically secure random token
func GenerateToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// HashToken creates a SHA-256 hash of a token for secure database storage
// This is NOT for passwords (use bcrypt for that), but for session tokens
// where we need fast comparison and don't need protection against offline attacks
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return base64.URLEncoding.EncodeToString(hash[:])
}

// GenerateJWT creates a properly signed JWT token with HMAC-SHA256
func GenerateJWT(userID int, username, role string, duration time.Duration) (string, error) {
	secret := getSecretKey()

	now := time.Now()
	exp := now.Add(duration)

	// JWT Header
	header := `{"alg":"HS256","typ":"JWT"}`

	// JWT Payload with standard claims
	payload := fmt.Sprintf(`{"user_id":%d,"username":"%s","role":"%s","iat":%d,"exp":%d,"iss":"obsidian-waf","aud":"obsidian-ui"}`,
		userID, username, role, now.Unix(), exp.Unix())

	// Base64URL encode header and payload
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(payload))

	// Create signature using HMAC-SHA256
	signingInput := headerB64 + "." + payloadB64
	signature := signHS256(signingInput, secret)

	return signingInput + "." + signature, nil
}

// signHS256 creates HMAC-SHA256 signature
func signHS256(data, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Claims represents JWT token claims
type Claims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
	Iss      string `json:"iss"`
	Aud      string `json:"aud"`
}

// VerifyJWT validates a JWT token signature and returns claims
func VerifyJWT(tokenString string) (*Claims, error) {
	secret := getSecretKey()

	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	expectedSig := signHS256(signingInput, secret)

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, ErrInvalidToken
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}

	// Parse claims
	var claims Claims
	if err := parseJSONClaims(payloadBytes, &claims); err != nil {
		return nil, ErrInvalidToken
	}

	// Check expiration
	if claims.Exp < time.Now().Unix() {
		return nil, ErrTokenExpired
	}

	return &claims, nil
}

// parseJSONClaims manually parses JSON claims without using encoding/json (for simplicity)
func parseJSONClaims(data []byte, claims *Claims) error {
	// Simple JSON parsing using encoding/json
	type jsonClaims struct {
		UserID   int    `json:"user_id"`
		Username string `json:"username"`
		Role     string `json:"role"`
		Iat      int64  `json:"iat"`
		Exp      int64  `json:"exp"`
		Iss      string `json:"iss"`
		Aud      string `json:"aud"`
	}

	var jc jsonClaims
	if err := json.Unmarshal(data, &jc); err != nil {
		return err
	}

	claims.UserID = jc.UserID
	claims.Username = jc.Username
	claims.Role = jc.Role
	claims.Iat = jc.Iat
	claims.Exp = jc.Exp
	claims.Iss = jc.Iss
	claims.Aud = jc.Aud

	return nil
}

// Common weak passwords to reject (partial list - add more as needed)
var commonPasswords = map[string]bool{
	"password": true, "password123": true, "12345678": true, "123456789": true,
	"qwerty": true, "admin": true, "admin123": true, "letmein": true,
	"welcome": true, "monkey": true, "dragon": true, "master": true,
	"obsidian": true, "obsidian123": true, "security": true, "trustno1": true,
}

// ValidatePassword checks if password meets production security requirements
// PRODUCTION REQUIREMENTS:
// - Minimum 12 characters (was 8 in demo)
// - Must contain: uppercase, lowercase, number, AND special character
// - Cannot be a common password
// - Cannot be username or email (caller should check this separately)
func ValidatePassword(password string) error {
	// PRODUCTION: 12 character minimum
	if len(password) < 12 {
		return fmt.Errorf("%w: password must be at least 12 characters", ErrWeakPassword)
	}

	// Check for common passwords
	if commonPasswords[strings.ToLower(password)] {
		return fmt.Errorf("%w: password is too common", ErrWeakPassword)
	}

	hasUpper := false
	hasLower := false
	hasNumber := false
	hasSpecial := false

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

	// PRODUCTION: Require ALL character types
	if !hasUpper {
		return fmt.Errorf("%w: password must contain at least one uppercase letter", ErrWeakPassword)
	}
	if !hasLower {
		return fmt.Errorf("%w: password must contain at least one lowercase letter", ErrWeakPassword)
	}
	if !hasNumber {
		return fmt.Errorf("%w: password must contain at least one number", ErrWeakPassword)
	}
	if !hasSpecial {
		return fmt.Errorf("%w: password must contain at least one special character (!@#$%%^&*...)", ErrWeakPassword)
	}

	return nil
}

// GenerateCSRFToken creates a CSRF protection token
func GenerateCSRFToken() (string, error) {
	return GenerateToken(32)
}

// ValidateCSRFToken validates a CSRF token against expected value
func ValidateCSRFToken(token, expected string) bool {
	return hmac.Equal([]byte(token), []byte(expected))
}
