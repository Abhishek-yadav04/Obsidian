// Package graphql provides GraphQL-specific security rules for Obsidian WAF.
// Protects against GraphQL-specific attacks like query depth, complexity, and introspection abuse.
package graphql

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var (
	ErrQueryTooDeep         = errors.New("GraphQL query depth exceeds limit")
	ErrQueryTooComplex      = errors.New("GraphQL query complexity exceeds limit")
	ErrIntrospectionBlocked = errors.New("GraphQL introspection is disabled")
	ErrBatchingDisabled     = errors.New("GraphQL query batching is disabled")
	ErrFieldTooLong         = errors.New("GraphQL field alias too long")
	ErrTooManyAliases       = errors.New("too many field aliases in query")
	ErrMalformedQuery       = errors.New("malformed GraphQL query")
)

// Config for GraphQL security
type Config struct {
	// Enabled turns GraphQL protection on/off
	Enabled bool `json:"enabled"`

	// MaxDepth limits query nesting depth (default: 10)
	MaxDepth int `json:"max_depth"`

	// MaxComplexity limits calculated query complexity (default: 1000)
	MaxComplexity int `json:"max_complexity"`

	// BlockIntrospection blocks __schema and __type queries
	BlockIntrospection bool `json:"block_introspection"`

	// AllowBatching allows multiple queries in one request
	AllowBatching bool `json:"allow_batching"`

	// MaxBatchSize maximum queries per batch (if batching allowed)
	MaxBatchSize int `json:"max_batch_size"`

	// MaxAliases limits number of field aliases
	MaxAliases int `json:"max_aliases"`

	// MaxFieldLength limits alias/field name length
	MaxFieldLength int `json:"max_field_length"`

	// FieldComplexityMap custom complexity weights per field
	FieldComplexityMap map[string]int `json:"field_complexity_map"`
}

// DefaultConfig returns production-safe defaults
func DefaultConfig() *Config {
	return &Config{
		Enabled:            true,
		MaxDepth:           10,
		MaxComplexity:      1000,
		BlockIntrospection: true, // Block in production
		AllowBatching:      false,
		MaxBatchSize:       5,
		MaxAliases:         20,
		MaxFieldLength:     64,
		FieldComplexityMap: map[string]int{
			"users":        10,
			"allUsers":     50,
			"transactions": 20,
			"logs":         15,
		},
	}
}

// GraphQLRequest represents a parsed GraphQL request
type GraphQLRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName,omitempty"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
}

// Analyzer performs GraphQL query analysis
type Analyzer struct {
	config *Config
}

// NewAnalyzer creates a new GraphQL analyzer
func NewAnalyzer(config *Config) *Analyzer {
	if config == nil {
		config = DefaultConfig()
	}
	return &Analyzer{config: config}
}

// AnalysisResult contains the result of query analysis
type AnalysisResult struct {
	IsValid          bool     `json:"is_valid"`
	Error            error    `json:"error,omitempty"`
	Depth            int      `json:"depth"`
	Complexity       int      `json:"complexity"`
	AliasCount       int      `json:"alias_count"`
	FieldCount       int      `json:"field_count"`
	IsBatched        bool     `json:"is_batched"`
	BatchSize        int      `json:"batch_size"`
	HasIntrospection bool     `json:"has_introspection"`
	Warnings         []string `json:"warnings,omitempty"`
}

// AnalyzeBody parses and analyzes a GraphQL request body
func (a *Analyzer) AnalyzeBody(body []byte) (*AnalysisResult, error) {
	if !a.config.Enabled {
		return &AnalysisResult{IsValid: true}, nil
	}

	result := &AnalysisResult{
		IsValid:  true,
		Warnings: make([]string, 0),
	}

	// Try to parse as single request
	var req GraphQLRequest
	if err := json.Unmarshal(body, &req); err == nil {
		return a.analyzeQuery(req.Query, result)
	}

	// Try to parse as batch request
	var batch []GraphQLRequest
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, ErrMalformedQuery
	}

	result.IsBatched = true
	result.BatchSize = len(batch)

	// Check batching rules
	if !a.config.AllowBatching {
		result.IsValid = false
		result.Error = ErrBatchingDisabled
		return result, nil
	}

	if len(batch) > a.config.MaxBatchSize {
		result.IsValid = false
		result.Error = errors.New("batch size exceeds limit")
		return result, nil
	}

	// Analyze each query in batch
	totalComplexity := 0
	maxDepth := 0

	for _, req := range batch {
		queryResult, err := a.analyzeQuery(req.Query, &AnalysisResult{IsValid: true})
		if err != nil {
			return nil, err
		}

		if !queryResult.IsValid {
			return queryResult, nil
		}

		totalComplexity += queryResult.Complexity
		if queryResult.Depth > maxDepth {
			maxDepth = queryResult.Depth
		}
		result.AliasCount += queryResult.AliasCount
		result.FieldCount += queryResult.FieldCount
	}

	result.Complexity = totalComplexity
	result.Depth = maxDepth

	// Check total complexity for batch
	if result.Complexity > a.config.MaxComplexity {
		result.IsValid = false
		result.Error = ErrQueryTooComplex
	}

	return result, nil
}

