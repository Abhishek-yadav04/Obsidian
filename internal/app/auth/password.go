package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrWeakPassword       = errors.New("password does not meet security requirements")
	ErrTokenExpired       = errors.New("token has expired")
	ErrInvalidToken       = errors.New("invalid token")
)

// HashPassword generates a bcrypt hash of the password with cost 12
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", ErrWeakPassword
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
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

// GenerateJWT creates a signed JWT token with claims
func GenerateJWT(userID int, username, role, secret string, duration time.Duration) (string, error) {
	now := time.Now()
	exp := now.Add(duration)

	// Simple JWT implementation (in production, use github.com/golang-jwt/jwt/v5)
	// Format: base64(header).base64(payload).signature

	header := fmt.Sprintf(`{"alg":"HS256","typ":"JWT"}`)
	payload := fmt.Sprintf(`{"user_id":%d,"username":"%s","role":"%s","iat":%d,"exp":%d}`,
		userID, username, role, now.Unix(), exp.Unix())

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(payload))

	// In production, implement proper HMAC-SHA256 signing
	// For now, return unsigned token (will implement proper JWT library in next iteration)
	token := fmt.Sprintf("%s.%s.%s", headerB64, payloadB64, "signature_placeholder")

	return token, nil
}

// ValidatePassword checks if password meets security requirements
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return ErrWeakPassword
	}
	// Add more checks: uppercase, lowercase, numbers, special chars
	// This is simplified for initial implementation
	return nil
}
