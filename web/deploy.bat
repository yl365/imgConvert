@echo off
chcp 65001 >nul
setlocal EnableDelayedExpansion

REM ============================================================
REM  imgConvert static site -> Cloudflare Pages one-click deploy
REM  Usage: double-click this file, or run  deploy.bat
REM ============================================================

REM ---------- configurable ----------
REM Cloudflare Pages project name (auto-created if not exist)
set "PROJECT_NAME=imgconvert"

REM Deploy dir = directory where this bat lives (the web folder)
set "DIST_DIR=%~dp0"
REM strip trailing backslash to avoid wrangler parse issues
if "%DIST_DIR:~-1%"=="\" set "DIST_DIR=%DIST_DIR:~0,-1%"

REM ---------- check npx / Node.js ----------
where npx >nul 2>nul
if errorlevel 1 goto :no_node

REM ---------- auth handling ----------
REM Option A (recommended): already logged in via  npx wrangler login
REM Option B: set env vars for API Token deploy (CI / headless)
REM   set CLOUDFLARE_ACCOUNT_ID=your_account_id
REM   set CLOUDFLARE_API_TOKEN=your_api_token
REM   token needs Pages Edit permission under Account - Cloudflare Pages
if not defined CLOUDFLARE_API_TOKEN (
    echo.
    echo [INFO] CLOUDFLARE_API_TOKEN not set - will use local logged-in
    echo        Wrangler identity via npx wrangler login.
    echo.
)

REM ---------- deploy ----------
echo ============================================================
echo   Project : %PROJECT_NAME%
echo   Dir     : %DIST_DIR%
echo ============================================================
echo.

npx wrangler pages deploy "%DIST_DIR%" --project-name=%PROJECT_NAME% --commit-dirty=true
set "DEPLOY_ERROR=%errorlevel%"

if not %DEPLOY_ERROR%==0 goto :deploy_fail

echo.
echo [OK] Deployed! Check the Cloudflare Pages dashboard for the URL.
echo.
goto :end

:no_node
echo [ERROR] Node.js / npx not found. Install from https://nodejs.org
echo         then reopen this window and retry.
pause
exit /b 1

:deploy_fail
echo.
echo [FAILED] deploy error code %DEPLOY_ERROR%.
echo   Common causes:
echo     1. Not logged in - run  npx wrangler login  first
echo     2. Project name conflict or insufficient perms - check token
echo     3. Network issue - retry once
echo.
echo   For API Token deploy, set CLOUDFLARE_ACCOUNT_ID and
echo   CLOUDFLARE_API_TOKEN as environment variables before running.
pause
exit /b %DEPLOY_ERROR%

:end
pause
