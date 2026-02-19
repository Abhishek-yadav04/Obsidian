# Obsidian WAF Mobile (Capacitor)

This folder contains a Capacitor wrapper for the Obsidian WAF UI.

## ✅ CRITICAL FIX APPLIED

**MainActivity.java now loads backend server directly:**
```java
getBridge().setServerUrl("http://127.0.0.1:8082");
```

This fixes the "Server returned an invalid response" error by loading the backend server instead of local HTML files.

## Rebuild and Test

**1. Sync Capacitor:**
```bash
cd mobile
npm run cap:sync
```

**2. Rebuild in Android Studio:**
- Build > Clean Project
- Build > Rebuild Project
- Run > Run 'app'

**3. Verify in Edge DevTools:**
```
edge://inspect
```

You should now see:
- WebView origin: `http://127.0.0.1:8082/`
- NOT `http://localhost/login.html`

**4. Test login:**
- App should load from backend server
- Login should work successfully
- Check Network tab shows `/api/login` with status 200

## 🔍 Debugging "Server returned an invalid response"

This error means the backend responds, but the app cannot parse it as JSON.

### Critical Fix: Enable Cleartext HTTP

**AndroidManifest.xml already updated with:**
```xml
android:usesCleartextTraffic="true"
```

This allows HTTP connections in Android WebView (required for development).

### Step 1: Inspect WebView Console (MOST IMPORTANT)

**On laptop (Chrome or Edge):**

**For Edge:**
```
edge://inspect
```

**For Chrome:**
```
chrome://inspect
```

1. Open your device's WebView
2. Go to **Network** tab
3. Attempt login in the app
4. Check the `/api/login` request:
   - **Request URL** (should be `http://127.0.0.1:8082/api/login`)
   - **Status Code** (should be 200)
   - **Response** tab (should be JSON, not HTML)

### Step 2: Verify API Base URL

In `login.html`, temporarily add at the top of the script:

```javascript
console.log('Fetch URL:', '/api/login');
console.log('Window location:', window.location.href);
```

**Expected window location:** `capacitor://localhost` or `http://localhost`

**Expected fetch URL:** Relative `/api/login` (resolves to backend)

### Step 3: Add Response Debug Logging

In `login.html`, replace the fetch block with:

```javascript
const response = await fetch('/api/login', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username, password })
});

console.log('Response status:', response.status);
console.log('Response content-type:', response.headers.get('content-type'));

const text = await response.text();
console.log('Response body (first 200 chars):', text.substring(0, 200));

let data;
try {
  data = JSON.parse(text);
} catch (parseErr) {
  console.error('JSON parse failed:', parseErr);
  showError('Server returned invalid response: ' + text.substring(0, 50));
  resetButton();
  return;
}
```

This will show exactly what the backend returns.

### Common Causes

1. **HTTPS vs HTTP mismatch**
   - App calls `https://127.0.0.1:8082` but backend serves HTTP
   - Fix: Ensure all URLs use `http://`

2. **Backend returns HTML instead of JSON**
   - Status 401/404 returns HTML error page
   - Fix: Backend must return JSON for all API endpoints

3. **Cleartext HTTP blocked** ✅ FIXED
   - AndroidManifest missing `usesCleartextTraffic="true"`
   - Already fixed in this commit

4. **Wrong base URL**
   - App uses `http://192.168.0.104:8082` without `adb reverse`
   - Fix: Use `http://127.0.0.1:8082` with `adb reverse`

## Rebuild After Changes

**After updating AndroidManifest.xml or any code:**

```bash
# From mobile/ directory
cd mobile
npm run cap:sync
```

**Then in Android Studio:**
1. Build > Clean Project
2. Build > Rebuild Project
3. Run > Run 'app'

**Or use terminal:**
```bash
# From mobile/android directory
cd android
.\gradlew clean assembleDebug
```

## Verify Fix with Edge DevTools

1. Open Edge: `edge://inspect`
2. Find your device under "Remote Target"
3. Click "inspect" on the WebView
4. Go to **Network** tab
5. Attempt login in app
6. Check `/api/login` request:
   - Status should be **200**
   - Response should be **JSON with token**
   - NOT HTML error page

If you get "Server returned an invalid response" when running via Android Studio:

```bash
# Terminal 1: Start backend
cd ..
go run ./cmd/obsidian

# Terminal 2: Setup ADB reverse
adb reverse tcp:8082 tcp:8082

# Verify
adb reverse --list
```

Now run the app from Android Studio. It will connect to `http://127.0.0.1:8082` which maps to your laptop backend.

## Local build (Windows/macOS/Linux)

1. Install Node.js (LTS) and Android Studio (with Android SDK + platform tools).
2. From `mobile/`:
   - `npm install`
   - `npm run cap:add`
   - `npm run cap:sync`
3. Open the Android project:
   - `npm run cap:open`
4. Build APK in Android Studio (Build > Build APK(s)).

## UI update workflow (important)

If you change files under `cmd/obsidian/ui`, run this before testing on Android:

- `npm run cap:sync`

This copies the latest web UI into `mobile/android/app/src/main/assets/public`.

## Development Setup

### Option A: USB Debugging (Recommended)

1. Connect device via USB
2. Enable USB debugging on device
3. Run: `adb reverse tcp:8082 tcp:8082`
4. App uses `http://127.0.0.1:8082` automatically

### Option B: Wireless Debugging

1. Ensure backend binds to `0.0.0.0:8082` (already correct)
2. Open Windows Firewall port 8082
3. Get laptop IP: `ipconfig` (e.g., 192.168.0.104)
4. In app, set before login:
   ```javascript
   localStorage.setItem('obsidian_api_base', 'http://192.168.0.104:8082');
   ```

## Troubleshooting

### "Server returned an invalid response"

**Cause:** App cannot reach backend

**Fix:**
```bash
# Check ADB connection
adb devices

# Setup port forwarding
adb reverse tcp:8082 tcp:8082

# Verify backend is running
curl http://127.0.0.1:8082/api/health
```

### "Connection refused"

**Cause:** Backend not running or ADB reverse not set

**Fix:**
1. Start backend: `go run ./cmd/obsidian`
2. Run: `adb reverse tcp:8082 tcp:8082`
3. Rebuild app in Android Studio

### "CORS error"

**Cause:** Using IP address without CORS config

**Fix:**
Add to `.env`:
```bash
OBSIDIAN_CORS_ORIGINS=http://192.168.0.104:8082,capacitor://localhost
```

## Notes

- The web assets are loaded from `../cmd/obsidian/ui`.
- Backend APIs must be reachable from the device/emulator.
- For development, use `adb reverse` to forward localhost ports.
- Capacitor config allows cleartext HTTP for development.
