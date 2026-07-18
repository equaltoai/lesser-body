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

mapfile -t published_assets < <(bash "${ROOT_DIR}/scripts/list_release_assets.sh" "${OUT_DIR}")
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

def read_json(name):
    return json.loads((root / name).read_text())

def digest(name):
    path = root / name
    data = path.read_bytes()
    return hashlib.sha256(data).hexdigest(), path.stat().st_size

def err(message):
    errors.append(message)

def ref_name(value):
    return value.get("Ref") if isinstance(value, dict) and isinstance(value.get("Ref"), str) else ""

def value_at_path(root_obj, dotted):
    cur = root_obj
    for part in dotted.split("."):
        if not isinstance(cur, dict) or part not in cur:
            return None
        cur = cur[part]
    return cur

def sibling_at_path(root_obj, dotted, sibling):
    parts = dotted.split(".")
    if not parts:
        return None
    return value_at_path(root_obj, ".".join(parts[:-1] + [sibling]))

def stable_json(value):
    return json.dumps(value, sort_keys=True)

errors = []
deploy = read_json("lesser-body-deploy.json")
release = read_json("lesser-body-release.json")

schema = deploy.get("schema")
if schema not in (1, 2):
    err(f"lesser-body-deploy.json has unsupported schema: {schema}")
if release.get("schema") != schema:
    err(f"lesser-body-release.json schema must match deploy schema {schema}, got {release.get('schema')}")
if deploy.get("version") != version:
    err(f"lesser-body-deploy.json version mismatch: {deploy.get('version')} != {version}")
if release.get("version") != version:
    err(f"lesser-body-release.json version mismatch: {release.get('version')} != {version}")
if release.get("deploy", {}).get("manifest_path") != "lesser-body-deploy.json":
    err("lesser-body-release.json deploy.manifest_path must be lesser-body-deploy.json")
if release.get("deploy", {}).get("source_checkout_required") is not False:
    err("lesser-body-release.json deploy.source_checkout_required must be false")
if release.get("deploy", {}).get("npm_install_required") is not False:
    err("lesser-body-release.json deploy.npm_install_required must be false")
if schema == 2:
    for label, caps in {
        "lesser-body-deploy.json required_capabilities": deploy.get("required_capabilities") or [],
        "lesser-body-release.json deploy.required_capabilities": release.get("deploy", {}).get("required_capabilities") or [],
    }.items():
        if "managed_auxiliary_assets_v1" not in caps:
            err(f"{label} must include managed_auxiliary_assets_v1")
        for cap in caps:
            if cap != "managed_auxiliary_assets_v1":
                err(f"{label} contains unsupported capability {cap}")

expected_template_parameters = {
    "AppName",
    "BaseDomain",
    "LesserBodyCodeBucketName",
    "LesserBodyCodeObjectKey",
    "LesserHostInstanceKeyARN",
    "LesserSoulBindingIntegrationBearerSecretARN",
    "JWTSecretArnParamPath",
    "JWTSecretKeyArnParamPath",
    "LesserStageDomainParamPath",
    "LesserTableNameParamPath",
}
deploy_template_parameters = {entry.get("name") for entry in deploy.get("template_parameters", []) if isinstance(entry, dict)}
missing_template_parameters = sorted(expected_template_parameters.difference(deploy_template_parameters))
if missing_template_parameters:
    err(f"lesser-body-deploy.json template_parameters missing {missing_template_parameters}")

expected_artifacts = {
    "lambda_zip": "lesser-body.zip",
    "deploy_manifest": "lesser-body-deploy.json",
    "deploy_script": "deploy-lesser-body-from-release.sh",
}

for key, path_name in expected_artifacts.items():
    meta = release.get("artifacts", {}).get(key, {})
    actual_sha, actual_bytes = digest(path_name)
    if meta.get("path") != path_name:
        err(f"lesser-body-release.json artifacts.{key}.path must be {path_name}, got {meta.get('path')!r}")
    if meta.get("sha256") != actual_sha:
        err(f"lesser-body-release.json artifacts.{key}.sha256 mismatch")
    if meta.get("bytes") != actual_bytes:
        err(f"lesser-body-release.json artifacts.{key}.bytes mismatch")

if deploy.get("lambda", {}).get("sha256") != release.get("artifacts", {}).get("lambda_zip", {}).get("sha256"):
    err("deploy lambda checksum must match release lambda_zip checksum")
if deploy.get("script", {}).get("sha256") != release.get("artifacts", {}).get("deploy_script", {}).get("sha256"):
    err("deploy script checksum must match release deploy_script checksum")

