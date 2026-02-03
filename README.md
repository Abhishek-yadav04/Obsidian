# 🛡️ OBSIDIAN Sentinel WAF v2.1.0 Enterprise Edition

<p align="center">
  <img src="cmd/obsidian/ui/assets/logo.svg" alt="Obsidian Sentinel WAF" width="200"/>
</p>

<p align="center">
  <strong>Enterprise-Grade Web Application Firewall with Advanced Security Features</strong><br/>
  Built on Coraza v3 Engine | Real-Time Protection | Zero-Trust Architecture | GeoIP Blocking | Advanced Analytics
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#api-reference">API</a> •
  <a href="#deployment">Deployment</a> •
  <a href="#security">Security</a>
</p>

---

## 📋 Overview

<p align="center">
  <img src="cmd/obsidian/docs/architecture.svg" alt="Obsidian architecture diagram" width="1100"/>
</p>

**Obsidian Sentinel** is an enterprise-ready Web Application Firewall that provides comprehensive protection against advanced cyber threats. It combines the battle-tested Coraza WAF engine with cutting-edge enterprise features including GeoIP blocking, advanced rate limiting, threat intelligence, webhook alerting, and sophisticated analytics.

### Why Obsidian?

- **🔒 Zero-Trust Security**: HMAC-SHA256 JWT authentication, RBAC, CSRF protection, and cryptographic token validation
- **⚡ High Performance**: Concurrent-safe design with minimal allocation on hot paths (256-shard rate limiter)
- **📊 Real-Time Monitoring**: WebSocket-based live dashboard with Chart.js visualizations and instant threat visibility
- **🌐 Advanced Threat Intelligence**: Integrates with Spamhaus, Emerging Threats, Firehol, and custom feeds (2000+ threats)
- **🗺️ GeoIP Protection**: Country-based blocking with MaxMind database support and risk assessment
- **🚨 Smart Alerting**: Webhook integrations for Slack, Teams, Discord, PagerDuty with severity filtering
- **📈 Enterprise Analytics**: PostgreSQL integration, comprehensive audit logging, and executive reporting
- **📱 Modern UI**: Responsive Bootstrap 5 dark theme dashboard with mobile support
- **📦 Single Binary**: All assets embedded - Redis/PostgreSQL optional for enterprise features

---

## ✨ Features

### Core Security Features
| Feature | Description |
|---------|-------------|
| **59+ Advanced WAF Rules** | Protection against XSS, SQLi, RCE, LFI, RFI, SSRF, XXE, SSTI, LDAP injection |
| **JWT Authentication** | HMAC-SHA256 signed tokens with configurable expiration and refresh |
| **Advanced RBAC** | Role-based access control (Admin, Analyst, Viewer) with granular permissions |
| **256-Shard Rate Limiting** | High-performance sliding window with Redis clustering support |
| **Multi-Feed Threat Intel** | Real-time protection from Spamhaus, Emerging Threats, Firehol (2000+ threats) |
| **GeoIP Blocking** | Country-based protection with MaxMind database and risk scoring |
| **CSRF Protection** | Token-based cross-site request forgery prevention |
| **Security Headers** | CSP, HSTS, X-Frame-Options, X-Content-Type-Options |

### Enterprise Features
| Feature | Description |
|---------|-------------|
| **PostgreSQL Integration** | Enterprise-grade data persistence and analytics |
| **Redis Clustering** | Distributed rate limiting and session management |
| **Webhook Alerting** | Real-time notifications to Slack, Teams, Discord, PagerDuty |
| **Executive Reporting** | PDF/Excel reports with charts and threat analysis |
| **Comprehensive Audit** | Complete security event trail with PostgreSQL storage |
| **WebSocket Real-Time** | Live dashboard updates without polling overhead |
| **Advanced Analytics** | Request patterns, geo-distribution, threat correlation |
| **Multi-Tenant Support** | Admin, Security Analyst, and Read-Only Viewer roles |
| **Health & Metrics** | Prometheus-compatible metrics and Kubernetes-ready health checks |
| **Request ID Tracing** | End-to-end request tracking for incident response |

### Attack Categories Protected
- Cross-Site Scripting (XSS)
- SQL Injection (SQLi)
- Remote Code Execution (RCE)
- Local File Inclusion (LFI)
- Remote File Inclusion (RFI)
- Server-Side Request Forgery (SSRF)
- XML External Entity (XXE)
- Template Injection (SSTI)
- LDAP Injection
- Session Fixation
- Java/Deserialization Attacks

