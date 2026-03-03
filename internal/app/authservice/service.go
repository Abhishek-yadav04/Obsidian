// Package authservice provides secure authentication for Obsidian WAF.
// This implementation integrates the security manager with the persistence layer
// to provide enterprise-grade authentication without hardcoded credentials.
package authservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/corazawaf/coraza/v3/internal/app/persistence"
	"github.com/corazawaf/coraza/v3/internal/app/security"
)

// Logger interface for authentication logging
type Logger interface {
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
	LogAuth(username, clientIP, result string, success bool)
}

// Errors for authentication operations
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserDisabled       = errors.New("user account is disabled")
	ErrPasswordExpired    = errors.New("password has expired, please reset")
	ErrTooManyAttempts    = errors.New("too many failed attempts, account locked")
	ErrSessionExpired     = errors.New("session has expired")
	ErrInvalidSession     = errors.New("invalid session")
	ErrWeakPassword       = errors.New("password does not meet security requirements")
	ErrPasswordReused     = errors.New("cannot reuse recent passwords")
)

// Config for authentication service
type Config struct {
	// TokenExpiry for access tokens
	TokenExpiry time.Duration

	// RefreshTokenExpiry for refresh tokens
	RefreshTokenExpiry time.Duration

	// MaxFailedAttempts before account lockout
	MaxFailedAttempts int

	// LockoutDuration for failed attempts
	LockoutDuration time.Duration

	// PasswordExpiryDays (0 = no expiry)
	PasswordExpiryDays int

	// RequirePasswordChange on first login
	RequirePasswordChange bool

	// SessionInactivityTimeout
	SessionInactivityTimeout time.Duration

	// MaxConcurrentSessions per user (0 = unlimited)
	MaxConcurrentSessions int
}

// DefaultConfig returns secure defaults
func DefaultConfig() Config {
	return Config{
		TokenExpiry:              15 * time.Minute,
		RefreshTokenExpiry:       7 * 24 * time.Hour,
		MaxFailedAttempts:        5,
		LockoutDuration:          15 * time.Minute,
		PasswordExpiryDays:       90,
		RequirePasswordChange:    true,
		SessionInactivityTimeout: 30 * time.Minute,
		MaxConcurrentSessions:    3,
	}
}

// LoginAttempt tracks login attempts for rate limiting
type LoginAttempt struct {
	Username   string
	IPAddress  string
	Timestamp  time.Time
	Successful bool
	FailReason string
}

// AuthResult contains authentication result details
type AuthResult struct {
	User                   *model.User
	AccessToken            string
	RefreshToken           string
	ExpiresIn              int64 // seconds
	SessionID              string
	RequiresMFA            bool
	RequiresPasswordChange bool
}

// Service provides secure authentication
type Service struct {
	store    persistence.Store
	security *security.Manager
	logger   Logger
	config   Config

	// mu protects attempts and lockedUsers maps from concurrent access
	mu sync.RWMutex

	// In-memory tracking for login attempts (could be Redis-backed in production)
	attempts    map[string][]LoginAttempt // username -> attempts
	lockedUsers map[string]time.Time      // username -> locked until
}

// NewService creates a new authentication service
func NewService(store persistence.Store, secMgr *security.Manager, logger Logger, cfg Config) *Service {
	return &Service{
		store:       store,
		security:    secMgr,
		logger:      logger,
		config:      cfg,
		attempts:    make(map[string][]LoginAttempt),
		lockedUsers: make(map[string]time.Time),
	}
}

