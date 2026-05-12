#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: deploy-lesser-body-from-release.sh --stack-name <name> --asset-bucket <bucket> --stage <stage> [options]

Required:
  --stack-name <name>      CloudFormation stack name to create or update
  --asset-bucket <bucket>  S3 bucket in the target account used to stage lesser-body.zip and CloudFormation templates
  --stage <stage>          Lesser stage: dev | staging | live

Optional:
  --app <slug>             Lesser app slug (default: lesser)
  --base-domain <domain>   Optional base domain override; omit to use Lesser's exported stage domain from SSM
  --lesser-host-instance-key-arn <arn>
                           Optional exact Secrets Manager ARN for the managed lesser-host instance key.
                           Defaults to $LESSER_HOST_INSTANCE_KEY_ARN when set.
  --asset-prefix <prefix>  S3 key prefix for staged zip and auxiliary assets (default: releases/lesser-body/<version>)
  --no-execute-changeset   Pass through to aws cloudformation deploy for verification-only change set creation
  --dry-run                Print the AWS commands without executing them
  -h, --help               Show this help text
EOF
}

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ZIP_PATH="${SCRIPT_DIR}/lesser-body.zip"
DEPLOY_MANIFEST_PATH="${SCRIPT_DIR}/lesser-body-deploy.json"

STACK_NAME=""
ASSET_BUCKET=""
ASSET_PREFIX=""
APP_NAME="lesser"
STAGE=""
BASE_DOMAIN=""
LESSER_HOST_INSTANCE_KEY_ARN="${LESSER_HOST_INSTANCE_KEY_ARN:-}"
DRY_RUN=0
NO_EXECUTE_CHANGESET=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --stack-name)
      STACK_NAME="$2"
      shift 2
      ;;
    --asset-bucket)
      ASSET_BUCKET="$2"
      shift 2
      ;;
    --asset-prefix)
      ASSET_PREFIX="$2"
      shift 2
      ;;
    --app)
      APP_NAME="$2"
      shift 2
      ;;
    --stage)
      STAGE="$2"
      shift 2
      ;;
    --base-domain)
      BASE_DOMAIN="$2"
      shift 2
      ;;
    --lesser-host-instance-key-arn)
      LESSER_HOST_INSTANCE_KEY_ARN="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --no-execute-changeset)
      NO_EXECUTE_CHANGESET=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

require_file() {
  local path="$1"
  if [[ ! -f "${path}" ]]; then
    echo "required release asset is missing: ${path}" >&2
    exit 1
  fi
}

print_cmd() {
  printf '%q ' "$@"
  printf '\n'
}

run_or_print() {
  if [[ "${DRY_RUN}" == "1" ]]; then
    print_cmd "$@"
    return 0
  fi
  "$@"
}

load_auxiliary_assets() {
  python3 - "${DEPLOY_MANIFEST_PATH}" <<'PY'
import json
import pathlib
import re
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text())
schema = manifest.get("schema", 1)
if schema == 1:
    raise SystemExit(0)
if schema != 2:
    raise SystemExit(f"unsupported lesser-body deploy manifest schema: {schema}")
capabilities = manifest.get("required_capabilities") or []
if "managed_auxiliary_assets_v1" not in capabilities:
    raise SystemExit("lesser-body deploy manifest schema 2 requires managed_auxiliary_assets_v1 capability")
unsupported = [cap for cap in capabilities if cap != "managed_auxiliary_assets_v1"]
if unsupported:
    raise SystemExit(f"unsupported lesser-body required capability: {unsupported[0]}")

def safe(value, label):
    if not isinstance(value, str) or not value.strip():
        raise SystemExit(f"{label} is required")
    value = value.strip()
    if value.startswith("/") or "\\" in value or "\n" in value or "\r" in value:
        raise SystemExit(f"{label} must be a safe relative path")
    for part in value.split("/"):
        if part in ("", ".", ".."):
            raise SystemExit(f"{label} must not contain empty/current/parent path segments")
    return value

def param(value, label):
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z][A-Za-z0-9]*", value.strip()):
        raise SystemExit(f"{label} must be a CloudFormation parameter identifier")
    return value.strip()

for index, asset in enumerate(manifest.get("auxiliary_assets") or []):
    if not isinstance(asset, dict):
        raise SystemExit(f"auxiliary asset {index} must be an object")
    asset_id = str(asset.get("id") or "").strip()
    if not asset_id:
        raise SystemExit(f"auxiliary asset {index} id is required")
    path = safe(asset.get("path"), f"auxiliary asset {asset_id} path")
    s3_key = safe(asset.get("s3_key"), f"auxiliary asset {asset_id} s3_key")
    template_parameter = param(asset.get("template_parameter"), f"auxiliary asset {asset_id} template_parameter")
    content_type = str(asset.get("content_type") or "").strip()
    required = "false" if asset.get("required") is False else "true"
    print("\t".join([asset_id, path, s3_key, template_parameter, content_type, required]))
PY
}

require_file "${ZIP_PATH}"
require_file "${DEPLOY_MANIFEST_PATH}"

if [[ -z "${STACK_NAME}" ]]; then
  echo "--stack-name is required" >&2
  exit 1
fi
if [[ -z "${ASSET_BUCKET}" ]]; then
  echo "--asset-bucket is required" >&2
  exit 1
