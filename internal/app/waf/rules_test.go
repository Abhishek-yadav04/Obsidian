package waf

import (
	"path/filepath"
	"testing"

	"github.com/corazawaf/coraza/v3"
)

func TestCustomRulesEnforcement(t *testing.T) {
	rulesPath := filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf")
	waf, err := coraza.NewWAF(coraza.NewWAFConfig().
		WithDirectives("SecRuleEngine On").
		WithDirectivesFromFile(rulesPath),
	)
	if err != nil {
		t.Fatalf("failed to init WAF: %v", err)
	}

	tests := []struct {
		name       string
		uri        string
		queryKey   string
		queryValue string
		ruleID     int
	}{
		{
			name:       "Test Rule",
			uri:        "/?attack=test",
			queryKey:   "attack",
			queryValue: "test",
			ruleID:     900001,
		},
		{
			name:   "LFI Rule",
			uri:    "/../../etc/passwd",
			ruleID: 930100,
		},
		{
			name:   "XSS Rule",
			uri:    "/<script>alert(1)</script>",
			ruleID: 941100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := waf.NewTransaction()
			tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)
			tx.ProcessURI(tt.uri, "GET", "HTTP/1.1")
			tx.AddRequestHeader("Host", "localhost")
			if tt.queryKey != "" && tt.uri == "/" {
				tx.AddGetRequestArgument(tt.queryKey, tt.queryValue)
			}

			_ = tx.ProcessRequestHeaders()
			if !tx.IsInterrupted() {
				t.Fatalf("expected interruption for rule %d", tt.ruleID)
			}
			if tx.Interruption() == nil || tx.Interruption().RuleID != tt.ruleID {
				t.Fatalf("expected rule %d, got %+v", tt.ruleID, tx.Interruption())
			}
			_ = tx.Close()
		})
	}
}