// Authenticate validates credentials and returns tokens
func (s *Service) Authenticate(ctx context.Context, username, password, clientIP, userAgent string) (*AuthResult, error) {
	// Check if account is locked
	s.mu.Lock()
	if lockedUntil, locked := s.lockedUsers[username]; locked {
		if time.Now().Before(lockedUntil) {
			s.mu.Unlock()
			s.recordAttempt(username, clientIP, false, "account_locked")
			s.logger.LogAuth(username, clientIP, "account_locked", false)
			return nil, ErrTooManyAttempts
		}
		// Lockout expired, remove
		delete(s.lockedUsers, username)
	}
	s.mu.Unlock()

	// Get user from database
	user, err := s.store.GetUser(ctx, username)
	if err != nil {
		if errors.Is(err, persistence.ErrUserNotFound) {
			s.recordAttempt(username, clientIP, false, "user_not_found")
			s.logger.LogAuth(username, clientIP, "invalid_credentials", false)
			// Don't reveal that user doesn't exist
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Check if user is enabled
	if !user.Enabled {
		s.recordAttempt(username, clientIP, false, "user_disabled")
		s.logger.LogAuth(username, clientIP, "user_disabled", false)
		return nil, ErrUserDisabled
	}

	// Verify password using bcrypt
	if err := auth.VerifyPassword(user.PasswordHash, password); err != nil {
		s.recordAttempt(username, clientIP, false, "invalid_password")
		s.logger.LogAuth(username, clientIP, "invalid_password", false)

		// Check if we should lock the account
		s.checkLockout(username)

		return nil, ErrInvalidCredentials
	}

	// Successful authentication
	s.recordAttempt(username, clientIP, true, "")
	s.logger.LogAuth(username, clientIP, "success", true)

	// Clear failed attempts on success
	s.mu.Lock()
	delete(s.attempts, username)
	s.mu.Unlock()

	// Ensure JWT secret is initialized via the security manager
	_ = s.security.GetJWTSecret()

	accessToken, err := auth.GenerateJWT(user.ID, user.Username, user.Role, s.config.TokenExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	refreshToken, err := auth.GenerateToken(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	// Create session with cryptographically random session ID
	sessBytes := make([]byte, 16)
	if _, err := rand.Read(sessBytes); err != nil {
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}
	sessionID := "sess-" + hex.EncodeToString(sessBytes)
	session := &model.Session{
		ID:           sessionID,
		UserID:       user.ID,
		Token:        refreshToken,
		ExpiresAt:    time.Now().Add(s.config.RefreshTokenExpiry),
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		IPAddress:    clientIP,
		UserAgent:    userAgent,
	}

	// Enforce max concurrent sessions
	if s.config.MaxConcurrentSessions > 0 {
		// This would clean up old sessions in a real implementation
	}

	if err := s.store.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Update last login
	now := time.Now()
	user.LastLogin = &now
	if err := s.store.UpdateUser(ctx, user); err != nil {
		s.logger.Warn("failed to update last login",
			zap.Int("user_id", user.ID),
		)
	}

	// Add audit log
	s.addAuditLog(ctx, user.ID, "login", "session", fmt.Sprintf("User logged in from %s", clientIP), clientIP)

	// Check if password change is required
	requiresChange := false
	if s.config.PasswordExpiryDays > 0 {
		// Check password age (would need password_changed_at field in user model)
	}

	return &AuthResult{
		User:                   user,
		AccessToken:            accessToken,
		RefreshToken:           refreshToken,
		ExpiresIn:              int64(s.config.TokenExpiry.Seconds()),
		SessionID:              sessionID,
		RequiresMFA:            false, // MFA not implemented yet
		RequiresPasswordChange: requiresChange,
	}, nil
}

// RefreshToken generates new tokens using a valid refresh token
func (s *Service) RefreshToken(ctx context.Context, refreshToken, clientIP string) (*AuthResult, error) {
	// In a real implementation, look up the session by refresh token
	// For now, just validate the token format
	if len(refreshToken) < 32 {
		return nil, ErrInvalidSession
	}

	// This would look up the session and validate it
	// session, err := s.store.GetSessionByToken(ctx, refreshToken)

	return nil, errors.New("refresh token validation not fully implemented")
}

// Logout invalidates a session
func (s *Service) Logout(ctx context.Context, sessionID string, userID int, clientIP string) error {
	if err := s.store.DeleteSession(ctx, sessionID); err != nil {
		s.logger.Warn("failed to delete session")
	}

	s.addAuditLog(ctx, userID, "logout", "session", "User logged out", clientIP)
	s.logger.LogAuth(fmt.Sprintf("user_%d", userID), clientIP, "logout", true)

	return nil
}

// LogoutAll invalidates all sessions for a user
func (s *Service) LogoutAll(ctx context.Context, userID int, clientIP string) error {
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("failed to delete sessions: %w", err)
	}

	s.addAuditLog(ctx, userID, "logout_all", "session", "All sessions terminated", clientIP)

	return nil
}

// ChangePassword changes user password with validation
func (s *Service) ChangePassword(ctx context.Context, userID int, currentPassword, newPassword, clientIP string) error {
	// Get user by ID
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, persistence.ErrUserNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Verify current password
	if err := auth.VerifyPassword(user.PasswordHash, currentPassword); err != nil {
		return ErrInvalidCredentials
	}

	// Validate new password using security manager
	if err := s.security.ValidatePassword(newPassword); err != nil {
		return fmt.Errorf("%w: %w", ErrWeakPassword, err)
	}

	// Hash new password
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Update user
	user.PasswordHash = newHash
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	// Invalidate all existing sessions
	_ = s.store.DeleteUserSessions(ctx, userID)

	s.addAuditLog(ctx, userID, "password_change", "user", "Password changed", clientIP)

	return nil
}

// CreateUser creates a new user with proper password validation
func (s *Service) CreateUser(ctx context.Context, username, password, email, role string, creatorID int, clientIP string) (*model.User, error) {
	// Validate password
	if err := s.security.ValidatePassword(password); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWeakPassword, err)
	}

	// Hash password
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &model.User{
		Username:     username,
		PasswordHash: hash,
		Email:        email,
		Role:         role,
		Enabled:      true,
		CreatedAt:    time.Now(),
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	s.addAuditLog(ctx, creatorID, "create_user", "user", fmt.Sprintf("Created user %s", username), clientIP)

	return user, nil
}

// recordAttempt records a login attempt
func (s *Service) recordAttempt(username, clientIP string, success bool, reason string) {
	attempt := LoginAttempt{
		Username:   username,
		IPAddress:  clientIP,
		Timestamp:  time.Now(),
		Successful: success,
		FailReason: reason,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.attempts[username] = append(s.attempts[username], attempt)

	// Keep only recent attempts (last hour)
	cutoff := time.Now().Add(-time.Hour)
	var recent []LoginAttempt
	for _, a := range s.attempts[username] {
		if a.Timestamp.After(cutoff) {
			recent = append(recent, a)
		}
	}
	s.attempts[username] = recent
}

// checkLockout checks if account should be locked
func (s *Service) checkLockout(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	attempts := s.attempts[username]

	// Count recent failed attempts
	cutoff := time.Now().Add(-s.config.LockoutDuration)
	failCount := 0
	for _, a := range attempts {
		if !a.Successful && a.Timestamp.After(cutoff) {
			failCount++
		}
	}

	if failCount >= s.config.MaxFailedAttempts {
		s.lockedUsers[username] = time.Now().Add(s.config.LockoutDuration)
		s.logger.Warn("account locked due to failed attempts",
			zap.String("username", username),
			zap.Int("failed_count", failCount),
		)
	}
}

// addAuditLog adds an audit log entry
func (s *Service) addAuditLog(ctx context.Context, userID int, action, resource, details, clientIP string) {
	log := &model.AuditLog{
		UserID:    &userID,
		Action:    action,
		Resource:  resource,
		Details:   details,
		IPAddress: clientIP,
		Timestamp: time.Now(),
	}

	if err := s.store.AddAuditLog(ctx, log); err != nil {
		s.logger.Warn("failed to add audit log",
			zap.String("action", action),
			zap.Int("user_id", userID),
		)
	}
}

// ValidateSession checks if a session is valid
func (s *Service) ValidateSession(ctx context.Context, sessionID string) (*model.Session, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, ErrInvalidSession
	}

	// Check expiration
	if time.Now().After(session.ExpiresAt) {
		_ = s.store.DeleteSession(ctx, sessionID)
		return nil, ErrSessionExpired
	}

	// Check inactivity
	if time.Since(session.LastActivity) > s.config.SessionInactivityTimeout {
		_ = s.store.DeleteSession(ctx, sessionID)
		return nil, ErrSessionExpired
	}

	// Refresh last activity timestamp
	session.LastActivity = time.Now()
	if err := s.store.UpdateSession(ctx, session); err != nil {
		s.logger.Warn("failed to refresh session activity",
			zap.String("session_id", sessionID),
		)
	}

	return session, nil
}

// GetLoginAttempts returns recent login attempts for monitoring
func (s *Service) GetLoginAttempts(username string) []LoginAttempt {
	s.mu.RLock()
	defer s.mu.RUnlock()

	src := s.attempts[username]
	result := make([]LoginAttempt, len(src))
	copy(result, src)
	return result
}

// IsAccountLocked checks if an account is locked
func (s *Service) IsAccountLocked(username string) (bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lockedUntil, locked := s.lockedUsers[username]; locked {
		if time.Now().Before(lockedUntil) {
			return true, lockedUntil
		}
		delete(s.lockedUsers, username)
	}
	return false, time.Time{}
}

// UnlockAccount manually unlocks an account (admin function)
func (s *Service) UnlockAccount(ctx context.Context, username string, adminID int, clientIP string) error {
	delete(s.lockedUsers, username)
	delete(s.attempts, username)

	s.addAuditLog(ctx, adminID, "unlock_account", "user", fmt.Sprintf("Unlocked account %s", username), clientIP)

	return nil
}
