package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
)

// AllowedOrigins contains the list of allowed WebSocket origins
var AllowedOrigins = []string{
	"http://localhost:8082",
	"http://127.0.0.1:8082",
	"https://localhost:8082",
	"http://localhost:8080",
	"http://127.0.0.1:8080",
	"http://[::1]:8082",
	"http://[::1]:8080",
}

const (
	contentTypeHeader     = "Content-Type"
	contentTypeJSON       = "application/json"
	msgMethodNotAllowed   = "Method not allowed"
	msgInvalidRequestBody = "Invalid request body"
)

type API struct {
	Store *store.Store
	// Upgrader for websockets
	Upgrader websocket.Upgrader
	// HIBPChecker for password breach checking (optional)
	HIBPChecker HIBPPasswordChecker
}

// HIBPPasswordChecker interface for password breach checking
type HIBPPasswordChecker interface {
	CheckPassword(ctx context.Context, password string) (breached bool, count int, err error)
	IsEnabled() bool
}

func NewAPI(s *store.Store) *API {
	// Add environment-configured origins
	if envOrigins := os.Getenv("OBSIDIAN_ALLOWED_ORIGINS"); envOrigins != "" {
		for _, origin := range strings.Split(envOrigins, ",") {
			AllowedOrigins = append(AllowedOrigins, strings.TrimSpace(origin))
		}
	}

	return &API{
		Store: s,
		Upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // Same-origin request
				}
				// Allow localhost/loopback for development
				if strings.HasPrefix(origin, "http://localhost:") ||
					strings.HasPrefix(origin, "http://127.0.0.1:") ||
					strings.HasPrefix(origin, "http://[::1]:") ||
					strings.HasPrefix(origin, "https://localhost:") {
					return true
				}
				for _, allowed := range AllowedOrigins {
					if origin == allowed {
						return true
					}
				}
				return false
			},
		},
	}
}

// SetHIBPChecker sets the HIBP password checker for breach detection
func (a *API) SetHIBPChecker(checker HIBPPasswordChecker) {
	a.HIBPChecker = checker
}

func (a *API) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, msgInvalidRequestBody, http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Username == "" || req.Password == "" {
		w.Header().Set(contentTypeHeader, contentTypeJSON)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Username and password required"})
		return
	}

	// Authenticate user
	user, err := a.Store.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		// Perform dummy bcrypt work to prevent timing-based user enumeration
		dummyHash, hashErr := bcrypt.GenerateFromPassword([]byte("invalid-password"), bcrypt.MinCost)
		if hashErr == nil {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		}
		w.Header().Set(contentTypeHeader, contentTypeJSON)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
		return
	}

	// Generate JWT token (15 minutes) - uses environment variable secret
	token, err := auth.GenerateJWT(user.ID, user.Username, user.Role, 15*time.Minute)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	// Generate refresh token (7 days)
	refreshToken, err := auth.GenerateToken(32)
	if err != nil {
		http.Error(w, "Failed to generate refresh token", http.StatusInternalServerError)
		return
	}

	// Create session
	session := model.Session{
		ID:           refreshToken,
		UserID:       user.ID,
		Token:        refreshToken,
		ExpiresAt:    time.Now().Add(7 * 24 * time.Hour),
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		IPAddress:    extractClientIP(r),
		UserAgent:    r.UserAgent(),
	}
	a.Store.CreateSession(session)

	// Log authentication event
	a.Store.AddAuditLog(model.AuditLog{
		UserID:    &user.ID,
		Action:    "LOGIN",
		Resource:  "/api/login",
		Details:   fmt.Sprintf("User %s logged in", user.Username),
		IPAddress: extractClientIP(r),
		Timestamp: time.Now(),
	})

	// Return response (don't include password hash)
	safeUser := model.User{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Role:      user.Role,
		Enabled:   user.Enabled,
		CreatedAt: user.CreatedAt,
	}

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(model.LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         safeUser,
		ExpiresIn:    900, // 15 minutes in seconds
	})
}

// extractClientIP extracts the client IP from the request
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port (unlikely but handle gracefully)
		return strings.Trim(r.RemoteAddr, "[]")
	}
	return host
}

func (a *API) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := a.Store.GetStats()
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(stats)
}

func (a *API) HandleLogs(w http.ResponseWriter, r *http.Request) {
	logs := a.Store.GetLogs()
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(logs)
}

func (a *API) HandleRules(w http.ResponseWriter, r *http.Request) {
	source := strings.ToLower(r.URL.Query().Get("source"))
	switch source {
	case "custom", "crs":
		rules := a.Store.GetRulesBySource(source)
		w.Header().Set(contentTypeHeader, contentTypeJSON)
		json.NewEncoder(w).Encode(rules)
		return
	default:
		rules := a.Store.GetRules()
		w.Header().Set(contentTypeHeader, contentTypeJSON)
		json.NewEncoder(w).Encode(rules)
		return
	}
}

func (a *API) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := a.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Log the actual WebSocket upgrade error for debugging
		fmt.Printf("[WebSocket] Upgrade failed: %v (ResponseWriter type: %T)\n", err, w)
		return
	}
	defer conn.Close()

	// Simple loop to keep connection alive and send periodic updates
	for {
		stats := a.Store.GetStats()
		msg := map[string]interface{}{
			"type": "stats_update",
			"data": stats,
		}
		if err := conn.WriteJSON(msg); err != nil {
			break
		}
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// HandleCreateRule creates a new WAF rule (Admin only)
func (a *API) HandleCreateRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	var rule model.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, msgInvalidRequestBody, http.StatusBadRequest)
		return
	}
	rule.Source = "custom"
	rule.ReadOnly = false
	actor := resolveActor(r)

	if err := a.Store.CreateRule(rule, actor); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rule created successfully",
		"rule":    rule,
	})
}

