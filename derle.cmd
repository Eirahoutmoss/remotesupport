@echo off
cd /d "%~dp0"
echo Derleme basladi: %DATE% %TIME% > derle.log
where go >> derle.log 2>&1
go version >> derle.log 2>&1
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" >> derle.log 2>&1
echo CIKIS KODU: %ERRORLEVEL% >> derle.log
