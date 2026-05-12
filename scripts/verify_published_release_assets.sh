#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: verify_published_release_assets.sh --version <tag> [options]

Required:
  --version <tag>          GitHub release tag to download and verify (example: v0.2.4)

Optional:
  --repo <owner/name>      GitHub repository to download from (default: equaltoai/lesser-body)
  --out-dir <dir>          Directory to download assets into (default: temporary directory)
  --stack-name <name>      CloudFormation stack name for optional deploy CLI verification
  --asset-bucket <bucket>  S3 bucket for optional deploy CLI verification
  --stage <stage>          Stage for optional deploy CLI verification: dev | staging | live
  --app <slug>             App slug for optional deploy CLI verification (default: lesser)
  --base-domain <domain>   Optional base domain override for deploy CLI verification
  --asset-prefix <prefix>  Optional asset prefix override for deploy CLI verification
EOF
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

VERSION=""
REPO="equaltoai/lesser-body"
OUT_DIR=""
STACK_NAME=""
ASSET_BUCKET=""
STAGE=""
APP_NAME="lesser"
BASE_DOMAIN=""
ASSET_PREFIX=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    --repo)
      REPO="$2"
      shift 2
      ;;
    --out-dir)
      OUT_DIR="$2"
      shift 2
      ;;
    --stack-name)
      STACK_NAME="$2"
      shift 2
      ;;
    --asset-bucket)
      ASSET_BUCKET="$2"
      shift 2
      ;;
    --stage)
      STAGE="$2"
      shift 2
      ;;
    --app)
      APP_NAME="$2"
      shift 2
      ;;
    --base-domain)
      BASE_DOMAIN="$2"
      shift 2
      ;;
    --asset-prefix)
      ASSET_PREFIX="$2"
      shift 2
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

if [[ -z "${VERSION}" ]]; then
  echo "--version is required" >&2
  exit 1
fi

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
mkdir -p "${OUT_DIR}"

download_asset() {
  local asset="$1"
  local target="${OUT_DIR}/${asset}"
  local target_dir
  target_dir="$(dirname "${target}")"
  mkdir -p "${target_dir}"

  if ! gh release download "${VERSION}" \
    --repo "${REPO}" \
    --dir "${OUT_DIR}" \
    --clobber \
    --pattern "${asset}"; then
    if [[ "${asset}" == */* ]]; then
      gh release download "${VERSION}" \
        --repo "${REPO}" \
        --dir "${OUT_DIR}" \
        --clobber \
        --pattern "$(basename "${asset}")"
    else
      return 1
    fi
  fi

  if [[ ! -f "${target}" && "${asset}" == */* && -f "${OUT_DIR}/$(basename "${asset}")" ]]; then
    mv -f "${OUT_DIR}/$(basename "${asset}")" "${target}"
  fi
  if [[ ! -f "${target}" ]]; then
    echo "downloaded release asset is missing: ${asset}" >&2
    exit 1
  fi
}

mapfile -t base_assets < <(bash "${ROOT_DIR}/scripts/list_release_assets.sh")
for asset in "${base_assets[@]}"; do
  download_asset "${asset}"
done

mapfile -t release_assets < <(bash "${ROOT_DIR}/scripts/list_release_assets.sh" "${OUT_DIR}")
for asset in "${release_assets[@]}"; do
  if [[ ! -f "${OUT_DIR}/${asset}" ]]; then
    download_asset "${asset}"
  fi
done

GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${OUT_DIR}"

if [[ -n "${STACK_NAME}" || -n "${ASSET_BUCKET}" || -n "${STAGE}" ]]; then
  if [[ -z "${STACK_NAME}" || -z "${ASSET_BUCKET}" || -z "${STAGE}" ]]; then
    echo "--stack-name, --asset-bucket, and --stage must all be provided for deploy CLI verification" >&2
    exit 1
  fi

  deploy_args=(
    --stack-name "${STACK_NAME}"
    --asset-bucket "${ASSET_BUCKET}"
    --stage "${STAGE}"
    --app "${APP_NAME}"
    --no-execute-changeset
  )
  if [[ -n "${BASE_DOMAIN}" ]]; then
    deploy_args+=(--base-domain "${BASE_DOMAIN}")
  fi
  if [[ -n "${ASSET_PREFIX}" ]]; then
    deploy_args+=(--asset-prefix "${ASSET_PREFIX}")
  fi

  (
    cd "${OUT_DIR}"
    bash ./deploy-lesser-body-from-release.sh "${deploy_args[@]}"
  )
fi

echo "Verified published release assets for ${REPO} ${VERSION} in ${OUT_DIR}"
