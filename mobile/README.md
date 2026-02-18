# Obsidian WAF Mobile (Capacitor)

This folder contains a Capacitor wrapper for the Obsidian WAF UI.

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

## Notes

- The web assets are loaded from `../cmd/obsidian/ui`.
- Backend APIs must be reachable from the device/emulator.
- For development, you can use `adb reverse` to forward localhost ports to the emulator.
