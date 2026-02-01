# 🛡️ OBSIDIAN Sentinel WAF v2.0.0

<p align="center">
  <img src="cmd/obsidian/ui/assets/logo.svg" alt="Obsidian Sentinel WAF" width="200"/>
</p>

<p align="center">
  <strong>Enterprise-Grade Web Application Firewall</strong><br/>
  Built on Coraza v3 Engine | Real-Time Protection | Zero-Trust Architecture
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

**Obsidian Sentinel** is a production-ready Web Application Firewall that provides real-time protection against OWASP Top 10 threats. It combines the battle-tested Coraza WAF engine with enterprise features including threat intelligence, rate limiting, and comprehensive audit logging.

### Why Obsidian?

- **🔒 Zero-Trust Security**: HMAC-SHA256 JWT authentication, CSRF protection, and cryptographic token validation
- **⚡ High Performance**: Concurrent-safe design with minimal allocation on hot paths
- **📊 Real-Time Monitoring**: WebSocket-based live dashboard with instant threat visibility
- **🌐 Threat Intelligence**: Integrates with Spamhaus, Emerging Threats, and custom blocklists
- **📱 Modern UI**: Responsive Bootstrap 5 dashboard with Chart.js visualizations
- **📦 Single Binary**: All assets embedded - no external dependencies required

---

## ✨ Features

### Security Features
| Feature | Description |
|---------|-------------|
| **55+ WAF Rules** | Protection against XSS, SQLi, RCE, LFI, RFI, SSRF, XXE, SSTI |
| **JWT Authentication** | HMAC-SHA256 signed tokens with configurable expiration |
| **RBAC** | Role-based access control (Admin, Analyst, Viewer) |
| **Rate Limiting** | Sliding window algorithm with configurable thresholds |
| **Threat Intelligence** | Real-time IP reputation checking from multiple feeds |
| **CSRF Protection** | Token-based cross-site request forgery prevention |
| **Security Headers** | CSP, X-Frame-Options, X-Content-Type-Options |

### Enterprise Features
| Feature | Description |
|---------|-------------|
| **PDF Reports** | Generate executive security reports |
| **Audit Logging** | Complete audit trail of all security events |
| **WebSocket Updates** | Real-time dashboard without polling |
| **Multi-User Support** | Admin, Analyst, and Viewer roles |
| **Graceful Shutdown** | Proper cleanup on SIGTERM/SIGINT |
| **Health Endpoints** | Kubernetes-ready health checks |

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

### Build and Run

```bash
# Clone the repository
git clone https://github.com/Abhishek-yadav04/Obsidian.git
cd obsidian

# Build the application
cd cmd/obsidian
go build -o obsidian.exe .

# Run with default settings
./obsidian.exe -port 8082

# Run in development mode
./obsidian.exe -port 8082 -dev
```

### Access the Dashboard

Open your browser and navigate to: **http://localhost:8082**

**Default Credentials:**
- Username: `admin`
- Password: `password`

**Default Roles:**
- **Admin**: Full access (rules, threats, users, audit logs)
- **Analyst**: Read-only security data + report export
- **Viewer**: Read-only dashboard and logs

> ⚠️ **Important**: Change the default password in production!

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
├── cmd/obsidian/           # Main application entry point
│   ├── main.go             # Server initialization
│   └── ui/                 # Embedded frontend assets
│       ├── index.html      # Dashboard
│       ├── login.html      # Authentication page
│       ├── js/app.js       # Frontend logic
│       └── css/styles.css  # Styling
├── internal/app/           # Core application packages
│   ├── api/                # REST API handlers
│   ├── auth/               # JWT authentication
│   ├── model/              # Data models
│   ├── ratelimit/          # Rate limiting
│   ├── report/             # PDF/text reports
│   ├── store/              # Data persistence
│   ├── threat/             # Threat intelligence
│   └── waf/                # WAF rules engine
└── configs/                # Configuration files
```

---

## 🔧 Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OBSIDIAN_JWT_SECRET` | JWT signing secret (min 32 chars) | Random on startup |
| `OBSIDIAN_ALLOWED_ORIGINS` | Comma-separated WebSocket origins | localhost:8082 |
| `OBSIDIAN_DATA_PATH` | Path for persistent storage | `./data.json` |
| `OBSIDIAN_THREAT_PATH` | Path for threat intelligence data | `./threats.json` |

### Command Line Flags

```bash
./obsidian.exe [options]

Options:
  -port int     Port to run the server on (default 8082)
  -dev          Run in development mode
```

### Production Configuration

```bash
# Set secure JWT secret
export OBSIDIAN_JWT_SECRET="your-secure-random-secret-at-least-32-characters"

# Set allowed origins for WebSocket
export OBSIDIAN_ALLOWED_ORIGINS="https://your-domain.com,https://www.your-domain.com"

# Run the application
./obsidian.exe -port 8082
```

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

| Endpoint | Method | Description | Required Role |
|----------|--------|-------------|---------------|
| `/api/stats` | GET | Dashboard statistics | Viewer |
| `/api/logs` | GET | Security event logs | Viewer |
| `/api/rules` | GET | WAF rules list | Viewer |
| `/api/rules/create` | POST | Create new rule | Admin |
| `/api/rules/update` | PUT | Update rule | Admin |
| `/api/rules/delete` | POST | Delete rule | Admin |
| `/api/threats` | GET | Threat intelligence data | Viewer |
| `/api/threats/block` | POST | Block IP address | Admin |
| `/api/metrics` | GET | System metrics | Viewer |
| `/api/export` | GET | Export report | Viewer |
| `/api/admin/users` | GET | User management | Admin |
| `/api/admin/audit` | GET | Audit logs | Admin |

### Health Check

#### GET /api/health
Returns system health status.

```json
{
  "status": "healthy",
  "uptime": "2h30m15s",
  "version": "2.0.0",
  "name": "Obsidian Sentinel WAF"
}
```

---

## 🔐 Security

### JWT Token Security
- Tokens signed with HMAC-SHA256
- Configurable expiration (default: 15 minutes)
- Secrets stored in environment variables
- Constant-time signature comparison

### Rate Limiting
- Sliding window algorithm
- Per-IP tracking
- Configurable limits:
  - 100 requests/minute general
  - 5 login attempts/minute

### Threat Intelligence Sources
- Spamhaus DROP/EDROP
- Emerging Threats
- Firehol Level 1
- Custom blocklists

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

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN cd cmd/obsidian && go build -o obsidian .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/cmd/obsidian/obsidian .
EXPOSE 8082
CMD ["./obsidian", "-port", "8082"]
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: obsidian-waf
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
        image: obsidian:2.0.0
        ports:
        - containerPort: 8082
        env:
        - name: OBSIDIAN_JWT_SECRET
          valueFrom:
            secretKeyRef:
              name: obsidian-secrets
              key: jwt-secret
        livenessProbe:
          httpGet:
            path: /api/health
            port: 8082
          initialDelaySeconds: 5
          periodSeconds: 10
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

`GET /api/metrics` returns:
```json
{
  "total_requests": 15432,
  "blocked_requests": 127,
  "uptime_seconds": 86400,
  "memory_alloc_mb": 45,
  "memory_sys_mb": 72,
  "goroutines": 15
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
