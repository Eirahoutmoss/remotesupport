@echo off
cd /d "%~dp0"
echo Tani calisiyor, 1-2 dakika surebilir...
go run ./cmd/nd-check > tani.log 2>&1
echo CIKIS KODU: %ERRORLEVEL% >> tani.log