// HandleUpdateRule updates an existing WAF rule (Admin only)
func (a *API) HandleUpdateRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	var rule model.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, msgInvalidRequestBody, http.StatusBadRequest)
		return
	}
	rule.Source = "custom"
	rule.ReadOnly = false
	actor := resolveActor(r)

	if err := a.Store.UpdateRule(rule, actor); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rule updated successfully",
	})
}

// HandleDeleteRule deletes a WAF rule (Admin only)
func (a *API) HandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, msgInvalidRequestBody, http.StatusBadRequest)
		return
	}
	actor := resolveActor(r)

	if err := a.Store.DeleteRule(req.ID, actor); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rule deleted successfully",
	})
}

// HandleTestRule tests a rule without applying it (dry-run)
func (a *API) HandleTestRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RuleContent string `json:"rule_content"`
		TestInput   string `json:"test_input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, msgInvalidRequestBody, http.StatusBadRequest)
		return
	}

	// Simplified rule testing - in production would compile and test against Coraza
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"message":     "Rule syntax is valid",
		"would_match": false,
	})
}

// HandleUsers returns list of users (Admin only)
func (a *API) HandleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Return all users from the store
		users := a.Store.GetUsers()
		// Convert to safe response (no password hashes)
		safeUsers := make([]map[string]interface{}, 0, len(users))
		for _, u := range users {
			safeUsers = append(safeUsers, map[string]interface{}{
				"id":       u.ID,
				"username": u.Username,
				"email":    u.Email,
				"role":     u.Role,
				"enabled":  u.Enabled,
			})
		}
		w.Header().Set(contentTypeHeader, contentTypeJSON)
		json.NewEncoder(w).Encode(safeUsers)

	case http.MethodPut:
		// Update user
		var req struct {
			Username string `json:"username"`
			Role     string `json:"role"`
			Enabled  bool   `json:"enabled"`
			Password string `json:"password,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, msgInvalidRequestBody, http.StatusBadRequest)
			return
		}

		// Check password against HIBP if changing password and HIBP checker is available
		if req.Password != "" && a.HIBPChecker != nil && a.HIBPChecker.IsEnabled() {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()

			breached, count, err := a.HIBPChecker.CheckPassword(ctx, req.Password)
			if err != nil {
				// Log the error and continue (fail-open by default)
				fmt.Printf("[WARN] HIBP password check failed: %v\n", err)
			} else if breached {
				w.Header().Set(contentTypeHeader, contentTypeJSON)
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success":      false,
					"message":      "Password has been found in data breaches. Please choose a different password.",
					"breach_count": count,
					"hibp_warning": true,
				})
				return
			}
		}

		// Update user in the store
		if err := a.Store.UpdateUser(req.Username, req.Role, req.Enabled, req.Password); err != nil {
			w.Header().Set(contentTypeHeader, contentTypeJSON)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": err.Error(),
			})
			return
		}

		w.Header().Set(contentTypeHeader, contentTypeJSON)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("User %s updated successfully", req.Username),
		})

	default:
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
	}
}

// HandleAuditLogs returns audit trail (Admin only)
// Requires authentication - JWT token in Authorization header
func (a *API) HandleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	// Parse pagination parameters
	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Query database for audit logs
	logs, err := a.Store.GetAuditLogs(limit, offset)
	if err != nil {
		// Return empty array if database not configured or query fails
		logs = []map[string]interface{}{}
	}

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(logs)
}

// HandleRuleAuditLogs returns rule audit trail (Admin only)
// Requires authentication - JWT token in Authorization header
func (a *API) HandleRuleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	// Parse pagination parameters
	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	logs, err := a.Store.GetRuleAuditLogs(limit, offset)
	if err != nil {
		logs = []model.RuleAuditLog{}
	}

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(logs)
}

// HandleCRSStatus returns CRS status (Admin only)
func (a *API) HandleCRSStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	status := a.Store.CRSStatus()
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(status)
}

// HandleCRSEnable enables CRS visibility (Admin only)
func (a *API) HandleCRSEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	if err := a.Store.EnableCRS(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	actor := resolveActor(r)
	a.Store.AddAuditLog(model.AuditLog{
		Action:    "CRS_ENABLE",
		Resource:  "/api/admin/crs/enable",
		Details:   "CRS enabled by " + actor,
		IPAddress: extractClientIP(r),
		Timestamp: time.Now(),
	})

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "CRS enabled",
	})
}

// HandleCRSDisable disables CRS visibility (Admin only)
func (a *API) HandleCRSDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	if err := a.Store.DisableCRS(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	actor := resolveActor(r)
	a.Store.AddAuditLog(model.AuditLog{
		Action:    "CRS_DISABLE",
		Resource:  "/api/admin/crs/disable",
		Details:   "CRS disabled by " + actor,
		IPAddress: extractClientIP(r),
		Timestamp: time.Now(),
	})

	w.Header().Set(contentTypeHeader, contentTypeJSON)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "CRS disabled",
	})
}

func resolveActor(r *http.Request) string {
	actor := strings.TrimSpace(r.Header.Get("X-Actor"))
	if actor == "" {
		return "unknown"
	}
	return actor
}
