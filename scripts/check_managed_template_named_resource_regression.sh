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
  python3 "${ROOT_DIR}/scripts/managed_release.py" refresh-metadata "${release_dir}"
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

TASK_LOGICAL_ID_DIR="${TMP_DIR}/release-bad-task-logical-id"
cp -R "${SOURCE_DIR}" "${TASK_LOGICAL_ID_DIR}"

python3 - "${TASK_LOGICAL_ID_DIR}/lesser-body-managed-dev.template.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
template = json.loads(path.read_text())
resources = template["Resources"]
resources["BrokenTaskTable12345678"] = resources.pop("McpServerTaskTable72DDFBBB")
env = next(
    resource["Properties"]["Environment"]["Variables"]
    for resource in resources.values()
    if resource.get("Type") == "AWS::Lambda::Function"
    and resource.get("Properties", {}).get("Handler") == "bootstrap"
)
env["MCP_TASK_TABLE"] = {"Ref": "BrokenTaskTable12345678"}
path.write_text(json.dumps(template, indent=2) + "\n")
PY

refresh_release_metadata "${TASK_LOGICAL_ID_DIR}"

TASK_LOGICAL_ID_ERR="${TMP_DIR}/verify-task-logical-id.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${TASK_LOGICAL_ID_DIR}" > /dev/null 2> "${TASK_LOGICAL_ID_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted a managed template with a drifted task table logical ID" >&2
  exit 1
fi

if ! grep -Fq 'lesser-body-managed-dev.template.json: missing expected resource McpServerTaskTable72DDFBBB' "${TASK_LOGICAL_ID_ERR}"; then
  echo "verify_release_assets.sh did not report the expected task-table logical-ID regression" >&2
  cat "${TASK_LOGICAL_ID_ERR}" >&2
  exit 1
fi

SPILL_BUCKET_DIR="${TMP_DIR}/release-missing-stream-spill-bucket"
cp -R "${SOURCE_DIR}" "${SPILL_BUCKET_DIR}"

python3 - "${SPILL_BUCKET_DIR}/lesser-body-managed-dev.template.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
template = json.loads(path.read_text())
resources = template["Resources"]
for logical_id, resource in list(resources.items()):
    if resource.get("Type") == "AWS::S3::Bucket" and "LifecycleConfiguration" in resource.get("Properties", {}):
        resources.pop(logical_id)
path.write_text(json.dumps(template, indent=2) + "\n")
PY

refresh_release_metadata "${SPILL_BUCKET_DIR}"

SPILL_BUCKET_ERR="${TMP_DIR}/verify-spill-bucket.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${SPILL_BUCKET_DIR}" > /dev/null 2> "${SPILL_BUCKET_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted a managed template without the stream-spill bucket" >&2
  exit 1
fi

if ! grep -Fq 'lesser-body-managed-dev.template.json: expected exactly one stream-spill bucket with lifecycle configuration, found 0' "${SPILL_BUCKET_ERR}"; then
  echo "verify_release_assets.sh did not report the expected stream-spill bucket regression" >&2
  cat "${SPILL_BUCKET_ERR}" >&2
  exit 1
fi

echo "Regression confirmed: verifier rejects MCP named-resource, table-name, task-storage, and stream-spill drift"
