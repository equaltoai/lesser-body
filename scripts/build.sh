#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR"

mkdir -p dist
rm -f dist/lesser-body.zip

BUILD_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$BUILD_DIR"
}
trap cleanup EXIT

echo "Building Lambda bootstrap (linux/arm64)..."
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/bootstrap" ./cmd/lesser-body
chmod +x "$BUILD_DIR/bootstrap"

echo "Packaging dist/lesser-body.zip..."
(cd "$BUILD_DIR" && zip -q "$ROOT_DIR/dist/lesser-body.zip" bootstrap)

echo "OK: dist/lesser-body.zip"

