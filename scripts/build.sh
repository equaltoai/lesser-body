#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

cd "$ROOT_DIR"

mkdir -p dist
rm -f dist/lesser-body.zip dist/lesser-body-instance.zip dist/checksums.txt

BUILD_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$BUILD_DIR"
}
trap cleanup EXIT

build_lambda_zip() {
  local package_path="$1"
  local zip_name="$2"
  local package_build_dir="$BUILD_DIR/${zip_name%.zip}"

  mkdir -p "$package_build_dir"

  echo "Building ${zip_name} bootstrap (linux/arm64)..."
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$package_build_dir/bootstrap" "$package_path"
  chmod +x "$package_build_dir/bootstrap"
  touch -t 198001010000 "$package_build_dir/bootstrap"

  echo "Packaging dist/${zip_name}..."
  (cd "$package_build_dir" && zip -X -q "$ROOT_DIR/dist/${zip_name}" bootstrap)

  echo "OK: dist/${zip_name}"
}

write_checksums() {
  echo "Writing dist/checksums.txt..."
  sha256sum dist/lesser-body.zip dist/lesser-body-instance.zip > dist/checksums.txt
  sha256sum -c dist/checksums.txt
  echo "OK: dist/checksums.txt"
}

build_lambda_zip ./cmd/lesser-body lesser-body.zip
build_lambda_zip ./cmd/lesser-body-instance lesser-body-instance.zip
write_checksums
