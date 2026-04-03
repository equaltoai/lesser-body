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
mapfile -t checksum_descriptor < <(python3 - "${OUT_DIR}/lesser-body-release.json" <<'PY'
import json
import pathlib
import sys

release = json.loads(pathlib.Path(sys.argv[1]).read_text())
checksums = release.get("artifacts", {}).get("checksums")
if not isinstance(checksums, dict):
    raise SystemExit("lesser-body-release.json is missing artifacts.checksums")

path = checksums.get("path")
if not isinstance(path, str) or not path:
    raise SystemExit("lesser-body-release.json is missing artifacts.checksums.path")

algorithm = checksums.get("algorithm")
if not isinstance(algorithm, str) or not algorithm:
    raise SystemExit("lesser-body-release.json is missing artifacts.checksums.algorithm")

print(path)
print(algorithm)
PY
)

CHECKSUMS_PATH="${checksum_descriptor[0]:-}"
CHECKSUMS_ALGORITHM="${checksum_descriptor[1]:-}"

if [[ "${CHECKSUMS_PATH}" != "checksums.txt" ]]; then
  echo "lesser-body-release.json has unsupported checksums.path: ${CHECKSUMS_PATH}" >&2
  exit 1
fi
if [[ "${CHECKSUMS_ALGORITHM}" != "sha256" ]]; then
  echo "lesser-body-release.json has unsupported checksums.algorithm: ${CHECKSUMS_ALGORITHM}" >&2
  exit 1
fi

CHECKSUMS_FILE="${OUT_DIR}/${CHECKSUMS_PATH}"

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
done < "${CHECKSUMS_FILE}"

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
  sha256sum -c "${CHECKSUMS_PATH}"
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

expected_template_parameters = {
    "AppName",
    "BaseDomain",
    "LesserBodyCodeBucketName",
    "LesserBodyCodeObjectKey",
    "JWTSecretArnParamPath",
    "JWTSecretKeyArnParamPath",
    "LesserStageDomainParamPath",
    "LesserTableNameParamPath",
}
deploy_template_parameters = {entry["name"] for entry in deploy["template_parameters"]}
assert expected_template_parameters.issubset(deploy_template_parameters), deploy["template_parameters"]

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

template_errors = []

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
        "JWTSecretArnParamPath",
        "JWTSecretKeyArnParamPath",
        "LesserStageDomainParamPath",
        "LesserTableNameParamPath",
    }
    assert required_params.issubset(template["Parameters"].keys()), template["Parameters"].keys()

    for param_name, param_spec in template["Parameters"].items():
        if "Default" in param_spec and not isinstance(param_spec["Default"], str):
            template_errors.append(
                f"{stage_path.name}: Parameters.{param_name}.Default must be a string, got {type(param_spec['Default']).__name__}"
            )

if template_errors:
    raise SystemExit("\n".join(template_errors))
PY

RELEASE_ABS_DIR="$(cd "${OUT_DIR}" && pwd)"

run_deploy_dry_run() {
  local stage="$1"
  local log_path="$2"

  (
    cd "${OUT_DIR}"
    bash ./deploy-lesser-body-from-release.sh \
      --dry-run \
      --stack-name "lesser-${stage}-lesser-body" \
      --asset-bucket example-artifacts-bucket \
      --app lesser \
      --stage "${stage}" \
      --base-domain example.com > "${log_path}"
  )
}

DRY_RUN_LOG_DEV="${RELEASE_ABS_DIR}/deploy-dry-run-dev.log"
DRY_RUN_LOG_STAGING="${RELEASE_ABS_DIR}/deploy-dry-run-staging.log"
DRY_RUN_LOG_LIVE="${RELEASE_ABS_DIR}/deploy-dry-run-live.log"

run_deploy_dry_run dev "${DRY_RUN_LOG_DEV}"
run_deploy_dry_run staging "${DRY_RUN_LOG_STAGING}"
run_deploy_dry_run live "${DRY_RUN_LOG_LIVE}"

DRY_RUN_LOG_NO_EXECUTE_CHANGESET="${RELEASE_ABS_DIR}/deploy-dry-run-dev-no-execute-changeset.log"
(
  cd "${OUT_DIR}"
  bash ./deploy-lesser-body-from-release.sh \
    --dry-run \
    --no-execute-changeset \
    --stack-name lesser-dev-lesser-body \
    --asset-bucket example-artifacts-bucket \
    --app lesser \
    --stage dev \
    --base-domain example.com > "${DRY_RUN_LOG_NO_EXECUTE_CHANGESET}"
)

