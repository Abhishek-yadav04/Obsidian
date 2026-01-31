package waf

import (
	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/experimental/plugins"
	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/corazawaf/coraza/v3/types"
)

func NewWAF(s *store.Store) (coraza.WAF, error) {
	// Register the custom logger
	plugins.RegisterAuditLogWriter("hybrid", func() plugintypes.AuditLogWriter {
		return store.NewHybridAuditLogger(s)
	})

	config := coraza.NewWAFConfig().
		WithDirectives(`
			# Basic Setup
			SecRuleEngine On
			SecRequestBodyAccess On
			SecResponseBodyAccess On
			SecResponseBodyMimeType text/plain text/html text/xml application/json
			
			# Audit Log Setup
			SecAuditEngine RelevantOnly
			SecAuditLogType hybrid
			SecAuditLogRelevantStatus "^[45].."

			# ============================================
			# XSS Protection Rules (Check full URI including query string)
			# ============================================
			SecRule REQUEST_URI "@rx (?i)(<script|%3Cscript)" \
				"id:941100,phase:1,deny,status:403,msg:'XSS Attack: Script Tag',severity:CRITICAL,tag:'attack-xss'"
			
			SecRule REQUEST_URI "@rx (?i)(javascript:|%6Aavascript:)" \
				"id:941110,phase:1,deny,status:403,msg:'XSS Attack: JavaScript Protocol',severity:CRITICAL,tag:'attack-xss'"
			
			SecRule REQUEST_URI "@rx (?i)(onerror|onload|onclick|onmouseover)\s*=" \
				"id:941120,phase:1,deny,status:403,msg:'XSS Attack: Event Handler',severity:CRITICAL,tag:'attack-xss'"
			
			SecRule REQUEST_URI "@rx (?i)alert\s*\(" \
				"id:941140,phase:1,deny,status:403,msg:'XSS Attack: Alert Function',severity:WARNING,tag:'attack-xss'"

			# ============================================
			# SQL Injection Rules
			# ============================================
			SecRule REQUEST_URI "@rx (?i)[\s\+]+(or|and)[\s\+]+\d+[\s\+]*=" \
				"id:942100,phase:1,deny,status:403,msg:'SQL Injection: Boolean Logic',severity:CRITICAL,tag:'attack-sqli'"
			
			SecRule REQUEST_URI "@rx (?i)'[\s\+]*(or|and)[\s\+]*'" \
				"id:942110,phase:1,deny,status:403,msg:'SQL Injection: String Logic',severity:CRITICAL,tag:'attack-sqli'"
			
			SecRule REQUEST_URI "@rx (?i)(union).*(select)" \
				"id:942120,phase:1,deny,status:403,msg:'SQL Injection: UNION SELECT',severity:CRITICAL,tag:'attack-sqli'"
			
			SecRule REQUEST_URI "@rx '--" \
				"id:942130,phase:1,deny,status:403,msg:'SQL Injection: Comment Sequence',severity:CRITICAL,tag:'attack-sqli'"
			
			SecRule REQUEST_URI "@rx (?i)(select|insert|update|delete|drop)[\s\+]" \
				"id:942140,phase:1,deny,status:403,msg:'SQL Injection: SQL Keyword',severity:WARNING,tag:'attack-sqli'"

			# ============================================
			# Path Traversal Rules
			# ============================================
			SecRule REQUEST_URI "@rx (\.\.\/|\.\.\\|%2e%2e%2f|%2e%2e\/|\.\.%2f|%2e%2e%5c)" \
				"id:930100,phase:1,deny,status:403,msg:'Path Traversal Attack',severity:CRITICAL,tag:'attack-lfi'"
			
			SecRule REQUEST_URI "@rx (?i)(/etc/passwd|/etc/shadow)" \
				"id:930110,phase:1,deny,status:403,msg:'Path Traversal: Sensitive File Access',severity:CRITICAL,tag:'attack-lfi'"

			# ============================================
			# Command Injection Rules
			# ============================================
			SecRule REQUEST_URI "@rx [;&|]\s*(cat|ls|dir|wget|curl|nc|bash|sh)" \
				"id:932100,phase:1,deny,status:403,msg:'OS Command Injection',severity:CRITICAL,tag:'attack-rce'"

			# ============================================
			# Remote File Inclusion Rules
			# ============================================
			SecRule QUERY_STRING "@rx (?i)^.*(https?|ftp)://" \
				"id:931100,phase:1,deny,status:403,msg:'Remote File Inclusion Attempt',severity:CRITICAL,tag:'attack-rfi'"

			# ============================================
			# Scanner/Bot Detection
			# ============================================
			SecRule REQUEST_HEADERS:User-Agent "@rx (?i)(nikto|sqlmap|nmap|masscan|burp|owasp|dirbuster|gobuster|wfuzz|hydra)" \
				"id:913100,phase:1,deny,status:403,msg:'Security Scanner Detected',severity:WARNING,tag:'automation-security'"
			
			# ============================================
			# Test Rule - Easy to trigger for testing
			# ============================================
			SecRule QUERY_STRING "@rx attack=test" \
				"id:900001,phase:1,deny,status:403,msg:'Test Attack Triggered',severity:CRITICAL,tag:'test'"
		`).
		WithErrorCallback(func(rule types.MatchedRule) {
			// This callback runs on every match
		})

	return coraza.NewWAF(config)
}
