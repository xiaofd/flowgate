# build.ps1 —— 构建 Windows 与 Linux 单文件产物
# 用法: powershell -ExecutionPolicy Bypass -File scripts\build.ps1
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$Out = Join-Path $Root "build"
New-Item -ItemType Directory -Force -Path $Out | Out-Null

$env:CGO_ENABLED = "0"

Write-Host "==> 构建 flowgate (windows/amd64) ..."
$env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o "$Out\flowgate-windows-amd64.exe" "$Root"

Write-Host "==> 构建 flowgate (linux/amd64) ..."
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o "$Out\flowgate-linux-amd64" "$Root"

Write-Host "==> 构建 flowgate (linux/arm64) ..."
$env:GOOS = "linux"; $env:GOARCH = "arm64"
go build -trimpath -ldflags "-s -w" -o "$Out\flowgate-linux-arm64" "$Root"

$env:GOOS = ""; $env:GOARCH = ""
Write-Host "==> 完成。产物目录: $Out"
Get-ChildItem $Out | Select-Object Name, @{N='SizeMB';E={[math]::Round($_.Length/1MB,1)}} | Format-Table -AutoSize
