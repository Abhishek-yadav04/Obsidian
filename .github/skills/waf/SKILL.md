# WAF-CRS Integration Skill

## Purpose
Integrate OWASP Core Rule Set (CRS) into a Coraza-backed Go WAF in a production-correct, externalized, and maintainable way.

## When To Use
- You need CRS as an external ruleset loaded at runtime (no vendoring).
- You want a clear separation between WAF engine configuration and rules.
- You are preparing for AppSec/SOC review and need defensible CRS behavior.

## What It Does
- Adds configurable CRS loading (path, enable flag, mode).
- Preserves safe defaults (DetectionOnly by default).
- Supports fail-open vs fail-closed startup behavior.
- Keeps custom rules separate from CRS.

## Keywords
coraza, waf, owasp crs, modsecurity, ruleset, detectiononly, appsec, soc

## Example
Enable CRS from disk:

```
OBSIDIAN_CRS_ENABLED=true
OBSIDIAN_CRS_PATH=/opt/owasp-crs
OBSIDIAN_CRS_MODE=DetectionOnly
```
