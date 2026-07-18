#!/usr/bin/env python3
"""Managed lesser-body release asset helpers.

This script is intentionally stdlib-only. It is used by the release builder,
verifier, and regression harnesses so schema-2 auxiliary asset handling stays
consistent across producer-side checks.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import shutil
import stat
import sys
import zipfile
from pathlib import Path
from typing import Any

STAGES = ("dev", "staging", "live")
BASE_RELEASE_ASSETS = [
    "lesser-body.zip",
    "lesser-body-deploy.json",
    "lesser-body-managed-dev.template.json",
    "lesser-body-managed-staging.template.json",
    "lesser-body-managed-live.template.json",
    "deploy-lesser-body-from-release.sh",
    "checksums.txt",
    "lesser-body-release.json",
]
AUXILIARY_CAPABILITY = "managed_auxiliary_assets_v1"
PRIMARY_BUCKET_PARAMETER = "LesserBodyCodeBucketName"
PRIMARY_KEY_PARAMETER = "LesserBodyCodeObjectKey"
APP_THEORY_AUTO_DELETE_ID = "apptheory-s3-auto-delete-objects-provider"
APP_THEORY_AUTO_DELETE_PARAMETER = "AppTheoryAutoDeleteObjectsCodeObjectKey"


def fail(message: str) -> None:
    raise SystemExit(message)


def read_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text())
    except FileNotFoundError:
        fail(f"missing JSON file: {path}")
    except json.JSONDecodeError as exc:
        fail(f"invalid JSON in {path}: {exc}")
    if not isinstance(value, dict):
        fail(f"{path} must contain a JSON object")
    return value


def write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=False) + "\n")


def sha256_of(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def bytes_of(path: Path) -> int:
    return path.stat().st_size


def digest(path: Path) -> tuple[str, int]:
    return sha256_of(path), bytes_of(path)


def safe_asset_path(value: str, label: str) -> str:
    value = str(value or "").strip()
    if not value:
        fail(f"{label} is required")
    if value.startswith("/") or "\\" in value or "\n" in value or "\r" in value:
        fail(f"{label} must be a safe relative asset path: {value!r}")
    for part in value.split("/"):
        if part in ("", ".", ".."):
            fail(f"{label} must not contain empty/current/parent path segments: {value!r}")
    return value


def sanitize_identifier(value: str) -> str:
    words = re.findall(r"[A-Za-z0-9]+", value)
    if not words:
        return "CdkFileAsset"
    out = "".join(word[:1].upper() + word[1:] for word in words)
    if out[0].isdigit():
        out = "CdkFileAsset" + out
    return out


def asset_identity(asset_hash: str, display_name: str) -> tuple[str, str, str]:
    if "S3AutoDeleteObjectsCustomResourceProvider" in display_name:
        return (
            APP_THEORY_AUTO_DELETE_ID,
            APP_THEORY_AUTO_DELETE_PARAMETER,
            f"{APP_THEORY_AUTO_DELETE_ID}-{asset_hash}.zip",
        )

    short = asset_hash[:16]
    ident = f"cdk-file-asset-{short}"
    parameter = f"CdkFileAsset{sanitize_identifier(short)}ObjectKey"
    return ident, parameter, f"{ident}.zip"


def zip_directory(source_dir: Path, dest: Path) -> None:
    if not source_dir.is_dir():
        fail(f"zip source is not a directory: {source_dir}")
    dest.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(dest, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        files = sorted(path for path in source_dir.rglob("*") if path.is_file())
        if not files:
            fail(f"zip source contains no files: {source_dir}")
        for path in files:
            rel = path.relative_to(source_dir).as_posix()
            info = zipfile.ZipInfo(rel)
            info.date_time = (1980, 1, 1, 0, 0, 0)
            mode = stat.S_IMODE(path.stat().st_mode) or 0o644
            info.external_attr = (stat.S_IFREG | mode) << 16
            zf.writestr(info, path.read_bytes())


def package_file_asset(stage_outdir: Path, asset: dict[str, Any], dest: Path) -> None:
    source = asset.get("source")
    if not isinstance(source, dict):
        fail("CDK asset is missing source metadata")
    source_path_value = source.get("path")
    if not isinstance(source_path_value, str) or not source_path_value.strip():
        fail("CDK asset source.path is required")
    source_path = stage_outdir / source_path_value
    packaging = str(source.get("packaging") or "file").strip()
    if packaging == "zip":
        zip_directory(source_path, dest)
    elif packaging == "file":
        if not source_path.is_file():
            fail(f"file asset source is not a file: {source_path}")
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source_path, dest)
    else:
        fail(f"unsupported CDK asset packaging {packaging!r} for {source_path_value}")


def destination_object_key(asset: dict[str, Any]) -> str:
    destinations = asset.get("destinations")
    if not isinstance(destinations, dict) or not destinations:
        fail("CDK file asset is missing destinations")
    for destination in destinations.values():
        if isinstance(destination, dict) and isinstance(destination.get("objectKey"), str):
            return destination["objectKey"].strip()
    fail("CDK file asset destination is missing objectKey")


def is_template_asset(asset: dict[str, Any], template_source_name: str) -> bool:
    source = asset.get("source")
    if not isinstance(source, dict):
        return False
    source_path = str(source.get("path") or "").strip()
    return source_path == template_source_name or source_path.endswith(".template.json")


def lambda_code_matches_object_key(template: dict[str, Any], object_key: str) -> list[tuple[str, dict[str, Any]]]:
    resources = template.get("Resources")
    if not isinstance(resources, dict):
        fail("managed template is missing Resources")
    matches: list[tuple[str, dict[str, Any]]] = []
    for logical_id, resource in resources.items():
        if not isinstance(resource, dict) or resource.get("Type") != "AWS::Lambda::Function":
            continue
        props = resource.get("Properties")
        if not isinstance(props, dict):
            continue
        code = props.get("Code")
        if not isinstance(code, dict):
            continue
        if code.get("S3Key") == object_key:
            matches.append((str(logical_id), code))
    return matches


def remove_bootstrap_artifacts(template: dict[str, Any]) -> None:
    params = template.get("Parameters")
    if isinstance(params, dict):
        params.pop("BootstrapVersion", None)
    rules = template.get("Rules")
    if isinstance(rules, dict):
        rules.pop("CheckBootstrapVersion", None)
        if not rules:
            template.pop("Rules", None)


def assert_no_bootstrap_refs(template: dict[str, Any], template_name: str) -> None:
    text = json.dumps(template, sort_keys=True)
    forbidden = ["cdk-hnb659fds", "/cdk-bootstrap/", "BootstrapVersion", "aws:asset:path"]
    for marker in forbidden:
        if marker in text:
            fail(f"{template_name}: managed template still contains bootstrap/CDK asset marker {marker!r}")


def process_stage(stage: str, stage_outdir: Path, out_dir: Path, aux_by_id: dict[str, dict[str, Any]]) -> None:
    template_sources = sorted(stage_outdir.glob("*.template.json"))
    if len(template_sources) != 1:
        fail(f"expected exactly one template for stage {stage}, found {len(template_sources)}")
    template_source = template_sources[0]
    template = read_json(template_source)

    assets_paths = sorted(stage_outdir.glob("*.assets.json"))
    if len(assets_paths) != 1:
        fail(f"expected exactly one CDK assets manifest for stage {stage}, found {len(assets_paths)}")
    assets_doc = read_json(assets_paths[0])
    file_assets = assets_doc.get("files")
    if not isinstance(file_assets, dict):
        fail(f"{assets_paths[0]} is missing files")

    template_name = f"lesser-body-managed-{stage}.template.json"
    parameters = template.setdefault("Parameters", {})
    if not isinstance(parameters, dict):
        fail(f"{template_name}: Parameters must be an object")

    for asset_hash, asset in sorted(file_assets.items()):
        if not isinstance(asset, dict):
            continue
        if is_template_asset(asset, template_source.name):
            continue
        object_key = destination_object_key(asset)
        matches = lambda_code_matches_object_key(template, object_key)
        if not matches:
            display_name = str(asset.get("displayName") or asset_hash)
            fail(f"{template_name}: CDK file asset {display_name!r} object key {object_key!r} is not referenced by Lambda Code")

        display_name = str(asset.get("displayName") or asset_hash)
        aux_id, parameter_name, release_path = asset_identity(str(asset_hash), display_name)
        release_path = safe_asset_path(release_path, f"auxiliary asset {aux_id} path")
        dest = out_dir / release_path
        if aux_id not in aux_by_id:
            package_file_asset(stage_outdir, asset, dest)
            sha, size = digest(dest)
            aux_by_id[aux_id] = {
                "id": aux_id,
                "required": True,
                "path": release_path,
                "sha256": sha,
                "bytes": size,
                "content_type": "application/zip" if release_path.endswith(".zip") else "application/octet-stream",
                "s3_key": release_path,
                "template_parameter": parameter_name,
                "source": {
                    "kind": "cdk-file-asset",
                    "source_hash": str(asset_hash),
                    "construct_path": display_name,
                },
                "template_references": [],
            }
        else:
            existing = aux_by_id[aux_id]
            if existing["template_parameter"] != parameter_name or existing["path"] != release_path:
                fail(f"auxiliary asset {aux_id} has inconsistent metadata across stages")

        parameters[parameter_name] = {
            "Type": "String",
            "Description": f"S3 object key for release-managed auxiliary asset {aux_id}.",
        }

        for logical_id, code in matches:
            code["S3Bucket"] = {"Ref": PRIMARY_BUCKET_PARAMETER}
            code["S3Key"] = {"Ref": parameter_name}
            ref = {
                "stage": stage,
                "template": template_name,
                "logical_id": logical_id,
                "property_path": "Properties.Code.S3Key",
                "bucket_parameter": PRIMARY_BUCKET_PARAMETER,
                "key_parameter": parameter_name,
            }
            refs = aux_by_id[aux_id]["template_references"]
            if ref not in refs:
                refs.append(ref)

    remove_bootstrap_artifacts(template)
    assert_no_bootstrap_refs(template, template_name)
    write_json(out_dir / template_name, template)


def template_meta(out_dir: Path, stage: str) -> dict[str, Any]:
    path = f"lesser-body-managed-{stage}.template.json"
    p = out_dir / path
    sha, size = digest(p)
    return {"path": path, "sha256": sha, "bytes": size, "format": "cloudformation-json"}


def base_script_inputs(version: str) -> list[dict[str, Any]]:
    return [
        {"name": "stack_name", "required": True, "description": "CloudFormation stack name to create or update."},
        {"name": "asset_bucket", "required": True, "description": "S3 bucket in the target account used to stage lesser-body.zip, auxiliary assets, and CloudFormation templates."},
        {"name": "stage", "required": True, "allowed_values": list(STAGES), "description": "Target Lesser stage used to select the matching release-produced CloudFormation template."},
        {"name": "app", "required": False, "default": "lesser", "description": "Lesser app slug used in resource naming and SSM paths."},
        {"name": "base_domain", "required": False, "description": "Optional base domain override. When omitted, the template reads /<app>/<stage>/lesser/exports/v1/domain from SSM."},
        {"name": "lesser_host_instance_key_arn", "required": False, "description": "Optional exact Secrets Manager ARN for the managed lesser-host instance key. When supplied, lesser-body injects LESSER_HOST_INSTANCE_KEY_ARN and also grants direct read access to that secret."},
        {"name": "soul_binding_integration_bearer_secret_arn", "required": False, "description": "Optional exact Secrets Manager ARN for the dedicated Body/Ptah to Lesser soul-binding bearer. When supplied, lesser-body injects LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN on the instance MCP Lambda and grants direct read access to that secret."},
        {"name": "asset_prefix", "required": False, "default": f"releases/lesser-body/{version}", "description": "Optional S3 key prefix used when staging lesser-body.zip and auxiliary assets."},
        {"name": "no_execute_changeset", "required": False, "default": False, "description": "Optional helper flag that passes --no-execute-changeset to aws cloudformation deploy for verification-only runs."},
    ]


def base_template_parameters(auxiliary_assets: list[dict[str, Any]]) -> list[dict[str, Any]]:
    params: list[dict[str, Any]] = [
        {"name": "AppName", "required": False, "default": "lesser"},
        {"name": "BaseDomain", "required": False, "default": ""},
        {"name": PRIMARY_BUCKET_PARAMETER, "required": True},
        {"name": PRIMARY_KEY_PARAMETER, "required": True},
        {"name": "LesserHostInstanceKeyARN", "required": False, "default": "", "description": "Optional exact Secrets Manager ARN for the managed lesser-host instance key. The release helper forwards --lesser-host-instance-key-arn (or $LESSER_HOST_INSTANCE_KEY_ARN) into this parameter."},
        {"name": "LesserSoulBindingIntegrationBearerSecretARN", "required": False, "default": "", "description": "Optional exact Secrets Manager ARN for the dedicated Body/Ptah to Lesser soul-binding bearer. The release helper forwards --soul-binding-integration-bearer-secret-arn (or $LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN) into this parameter."},
        {"name": "JWTSecretArnParamPath", "required": False, "default": "/<app>/shared/secrets/jwt-secret-arn", "description": "SSM parameter path for the shared JWT secret ARN. The release helper derives this from the target app."},
        {"name": "JWTSecretKeyArnParamPath", "required": False, "default": "/<app>/shared/kms/encryption-key-arn", "description": "SSM parameter path for the shared KMS key ARN. The release helper derives this from the target app."},
        {"name": "LesserStageDomainParamPath", "required": False, "default": "/<app>/<stage>/lesser/exports/v1/domain", "description": "SSM parameter path for the Lesser stage domain. The stage-specific template and release helper align on this path."},
        {"name": "LesserTableNameParamPath", "required": False, "default": "/<app>/<stage>/lesser/exports/v1/table_name", "description": "SSM parameter path for the Lesser table name. The release helper derives this from the target app and stage."},
    ]
    for asset in auxiliary_assets:
        params.append({
            "name": asset["template_parameter"],
            "required": True,
            "derived_from_auxiliary_asset": asset["id"],
            "description": f"S3 object key for release-managed auxiliary asset {asset['id']}.",
        })
    return params


def exports_contract() -> list[dict[str, str]]:
    return [
        {"name": "mcp_lambda_arn", "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_lambda_arn"},
        {"name": "mcp_endpoint_url", "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url"},
        {"name": "mcp_session_table_name", "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_session_table_name"},
        {"name": "mcp_stream_table_name", "path": "/<app>/<stage>/lesser-body/exports/v1/mcp_stream_table_name"},
    ]


def build_from_assemblies(args: argparse.Namespace) -> None:
    out_dir = Path(args.out_dir)
    assembly_dir = Path(args.assembly_dir)
    version = args.version

    aux_by_id: dict[str, dict[str, Any]] = {}
    for stage in STAGES:
        process_stage(stage, assembly_dir / stage, out_dir, aux_by_id)

    auxiliary_assets = [aux_by_id[key] for key in sorted(aux_by_id)]
    schema = 2 if auxiliary_assets else 1
    templates = {stage: template_meta(out_dir, stage) for stage in STAGES}
    zip_sha, zip_bytes = digest(out_dir / "lesser-body.zip")
    script_sha, script_bytes = digest(out_dir / "deploy-lesser-body-from-release.sh")

    deploy: dict[str, Any] = {
        "schema": schema,
        "name": "lesser-body-deploy",
        "version": version,
    }
    if schema == 2:
        deploy["required_capabilities"] = [AUXILIARY_CAPABILITY]
        deploy["asset_prefix_default"] = f"releases/lesser-body/{version}"
    deploy.update({
        "lambda": {"path": "lesser-body.zip", "sha256": zip_sha, "bytes": zip_bytes},
        "templates": templates,
    })
    if schema == 2:
        deploy["auxiliary_assets"] = auxiliary_assets
    deploy.update({
        "script": {"path": "deploy-lesser-body-from-release.sh", "sha256": script_sha, "bytes": script_bytes},
        "deploy_input_schema": schema,
        "source_checkout_required": False,
        "npm_install_required": False,
        "script_inputs": base_script_inputs(version),
        "template_parameters": base_template_parameters(auxiliary_assets),
        "exports": exports_contract(),
    })
    deploy_path = out_dir / "lesser-body-deploy.json"
    write_json(deploy_path, deploy)
    deploy_sha, deploy_bytes = digest(deploy_path)

    release: dict[str, Any] = {
        "schema": schema,
        "name": "lesser-body",
        "version": version,
        "git_sha": args.git_sha,
        "go_version": args.go_version,
        "mcp": {"protocol_version": args.mcp_protocol_version},
        "artifacts": {
            "checksums": {"path": "checksums.txt", "algorithm": "sha256"},
            "lambda_zip": {"path": "lesser-body.zip", "sha256": zip_sha, "bytes": zip_bytes},
            "deploy_manifest": {"path": "lesser-body-deploy.json", "sha256": deploy_sha, "bytes": deploy_bytes, "schema": schema},
            "deploy_templates": templates,
            "deploy_script": {"path": "deploy-lesser-body-from-release.sh", "sha256": script_sha, "bytes": script_bytes},
        },
        "deploy": {
            "schema": schema,
            "manifest_path": "lesser-body-deploy.json",
            "template_selection": "by_stage",
            "source_checkout_required": False,
            "npm_install_required": False,
        },
    }
    if schema == 2:
        release["artifacts"]["auxiliary_assets"] = auxiliary_assets
        release["deploy"]["required_capabilities"] = [AUXILIARY_CAPABILITY]
    write_json(out_dir / "lesser-body-release.json", release)
    write_checksums(out_dir)


def list_assets(release_dir: Path | None = None) -> list[str]:
    assets = list(BASE_RELEASE_ASSETS)
    if release_dir is not None and (release_dir / "lesser-body-deploy.json").is_file():
        deploy = read_json(release_dir / "lesser-body-deploy.json")
        for asset in deploy.get("auxiliary_assets") or []:
            if not isinstance(asset, dict):
                continue
            path = safe_asset_path(str(asset.get("path") or ""), "auxiliary asset path")
            if path not in assets:
                assets.append(path)
    return assets


def write_checksums(release_dir: Path) -> None:
    lines = []
    for asset in list_assets(release_dir):
        if asset == "checksums.txt":
            continue
        path = release_dir / asset
        if not path.is_file():
            fail(f"missing release asset for checksums: {asset}")
        lines.append(f"{sha256_of(path)}  {asset}\n")
    (release_dir / "checksums.txt").write_text("".join(lines))


def update_artifact_meta(meta: dict[str, Any], release_dir: Path) -> None:
    path = safe_asset_path(str(meta.get("path") or ""), "artifact path")
    file_path = release_dir / path
    if not file_path.is_file():
        fail(f"missing artifact for metadata refresh: {path}")
    sha, size = digest(file_path)
    meta["sha256"] = sha
    meta["bytes"] = size


def refresh_metadata(args: argparse.Namespace) -> None:
    release_dir = Path(args.release_dir)
    deploy_path = release_dir / "lesser-body-deploy.json"
    release_path = release_dir / "lesser-body-release.json"
    deploy = read_json(deploy_path)
    release = read_json(release_path)

    update_artifact_meta(deploy["lambda"], release_dir)
    update_artifact_meta(deploy["script"], release_dir)
    for stage in STAGES:
        update_artifact_meta(deploy["templates"][stage], release_dir)
    for asset in deploy.get("auxiliary_assets") or []:
        update_artifact_meta(asset, release_dir)

    write_json(deploy_path, deploy)

    release["schema"] = deploy.get("schema", release.get("schema", 1))
    release["artifacts"]["lambda_zip"].update({k: deploy["lambda"][k] for k in ("path", "sha256", "bytes")})
    release["artifacts"]["deploy_script"].update({k: deploy["script"][k] for k in ("path", "sha256", "bytes")})
    for stage in STAGES:
        release["artifacts"]["deploy_templates"][stage].update(deploy["templates"][stage])
    if deploy.get("schema") == 2:
        release["artifacts"]["auxiliary_assets"] = deploy.get("auxiliary_assets") or []
        release["deploy"]["required_capabilities"] = [AUXILIARY_CAPABILITY]
    else:
        release["artifacts"].pop("auxiliary_assets", None)
        release["deploy"].pop("required_capabilities", None)
    deploy_sha, deploy_bytes = digest(deploy_path)
    release["artifacts"]["deploy_manifest"].update({"path": "lesser-body-deploy.json", "sha256": deploy_sha, "bytes": deploy_bytes, "schema": deploy.get("schema", 1)})
    release["deploy"]["schema"] = deploy.get("schema", 1)
    write_json(release_path, release)
    write_checksums(release_dir)


def main() -> None:
    parser = argparse.ArgumentParser(description="lesser-body managed release helper")
    sub = parser.add_subparsers(dest="command", required=True)

    build = sub.add_parser("build-from-assemblies")
    build.add_argument("--version", required=True)
    build.add_argument("--out-dir", required=True)
    build.add_argument("--assembly-dir", required=True)
    build.add_argument("--git-sha", required=True)
    build.add_argument("--go-version", required=True)
    build.add_argument("--mcp-protocol-version", required=True)

    list_cmd = sub.add_parser("list-assets")
    list_cmd.add_argument("release_dir", nargs="?")

    refresh = sub.add_parser("refresh-metadata")
    refresh.add_argument("release_dir")

    args = parser.parse_args()
    if args.command == "build-from-assemblies":
        build_from_assemblies(args)
    elif args.command == "list-assets":
        release_dir = Path(args.release_dir) if args.release_dir else None
        for asset in list_assets(release_dir):
            print(asset)
    elif args.command == "refresh-metadata":
        refresh_metadata(args)
    else:
        parser.error(f"unknown command {args.command}")


if __name__ == "__main__":
    main()
