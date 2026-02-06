// Package alerts provides webhook-based alerting for Obsidian WAF.
// This implementation supports Slack, Microsoft Teams, Discord, and generic webhooks
// for sending notifications about security events, threshold breaches, and system status.
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Errors for alert operations
var (
	ErrWebhookNotConfigured = errors.New("webhook URL not configured")
	ErrAlertFailed          = errors.New("failed to send alert")
	ErrRateLimited          = errors.New("alert rate limited")
	ErrInvalidSeverity      = errors.New("invalid severity level")
)

// Severity levels for alerts
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// AlertType categorizes the type of alert
type AlertType string

const (
	AlertTypeSecurity    AlertType = "security"
	AlertTypeThreshold   AlertType = "threshold"
	AlertTypeSystem      AlertType = "system"
	AlertTypeWAF         AlertType = "waf"
	AlertTypeRateLimit   AlertType = "rate_limit"
	AlertTypeThreatIntel AlertType = "threat_intel"
	AlertTypeAuth        AlertType = "auth"
)

// WebhookType identifies the webhook platform
type WebhookType string

const (
	WebhookSlack     WebhookType = "slack"
	WebhookTeams     WebhookType = "teams"
	WebhookDiscord   WebhookType = "discord"
	WebhookGeneric   WebhookType = "generic"
	WebhookPagerDuty WebhookType = "pagerduty"
)

