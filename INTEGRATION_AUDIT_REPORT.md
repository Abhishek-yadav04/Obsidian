# 🔍 Obsidian WAF - In-Depth Integration Audit Report

**Date**: February 4, 2026  
**Version**: v2.2.2  
**Auditor**: Automated Code Analysis

---

## Executive Summary

This audit identifies **orphaned code, duplicate functionality, and underutilized packages** in the Obsidian WAF project. The goal is to maximize code utilization and eliminate technical debt.

### Key Findings

| Category | Count | Impact |
|----------|-------|--------|
| **Duplicate Applications** | 2 | HIGH - Maintenance burden |
| **Orphaned Packages** | 6 | MEDIUM - Dead code |
| **Underutilized Features** | 4 | HIGH - Missing functionality |
| **Duplicate UI Files** | 1 | LOW - Confusion |
| **Outdated Examples** | 1 | LOW - Developer confusion |

---

## 🚨 CRITICAL: Duplicate Applications

### Finding 1: Two Separate Main Applications

| Application | Location | Lines | Purpose | Port |
|-------------|----------|-------|---------|------|
| **Obsidian (Main)** | `cmd/obsidian/main.go` | 1716 | Production WAF | 8082 |
| **Sentinel API** | `cmd/sentinel-api/main.go` | 1181 | Alternative API | 8090 |

#### What Each Application Uses

**cmd/obsidian** imports (15+ internal packages):
```
✅ alerts, api, auth, cache, database, geoip, logging, metrics, 
   model, ratelimit, report, requestid, security, store, threat, waf
```

**cmd/sentinel-api** imports (6 unique packages NOT in cmd/obsidian):
```
⚠️ apikeys, graphql, hibp, ipallow, respbody, secrets
```

#### Analysis

The `cmd/sentinel-api` has **6 valuable security packages** that are NOT integrated into the main application:

1. **apikeys** - API key management with scopes and rate limiting
2. **graphql** - GraphQL query security (depth, complexity, introspection blocking)
3. **hibp** - Have I Been Pwned password breach checking
4. **ipallow** - Admin IP allowlisting for restricted access
5. **respbody** - Response body inspection for data leakage (SSN, credit cards, API keys)
6. **secrets** - Hot-reload support for credentials

#### Recommendation: INTEGRATE or CONSOLIDATE

**Option A (Recommended)**: Migrate unique features from `sentinel-api` into `cmd/obsidian`:
- Estimated effort: 2-3 days
- Benefit: Single unified application, all features available

**Option B**: Keep both but document clearly as separate deployment options
- Risk: Maintenance burden, feature divergence

#### Impact if Removed

Removing `cmd/sentinel-api` without integration would **lose these security features**:
- No GraphQL attack protection
- No password breach checking
- No API key management
- No response body DLP scanning
- No IP allowlisting for admin
- No secrets hot-reload

---

## 🔶 Orphaned Packages (Not Used by Main App)

### Package 1: `internal/app/authservice`

**Location**: `internal/app/authservice/service.go` (473 lines)

**What It Does**:
- Enterprise authentication service
- Account lockout after failed attempts
- Password expiry enforcement
- Session management with limits
- Integrates with `persistence` and `security` packages

**Current Status**: NOT IMPORTED by either main application

**Why It's Valuable**:
```go
// Features in authservice that cmd/obsidian lacks:
- MaxFailedAttempts: 5 (account lockout)
- LockoutDuration: 15 * time.Minute
- PasswordExpiryDays: 90
- SessionInactivityTimeout: 30 * time.Minute
- MaxConcurrentSessions: 3
```

**Recommendation**: **INTEGRATE** - This is an improved auth implementation

---

### Package 2: `internal/app/persistence`

**Location**: `internal/app/persistence/postgres.go` (766 lines)

**What It Does**:
- Complete `Store` interface with 20+ methods
- PostgreSQL implementation for:
  - User CRUD operations
  - Log storage and filtering
  - Rule management
  - Audit logging
  - Session management

