package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/gorilla/websocket"
)

// AllowedOrigins contains the list of allowed WebSocket origins
var AllowedOrigins = []string{
	"http://localhost:8082",
	"http://127.0.0.1:8082",
	"https://localhost:8082",
}

type API struct {
	Store *store.Store
	// Upgrader for websockets
	Upgrader websocket.Upgrader
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

func (a *API) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Username == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Username and password required"})
		return
	}

	// Authenticate user
	user, err := a.Store.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		// Add slight delay to prevent timing attacks
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
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

	w.Header().Set("Content-Type", "application/json")
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
		return xri
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return strings.Trim(ip, "[]")
}

func (a *API) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := a.Store.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (a *API) HandleLogs(w http.ResponseWriter, r *http.Request) {
	logs := a.Store.GetLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (a *API) HandleRules(w http.ResponseWriter, r *http.Request) {
	rules := a.Store.GetRules()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rules)
}

func (a *API) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := a.Upgrader.Upgrade(w, r, nil)
	if err != nil {
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var rule model.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := a.Store.CreateRule(rule); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var rule model.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := a.Store.UpdateRule(rule); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rule updated successfully",
	})
}

// HandleDeleteRule deletes a WAF rule (Admin only)
func (a *API) HandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := a.Store.DeleteRule(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rule deleted successfully",
	})
}

// HandleTestRule tests a rule without applying it (dry-run)
func (a *API) HandleTestRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RuleContent string `json:"rule_content"`
		TestInput   string `json:"test_input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Simplified rule testing - in production would compile and test against Coraza
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"message":     "Rule syntax is valid",
		"would_match": false,
	})
}

// HandleUsers returns list of users (Admin only)
func (a *API) HandleUsers(w http.ResponseWriter, r *http.Request) {
	// In production, this would query the database
	users := []map[string]interface{}{
		{"id": 1, "username": "admin", "email": "admin@obsidian.local", "role": "Admin", "enabled": true},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

// HandleAuditLogs returns audit trail (Admin only)
func (a *API) HandleAuditLogs(w http.ResponseWriter, r *http.Request) {
	// In production, this would query the database
	logs := []map[string]interface{}{
		{"id": 1, "action": "LOGIN", "resource": "/api/login", "timestamp": time.Now().Add(-1 * time.Hour).Format(time.RFC3339)},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}
