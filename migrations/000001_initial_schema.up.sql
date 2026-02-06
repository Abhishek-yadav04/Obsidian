-- ============================================================================
-- OBSIDIAN SENTINEL WAF - Database Schema v2.2.4
-- PostgreSQL 12+ Compatible
-- ============================================================================

-- =============================================================================
-- USERS TABLE - Authentication and RBAC
-- =============================================================================
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(50) UNIQUE NOT NULL,
    email VARCHAR(255),
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'Viewer',  -- Admin, Analyst, Viewer
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Default users are provisioned by the application bootstrap process.
-- Credentials are read from environment variables: DEFAULT_ADMIN_PW, DEFAULT_ANALYST_PW, DEFAULT_VIEWER_PW.
-- If no env vars are set, temporary random passwords are generated and logged at first startup.
-- All default accounts require a password change on first login.

-- =============================================================================
-- SESSIONS TABLE - JWT Token Management
-- =============================================================================
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(255) PRIMARY KEY,
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    ip_address VARCHAR(45),
    user_agent TEXT
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- =============================================================================
-- AUDIT LOGS TABLE - Security Event Trail
-- =============================================================================
CREATE TABLE IF NOT EXISTS audit_logs (
    id SERIAL PRIMARY KEY,
    time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    rule_id VARCHAR(50),
    severity VARCHAR(20),
    message TEXT,
    type VARCHAR(20),
    client_ip VARCHAR(45),
    request_uri TEXT,
    user_agent TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_time ON audit_logs(time DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_severity ON audit_logs(severity);

-- =============================================================================
-- ATTACK LOGS TABLE - WAF Attack Records
-- =============================================================================
CREATE TABLE IF NOT EXISTS attack_logs (
    id SERIAL PRIMARY KEY,
    time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    client_ip VARCHAR(45) NOT NULL,
    method VARCHAR(10),
    uri TEXT,
    rule_id VARCHAR(50),
    rule_msg TEXT,
    severity VARCHAR(20),
    action VARCHAR(20),
    request_headers JSONB,
    response_status INTEGER
);

CREATE INDEX IF NOT EXISTS idx_attack_logs_time ON attack_logs(time DESC);
CREATE INDEX IF NOT EXISTS idx_attack_logs_client_ip ON attack_logs(client_ip);

-- =============================================================================
-- WAF RULES TABLE - Custom Rule Storage
-- =============================================================================
CREATE TABLE IF NOT EXISTS waf_rules (
    id SERIAL PRIMARY KEY,
    rule_id INTEGER UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    pattern TEXT NOT NULL,
    phase INTEGER DEFAULT 1,
    severity VARCHAR(20) DEFAULT 'medium',
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- =============================================================================
-- STATS TABLE - Application Statistics
-- =============================================================================
CREATE TABLE IF NOT EXISTS stats (
    id SERIAL PRIMARY KEY,
    stat_name VARCHAR(100) UNIQUE NOT NULL,
    stat_value BIGINT DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Initialize stats
INSERT INTO stats (stat_name, stat_value) VALUES
    ('total_requests', 0),
    ('blocked_requests', 0),
    ('threats_detected', 0)
ON CONFLICT (stat_name) DO NOTHING;

-- =============================================================================
-- THREAT INTELLIGENCE TABLE - IP Reputation
-- =============================================================================
CREATE TABLE IF NOT EXISTS threat_intel (
    id SERIAL PRIMARY KEY,
    ip_address VARCHAR(45) UNIQUE NOT NULL,
    threat_level VARCHAR(20) NOT NULL,
    source VARCHAR(100),
    reason TEXT,
    blocked BOOLEAN DEFAULT false,
    first_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    hit_count INTEGER DEFAULT 1
);

-- idx_threat_intel_ip is not needed: the UNIQUE constraint on ip_address already creates an implicit index.
CREATE INDEX IF NOT EXISTS idx_threat_intel_blocked ON threat_intel(blocked) WHERE blocked = true;