release_auxiliary_assets = release.get("artifacts", {}).get("auxiliary_assets") or []
deploy_auxiliary_assets = deploy.get("auxiliary_assets") or []
if schema == 1 and (release_auxiliary_assets or deploy_auxiliary_assets):
    err("schema 1 release must not declare auxiliary assets")
if schema == 2 and not deploy_auxiliary_assets:
    err("schema 2 release must declare auxiliary_assets")
if len(release_auxiliary_assets) != len(deploy_auxiliary_assets):
    err("release and deploy auxiliary asset counts must match")

release_aux_by_path = {asset.get("path"): asset for asset in release_auxiliary_assets if isinstance(asset, dict)}
aux_by_param = {}
aux_by_id = {}
for asset in deploy_auxiliary_assets:
    if not isinstance(asset, dict):
        err("auxiliary asset entries must be objects")
        continue
    asset_id = asset.get("id")
    path_name = asset.get("path")
    parameter = asset.get("template_parameter")
    s3_key = asset.get("s3_key")
    for field, value in (("id", asset_id), ("path", path_name), ("template_parameter", parameter), ("s3_key", s3_key)):
        if not isinstance(value, str) or not value:
            err(f"auxiliary asset {asset_id or path_name or '<unknown>'} missing {field}")
    if isinstance(path_name, str) and path_name:
        actual_sha, actual_bytes = digest(path_name)
        if asset.get("sha256") != actual_sha:
            err(f"auxiliary asset {path_name} sha256 mismatch")
        if asset.get("bytes") != actual_bytes:
            err(f"auxiliary asset {path_name} bytes mismatch")
        release_asset = release_aux_by_path.get(path_name)
        if not isinstance(release_asset, dict):
            err(f"release manifest is missing auxiliary asset {path_name}")
        else:
            for field in ("id", "sha256", "bytes", "required", "s3_key", "template_parameter", "content_type"):
                if release_asset.get(field) != asset.get(field):
                    err(f"auxiliary asset {path_name} {field} does not match release manifest")
    if isinstance(parameter, str):
        aux_by_param[parameter] = asset
    if isinstance(asset_id, str):
        aux_by_id[asset_id] = asset

for entry in deploy.get("template_parameters", []):
    if not isinstance(entry, dict):
        continue
    derived = entry.get("derived_from_auxiliary_asset")
    if derived and derived not in aux_by_id:
        err(f"template parameter {entry.get('name')} derives from unknown auxiliary asset {derived}")

expected_named_tables = {
    "McpServerSessionTable469EA0FB": {
        "type": "AWS::DynamoDB::Table",
        "table_name_contains": "mcp-sessions",
    },
    "McpServerStreamTableC6A2DC7E": {
        "type": "AWS::DynamoDB::Table",
        "table_name_contains": "mcp-streams-v2",
    },
    "McpServerTaskTable72DDFBBB": {
        "type": "AWS::DynamoDB::Table",
        "table_name_contains": "mcp-tasks",
    },
}
expected_export_refs = {
    "McpSessionTableParam11A03692": {
        "name_contains": "mcp_session_table_name",
        "ref": "McpServerSessionTable469EA0FB",
    },
    "McpStreamTableParam604E9EFA": {
        "name_contains": "mcp_stream_table_name",
        "ref": "McpServerStreamTableC6A2DC7E",
    },
}
forbidden_legacy_resources = {
    "McpStreamTableEDC02B0A": "legacy stream table logical ID",
}
expected_spill_env = {
    "MCP_STREAM_SPILL_BUCKET",
    "MCP_STREAM_SPILL_PREFIX",
    "MCP_STREAM_SPILL_INLINE_MAX_BYTES",
    "MCP_STREAM_MAX_EVENT_BYTES",
}
expected_task_env = {
    "MCP_TASK_TABLE": {"Ref": "McpServerTaskTable72DDFBBB"},
    "MCP_TASK_TTL_MINUTES": "10",
}