// Alert represents a single alert event
type Alert struct {
	ID         string                 `json:"id"`
	Type       AlertType              `json:"type"`
	Severity   Severity               `json:"severity"`
	Title      string                 `json:"title"`
	Message    string                 `json:"message"`
	Details    map[string]interface{} `json:"details,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	Source     string                 `json:"source"`
	ClientIP   string                 `json:"client_ip,omitempty"`
	RuleID     int                    `json:"rule_id,omitempty"`
	Resolved   bool                   `json:"resolved"`
	ResolvedAt *time.Time             `json:"resolved_at,omitempty"`
}

// WebhookConfig holds configuration for a webhook destination
type WebhookConfig struct {
	Name        string            `json:"name"`
	Type        WebhookType       `json:"type"`
	URL         string            `json:"url"`
	Enabled     bool              `json:"enabled"`
	MinSeverity Severity          `json:"min_severity"` // Only send alerts >= this severity
	AlertTypes  []AlertType       `json:"alert_types"`  // Filter by alert types (empty = all)
	Headers     map[string]string `json:"headers,omitempty"`
	RateLimit   *RateLimitConfig  `json:"rate_limit,omitempty"`
}

// RateLimitConfig for webhook throttling
type RateLimitConfig struct {
	MaxPerMinute int `json:"max_per_minute"`
	MaxPerHour   int `json:"max_per_hour"`
}

// Config for the alert service
type Config struct {
	// Webhooks is the list of configured webhook destinations
	Webhooks []WebhookConfig

	// DefaultRateLimit applies to all webhooks without specific limits
	DefaultRateLimit RateLimitConfig

	// BatchWindow groups alerts within this duration
	BatchWindow time.Duration

	// RetryAttempts for failed webhook deliveries
	RetryAttempts int

	// RetryDelay between retry attempts
	RetryDelay time.Duration

	// BufferSize for the alert queue
	BufferSize int
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		Webhooks: []WebhookConfig{},
		DefaultRateLimit: RateLimitConfig{
			MaxPerMinute: 10,
			MaxPerHour:   100,
		},
		BatchWindow:   5 * time.Second,
		RetryAttempts: 3,
		RetryDelay:    time.Second,
		BufferSize:    1000,
	}
}

// Metrics for alert service
type Metrics struct {
	TotalSent        int64               `json:"total_sent"`
	TotalFailed      int64               `json:"total_failed"`
	TotalRateLimited int64               `json:"total_rate_limited"`
	ByType           map[AlertType]int64 `json:"by_type"`
	BySeverity       map[Severity]int64  `json:"by_severity"`
	ByWebhook        map[string]int64    `json:"by_webhook"`
}

// webhookState tracks rate limiting for a webhook
type webhookState struct {
	mu          sync.Mutex
	minuteCount int
	hourCount   int
	lastMinute  time.Time
	lastHour    time.Time
}

// Service provides webhook alerting functionality
type Service struct {
	mu         sync.RWMutex
	config     Config
	webhooks   map[string]*WebhookConfig
	states     map[string]*webhookState
	metrics    Metrics
	alertChan  chan *Alert
	stopCh     chan struct{}
	wg         sync.WaitGroup
	httpClient *http.Client
}

// NewService creates a new alert service
func NewService(cfg Config) *Service {
	s := &Service{
		config:    cfg,
		webhooks:  make(map[string]*WebhookConfig),
		states:    make(map[string]*webhookState),
		alertChan: make(chan *Alert, cfg.BufferSize),
		stopCh:    make(chan struct{}),
		metrics: Metrics{
			ByType:     make(map[AlertType]int64),
			BySeverity: make(map[Severity]int64),
			ByWebhook:  make(map[string]int64),
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	// Register webhooks
	for i := range cfg.Webhooks {
		wh := &cfg.Webhooks[i]
		s.webhooks[wh.Name] = wh
		s.states[wh.Name] = &webhookState{}
	}

	// Start background worker
	s.wg.Add(1)
	go s.worker()

	return s
}

// Stop gracefully shuts down the alert service
func (s *Service) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

// Send queues an alert for delivery
func (s *Service) Send(alert *Alert) error {
	// Set defaults
	if alert.ID == "" {
		alert.ID = fmt.Sprintf("alert-%d", time.Now().UnixNano())
	}
	if alert.Timestamp.IsZero() {
		alert.Timestamp = time.Now()
	}
	if alert.Source == "" {
		alert.Source = "obsidian-waf"
	}

	// Queue the alert
	select {
	case s.alertChan <- alert:
		return nil
	default:
		return errors.New("alert queue full")
	}
}

// SendImmediate sends an alert immediately, bypassing the queue
func (s *Service) SendImmediate(ctx context.Context, alert *Alert) error {
	return s.processAlert(ctx, alert)
}

// worker processes alerts from the queue
func (s *Service) worker() {
	defer s.wg.Done()

	for {
		select {
		case <-s.stopCh:
			// Drain remaining alerts
			for len(s.alertChan) > 0 {
				alert := <-s.alertChan
				_ = s.processAlert(context.Background(), alert)
			}
			return
		case alert := <-s.alertChan:
			_ = s.processAlert(context.Background(), alert)
		}
	}
}

// processAlert sends an alert to all matching webhooks
func (s *Service) processAlert(ctx context.Context, alert *Alert) error {
	s.mu.RLock()
	webhooks := make([]*WebhookConfig, 0, len(s.webhooks))
	for _, wh := range s.webhooks {
		webhooks = append(webhooks, wh)
	}
	s.mu.RUnlock()

	var lastErr error
	for _, wh := range webhooks {
		if !wh.Enabled {
			continue
		}

		// Check severity filter
		if !s.severityAllowed(alert.Severity, wh.MinSeverity) {
			continue
		}

		// Check alert type filter
		if !s.typeAllowed(alert.Type, wh.AlertTypes) {
			continue
		}

		// Check rate limit
		if !s.checkRateLimit(wh.Name, wh.RateLimit) {
			s.mu.Lock()
			s.metrics.TotalRateLimited++
			s.mu.Unlock()
			continue
		}

		// Send to webhook
		if err := s.sendToWebhook(ctx, wh, alert); err != nil {
			lastErr = err
			s.mu.Lock()
			s.metrics.TotalFailed++
			s.mu.Unlock()
		} else {
			// Increment rate-limit counters only on successful send
			s.incrementRateLimitCounters(wh.Name)
			s.mu.Lock()
			s.metrics.TotalSent++
			s.metrics.ByType[alert.Type]++
			s.metrics.BySeverity[alert.Severity]++
			s.metrics.ByWebhook[wh.Name]++
			s.mu.Unlock()
		}
	}

	return lastErr
}

// severityAllowed checks if alert severity meets minimum threshold
func (s *Service) severityAllowed(alertSev, minSev Severity) bool {
	sevLevels := map[Severity]int{
		SeverityInfo:     0,
		SeverityLow:      1,
		SeverityMedium:   2,
		SeverityHigh:     3,
		SeverityCritical: 4,
	}

	alertLevel, ok1 := sevLevels[alertSev]
	minLevel, ok2 := sevLevels[minSev]

	if !ok1 || !ok2 {
		return true // Allow if severity not recognized
	}

	return alertLevel >= minLevel
}

// typeAllowed checks if alert type is in the allowed list
func (s *Service) typeAllowed(alertType AlertType, allowedTypes []AlertType) bool {
	if len(allowedTypes) == 0 {
		return true // Empty = all allowed
	}

	for _, t := range allowedTypes {
		if t == alertType {
			return true
		}
	}
	return false
}

// checkRateLimit checks and updates rate limit state
func (s *Service) checkRateLimit(webhookName string, cfg *RateLimitConfig) bool {
	state, ok := s.states[webhookName]
	if !ok {
		return true
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	limit := cfg
	if limit == nil {
		limit = &s.config.DefaultRateLimit
	}

	// Reset minute counter
	if now.Sub(state.lastMinute) >= time.Minute {
		state.minuteCount = 0
		state.lastMinute = now
	}

	// Reset hour counter
	if now.Sub(state.lastHour) >= time.Hour {
		state.hourCount = 0
		state.lastHour = now
	}

	// Check limits
	if state.minuteCount >= limit.MaxPerMinute || state.hourCount >= limit.MaxPerHour {
		return false
	}

	return true
}

// incrementRateLimitCounters increments rate-limit counters after a successful send
func (s *Service) incrementRateLimitCounters(webhookName string) {
	state, ok := s.states[webhookName]
	if !ok {
		return
	}
	state.mu.Lock()
	state.minuteCount++
	state.hourCount++
	state.mu.Unlock()
}

// getRoutingKey returns the PagerDuty routing key from webhook headers
func (s *Service) getRoutingKey(alert *Alert) string {
	// Routing key is stored in the webhook headers for PagerDuty
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, wh := range s.webhooks {
		if wh.Type == WebhookPagerDuty {
			if key, ok := wh.Headers["routing_key"]; ok {
				return key
			}
		}
	}
	return ""
}

// sendToWebhook sends an alert to a specific webhook
func (s *Service) sendToWebhook(ctx context.Context, wh *WebhookConfig, alert *Alert) error {
	// Handle internal console logging webhook
	if wh.URL == "internal://console" {
		fmt.Printf("[ALERT] [%s] [%s] %s: %s\n", alert.Timestamp.Format("2006-01-02 15:04:05"), alert.Severity, alert.Title, alert.Message)
		if alert.ClientIP != "" {
			fmt.Printf("        Client IP: %s\n", alert.ClientIP)
		}
		return nil
	}

	var payload []byte
	var err error

	switch wh.Type {
	case WebhookSlack:
		payload, err = s.formatSlackPayload(alert)
	case WebhookTeams:
		payload, err = s.formatTeamsPayload(alert)
	case WebhookDiscord:
		payload, err = s.formatDiscordPayload(alert)
	case WebhookPagerDuty:
		payload, err = s.formatPagerDutyPayload(alert)
	default:
		payload, err = s.formatGenericPayload(alert)
	}

	if err != nil {
		return fmt.Errorf("failed to format payload: %w", err)
	}

	// Send with retries
	for attempt := 0; attempt <= s.config.RetryAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(s.config.RetryDelay * time.Duration(attempt))
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.URL, bytes.NewReader(payload))
		if err != nil {
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		for k, v := range wh.Headers {
			req.Header.Set(k, v)
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
	}

	return ErrAlertFailed
}

// Slack payload format
func (s *Service) formatSlackPayload(alert *Alert) ([]byte, error) {
	color := s.severityColor(alert.Severity)

	payload := map[string]interface{}{
		"attachments": []map[string]interface{}{
			{
				"color":  color,
				"title":  fmt.Sprintf("[%s] %s", alert.Severity, alert.Title),
				"text":   alert.Message,
				"footer": "Obsidian WAF",
				"ts":     alert.Timestamp.Unix(),
				"fields": []map[string]interface{}{
					{"title": "Type", "value": string(alert.Type), "short": true},
					{"title": "Severity", "value": string(alert.Severity), "short": true},
					{"title": "Source", "value": alert.Source, "short": true},
				},
			},
		},
	}

	if alert.ClientIP != "" {
		payload["attachments"].([]map[string]interface{})[0]["fields"] = append(
			payload["attachments"].([]map[string]interface{})[0]["fields"].([]map[string]interface{}),
			map[string]interface{}{"title": "Client IP", "value": alert.ClientIP, "short": true},
		)
	}

	return json.Marshal(payload)
}

// Microsoft Teams payload format
func (s *Service) formatTeamsPayload(alert *Alert) ([]byte, error) {
	color := s.severityColor(alert.Severity)

	payload := map[string]interface{}{
		"@type":      "MessageCard",
		"@context":   "http://schema.org/extensions",
		"themeColor": color,
		"summary":    alert.Title,
		"sections": []map[string]interface{}{
			{
				"activityTitle":    fmt.Sprintf("🛡️ [%s] %s", alert.Severity, alert.Title),
				"activitySubtitle": fmt.Sprintf("Obsidian WAF - %s", alert.Timestamp.Format(time.RFC3339)),
				"text":             alert.Message,
				"facts": []map[string]string{
					{"name": "Type", "value": string(alert.Type)},
					{"name": "Severity", "value": string(alert.Severity)},
					{"name": "Source", "value": alert.Source},
				},
			},
		},
	}

	if alert.ClientIP != "" {
		payload["sections"].([]map[string]interface{})[0]["facts"] = append(
			payload["sections"].([]map[string]interface{})[0]["facts"].([]map[string]string),
			map[string]string{"name": "Client IP", "value": alert.ClientIP},
		)
	}

	return json.Marshal(payload)
}

// Discord payload format
func (s *Service) formatDiscordPayload(alert *Alert) ([]byte, error) {
	color := s.severityColorInt(alert.Severity)

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("[%s] %s", alert.Severity, alert.Title),
				"description": alert.Message,
				"color":       color,
				"timestamp":   alert.Timestamp.Format(time.RFC3339),
				"footer": map[string]string{
					"text": "Obsidian WAF",
				},
				"fields": []map[string]interface{}{
					{"name": "Type", "value": string(alert.Type), "inline": true},
					{"name": "Severity", "value": string(alert.Severity), "inline": true},
					{"name": "Source", "value": alert.Source, "inline": true},
				},
			},
		},
	}

	return json.Marshal(payload)
}

// PagerDuty payload format
func (s *Service) formatPagerDutyPayload(alert *Alert) ([]byte, error) {
	severity := "info"
	switch alert.Severity {
	case SeverityCritical:
		severity = "critical"
	case SeverityHigh:
		severity = "error"
	case SeverityMedium:
		severity = "warning"
	}

	payload := map[string]interface{}{
		"routing_key":  s.getRoutingKey(alert),
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":   alert.Title,
			"severity":  severity,
			"source":    alert.Source,
			"timestamp": alert.Timestamp.Format(time.RFC3339),
			"custom_details": map[string]interface{}{
				"message":   alert.Message,
				"type":      alert.Type,
				"client_ip": alert.ClientIP,
			},
		},
	}

	return json.Marshal(payload)
}

// Generic webhook payload
func (s *Service) formatGenericPayload(alert *Alert) ([]byte, error) {
	return json.Marshal(alert)
}

// severityColor returns hex color for Slack/Teams
func (s *Service) severityColor(sev Severity) string {
	switch sev {
	case SeverityCritical:
		return "#FF0000"
	case SeverityHigh:
		return "#FF6600"
	case SeverityMedium:
		return "#FFCC00"
	case SeverityLow:
		return "#00CC00"
	default:
		return "#0099FF"
	}
}

// severityColorInt returns int color for Discord
func (s *Service) severityColorInt(sev Severity) int {
	switch sev {
	case SeverityCritical:
		return 16711680 // Red
	case SeverityHigh:
		return 16737280 // Orange
	case SeverityMedium:
		return 16763904 // Yellow
	case SeverityLow:
		return 52224 // Green
	default:
		return 39423 // Blue
	}
}

// AddWebhook adds a new webhook configuration
func (s *Service) AddWebhook(wh WebhookConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.webhooks[wh.Name] = &wh
	s.states[wh.Name] = &webhookState{}
}

// RemoveWebhook removes a webhook configuration
func (s *Service) RemoveWebhook(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.webhooks, name)
	delete(s.states, name)
}

// GetWebhooks returns all configured webhooks
func (s *Service) GetWebhooks() []WebhookConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	webhooks := make([]WebhookConfig, 0, len(s.webhooks))
	for _, wh := range s.webhooks {
		webhooks = append(webhooks, *wh)
	}
	return webhooks
}

// GetMetrics returns service metrics (deep copy to prevent races)
func (s *Service) GetMetrics() Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	copy := s.metrics
	copy.ByType = make(map[AlertType]int64, len(s.metrics.ByType))
	for k, v := range s.metrics.ByType {
		copy.ByType[k] = v
	}
	copy.BySeverity = make(map[Severity]int64, len(s.metrics.BySeverity))
	for k, v := range s.metrics.BySeverity {
		copy.BySeverity[k] = v
	}
	copy.ByWebhook = make(map[string]int64, len(s.metrics.ByWebhook))
	for k, v := range s.metrics.ByWebhook {
		copy.ByWebhook[k] = v
	}
	return copy
}

// TestWebhook sends a test alert to a specific webhook
func (s *Service) TestWebhook(ctx context.Context, webhookName string) error {
	s.mu.RLock()
	wh, ok := s.webhooks[webhookName]
	s.mu.RUnlock()

	if !ok {
		return errors.New("webhook not found")
	}

	testAlert := &Alert{
		ID:        fmt.Sprintf("test-%d", time.Now().UnixNano()),
		Type:      AlertTypeSystem,
		Severity:  SeverityInfo,
		Title:     "Test Alert from Obsidian WAF",
		Message:   "This is a test alert to verify webhook configuration.",
		Timestamp: time.Now(),
		Source:    "obsidian-waf",
		Details: map[string]interface{}{
			"test": true,
		},
	}

	return s.sendToWebhook(ctx, wh, testAlert)
}

// HTTP Handlers

// HandleWebhooks handles webhook CRUD operations
func (s *Service) HandleWebhooks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		webhooks := s.GetWebhooks()
		// Don't expose URLs in response for security
		for i := range webhooks {
			webhooks[i].URL = "***"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(webhooks)

	case http.MethodPost:
		var wh WebhookConfig
		if err := json.NewDecoder(r.Body).Decode(&wh); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		s.AddWebhook(wh)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Webhook added", "name": wh.Name})

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "Webhook name required", http.StatusBadRequest)
			return
		}
		s.RemoveWebhook(name)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Webhook removed", "name": name})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleTest handles webhook test requests
func (s *Service) HandleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "Webhook name required", http.StatusBadRequest)
		return
	}

	if err := s.TestWebhook(r.Context(), name); err != nil {
		http.Error(w, fmt.Sprintf("Test failed: %s", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true,"message":"Test alert sent to %s"}`, name)
}

