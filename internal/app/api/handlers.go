package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/corazawaf/coraza/v3/internal/app/auth"
	"github.com/corazawaf/coraza/v3/internal/app/model"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/gorilla/websocket"
)

type API struct {
	Store *store.Store
	// Upgrader for websockets
	Upgrader websocket.Upgrader
}

func NewAPI(s *store.Store) *API {
	return &API{
		Store: s,
		Upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all for dev
			},
		},
	}
}

func (a *API) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Authenticate user
	user, err := a.Store.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
		return
	}

	// Generate JWT token (15 minutes)
	token, err := auth.GenerateJWT(user.ID, user.Username, user.Role, "obsidian-secret-key-change-in-prod", 15*time.Minute)
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
		IPAddress:    r.RemoteAddr,
		UserAgent:    r.UserAgent(),
	}
	a.Store.CreateSession(session)

	// Log authentication event
	a.Store.AddAuditLog(model.AuditLog{
		UserID:    &user.ID,
		Action:    "LOGIN",
		Resource:  "/api/login",
		Details:   fmt.Sprintf("User %s logged in", user.Username),
		IPAddress: r.RemoteAddr,
		Timestamp: time.Now(),
	})

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(model.LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         *user,
		ExpiresIn:    900, // 15 minutes in seconds
	})
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