for stage in ("dev", "staging", "live"):
    stage_meta = release["artifacts"]["deploy_templates"][stage]
    stage_path = root / stage_meta["path"]
    actual_sha, actual_bytes = digest(stage_meta["path"])
    if stage_meta.get("sha256") != actual_sha:
        err(f"{stage_path.name}: release template checksum mismatch")
    if stage_meta.get("bytes") != actual_bytes:
        err(f"{stage_path.name}: release template byte size mismatch")
    if deploy["templates"][stage].get("sha256") != stage_meta.get("sha256"):
        err(f"{stage_path.name}: deploy template checksum must match release manifest")

    template = json.loads(stage_path.read_text())
    template_text = json.dumps(template, sort_keys=True)
    for marker in ("cdk-hnb659fds", "/cdk-bootstrap/", "BootstrapVersion", "aws:asset:path", "../dist/lesser-body.zip"):
        if marker in template_text:
            err(f"{stage_path.name}: managed template contains forbidden bootstrap/source marker {marker}")

    required_params = set(expected_template_parameters)
    for asset in deploy_auxiliary_assets:
        if not isinstance(asset, dict):
            continue
        for ref in asset.get("template_references") or []:
            if not isinstance(ref, dict):
                continue
            if ref.get("stage") == stage and ref.get("template") == stage_path.name:
                required_params.add(asset.get("template_parameter"))
    params = template.get("Parameters", {})
    if not isinstance(params, dict):
        err(f"{stage_path.name}: template is missing Parameters")
        params = {}
    missing_params = sorted(p for p in required_params if p not in params)
    if missing_params:
        err(f"{stage_path.name}: missing required Parameters {missing_params}")

    for param_name, param_spec in params.items():
        if isinstance(param_spec, dict) and "Default" in param_spec and not isinstance(param_spec["Default"], str):
            err(f"{stage_path.name}: Parameters.{param_name}.Default must be a string, got {type(param_spec['Default']).__name__}")

    resources = template.get("Resources", {})
    if not isinstance(resources, dict):
        err(f"{stage_path.name}: template is missing Resources")
        continue

    for logical_id, label in forbidden_legacy_resources.items():
        if logical_id in resources:
            err(f"{stage_path.name}: forbidden {label} {logical_id} is present")

    for logical_id, spec in expected_named_tables.items():
        resource = resources.get(logical_id)
        if not isinstance(resource, dict):
            err(f"{stage_path.name}: missing expected resource {logical_id}")
            continue
        if resource.get("Type") != spec["type"]:
            err(f"{stage_path.name}: {logical_id} expected type {spec['type']}, got {resource.get('Type')!r}")
            continue
        props = resource.get("Properties", {})
        table_name_text = stable_json(props.get("TableName"))
        if spec["table_name_contains"] not in table_name_text:
            err(f"{stage_path.name}: {logical_id} TableName must contain {spec['table_name_contains']}, got {table_name_text}")

    for logical_id, spec in expected_export_refs.items():
        resource = resources.get(logical_id)
        if not isinstance(resource, dict):
            err(f"{stage_path.name}: missing expected resource {logical_id}")
            continue
        if resource.get("Type") != "AWS::SSM::Parameter":
            err(f"{stage_path.name}: {logical_id} expected type AWS::SSM::Parameter, got {resource.get('Type')!r}")
            continue
        props = resource.get("Properties", {})
        name_text = stable_json(props.get("Name"))
        if spec["name_contains"] not in name_text:
            err(f"{stage_path.name}: {logical_id} Name must contain {spec['name_contains']}, got {name_text}")
        expected_ref = {"Ref": spec["ref"]}
        if props.get("Value") != expected_ref:
            err(f"{stage_path.name}: {logical_id} Value must equal {expected_ref}, got {props.get('Value')!r}")

    spill_buckets = []
    for logical_id, resource in resources.items():
        if isinstance(resource, dict) and resource.get("Type") == "AWS::S3::Bucket":
            props = resource.get("Properties", {})
            if isinstance(props, dict) and "LifecycleConfiguration" in props:
                spill_buckets.append((logical_id, props))
    if len(spill_buckets) != 1:
        err(f"{stage_path.name}: expected exactly one stream-spill bucket with lifecycle configuration, found {len(spill_buckets)}")
        spill_logical_id = None
    else:
        spill_logical_id, spill_props = spill_buckets[0]
        public_block = spill_props.get("PublicAccessBlockConfiguration")
        expected_public_block = {"BlockPublicAcls", "BlockPublicPolicy", "IgnorePublicAcls", "RestrictPublicBuckets"}
        if not isinstance(public_block, dict) or not all(public_block.get(flag) is True for flag in expected_public_block):
            err(f"{stage_path.name}: {spill_logical_id} must block public access, got {stable_json(public_block)}")
        encryption = stable_json(spill_props.get("BucketEncryption"))
        if "AES256" not in encryption:
            err(f"{stage_path.name}: {spill_logical_id} must use S3-managed encryption, got {encryption}")
        lifecycle = stable_json(spill_props.get("LifecycleConfiguration"))
        if "ExpirationInDays" not in lifecycle:
            err(f"{stage_path.name}: {spill_logical_id} must configure lifecycle expiration, got {lifecycle}")

    handlers = []
    for logical_id, resource in resources.items():
        if isinstance(resource, dict) and resource.get("Type") == "AWS::Lambda::Function":
            props = resource.get("Properties", {})
            if isinstance(props, dict) and props.get("Handler") == "bootstrap":
                handlers.append((logical_id, props))
    if len(handlers) != 1:
        err(f"{stage_path.name}: expected exactly one MCP handler Lambda with Handler=bootstrap, found {len(handlers)}")
    else:
        handler_logical_id, handler_props = handlers[0]
        env = handler_props.get("Environment", {}).get("Variables", {})
        if not isinstance(env, dict):
            err(f"{stage_path.name}: {handler_logical_id} missing Environment.Variables")
        else:
            missing_env = sorted(expected_spill_env.difference(env))
            if missing_env:
                err(f"{stage_path.name}: MCP handler missing stream-spill env vars {missing_env}")
            elif spill_logical_id is not None and env.get("MCP_STREAM_SPILL_BUCKET") != {"Ref": spill_logical_id}:
                err(f"{stage_path.name}: MCP_STREAM_SPILL_BUCKET must Ref {spill_logical_id}, got {env.get('MCP_STREAM_SPILL_BUCKET')!r}")
            for key, expected_value in expected_task_env.items():
                if env.get(key) != expected_value:
                    err(f"{stage_path.name}: {key} must equal {expected_value!r}, got {env.get(key)!r}")

    declared_aux_params = set(aux_by_param)
    for logical_id, resource in resources.items():
        if not isinstance(resource, dict) or resource.get("Type") != "AWS::Lambda::Function":
            continue
        code = value_at_path(resource, "Properties.Code")
        if not isinstance(code, dict) or ("S3Bucket" not in code and "S3Key" not in code):
            continue
        bucket_ref = ref_name(code.get("S3Bucket"))
        key_ref = ref_name(code.get("S3Key"))
        if bucket_ref != "LesserBodyCodeBucketName":
            err(f"{stage_path.name}: lambda {logical_id} Code.S3Bucket must Ref LesserBodyCodeBucketName; literal, Fn::Sub, and CDK bootstrap buckets are not allowed")
            continue
        if not key_ref:
            err(f"{stage_path.name}: lambda {logical_id} Code.S3Key must Ref LesserBodyCodeObjectKey or a declared auxiliary asset parameter")
            continue
        if key_ref == "LesserBodyCodeObjectKey":
            continue
        if key_ref not in declared_aux_params:
            err(f"{stage_path.name}: references auxiliary code key parameter {key_ref} from {logical_id} without a declared auxiliary asset")

    for asset in deploy_auxiliary_assets:
        if not isinstance(asset, dict):
            continue
        for ref in asset.get("template_references") or []:
            if not isinstance(ref, dict):
                err(f"auxiliary asset {asset.get('id')} template_references entries must be objects")
                continue
            if ref.get("stage") != stage or ref.get("template") != stage_path.name:
                continue
            logical_id = ref.get("logical_id")
            resource = resources.get(logical_id)
            if not isinstance(resource, dict):
                err(f"{stage_path.name}: missing auxiliary asset {asset.get('id')} logical resource {logical_id}")
                continue
            value = value_at_path(resource, ref.get("property_path", ""))
            if ref_name(value) != ref.get("key_parameter"):
                err(f"{stage_path.name}: property {ref.get('property_path')} for auxiliary asset {asset.get('id')} must Ref {ref.get('key_parameter')}")
            bucket_value = sibling_at_path(resource, ref.get("property_path", ""), "S3Bucket")
            if ref.get("bucket_parameter") and ref_name(bucket_value) != ref.get("bucket_parameter"):
                err(f"{stage_path.name}: bucket property for auxiliary asset {asset.get('id')} must Ref {ref.get('bucket_parameter')}")

