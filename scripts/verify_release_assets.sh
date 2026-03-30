#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <version> [out-dir]" >&2
  exit 1
fi

VERSION="$1"
OUT_DIR="${2:-}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cleanup() {
  if [[ -n "${TMP_DIR:-}" && -d "${TMP_DIR}" ]]; then
    rm -rf "${TMP_DIR}"
  fi
}
trap cleanup EXIT

if [[ -z "${OUT_DIR}" ]]; then
  TMP_DIR="$(mktemp -d)"
  OUT_DIR="${TMP_DIR}/release"
fi

if [[ ! -f "${OUT_DIR}/lesser-body-release.json" ]]; then
  bash "${ROOT_DIR}/scripts/build_release_assets.sh" "${VERSION}" "${OUT_DIR}"
fi

mapfile -t published_assets < <(bash "${ROOT_DIR}/scripts/list_release_assets.sh")

required_files=()
for asset in "${published_assets[@]}"; do
  required_files+=("${OUT_DIR}/${asset}")
done

for file in "${required_files[@]}"; do
  if [[ ! -f "${file}" ]]; then
    echo "missing release asset: ${file}" >&2
    exit 1
  fi
done

checksum_assets=()
while read -r _ path _; do
  if [[ -n "${path:-}" ]]; then
    checksum_assets+=("${path}")
  fi
done < "${OUT_DIR}/checksums.txt"

for asset in "${published_assets[@]}"; do
  if [[ "${asset}" == "checksums.txt" ]]; then
    continue
  fi
  if ! printf '%s\n' "${checksum_assets[@]}" | grep -Fxq "${asset}"; then
    echo "checksums.txt is missing published managed asset: ${asset}" >&2
    exit 1
  fi
done

(
  cd "${OUT_DIR}"
  sha256sum -c checksums.txt
)

python3 - "${OUT_DIR}" "${VERSION}" <<'PY'
import hashlib
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
version = sys.argv[2]

deploy = json.loads((root / "lesser-body-deploy.json").read_text())
release = json.loads((root / "lesser-body-release.json").read_text())

assert deploy["schema"] == 1, deploy["schema"]
assert deploy["version"] == version, deploy["version"]
assert release["schema"] == 1, release["schema"]
assert release["version"] == version, release["version"]
assert release["deploy"]["manifest_path"] == "lesser-body-deploy.json"
assert release["deploy"]["source_checkout_required"] is False
assert release["deploy"]["npm_install_required"] is False

expected_artifacts = {
    "lambda_zip": "lesser-body.zip",
    "deploy_manifest": "lesser-body-deploy.json",
    "deploy_script": "deploy-lesser-body-from-release.sh",
}

for key, path_name in expected_artifacts.items():
    meta = release["artifacts"][key]
    path = root / path_name
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    assert meta["path"] == path_name, meta
    assert meta["sha256"] == digest, (key, meta["sha256"], digest)
    assert meta["bytes"] == path.stat().st_size, (key, meta["bytes"], path.stat().st_size)

assert deploy["lambda"]["sha256"] == release["artifacts"]["lambda_zip"]["sha256"]
assert deploy["script"]["sha256"] == release["artifacts"]["deploy_script"]["sha256"]

for stage in ("dev", "staging", "live"):
    stage_meta = release["artifacts"]["deploy_templates"][stage]
    stage_path = root / stage_meta["path"]
    digest = hashlib.sha256(stage_path.read_bytes()).hexdigest()
    assert stage_meta["sha256"] == digest, (stage, stage_meta["sha256"], digest)
    assert stage_meta["bytes"] == stage_path.stat().st_size, (stage, stage_meta["bytes"], stage_path.stat().st_size)
    assert deploy["templates"][stage]["sha256"] == stage_meta["sha256"]

    template = json.loads(stage_path.read_text())
    template_text = json.dumps(template, sort_keys=True)
    assert "cdk-hnb659fds" not in template_text
    assert "aws:asset:path" not in template_text
    assert "../dist/lesser-body.zip" not in template_text

    required_params = {
        "AppName",
        "BaseDomain",
        "LesserBodyCodeBucketName",
        "LesserBodyCodeObjectKey",
    }
    assert required_params.issubset(template["Parameters"].keys()), template["Parameters"].keys()
PY

DRY_RUN_LOG="$(cd "${OUT_DIR}" && pwd)/deploy-dry-run.log"
(
  cd "${OUT_DIR}"
  bash ./deploy-lesser-body-from-release.sh \
    --dry-run \
    --stack-name lesser-dev-lesser-body \
    --asset-bucket example-artifacts-bucket \
    --app lesser \
    --stage dev \
    --base-domain example.com > "${DRY_RUN_LOG}"
)

if ! grep -q 'lesser-body.zip' "${DRY_RUN_LOG}"; then
  echo "dry-run output did not reference lesser-body.zip" >&2
  exit 1
fi
if ! grep -q 'lesser-body-managed-dev.template.json' "${DRY_RUN_LOG}"; then
  echo "dry-run output did not reference the stage-specific managed deploy template" >&2
  exit 1
fi
RELEASE_ABS_DIR="$(cd "${OUT_DIR}" && pwd)"
if ! grep -q "${RELEASE_ABS_DIR}/lesser-body.zip" "${DRY_RUN_LOG}"; then
  echo "dry-run output did not stage the release-produced lambda zip from the release directory" >&2
  exit 1
fi
if ! grep -q "${RELEASE_ABS_DIR}/lesser-body-managed-dev.template.json" "${DRY_RUN_LOG}"; then
  echo "dry-run output did not use the release-produced dev template from the release directory" >&2
  exit 1
fi
if grep -Eq 'scripts/build\.sh|cmd/release-template|/cdk/' "${DRY_RUN_LOG}"; then
  echo "dry-run output leaked source-checkout build paths" >&2
  exit 1
fi

echo "Verified release assets in ${OUT_DIR}"
