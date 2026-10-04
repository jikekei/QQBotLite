@echo off
chcp 65001 >nul
cd /d "%~dp0"
QQBotLite.exe --check
pause
