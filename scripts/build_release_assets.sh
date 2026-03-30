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

export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

if [[ -z "${VERSION}" ]]; then
  echo "version is required" >&2
  exit 1
fi
if [[ "${VERSION}" != v* ]]; then
  echo "version must start with 'v' (for example: v1.0.0)" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"
rm -f \
  "${OUT_DIR}/lesser-body.zip" \
  "${OUT_DIR}/lesser-body-deploy.json" \
  "${OUT_DIR}/lesser-body-managed-dev.template.json" \
  "${OUT_DIR}/lesser-body-managed-staging.template.json" \
  "${OUT_DIR}/lesser-body-managed-live.template.json" \
  "${OUT_DIR}/deploy-lesser-body-from-release.sh" \
  "${OUT_DIR}/checksums.txt" \
  "${OUT_DIR}/lesser-body-release.json"

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
' "$(go list -f '{{.Dir}}' github.com/theory-cloud/apptheory/runtime/mcp)/server.go")"
if [[ -z "${MCP_PROTOCOL_VERSION}" ]]; then
  echo "failed to resolve MCP protocol version from github.com/theory-cloud/apptheory/runtime/mcp" >&2
  exit 1
fi

sha256_of() {
  sha256sum "$1" | awk '{print $1}'
}

bytes_of() {
  wc -c < "$1" | tr -d '[:space:]'
}

bash scripts/build.sh
cp -f dist/lesser-body.zip "${OUT_DIR}/lesser-body.zip"
cp -f scripts/deploy-lesser-body-from-release.sh "${OUT_DIR}/deploy-lesser-body-from-release.sh"
chmod +x "${OUT_DIR}/deploy-lesser-body-from-release.sh"

declare -A TEMPLATE_SHA256
declare -A TEMPLATE_BYTES
declare -A TEMPLATE_PATHS

for stage in dev staging live; do
  stage_outdir="${ASSEMBLY_DIR}/${stage}"
  (
    cd "${ROOT_DIR}/cdk"
    go run ./cmd/release-template --version "${VERSION}" --stage "${stage}" --outdir "${stage_outdir}"
  )

  template_source="$(find "${stage_outdir}" -maxdepth 1 -name '*.template.json' | head -n 1)"
  if [[ -z "${template_source}" || ! -f "${template_source}" ]]; then
    echo "failed to synthesize lesser-body managed deploy template for stage ${stage}" >&2
    exit 1
  fi

  template_name="lesser-body-managed-${stage}.template.json"
  cp -f "${template_source}" "${OUT_DIR}/${template_name}"
  TEMPLATE_PATHS["${stage}"]="${template_name}"
  TEMPLATE_SHA256["${stage}"]="$(sha256_of "${OUT_DIR}/${template_name}")"
  TEMPLATE_BYTES["${stage}"]="$(bytes_of "${OUT_DIR}/${template_name}")"
done

ZIP_SHA256="$(sha256_of "${OUT_DIR}/lesser-body.zip")"
ZIP_BYTES="$(bytes_of "${OUT_DIR}/lesser-body.zip")"
SCRIPT_SHA256="$(sha256_of "${OUT_DIR}/deploy-lesser-body-from-release.sh")"
SCRIPT_BYTES="$(bytes_of "${OUT_DIR}/deploy-lesser-body-from-release.sh")"