// HandleMetrics returns alert service metrics
func (s *Service) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := s.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// Convenience functions for common alerts

// AlertSecurityEvent sends a security-related alert
func (s *Service) AlertSecurityEvent(severity Severity, title, message, clientIP string, details map[string]interface{}) error {
	return s.Send(&Alert{
		Type:     AlertTypeSecurity,
		Severity: severity,
		Title:    title,
		Message:  message,
		ClientIP: clientIP,
		Details:  details,
	})
}

// AlertWAFBlock sends a WAF block alert
func (s *Service) AlertWAFBlock(ruleID int, clientIP, uri, reason string) error {
	return s.Send(&Alert{
		Type:     AlertTypeWAF,
		Severity: SeverityMedium,
		Title:    fmt.Sprintf("WAF Block - Rule %d", ruleID),
		Message:  fmt.Sprintf("Request blocked: %s", reason),
		ClientIP: clientIP,
		RuleID:   ruleID,
		Details: map[string]interface{}{
			"uri":    uri,
			"reason": reason,
		},
	})
}

// AlertRateLimit sends a rate limit alert
func (s *Service) AlertRateLimit(clientIP, endpoint string) error {
	return s.Send(&Alert{
		Type:     AlertTypeRateLimit,
		Severity: SeverityLow,
		Title:    "Rate Limit Exceeded",
		Message:  fmt.Sprintf("IP %s exceeded rate limit on %s", clientIP, endpoint),
		ClientIP: clientIP,
		Details: map[string]interface{}{
			"endpoint": endpoint,
		},
	})
}