fi
case "${STAGE}" in
  dev|staging|live) ;;
  *)
    echo "--stage must be one of: dev, staging, live" >&2
    exit 1
    ;;
esac

TEMPLATE_PATH="${SCRIPT_DIR}/lesser-body-managed-${STAGE}.template.json"
require_file "${TEMPLATE_PATH}"

RELEASE_VERSION="$(awk -F'"' '/"version"[[:space:]]*:/ { print $4; exit }' "${DEPLOY_MANIFEST_PATH}")"
if [[ -z "${RELEASE_VERSION}" ]]; then
  echo "failed to resolve release version from ${DEPLOY_MANIFEST_PATH}" >&2
  exit 1
fi

if [[ -z "${ASSET_PREFIX}" ]]; then
  ASSET_PREFIX="releases/lesser-body/${RELEASE_VERSION}"
fi
ASSET_PREFIX="${ASSET_PREFIX#/}"
ASSET_KEY="${ASSET_PREFIX%/}/lesser-body.zip"
mapfile -t AUXILIARY_ASSETS < <(load_auxiliary_assets)
JWT_SECRET_ARN_PARAM_PATH="/${APP_NAME}/shared/secrets/jwt-secret-arn"
JWT_SECRET_KEY_ARN_PARAM_PATH="/${APP_NAME}/shared/kms/encryption-key-arn"
LESSER_STAGE_DOMAIN_PARAM_PATH="/${APP_NAME}/${STAGE}/lesser/exports/v1/domain"
LESSER_TABLE_NAME_PARAM_PATH="/${APP_NAME}/${STAGE}/lesser/exports/v1/table_name"

parameter_overrides=(
  "AppName=${APP_NAME}"
  "LesserBodyCodeBucketName=${ASSET_BUCKET}"
  "LesserBodyCodeObjectKey=${ASSET_KEY}"
  "JWTSecretArnParamPath=${JWT_SECRET_ARN_PARAM_PATH}"
  "JWTSecretKeyArnParamPath=${JWT_SECRET_KEY_ARN_PARAM_PATH}"
  "LesserStageDomainParamPath=${LESSER_STAGE_DOMAIN_PARAM_PATH}"
  "LesserTableNameParamPath=${LESSER_TABLE_NAME_PARAM_PATH}"
)

for asset_row in "${AUXILIARY_ASSETS[@]}"; do
  IFS=$'\t' read -r aux_id aux_path aux_s3_key aux_template_parameter aux_content_type aux_required <<< "${asset_row}"
  aux_object_key="${ASSET_PREFIX%/}/${aux_s3_key}"
  parameter_overrides+=("${aux_template_parameter}=${aux_object_key}")
done

if [[ -n "${BASE_DOMAIN}" ]]; then
  parameter_overrides+=("BaseDomain=${BASE_DOMAIN}")
fi
if [[ -n "${LESSER_HOST_INSTANCE_KEY_ARN}" ]]; then
  if [[ "${LESSER_HOST_INSTANCE_KEY_ARN}" == *"*"* || "${LESSER_HOST_INSTANCE_KEY_ARN}" == *"?"* ]]; then
    echo "--lesser-host-instance-key-arn must be an exact Secrets Manager secret ARN without wildcards" >&2
    exit 1
  fi
  if [[ ! "${LESSER_HOST_INSTANCE_KEY_ARN}" =~ ^arn:[^:*]+:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:[A-Za-z0-9/_+=.@-]+$ ]]; then
    echo "--lesser-host-instance-key-arn must be an exact Secrets Manager secret ARN" >&2
    exit 1
  fi
  parameter_overrides+=("LesserHostInstanceKeyARN=${LESSER_HOST_INSTANCE_KEY_ARN}")
fi

upload_cmd=(
  aws s3 cp
  "${ZIP_PATH}"
  "s3://${ASSET_BUCKET}/${ASSET_KEY}"
)

deploy_cmd=(
  aws cloudformation deploy
  --stack-name "${STACK_NAME}"
  --template-file "${TEMPLATE_PATH}"
  --s3-bucket "${ASSET_BUCKET}"
  --s3-prefix "${ASSET_PREFIX%/}/templates"
  --capabilities CAPABILITY_NAMED_IAM
)
if [[ "${NO_EXECUTE_CHANGESET}" == "1" ]]; then
  deploy_cmd+=(--no-execute-changeset)
fi
deploy_cmd+=(--parameter-overrides)
deploy_cmd+=("${parameter_overrides[@]}")

run_or_print "${upload_cmd[@]}"
for asset_row in "${AUXILIARY_ASSETS[@]}"; do
  IFS=$'\t' read -r aux_id aux_path aux_s3_key aux_template_parameter aux_content_type aux_required <<< "${asset_row}"
  aux_file="${SCRIPT_DIR}/${aux_path}"
  require_file "${aux_file}"
  aux_object_key="${ASSET_PREFIX%/}/${aux_s3_key}"
  aux_cmd=(aws s3 cp "${aux_file}" "s3://${ASSET_BUCKET}/${aux_object_key}")
  if [[ -n "${aux_content_type}" ]]; then
    aux_cmd+=(--content-type "${aux_content_type}")
  fi
  run_or_print "${aux_cmd[@]}"
done
run_or_print "${deploy_cmd[@]}"
