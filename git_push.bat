@echo off
chcp 65001 >nul
setlocal EnableDelayedExpansion

REM ============================================================
REM  imgConvert - auto git add / commit / push
REM  Usage: double-click this file
REM ============================================================

REM repo root = directory where this bat lives
set "REPO_DIR=%~dp0"
if "%REPO_DIR:~-1%"=="\" set "REPO_DIR=%REPO_DIR:~0,-1%"

cd /d "%REPO_DIR%"

REM ---------- check git ----------
where git >nul 2>nul
if errorlevel 1 goto :no_git

REM ---------- check inside a repo ----------
git rev-parse --is-inside-work-tree >nul 2>nul
if errorlevel 1 goto :not_repo

REM ---------- stage all ----------
git add -A

REM ---------- detect changes ----------
for /f %%i in ('git status --porcelain ^| find /c /v ""') do set "CHANGES=%%i"
if "%CHANGES%"=="0" (
    echo [INFO] No changes to commit - pushing if needed.
    goto :push
)

REM ---------- commit ----------
set "MSG=chore: auto commit %date% %time%"
git commit -m "%MSG%"
if errorlevel 1 goto :commit_fail

:push
git push
if errorlevel 1 goto :push_fail

echo.
echo [OK] Committed and pushed to remote.
echo.
goto :end

:no_git
echo [ERROR] git not found. Install Git from https://git-scm.com then retry.
pause
exit /b 1

:not_repo
echo [ERROR] Current folder is not a git repository.
pause
exit /b 1

:commit_fail
echo [ERROR] git commit failed - check the messages above.
pause
exit /b 1

:push_fail
echo [ERROR] git push failed - check network or auth, then retry.
pause
exit /b 1

:end
pause