// AlertThreatIntel sends a threat intelligence alert
func (s *Service) AlertThreatIntel(clientIP, category, details string) error {
	return s.Send(&Alert{
		Type:     AlertTypeThreatIntel,
		Severity: SeverityHigh,
		Title:    "Threat Intelligence Match",
		Message:  fmt.Sprintf("IP %s matched threat category: %s", clientIP, category),
		ClientIP: clientIP,
		Details: map[string]interface{}{
			"category": category,
			"details":  details,
		},
	})
}

// AlertAuthFailure sends an authentication failure alert
func (s *Service) AlertAuthFailure(username, clientIP, reason string) error {
	// Mask username to avoid PII leakage in alerts
	maskedUser := maskUsername(username)
	return s.Send(&Alert{
		Type:     AlertTypeAuth,
		Severity: SeverityMedium,
		Title:    "Authentication Failure",
		Message:  fmt.Sprintf("Failed login attempt for user %s from %s: %s", maskedUser, clientIP, reason),
		ClientIP: clientIP,
		Details: map[string]interface{}{
			"username_masked": maskedUser,
			"reason":          reason,
		},
	})
}

// maskUsername masks a username for safe logging
func maskUsername(u string) string {
	if len(u) <= 2 {
		return "***"
	}
	return u[:1] + "***" + u[len(u)-1:]
}

// AlertSystemEvent sends a system-level alert
func (s *Service) AlertSystemEvent(severity Severity, title, message string) error {
	return s.Send(&Alert{
		Type:     AlertTypeSystem,
		Severity: severity,
		Title:    title,
		Message:  message,
	})
}
