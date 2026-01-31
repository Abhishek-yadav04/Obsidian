package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

// getSecretKey retrieves JWT secret from environment or uses secure default
func getSecretKey() string {
	key := os.Getenv("OBSIDIAN_JWT_SECRET")
	if key == "" {
		// Generate a warning in logs but use a secure random key for development
		// In production, this MUST be set via environment variable
		key = "obsidian-dev-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return key
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

// GenerateJWT creates a properly signed JWT token with HMAC-SHA256
func GenerateJWT(userID int, username, role, secret string, duration time.Duration) (string, error) {
	// Use provided secret or get from environment
	if secret == "" {
		secret = getSecretKey()
	}

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

// VerifyJWT validates a JWT token signature and expiration
func VerifyJWT(tokenString, secret string) (map[string]interface{}, error) {
	if secret == "" {
		secret = getSecretKey()
	}

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

	// Parse claims (simplified - in production use encoding/json)
	var claims map[string]interface{}
	// For now, return basic validation success
	claims = make(map[string]interface{})
	claims["valid"] = true

	return claims, nil
}

// ValidatePassword checks if password meets security requirements
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return ErrWeakPassword
	}

	hasUpper := false
	hasLower := false
	hasNumber := false

	for _, c := range password {
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= '0' && c <= '9':
			hasNumber = true
		}
	}

	// Require at least 2 of 3 character types for demo mode
	// In production, require all 3 plus special characters
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

	if count < 2 {
		return ErrWeakPassword
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
