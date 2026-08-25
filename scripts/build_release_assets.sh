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
if [[ -f "${OUT_DIR}/lesser-body-deploy.json" ]]; then
  mapfile -t EXISTING_ASSETS < <(bash "${ROOT_DIR}/scripts/list_release_assets.sh" "${OUT_DIR}")
else
  mapfile -t EXISTING_ASSETS < <(bash "${ROOT_DIR}/scripts/list_release_assets.sh")
fi
for asset in "${EXISTING_ASSETS[@]}"; do
  rm -f "${OUT_DIR}/${asset}"
done

ASSEMBLY_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${ASSEMBLY_DIR}"
}
trap cleanup EXIT

GIT_SHA="$(git rev-parse --verify HEAD)"
GO_VERSION="$(go env GOVERSION)"

MCP_PROTOCOL_VERSION="$(awk '
  /const protocolVersion =/ {
    gsub(/"/, "", $4)
    print $4
    exit
  }
' "$(go list -f '{{.Dir}}' github.com/theory-cloud/apptheory/v4/runtime/mcp)/server.go")"
if [[ -z "${MCP_PROTOCOL_VERSION}" ]]; then
  echo "failed to resolve MCP protocol version from github.com/theory-cloud/apptheory/v4/runtime/mcp" >&2
  exit 1
fi

bash scripts/build.sh
cp -f dist/lesser-body.zip "${OUT_DIR}/lesser-body.zip"
cp -f scripts/deploy-lesser-body-from-release.sh "${OUT_DIR}/deploy-lesser-body-from-release.sh"
chmod +x "${OUT_DIR}/deploy-lesser-body-from-release.sh"

(
  cd "${ROOT_DIR}/cdk"
  npm ci
  npm run build
)

for stage in dev staging live; do
  stage_outdir="${ASSEMBLY_DIR}/${stage}"
  (
    cd "${ROOT_DIR}/cdk"
    node dist/bin/release-template.js --version "${VERSION}" --stage "${stage}" --outdir "${stage_outdir}"
  )
done

python3 "${ROOT_DIR}/scripts/managed_release.py" build-from-assemblies \
  --version "${VERSION}" \
  --out-dir "${OUT_DIR}" \
  --assembly-dir "${ASSEMBLY_DIR}" \
  --git-sha "${GIT_SHA}" \
  --go-version "${GO_VERSION}" \
  --mcp-protocol-version "${MCP_PROTOCOL_VERSION}"

echo "Wrote release assets to ${OUT_DIR}"
