#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: deploy-lesser-body-from-release.sh --stack-name <name> --asset-bucket <bucket> --stage <stage> [options]

Required:
  --stack-name <name>      CloudFormation stack name to create or update
  --asset-bucket <bucket>  S3 bucket in the target account used to stage lesser-body.zip
  --stage <stage>          Lesser stage: dev | staging | live

Optional:
  --app <slug>             Lesser app slug (default: lesser)
  --base-domain <domain>   Optional base domain override; omit to use Lesser's exported stage domain from SSM
  --asset-prefix <prefix>  S3 key prefix for the staged zip (default: releases/lesser-body/<version>)
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
DRY_RUN=0

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
    --dry-run)
      DRY_RUN=1
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

parameter_overrides=(
  "AppName=${APP_NAME}"
  "LesserBodyCodeBucketName=${ASSET_BUCKET}"
  "LesserBodyCodeObjectKey=${ASSET_KEY}"
)
if [[ -n "${BASE_DOMAIN}" ]]; then
  parameter_overrides+=("BaseDomain=${BASE_DOMAIN}")
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
  --capabilities CAPABILITY_NAMED_IAM
  --parameter-overrides
)
deploy_cmd+=("${parameter_overrides[@]}")

run_or_print "${upload_cmd[@]}"
run_or_print "${deploy_cmd[@]}"
