// Package handler provides HTTP handlers for security feature management.
package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/corazawaf/coraza/v3/internal/app/apikeys"
	"github.com/corazawaf/coraza/v3/internal/app/graphql"
	"github.com/corazawaf/coraza/v3/internal/app/hibp"
	"github.com/corazawaf/coraza/v3/internal/app/ipallow"
	"github.com/corazawaf/coraza/v3/internal/app/respbody"
	"github.com/corazawaf/coraza/v3/internal/app/secrets"
)

// SecurityServices holds all security feature services
type SecurityServices struct {
	APIKeys     *apikeys.Manager
	IPAllowlist *ipallow.Manager
	HIBP        *hibp.Checker
	Secrets     *secrets.Manager
	GraphQL     *graphql.Analyzer
	RespBody    *respbody.Inspector
	Logger      *zap.Logger
}

// NewSecurityServices creates and initializes all security services
func NewSecurityServices(logger *zap.Logger) *SecurityServices {
	ss := &SecurityServices{
		Logger: logger,
	}

	// Initialize API Key Manager (nil = in-memory storage)
	ss.APIKeys = apikeys.NewManager(nil)

	// Initialize IP Allowlist Manager (nil = in-memory storage)
	ss.IPAllowlist = ipallow.NewManager(ipallow.DefaultConfig(), nil)

	// Initialize HIBP Checker
	ss.HIBP = hibp.NewChecker(hibp.DefaultConfig())

	// Initialize Secrets Manager
	ss.Secrets = secrets.NewManager()

	// Initialize GraphQL Analyzer
	ss.GraphQL = graphql.NewAnalyzer(graphql.DefaultConfig())

	// Initialize Response Body Inspector
	ss.RespBody = respbody.NewInspector(respbody.DefaultConfig())

	return ss
}

// ==================== API KEY HANDLERS ====================

// HandleAPIKeyCreate creates a new API key
func (ss *SecurityServices) HandleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name     string   `json:"name"`
		Scopes   []string `json:"scopes"`
		OwnerID  string   `json:"owner_id"`
		ExpireIn string   `json:"expire_in"` // e.g., "720h" for 30 days
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Parse expiration duration
	expireIn := 30 * 24 * time.Hour // Default 30 days
	if req.ExpireIn != "" {
		if d, err := time.ParseDuration(req.ExpireIn); err == nil {
			expireIn = d
		}
	}

	// Convert string scopes to apikeys.Scope
	scopes := make([]apikeys.Scope, len(req.Scopes))
	for i, s := range req.Scopes {
		scopes[i] = apikeys.Scope(s)
	}

	// Generate the key
	plainKey, key, err := ss.APIKeys.GenerateKey(r.Context(), req.Name, scopes, &expireIn, 0, "", req.OwnerID)
	if err != nil {
		ss.Logger.Error("Failed to generate API key", zap.Error(err))
		http.Error(w, "Failed to generate API key", http.StatusInternalServerError)
		return
	}

	// Return the key (only time the raw key is visible)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":     plainKey, // Only returned on creation
		"id":      key.ID,
		"name":    key.Name,
		"scopes":  key.Scopes,
		"created": key.CreatedAt,
		"expires": key.ExpiresAt,
	})
}

// HandleAPIKeyList lists all API keys (without raw values)
func (ss *SecurityServices) HandleAPIKeyList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	keys := ss.APIKeys.ListKeys()

	// Sanitize - remove hash from response
	safeKeys := make([]map[string]interface{}, len(keys))
	for i, k := range keys {
		safeKeys[i] = map[string]interface{}{
			"id":         k.ID,
			"name":       k.Name,
			"prefix":     k.KeyPrefix,
			"scopes":     k.Scopes,
			"created_by": k.CreatedBy,
			"created_at": k.CreatedAt,
			"expires_at": k.ExpiresAt,
			"last_used":  k.LastUsedAt,
			"enabled":    k.Enabled,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(safeKeys)
}

// HandleAPIKeyRevoke revokes an API key
func (ss *SecurityServices) HandleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		KeyID string `json:"key_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.KeyID == "" {
		http.Error(w, "key_id is required", http.StatusBadRequest)
		return
	}

	if err := ss.APIKeys.RevokeKey(r.Context(), req.KeyID); err != nil {
		http.Error(w, "Failed to revoke key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked"})
}

// ==================== IP ALLOWLIST HANDLERS ====================

// HandleIPAllowlistAdd adds an IP or CIDR to the allowlist
func (ss *SecurityServices) HandleIPAllowlistAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IP          string `json:"ip"`
		CIDR        string `json:"cidr"`
		Description string `json:"description"`
		AddedBy     string `json:"added_by"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var err error
	if req.CIDR != "" {
		_, err = ss.IPAllowlist.AddCIDR(r.Context(), req.CIDR, req.Description, req.AddedBy)
	} else if req.IP != "" {
		_, err = ss.IPAllowlist.AddIP(r.Context(), req.IP, req.Description, req.AddedBy)
	} else {
		http.Error(w, "Must provide 'ip' or 'cidr'", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}

// HandleIPAllowlistList lists all allowlist entries
func (ss *SecurityServices) HandleIPAllowlistList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries := ss.IPAllowlist.ListEntries()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// HandleIPAllowlistRemove removes an entry from the allowlist
func (ss *SecurityServices) HandleIPAllowlistRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	if err := ss.IPAllowlist.RemoveEntry(r.Context(), req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}

// HandleIPAllowlistCheck checks if an IP is allowed
func (ss *SecurityServices) HandleIPAllowlistCheck(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		http.Error(w, "Missing 'ip' parameter", http.StatusBadRequest)
		return
	}

	allowed := ss.IPAllowlist.IsAllowed(ip)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed})
}