---

## 🚀 Quick Start

### Prerequisites
- Go 1.22+ (or TinyGo for WASM builds)
- Windows, Linux, or macOS
- **Optional for Enterprise Features:**
  - PostgreSQL 12+ (for advanced analytics and audit logging)
  - Redis 6+ (for distributed rate limiting and clustering)
  - MaxMind GeoIP2 database (for country-based blocking)

### Build and Run

```bash
# Clone the repository
git clone https://github.com/Abhishek-yadav04/Obsidian.git
cd obsidian

# Install dependencies
go mod tidy

# Build the application
cd cmd/obsidian
go build -o obsidian.exe .

# Run with default settings (in-memory mode)
./obsidian.exe -port 8082

# Run in development mode with debug logging
./obsidian.exe -port 8082 -dev

# Run with PostgreSQL and Redis (Enterprise mode)
export DATABASE_URL="postgres://user:pass@localhost/obsidian"
export REDIS_URL="redis://localhost:6379"
./obsidian.exe -port 8082
```

### Access the Dashboard

Open your browser and navigate to: **http://localhost:8082**

**Default Credentials (CHANGE IMMEDIATELY IN PRODUCTION):**

| Role | Username | Password |
|------|----------|----------|
| Admin | `admin` | `ObsidianAdmin#2024` |
| Analyst | `analyst` | `ObsidianAnalyst#2024` |
| Viewer | `viewer` | `ObsidianViewer#2024` |

**Role Permissions:**
- **Admin**: Full access (rules, threats, users, audit logs, settings)
- **Analyst**: Read-only security data + report export
- **Viewer**: Read-only dashboard and logs

> 🔐 **SECURITY NOTICE**: 
> - Default passwords are documented and MUST be changed before production
> - Set `OBSIDIAN_JWT_SECRET` environment variable (min 32 characters)
> - Do NOT commit `.env` files to version control

---

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client Request                            │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Security Headers Middleware                    │
│         (CSP, X-Frame-Options, X-Content-Type-Options)          │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Rate Limiter Middleware                      │
│              (Sliding Window, Per-IP Tracking)                   │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Threat Intelligence Check                     │
│         (Spamhaus DROP, Emerging Threats, Custom Lists)         │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Coraza WAF Engine                           │
│                  (55+ ModSecurity Rules)                         │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Application Router                           │
│               (API Handlers, Static Files)                       │
└─────────────────────────────────────────────────────────────────┘
```

### Project Structure

```
obsidian/
├── cmd/obsidian/               # Main application entry point
│   ├── main.go                 # Server initialization and routing
│   └── ui/                     # Embedded frontend assets
│       ├── index.html          # Main dashboard (dark theme)
│       ├── login.html          # Authentication page
│       ├── js/app.js           # Frontend application logic
│       ├── css/styles.css      # Enterprise dark theme styling
│       └── assets/             # Static assets (logo, icons)
├── internal/app/               # Core application packages
│   ├── alerts/                 # Webhook alert management
│   ├── auth/                   # JWT authentication & RBAC
│   ├── geoip/                  # Geographic IP blocking service
│   ├── logging/                # Structured logging (Zap)
│   ├── metrics/                # Prometheus-compatible metrics
│   ├── ratelimit/              # 256-shard rate limiter
│   ├── report/                 # PDF/Excel report generation
│   ├── security/               # Security middleware & headers
│   ├── store/                  # Data persistence (PostgreSQL/memory)
│   ├── threatintel/            # Threat intelligence feeds
│   └── tracing/                # Request ID tracing
├── migrations/                 # Database migration scripts
│   ├── 000001_initial_schema.up.sql
│   └── 000001_initial_schema.down.sql
├── configs/                    # Configuration files
│   └── config.yaml             # Default configuration
└── testing/                    # Test suites and benchmarks
    ├── e2e/                    # End-to-end integration tests
    ├── performance/            # Load testing scenarios
    └── testdata/               # Test fixtures and data
```

---

## 🔧 Configuration

### Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `OBSIDIAN_JWT_SECRET` | JWT signing secret (min 32 chars) | Random in dev | **Yes (prod)** |
| `OBSIDIAN_ENV` | Environment (development/production) | development | No |
| `OBSIDIAN_ALLOWED_ORIGINS` | Comma-separated WebSocket origins | localhost:8082 | No |
| `DATABASE_URL` | PostgreSQL connection string | None | **Yes** |
| `REDIS_URL` | Redis connection string | None | No |
| `GEOIP_DATABASE_PATH` | Path to MaxMind GeoIP2 database | None | No |
| `LOG_LEVEL` | Logging level (debug, info, warn, error) | info | No |
| `LOG_FORMAT` | Log format (json, console) | json | No |

### Command Line Flags

```bash
./obsidian.exe [options]