all_dry_run_logs=(
  "${DRY_RUN_LOG_DEV}"
  "${DRY_RUN_LOG_STAGING}"
  "${DRY_RUN_LOG_LIVE}"
  "${DRY_RUN_LOG_NO_EXECUTE_CHANGESET}"
)

expected_s3_prefix="releases/lesser-body/${VERSION}/templates"
expected_parameter_overrides=(
  'JWTSecretArnParamPath=/lesser/shared/secrets/jwt-secret-arn'
  'JWTSecretKeyArnParamPath=/lesser/shared/kms/encryption-key-arn'
)

for stage in dev staging live; do
  stage_log="${RELEASE_ABS_DIR}/deploy-dry-run-${stage}.log"

  if ! grep -q -- 'lesser-body.zip' "${stage_log}"; then
    echo "dry-run output did not reference lesser-body.zip (stage: ${stage})" >&2
    exit 1
  fi
  if ! grep -q -- "lesser-body-managed-${stage}.template.json" "${stage_log}"; then
    echo "dry-run output did not reference the stage-specific managed deploy template (stage: ${stage})" >&2
    exit 1
  fi
  if ! grep -q -- "${RELEASE_ABS_DIR}/lesser-body.zip" "${stage_log}"; then
    echo "dry-run output did not stage the release-produced lambda zip from the release directory (stage: ${stage})" >&2
    exit 1
  fi
  if ! grep -q -- "${RELEASE_ABS_DIR}/lesser-body-managed-${stage}.template.json" "${stage_log}"; then
    echo "dry-run output did not use the release-produced stage template from the release directory (stage: ${stage})" >&2
    exit 1
  fi
  if ! grep -q -- '--s3-bucket example-artifacts-bucket' "${stage_log}"; then
    echo "dry-run output did not include expected --s3-bucket flag (stage: ${stage})" >&2
    exit 1
  fi
  if ! grep -q -- "--s3-prefix ${expected_s3_prefix}" "${stage_log}"; then
    echo "dry-run output did not include expected --s3-prefix (stage: ${stage}): ${expected_s3_prefix}" >&2
    exit 1
  fi

  for override in "${expected_parameter_overrides[@]}"; do
    if ! grep -q -- "${override}" "${stage_log}"; then
      echo "dry-run output did not include expected parameter override (stage: ${stage}): ${override}" >&2
      exit 1
    fi
  done

  expected_stage_overrides=(
    "LesserStageDomainParamPath=/lesser/${stage}/lesser/exports/v1/domain"
    "LesserTableNameParamPath=/lesser/${stage}/lesser/exports/v1/table_name"
  )
  for override in "${expected_stage_overrides[@]}"; do
    if ! grep -q -- "${override}" "${stage_log}"; then
      echo "dry-run output did not include expected stage parameter override (stage: ${stage}): ${override}" >&2
      exit 1
    fi
  done
done

if ! grep -q -- '--no-execute-changeset' "${DRY_RUN_LOG_NO_EXECUTE_CHANGESET}"; then
  echo "dry-run output did not include --no-execute-changeset when requested" >&2
  exit 1
fi

if ! grep -q -- '--s3-bucket example-artifacts-bucket' "${DRY_RUN_LOG_NO_EXECUTE_CHANGESET}"; then
  echo "dry-run output did not include expected --s3-bucket flag for --no-execute-changeset run" >&2
  exit 1
fi
if ! grep -q -- "--s3-prefix ${expected_s3_prefix}" "${DRY_RUN_LOG_NO_EXECUTE_CHANGESET}"; then
  echo "dry-run output did not include expected --s3-prefix for --no-execute-changeset run: ${expected_s3_prefix}" >&2
  exit 1
fi

for log in "${all_dry_run_logs[@]}"; do
  if grep -Eq 'scripts/build\.sh|cmd/release-template|/cdk/' "${log}"; then
    echo "dry-run output leaked source-checkout build paths: ${log}" >&2
    exit 1
  fi
done

echo "Verified release assets in ${OUT_DIR}"
