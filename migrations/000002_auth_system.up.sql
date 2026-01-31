-- users table
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'Viewer', -- Admin, Analyst, Viewer
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login TIMESTAMP,
    CONSTRAINT valid_role CHECK (role IN ('Admin', 'Analyst', 'Viewer'))
);

-- sessions table
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(64) PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL, -- JWT refresh token
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_activity TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ip_address VARCHAR(45),
    user_agent TEXT,
    INDEX idx_user_id (user_id),
    INDEX idx_expires_at (expires_at)
);

-- audit_logs table
CREATE TABLE IF NOT EXISTS audit_logs (
    id SERIAL PRIMARY KEY,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(100) NOT NULL,
    resource VARCHAR(255) NOT NULL,
    details TEXT,
    ip_address VARCHAR(45),
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_user_id (user_id),
    INDEX idx_timestamp (timestamp),
    INDEX idx_action (action)
);

-- waf_rules table (for persistent rule storage)
CREATE TABLE IF NOT EXISTS waf_rules (
    id INTEGER PRIMARY KEY,
    description TEXT NOT NULL,
    severity VARCHAR(20) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    category VARCHAR(50) NOT NULL,
    rule_content TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_by INTEGER REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_enabled (enabled),
    INDEX idx_category (category)
);

-- security_events table (for WAF attack logs)
CREATE TABLE IF NOT EXISTS security_events (
    id VARCHAR(64) PRIMARY KEY,
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    client_ip VARCHAR(45) NOT NULL,
    method VARCHAR(10) NOT NULL,
    uri TEXT NOT NULL,
    rule_id INTEGER,
    action VARCHAR(20) NOT NULL,
    details TEXT,
    severity VARCHAR(20),
    user_agent TEXT,
    INDEX idx_timestamp (timestamp),
    INDEX idx_client_ip (client_ip),
    INDEX idx_rule_id (rule_id),
    INDEX idx_action (action)
);

-- Create default admin user (password: Admin@123)
INSERT INTO users (username, password_hash, email, role, enabled) 
VALUES ('admin', '$2a$10$rN8xJZV4yXz.XY7K5jK5S.eKGZJ8vZ7YJqJK5jK5S.eKGZJ8vZ7YJ', 'admin@obsidian.local', 'Admin', true)
ON CONFLICT (username) DO NOTHING;
