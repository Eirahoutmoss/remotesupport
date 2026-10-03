$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Dist = Join-Path $Root 'dist'
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go bulunamadı. Go kurulumu sonrası PowerShell yeniden açılmalı.'
}

Push-Location $Root
try {
    Write-Host 'Building NexDesk.exe ...'
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    # Exe ikonu + manifest: assets\nexdesk.ico / nexdesk.manifest -> rsrc_windows_amd64.syso
    # (go build bunu otomatik gömer). Manifest DPI (gdiScaling) için gerekli.
    $Ico = Join-Path $Root 'cmd\remotesupport\assets\nexdesk.ico'
    $Manifest = Join-Path $Root 'cmd\remotesupport\assets\nexdesk.manifest'
    $Syso = Join-Path $Root 'cmd\remotesupport\rsrc_windows_amd64.syso'
    $NeedSyso = -not (Test-Path $Syso) -or `
        (Get-Item $Ico).LastWriteTime -gt (Get-Item $Syso).LastWriteTime -or `
        (Get-Item $Manifest).LastWriteTime -gt (Get-Item $Syso).LastWriteTime
    if ($NeedSyso) {
        Write-Host 'Generating icon + manifest resource ...'
        go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico $Ico -manifest $Manifest -o $Syso
        if ($LASTEXITCODE -ne 0) { throw 'Icon/manifest resource generation failed.' }
    }
    # -H=windowsgui: GUI alt-sistemi olarak derle; açılışta konsol (DOS) penceresi çıkmasın.
    go build -trimpath -ldflags='-s -w -H=windowsgui' -o (Join-Path $Dist 'NexDesk.exe') './cmd/remotesupport'
    if ($LASTEXITCODE -ne 0) { throw 'NexDesk.exe build failed.' }

    Write-Host 'Signaling embedded in NexDesk.exe.'

    Write-Host ''
    Write-Host 'BUILD OK'
    Write-Host (Join-Path $Dist 'NexDesk.exe')
}
finally { Pop-Location }