Options:
  -port int        Port to run the server on (default 8082)
  -dev             Run in development mode (relaxed security)
  -log-level       Override LOG_LEVEL env var
  -log-format      Override LOG_FORMAT env var
```

### Sample .env File

```bash
# ⚠️ NEVER COMMIT THIS FILE TO VERSION CONTROL
# Copy to .env and customize for your environment

# JWT Authentication (REQUIRED in production - min 32 chars)
OBSIDIAN_JWT_SECRET=your-super-secure-random-secret-minimum-32-chars

# Environment
OBSIDIAN_ENV=production

# PostgreSQL Database (REQUIRED)
DATABASE_URL=postgresql://obsidian:secure_password@localhost:5432/obsidian?sslmode=require

# Redis Cache (OPTIONAL - enables distributed rate limiting)
REDIS_URL=redis://localhost:6379/0
# For TLS: REDIS_URL=rediss://user:pass@host:port/0

# WebSocket Origins (customize for your domain)
OBSIDIAN_ALLOWED_ORIGINS=https://your-domain.com

# GeoIP Database (OPTIONAL)
GEOIP_DATABASE_PATH=/opt/maxmind/GeoLite2-Country.mmdb

# Logging
LOG_LEVEL=info
LOG_FORMAT=json
```

### Production Deployment Checklist

- [ ] Set `OBSIDIAN_JWT_SECRET` (32+ random characters)
- [ ] Set `OBSIDIAN_ENV=production`
- [ ] Configure PostgreSQL with SSL (`sslmode=require`)
- [ ] Change all default user passwords immediately
- [ ] Configure proper `OBSIDIAN_ALLOWED_ORIGINS`
- [ ] Set up Redis for distributed rate limiting
- [ ] Enable GeoIP blocking if needed
- [ ] Configure reverse proxy (nginx/Caddy) with TLS
- [ ] Set up log aggregation
- [ ] Configure alerting webhooks

---

## 📡 API Reference

### Authentication

#### POST /api/login
Authenticate and receive JWT token.

**Request:**
```json
{
  "username": "admin",
  "password": "password"
}
```

**Response:**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "...",
  "user": {
    "id": 1,
    "username": "admin",
    "role": "Admin"
  },
  "expires_in": 900
}
```

### Protected Endpoints (Require Bearer Token)

#### Core Security APIs
| Endpoint | Method | Description | Required Role |
|----------|--------|-------------|---------------|
| `/api/stats` | GET | Dashboard statistics with geo data | Viewer |
| `/api/logs` | GET | Security event logs with pagination | Viewer |
| `/api/rules` | GET | WAF rules list (59 rules) | Viewer |
| `/api/rules/create` | POST | Create new WAF rule | Admin |
| `/api/rules/update` | PUT | Update existing rule | Admin |
| `/api/rules/delete` | DELETE | Delete rule by ID | Admin |
| `/api/threats` | GET | Threat intelligence data (2000+ threats) | Viewer |
| `/api/threats/block` | POST | Block IP address | Admin |

#### Enterprise Analytics APIs
| Endpoint | Method | Description | Required Role |
|----------|--------|-------------|---------------|
| `/api/metrics` | GET | System and security metrics | Viewer |
| `/api/geoip/lookup` | GET | GeoIP lookup for any IP | Viewer |
| `/api/geoip/blocked` | GET/POST/DELETE | Manage blocked countries | Admin |
| `/api/geoip/metrics` | GET | GeoIP service statistics | Viewer |
| `/api/ratelimit/blacklist` | POST/DELETE | Manage IP blacklist | Admin |
| `/api/ratelimit/whitelist` | POST/DELETE | Manage IP whitelist | Admin |

#### Alert and Integration APIs
| Endpoint | Method | Description | Required Role |
|----------|--------|-------------|---------------|
| `/api/alerts/webhooks` | GET/POST/DELETE | Manage webhook integrations | Admin |
| `/api/alerts/webhooks/test` | POST | Test webhook configuration | Admin |
| `/api/export` | GET | Export security reports (PDF/Excel) | Analyst |
| `/api/admin/users` | GET | User management | Admin |
| `/api/admin/audit` | GET | Comprehensive audit logs | Admin |

