package waf

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/corazawaf/coraza/v3/internal/app/store"
)

// TestWAFStartsWithoutCRS verifies the WAF starts normally when CRS is disabled.
func TestWAFStartsWithoutCRS(t *testing.T) {
	s := store.NewStore("")
	w, err := NewWAF(s, Config{
		CRSEnabled:      false,
		CRSPath:         "",
		CRSMode:         "",
		CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf"),
	})
	if err != nil {
		t.Fatalf("WAF should start without CRS: %v", err)
	}

	// Verify custom rules are active
	tx := w.NewTransaction()
	defer tx.Close()
	tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)
	tx.ProcessURI("/?attack=test", "GET", "HTTP/1.1")
	tx.AddRequestHeader("Host", "localhost")
	_ = tx.ProcessRequestHeaders()
	if !tx.IsInterrupted() {
		t.Fatal("custom rule 900001 should block ?attack=test even without CRS")
	}
}

// TestWAFStartsWithCRSDisabledNoPath verifies no crash when CRS is off and path is empty.
func TestWAFStartsWithCRSDisabledNoPath(t *testing.T) {
	s := store.NewStore("")
	w, err := NewWAF(s, Config{
		CRSEnabled:      false,
		CRSPath:         "",
		CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf"),
	})
	if err != nil {
		t.Fatalf("WAF should start with CRS disabled and empty path: %v", err)
	}
	tx := w.NewTransaction()
	defer tx.Close()
	tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)
	tx.ProcessURI("/api/health", "GET", "HTTP/1.1")
	tx.AddRequestHeader("Host", "localhost")
	_ = tx.ProcessRequestHeaders()
	if tx.IsInterrupted() {
		t.Fatal("normal request should not be blocked")
	}
}

// TestWAFStartsWithCRSEnabledButMissingPath verifies graceful degradation
// when CRS is enabled but the path doesn't exist.
func TestWAFStartsWithCRSEnabledButMissingPath(t *testing.T) {
	s := store.NewStore("")
	w, err := NewWAF(s, Config{
		CRSEnabled:      true,
		CRSPath:         "/nonexistent/crs/path",
		CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf"),
	})
	if err != nil {
		t.Fatalf("WAF should start even when CRS path is missing: %v", err)
	}

	// Custom rules should still work
	tx := w.NewTransaction()
	defer tx.Close()
	tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)
	tx.ProcessURI("/?attack=test", "GET", "HTTP/1.1")
	tx.AddRequestHeader("Host", "localhost")
	_ = tx.ProcessRequestHeaders()
	if !tx.IsInterrupted() {
		t.Fatal("custom rules should work even when CRS path is missing")
	}
}

// TestWAFStartsWithCRSEnabledButEmptyPath verifies graceful degradation
// when CRS is enabled but the path is empty string.
func TestWAFStartsWithCRSEnabledButEmptyPath(t *testing.T) {
	s := store.NewStore("")
	w, err := NewWAF(s, Config{
		CRSEnabled:      true,
		CRSPath:         "",
		CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf"),
	})
	if err != nil {
		t.Fatalf("WAF should start even when CRS path is empty: %v", err)
	}

	// Custom rules should still work
	tx := w.NewTransaction()
	defer tx.Close()
	tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)
	tx.ProcessURI("/?attack=test", "GET", "HTTP/1.1")
	tx.AddRequestHeader("Host", "localhost")
	_ = tx.ProcessRequestHeaders()
	if !tx.IsInterrupted() {
		t.Fatal("custom rules should work even when CRS path is empty")
	}
}

// TestWAFStartsWithMissingCustomRulesFile verifies the WAF starts even when
// the custom rules file doesn't exist.
func TestWAFStartsWithMissingCustomRulesFile(t *testing.T) {
	s := store.NewStore("")
	w, err := NewWAF(s, Config{
		CRSEnabled:      false,
		CustomRulesPath: "/nonexistent/custom-rules.conf",
	})
	if err != nil {
		t.Fatalf("WAF should start even when custom rules file is missing: %v", err)
	}

	// Base WAF should still work (no custom rules to block, but engine runs)
	tx := w.NewTransaction()
	defer tx.Close()
	tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)
	tx.ProcessURI("/api/health", "GET", "HTTP/1.1")
	tx.AddRequestHeader("Host", "localhost")
	_ = tx.ProcessRequestHeaders()
	if tx.IsInterrupted() {
		t.Fatal("normal request should not be blocked with missing custom rules")
	}
}

