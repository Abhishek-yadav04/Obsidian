// Package core contains the pure domain logic for Obsidian
package core

import (
	"fmt"
	"time"
)

// Rule represents a WAF rule
type Rule struct {
	ID       string
	Name     string
	Severity string
	Enabled  bool
}

// Alert represents a security alert
type Alert struct {
	ID        string
	Timestamp time.Time
	RuleID    string
	Message   string
	Severity  string
	TenantID  string
}

// Policy represents a tenant's security policy
type Policy struct {
	TenantID string
	Rules    []Rule
}

// ValidateRule validates a rule configuration
func ValidateRule(rule Rule) error {
	if rule.ID == "" {
		return fmt.Errorf("rule ID cannot be empty")
	}
	return nil
}

// ProcessAlert processes an incoming alert
func ProcessAlert(alert Alert) error {
	// Domain logic for alert processing
	return nil
}