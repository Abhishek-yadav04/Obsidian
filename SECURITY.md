# Security Policy

## Obsidian Sentinel WAF v2.1.0 Enterprise Edition

Obsidian Sentinel is a security-focused application designed to protect web applications. We take security vulnerabilities seriously and appreciate responsible disclosure from the security community.

## Supported Versions

Versions currently being supported with security updates.

| Version | Supported          | Status        | End of Life   |
| ------- | ------------------ | ------------- | ------------- |
| v3.x    | :white_check_mark: | Current       | TBD           |
| v2.x    | :white_check_mark: | LTS Support   | 2026-12-31    |
| v1.x    | :x:                | Deprecated    | 2024-12-31    |

## Enterprise Security Features

Obsidian Sentinel v2.1.0 includes comprehensive enterprise-grade security measures:

### Authentication & Authorization
- **Enhanced JWT Security**: HMAC-SHA256 signed tokens with improved entropy generation
- **Role-Based Access Control**: Granular permissions across 12+ distinct capabilities
- **Multi-Layer Authorization**: API-level, resource-level, and action-level permissions
- **Session Management**: Redis-backed session tracking with automatic cleanup
- **Password Security**: bcrypt cost factor 12 with password complexity requirements
- **Token Refresh**: Automatic token rotation with secure refresh mechanisms

### Network & Traffic Protection
- **Advanced Rate Limiting**: 256-shard distributed rate limiter with Redis clustering
- **Geographic IP Blocking**: Country-based protection with MaxMind GeoIP2 integration
- **Multi-Feed Threat Intelligence**: 2000+ malicious IPs from Spamhaus, Emerging Threats, Firehol
- **Real-Time IP Reputation**: Continuous threat feed updates every 4 hours
- **VPN/Proxy/Tor Detection**: Advanced anonymization service detection
- **CIDR Block Support**: Network range blocking capabilities

### Web Application Security
- **59+ WAF Rules**: Protection against XSS, SQLi, RCE, LFI, RFI, SSRF, XXE, SSTI
- **ModSecurity Compatible**: Seclang rule format support
- **Custom Rule Engine**: Organization-specific security rules
- **Request Sanitization**: Comprehensive input validation and sanitization
- **Response Filtering**: Output filtering to prevent data leakage

### Data Protection & Privacy
- **PostgreSQL Encryption**: Database-level encryption for sensitive data
- **Audit Trail Integrity**: Tamper-evident logging with cryptographic hashing
- **PII Detection**: Automatic detection and masking of personally identifiable information
- **GDPR Compliance**: Data retention policies and right-to-deletion support
- **Secure Configuration**: Environment-based secrets management

### Infrastructure Security
- **Container Security**: Minimal attack surface with Alpine Linux base images
- **Network Isolation**: Kubernetes network policies and service mesh compatibility
- **Secret Management**: Integration with HashiCorp Vault and Kubernetes secrets
- **TLS/SSL**: End-to-end encryption with modern cipher suites
- **Security Headers**: Comprehensive OWASP-compliant security headers

## Reporting a Vulnerability

### How to Report

**⚠️ CRITICAL: Do NOT report security vulnerabilities through public GitHub issues.**

Please report security vulnerabilities using one of these secure channels:

1. **GitHub Security Advisory** (Preferred): [Create Security Advisory](https://github.com/Abhishek-yadav04/Obsidian/security/advisories/new)
2. **Email**: security@obsidianwaf.com (PGP Key: Available on request)
3. **Bug Bounty Program**: Details available at [security.obsidianwaf.com](https://security.obsidianwaf.com)

### Vulnerability Classification

#### Critical (CVSS 9.0-10.0)
- Authentication bypass
- Remote code execution
- SQL injection in core functions
- Privilege escalation to admin

#### High (CVSS 7.0-8.9)
- Cross-site scripting (XSS)
- Information disclosure of sensitive data
- Denial of service affecting availability
- CSRF with significant impact

#### Medium (CVSS 4.0-6.9)
- Information disclosure (non-sensitive)
- Rate limit bypass
- Access control issues
- Configuration vulnerabilities

#### Low (CVSS 0.1-3.9)
- Information disclosure (minimal)
- UI/UX security issues
- Non-exploitable findings

### What to Include in Your Report

#### Required Information
- **Vulnerability Type**: Category (e.g., authentication bypass, injection, XSS)
- **Affected Components**: Specific modules, APIs, or UI components
- **Version Information**: Obsidian version and deployment configuration
- **Reproducible Steps**: Clear step-by-step instructions
- **Impact Assessment**: Potential consequences and attack scenarios
- **Supporting Evidence**: Screenshots, logs, or proof-of-concept code

#### Optional but Helpful
- **Suggested Fix**: Recommendations for remediation
- **CVSS Score**: Your assessment of severity
- **Related CVEs**: Any similar vulnerabilities
- **Environment Details**: OS, browser, network configuration

### Response Timeline & Process

| Phase | Timeline | Description |
|-------|----------|-------------|
| **Acknowledgment** | 24 hours | Initial response confirming receipt |
| **Triage** | 72 hours | Severity assessment and team assignment |
| **Investigation** | 5-10 days | Technical analysis and impact evaluation |
| **Fix Development** | 15-30 days | Patch development and testing |
| **Release** | 30-45 days | Security update release |
| **Public Disclosure** | 90 days | Coordinated disclosure (if applicable) |

**Critical vulnerabilities** may have accelerated timelines with emergency patches released within 24-48 hours.

## Security Best Practices for Deployment

### Environment Hardening
```bash
# Generate cryptographically secure JWT secret
export OBSIDIAN_JWT_SECRET="$(openssl rand -base64 32)"

# Configure secure database connection
export DATABASE_URL="postgres://obsidian:$(openssl rand -base64 16)@localhost:5432/obsidian?sslmode=require"

# Set Redis with authentication
export REDIS_URL="redis://:$(openssl rand -base64 16)@localhost:6379/0"

# Restrict WebSocket origins to your domains
export OBSIDIAN_ALLOWED_ORIGINS="https://security.company.com,https://waf.company.com"

# Enable GeoIP with secure database location
export GEOIP_DATABASE_PATH="/secure/geoip/GeoLite2-Country.mmdb"

# Production logging level
export LOG_LEVEL="warn"
```

### Network Configuration
```bash
# Deploy behind reverse proxy with rate limiting
upstream obsidian {
    server 127.0.0.1:8082;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name waf.company.com;
    
    # Modern TLS configuration
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-RSA-AES256-GCM-SHA512:DHE-RSA-AES256-GCM-SHA512;
    ssl_prefer_server_ciphers off;
    ssl_session_timeout 1d;
    ssl_session_cache shared:MozTLS:10m;
    
    # Security headers
    add_header Strict-Transport-Security "max-age=63072000" always;
    add_header X-Frame-Options DENY;
    add_header X-Content-Type-Options nosniff;
    add_header Referrer-Policy strict-origin-when-cross-origin;
    
    location / {
        proxy_pass http://obsidian;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### Kubernetes Security
```yaml
apiVersion: v1
kind: SecurityContext
spec:
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    fsGroup: 65534
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: obsidian
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
        - ALL
      readOnlyRootFilesystem: true
    resources:
      limits:
        cpu: 500m
        memory: 512Mi
      requests:
        cpu: 100m
        memory: 128Mi
```

### Database Security
```sql
-- Create dedicated database user with minimal privileges
CREATE USER obsidian_app WITH PASSWORD 'secure_random_password';
CREATE DATABASE obsidian_prod OWNER obsidian_app;
GRANT CONNECT ON DATABASE obsidian_prod TO obsidian_app;
GRANT USAGE ON SCHEMA public TO obsidian_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO obsidian_app;

-- Enable row-level security
ALTER TABLE security_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
```

## Security Monitoring & Alerting

### Key Metrics to Monitor
```json
{
  "security_events_per_minute": "< 100 normal, > 500 investigate",
  "blocked_requests_percentage": "< 5% normal, > 20% investigate", 
  "failed_login_attempts": "< 10/hour normal, > 50/hour investigate",
  "jwt_token_failures": "< 1% normal, > 5% investigate",
  "rate_limit_triggers": "< 50/hour normal, > 200/hour investigate",
  "threat_intel_blocks": "monitor trends, sudden spikes indicate attacks",
  "geoip_blocks": "unusual countries may indicate compromise"
}
```

### Webhook Alert Configuration
```json
{
  "critical_alerts": {
    "webhook_url": "https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK",
    "events": ["authentication_bypass", "privilege_escalation", "rce_attempt"],
    "min_severity": "critical"
  },
  "security_team_alerts": {
    "webhook_url": "https://security.company.com/api/alerts",
    "events": ["high_volume_attacks", "new_threat_patterns"],
    "min_severity": "high"
  }
}
```

## Known Security Considerations & Mitigations

### Token Storage & Session Management
**Consideration**: JWT tokens stored in browser localStorage are accessible to XSS attacks.

**Mitigations**:
- Content Security Policy blocks inline scripts
- XSS protection via WAF rules
- Short token expiration (15 minutes default)
- Automatic token refresh
- Consider HTTP-only cookies for highest security environments

### Development Mode Exposure
**Consideration**: Development mode (`-dev` flag) disables security features for testing.

**Mitigations**:
- Never use `-dev` in production
- Environment detection prevents accidental dev mode
- Docker containers default to production mode
- Kubernetes deployments enforce production configuration

### Rate Limiting Bypass
**Consideration**: Distributed attacks from many IPs may bypass per-IP rate limits.

**Mitigations**:
- Geographic rate limiting
- Threat intelligence integration
- Adaptive rate limiting based on behavior patterns
- Redis-backed distributed counting

### Database Security
**Consideration**: Direct database access could compromise audit integrity.

**Mitigations**:
- Database-level encryption at rest
- Row-level security policies
- Audit log signing with HMAC
- Network isolation of database servers
- Regular security audits of database access

## Compliance & Certifications

### Current Compliance Status
- **OWASP Top 10 2021**: Full protection implemented
- **NIST Cybersecurity Framework**: Aligned with Identify, Protect, Detect, Respond, Recover
- **ISO 27001**: Security controls implemented for information security management
- **SOC 2 Type II**: Ready for audit (controls in place)

### Future Certifications (Roadmap)
- **Common Criteria EAL4**: Planned for v3.5.0
- **FIPS 140-2**: Cryptographic module certification
- **FedRAMP**: U.S. Federal government compliance

## Security Audit History

| Date | Auditor | Scope | Findings | Status |
|------|---------|-------|----------|---------|
| 2026-01-15 | Internal | v2.1.0 Full Stack | 3 Medium, 7 Low | ✅ All Resolved |
| 2025-11-20 | Third-Party | Authentication & Authorization | 1 High, 2 Medium | ✅ All Resolved |
| 2025-09-10 | Bug Bounty | Public Interface | 5 Low, 12 Info | ✅ All Resolved |

## Security Training & Awareness

### Developer Security Training
- Secure coding practices for Go applications
- OWASP Top 10 awareness and prevention
- Cryptographic implementation best practices
- Security testing and vulnerability assessment

### Operational Security
- Incident response procedures
- Log analysis and threat hunting
- Security monitoring and alerting
- Backup and disaster recovery

## Contact Information

### Security Team
- **Security Lead**: security-lead@company.com
- **Incident Response**: incident@company.com (24/7)
- **Bug Bounty Program**: bounty@company.com

### PGP Keys
Security team PGP keys are available at: [https://security.obsidianwaf.com/pgp-keys](https://security.obsidianwaf.com/pgp-keys)

---

**Last Updated**: February 1, 2026  
**Next Review**: May 1, 2026  
**Document Version**: 3.0
|------|---------|----------|--------|
| 2024-01-31 | Internal Review | JWT signature placeholder | Fixed in v2.0.0 |
| 2024-01-31 | Internal Review | Mock data removal | Fixed in v2.0.0 |
| 2024-01-31 | Internal Review | Race condition in persistence | Fixed in v2.0.0 |

## 🏆 Hall of Fame 🏆

Security researchers who have responsibly disclosed vulnerabilities:

1. *Your name could be here!*

---

Thank you for helping keep Obsidian Sentinel and our users safe!
