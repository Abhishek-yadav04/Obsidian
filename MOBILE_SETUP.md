# Mobile Debugging Setup for Obsidian WAF

## 🔍 Root Cause Analysis

Your error "Server returned an invalid error" means the mobile app **cannot reach the backend at all**.

This is a **network routing problem**, NOT a CORS problem.

---

## ✅ Correct Diagnosis Steps

### Step 1: Verify Backend is Reachable

From your **laptop browser**, open:
```
http://192.168.0.104:8082/api/health
```

**Expected:** JSON response `{"status": "healthy"}`

**If it fails:**
- Backend is not binding to `0.0.0.0:8082`
- Windows Firewall is blocking port 8082
- Backend is not running

### Step 2: Test from Phone Browser

From your **phone browser** (Chrome/Safari), open:
```
http://192.168.0.104:8082/api/health
```

**Expected:** Same JSON response

**If it fails:**
- Phone and laptop are on different WiFi networks
- Windows Firewall is blocking external access
- Backend bound to `127.0.0.1` instead of `0.0.0.0`

### Step 3: Check Backend Binding

When you run `go run ./cmd/obsidian`, the server MUST bind to:
```
0.0.0.0:8082
```

NOT:
```
127.0.0.1:8082
localhost:8082
```

The Go code already does this correctly:
```go
Addr: fmt.Sprintf(":%d", *port)  // Binds to 0.0.0.0:8082
```

---

## 🛠️ Solutions (Choose Based on Your Setup)

### Option A: USB Debugging with ADB Reverse (Development)

**When to use:** USB connected, Android Studio debugging

```bash
# Connect device
adb devices

# Forward port
adb reverse tcp:8082 tcp:8082

# Verify
adb reverse --list
```

Now mobile app can use `http://127.0.0.1:8082`

**Limitations:**
- Requires USB cable
- Only works during active debugging
- Not for production testing

---

### Option B: Wireless Debugging (Recommended)

**When to use:** Testing over WiFi, no USB cable

#### 1. Ensure Backend Binds to All Interfaces

The code already does this. Verify by checking startup logs:
```
Server: http://localhost:8082
```

This actually binds to `0.0.0.0:8082` (all interfaces).

#### 2. Open Windows Firewall

**Windows Defender Firewall:**
1. Open "Windows Defender Firewall with Advanced Security"
2. Click "Inbound Rules" → "New Rule"
3. Rule Type: **Port**
4. Protocol: **TCP**, Port: **8082**
5. Action: **Allow the connection**
6. Profile: Check **Private** (your home WiFi)
7. Name: **Obsidian WAF**

Or via PowerShell (Run as Administrator):
```powershell
New-NetFirewallRule -DisplayName "Obsidian WAF" -Direction Inbound -Protocol TCP -LocalPort 8082 -Action Allow -Profile Private
```

#### 3. Configure Mobile App

In your mobile app, before login, set:
```javascript
localStorage.setItem('obsidian_api_base', 'http://192.168.0.104:8082');
```

Or add to your app's config:
```javascript
const API_BASE = 'http://192.168.0.104:8082';
```

#### 4. Update CORS (Only if Needed)

If you get CORS errors AFTER the connection works, add to `.env`:
```bash
OBSIDIAN_CORS_ORIGINS=http://192.168.0.104:8082,capacitor://localhost
```

Restart backend:
```bash
go run ./cmd/obsidian
```

---

## 🔍 Troubleshooting

### Error: "Connection refused" or "Network error"

**Cause:** Backend not reachable

**Fix:**
1. Verify backend is running: `go run ./cmd/obsidian`
2. Check firewall allows port 8082
3. Ensure phone and laptop on same WiFi
4. Test from laptop browser: `http://192.168.0.104:8082/api/health`

### Error: "CORS policy blocked"

**Cause:** Request reached backend but browser blocked response

**Fix:**
1. Add your IP to `OBSIDIAN_CORS_ORIGINS` in `.env`
2. Restart backend

### Error: "Server returned an invalid error"

**Cause:** Backend returned HTML error page instead of JSON

**Fix:**
1. Check backend logs for errors
2. Verify endpoint exists: `curl http://192.168.0.104:8082/api/health`
3. Ensure backend is not returning 404 HTML page

### Error: "Timeout"

**Cause:** Network routing issue

**Fix:**
1. Verify laptop IP hasn't changed: `ipconfig`
2. Check both devices on same WiFi network
3. Disable VPN on laptop if active
4. Try pinging laptop from phone (use network tools app)

---

## 📋 Quick Checklist

- [ ] Backend running: `go run ./cmd/obsidian`
- [ ] Laptop browser works: `http://192.168.0.104:8082/api/health`
- [ ] Phone browser works: `http://192.168.0.104:8082/api/health`
- [ ] Windows Firewall allows port 8082 (Private network)
- [ ] Both devices on same WiFi network
- [ ] Mobile app configured with correct IP: `192.168.0.104:8082`
- [ ] CORS configured if needed (only after connection works)

---

## 🎯 What CORS Actually Does

CORS middleware is **only relevant** when:

✅ Request reaches backend successfully
✅ Backend processes request
✅ Browser blocks response due to origin mismatch

CORS does **NOT fix**:

❌ Connection refused
❌ Network unreachable
❌ Timeout
❌ DNS resolution
❌ Firewall blocking

---

## 🔧 Backend Configuration

The backend already:
- ✅ Binds to `0.0.0.0:8082` (all interfaces)
- ✅ Has CORS middleware for cross-origin requests
- ✅ Returns proper JSON responses
- ✅ Supports configurable CORS origins via env var

No backend code changes needed for wireless debugging.

---

## 📱 Mobile App Configuration

The frontend already:
- ✅ Supports configurable API base URL
- ✅ Auto-detects Capacitor runtime
- ✅ Defaults to `http://127.0.0.1:8082` for ADB reverse
- ✅ Can be overridden via localStorage

To use wireless debugging, just set:
```javascript
localStorage.setItem('obsidian_api_base', 'http://192.168.0.104:8082');
```

Before calling any API.

---

## 🚀 Production Deployment

For production:

1. Use proper domain name (not IP)
2. Enable HTTPS with valid certificate
3. Restrict CORS to specific origins
4. Use environment-specific config
5. Never hardcode IPs in mobile app
