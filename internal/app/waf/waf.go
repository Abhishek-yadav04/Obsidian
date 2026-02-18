package waf

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/experimental/plugins"
	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/corazawaf/coraza/v3/types"
)

type Config struct {
	CRSEnabled      bool
	CRSPath         string
	CRSMode         string // "DetectionOnly" or "On"
	CustomRulesPath string
}

func NewWAF(s *store.Store, cfg Config) (coraza.WAF, error) {
	// Register the custom logger
	plugins.RegisterAuditLogWriter("hybrid", func() plugintypes.AuditLogWriter {
		return store.NewHybridAuditLogger(s)
	})

	if cfg.CustomRulesPath == "" {
		cfg.CustomRulesPath = "rules/obsidian-custom.conf"
	}
	if cfg.CRSMode == "" {
		cfg.CRSMode = "DetectionOnly"
	}

	wafCfg := coraza.NewWAFConfig().
		WithDirectives(`
# Basic Setup
SecRequestBodyAccess On
SecResponseBodyAccess On
SecResponseBodyMimeType text/plain text/html text/xml application/json

# Audit Log Setup
SecAuditEngine RelevantOnly
SecAuditLogType hybrid
SecAuditLogRelevantStatus "^[45].."
`).
		WithErrorCallback(func(rule types.MatchedRule) {
			// This callback runs on every match
		})

	// Load Obsidian custom rules (non-fatal if file is missing)
	if cfg.CustomRulesPath != "" {
		customRulesPath := resolvePath(cfg.CustomRulesPath)
		if _, err := os.Stat(customRulesPath); err != nil {
			log.Printf("[WAF] Custom rules file not found: %s; starting with base rules only", customRulesPath)
		} else {
			customData, err := os.ReadFile(customRulesPath)
			if err != nil {
				log.Printf("[WAF] Failed to read custom rules file %s: %v; continuing without custom rules", customRulesPath, err)
			} else {
				customDirectives := string(customData)
				// When CRS is enabled, strip out any custom rules whose IDs duplicate
				// rules from the CRS files to avoid Coraza "duplicated rule id" errors.
				if cfg.CRSEnabled && cfg.CRSPath != "" {
					crsIDs := collectCRSRuleIDs(cfg.CRSPath)
					log.Printf("[WAF] Collected %d CRS rule IDs from %s for dedup", len(crsIDs), cfg.CRSPath)
					if len(crsIDs) > 0 {
						customDirectives = filterDuplicateRules(customDirectives, crsIDs)
					}
				}
				wafCfg = wafCfg.WithDirectives(customDirectives)
				log.Printf("[WAF] Loaded custom rules from %s", customRulesPath)
			}
		}
	}

	// Optional CRS integration — all failures are non-fatal.
	// The WAF always starts; CRS just adds extra protection.
	if cfg.CRSEnabled {
		if cfg.CRSPath == "" {
			log.Printf("[WAF] CRS enabled but OBSIDIAN_CRS_PATH is empty; continuing without CRS")
		} else {
			crsPath := resolvePath(cfg.CRSPath)
			if _, err := os.Stat(crsPath); err != nil {
				log.Printf("[WAF] CRS path not found: %s; continuing without CRS", crsPath)
			} else {
				// CRS path exists — load it
				mode := strings.ToLower(cfg.CRSMode)
				switch mode {
				case "on":
					wafCfg = wafCfg.WithDirectives("SecRuleEngine On")
				default:
					wafCfg = wafCfg.WithDirectives("SecRuleEngine DetectionOnly")
				}

				// Set the root FS to the CRS rules/ directory so that operators
				// like @pmFromFile can resolve their .data files.
				crsRulesDir := filepath.Join(crsPath, "rules")
				wafCfg = wafCfg.WithRootFS(os.DirFS(crsRulesDir))

				// Load CRS .conf files manually to avoid Coraza readfile/Include
				// issues with Windows paths containing spaces.
				setupPath := filepath.Join(crsPath, "crs-setup.conf")
				if setupData, err := os.ReadFile(setupPath); err == nil {
					wafCfg = wafCfg.WithDirectives(string(setupData))
					log.Printf("[WAF] Loaded CRS setup from %s", setupPath)
				} else {
					log.Printf("[WAF] CRS setup file not found: %s; continuing without CRS setup", setupPath)
				}

				rulesPattern := filepath.Join(crsPath, "rules", "*.conf")
				ruleFiles, _ := filepath.Glob(rulesPattern)
				for _, rf := range ruleFiles {
					ruleData, err := os.ReadFile(rf)
					if err != nil {
						log.Printf("[WAF] Warning: failed to read CRS rule file %s: %v", rf, err)
						continue
					}
					wafCfg = wafCfg.WithDirectives(string(ruleData))
				}
				log.Printf("[WAF] Loaded %d CRS rule files from %s", len(ruleFiles), crsPath)
			}
		}
	} else {
		// Preserve current behavior when CRS is disabled
		wafCfg = wafCfg.WithDirectives("SecRuleEngine On")
	}

	return coraza.NewWAF(wafCfg)
}

func resolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	// Prefer working directory (go run uses temp exe path)
	if wd, err := os.Getwd(); err == nil {
		cwdPath := filepath.Join(wd, path)
		if _, err := os.Stat(cwdPath); err == nil {
			return cwdPath
		}
		if root, ok := findRepoRoot(wd); ok {
			rootPath := filepath.Join(root, path)
			if _, err := os.Stat(rootPath); err == nil {
				return rootPath
			}
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return path
	}
	exeDir := filepath.Dir(exe)
	if root, ok := findRepoRoot(exeDir); ok {
		rootPath := filepath.Join(root, path)
		if _, err := os.Stat(rootPath); err == nil {
			return rootPath
		}
	}
	return filepath.Join(exeDir, path)
}

func findRepoRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ruleIDRe matches id:NNNNN inside SecRule/SecAction directives.
var ruleIDRe = regexp.MustCompile(`\bid:(\d+)`)

// collectCRSRuleIDs scans CRS .conf files and returns a set of all rule IDs.
func collectCRSRuleIDs(crsPath string) map[string]struct{} {
	ids := make(map[string]struct{})
	files, _ := filepath.Glob(filepath.Join(crsPath, "rules", "*.conf"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			for _, m := range ruleIDRe.FindAllStringSubmatch(sc.Text(), -1) {
				ids[m[1]] = struct{}{}
			}
		}
		fh.Close()
	}
	return ids
}

// filterDuplicateRules removes complete SecRule/SecAction blocks from the
// directive text whose id:NNNNN value appears in the duplicates set.
// Multi-line blocks (lines ending with \) are handled correctly.
func filterDuplicateRules(directives string, duplicates map[string]struct{}) string {
	var result strings.Builder
	lines := strings.Split(directives, "\n")
	i := 0
	skipped := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check if this is the start of a SecRule/SecAction block.
		if strings.HasPrefix(trimmed, "SecRule") || strings.HasPrefix(trimmed, "SecAction") {
			// Collect the entire block (follows \ continuations).
			blockStart := i
			for i < len(lines) && strings.HasSuffix(strings.TrimSpace(lines[i]), "\\") {
				i++
			}
			i++ // include the final line (no trailing \)
			block := strings.Join(lines[blockStart:i], "\n")

			// Extract the rule ID from the block.
			m := ruleIDRe.FindStringSubmatch(block)
			if m != nil {
				if _, dup := duplicates[m[1]]; dup {
					skipped++
					continue // skip the entire block
				}
			}
			result.WriteString(block)
			result.WriteByte('\n')
		} else {
			result.WriteString(line)
			result.WriteByte('\n')
			i++
		}
	}
	if skipped > 0 {
		log.Printf("[WAF] Filtered %d custom rules that duplicate CRS rule IDs", skipped)
	}
	return result.String()
}