cat > "${OUT_DIR}/lesser-body-deploy.json" <<JSON
{
  "schema": 1,
  "name": "lesser-body-deploy",
  "version": "${VERSION}",
  "lambda": {
    "path": "lesser-body.zip",
    "sha256": "${ZIP_SHA256}",
    "bytes": ${ZIP_BYTES}
  },
  "templates": {
    "dev": {
      "path": "${TEMPLATE_PATHS[dev]}",
      "sha256": "${TEMPLATE_SHA256[dev]}",
      "bytes": ${TEMPLATE_BYTES[dev]},
      "format": "cloudformation-json"
    },
    "staging": {
      "path": "${TEMPLATE_PATHS[staging]}",
      "sha256": "${TEMPLATE_SHA256[staging]}",
      "bytes": ${TEMPLATE_BYTES[staging]},
      "format": "cloudformation-json"
    },
    "live": {
      "path": "${TEMPLATE_PATHS[live]}",
      "sha256": "${TEMPLATE_SHA256[live]}",
      "bytes": ${TEMPLATE_BYTES[live]},
      "format": "cloudformation-json"
    }
  },
  "script": {
    "path": "deploy-lesser-body-from-release.sh",
    "sha256": "${SCRIPT_SHA256}",
    "bytes": ${SCRIPT_BYTES}
  },
  "deploy_input_schema": 1,
  "source_checkout_required": false,
  "npm_install_required": false,
  "script_inputs": [
    {
      "name": "stack_name",
      "required": true,
      "description": "CloudFormation stack name to create or update."
    },
    {
      "name": "asset_bucket",
      "required": true,
      "description": "S3 bucket in the target account used to stage lesser-body.zip."
    },
    {
      "name": "stage",
      "required": true,
      "allowed_values": ["dev", "staging", "live"],
      "description": "Target Lesser stage used to select the matching release-produced CloudFormation template."
    },
    {
      "name": "app",
      "required": false,
      "default": "lesser",
      "description": "Lesser app slug used in resource naming and SSM paths."
    },
    {
      "name": "base_domain",
      "required": false,
      "description": "Optional base domain override. When omitted, the template reads /<app>/<stage>/lesser/exports/v1/domain from SSM."
    },
    {
      "name": "asset_prefix",
      "required": false,
      "default": "releases/lesser-body/${VERSION}",
      "description": "Optional S3 key prefix used when staging lesser-body.zip."
    }
  ],
  "template_parameters": [
    {
      "name": "AppName",
      "required": false,
      "default": "lesser"
    },
    {
      "name": "BaseDomain",
      "required": false,
      "default": ""
    },
    {
      "name": "LesserBodyCodeBucketName",
      "required": true
    },
    {
      "name": "LesserBodyCodeObjectKey",
      "required": true
    }
  ],
  "exports": [
    {
      "name": "mcp_lambda_arn",
      "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_lambda_arn"
    },
    {
      "name": "mcp_endpoint_url",
      "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url"
    },
    {
      "name": "mcp_session_table_name",
      "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_session_table_name"
    },
    {
      "name": "mcp_stream_table_name",
      "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_stream_table_name"
    }
  ]
}
JSON

DEPLOY_MANIFEST_SHA256="$(sha256_of "${OUT_DIR}/lesser-body-deploy.json")"
DEPLOY_MANIFEST_BYTES="$(bytes_of "${OUT_DIR}/lesser-body-deploy.json")"

cat > "${OUT_DIR}/lesser-body-release.json" <<JSON
{
  "schema": 1,
  "name": "lesser-body",
  "version": "${VERSION}",
  "git_sha": "${GIT_SHA}",
  "go_version": "${GO_VERSION}",
  "mcp": {
    "protocol_version": "${MCP_PROTOCOL_VERSION}"
  },
  "artifacts": {
    "checksums": {
      "path": "checksums.txt",
      "algorithm": "sha256"
    },
    "lambda_zip": {
      "path": "lesser-body.zip",
      "sha256": "${ZIP_SHA256}",
      "bytes": ${ZIP_BYTES}
    },
    "deploy_manifest": {
      "path": "lesser-body-deploy.json",
      "sha256": "${DEPLOY_MANIFEST_SHA256}",
      "bytes": ${DEPLOY_MANIFEST_BYTES},
      "schema": 1
    },
    "deploy_templates": {
      "dev": {
        "path": "${TEMPLATE_PATHS[dev]}",
        "sha256": "${TEMPLATE_SHA256[dev]}",
        "bytes": ${TEMPLATE_BYTES[dev]},
        "format": "cloudformation-json"
      },
      "staging": {
        "path": "${TEMPLATE_PATHS[staging]}",
        "sha256": "${TEMPLATE_SHA256[staging]}",
        "bytes": ${TEMPLATE_BYTES[staging]},
        "format": "cloudformation-json"
      },
      "live": {
        "path": "${TEMPLATE_PATHS[live]}",
        "sha256": "${TEMPLATE_SHA256[live]}",
        "bytes": ${TEMPLATE_BYTES[live]},
        "format": "cloudformation-json"
      }
    },
    "deploy_script": {
      "path": "deploy-lesser-body-from-release.sh",
      "sha256": "${SCRIPT_SHA256}",
      "bytes": ${SCRIPT_BYTES}
    }
  },
  "deploy": {
    "schema": 1,
    "manifest_path": "lesser-body-deploy.json",
    "template_selection": "by_stage",
    "source_checkout_required": false,
    "npm_install_required": false
  }
}
JSON

(
  cd "${OUT_DIR}"
  sha256sum \
    lesser-body.zip \
    lesser-body-deploy.json \
    "${TEMPLATE_PATHS[dev]}" \
    "${TEMPLATE_PATHS[staging]}" \
    "${TEMPLATE_PATHS[live]}" \
    deploy-lesser-body-from-release.sh \
    lesser-body-release.json > checksums.txt
)

echo "Wrote release assets to ${OUT_DIR}"
