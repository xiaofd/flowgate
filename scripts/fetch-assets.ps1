# fetch-assets.ps1 —— 从源码最小化构建 sing-box 核心（仅 wireguard + clash_api 标签）
# 相比官方全功能发布版（81MB），最小化构建约 35MB，成品 flowgate 体积减半。
# 依赖：Go 1.24+（GOTOOLCHAIN=go1.25.5 会自动下载所需工具链）+ goproxy.cn
# 用法: powershell -ExecutionPolicy Bypass -File scripts\fetch-assets.ps1
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$Assets = Join-Path $Root "internal\assets\assets"
$Rules = Join-Path $Assets "rules"
New-Item -ItemType Directory -Force -Path $Rules | Out-Null

# Go 环境（使用 .tools 便携工具链，若存在）
$ToolsGo = "D:\aiwork\test\.tools\go\bin\go.exe"
if (Test-Path $ToolsGo) {
    $env:GOROOT = "D:\aiwork\test\.tools\go"
    $env:GOPATH = "D:\aiwork\test\.tools\gopath"
    $env:GOCACHE = "D:\aiwork\test\.tools\gocache"
    $env:PATH = "$env:GOROOT\bin;" + $env:PATH
}
$env:GOTOOLCHAIN = "go1.25.5"   # sing-box 1.14.1 要求 >=1.25.5（强制下载完整工具链）
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOSUMDB = "sum.golang.org"
$env:CGO_ENABLED = "0"

$SBVer = "1.14.1"
$Tags = "with_wireguard with_clash_api"
$LdFlags = "-s -w -X github.com/sagernet/sing-box/constant.Version=$SBVer-min"

Write-Host "==> 源码构建 sing-box $SBVer (Windows amd64, 最小标签) ..."
$env:GOOS = ""; $env:GOARCH = ""
go install -trimpath -ldflags $LdFlags -tags $Tags "github.com/sagernet/sing-box/cmd/sing-box@v$SBVer"
Copy-Item "$env:GOPATH\bin\sing-box.exe" "$Assets\fg-core-windows-amd64.exe" -Force

Write-Host "==> 源码构建 sing-box $SBVer (Linux amd64, 最小标签) ..."
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go install -trimpath -ldflags $LdFlags -tags $Tags "github.com/sagernet/sing-box/cmd/sing-box@v$SBVer"
Copy-Item "$env:GOPATH\bin\linux_amd64\sing-box" "$Assets\fg-core-linux-amd64" -Force

Write-Host "==> 源码构建 sing-box $SBVer (Linux arm64, 最小标签) ..."
$env:GOOS = "linux"; $env:GOARCH = "arm64"
go install -trimpath -ldflags $LdFlags -tags $Tags "github.com/sagernet/sing-box/cmd/sing-box@v$SBVer"
Copy-Item "$env:GOPATH\bin\linux_arm64\sing-box" "$Assets\fg-core-linux-arm64" -Force
$env:GOOS = ""; $env:GOARCH = ""

Write-Host "==> 下载 wintun 0.14.1 ..."
$Tmp = "$env:TEMP\flowgate-assets"
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile "$Tmp\wintun.zip" -UseBasicParsing
Expand-Archive "$Tmp\wintun.zip" "$Tmp\wt" -Force
Copy-Item "$Tmp\wt\wintun\bin\amd64\wintun.dll" "$Assets\wintun.dll" -Force

Write-Host "==> 下载分流规则集（GFW / 国内）..."
$rules = @{
  "geosite-gfw.srs" = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs"
  "geosite-cn.srs"  = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs"
  "geoip-cn.srs"    = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/cn.srs"
}
foreach ($k in $rules.Keys) {
  Invoke-WebRequest -Uri $rules[$k] -OutFile (Join-Path $Rules $k) -UseBasicParsing
}

Write-Host "==> 编译开发站点名单（scripts/dev-sites.json → geosite-dev.srs）..."
& "$Assets\fg-core-windows-amd64.exe" rule-set compile (Join-Path $Root "scripts\dev-sites.json")
Copy-Item (Join-Path $Root "scripts\dev-sites.srs") (Join-Path $Rules "geosite-dev.srs") -Force

Write-Host "==> 完成。资产目录: $Assets"
Get-ChildItem $Assets -Recurse -File | Select-Object Name, @{N='SizeMB';E={[math]::Round($_.Length/1MB,2)}} | Format-Table -AutoSize
