$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Dist = Join-Path $Root 'dist'
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go bulunamadı. Go kurulumu sonrası PowerShell yeniden açılmalı.'
}

Push-Location $Root
try {
    Write-Host 'Building RemoteSupport.exe ...'
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    go build -trimpath -ldflags='-s -w' -o (Join-Path $Dist 'RemoteSupport.exe') './cmd/remotesupport'
    if ($LASTEXITCODE -ne 0) { throw 'RemoteSupport.exe build failed.' }

    Write-Host 'Signaling embedded in RemoteSupport.exe.'

    Write-Host ''
    Write-Host 'BUILD OK'
    Write-Host (Join-Path $Dist 'RemoteSupport.exe')
}
finally { Pop-Location }
