$ErrorActionPreference = 'Stop'

go get github.com/go-mswin/screencapture@main

go mod tidy

gofmt -w .\cmd\remotesupport\main.go .\client\capture\capture_windows.go .\client\screen\transport.go

$env:CGO_ENABLED="0"
$env:GOOS="windows"
$env:GOARCH="amd64"

go build -a -ldflags="-H=windowsgui" -o .\RemoteSupport.exe .\cmd\remotesupport