### Health Check

#### GET /api/health
Returns comprehensive system health status.

```json
{
  "status": "healthy",
  "uptime": "2h30m15s",
  "version": "2.1.0",
  "edition": "Enterprise",
  "name": "Obsidian Sentinel WAF",
  "features": {
    "waf_engine": true,
    "threat_intelligence": true,
    "rate_limiting": true,
    "geoip_blocking": true,
    "webhook_alerts": true,
    "postgresql": true,
    "redis": true,
    "advanced_analytics": true
  },
  "stats": {
    "total_requests": 15432,
    "blocked_requests": 127,
    "active_threats": 2041,
    "blocked_countries": 3
  }
}
```

---

## 🔐 Security

### JWT Token Security
- Tokens signed with HMAC-SHA256
- Configurable expiration (default: 15 minutes)
- Refresh token rotation
- Secrets stored in environment variables
- Constant-time signature comparison
- Role-based claims validation

### Advanced Rate Limiting
- 256-shard sliding window algorithm
- Redis-backed distributed limiting
- Per-IP and per-endpoint tracking
- Configurable limits:
  - 200 requests/minute general
  - 5 login attempts/minute
  - Custom thresholds per endpoint
- Whitelist/blacklist IP management
- Geographic rate limiting

### Threat Intelligence Sources
- **Spamhaus DROP/EDROP** - 800+ malicious networks
- **Emerging Threats** - 1000+ compromised IPs
- **Firehol Level 1** - 200+ high-confidence threats
- **Custom feeds** - User-defined blocklists
- **GeoIP risk scoring** - Country-based threat assessment
- **Real-time updates** - Feeds refreshed every 4 hours

### GeoIP Security
- MaxMind GeoIP2 database integration
- Country-based blocking/allowing
- Risk score calculation
- VPN/Proxy/Tor detection
- Threat score based on geography
- Custom country rules with reasons

### Security Headers
```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-XSS-Protection: 1; mode=block
Referrer-Policy: strict-origin-when-cross-origin
Content-Security-Policy: default-src 'self'; ...
```

---

## 📦 Deployment

### Docker (Recommended)

#### Simple Deployment
```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN cd cmd/obsidian && go build -o obsidian .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/cmd/obsidian/obsidian .
EXPOSE 8082
CMD ["./obsidian", "-port", "8082"]
```

#### Enterprise Deployment with PostgreSQL and Redis
```yaml
version: '3.8'
services:
  obsidian:
    build: .
    ports:
      - "8082:8082"
    environment:
      - DATABASE_URL=postgres://obsidian:secure_pass@postgres:5432/obsidian
      - REDIS_URL=redis://redis:6379/0
      - OBSIDIAN_JWT_SECRET=your-super-secure-secret-key-here
      - GEOIP_DATABASE_PATH=/data/GeoLite2-Country.mmdb
    volumes:
      - ./geoip:/data
    depends_on:
      - postgres
      - redis

  postgres:
    image: postgres:15-alpine
    environment:
      - POSTGRES_DB=obsidian
      - POSTGRES_USER=obsidian
      - POSTGRES_PASSWORD=secure_pass
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./migrations:/docker-entrypoint-initdb.d

  redis:
    image: redis:7-alpine
    command: redis-server --appendonly yes
    volumes:
      - redis_data:/data

volumes:
  postgres_data:
  redis_data:
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: obsidian-waf
  namespace: security
spec:
  replicas: 3
  selector:
    matchLabels:
      app: obsidian-waf
  template:
    metadata:
      labels:
        app: obsidian-waf
    spec:
      containers:
      - name: obsidian
        image: obsidian:2.1.0
        ports:
        - containerPort: 8082
        env:
        - name: OBSIDIAN_JWT_SECRET
          valueFrom:
            secretKeyRef:
              name: obsidian-secrets
              key: jwt-secret
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: obsidian-secrets
              key: database-url
        - name: REDIS_URL
          value: "redis://obsidian-redis:6379/0"
        - name: GEOIP_DATABASE_PATH
          value: "/data/GeoLite2-Country.mmdb"
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /api/health
            port: 8082
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /api/health
            port: 8082
          initialDelaySeconds: 5
          periodSeconds: 10
        volumeMounts:
        - name: geoip-data
          mountPath: /data
      volumes:
      - name: geoip-data
        configMap:
          name: geoip-database
---
apiVersion: v1
kind: Service
metadata:
  name: obsidian-waf-service
spec:
  selector:
    app: obsidian-waf
  ports:
  - protocol: TCP
    port: 80
    targetPort: 8082
  type: LoadBalancer
```

