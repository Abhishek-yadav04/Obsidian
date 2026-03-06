# Obsidian WAF - Android Development Launch Script
# Run this AFTER starting your Android Emulator in Android Studio

Write-Host ""
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "  OBSIDIAN WAF - Android Launch Setup    " -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host ""

$rootDir = Split-Path -Parent $PSScriptRoot
$obsidianExe = Join-Path $rootDir "cmd\obsidian\obsidian.exe"
$adb = "C:\Users\admin\AppData\Local\Android\Sdk\platform-tools\adb.exe"
$envFile = Join-Path $rootDir ".env"

# Step 1: Load .env
if (Test-Path $envFile) {
    Write-Host "[1/3] Loading environment variables from .env..." -ForegroundColor Yellow
    Get-Content $envFile | ForEach-Object {
        if ($_ -match '^\s*([^#][^=]+)=(.*)$') {
            [Environment]::SetEnvironmentVariable($matches[1].Trim(), $matches[2].Trim(), "Process")
        }
    }
    Write-Host "      Done." -ForegroundColor Green
} else {
    Write-Host "[1/3] No .env file found at: $envFile" -ForegroundColor Red
    Write-Host "      Set OBSIDIAN_JWT_SECRET manually or create .env" -ForegroundColor Red
}

# Step 2: Set up ADB reverse (so emulator can reach localhost:8082)
Write-Host ""
Write-Host "[2/3] Setting up ADB port reverse (emulator -> host:8082)..." -ForegroundColor Yellow
if (Test-Path $adb) {
    $result = & $adb reverse tcp:8082 tcp:8082 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Host "      adb reverse tcp:8082 tcp:8082 - OK" -ForegroundColor Green
    } else {
        Write-Host "      ADB reverse failed: $result" -ForegroundColor Red
        Write-Host "      Make sure Android Emulator is running first!" -ForegroundColor Red
        Write-Host "      You can run manually: adb reverse tcp:8082 tcp:8082" -ForegroundColor Yellow
    }
} else {
    Write-Host "      ADB not found at: $adb" -ForegroundColor Red
    Write-Host "      Run manually: adb reverse tcp:8082 tcp:8082" -ForegroundColor Yellow
}

# Step 3: Start WAF server
Write-Host ""
Write-Host "[3/3] Starting Obsidian WAF server on port 8082..." -ForegroundColor Yellow
Write-Host ""
Write-Host "  Dashboard URL : http://localhost:8082" -ForegroundColor Cyan
Write-Host "  Android app   : Loads from http://127.0.0.1:8082 (via ADB reverse)" -ForegroundColor Cyan
Write-Host "  Login         : admin / admin123 (dev mode)" -ForegroundColor White
Write-Host ""
Write-Host "  Press Ctrl+C to stop the server." -ForegroundColor Gray
Write-Host ""

if (Test-Path $obsidianExe) {
    & $obsidianExe -dev -port 8082
} else {
    Write-Host "Binary not found at: $obsidianExe" -ForegroundColor Red
    Write-Host "Build it first: go build -o cmd/obsidian/obsidian.exe ./cmd/obsidian" -ForegroundColor Yellow
}
