#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <version> [release-dir]" >&2
  exit 1
fi

VERSION="$1"
SOURCE_DIR="${2:-dist/release-test}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ ! -d "${SOURCE_DIR}" ]]; then
  echo "release directory does not exist: ${SOURCE_DIR}" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

refresh_release_metadata() {
  local release_dir="$1"
  python3 - "${release_dir}" <<'PY'
import hashlib
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])

def digest(path: pathlib.Path) -> tuple[str, int]:
    data = path.read_bytes()
    return hashlib.sha256(data).hexdigest(), path.stat().st_size

deploy_path = root / "lesser-body-deploy.json"
release_path = root / "lesser-body-release.json"
deploy = json.loads(deploy_path.read_text())
release = json.loads(release_path.read_text())

template_files = {
    "dev": "lesser-body-managed-dev.template.json",
    "staging": "lesser-body-managed-staging.template.json",
    "live": "lesser-body-managed-live.template.json",
}

for stage, filename in template_files.items():
    sha, size = digest(root / filename)
    deploy["templates"][stage]["sha256"] = sha
    deploy["templates"][stage]["bytes"] = size
    release["artifacts"]["deploy_templates"][stage]["sha256"] = sha
    release["artifacts"]["deploy_templates"][stage]["bytes"] = size

deploy_path.write_text(json.dumps(deploy, indent=2) + "\n")
deploy_sha, deploy_size = digest(deploy_path)

for artifact_key, filename in {
    "lambda_zip": "lesser-body.zip",
    "deploy_script": "deploy-lesser-body-from-release.sh",
}.items():
    sha, size = digest(root / filename)
    release["artifacts"][artifact_key]["sha256"] = sha
    release["artifacts"][artifact_key]["bytes"] = size

release["artifacts"]["deploy_manifest"]["sha256"] = deploy_sha
release["artifacts"]["deploy_manifest"]["bytes"] = deploy_size
release_path.write_text(json.dumps(release, indent=2) + "\n")

checksummed_assets = [
    "lesser-body.zip",
    "lesser-body-deploy.json",
    "lesser-body-managed-dev.template.json",
    "lesser-body-managed-staging.template.json",
    "lesser-body-managed-live.template.json",
    "deploy-lesser-body-from-release.sh",
    "lesser-body-release.json",
]

lines = []
for asset in checksummed_assets:
    sha, _ = digest(root / asset)
    lines.append(f"{sha}  {asset}\n")

(root / "checksums.txt").write_text("".join(lines))
PY
}

LOGICAL_ID_DIR="${TMP_DIR}/release-bad-stream-logical-id"
cp -R "${SOURCE_DIR}" "${LOGICAL_ID_DIR}"

python3 - "${LOGICAL_ID_DIR}/lesser-body-managed-dev.template.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
template = json.loads(path.read_text())
resources = template["Resources"]
resources["BrokenStreamTable12345678"] = resources.pop("McpServerStreamTableC6A2DC7E")
resources["McpStreamTableParam604E9EFA"]["Properties"]["Value"] = {"Ref": "BrokenStreamTable12345678"}
path.write_text(json.dumps(template, indent=2) + "\n")
PY

refresh_release_metadata "${LOGICAL_ID_DIR}"

LOGICAL_ID_ERR="${TMP_DIR}/verify-logical-id.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${LOGICAL_ID_DIR}" > /dev/null 2> "${LOGICAL_ID_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted a managed template with a drifted stream table logical ID" >&2
  exit 1
fi

if ! grep -Fq 'lesser-body-managed-dev.template.json: missing expected resource McpServerStreamTableC6A2DC7E' "${LOGICAL_ID_ERR}"; then
  echo "verify_release_assets.sh did not report the expected stream-table logical-ID regression" >&2
  cat "${LOGICAL_ID_ERR}" >&2
  exit 1
fi

TABLE_NAME_DIR="${TMP_DIR}/release-bad-stream-table-name"
cp -R "${SOURCE_DIR}" "${TABLE_NAME_DIR}"

python3 - "${TABLE_NAME_DIR}/lesser-body-managed-dev.template.json" <<'PY'
import json
import pathlib
import sys

def rewrite(node):
    if isinstance(node, str):
        return node.replace("mcp-streams-v2", "mcp-streams-v3")
    if isinstance(node, list):
        return [rewrite(item) for item in node]
    if isinstance(node, dict):
        return {key: rewrite(value) for key, value in node.items()}
    return node

path = pathlib.Path(sys.argv[1])
template = json.loads(path.read_text())
table = template["Resources"]["McpServerStreamTableC6A2DC7E"]
table["Properties"]["TableName"] = rewrite(table["Properties"]["TableName"])
path.write_text(json.dumps(template, indent=2) + "\n")
PY

refresh_release_metadata "${TABLE_NAME_DIR}"

TABLE_NAME_ERR="${TMP_DIR}/verify-table-name.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${TABLE_NAME_DIR}" > /dev/null 2> "${TABLE_NAME_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted a managed template with a drifted stream table name baseline" >&2
  exit 1
fi

if ! grep -Fq 'lesser-body-managed-dev.template.json: McpServerStreamTableC6A2DC7E TableName must contain mcp-streams-v2' "${TABLE_NAME_ERR}"; then
  echo "verify_release_assets.sh did not report the expected stream-table name regression" >&2
  cat "${TABLE_NAME_ERR}" >&2
  exit 1
fi

echo "Regression confirmed: verifier rejects MCP named-resource logical-ID and table-name drift"
