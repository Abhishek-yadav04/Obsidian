package report

import (
	"bytes"
	"fmt"
	"time"

	"github.com/corazawaf/coraza/v3/internal/app/model"
)

// Generator creates PDF reports
type Generator struct{}

const (
	textBoxTop    = "┌──────────────────────────────────────────────────────────────┐\n"
	textBoxMid    = "├──────────────────────────────────────────────────────────────┤\n"
	textBoxBottom = "└──────────────────────────────────────────────────────────────┘\n\n"
)

// NewGenerator creates a new report generator
func NewGenerator() *Generator {
	return &Generator{}
}

// GeneratePDF creates a PDF report from WAF data
// Note: This is a simplified text-based PDF structure
// In production, use a library like gofpdf or pdfcpu
func (g *Generator) GeneratePDF(stats model.Stats, logs []model.LogEntry, rules []model.Rule) ([]byte, error) {
	var buf bytes.Buffer

	// PDF Header
	buf.WriteString("%PDF-1.4\n")

	// Catalog object
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Pages object
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	// Page object
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	// Content stream
	content := g.generateContent(stats, logs, rules)
	buf.WriteString(fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content))

	// Font object
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	// Cross-reference table
	buf.WriteString("xref\n0 6\n0000000000 65535 f \n")

	// Trailer
	buf.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n0\n%%EOF\n")

	return buf.Bytes(), nil
}

// generateContent creates the PDF content stream
func (g *Generator) generateContent(stats model.Stats, logs []model.LogEntry, rules []model.Rule) string {
	var buf bytes.Buffer

	y := 750 // Start from top

	// Title
	buf.WriteString(fmt.Sprintf("BT /F1 24 Tf 50 %d Td (OBSIDIAN SENTINEL WAF) Tj ET\n", y))
	y -= 30
	buf.WriteString(fmt.Sprintf("BT /F1 14 Tf 50 %d Td (Security Report) Tj ET\n", y))
	y -= 20
	buf.WriteString(fmt.Sprintf("BT /F1 10 Tf 50 %d Td (Generated: %s) Tj ET\n", y, time.Now().Format("2006-01-02 15:04:05")))
	y -= 40

	// Statistics Section
	buf.WriteString(fmt.Sprintf("BT /F1 14 Tf 50 %d Td (STATISTICS) Tj ET\n", y))
	y -= 20
	buf.WriteString(fmt.Sprintf("BT /F1 10 Tf 50 %d Td (Total Requests: %d) Tj ET\n", y, stats.TotalRequests))
	y -= 15
	buf.WriteString(fmt.Sprintf("BT /F1 10 Tf 50 %d Td (Blocked Requests: %d) Tj ET\n", y, stats.BlockedRequests))
	y -= 15
	buf.WriteString(fmt.Sprintf("BT /F1 10 Tf 50 %d Td (Flagged Requests: %d) Tj ET\n", y, stats.FlaggedRequests))
	y -= 15
	buf.WriteString(fmt.Sprintf("BT /F1 10 Tf 50 %d Td (Safe Requests: %d) Tj ET\n", y, stats.SafeRequests))
	y -= 15
	buf.WriteString(fmt.Sprintf("BT /F1 10 Tf 50 %d Td (Active Rules: %d) Tj ET\n", y, stats.ActiveRulesCount))
	y -= 30

	// Rules Section
	buf.WriteString(fmt.Sprintf("BT /F1 14 Tf 50 %d Td (ACTIVE RULES) Tj ET\n", y))
	y -= 20
	for i, rule := range rules {
		if i >= 10 || y < 100 {
			break
		}
		text := fmt.Sprintf("Rule %d: %s [%s]", rule.ID, truncate(rule.Description, 40), rule.Severity)
		buf.WriteString(fmt.Sprintf("BT /F1 9 Tf 50 %d Td (%s) Tj ET\n", y, escapePDF(text)))
		y -= 12
	}
	y -= 20

	// Recent Events Section
	buf.WriteString(fmt.Sprintf("BT /F1 14 Tf 50 %d Td (RECENT SECURITY EVENTS) Tj ET\n", y))
	y -= 20
	for i, log := range logs {
		if i >= 15 || y < 100 {
			break
		}
		text := fmt.Sprintf("%s - Rule %d: %s", log.Timestamp.Format("15:04:05"), log.RuleID, truncate(log.Details, 50))
		buf.WriteString(fmt.Sprintf("BT /F1 8 Tf 50 %d Td (%s) Tj ET\n", y, escapePDF(text)))
		y -= 10
	}

	// Footer
	buf.WriteString("BT /F1 8 Tf 50 30 Td (Obsidian Sentinel WAF - Enterprise Security Report - Page 1) Tj ET\n")

	return buf.String()
}

// GenerateTextReport creates a plain text report (fallback)
func (g *Generator) GenerateTextReport(stats model.Stats, logs []model.LogEntry, rules []model.Rule) string {
	var buf bytes.Buffer

	buf.WriteString("╔══════════════════════════════════════════════════════════════╗\n")
	buf.WriteString("║          OBSIDIAN SENTINEL WAF - SECURITY REPORT             ║\n")
	buf.WriteString("╚══════════════════════════════════════════════════════════════╝\n\n")
	buf.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	buf.WriteString(textBoxTop)
	buf.WriteString("│                        STATISTICS                            │\n")
	buf.WriteString(textBoxMid)
	buf.WriteString(fmt.Sprintf("│ Total Requests:    %-40d │\n", stats.TotalRequests))
	buf.WriteString(fmt.Sprintf("│ Blocked Requests:  %-40d │\n", stats.BlockedRequests))
	buf.WriteString(fmt.Sprintf("│ Flagged Requests:  %-40d │\n", stats.FlaggedRequests))
	buf.WriteString(fmt.Sprintf("│ Safe Requests:     %-40d │\n", stats.SafeRequests))
	buf.WriteString(fmt.Sprintf("│ Active Rules:      %-40d │\n", stats.ActiveRulesCount))
	buf.WriteString(textBoxBottom)

	buf.WriteString(textBoxTop)
	buf.WriteString("│                       ACTIVE RULES                           │\n")
	buf.WriteString(textBoxMid)
	for _, rule := range rules {
		status := "ENABLED"
		if !rule.Enabled {
			status = "DISABLED"
		}
		buf.WriteString(fmt.Sprintf("│ [%d] %-35s [%-8s] │\n",
			rule.ID, truncate(rule.Description, 35), status))
	}
	buf.WriteString(textBoxBottom)

	buf.WriteString(textBoxTop)
	buf.WriteString("│                   RECENT SECURITY EVENTS                     │\n")
	buf.WriteString(textBoxMid)
	for i, log := range logs {
		if i >= 20 {
			buf.WriteString("│ ... (truncated)                                              │\n")
			break
		}
		buf.WriteString(fmt.Sprintf("│ %s Rule %-6d %-35s │\n",
			log.Timestamp.Format("15:04"),
			log.RuleID,
			truncate(log.Action+" - "+log.Details, 35)))
	}
	buf.WriteString(textBoxBottom)

	buf.WriteString("════════════════════════════════════════════════════════════════\n")
	buf.WriteString("           End of Report - Obsidian Sentinel WAF\n")
	buf.WriteString("════════════════════════════════════════════════════════════════\n")

	return buf.String()
}

// Helper functions
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func escapePDF(s string) string {
	// Escape special PDF characters
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', ')', '\\':
			result = append(result, '\\', s[i])
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}
