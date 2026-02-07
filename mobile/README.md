# Obsidian WAF Mobile (Capacitor)

This folder contains a Capacitor wrapper for the Obsidian WAF UI.

## Local build (Windows/macOS/Linux)

1. Install Node.js (LTS) and Android Studio (with Android SDK + platform tools).
2. From `mobile/`:
   - `npm install`
   - `npx cap add android`
   - `npx cap sync android`
3. Open the Android project:
   - `npx cap open android`
4. Build APK in Android Studio (Build > Build APK(s)).

## Notes

- The web assets are loaded from `../cmd/obsidian/ui`.
- Backend APIs must be reachable from the device/emulator.
- For development, you can use `adb reverse` to forward localhost ports to the emulator.