// ==================== PASSWORD CHECK HANDLERS ====================

// HandlePasswordCheck checks a password against HIBP
func (ss *SecurityServices) HandlePasswordCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Password == "" {
		http.Error(w, "Password required", http.StatusBadRequest)
		return
	}

	result, err := ss.HIBP.CheckPassword(r.Context(), req.Password)
	if err != nil {
		ss.Logger.Error("HIBP check failed", zap.Error(err))
		http.Error(w, "Password check failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"breached": result.IsBreached,
		"count":    result.BreachCount,
		"safe":     !result.IsBreached,
	})
}

// ==================== SECRETS HANDLERS ====================

// HandleSecretsReload triggers a secrets reload
func (ss *SecurityServices) HandleSecretsReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := ss.Secrets.ReloadAll(r.Context()); err != nil {
		ss.Logger.Error("Secrets reload failed", zap.Error(err))
		http.Error(w, "Reload failed", http.StatusInternalServerError)
		return
	}

	stats := ss.Secrets.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "reloaded",
		"reload_count": stats.ReloadCount,
	})
}

// HandleSecretsRotateJWT rotates the JWT secret
func (ss *SecurityServices) HandleSecretsRotateJWT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	newSecret, _, err := ss.Secrets.RotateJWTSecret()
	if err != nil {
		ss.Logger.Error("JWT rotation failed", zap.Error(err))
		http.Error(w, "Rotation failed", http.StatusInternalServerError)
		return
	}

	// Don't return the actual secret, just confirm rotation
	_ = newSecret
	stats := ss.Secrets.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "rotated",
		"reload_count": stats.ReloadCount,
		"note":         "All existing tokens are now invalid",
	})
}

// ==================== GRAPHQL HANDLERS ====================

// HandleGraphQLAnalyze analyzes a GraphQL query for security issues
func (ss *SecurityServices) HandleGraphQLAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query string `json:"query"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Build payload safely using json.Marshal to prevent JSON injection
	payload := map[string]string{"query": req.Query}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		ss.Logger.Error("Failed to marshal GraphQL payload", zap.Error(err))
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	result, err := ss.GraphQL.AnalyzeBody(payloadBytes)
	if err != nil {
		ss.Logger.Error("GraphQL analysis failed", zap.Error(err))
		http.Error(w, "Analysis failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleGraphQLConfig returns/updates GraphQL security config
func (ss *SecurityServices) HandleGraphQLConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ss.GraphQL.GetConfig())
		return
	}

	if r.Method == http.MethodPost {
		var config graphql.Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, "Invalid config", http.StatusBadRequest)
			return
		}
		ss.GraphQL.SetConfig(&config)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// ==================== RESPONSE BODY HANDLERS ====================

// HandleRespBodyConfig returns/updates response body inspection config
func (ss *SecurityServices) HandleRespBodyConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ss.RespBody.GetConfig())
		return
	}

	if r.Method == http.MethodPost {
		var config respbody.Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, "Invalid config", http.StatusBadRequest)
			return
		}
		ss.RespBody.SetConfig(&config)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// HandleRespBodyTest tests response body inspection with sample data
func (ss *SecurityServices) HandleRespBodyTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Body        string `json:"body"`
		ContentType string `json:"content_type"`
		Path        string `json:"path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	result := ss.RespBody.Inspect([]byte(req.Body), req.ContentType, req.Path)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ==================== SECURITY OVERVIEW HANDLER ====================

// HandleSecurityOverview returns overall security status
func (ss *SecurityServices) HandleSecurityOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiKeyCount := 0
	keys := ss.APIKeys.ListKeys()
	apiKeyCount = len(keys)

	// Get HIBP cache stats
	hibpEntries, hibpHitRate := ss.HIBP.GetCacheStats()

	// Get secrets stats
	secretStats := ss.Secrets.GetStats()

	overview := map[string]interface{}{
		"api_keys": map[string]interface{}{
			"enabled": true,
			"count":   apiKeyCount,
		},
		"ip_allowlist": map[string]interface{}{
			"enabled": ss.IPAllowlist.IsEnabled(),
			"count":   len(ss.IPAllowlist.ListEntries()),
		},
		"hibp": map[string]interface{}{
			"enabled":        ss.HIBP.IsEnabled(),
			"cache_entries":  hibpEntries,
			"cache_hit_rate": hibpHitRate,
		},
		"secrets": map[string]interface{}{
			"reload_count": secretStats.ReloadCount,
		},
		"graphql": map[string]interface{}{
			"enabled": ss.GraphQL.GetConfig().Enabled,
		},
		"response_inspection": map[string]interface{}{
			"enabled": ss.RespBody.GetConfig().Enabled,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(overview)
}