**Current Status**: Only used by `authservice` (which is also orphaned)

**Why It's Valuable**:
- cmd/obsidian uses `internal/app/database` and `internal/app/store` separately
- This provides a unified interface that could replace both

**Recommendation**: **EVALUATE** - Compare with current database/store implementation

---

### Package 3: `internal/app/apikeys`

**Location**: `internal/app/apikeys/apikeys.go` (354 lines)

**What It Does**:
```go
// API Key Management Features:
- Secure key generation (obs_<random_hex>)
- SHA256 hashing (never store plaintext)
- Scope-based access: Read, Write, Admin, Threat, Export, Webhook
- Per-key rate limiting
- Key expiration support
- Usage tracking (last used, IP)
```

**Current Status**: Only used by `sentinel-api`

**Integration Value**: **HIGH** - Enables service-to-service authentication

---

### Package 4: `internal/app/graphql`

**Location**: `internal/app/graphql/graphql.go` (377 lines)

**What It Does**:
```go
// GraphQL Security Features:
- Query depth limiting (default: 10)
- Query complexity analysis (default: 1000)
- Introspection blocking (production)
- Batch query limiting
- Alias count limits
- Custom field complexity weights
```

**Current Status**: Only used by `sentinel-api`

**Integration Value**: **MEDIUM** - Only if protecting GraphQL APIs

---

### Package 5: `internal/app/hibp`

**Location**: `internal/app/hibp/hibp.go` (307 lines)

**What It Does**:
```go
// Have I Been Pwned Password Checking:
- K-anonymity (only first 5 SHA1 chars sent)
- Configurable breach threshold
- Response caching (24h default)
- Enforce on registration/password change
- Warn-only mode option
```

**Current Status**: Only used by `sentinel-api`

**Integration Value**: **HIGH** - Critical for password security

**Note**: cmd/obsidian has a `/api/security/password/check` endpoint but it's CLIENT-SIDE only in the UI. This server-side implementation would be more secure.

---

### Package 6: `internal/app/respbody`

**Location**: `internal/app/respbody/respbody.go` (457 lines)

**What It Does**:
```go
// Response Body Data Leakage Prevention:
- SSN detection (US Social Security Numbers)
- Credit card number detection
- API key pattern detection
- AWS key detection
- Private key (PEM) detection
- JWT token detection
- Password field detection
- Bulk email exposure (>5 emails)
- Bulk IP exposure (>10 IPs)
- Custom regex patterns
- Block or warn on detection
```

**Current Status**: Only used by `sentinel-api`

**Integration Value**: **CRITICAL** - Data Loss Prevention is enterprise-essential

---

### Package 7: `internal/app/secrets`

**Location**: `internal/app/secrets/secrets.go` (313 lines)

**What It Does**:
```go
// Secrets Hot-Reload Features:
- Load from environment variables
- Version tracking
- Reload callbacks
- Masked display (first 4 + last 4 chars)
- JWT rotation support
- Multiple secret types: JWT, DB, Redis, API Key, Webhook, Encryption
```

**Current Status**: Only used by `sentinel-api`

**Integration Value**: **HIGH** - Zero-downtime secret rotation

---

### Package 8: `internal/app/ipallow`

**Location**: `internal/app/ipallow/ipallow.go` (396 lines)

**What It Does**:
```go
// Admin IP Allowlisting:
- Single IP or CIDR range support
- Enforce for API endpoints
- Enforce for dashboard access
- Localhost bypass option
- Header-based bypass secret
- Hit tracking and statistics
```

**Current Status**: Only used by `sentinel-api`

**Integration Value**: **HIGH** - Admin access restriction

**Note**: cmd/obsidian has `IPAllowEntry` but it's a simplified in-memory implementation. This package is more complete with persistence support.

---

## 🔵 Duplicate/Outdated Files

### Finding: Duplicate JavaScript Files