### Systemd Service

```ini
[Unit]
Description=Obsidian Sentinel WAF
After=network.target

[Service]
Type=simple
User=obsidian
WorkingDirectory=/opt/obsidian
Environment=OBSIDIAN_JWT_SECRET=your-secret-here
ExecStart=/opt/obsidian/obsidian -port 8082
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

---

## 📊 Documentation

| Document | Description |
|----------|-------------|
| [Production Audit Report](docs/PRODUCTION_AUDIT_REPORT.md) | Comprehensive security audit with 100 issues and 50 features |
| [Contributing Guide](CONTRIBUTING.md) | How to contribute to the project |
| [Security Policy](SECURITY.md) | How to report vulnerabilities |
| [License](LICENSE) | Apache 2.0 License |

---

## 🧪 Testing

### Run Tests
```bash
go test ./... -v
```

### Run with Coverage
```bash
go test ./... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Test WAF Rules
```bash
# Test XSS blocking
curl -X GET "http://localhost:8082/api/test?input=<script>alert(1)</script>"

# Test SQL injection blocking
curl -X GET "http://localhost:8082/api/test?id=1' OR '1'='1"
```

---

## 📊 Monitoring

### Metrics Endpoint

`GET /api/metrics` returns comprehensive system metrics:
```json
{
  "total_requests": 25432,
  "blocked_requests": 327,
  "uptime_seconds": 172800,
  "memory_alloc_mb": 65,
  "memory_sys_mb": 128,
  "goroutines": 23,
  "rate_limiter": {
    "active_visitors": 15,
    "blacklist_count": 5,
    "whitelist_count": 10,
    "rate_limited_ips": 3,
    "requests_per_minute": 200,
    "shards": 256,
    "total_allowed": 25105,
    "total_blocked": 327
  },
  "threat_intel": {
    "total_threats": 2041,
    "feeds_active": 4,
    "last_update": "2026-02-01T14:30:00Z",
    "blocked_today": 127,
    "high_risk_count": 1205
  },
  "geoip": {
    "blocked_countries": 3,
    "total_lookups": 15432,
    "cache_hits": 12890,
    "cache_misses": 2542
  },
  "webhooks": {
    "active_webhooks": 2,
    "alerts_sent_today": 15,
    "alerts_failed": 1
  }
}
```

### WebSocket Real-Time Updates

Connect to `ws://localhost:8082/api/ws?token=<jwt>` for live stats updates.

---

## 🤝 Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed guidelines.

---

## 📜 License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgments

- [Coraza WAF](https://coraza.io) - The core WAF engine
- [OWASP CRS](https://coreruleset.org) - Core Rule Set inspiration
- [ModSecurity](https://modsecurity.org) - SecLang rule language

---

## 📞 Support

- **Issues**: [GitHub Issues](https://github.com/Abhishek-yadav04/Obsidian/issues)
- **Discussions**: [GitHub Discussions](https://github.com/Abhishek-yadav04/Obsidian/discussions)
- **Security**: See [SECURITY.md](SECURITY.md) for reporting vulnerabilities

---
## 👨‍💻 Author

<p align="center">
  <img src="https://github.com/Abhishek-yadav04.png" width="100px" style="border-radius: 50%;" alt="Abhishek Yadav" />
</p>

<p align="center">
  <b>Abhishek Yadav</b><br>
  Computer Science Student
</p>

<p align="center">
  <a href="https://github.com/Abhishek-yadav04">
    <img src="https://img.shields.io/badge/GitHub-181717?style=for-the-badge&logo=github" alt="GitHub" />
  </a>
</p>

---

<p align="center">
  <b>⭐ Star this repo if you find it helpful!</b>
</p>

<p align="center">
  Made with ❤️ for the cybersecurity community
</p>



First and foremost, huge thanks to [Juan Pablo Tosso](https://twitter.com/jptosso) for starting this project, and building an amazing community around Coraza!

Today we have lots of amazing contributors, we could not have done this without you!

<a href="https://github.com/corazawaf/coraza/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=corazawaf/coraza" />
</a>

Made with [contrib.rocks](https://contrib.rocks).
