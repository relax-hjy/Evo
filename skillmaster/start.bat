@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo Starting SkillMaster server...
start "SkillMaster" /min cmd /c "timeout /t 2 /nobreak >nul & start http://127.0.0.1:8765"
py -m uvicorn server:app --host 127.0.0.1 --port 8765
