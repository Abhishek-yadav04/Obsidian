package report

import (
	"testing"
	"time"

	"github.com/corazawaf/coraza/v3/internal/app/model"
)

func TestEnterprisePDFGenerator(t *testing.T) {
	// Create test data
	stats := model.Stats{
		TotalRequests:    1000,
		BlockedRequests:  50,
		FlaggedRequests:  25,
		SafeRequests:     925,
		ActiveRulesCount: 150,
	}

	logs := []model.LogEntry{
		{
			Timestamp: time.Now(),
			RuleID:    1,
			Action:    "BLOCK",
			Details:   "SQL injection attempt detected",
		},
		{
			Timestamp: time.Now().Add(-time.Hour),
			RuleID:    2,
			Action:    "LOG",
			Details:   "Suspicious request pattern",
		},
	}

	rules := []model.Rule{
		{ID: 1, Description: "SQL Injection Protection", Severity: "HIGH", Enabled: true},
		{ID: 2, Description: "XSS Protection", Severity: "MEDIUM", Enabled: true},
		{ID: 3, Description: "File Upload Protection", Severity: "HIGH", Enabled: false},
	}

	// Test enterprise PDF generator
	gen := NewEnterprisePDFGenerator("Test Organization", "CONFIDENTIAL")

	config := PDFSecurityConfig{
		OwnerPassword:   "test123",
		UserPassword:    "",
		AllowPrinting:   true,
		AllowCopying:    false,
		AllowModifying:  false,
		EncryptionLevel: 128,
		WatermarkText:   "CONFIDENTIAL - Test Organization",
		ExpirationHours: 168,
	}

	pdfData, err := gen.GenerateSecurePDF(stats, logs, rules, config)
	if err != nil {
		t.Fatalf("Failed to generate enterprise PDF: %v", err)
	}

	if len(pdfData) == 0 {
		t.Error("Generated PDF is empty")
	}

	// Check for security features in PDF
	pdfStr := string(pdfData)
	if !contains(pdfStr, "CONFIDENTIAL") {
		t.Error("PDF does not contain classification marking")
	}
	if !contains(pdfStr, "Test Organization") {
		t.Error("PDF does not contain organization name")
	}
	if !contains(pdfStr, "Audit Trail") {
		t.Error("PDF does not contain audit trail")
	}

	t.Logf("Successfully generated enterprise PDF with %d bytes", len(pdfData))
}

func TestSecurityRatingCalculation(t *testing.T) {
	gen := NewEnterprisePDFGenerator("Test", "CONFIDENTIAL")

	stats := model.Stats{
		TotalRequests:    1000,
		BlockedRequests:  10, // 1% block rate - good
		SafeRequests:     990,
		ActiveRulesCount: 100, // Good rule coverage
	}

	logs := []model.LogEntry{} // No recent threats
	rules := make([]model.Rule, 100)
	for i := range rules {
		rules[i] = model.Rule{ID: i + 1, Enabled: true}
	}

	rating := gen.calculateEnterpriseSecurityRating(stats, logs, rules)

	if rating.Score < 90 {
		t.Errorf("Expected high security score, got %d", rating.Score)
	}

	if rating.Grade != "A" && rating.Grade != "A+" {
		t.Errorf("Expected excellent grade, got %s", rating.Grade)
	}

	t.Logf("Security Rating: Score=%d, Grade=%s, Status=%s",
		rating.Score, rating.Grade, rating.Status)
}

func TestInputValidation(t *testing.T) {
	gen := NewEnterprisePDFGenerator("Test", "CONFIDENTIAL")

	// Test invalid stats
	invalidStats := model.Stats{
		TotalRequests:   -1, // Invalid
		BlockedRequests: 50,
		SafeRequests:    950,
	}

	logs := []model.LogEntry{}
	rules := []model.Rule{}

	err := gen.validateInputData(invalidStats, logs, rules)
	if err == nil {
		t.Error("Expected validation error for negative total requests")
	}

	// Test invalid log entry
	invalidLogs := []model.LogEntry{
		{RuleID: -1}, // Invalid rule ID
	}

	err = gen.validateInputData(model.Stats{TotalRequests: 100}, invalidLogs, rules)
	if err == nil {
		t.Error("Expected validation error for negative rule ID")
	}

	t.Log("Input validation working correctly")
}

func TestLegacyGenerator(t *testing.T) {
	gen := NewGenerator()

	stats := model.Stats{TotalRequests: 100, BlockedRequests: 5}
	logs := []model.LogEntry{}
	rules := []model.Rule{}

	// Test PDF generation
	pdfData, err := gen.GeneratePDF(stats, logs, rules)
	if err != nil {
		t.Fatalf("Legacy PDF generation failed: %v", err)
	}

	if len(pdfData) == 0 {
		t.Error("Legacy PDF is empty")
	}

	// Test text report generation
	textReport := gen.GenerateTextReport(stats, logs, rules)
	if len(textReport) == 0 {
		t.Error("Text report is empty")
	}

	if !contains(textReport, "OBSIDIAN SENTINEL WAF") {
		t.Error("Text report does not contain expected header")
	}

	t.Logf("Legacy generator working - PDF: %d bytes, Text: %d chars",
		len(pdfData), len(textReport))
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			func() bool {
				for i := 1; i <= len(s)-len(substr); i++ {
					if s[i:i+len(substr)] == substr {
						return true
					}
				}
				return false
			}()))
}
