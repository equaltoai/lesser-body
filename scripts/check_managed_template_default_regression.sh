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

REGRESSION_DIR="${TMP_DIR}/release-bad-managed-template-default"
cp -R "${SOURCE_DIR}" "${REGRESSION_DIR}"

python3 - "${REGRESSION_DIR}/lesser-body-managed-dev.template.json" <<'PY'
import json
import hashlib
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
template = json.loads(path.read_text())
template["Parameters"]["LesserStageDomainParamLookupParameter"] = {
    "Type": "AWS::SSM::Parameter::Value<String>",
    "Default": {"Ref": "LesserStageDomainParamPath"},
}
path.write_text(json.dumps(template, indent=2) + "\n")

digest = hashlib.sha256(path.read_bytes()).hexdigest()
size = path.stat().st_size

deploy_manifest_path = path.parent / "lesser-body-deploy.json"
deploy_manifest = json.loads(deploy_manifest_path.read_text())
deploy_manifest["templates"]["dev"]["sha256"] = digest
deploy_manifest["templates"]["dev"]["bytes"] = size
deploy_manifest_path.write_text(json.dumps(deploy_manifest, indent=2) + "\n")
deploy_manifest_digest = hashlib.sha256(deploy_manifest_path.read_bytes()).hexdigest()
deploy_manifest_size = deploy_manifest_path.stat().st_size

release_manifest_path = path.parent / "lesser-body-release.json"
release_manifest = json.loads(release_manifest_path.read_text())
release_manifest["artifacts"]["deploy_manifest"]["sha256"] = deploy_manifest_digest
release_manifest["artifacts"]["deploy_manifest"]["bytes"] = deploy_manifest_size
release_manifest["artifacts"]["deploy_templates"]["dev"]["sha256"] = digest
release_manifest["artifacts"]["deploy_templates"]["dev"]["bytes"] = size
release_manifest_path.write_text(json.dumps(release_manifest, indent=2) + "\n")
PY

python3 - "${REGRESSION_DIR}" <<'PY'
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
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
    data = (root / asset).read_bytes()
    lines.append(f"{hashlib.sha256(data).hexdigest()}  {asset}\n")

(root / "checksums.txt").write_text("".join(lines))
PY

VERIFY_ERR="${TMP_DIR}/verify.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${REGRESSION_DIR}" > /dev/null 2> "${VERIFY_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted a managed template with a non-string parameter default" >&2
  exit 1
fi

EXPECTED_ERROR='lesser-body-managed-dev.template.json: Parameters.LesserStageDomainParamLookupParameter.Default must be a string, got dict'
if ! grep -Fqx "${EXPECTED_ERROR}" "${VERIFY_ERR}"; then
  echo "verify_release_assets.sh did not report the expected managed-template default error" >&2
  cat "${VERIFY_ERR}" >&2
  exit 1
fi

echo "Regression confirmed: verifier rejects non-string managed template parameter defaults"
