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

aux_field() {
  local field="$1"
  python3 - "${SOURCE_DIR}/lesser-body-deploy.json" "${field}" <<'PY'
import json
import pathlib
import sys
manifest = json.loads(pathlib.Path(sys.argv[1]).read_text())
field = sys.argv[2]
assets = manifest.get("auxiliary_assets") or []
if not assets:
    raise SystemExit("source release does not declare auxiliary assets")
value = assets[0].get(field)
if not isinstance(value, str) or not value:
    raise SystemExit(f"first auxiliary asset is missing {field}")
print(value)
PY
}

AUX_PATH="$(aux_field path)"
AUX_PARAMETER="$(aux_field template_parameter)"

refresh_release_metadata() {
  local release_dir="$1"
  python3 "${ROOT_DIR}/scripts/managed_release.py" refresh-metadata "${release_dir}"
}

MISSING_CHECKSUM_DIR="${TMP_DIR}/release-missing-auxiliary-checksum"
cp -R "${SOURCE_DIR}" "${MISSING_CHECKSUM_DIR}"
awk -v asset="${AUX_PATH}" '$2 != asset { print }' "${MISSING_CHECKSUM_DIR}/checksums.txt" > "${MISSING_CHECKSUM_DIR}/checksums.txt.tmp"
mv "${MISSING_CHECKSUM_DIR}/checksums.txt.tmp" "${MISSING_CHECKSUM_DIR}/checksums.txt"

MISSING_CHECKSUM_ERR="${TMP_DIR}/verify-missing-auxiliary-checksum.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${MISSING_CHECKSUM_DIR}" > /dev/null 2> "${MISSING_CHECKSUM_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted missing checksum coverage for auxiliary asset ${AUX_PATH}" >&2
  exit 1
fi

if ! grep -Fqx "checksums.txt is missing published managed asset: ${AUX_PATH}" "${MISSING_CHECKSUM_ERR}"; then
  echo "verify_release_assets.sh did not report the expected missing-auxiliary checksum error" >&2
  cat "${MISSING_CHECKSUM_ERR}" >&2
  exit 1
fi

HIDDEN_BOOTSTRAP_DIR="${TMP_DIR}/release-hidden-bootstrap-auxiliary-code"
cp -R "${SOURCE_DIR}" "${HIDDEN_BOOTSTRAP_DIR}"

python3 - "${HIDDEN_BOOTSTRAP_DIR}" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
deploy = json.loads((root / "lesser-body-deploy.json").read_text())
asset = deploy["auxiliary_assets"][0]
ref = next(ref for ref in asset["template_references"] if ref["stage"] == "dev")
template_path = root / ref["template"]
template = json.loads(template_path.read_text())
code = template["Resources"][ref["logical_id"]]["Properties"]["Code"]
code["S3Bucket"] = {"Fn::Sub": "cdk-hnb659fds-assets-${AWS::AccountId}-${AWS::Region}"}
code["S3Key"] = "hidden-cdk-bootstrap-provider.zip"
template_path.write_text(json.dumps(template, indent=2) + "\n")
PY

refresh_release_metadata "${HIDDEN_BOOTSTRAP_DIR}"

HIDDEN_BOOTSTRAP_ERR="${TMP_DIR}/verify-hidden-bootstrap.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${HIDDEN_BOOTSTRAP_DIR}" > /dev/null 2> "${HIDDEN_BOOTSTRAP_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted a managed template with hidden CDK/bootstrap auxiliary code" >&2
  exit 1
fi

if ! grep -Fq 'lesser-body-managed-dev.template.json: lambda ' "${HIDDEN_BOOTSTRAP_ERR}" || \
   ! grep -Fq 'Code.S3Bucket must Ref LesserBodyCodeBucketName' "${HIDDEN_BOOTSTRAP_ERR}"; then
  echo "verify_release_assets.sh did not report the expected hidden-bootstrap auxiliary code error" >&2
  cat "${HIDDEN_BOOTSTRAP_ERR}" >&2
  exit 1
fi

UNDECLARED_AUX_DIR="${TMP_DIR}/release-undeclared-auxiliary-code"
cp -R "${SOURCE_DIR}" "${UNDECLARED_AUX_DIR}"

python3 - "${UNDECLARED_AUX_DIR}" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
deploy_path = root / "lesser-body-deploy.json"
release_path = root / "lesser-body-release.json"
deploy = json.loads(deploy_path.read_text())
release = json.loads(release_path.read_text())
deploy["auxiliary_assets"] = []
release.setdefault("artifacts", {})["auxiliary_assets"] = []
deploy_path.write_text(json.dumps(deploy, indent=2) + "\n")
release_path.write_text(json.dumps(release, indent=2) + "\n")
PY

refresh_release_metadata "${UNDECLARED_AUX_DIR}"

UNDECLARED_AUX_ERR="${TMP_DIR}/verify-undeclared-auxiliary.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${UNDECLARED_AUX_DIR}" > /dev/null 2> "${UNDECLARED_AUX_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted a template referencing an undeclared auxiliary asset parameter" >&2
  exit 1
fi

if ! grep -Fq "references auxiliary code key parameter ${AUX_PARAMETER}" "${UNDECLARED_AUX_ERR}"; then
  echo "verify_release_assets.sh did not report the expected undeclared auxiliary parameter error" >&2
  cat "${UNDECLARED_AUX_ERR}" >&2
  exit 1
fi

echo "Regression confirmed: verifier rejects missing auxiliary checksums, hidden CDK/bootstrap code refs, and undeclared auxiliary parameters"