// analyzeQuery analyzes a single GraphQL query string
func (a *Analyzer) analyzeQuery(query string, result *AnalysisResult) (*AnalysisResult, error) {
	if query == "" {
		return result, nil
	}

	// Check for introspection
	if a.hasIntrospection(query) {
		result.HasIntrospection = true
		if a.config.BlockIntrospection {
			result.IsValid = false
			result.Error = ErrIntrospectionBlocked
			return result, nil
		}
		result.Warnings = append(result.Warnings, "introspection query detected")
	}

	// Calculate depth
	result.Depth = a.calculateDepth(query)
	if result.Depth > a.config.MaxDepth {
		result.IsValid = false
		result.Error = ErrQueryTooDeep
		return result, nil
	}

	// Count aliases and fields
	result.AliasCount = a.countAliases(query)
	if result.AliasCount > a.config.MaxAliases {
		result.IsValid = false
		result.Error = ErrTooManyAliases
		return result, nil
	}

	// Check field length
	if a.hasLongField(query) {
		result.IsValid = false
		result.Error = ErrFieldTooLong
		return result, nil
	}

	// Calculate complexity
	result.Complexity = a.calculateComplexity(query)
	if result.Complexity > a.config.MaxComplexity {
		result.IsValid = false
		result.Error = ErrQueryTooComplex
		return result, nil
	}

	// Count total fields
	result.FieldCount = a.countFields(query)

	return result, nil
}

// hasIntrospection checks for introspection queries
func (a *Analyzer) hasIntrospection(query string) bool {
	lower := strings.ToLower(query)
	return strings.Contains(lower, "__schema") ||
		strings.Contains(lower, "__type") ||
		strings.Contains(lower, "__typename")
}

// calculateDepth calculates the nesting depth of a query
func (a *Analyzer) calculateDepth(query string) int {
	maxDepth := 0
	currentDepth := 0

	for _, char := range query {
		switch char {
		case '{':
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
		case '}':
			currentDepth--
		}
	}

	return maxDepth
}

// countAliases counts the number of field aliases in a query
func (a *Analyzer) countAliases(query string) int {
	// Simple regex to match alias patterns: "aliasName: fieldName"
	aliasPattern := regexp.MustCompile(`\w+\s*:\s*\w+`)
	matches := aliasPattern.FindAllString(query, -1)
	return len(matches)
}

// hasLongField checks if any field/alias exceeds max length
func (a *Analyzer) hasLongField(query string) bool {
	fieldPattern := regexp.MustCompile(`\b(\w+)\b`)
	matches := fieldPattern.FindAllString(query, -1)

	for _, match := range matches {
		if len(match) > a.config.MaxFieldLength {
			return true
		}
	}
	return false
}

// calculateComplexity calculates query complexity based on fields and depth
func (a *Analyzer) calculateComplexity(query string) int {
	complexity := 0
	depth := a.calculateDepth(query)

	// Base complexity from depth
	complexity = depth * 10

	// Add complexity for known expensive fields
	lower := strings.ToLower(query)
	for field, weight := range a.config.FieldComplexityMap {
		if strings.Contains(lower, strings.ToLower(field)) {
			complexity += weight
		}
	}

	// Add complexity for list fields (heuristic)
	if strings.Contains(lower, "first:") || strings.Contains(lower, "last:") ||
		strings.Contains(lower, "limit:") || strings.Contains(lower, "take:") {
		complexity += 20
	}

	// Add complexity for connections/edges pattern
	if strings.Contains(lower, "edges") && strings.Contains(lower, "node") {
		complexity += 30
	}

	return complexity
}

// countFields counts the approximate number of fields in the query
func (a *Analyzer) countFields(query string) int {
	// Simple heuristic: count word-like tokens that aren't keywords
	keywords := map[string]bool{
		"query": true, "mutation": true, "subscription": true,
		"fragment": true, "on": true, "true": true, "false": true,
		"null": true, "type": true,
	}

	wordPattern := regexp.MustCompile(`\b([a-zA-Z_]\w*)\b`)
	matches := wordPattern.FindAllString(query, -1)

	count := 0
	for _, match := range matches {
		if !keywords[strings.ToLower(match)] {
			count++
		}
	}

	return count
}

// GetConfig returns the current configuration
func (a *Analyzer) GetConfig() *Config {
	return a.config
}

// SetConfig updates the configuration
func (a *Analyzer) SetConfig(config *Config) {
	a.config = config
}

// IsGraphQLRequest checks if a request appears to be GraphQL
func IsGraphQLRequest(contentType, path string, body []byte) bool {
	// Check content type
	if strings.Contains(contentType, "application/graphql") {
		return true
	}

	// Check common GraphQL paths
	lowerPath := strings.ToLower(path)
	if strings.Contains(lowerPath, "/graphql") {
		return true
	}

	// Check body structure
	if len(body) > 0 {
		trimmed := strings.TrimSpace(string(body))
		if strings.HasPrefix(trimmed, "{") {
			// Quick check for "query" key
			if strings.Contains(trimmed[:min(100, len(trimmed))], `"query"`) {
				return true
			}
		}
	}

	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