if errors:
    raise SystemExit("\n".join(errors))
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

DRY_RUN_LOG_WITH_INSTANCE_KEY_ARN="${RELEASE_ABS_DIR}/deploy-dry-run-dev-with-instance-key-arn.log"
(
  cd "${OUT_DIR}"
  LESSER_HOST_INSTANCE_KEY_ARN="arn:aws:secretsmanager:us-east-1:123456789012:secret:lesser-host/lab/instances/lesser/instance-key-example" \
    bash ./deploy-lesser-body-from-release.sh \
      --dry-run \
      --stack-name lesser-dev-lesser-body \
      --asset-bucket example-artifacts-bucket \
      --app lesser \
      --stage dev \
      --base-domain example.com > "${DRY_RUN_LOG_WITH_INSTANCE_KEY_ARN}"
)

DRY_RUN_LOG_WITH_SOUL_BINDING_BEARER_ARN="${RELEASE_ABS_DIR}/deploy-dry-run-dev-with-soul-binding-bearer-arn.log"
(
  cd "${OUT_DIR}"
  LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN="arn:aws:secretsmanager:us-east-1:123456789012:secret:lesser/soul-binding-integration-bearer-example" \
    bash ./deploy-lesser-body-from-release.sh \
      --dry-run \
      --stack-name lesser-dev-lesser-body \
      --asset-bucket example-artifacts-bucket \
      --app lesser \
      --stage dev \
      --base-domain example.com > "${DRY_RUN_LOG_WITH_SOUL_BINDING_BEARER_ARN}"
)

