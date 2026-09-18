#!/usr/bin/env bash
# build.sh —— 构建 Windows 与 Linux 单文件产物
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/build"
mkdir -p "$OUT"

export CGO_ENABLED=0

echo "==> 构建 flowgate (linux/amd64) ..."
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$OUT/flowgate-linux-amd64" "$ROOT"

echo "==> 构建 flowgate (linux/arm64) ..."
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "$OUT/flowgate-linux-arm64" "$ROOT"

echo "==> 构建 flowgate (windows/amd64) ..."
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$OUT/flowgate-windows-amd64.exe" "$ROOT"

echo "==> 完成。产物目录: $OUT"
ls -lh "$OUT"
