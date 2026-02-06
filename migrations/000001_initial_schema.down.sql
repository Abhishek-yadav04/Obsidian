-- ============================================================================
-- OBSIDIAN SENTINEL WAF - Rollback Schema v2.2.4
-- WARNING: This will DELETE ALL DATA
-- ============================================================================

-- Drop indexes first
DROP INDEX IF EXISTS idx_threat_intel_blocked;
DROP INDEX IF EXISTS idx_threat_intel_ip;
DROP INDEX IF EXISTS idx_attack_logs_client_ip;
DROP INDEX IF EXISTS idx_attack_logs_time;
DROP INDEX IF EXISTS idx_audit_logs_severity;
DROP INDEX IF EXISTS idx_audit_logs_time;
DROP INDEX IF EXISTS idx_sessions_expires;
DROP INDEX IF EXISTS idx_sessions_user_id;

-- Drop tables in dependency order (sessions references users)
DROP TABLE IF EXISTS threat_intel;
DROP TABLE IF EXISTS stats;
DROP TABLE IF EXISTS waf_rules;
DROP TABLE IF EXISTS attack_logs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;