// TestWAFStartsWithEmptyCustomRulesPath verifies the WAF starts when
// custom rules path is empty string.
func TestWAFStartsWithEmptyCustomRulesPath(t *testing.T) {
	s := store.NewStore("")
	// Even with empty path, the default "rules/obsidian-custom.conf" kicks in.
	// If that also doesn't exist, it should still not crash.
	w, err := NewWAF(s, Config{
		CRSEnabled:      false,
		CustomRulesPath: "", // will default to rules/obsidian-custom.conf
	})
	// This might or might not find the file depending on cwd, but should never crash.
	if err != nil {
		t.Fatalf("WAF should start even with empty custom rules path: %v", err)
	}
	if w == nil {
		t.Fatal("WAF instance should not be nil")
	}
}

// TestWAFWithCRSBlocksSQLi verifies CRS rules are active when properly configured.
func TestWAFWithCRSBlocksSQLi(t *testing.T) {
	crsPath := `C:\owasp-crs`
	if _, err := os.Stat(crsPath); err != nil {
		t.Skipf("CRS not installed at %s", crsPath)
	}
	s := store.NewStore("")
	w, err := NewWAF(s, Config{
		CRSEnabled:      true,
		CRSPath:         crsPath,
		CRSMode:         "On",
		CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf"),
	})
	if err != nil {
		t.Fatalf("WAF should start with CRS: %v", err)
	}

	tests := []struct {
		name    string
		uri     string
		blocked bool
	}{
		{"SQLi", "/?id=1%27%20OR%20%271%27=%271", true},
		{"XSS", "/?q=%3Cscript%3Ealert(1)%3C/script%3E", true},
		{"Normal", "/api/health", false},
		{"Custom rule", "/?attack=test", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := w.NewTransaction()
			defer tx.Close()
			tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)

			req, _ := http.NewRequest("GET", "http://localhost:8082"+tt.uri, nil)
			tx.ProcessURI(req.URL.RequestURI(), "GET", "HTTP/1.1")
			tx.AddRequestHeader("Host", "localhost")
			tx.AddRequestHeader("User-Agent", "Mozilla/5.0")
			for k, vs := range req.URL.Query() {
				for _, v := range vs {
					tx.AddGetRequestArgument(k, v)
				}
			}
			_ = tx.ProcessRequestHeaders()

			// CRS uses anomaly scoring: rules in phase 1 increment the score,
			// but the blocking evaluation happens in phase 2. We must process
			// the request body to trigger the phase 2 blocking rules.
			if !tx.IsInterrupted() {
				_, _ = tx.ProcessRequestBody()
			}

			if tt.blocked && !tx.IsInterrupted() {
				t.Errorf("expected %s to be blocked", tt.name)
			}
			if !tt.blocked && tx.IsInterrupted() {
				t.Errorf("expected %s to pass, got interrupted: %+v", tt.name, tx.Interruption())
			}
		})
	}
}

// TestWAFNormalTrafficAlwaysPasses verifies legitimate requests pass
// regardless of CRS configuration.
func TestWAFNormalTrafficAlwaysPasses(t *testing.T) {
	configs := []struct {
		name string
		cfg  Config
	}{
		{"no_CRS", Config{CRSEnabled: false, CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf")}},
		{"CRS_missing_path", Config{CRSEnabled: true, CRSPath: "/nonexistent", CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf")}},
		{"CRS_empty_path", Config{CRSEnabled: true, CRSPath: "", CustomRulesPath: filepath.Join("..", "..", "..", "rules", "obsidian-custom.conf")}},
		{"no_custom_rules", Config{CRSEnabled: false, CustomRulesPath: "/nonexistent.conf"}},
	}

	normalPaths := []string{"/", "/api/health", "/api/stats", "/login.html", "/assets/js/app.js"}

	for _, cc := range configs {
		t.Run(cc.name, func(t *testing.T) {
			s := store.NewStore("")
			w, err := NewWAF(s, cc.cfg)
			if err != nil {
				t.Fatalf("WAF should start in config %s: %v", cc.name, err)
			}
			for _, path := range normalPaths {
				tx := w.NewTransaction()
				tx.ProcessConnection("127.0.0.1", 12345, "127.0.0.1", 8082)
				tx.ProcessURI(path, "GET", "HTTP/1.1")
				tx.AddRequestHeader("Host", "localhost")
				tx.AddRequestHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
				_ = tx.ProcessRequestHeaders()
				if tx.IsInterrupted() {
					t.Errorf("[%s] normal request to %s was blocked", cc.name, path)
				}
				tx.Close()
			}
		})
	}
}
