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

			# XSS Protection
			SecRule ARGS|REQUEST_HEADERS "@rx <script>" \
				"id:941100,phase:2,deny,status:403,msg:'XSS Attack Detected',severity:CRITICAL"

			# SQL Injection
			SecRule ARGS|REQUEST_HEADERS "@rx OR 1=1" \
				"id:942100,phase:2,deny,status:403,msg:'SQL Injection Attempt',severity:CRITICAL"
			
			# Bad User Agent
			SecRule REQUEST_HEADERS:User-Agent "@rx malicious" \
				"id:913100,phase:1,deny,status:403,msg:'Malicious UA Detected',severity:NOTICE"

			# Directory Traversal
			SecRule REQUEST_URI "@rx \.\./" \
				"id:930100,phase:1,deny,status:403,msg:'Path Traversal Attempt',severity:CRITICAL"
		`).
		WithErrorCallback(func(rule types.MatchedRule) {
			// This callback runs on every match, we can use it for stats too
			// fmt.Printf("Match: %d\n", rule.Rule().ID())
		})

	return coraza.NewWAF(config)
}
