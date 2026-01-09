// Package adapter contains connectors to external systems
package adapter

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresAdapter handles database operations
type PostgresAdapter struct {
	pool *pgxpool.Pool
}

// NewPostgresAdapter creates a new PostgreSQL adapter
func NewPostgresAdapter(connString string) (*PostgresAdapter, error) {
	pool, err := pgxpool.New(context.Background(), connString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	return &PostgresAdapter{pool: pool}, nil
}

// Close closes the database connection
func (p *PostgresAdapter) Close() {
	p.pool.Close()
}

// StoreAuditLog stores an audit log entry
func (p *PostgresAdapter) StoreAuditLog(tenantID, time, ruleID, severity, message, logType string) error {
	_, err := p.pool.Exec(context.Background(),
		"INSERT INTO audit_logs (tenant_id, time, rule_id, severity, message, type) VALUES ($1, $2, $3, $4, $5, $6)",
		tenantID, time, ruleID, severity, message, logType)
	return err
}

// GetAuditLogs retrieves audit logs with pagination
func (p *PostgresAdapter) GetAuditLogs(tenantID string, limit, offset int) ([]map[string]interface{}, error) {
	rows, err := p.pool.Query(context.Background(),
		"SELECT time, rule_id, severity, message, type FROM audit_logs WHERE tenant_id = $1 ORDER BY time DESC LIMIT $2 OFFSET $3",
		tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []map[string]interface{}
	for rows.Next() {
		var time, ruleID, severity, message, logType string
		err := rows.Scan(&time, &ruleID, &severity, &message, &logType)
		if err != nil {
			return nil, err
		}
		logs = append(logs, map[string]interface{}{
			"time":     time,
			"rule_id":  ruleID,
			"severity": severity,
			"message":  message,
			"type":     logType,
		})
	}
	return logs, nil
}

// GetStats retrieves attack statistics
func (p *PostgresAdapter) GetStats(tenantID string) (map[string]int, error) {
	row := p.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) as sqli FROM audit_logs WHERE tenant_id = $1 AND type = 'SQLI'", tenantID)
	var sqli int
	err := row.Scan(&sqli)
	if err != nil {
		return nil, err
	}

	row = p.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) as body FROM audit_logs WHERE tenant_id = $1 AND type = 'BODY'", tenantID)
	var body int
	err = row.Scan(&body)
	if err != nil {
		return nil, err
	}

	return map[string]int{"sqli": sqli, "body": body}, nil
}