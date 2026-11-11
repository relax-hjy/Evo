@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo Starting EVO server (evo.exe) at http://127.0.0.1:8765
start "EVO" /min cmd /c "timeout /t 2 /nobreak >nul & start http://127.0.0.1:8765"
evo.exe
