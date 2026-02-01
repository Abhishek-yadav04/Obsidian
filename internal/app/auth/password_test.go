package auth

import (
	"os"
	"strings"
	"testing"
	"time"
)

const (
	testSecretValue      = "test-secret-1234567890"
	errGenerateJWTFormat = "generate jwt error: %v"
)

func resetSecretKey(t *testing.T, value string) {
	t.Helper()
	cachedSecretKey = ""
	if err := os.Setenv("OBSIDIAN_JWT_SECRET", value); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "too_short", password: "Ab12", wantErr: true},
		{name: "single_class_lower", password: "password", wantErr: true},
		{name: "single_class_upper", password: "PASSWORD", wantErr: true},
		{name: "single_class_number", password: "12345678", wantErr: true},
		{name: "upper_lower", password: "Password", wantErr: false},
		{name: "lower_number", password: "password1", wantErr: false},
		{name: "upper_number", password: "PASSWORD1", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q", tc.password)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.password, err)
			}
		})
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	password := "StrongPass1"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if err := VerifyPassword(hash, password); err != nil {
		t.Fatalf("verify error: %v", err)
	}
	if err := VerifyPassword(hash, "WrongPass1"); err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken(32)
	if err != nil {
		t.Fatalf("token error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if strings.Contains(token, "+") || strings.Contains(token, "/") {
		t.Fatal("expected base64url-encoded token")
	}
}

func TestJWTGenerateVerify(t *testing.T) {
	resetSecretKey(t, testSecretValue)
	token, err := GenerateJWT(1, "admin", "Admin", time.Minute)
	if err != nil {
		t.Fatalf(errGenerateJWTFormat, err)
	}

	claims, err := VerifyJWT(token)
	if err != nil {
		t.Fatalf("verify jwt error: %v", err)
	}
	if claims.UserID != 1 || claims.Username != "admin" || claims.Role != "Admin" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if claims.Exp <= claims.Iat {
		t.Fatalf("expected exp > iat: exp=%d iat=%d", claims.Exp, claims.Iat)
	}
}

func TestJWTExpired(t *testing.T) {
	resetSecretKey(t, testSecretValue)
	token, err := GenerateJWT(1, "admin", "Admin", -time.Minute)
	if err != nil {
		t.Fatalf(errGenerateJWTFormat, err)
	}
	if _, err := VerifyJWT(token); err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got: %v", err)
	}
}

func TestJWTInvalidSignature(t *testing.T) {
	resetSecretKey(t, testSecretValue)
	token, err := GenerateJWT(1, "admin", "Admin", time.Minute)
	if err != nil {
		t.Fatalf(errGenerateJWTFormat, err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected token format: %s", token)
	}
	parts[2] = "tampered"
	bad := strings.Join(parts, ".")
	if _, err := VerifyJWT(bad); err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got: %v", err)
	}
}

func TestCSRFTokenValidation(t *testing.T) {
	resetSecretKey(t, testSecretValue)
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("csrf token error: %v", err)
	}
	if !ValidateCSRFToken(token, token) {
		t.Fatal("expected csrf token to validate")
	}
	if ValidateCSRFToken(token, token+"x") {
		t.Fatal("expected csrf token mismatch to fail")
	}
}
