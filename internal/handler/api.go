// Package handler contains API handlers
package handler

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// AttackLog represents a logged attack
type AttackLog struct {
	Time     string `json:"time"`
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Type     string `json:"type"`
}

// GetLogsHandler returns audit logs
func GetLogsHandler(logger *zap.Logger, db interface{}, logs *[]AttackLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Get tenant ID from context (set by middleware)
		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			tenantID = "default"
		}

		// For demo, return real logs from memory
		var realLogs []map[string]interface{}
		if logs != nil {
			// Iterate efficiently
			for i := len(*logs) - 1; i >= 0; i-- {
				log := (*logs)[i]
				realLogs = append(realLogs, map[string]interface{}{
					"time":     log.Time,
					"rule_id":  log.RuleID,
					"severity": log.Severity,
					"message":  log.Message,
				})
			}
		} else {
			realLogs = []map[string]interface{}{}
		}

		if err := json.NewEncoder(w).Encode(realLogs); err != nil {
			logger.Error("Failed to encode logs response", zap.Error(err))
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

// GetStatsHandler returns attack statistics
func GetStatsHandler(logger *zap.Logger, db interface{}, logs *[]AttackLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Calculate real stats from logs
		sqli := 0
		xss := 0
		if logs != nil {
			for _, log := range *logs {
				if log.Type == "SQLI" {
					sqli++
				} else if log.Type == "XSS" {
					xss++
				}
			}
		}
		stats := map[string]int{
			"sqli": sqli,
			"xss":  xss,
		}

		if err := json.NewEncoder(w).Encode(stats); err != nil {
			logger.Error("Failed to encode stats response", zap.Error(err))
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}