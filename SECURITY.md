# Security Policy

## Obsidian Sentinel WAF Security

Obsidian Sentinel is a security-focused application. We take security vulnerabilities seriously and appreciate responsible disclosure.

## Supported Versions

Versions currently being supported with security updates.

| Version | Supported          | Status        |
| ------- | ------------------ | ------------- |
| v2.x    | :white_check_mark: | Current       |
| v1.x    | :x:                | Deprecated    |

## Security Features

Obsidian Sentinel v2.0.0 includes the following security measures:

### Authentication & Authorization
- **HMAC-SHA256 JWT Signing**: Cryptographically secure token generation
- **Environment-Based Secrets**: JWT secrets via `OBSIDIAN_JWT_SECRET` environment variable
- **Role-Based Access Control**: Admin, Analyst, Viewer permission levels
- **bcrypt Password Hashing**: Cost factor 12 for password storage

### Network Security
- **Rate Limiting**: Sliding window algorithm to prevent abuse
- **Threat Intelligence**: Real-time IP reputation checking
- **WebSocket Origin Validation**: Configurable allowed origins
- **Security Headers**: CSP, X-Frame-Options, X-Content-Type-Options

### Data Protection
- **Atomic File Writes**: Crash-safe data persistence
- **No Plaintext Secrets**: All sensitive data uses environment variables
- **Audit Logging**: Complete trail of security events

## Reporting a Vulnerability

### How to Report

**Do NOT report security vulnerabilities through public GitHub issues.**

Please report security vulnerabilities by:
1. Email: security@obsidian-waf.local (placeholder - update with real email)
2. GitHub Security Advisory: [Create new advisory](https://github.com/yourusername/obsidian/security/advisories/new)

### What to Include

- Type of issue (e.g., authentication bypass, injection, XSS)
- Full paths of source files related to the issue
- Location of affected source code (tag/branch/commit or direct URL)
- Step-by-step instructions to reproduce
- Proof-of-concept or exploit code (if possible)
- Impact of the issue and how an attacker might exploit it

### Response Timeline

- **Initial Response**: Within 48 hours
- **Assessment**: Within 7 days
- **Fix Development**: Within 30 days (for critical issues)
- **Public Disclosure**: 90 days after initial report

## Security Best Practices for Deployment

### Environment Configuration
```bash
# Set a strong JWT secret (min 32 characters)
export OBSIDIAN_JWT_SECRET="$(openssl rand -base64 32)"

# Restrict WebSocket origins
export OBSIDIAN_ALLOWED_ORIGINS="https://your-domain.com"
```

### Network Configuration
- Deploy behind a reverse proxy (nginx, Caddy)
- Enable HTTPS/TLS termination at the proxy
- Restrict direct access to port 8082
- Enable firewall rules for allowed IP ranges

### Operational Security
- Change default credentials immediately
- Enable audit logging
- Monitor `/api/metrics` for anomalies
- Regularly update threat intelligence feeds
- Review blocked requests in dashboard

## Known Security Considerations

### Token Storage
JWT tokens are stored in browser localStorage. For high-security environments, consider:
- Shorter token expiration times
- HTTP-only cookie storage (requires code modification)
- Additional session binding

### Development Mode
Running with `-dev` flag disables some security features. **Never use in production.**

## Security Audit History

| Date | Auditor | Findings | Status |
|------|---------|----------|--------|
| 2024-01-31 | Internal Review | JWT signature placeholder | Fixed in v2.0.0 |
| 2024-01-31 | Internal Review | Mock data removal | Fixed in v2.0.0 |
| 2024-01-31 | Internal Review | Race condition in persistence | Fixed in v2.0.0 |

## 🏆 Hall of Fame 🏆

Security researchers who have responsibly disclosed vulnerabilities:

1. *Your name could be here!*

---

Thank you for helping keep Obsidian Sentinel and our users safe!
