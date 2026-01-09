-- Create audit_logs table
CREATE TABLE IF NOT EXISTS audit_logs (
    id SERIAL PRIMARY KEY,
    time TIMESTAMP NOT NULL,
    rule_id VARCHAR(50),
    severity VARCHAR(20),
    message TEXT,
    type VARCHAR(20)
);

-- Create users table for RBAC
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(20) NOT NULL -- viewer, admin, system
);