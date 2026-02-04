# Obsidian Sentinel WAF v2.2.2 - API Documentation

This document provides comprehensive API documentation for the Obsidian Sentinel WAF Enterprise Edition.

## Table of Contents

- [Overview](#overview)
- [Authentication](#authentication)
- [Base URLs](#base-urls)
- [Common Headers](#common-headers)
- [Error Handling](#error-handling)
- [Rate Limiting](#rate-limiting)
- [API Endpoints](#api-endpoints)
  - [Authentication APIs](#authentication-apis)
  - [WAF Management APIs](#waf-management-apis)
  - [Security Analytics APIs](#security-analytics-apis)
  - [Geographic Security APIs](#geographic-security-apis)
  - [Threat Intelligence APIs](#threat-intelligence-apis)
  - [Webhook Management APIs](#webhook-management-apis)
  - [System Management APIs](#system-management-apis)
  - [Monitoring APIs](#monitoring-apis)

## Overview

The Obsidian Sentinel WAF API provides comprehensive access to all enterprise security features including:

- 🔐 **Authentication & Authorization**
- 🛡️ **WAF Rule Management**
- 📊 **Security Analytics & Reporting**  
- 🌍 **Geographic IP Blocking**
- 🎯 **Threat Intelligence Integration**
- 📡 **Real-time Webhook Alerts**
- ⚙️ **System Configuration & Health**
- 📈 **Performance Monitoring**

### API Characteristics

- **RESTful Design**: Standard HTTP methods (GET, POST, PUT, DELETE)
- **JSON Format**: All requests and responses use JSON
- **JWT Authentication**: Bearer token authentication required
- **Rate Limited**: Configurable rate limiting per endpoint
- **Real-time**: WebSocket support for live updates
- **Versioned**: API versioning for backward compatibility

## Authentication

### JWT Token Authentication

All API endpoints (except `/api/login` and `/api/health`) require a valid JWT token.

#### Login Request

```http
POST /api/login
Content-Type: application/json

{
  "username": "admin",
  "password": "your-password"
}
```

#### Login Response

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires": "2024-12-25T10:30:00Z",
  "user": {
    "username": "admin",
    "role": "administrator",
    "permissions": ["read", "write", "admin"]
  }
}
```

#### Using the Token

Include the JWT token in the Authorization header:

```http
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

## Base URLs

| Environment | Base URL |
|-------------|----------|
| Development | `http://localhost:8082/api` |
| Staging | `https://waf-staging.company.com/api` |
| Production | `https://waf.company.com/api` |

## Common Headers

### Required Headers

```http
Content-Type: application/json
Authorization: Bearer {jwt_token}
```

### Optional Headers

```http
X-Request-ID: unique-request-identifier
User-Agent: YourApp/1.0.0
Accept: application/json
```

## Error Handling

### Standard Error Response

```json
{
  "error": {
    "code": "INVALID_REQUEST",
    "message": "The request is invalid or malformed",
    "details": "Field 'country_code' is required",
    "timestamp": "2024-12-24T10:30:00Z",
    "request_id": "req-123456789"
  }
}
```

### HTTP Status Codes

| Code | Meaning | Description |
|------|---------|-------------|
| `200` | OK | Request successful |
| `201` | Created | Resource created successfully |
| `400` | Bad Request | Invalid request format or parameters |
| `401` | Unauthorized | Authentication required or invalid token |
| `403` | Forbidden | Insufficient permissions |
| `404` | Not Found | Resource not found |
| `429` | Too Many Requests | Rate limit exceeded |
| `500` | Internal Server Error | Server error occurred |

### Error Codes

| Code | Description |
|------|-------------|
| `AUTHENTICATION_FAILED` | Invalid credentials |
| `TOKEN_EXPIRED` | JWT token has expired |
| `INSUFFICIENT_PERMISSIONS` | User lacks required permissions |
| `INVALID_REQUEST` | Request format or parameters invalid |
| `RESOURCE_NOT_FOUND` | Requested resource doesn't exist |
| `RATE_LIMIT_EXCEEDED` | Too many requests |
| `VALIDATION_ERROR` | Input validation failed |
| `INTERNAL_ERROR` | Server-side error occurred |

## Rate Limiting

### Rate Limit Headers

```http
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1640349600
X-RateLimit-Window: 3600
```

### Rate Limit Tiers

| Endpoint Pattern | Limit | Window |
|-----------------|--------|---------|
| `/api/login` | 5 requests | 1 minute |
| `/api/admin/*` | 100 requests | 1 hour |
| `/api/analytics/*` | 1000 requests | 1 hour |
| `/api/geoip/*` | 500 requests | 1 hour |
| `/api/metrics` | 10000 requests | 1 hour |

## API Endpoints

## Authentication APIs

### Login

Authenticate user and obtain JWT token.

```http
POST /api/login
```

**Request Body:**

```json
{
  "username": "admin",
  "password": "secure-password"
}
```

**Response:**

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires": "2024-12-25T10:30:00Z",
  "user": {
    "username": "admin",
    "role": "administrator"
  }
}
```

### Refresh Token

Refresh an existing JWT token.

```http
POST /api/refresh
Authorization: Bearer {token}
```

**Response:**

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires": "2024-12-25T11:30:00Z"
}
```

### Logout

Invalidate current JWT token.

```http
POST /api/logout
Authorization: Bearer {token}
```

**Response:**

```json
{
  "message": "Successfully logged out"
}
```

## WAF Management APIs

### Get WAF Statistics

Retrieve comprehensive WAF performance metrics.

```http
GET /api/waf/stats
Authorization: Bearer {token}
```

**Response:**

```json
{
  "requests_processed": 1250000,
  "threats_blocked": 2041,
  "rules_active": 59,
  "uptime_seconds": 345600,
  "average_response_time_ms": 12.5,
  "memory_usage_mb": 245.7,
  "cpu_usage_percent": 15.3,
  "last_updated": "2024-12-24T10:30:00Z"
}
```

### Get Active Rules

Retrieve all active WAF rules.

```http
GET /api/waf/rules
Authorization: Bearer {token}
```

**Query Parameters:**

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `page` | integer | Page number | 1 |
| `limit` | integer | Items per page | 50 |
| `category` | string | Rule category filter | - |
| `severity` | string | Rule severity filter | - |
| `enabled` | boolean | Filter by enabled status | - |

**Response:**

```json
{
  "rules": [
    {
      "id": "950001",
      "name": "SQL Injection Attack",
      "category": "injection",
      "severity": "critical",
      "enabled": true,
      "description": "Detects SQL injection attempts",
      "pattern": "(?i)(union.*select|select.*from|insert.*into)",
      "actions": ["block", "log"],
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-12-24T10:30:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 50,
    "total": 59,
    "total_pages": 2
  }
}
```

### Create WAF Rule

Create a new WAF rule.

```http
POST /api/waf/rules
Authorization: Bearer {token}
Content-Type: application/json
```

**Request Body:**

```json
{
  "name": "Custom XSS Protection",
  "category": "xss",
  "severity": "high",
  "description": "Custom XSS pattern detection",
  "pattern": "(?i)(<script|javascript:|onload=|onerror=)",
  "actions": ["block", "log", "alert"],
  "enabled": true,
  "tags": ["custom", "xss", "javascript"]
}
```

**Response:**

```json
{
  "id": "custom_001",
  "message": "Rule created successfully",
  "rule": {
    "id": "custom_001",
    "name": "Custom XSS Protection",
    "category": "xss",
    "severity": "high",
    "enabled": true,
    "created_at": "2024-12-24T10:30:00Z"
  }
}
```

### Update WAF Rule

Update an existing WAF rule.

```http
PUT /api/waf/rules/{rule_id}
Authorization: Bearer {token}
Content-Type: application/json
```

**Request Body:**

```json
{
  "name": "Updated XSS Protection",
  "enabled": false,
  "severity": "medium"
}
```

### Delete WAF Rule

Delete a WAF rule.

```http
DELETE /api/waf/rules/{rule_id}
Authorization: Bearer {token}
```

**Response:**

```json
{
  "message": "Rule deleted successfully"
}
```

## Security Analytics APIs

### Get Security Dashboard

Retrieve comprehensive security analytics for dashboard.

```http
GET /api/analytics/dashboard
Authorization: Bearer {token}
```

**Query Parameters:**

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `timeframe` | string | Time period (1h,24h,7d,30d) | 24h |
| `timezone` | string | Timezone (UTC, America/New_York) | UTC |

**Response:**

```json
{
  "timeframe": "24h",
  "summary": {
    "total_requests": 1250000,
    "blocked_requests": 2041,
    "allowed_requests": 1247959,
    "block_rate": 0.16,
    "top_threats": [
      {
        "type": "SQL Injection",
        "count": 892,
        "percentage": 43.7
      },
      {
        "type": "XSS",
        "count": 634,
        "percentage": 31.1
      }
    ]
  },
  "timeline": [
    {
      "timestamp": "2024-12-24T09:00:00Z",
      "requests": 52083,
      "blocked": 85,
      "allowed": 51998
    }
  ],
  "geographic": {
    "blocked_countries": [
      {
        "country": "CN",
        "country_name": "China",
        "count": 1245
      },
      {
        "country": "RU", 
        "country_name": "Russia",
        "count": 892
      }
    ]
  },
  "top_ips": [
    {
      "ip": "203.0.113.42",
      "requests": 1250,
      "blocked": 1245,
      "country": "CN"
    }
  ]
}
```

### Get Threat Analytics

Retrieve detailed threat analysis and patterns.

```http
GET /api/analytics/threats
Authorization: Bearer {token}
```

**Query Parameters:**

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `timeframe` | string | Time period | 24h |
| `threat_type` | string | Filter by threat type | - |
| `severity` | string | Filter by severity | - |

**Response:**

```json
{
  "threats": [
    {
      "type": "SQL Injection",
      "severity": "critical", 
      "count": 892,
      "trend": "increasing",
      "first_seen": "2024-12-24T08:15:00Z",
      "last_seen": "2024-12-24T10:28:00Z",
      "patterns": [
        "' OR '1'='1",
        "UNION SELECT",
        "; DROP TABLE"
      ],
      "affected_endpoints": [
        "/api/users",
        "/login",
        "/search"
      ]
    }
  ],
  "attack_vectors": [
    {
      "vector": "Query Parameters",
      "count": 1456,
      "percentage": 65.2
    },
    {
      "vector": "POST Body",
      "count": 585,
      "percentage": 26.2
    }
  ]
}
```

### Get Security Logs

Retrieve security event logs with filtering.

```http
GET /api/analytics/logs
Authorization: Bearer {token}
```

**Query Parameters:**

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `page` | integer | Page number | 1 |
| `limit` | integer | Items per page | 100 |
| `severity` | string | Filter by severity | - |
| `ip` | string | Filter by IP address | - |
| `country` | string | Filter by country code | - |
| `rule_id` | string | Filter by rule ID | - |
| `start_time` | string | Start time (ISO 8601) | - |
| `end_time` | string | End time (ISO 8601) | - |

**Response:**

```json
{
  "logs": [
    {
      "id": "log_67890123",
      "timestamp": "2024-12-24T10:28:45Z",
      "severity": "critical",
      "rule_id": "950001",
      "rule_name": "SQL Injection Attack",
      "action": "blocked",
      "source_ip": "203.0.113.42",
      "country": "CN",
      "user_agent": "curl/7.68.0",
      "request": {
        "method": "POST",
        "url": "/api/users",
        "headers": {
          "Content-Type": "application/json"
        },
        "body": "username=admin' OR '1'='1--"
      },
      "response": {
        "status": 403,
        "blocked_reason": "SQL injection pattern detected"
      },
      "geo_info": {
        "country": "China",
        "city": "Beijing",
        "latitude": 39.9042,
        "longitude": 116.4074
      }
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 100,
    "total": 2041,
    "total_pages": 21
  }
}
```

## Geographic Security APIs

### Get Blocked Countries

Retrieve list of blocked countries with statistics.

```http
GET /api/geoip/blocked-countries
Authorization: Bearer {token}
```

**Response:**

```json
{
  "blocked_countries": [
    {
      "country_code": "CN",
      "country_name": "China",
      "blocked_requests": 1245,
      "last_blocked": "2024-12-24T10:28:45Z",
      "enabled": true,
      "added_date": "2024-01-15T10:00:00Z"
    },
    {
      "country_code": "RU",
      "country_name": "Russia", 
      "blocked_requests": 892,
      "last_blocked": "2024-12-24T10:25:12Z",
      "enabled": true,
      "added_date": "2024-01-20T14:30:00Z"
    }
  ],
  "total_countries": 2,
  "total_blocked_requests": 2137
}
```

### Block Country

Add a country to the blocked list.

```http
POST /api/geoip/block-country
Authorization: Bearer {token}
Content-Type: application/json
```

**Request Body:**

```json
{
  "country_code": "KP",
  "reason": "High-risk country blocking policy"
}
```

**Response:**

```json
{
  "message": "Country KP (North Korea) has been blocked successfully",
  "country": {
    "country_code": "KP",
    "country_name": "North Korea",
    "blocked_date": "2024-12-24T10:30:00Z",
    "enabled": true
  }
}
```

### Unblock Country

Remove a country from the blocked list.

```http
DELETE /api/geoip/unblock-country/{country_code}
Authorization: Bearer {token}
```

**Response:**

```json
{
  "message": "Country CN (China) has been unblocked successfully"
}
```

### GeoIP Lookup

Look up geographic information for an IP address.

```http
GET /api/geoip/lookup
Authorization: Bearer {token}
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `ip` | string | Yes | IP address to lookup |

**Response:**

```json
{
  "ip": "203.0.113.42",
  "country_code": "CN",
  "country_name": "China",
  "city": "Beijing",
  "latitude": 39.9042,
  "longitude": 116.4074,
  "is_blocked": true,
  "block_reason": "Country is in blocked list"
}
```

## Threat Intelligence APIs

### Get Threat Intelligence Status

Retrieve current threat intelligence feed status.

```http
GET /api/threat-intel/status
Authorization: Bearer {token}
```

**Response:**

```json
{
  "feeds": [
    {
      "name": "Malware IPs",
      "enabled": true,
      "last_updated": "2024-12-24T10:00:00Z",
      "entries_count": 12456,
      "source": "threat_feed_provider",
      "update_frequency": "hourly"
    },
    {
      "name": "Botnet C&C",
      "enabled": true,
      "last_updated": "2024-12-24T09:30:00Z", 
      "entries_count": 8923,
      "source": "security_vendor",
      "update_frequency": "hourly"
    }
  ],
  "total_threats": 21379,
  "last_global_update": "2024-12-24T10:00:00Z"
}
```

### Check IP Threat Status

Check if an IP address is in threat intelligence feeds.

```http
GET /api/threat-intel/check-ip
Authorization: Bearer {token}
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `ip` | string | Yes | IP address to check |

**Response:**

```json
{
  "ip": "198.51.100.42",
  "is_threat": true,
  "threat_types": ["malware", "botnet"],
  "risk_score": 85,
  "first_seen": "2024-12-20T15:30:00Z",
  "last_seen": "2024-12-24T09:45:00Z",
  "sources": [
    "Malware IPs",
    "Botnet C&C"
  ]
}
```

### Update Threat Intelligence Feeds

Manually trigger threat intelligence feed update.

```http
POST /api/threat-intel/update
Authorization: Bearer {token}
```

**Response:**

```json
{
  "message": "Threat intelligence feeds update initiated",
  "update_id": "update_123456789",
  "estimated_completion": "2024-12-24T10:35:00Z"
}
```

## Webhook Management APIs

### Get Webhooks

Retrieve configured webhooks.

```http
GET /api/webhooks
Authorization: Bearer {token}
```

**Response:**

```json
{
  "webhooks": [
    {
      "id": "webhook_001",
      "name": "Slack Security Alerts",
      "url": "https://hooks.slack.com/services/...",
      "events": ["threat_detected", "country_blocked", "rule_triggered"],
      "enabled": true,
      "last_triggered": "2024-12-24T10:15:00Z",
      "success_count": 245,
      "failure_count": 2,
      "created_at": "2024-01-10T12:00:00Z"
    }
  ]
}
```

### Create Webhook

Create a new webhook endpoint.

```http
POST /api/webhooks
Authorization: Bearer {token}
Content-Type: application/json
```

**Request Body:**

```json
{
  "name": "Discord Alerts",
  "url": "https://discord.com/api/webhooks/...",
  "events": ["critical_threat", "system_error"],
  "headers": {
    "Content-Type": "application/json",
    "X-API-Key": "secret-key"
  },
  "enabled": true,
  "retry_attempts": 3,
  "timeout_seconds": 30
}
```

**Response:**

```json
{
  "id": "webhook_002",
  "message": "Webhook created successfully",
  "webhook": {
    "id": "webhook_002",
    "name": "Discord Alerts",
    "url": "https://discord.com/api/webhooks/...",
    "enabled": true,
    "created_at": "2024-12-24T10:30:00Z"
  }
}
```

### Test Webhook

Send a test payload to a webhook.

```http
POST /api/webhooks/{webhook_id}/test
Authorization: Bearer {token}
```

**Response:**

```json
{
  "message": "Test webhook sent successfully",
  "response_status": 200,
  "response_time_ms": 245
}
```

### Update Webhook

Update webhook configuration.

```http
PUT /api/webhooks/{webhook_id}
Authorization: Bearer {token}
Content-Type: application/json
```

**Request Body:**

```json
{
  "name": "Updated Discord Alerts",
  "enabled": false,
  "events": ["critical_threat"]
}
```

### Delete Webhook

Delete a webhook.

```http
DELETE /api/webhooks/{webhook_id}
Authorization: Bearer {token}
```

**Response:**

```json
{
  "message": "Webhook deleted successfully"
}
```

## System Management APIs

### Get System Health

Retrieve comprehensive system health information.

```http
GET /api/health
```

**Response:**

```json
{
  "status": "healthy",
  "version": "2.1.0",
  "uptime_seconds": 345600,
  "timestamp": "2024-12-24T10:30:00Z",
  "services": {
    "database": {
      "status": "healthy",
      "connections_active": 15,
      "connections_max": 100,
      "response_time_ms": 2.3
    },
    "redis": {
      "status": "healthy",
      "memory_used_mb": 45.2,
      "memory_max_mb": 512,
      "response_time_ms": 0.8
    },
    "geoip": {
      "status": "healthy",
      "database_version": "2024-12-01",
      "last_updated": "2024-12-24T00:00:00Z"
    }
  },
  "system": {
    "cpu_usage_percent": 15.3,
    "memory_usage_mb": 245.7,
    "memory_total_mb": 1024,
    "disk_usage_percent": 35.2,
    "load_average": [0.5, 0.7, 0.8]
  }
}
```

### Get System Configuration

Retrieve current system configuration.

```http
GET /api/admin/config
Authorization: Bearer {token}
```

**Response:**

```json
{
  "waf": {
    "rules_enabled": true,
    "default_action": "block",
    "log_level": "info",
    "rate_limiting_enabled": true
  },
  "geoip": {
    "blocking_enabled": true,
    "database_path": "/data/GeoLite2-Country.mmdb",
    "blocked_countries": ["CN", "RU"]
  },
  "security": {
    "jwt_expiry_hours": 24,
    "session_timeout_minutes": 30,
    "max_login_attempts": 5
  },
  "integrations": {
    "webhook_timeout_seconds": 30,
    "threat_intel_enabled": true,
    "threat_intel_update_frequency": "hourly"
  }
}
```

### Update System Configuration

Update system configuration settings.

```http
PUT /api/admin/config
Authorization: Bearer {token}
Content-Type: application/json
```

**Request Body:**

```json
{
  "waf": {
    "log_level": "debug",
    "rate_limiting_enabled": false
  },
  "security": {
    "jwt_expiry_hours": 12
  }
}
```

**Response:**

```json
{
  "message": "Configuration updated successfully",
  "changes": [
    "waf.log_level changed from 'info' to 'debug'",
    "waf.rate_limiting_enabled changed from true to false",
    "security.jwt_expiry_hours changed from 24 to 12"
  ]
}
```

## Monitoring APIs

### Get Prometheus Metrics

Retrieve Prometheus-compatible metrics.

```http
GET /api/metrics
Authorization: Bearer {token}
```

**Response:**

```text
# HELP obsidian_requests_total Total number of requests processed
# TYPE obsidian_requests_total counter
obsidian_requests_total 1250000

# HELP obsidian_blocked_requests_total Total number of blocked requests
# TYPE obsidian_blocked_requests_total counter
obsidian_blocked_requests_total 2041

# HELP obsidian_response_time_seconds Response time in seconds
# TYPE obsidian_response_time_seconds histogram
obsidian_response_time_seconds_bucket{le="0.005"} 845623
obsidian_response_time_seconds_bucket{le="0.01"} 1198765
obsidian_response_time_seconds_bucket{le="0.025"} 1245890
obsidian_response_time_seconds_bucket{le="0.05"} 1249456
obsidian_response_time_seconds_bucket{le="0.1"} 1249890
obsidian_response_time_seconds_bucket{le="0.25"} 1249990
obsidian_response_time_seconds_bucket{le="0.5"} 1250000
obsidian_response_time_seconds_bucket{le="1"} 1250000
obsidian_response_time_seconds_bucket{le="2.5"} 1250000
obsidian_response_time_seconds_bucket{le="5"} 1250000
obsidian_response_time_seconds_bucket{le="10"} 1250000
obsidian_response_time_seconds_bucket{le="+Inf"} 1250000
obsidian_response_time_seconds_sum 15625.123
obsidian_response_time_seconds_count 1250000

# HELP obsidian_rules_active Number of active WAF rules
# TYPE obsidian_rules_active gauge
obsidian_rules_active 59

# HELP obsidian_geoip_blocks_total Total GeoIP blocks by country
# TYPE obsidian_geoip_blocks_total counter
obsidian_geoip_blocks_total{country="CN"} 1245
obsidian_geoip_blocks_total{country="RU"} 892
```

### Get Performance Metrics

Retrieve detailed performance metrics.

```http
GET /api/admin/metrics/performance
Authorization: Bearer {token}
```

**Query Parameters:**

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `timeframe` | string | Time period (1h,24h,7d) | 1h |
| `interval` | string | Data interval (1m,5m,1h) | 5m |

**Response:**

```json
{
  "timeframe": "1h",
  "interval": "5m",
  "metrics": [
    {
      "timestamp": "2024-12-24T10:00:00Z",
      "requests_per_second": 347.2,
      "response_time_avg_ms": 12.5,
      "response_time_p95_ms": 45.2,
      "response_time_p99_ms": 78.9,
      "cpu_usage_percent": 15.3,
      "memory_usage_mb": 245.7,
      "blocked_requests_per_second": 0.56
    }
  ],
  "summary": {
    "total_requests": 1250000,
    "avg_response_time_ms": 12.5,
    "max_response_time_ms": 245.8,
    "avg_cpu_usage_percent": 15.3,
    "avg_memory_usage_mb": 245.7
  }
}
```

### WebSocket Real-time Updates

Connect to real-time event stream.

```javascript
const ws = new WebSocket('wss://waf.company.com/api/ws/events');
ws.addEventListener('message', (event) => {
  const data = JSON.parse(event.data);
  console.log('Real-time event:', data);
});
```

**WebSocket Message Format:**

```json
{
  "type": "threat_detected",
  "timestamp": "2024-12-24T10:30:00Z",
  "data": {
    "rule_id": "950001",
    "severity": "critical",
    "source_ip": "203.0.113.42",
    "country": "CN",
    "blocked": true
  }
}
```

**Event Types:**

- `threat_detected` - New threat detected
- `country_blocked` - GeoIP country block
- `rule_triggered` - WAF rule triggered  
- `system_alert` - System health alert
- `config_changed` - Configuration updated

---

## SDK Examples

### Python SDK Example

```python
import requests
import json

class ObsidianClient:
    def __init__(self, base_url, username, password):
        self.base_url = base_url
        self.token = None
        self.login(username, password)
    
    def login(self, username, password):
        response = requests.post(
            f"{self.base_url}/api/login",
            json={"username": username, "password": password}
        )
        response.raise_for_status()
        self.token = response.json()["token"]
    
    def get_headers(self):
        return {
            "Authorization": f"Bearer {self.token}",
            "Content-Type": "application/json"
        }
    
    def get_waf_stats(self):
        response = requests.get(
            f"{self.base_url}/api/waf/stats",
            headers=self.get_headers()
        )
        return response.json()
    
    def block_country(self, country_code, reason="Security policy"):
        response = requests.post(
            f"{self.base_url}/api/geoip/block-country",
            json={"country_code": country_code, "reason": reason},
            headers=self.get_headers()
        )
        return response.json()

# Usage
client = ObsidianClient("https://waf.company.com", "admin", "password")
stats = client.get_waf_stats()
print(f"Threats blocked: {stats['threats_blocked']}")
```

### JavaScript SDK Example

```javascript
class ObsidianClient {
    constructor(baseUrl) {
        this.baseUrl = baseUrl;
        this.token = null;
    }

    async login(username, password) {
        const response = await fetch(`${this.baseUrl}/api/login`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ username, password })
        });
        
        if (!response.ok) throw new Error('Login failed');
        
        const data = await response.json();
        this.token = data.token;
        return data;
    }

    async apiRequest(endpoint, options = {}) {
        const response = await fetch(`${this.baseUrl}${endpoint}`, {
            ...options,
            headers: {
                'Authorization': `Bearer ${this.token}`,
                'Content-Type': 'application/json',
                ...options.headers
            }
        });
        
        if (!response.ok) throw new Error(`API Error: ${response.status}`);
        return await response.json();
    }

    async getWafStats() {
        return await this.apiRequest('/api/waf/stats');
    }

    async getSecurityLogs(page = 1, limit = 100) {
        return await this.apiRequest(`/api/analytics/logs?page=${page}&limit=${limit}`);
    }
}

// Usage
const client = new ObsidianClient('https://waf.company.com');
await client.login('admin', 'password');
const stats = await client.getWafStats();
console.log(`Threats blocked: ${stats.threats_blocked}`);
```

---

This API documentation provides comprehensive coverage of all Obsidian Sentinel WAF v2.1.0 Enterprise Edition endpoints. For additional questions or support, please refer to the main documentation or contact the development team.