| File | Lines | Purpose |
|------|-------|---------|
| `cmd/obsidian/ui/js/app.js` | 2236 | **Production dashboard** |
| `docs/js/app.js` | 1283 | Documentation site (?) |

**Analysis**:
- Both have same structure (`ObsidianApp` object)
- `cmd/obsidian/ui/js/app.js` is more complete and up-to-date
- `docs/js/app.js` appears to be an older copy

**Recommendation**: 
- If `docs/` is a static documentation site → Keep but consider if JS is needed
- If `docs/` mirrors the dashboard → **REMOVE** the duplicate

---

### Finding: Outdated Example

**Location**: `examples/http-server/`

**Contents**:
```
examples/http-server/
├── ui/
│   ├── index.html (450 lines - old dashboard)
│   └── login.html
├── default.conf
├── go.mod
├── main_test.go (references old paths)
├── README.md
└── testdata/
```

**Analysis**:
- No `main.go` - not a runnable example
- UI files are outdated (superseded by `cmd/obsidian/ui/`)
- Tests reference `examples/http-server/ui/` which is old

**Recommendation**: 
- **OPTION A**: Add a proper `main.go` example showing WAF integration
- **OPTION B**: Remove entirely if `cmd/obsidian` serves as the example

---

## 🟢 Well-Integrated Components

These are properly used and should be **kept**:

| Component | Used By | Status |
|-----------|---------|--------|
| `experimental/` | Root waf.go, internal packages | ✅ Core plugin system |
| `collection/` | internal/collections/* | ✅ WAF variable storage |
| `types/` | 20+ files | ✅ Core API types |
| `debuglog/` | All WAF packages | ✅ Logging infrastructure |
| `http/` | tests, e2e | ✅ But NOT used by cmd/obsidian |
| `config.go` | Public API | ✅ Configuration |
| `waf.go` | Public API | ✅ Entry point |
| `mage.go` + `magefile.go` | Build system | ✅ Build automation |

---

## 🛠️ Utility Tools

### `cmd/genhash/main.go`

**Purpose**: Generate bcrypt password hashes for default users

**Usage**:
```bash
go run cmd/genhash/main.go
# Output:
# admin: $2a$12$...
# analyst: $2a$12$...
# viewer: $2a$12$...
```

**Status**: **KEEP** - Useful utility for setup

**Recommendation**: Document in README under "Development Tools"

---

## 📋 Integration Plan

### Phase 1: High-Value Package Integration (Priority: HIGH)

**Estimated Effort**: 3-4 days

#### Step 1.1: Integrate `internal/app/hibp` into cmd/obsidian

```go
// Add to main.go imports
import "github.com/corazawaf/coraza/v3/internal/app/hibp"

// Initialize
hibpChecker := hibp.NewChecker(hibp.DefaultConfig())

// Add handler
mux.HandleFunc("/api/security/password/breach-check", handleHIBPCheck)
```

#### Step 1.2: Integrate `internal/app/respbody` for DLP

```go
// Add to WAF middleware
respInspector := respbody.NewInspector(respbody.DefaultConfig())

// Wrap response writer to inspect
type dlpResponseWriter struct {
    http.ResponseWriter
    inspector *respbody.Inspector
    buffer    *bytes.Buffer
}
```

#### Step 1.3: Integrate `internal/app/secrets` for hot-reload

```go
// Replace hardcoded secret loading
secretsManager := secrets.NewManager()
secretsManager.LoadFromEnv(secrets.SecretJWT, "OBSIDIAN_JWT_SECRET")

// Add reload endpoint
mux.HandleFunc("/api/admin/secrets/reload", handleSecretsReload)
```

### Phase 2: Security Enhancement (Priority: MEDIUM)

**Estimated Effort**: 2-3 days

#### Step 2.1: Integrate `internal/app/apikeys`

- Add API key authentication middleware
- Add key management endpoints
- Support scoped access

#### Step 2.2: Integrate `internal/app/ipallow`

- Replace simplified `IPAllowEntry` with full implementation
- Add CIDR support
- Add admin-only enforcement

### Phase 3: Cleanup (Priority: LOW)

**Estimated Effort**: 1 day

1. Remove or update `docs/js/app.js`
2. Create proper example in `examples/http-server/` or remove
3. Document `cmd/genhash` utility
4. Decide fate of `cmd/sentinel-api`

---

## 📊 Impact Summary

### If Orphaned Packages Are Removed (WITHOUT Integration)

| Package | Lost Functionality |
|---------|-------------------|
| authservice | Account lockout, password expiry, session limits |
| persistence | Unified Store interface |
| apikeys | Service-to-service auth, scoped access |
| graphql | GraphQL attack protection |
| hibp | Password breach checking |
| respbody | Data leakage prevention (SSN, CC, keys) |
| secrets | Zero-downtime secret rotation |
| ipallow | Admin IP restriction |

**Total Lost Lines of Code**: ~3,543 lines of valuable security features

### If Packages Are Integrated

| Benefit | Value |
|---------|-------|
| Password breach protection | Prevents credential stuffing |
| DLP scanning | Compliance (PCI-DSS, HIPAA) |
| API key management | B2B integrations |
| Secrets hot-reload | Zero-downtime operations |
| Admin IP allowlisting | Attack surface reduction |

---

## ✅ Recommended Actions

### Immediate (Do Now)
1. ✅ Document that `cmd/sentinel-api` exists and has additional features
2. ✅ Keep `cmd/genhash` - add to README

### Short-Term (This Sprint)
1. 🔄 Integrate `hibp` package for password breach checking
2. 🔄 Integrate `secrets` package for hot-reload
3. 🔄 Evaluate `authservice` vs current auth implementation

### Medium-Term (Next Sprint)
1. 📋 Integrate `respbody` for DLP compliance
2. 📋 Integrate `apikeys` for service auth
3. 📋 Integrate `ipallow` for admin restriction

### Long-Term (Backlog)
1. 📝 Decide on consolidating cmd/obsidian and cmd/sentinel-api
2. 📝 Clean up docs/ duplicate files
3. 📝 Fix or remove examples/http-server

---

## Appendix: File-by-File Status

| Path | Lines | Status | Action |
|------|-------|--------|--------|
| cmd/obsidian/main.go | 1716 | ✅ Main app | Keep |
| cmd/sentinel-api/main.go | 1181 | ⚠️ Duplicate | Evaluate |
| cmd/genhash/main.go | 26 | ✅ Utility | Document |
| internal/app/apikeys/apikeys.go | 354 | ❌ Unused | Integrate |
| internal/app/authservice/service.go | 473 | ❌ Orphaned | Evaluate |
| internal/app/graphql/graphql.go | 377 | ❌ Unused | Optional |
| internal/app/hibp/hibp.go | 307 | ❌ Unused | **Integrate** |
| internal/app/ipallow/ipallow.go | 396 | ❌ Unused | Integrate |
| internal/app/persistence/postgres.go | 766 | ❌ Orphaned | Evaluate |
| internal/app/respbody/respbody.go | 457 | ❌ Unused | **Integrate** |
| internal/app/secrets/secrets.go | 313 | ❌ Unused | **Integrate** |
| internal/handler/api.go | 84 | ⚠️ sentinel-only | With sentinel |
| docs/js/app.js | 1283 | ⚠️ Duplicate | Review |
| examples/http-server/ | - | ⚠️ Outdated | Fix/Remove |
| experimental/ | - | ✅ Used | Keep |
| collection/ | - | ✅ Used | Keep |
| types/ | - | ✅ Used | Keep |
| debuglog/ | - | ✅ Used | Keep |
| http/ | - | ✅ Tests only | Keep |

---

*Report generated by automated code analysis. Manual review recommended before taking action.*
