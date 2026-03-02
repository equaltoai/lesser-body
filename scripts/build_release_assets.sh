#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <version> [out-dir]" >&2
  exit 1
fi

VERSION="$1"
OUT_DIR="${2:-dist/release}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

if [[ -z "${VERSION}" ]]; then
  echo "version is required" >&2
  exit 1
fi
if [[ "${VERSION}" != v* ]]; then
  echo "version must start with 'v' (for example: v1.0.0)" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"

GIT_SHA="$(git rev-parse --verify HEAD)"
GO_VERSION="$(go env GOVERSION)"

MCP_PROTOCOL_VERSION="$(awk '
  /const protocolVersion =/ {
    gsub(/"/, "", $4)
    print $4
    exit
  }
' "$(go list -f '{{.Dir}}' github.com/theory-cloud/apptheory/runtime/mcp)/server.go")"
if [[ -z "${MCP_PROTOCOL_VERSION}" ]]; then
  echo "failed to resolve MCP protocol version from github.com/theory-cloud/apptheory/runtime/mcp" >&2
  exit 1
fi

bash scripts/build.sh
cp -f dist/lesser-body.zip "${OUT_DIR}/lesser-body.zip"

(
  cd "${OUT_DIR}"
  sha256sum lesser-body.zip > checksums.txt
)

cat > "${OUT_DIR}/lesser-body-release.json" <<JSON
{
  "schema": 1,
  "name": "lesser-body",
  "version": "${VERSION}",
  "git_sha": "${GIT_SHA}",
  "go_version": "${GO_VERSION}",
  "mcp": {
    "protocol_version": "${MCP_PROTOCOL_VERSION}"
  }
}
JSON

echo "Wrote release assets to ${OUT_DIR}"
