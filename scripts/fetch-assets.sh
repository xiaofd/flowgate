#!/usr/bin/env bash
# fetch-assets.sh —— 从源码最小化构建 sing-box 核心（仅 wireguard + clash_api 标签）
# 相比官方全功能发布版（81MB），最小化构建约 35MB，成品 flowgate 体积减半。
# 依赖：Go 1.24+（GOTOOLCHAIN=go1.25.5 会自动下载所需工具链）+ goproxy.cn
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ASSETS="$ROOT/internal/assets/assets"
RULES="$ASSETS/rules"
mkdir -p "$RULES"

export GOTOOLCHAIN=go1.25.5   # sing-box 1.14.1 要求 >=1.25.5
export GOPROXY="https://goproxy.cn,direct"
export GOSUMDB="sum.golang.org"
export CGO_ENABLED=0

SB_VER="1.14.1"
TAGS="with_wireguard with_clash_api"
LDFLAGS="-s -w -X github.com/sagernet/sing-box/constant.Version=${SB_VER}-min"

echo "==> 源码构建 sing-box $SB_VER (linux amd64, 最小标签) ..."
GOOS=linux GOARCH=amd64 go install -trimpath -ldflags "$LDFLAGS" -tags "$TAGS" "github.com/sagernet/sing-box/cmd/sing-box@v${SB_VER}"
cp "$(go env GOPATH)/bin/linux_amd64/sing-box" "$ASSETS/fg-core-linux-amd64"

echo "==> 源码构建 sing-box $SB_VER (linux arm64, 最小标签) ..."
GOOS=linux GOARCH=arm64 go install -trimpath -ldflags "$LDFLAGS" -tags "$TAGS" "github.com/sagernet/sing-box/cmd/sing-box@v${SB_VER}"
cp "$(go env GOPATH)/bin/linux_arm64/sing-box" "$ASSETS/fg-core-linux-arm64"

echo "==> 源码构建 sing-box $SB_VER (windows amd64, 最小标签) ..."
GOOS=windows GOARCH=amd64 go install -trimpath -ldflags "$LDFLAGS" -tags "$TAGS" "github.com/sagernet/sing-box/cmd/sing-box@v${SB_VER}"
cp "$(go env GOPATH)/bin/windows_amd64/sing-box.exe" "$ASSETS/fg-core-windows-amd64.exe"

echo "==> 下载 wintun 0.14.1 ..."
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fL "https://www.wintun.net/builds/wintun-0.14.1.zip" -o "$TMP/wintun.zip"
unzip -o "$TMP/wintun.zip" -d "$TMP/wt"
cp "$TMP/wt/wintun/bin/amd64/wintun.dll" "$ASSETS/wintun.dll"

echo "==> 下载分流规则集（GFW / 国内）..."
curl -fL "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs" -o "$RULES/geosite-gfw.srs"
curl -fL "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs" -o "$RULES/geosite-cn.srs"
curl -fL "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/cn.srs" -o "$RULES/geoip-cn.srs"

echo "==> 编译开发站点名单（scripts/dev-sites.json → geosite-dev.srs）..."
"$ASSETS/fg-core-linux-amd64" rule-set compile "$ROOT/scripts/dev-sites.json"
cp "$ROOT/scripts/dev-sites.srs" "$RULES/geosite-dev.srs"

echo "==> 完成。资产目录: $ASSETS"
ls -lh "$ASSETS" "$RULES"
