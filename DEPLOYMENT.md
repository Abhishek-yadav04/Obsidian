# Obsidian Sentinel WAF v2.2.4 - Enterprise Deployment Guide

This guide covers production deployment scenarios for Obsidian Sentinel WAF Enterprise Edition.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Single Server Deployment](#single-server-deployment)
- [High Availability Deployment](#high-availability-deployment)
- [Container Deployment](#container-deployment)
- [Kubernetes Deployment](#kubernetes-deployment)
- [Cloud Provider Guides](#cloud-provider-guides)
- [Security Hardening](#security-hardening)
- [Monitoring & Observability](#monitoring--observability)
- [Backup & Disaster Recovery](#backup--disaster-recovery)
- [Troubleshooting](#troubleshooting)

## Prerequisites

### System Requirements

#### Minimum Requirements
- **CPU**: 2 cores (2.0 GHz)
- **RAM**: 4GB
- **Storage**: 20GB SSD
- **Network**: 100 Mbps
- **OS**: Linux (Ubuntu 20.04+, CentOS 8+, RHEL 8+)

#### Recommended Requirements
- **CPU**: 4+ cores (2.4 GHz)
- **RAM**: 8GB+
- **Storage**: 50GB+ NVMe SSD
- **Network**: 1 Gbps
- **OS**: Ubuntu 22.04 LTS or equivalent

#### Enterprise Scale Requirements
- **CPU**: 8+ cores (3.0 GHz)
- **RAM**: 16GB+
- **Storage**: 100GB+ NVMe SSD (RAID 1 recommended)
- **Network**: 10 Gbps
- **Load Balancer**: HAProxy, nginx, or cloud LB

### Dependencies

#### Required
- **Go**: 1.22+ (for building from source)
- **systemd**: For service management

#### Optional (Enterprise Features)
- **PostgreSQL**: 12+ (recommended: 15+)
- **Redis**: 6+ (recommended: 7+)
- **MaxMind GeoIP2**: Country database
- **TLS Certificates**: Let's Encrypt or commercial CA

## Single Server Deployment

### 1. System Preparation

```bash
# Update system
sudo apt update && sudo apt upgrade -y

# Install required packages
sudo apt install -y curl wget git build-essential

# Create obsidian user
sudo useradd -r -s /bin/false obsidian
sudo mkdir -p /opt/obsidian
sudo chown obsidian:obsidian /opt/obsidian
```

### 2. Application Installation

```bash
# Download and build Obsidian
git clone https://github.com/Abhishek-yadav04/Obsidian.git
cd Obsidian
cd cmd/obsidian
go build -o obsidian .

# Install to system location
sudo mv obsidian /opt/obsidian/
sudo chown obsidian:obsidian /opt/obsidian/obsidian
sudo chmod +x /opt/obsidian/obsidian
```

### 3. Configuration

```bash
# Create configuration directory
sudo mkdir -p /etc/obsidian
sudo chown obsidian:obsidian /etc/obsidian

# Create environment file
sudo tee /etc/obsidian/environment <<EOF
OBSIDIAN_JWT_SECRET=$(openssl rand -base64 32)
LOG_LEVEL=info
DATABASE_URL=postgres://obsidian:$(openssl rand -base64 16)@localhost:5432/obsidian
REDIS_URL=redis://localhost:6379/0
GEOIP_DATABASE_PATH=/opt/obsidian/data/GeoLite2-Country.mmdb
OBSIDIAN_ALLOWED_ORIGINS=https://your-domain.com
EOF

sudo chmod 600 /etc/obsidian/environment
sudo chown obsidian:obsidian /etc/obsidian/environment
```

### 4. Service Configuration

```bash
# Create systemd service file
sudo tee /etc/systemd/system/obsidian.service <<EOF
[Unit]
Description=Obsidian Sentinel WAF
Documentation=https://github.com/Abhishek-yadav04/Obsidian
After=network-online.target postgresql.service redis.service
Wants=network-online.target
Requires=postgresql.service

[Service]
Type=simple
User=obsidian
Group=obsidian
WorkingDirectory=/opt/obsidian
EnvironmentFile=/etc/obsidian/environment
ExecStart=/opt/obsidian/obsidian -port 8082
ExecReload=/bin/kill -HUP \$MAINPID
TimeoutStopSec=30
Restart=always
RestartSec=5

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/obsidian/data

[Install]
WantedBy=multi-user.target
EOF

# Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable obsidian
sudo systemctl start obsidian
```

### 5. Reverse Proxy Configuration (nginx)

```bash
# Install nginx
sudo apt install -y nginx

# Create SSL certificates (Let's Encrypt)
sudo apt install -y certbot python3-certbot-nginx
sudo certbot --nginx -d your-domain.com

# Configure nginx
sudo tee /etc/nginx/sites-available/obsidian <<EOF
# Rate limiting
limit_req_zone \$binary_remote_addr zone=obsidian_login:10m rate=5r/m;
limit_req_zone \$binary_remote_addr zone=obsidian_api:10m rate=100r/m;

upstream obsidian_backend {
    server 127.0.0.1:8082;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name your-domain.com;
    
    # SSL configuration
    ssl_certificate /etc/letsencrypt/live/your-domain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/your-domain.com/privkey.pem;
    
    # Modern SSL configuration
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-RSA-AES256-GCM-SHA512:DHE-RSA-AES256-GCM-SHA512:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_timeout 1d;
    ssl_session_cache shared:MozTLS:10m;
    ssl_stapling on;
    ssl_stapling_verify on;
    
    # Security headers
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    add_header X-Frame-Options DENY always;
    add_header X-Content-Type-Options nosniff always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;
    
    # Rate limiting
    location /api/login {
        limit_req zone=obsidian_login burst=3 nodelay;
        proxy_pass http://obsidian_backend;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
    
    location /api/ {
        limit_req zone=obsidian_api burst=20 nodelay;
        proxy_pass http://obsidian_backend;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
    
    location / {
        proxy_pass http://obsidian_backend;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        
        # WebSocket support
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 86400;
    }
}

# Redirect HTTP to HTTPS
server {
    listen 80;
    server_name your-domain.com;
    return 301 https://\$server_name\$request_uri;
}
EOF

# Enable site
sudo ln -s /etc/nginx/sites-available/obsidian /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

## High Availability Deployment

### Architecture Overview

```
                           ┌─────────────────┐
                           │   Load Balancer │
                           │   (HAProxy/ALB) │
                           └─────────────────┘
                                    │
                    ┌───────────────┼───────────────┐
                    │               │               │
            ┌───────▼─────┐ ┌───────▼─────┐ ┌───────▼─────┐
            │  Obsidian   │ │  Obsidian   │ │  Obsidian   │
            │   Node 1    │ │   Node 2    │ │   Node 3    │
            └─────────────┘ └─────────────┘ └─────────────┘
                    │               │               │
                    └───────────────┼───────────────┘
                                    │
                    ┌───────────────▼───────────────┐
                    │         Shared Services       │
                    │  ┌─────────────────────────┐  │
                    │  │     PostgreSQL          │  │
                    │  │   (Primary + Replica)   │  │
                    │  └─────────────────────────┘  │
                    │  ┌─────────────────────────┐  │
                    │  │     Redis Cluster       │  │
                    │  │   (3 Masters + 3 Rep)   │  │
                    │  └─────────────────────────┘  │
                    └─────────────────────────────────┘
```

### 1. Database High Availability

#### PostgreSQL Primary-Replica Setup

```bash
# Primary server configuration
sudo -u postgres createuser obsidian
sudo -u postgres createdb obsidian -O obsidian

# Configure PostgreSQL for replication
sudo tee -a /etc/postgresql/15/main/postgresql.conf <<EOF
# Replication settings
wal_level = replica
max_wal_senders = 3
max_replication_slots = 3
archive_mode = on
archive_command = 'test ! -f /var/lib/postgresql/15/main/archive/%f && cp %p /var/lib/postgresql/15/main/archive/%f'
EOF

# Configure access
sudo tee -a /etc/postgresql/15/main/pg_hba.conf <<EOF
# Replication connections
host replication obsidian 10.0.1.0/24 md5
EOF

sudo systemctl restart postgresql
```

#### Redis Cluster Setup

```bash
# Create Redis cluster configuration
for port in 7000 7001 7002 7003 7004 7005; do
  mkdir -p /etc/redis/cluster/$port
  tee /etc/redis/cluster/$port/redis.conf <<EOF
port $port
cluster-enabled yes
cluster-config-file nodes-$port.conf
cluster-node-timeout 5000
appendonly yes
dir /var/lib/redis/cluster/$port
EOF
done

# Start Redis cluster nodes
for port in 7000 7001 7002 7003 7004 7005; do
  redis-server /etc/redis/cluster/$port/redis.conf --daemonize yes
done

# Create cluster
redis-cli --cluster create 127.0.0.1:7000 127.0.0.1:7001 127.0.0.1:7002 127.0.0.1:7003 127.0.0.1:7004 127.0.0.1:7005 --cluster-replicas 1
```

### 2. Load Balancer Configuration (HAProxy)

```bash
# Install HAProxy
sudo apt install -y haproxy

# Configure HAProxy
sudo tee /etc/haproxy/haproxy.cfg <<EOF
global
    daemon
    log stdout local0
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin
    stats timeout 30s
    user haproxy
    group haproxy

defaults
    mode http
    log global
    option httplog
    option dontlognull
    option http-server-close
    option forwardfor except 127.0.0.0/8
    option redispatch
    retries 3
    timeout http-request 10s
    timeout queue 1m
    timeout connect 10s
    timeout client 1m
    timeout server 1m
    timeout http-keep-alive 10s
    timeout check 10s
    maxconn 3000

# Stats interface
listen stats
    bind *:8404
    stats enable
    stats uri /stats
    stats refresh 30s
    stats admin if LOCALHOST

# Frontend
frontend obsidian_frontend
    bind *:443 ssl crt /etc/ssl/certs/obsidian.pem
    bind *:80
    redirect scheme https if !{ ssl_fc }
    
    # Security headers
    http-response set-header Strict-Transport-Security "max-age=31536000; includeSubDomains"
    http-response set-header X-Frame-Options DENY
    http-response set-header X-Content-Type-Options nosniff
    
    # Rate limiting
    stick-table type ip size 100k expire 30s store http_req_rate(10s)
    http-request track-sc0 src
    http-request deny if { sc_http_req_rate(0) gt 20 }
    
    default_backend obsidian_servers

# Backend
backend obsidian_servers
    balance roundrobin
    option httpchk GET /api/health
    http-check expect status 200
    
    server obsidian1 10.0.1.10:8082 check inter 5s rise 2 fall 3
    server obsidian2 10.0.1.11:8082 check inter 5s rise 2 fall 3
    server obsidian3 10.0.1.12:8082 check inter 5s rise 2 fall 3
EOF

sudo systemctl restart haproxy
```

---

## Releasing and pulling images

Releases for this repository are performed by the GitHub Actions workflows. For maintainers and contributors with permission to publish releases, the workflow will:

- Build platform binaries and upload them as artifacts
- Build a multi-arch Docker image and push it to GitHub Container Registry (GHCR)
- Generate an SBOM (CycloneDX JSON) and attach it to the release artifacts
- Create a GitHub Release and attach the built artifacts

To create a release locally, tag the repository and push the tag:

```bash
# Create an annotated semver tag
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

The workflows will run automatically for tags starting with `v`.

To verify and pull the published Docker image from GHCR:

```bash
# Authenticate to GHCR (use a PAT with appropriate scopes)
echo "${GHCR_TOKEN}" | docker login ghcr.io -u <USERNAME> --password-stdin

# Pull the image (example tag vX.Y.Z)
docker pull ghcr.io/<owner>/<repo>:vX.Y.Z
```

If the pull fails, check the GitHub Actions logs for the `docker` and `release` jobs, and open an issue with the tag name and failing logs.

## Container Deployment

### 1. Docker Compose (Development/Staging)

```yaml
# docker-compose.yml
version: '3.8'

services:
  obsidian:
    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "8082:8082"
    environment:
      - DATABASE_URL=postgres://obsidian:obsidian_pass@postgres:5432/obsidian
      - REDIS_URL=redis://redis:6379/0
      - OBSIDIAN_JWT_SECRET=${JWT_SECRET:-default_secret_change_me}
      - GEOIP_DATABASE_PATH=/data/GeoLite2-Country.mmdb
      - LOG_LEVEL=info
    volumes:
      - geoip_data:/data
      - ./logs:/app/logs
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8082/api/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s

  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_DB: obsidian
      POSTGRES_USER: obsidian
      POSTGRES_PASSWORD: obsidian_pass
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./migrations:/docker-entrypoint-initdb.d
    ports:
      - "5432:5432"
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U obsidian"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    command: redis-server --appendonly yes --requirepass redis_pass
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "redis_pass", "ping"]
      interval: 10s
      timeout: 3s
      retries: 3

  nginx:
    image: nginx:alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./ssl:/etc/ssl/certs:ro
    depends_on:
      - obsidian
    restart: unless-stopped

volumes:
  postgres_data:
  redis_data:
  geoip_data:
```

### 2. Production Dockerfile

```dockerfile
# Multi-stage build for production
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Create non-root user
RUN adduser -D -s /bin/sh -u 1001 obsidian

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o obsidian ./cmd/obsidian

# Final stage
FROM scratch

# Import from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /app/obsidian /obsidian

# Use non-root user
USER obsidian

# Expose port
EXPOSE 8082

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/obsidian", "-health-check"]

# Run application
ENTRYPOINT ["/obsidian"]
CMD ["-port", "8082"]
```

## Kubernetes Deployment

### 1. Namespace and RBAC

```yaml
# namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: obsidian-system
  labels:
    name: obsidian-system
    app.kubernetes.io/name: obsidian
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: obsidian
  namespace: obsidian-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: obsidian-reader
rules:
- apiGroups: [""]
  resources: ["pods", "services", "endpoints"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: obsidian-reader-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: obsidian-reader
subjects:
- kind: ServiceAccount
  name: obsidian
  namespace: obsidian-system
```

### 2. ConfigMap and Secrets

```yaml
# configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: obsidian-config
  namespace: obsidian-system
data:
  LOG_LEVEL: "info"
  GEOIP_DATABASE_PATH: "/data/GeoLite2-Country.mmdb"
  OBSIDIAN_ALLOWED_ORIGINS: "https://waf.company.com"
---
apiVersion: v1
kind: Secret
metadata:
  name: obsidian-secrets
  namespace: obsidian-system
type: Opaque
data:
  jwt-secret: <base64-encoded-jwt-secret>
  database-url: <base64-encoded-database-url>
  redis-url: <base64-encoded-redis-url>
```

### 3. Deployment and Service

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: obsidian
  namespace: obsidian-system
  labels:
    app: obsidian
    version: v3.0.0
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app: obsidian
  template:
    metadata:
      labels:
        app: obsidian
        version: v2.1.0
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8082"
        prometheus.io/path: "/api/metrics"
    spec:
      serviceAccountName: obsidian
      securityContext:
        runAsNonRoot: true
        runAsUser: 1001
        fsGroup: 1001
      containers:
      - name: obsidian
        image: obsidian:2.1.0
        ports:
        - containerPort: 8082
          name: http
          protocol: TCP
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
          valueFrom:
            secretKeyRef:
              name: obsidian-secrets
              key: redis-url
        envFrom:
        - configMapRef:
            name: obsidian-config
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /api/health
            port: 8082
          initialDelaySeconds: 30
          periodSeconds: 30
          timeoutSeconds: 10
          failureThreshold: 3
        readinessProbe:
          httpGet:
            path: /api/health
            port: 8082
          initialDelaySeconds: 5
          periodSeconds: 5
          timeoutSeconds: 5
          failureThreshold: 3
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop:
            - ALL
          readOnlyRootFilesystem: true
        volumeMounts:
        - name: geoip-data
          mountPath: /data
          readOnly: true
        - name: tmp
          mountPath: /tmp
      volumes:
      - name: geoip-data
        configMap:
          name: geoip-database
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: obsidian-service
  namespace: obsidian-system
  labels:
    app: obsidian
spec:
  type: ClusterIP
  ports:
  - port: 80
    targetPort: 8082
    protocol: TCP
    name: http
  selector:
    app: obsidian
```

### 4. Ingress Configuration

```yaml
# ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: obsidian-ingress
  namespace: obsidian-system
  annotations:
    kubernetes.io/ingress.class: nginx
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    nginx.ingress.kubernetes.io/force-ssl-redirect: "true"
    nginx.ingress.kubernetes.io/rate-limit: "100"
    nginx.ingress.kubernetes.io/rate-limit-window: "1m"
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
spec:
  tls:
  - hosts:
    - waf.company.com
    secretName: obsidian-tls
  rules:
  - host: waf.company.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: obsidian-service
            port:
              number: 80
```

## Security Hardening

### 1. Operating System Hardening

```bash
# Disable unnecessary services
sudo systemctl disable bluetooth cups
sudo systemctl stop bluetooth cups

# Configure firewall
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable

# Configure fail2ban
sudo apt install -y fail2ban
sudo tee /etc/fail2ban/jail.local <<EOF
[DEFAULT]
bantime = 3600
findtime = 600
maxretry = 3

[sshd]
enabled = true
port = ssh
filter = sshd
logpath = /var/log/auth.log

[nginx-http-auth]
enabled = true
filter = nginx-http-auth
logpath = /var/log/nginx/error.log
maxretry = 3
EOF

sudo systemctl restart fail2ban
```

### 2. SSL/TLS Configuration

```bash
# Generate DH parameters
sudo openssl dhparam -out /etc/ssl/certs/dhparam.pem 2048

# Create SSL configuration
sudo tee /etc/nginx/snippets/ssl-params.conf <<EOF
# Modern configuration
ssl_protocols TLSv1.2 TLSv1.3;
ssl_ciphers ECDHE-RSA-AES256-GCM-SHA512:DHE-RSA-AES256-GCM-SHA512:ECDHE-RSA-AES256-GCM-SHA384;
ssl_prefer_server_ciphers off;
ssl_ecdh_curve secp384r1;
ssl_session_timeout  10m;
ssl_session_cache shared:SSL:10m;
ssl_session_tickets off;
ssl_stapling on;
ssl_stapling_verify on;

# HSTS
add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload";

# Security headers
add_header X-Frame-Options DENY;
add_header X-Content-Type-Options nosniff;
add_header Referrer-Policy strict-origin-when-cross-origin;
add_header X-XSS-Protection "1; mode=block";
EOF
```

## Monitoring & Observability

### 1. Prometheus Configuration

```yaml
# prometheus.yml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - "obsidian_rules.yml"

scrape_configs:
  - job_name: 'obsidian'
    static_configs:
      - targets: ['localhost:8082']
    metrics_path: /api/metrics
    scrape_interval: 30s
    scrape_timeout: 10s

alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - alertmanager:9093
```

### 2. Grafana Dashboard

```json
{
  "dashboard": {
    "title": "Obsidian Sentinel WAF Dashboard",
    "panels": [
      {
        "title": "Request Rate",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(obsidian_requests_total[5m])",
            "legendFormat": "Requests/sec"
          }
        ]
      },
      {
        "title": "Block Rate", 
        "type": "graph",
        "targets": [
          {
            "expr": "rate(obsidian_blocked_requests_total[5m])",
            "legendFormat": "Blocks/sec"
          }
        ]
      },
      {
        "title": "Top Blocked Countries",
        "type": "table",
        "targets": [
          {
            "expr": "topk(10, obsidian_geoip_blocks_by_country)",
            "format": "table"
          }
        ]
      }
    ]
  }
}
```

### 3. Log Aggregation (ELK Stack)

```yaml
# filebeat.yml
filebeat.inputs:
- type: log
  enabled: true
  paths:
    - /var/log/obsidian/*.log
  json.keys_under_root: true
  json.add_error_key: true
  fields:
    service: obsidian
    environment: production

output.elasticsearch:
  hosts: ["elasticsearch:9200"]
  index: "obsidian-logs-%{+yyyy.MM.dd}"

setup.template.name: "obsidian"
setup.template.pattern: "obsidian-*"
```

## Backup & Disaster Recovery

### 1. Database Backup

```bash
#!/bin/bash
# backup-postgres.sh

BACKUP_DIR="/backup/postgres"
DATE=$(date +%Y%m%d_%H%M%S)
DATABASE="obsidian"

# Create backup directory
mkdir -p $BACKUP_DIR

# Perform backup
pg_dump -U obsidian -h localhost $DATABASE | gzip > $BACKUP_DIR/obsidian_$DATE.sql.gz

# Clean old backups (keep 30 days)
find $BACKUP_DIR -name "obsidian_*.sql.gz" -mtime +30 -delete

# Upload to S3 (optional)
aws s3 cp $BACKUP_DIR/obsidian_$DATE.sql.gz s3://your-backup-bucket/postgres/
```

### 2. Application Backup

```bash
#!/bin/bash
# backup-obsidian.sh

BACKUP_DIR="/backup/obsidian"
DATE=$(date +%Y%m%d_%H%M%S)

mkdir -p $BACKUP_DIR

# Backup configuration
tar -czf $BACKUP_DIR/config_$DATE.tar.gz /etc/obsidian/

# Backup data directory
tar -czf $BACKUP_DIR/data_$DATE.tar.gz /opt/obsidian/data/

# Backup logs
tar -czf $BACKUP_DIR/logs_$DATE.tar.gz /var/log/obsidian/

# Clean old backups
find $BACKUP_DIR -name "*_*.tar.gz" -mtime +7 -delete
```

### 3. Disaster Recovery Plan

```markdown
## Recovery Time Objectives (RTO)
- Critical systems: 1 hour
- Full functionality: 4 hours

## Recovery Point Objectives (RPO)
- Database: 15 minutes
- Configuration: 1 hour
- Logs: 1 hour

## Recovery Procedures

### Database Recovery
1. Restore PostgreSQL from latest backup
2. Apply transaction logs if available
3. Verify data integrity

### Application Recovery  
1. Deploy Obsidian from backup or source
2. Restore configuration files
3. Update DNS/load balancer
4. Verify all services

### Validation Checklist
- [ ] Authentication working
- [ ] WAF rules active
- [ ] Threat intelligence updated
- [ ] Monitoring restored
- [ ] Alerts configured
```

## Troubleshooting

### Common Issues

#### 1. High Memory Usage

```bash
# Check memory usage
ps aux | grep obsidian
free -h

# Adjust Go GC settings
export GOGC=50  # More aggressive GC
export GOMEMLIMIT=512MiB  # Memory limit

# Monitor GC metrics
curl -s http://localhost:8082/api/metrics | grep gc
```

#### 2. Database Connection Issues

```bash
# Check PostgreSQL status
sudo systemctl status postgresql
sudo -u postgres psql -c "SELECT version();"

# Test connection
psql -h localhost -U obsidian -d obsidian -c "SELECT NOW();"

# Check connection limits
sudo -u postgres psql -c "SHOW max_connections;"
sudo -u postgres psql -c "SELECT count(*) FROM pg_stat_activity;"
```

#### 3. Rate Limiting Issues

```bash
# Check Redis status
redis-cli ping
redis-cli info replication

# Monitor rate limiting
curl -s http://localhost:8082/api/metrics | grep rate_limit

# Clear rate limit data
redis-cli FLUSHDB
```

#### 4. GeoIP Issues

```bash
# Check GeoIP database
ls -la /opt/obsidian/data/GeoLite2-Country.mmdb

# Test GeoIP lookup
curl -H "Authorization: Bearer $TOKEN" \
     "http://localhost:8082/api/geoip/lookup?ip=8.8.8.8"

# Update GeoIP database
wget https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country&license_key=YOUR_KEY&suffix=tar.gz
```

### Performance Tuning

#### Go Application Tuning

```bash
# Environment variables for performance
export GOMAXPROCS=4           # Match CPU cores
export GOGC=100              # GC percentage
export GOMEMLIMIT=1GiB       # Memory limit
export GODEBUG=gctrace=1     # GC debugging
```

#### PostgreSQL Tuning

```sql
-- postgresql.conf optimizations
-- shared_buffers = 256MB
-- effective_cache_size = 1GB
-- work_mem = 4MB
-- maintenance_work_mem = 64MB
-- max_connections = 100
-- shared_preload_libraries = 'pg_stat_statements'
```

#### Redis Tuning

```bash
# redis.conf optimizations
maxmemory 512mb
maxmemory-policy allkeys-lru
tcp-keepalive 300
timeout 0
```

### Logging and Debugging

#### Enable Debug Logging

```bash
# Temporary debug logging
export LOG_LEVEL=debug
sudo systemctl restart obsidian

# Check logs
journalctl -u obsidian -f
tail -f /var/log/obsidian/app.log
```

#### Performance Profiling

```bash
# Enable pprof endpoint (development only)
curl http://localhost:8082/debug/pprof/
curl http://localhost:8082/debug/pprof/goroutine
curl http://localhost:8082/debug/pprof/heap
```

---

This deployment guide covers the most common scenarios for deploying Obsidian Sentinel WAF in production environments. For specific questions or additional configurations, refer to the main documentation or create an issue in the GitHub repository.