all_dry_run_logs=(
  "${DRY_RUN_LOG_DEV}"
  "${DRY_RUN_LOG_STAGING}"
  "${DRY_RUN_LOG_LIVE}"
  "${DRY_RUN_LOG_NO_EXECUTE_CHANGESET}"
  "${DRY_RUN_LOG_WITH_INSTANCE_KEY_ARN}"
  "${DRY_RUN_LOG_WITH_SOUL_BINDING_BEARER_ARN}"
)

expected_s3_prefix="releases/lesser-body/${VERSION}/templates"
expected_parameter_overrides=(
  'JWTSecretArnParamPath=/lesser/shared/secrets/jwt-secret-arn'
  'JWTSecretKeyArnParamPath=/lesser/shared/kms/encryption-key-arn'
)
mapfile -t auxiliary_asset_rows < <(python3 - "${OUT_DIR}/lesser-body-deploy.json" <<'PY'
import json
import pathlib
import sys
manifest = json.loads(pathlib.Path(sys.argv[1]).read_text())
for asset in manifest.get("auxiliary_assets") or []:
    print("\t".join([asset["path"], asset["s3_key"], asset["template_parameter"]]))
PY
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

  for row in "${auxiliary_asset_rows[@]}"; do
    IFS=$'\t' read -r aux_path aux_s3_key aux_param <<< "${row}"
    aux_object_key="releases/lesser-body/${VERSION}/${aux_s3_key}"
    if ! grep -q -- "${RELEASE_ABS_DIR}/${aux_path}" "${stage_log}"; then
      echo "dry-run output did not stage auxiliary asset from release directory (stage: ${stage}): ${aux_path}" >&2
      exit 1
    fi
    if ! grep -q -- "s3://example-artifacts-bucket/${aux_object_key}" "${stage_log}"; then
      echo "dry-run output did not upload auxiliary asset to expected object key (stage: ${stage}): ${aux_object_key}" >&2
      exit 1
    fi
    if ! grep -q -- "${aux_param}=${aux_object_key}" "${stage_log}"; then
      echo "dry-run output did not pass auxiliary parameter override (stage: ${stage}): ${aux_param}=${aux_object_key}" >&2
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

if ! grep -q -- 'LesserHostInstanceKeyARN=arn:aws:secretsmanager:us-east-1:123456789012:secret:lesser-host/lab/instances/lesser/instance-key-example' "${DRY_RUN_LOG_WITH_INSTANCE_KEY_ARN}"; then
  echo "dry-run output did not forward LESSER_HOST_INSTANCE_KEY_ARN into the managed deploy parameter overrides" >&2
  exit 1
fi
if ! grep -q -- 'LesserSoulBindingIntegrationBearerSecretARN=arn:aws:secretsmanager:us-east-1:123456789012:secret:lesser/soul-binding-integration-bearer-example' "${DRY_RUN_LOG_WITH_SOUL_BINDING_BEARER_ARN}"; then
  echo "dry-run output did not forward LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN into the managed deploy parameter overrides" >&2
  exit 1
fi

for log in "${all_dry_run_logs[@]}"; do
  if grep -Eq 'scripts/build\.sh|cmd/release-template|/cdk/' "${log}"; then
    echo "dry-run output leaked source-checkout build paths: ${log}" >&2
    exit 1
  fi
done

echo "Verified release assets in ${OUT_DIR}"
