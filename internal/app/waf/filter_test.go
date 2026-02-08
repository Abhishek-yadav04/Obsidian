package waf

import (
	"fmt"
	"os"
	"testing"
)

func TestCollectAndFilter(t *testing.T) {
	crsPath := `C:\owasp-crs`
	if _, err := os.Stat(crsPath); err != nil {
		t.Skipf("CRS not installed at %s", crsPath)
	}

	ids := collectCRSRuleIDs(crsPath)
	t.Logf("Collected %d CRS rule IDs", len(ids))

	// Check a known ID
	if _, ok := ids["913100"]; !ok {
		t.Error("Expected to find CRS rule ID 913100")
	}

	// Test the filter
	sample := `SecRule REQUEST_HEADERS:User-Agent "@rx nikto" \
    "id:913100,\
     phase:1,\
     deny,\
     status:403"

SecRule ARGS "@rx test" \
    "id:900001,\
     phase:1,\
     deny,\
     status:403"
`
	filtered := filterDuplicateRules(sample, ids)
	t.Logf("Filtered output:\n%s", filtered)

	// 913100 should be removed (it's in CRS), 900001 should remain
	if containsRuleID(filtered, "913100") {
		t.Error("913100 should have been filtered out")
	}
	if !containsRuleID(filtered, "900001") {
		t.Error("900001 should have been kept")
	}
}

func containsRuleID(text, id string) bool {
	return len(ruleIDRe.FindAllStringSubmatch(text, -1)) > 0 && func() bool {
		for _, m := range ruleIDRe.FindAllStringSubmatch(text, -1) {
			if m[1] == id {
				return true
			}
		}
		return false
	}()
}

func TestFilterWithRealCustomFile(t *testing.T) {
	crsPath := `C:\owasp-crs`
	if _, err := os.Stat(crsPath); err != nil {
		t.Skipf("CRS not installed at %s", crsPath)
	}

	customData, err := os.ReadFile(`../../../rules/obsidian-custom.conf`)
	if err != nil {
		t.Fatalf("failed to read custom rules: %v", err)
	}

	ids := collectCRSRuleIDs(crsPath)
	t.Logf("CRS IDs: %d", len(ids))

	// Count custom rule IDs before filtering
	beforeMatches := ruleIDRe.FindAllStringSubmatch(string(customData), -1)
	t.Logf("Custom rules before: %d", len(beforeMatches))

	filtered := filterDuplicateRules(string(customData), ids)

	// Count after
	afterMatches := ruleIDRe.FindAllStringSubmatch(filtered, -1)
	t.Logf("Custom rules after: %d", len(afterMatches))

	// List the remaining IDs
	for _, m := range afterMatches {
		fmt.Printf("  Kept rule ID: %s\n", m[1])
	}

	if len(afterMatches) >= len(beforeMatches) {
		t.Errorf("Expected some rules to be filtered: before=%d after=%d", len(beforeMatches), len(afterMatches))
	}
}
