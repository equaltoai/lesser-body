#!/usr/bin/env python3
"""Canary Lesser Body Ba instance-plane local-install flow.

Required environment:
  BA_MCP_ENDPOINT or MCP_ENDPOINT
      Ba instance MCP endpoint, for example https://api.dev.example.com/instance/ba/mcp.
  BA_MCP_BEARER_TOKEN or MCP_BEARER_TOKEN
      Account-holder Lesser OAuth access token for the Ba instance resource. Alternatively set
      BA_MCP_AUTHORIZATION or MCP_AUTHORIZATION to a complete "Bearer ..." header value.
  BA_AGENT_ID
      Account-scoped agent id whose current agent_soul and agent_instructions records should be packaged.
  BA_CANARY_CONFIRM_INSTALL_PLAN=true
      Required because agent_local_install_plan mints a one-time installer download grant.
  BA_CANARY_CONFIRM_DOWNLOAD=true
      Required because the canary consumes that grant and verifies the second GET returns 410 Gone.

Optional environment:
  BA_PROTECTED_RESOURCE_METADATA_URL  Override the derived RFC 9728 metadata URL.
  BA_ACTOR_USERNAME                   Explicit account-holder username; must match the token principal.
  BA_INSTALL_CLIENT                   claude_code or codex (default: codex).
  BA_INSTALL_PROFILE                  Optional profile alias; when supplied it must match BA_INSTALL_CLIENT.

The canary consumes the published RFC 9728 protected-resource metadata and AppTheory MCP tools/list surface, then runs
agent_local_install_plan -> header-free ZIP download -> checksum/manifest/entry verification -> safe temporary extract
summary -> second header-free GET expecting HTTP 410. It refuses redirects, never sends Authorization to the grant URL,
never prints raw download URLs, grant tokens, archives, full soul/instructions content, raw RPC payloads, or upstream
error bodies, and emits only bounded statuses, sizes, hashes, and opaque ids.
"""

from __future__ import annotations

import hashlib
import io
import json
import os
import pathlib
import re
import shutil
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from typing import Any


class CanaryError(RuntimeError):
    pass


class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Refuse redirects so Authorization and grant-token URLs never leave configured endpoints."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


NO_REDIRECT_OPENER = urllib.request.build_opener(NoRedirectHandler)
SAFE_IDENTIFIER_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%_-]{0,255}$")
MANIFEST_NAME = "MANIFEST.json"
PACK_SCHEMA = "lesserbody.agent_local_install_pack.v1"
# internal/installpack.MCPServerName: lesser_ka[_<environment>]_<actor>.
MCP_SERVER_NAME_RE = re.compile(r"^lesser_ka(?:_[a-z0-9]+)+$")


USAGE = __doc__ or ""


def env(*names: str, default: str = "") -> str:
    for name in names:
        value = os.environ.get(name, "").strip()
        if value:
            return value
    return default


def env_required(*names: str) -> str:
    value = env(*names)
    if not value:
        joined = " or ".join(names)
        raise CanaryError(f"{joined} is required")
    return value


def env_bool(name: str, *, default: bool = False) -> bool:
    raw = os.environ.get(name, "").strip().lower()
    if raw == "":
        return default
    if raw in {"1", "true", "yes", "y", "on"}:
        return True
    if raw in {"0", "false", "no", "n", "off"}:
        return False
    raise CanaryError(f"{name} must be true or false")


def sha12(value: str | bytes) -> str:
    if isinstance(value, str):
        value = value.encode("utf-8", errors="replace")
    return hashlib.sha256(value).hexdigest()[:12]


def sha256_prefixed(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def safe_identifier(value: Any) -> str:
    text = str(value or "").strip()
    if text and SAFE_IDENTIFIER_RE.match(text):
        return text
    if not text:
        return "<empty>"
    return f"<redacted len={len(text)} sha256_12={sha12(text)}>"


def redacted_payload_summary(value: Any) -> str:
    try:
        raw = json.dumps(value, sort_keys=True, separators=(",", ":"))
    except (TypeError, ValueError):
        raw = str(value)
    return f"len={len(raw)} sha256_12={sha12(raw)}"


def sanitized_error_payload(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict):
        return {"payload": redacted_payload_summary(value)}

    safe: dict[str, Any] = {}
    for key in ("code", "status"):
        if key not in value:
            continue
        item = value.get(key)
        if isinstance(item, str) and key == "code" and len(item) <= 80 and all(ch.isalnum() or ch in "._:-" for ch in item):
            safe[key] = item[:160]
        elif isinstance(item, (int, float, bool)) or item is None:
            safe[key] = item
        else:
            safe[key] = "<redacted>"
            safe[f"{key}_summary"] = redacted_payload_summary(item)
    if isinstance(value.get("message"), str):
        safe["message"] = "<redacted>"
        safe["message_summary"] = redacted_payload_summary(value["message"])
    details = value.get("details")
    if details is not None:
        safe["details_summary"] = redacted_payload_summary(details)
    if not safe:
        safe["payload"] = redacted_payload_summary(value)
    return safe


def log(message: str) -> None:
    print(message, flush=True)


def is_redirect_status(status: int) -> bool:
    return 300 <= int(status) <= 399


def canonical_url(raw: str) -> str:
    parsed = urllib.parse.urlparse(raw.strip())
    if parsed.scheme != "https" or not parsed.netloc:
        raise CanaryError("MCP endpoint must be an https URL")
    path = parsed.path.rstrip("/") or "/"
    return urllib.parse.urlunparse((parsed.scheme, parsed.netloc, path, "", "", ""))


def require_instance_surface(endpoint: str, surface: str) -> None:
    parsed = urllib.parse.urlparse(endpoint)
    if parsed.path.rstrip("/") != f"/instance/{surface}/mcp":
        raise CanaryError(f"endpoint path must be /instance/{surface}/mcp")


def protected_resource_metadata_url(endpoint: str, override: str, surface: str) -> str:
    if override:
        parsed = urllib.parse.urlparse(override)
        if parsed.scheme != "https" or not parsed.netloc:
            raise CanaryError("protected-resource metadata URL override must be https")
        return urllib.parse.urlunparse((parsed.scheme, parsed.netloc, parsed.path.rstrip("/") or "/", "", parsed.query, ""))
    parsed = urllib.parse.urlparse(endpoint)
    return urllib.parse.urlunparse(
        (
            parsed.scheme,
            parsed.netloc,
            f"/.well-known/oauth-protected-resource/instance/{surface}/mcp",
            "",
            "",
            "",
        )
    )


def open_no_redirect(req: urllib.request.Request, *, timeout: int):  # type: ignore[no-untyped-def]
    return NO_REDIRECT_OPENER.open(req, timeout=timeout)


def fetch_protected_resource_metadata(endpoint: str, metadata_url: str) -> dict[str, Any]:
    req = urllib.request.Request(metadata_url, headers={"Accept": "application/json"}, method="GET")
    try:
        with open_no_redirect(req, timeout=20) as resp:
            raw = resp.read()
            content_type = resp.headers.get("Content-Type", "")
    except urllib.error.HTTPError as exc:
        if is_redirect_status(exc.code):
            raise CanaryError(f"protected-resource metadata HTTP redirect {exc.code}: refusing redirect") from exc
        body = exc.read()
        raise CanaryError(f"protected-resource metadata HTTP {exc.code}: body_len={len(body)} body_sha256_12={sha12(body)}") from exc
    except urllib.error.URLError as exc:
        raise CanaryError(f"protected-resource metadata request failed: reason={safe_identifier(type(exc.reason).__name__)}") from exc

    if "json" not in content_type.lower():
        raise CanaryError(f"protected-resource metadata returned non-JSON: len={len(raw)} sha256_12={sha12(raw)}")
    try:
        data = json.loads(raw.decode("utf-8"))
    except json.JSONDecodeError as exc:
        raise CanaryError(f"protected-resource metadata JSON parse failed: len={len(raw)} sha256_12={sha12(raw)}") from exc
    if not isinstance(data, dict):
        raise CanaryError("protected-resource metadata was not an object")

    expected_resource = canonical_url(endpoint)
    resource = canonical_url(str(data.get("resource") or ""))
    if resource != expected_resource:
        raise CanaryError(
            "protected-resource metadata resource mismatch: "
            f"expected_sha256_12={sha12(expected_resource)} got_sha256_12={sha12(resource)}"
        )
    authorization_servers = data.get("authorization_servers")
    if not isinstance(authorization_servers, list) or not authorization_servers:
        raise CanaryError("protected-resource metadata missing authorization_servers")
    scopes = data.get("scopes_supported") if isinstance(data.get("scopes_supported"), list) else []
    if not {"read", "write"}.issubset({str(scope).strip() for scope in scopes}):
        raise CanaryError("protected-resource metadata missing read/write scopes")
    bearer_methods = data.get("bearer_methods_supported") if isinstance(data.get("bearer_methods_supported"), list) else []
    if bearer_methods and "header" not in {str(method).strip().lower() for method in bearer_methods}:
        raise CanaryError("protected-resource metadata did not advertise bearer header support")
    return data


def sse_data_events(raw: bytes) -> list[str]:
    text = raw.decode("utf-8", errors="replace")
    events: list[str] = []
    data_lines: list[str] = []
    for line in text.splitlines():
        if line == "":
            events.append("\n".join(data_lines))
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        if line.startswith("data:"):
            value = line[5:]
            if value.startswith(" "):
                value = value[1:]
            data_lines.append(value)
    if data_lines:
        events.append("\n".join(data_lines))
    return events


def decode_rpc_response(method: str, request_id: int, raw: bytes, content_type: str) -> dict[str, Any]:
    text = raw.decode("utf-8", errors="replace") if raw else ""
    is_sse = "text/event-stream" in content_type.lower() or text.lstrip().startswith(("event:", "data:", "id:"))
    if not is_sse:
        try:
            return json.loads(text) if raw else {}
        except json.JSONDecodeError as exc:
            raise CanaryError(f"{method} returned non-JSON body: len={len(raw)} sha256_12={sha12(raw)}") from exc

    parsed_events = 0
    for data in sse_data_events(raw):
        data = data.strip()
        if not data:
            continue
        parsed_events += 1
        try:
            parsed = json.loads(data)
        except json.JSONDecodeError:
            continue
        if not isinstance(parsed, dict):
            continue
        if parsed.get("jsonrpc") == "2.0" and parsed.get("id") == request_id and not parsed.get("method"):
            return parsed
    raise CanaryError(
        f"{method} returned SSE without a final JSON-RPC response for id {request_id}; "
        f"parsed_events={parsed_events}"
    )


class MCPClient:
    def __init__(self, endpoint: str, authorization: str) -> None:
        self.endpoint = endpoint
        self.authorization = authorization
        self.session_id = ""
        self.next_id = 1
        self.last_response_bytes = 0

    def post_rpc(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        request_id = self.next_id
        payload: dict[str, Any] = {"jsonrpc": "2.0", "id": request_id, "method": method}
        self.next_id += 1
        if params is not None:
            payload["params"] = params

        headers = {
            "Accept": "application/json, text/event-stream",
            "Content-Type": "application/json",
            "Authorization": self.authorization,
        }
        if self.session_id:
            headers["mcp-session-id"] = self.session_id

        req = urllib.request.Request(
            self.endpoint,
            data=json.dumps(payload, separators=(",", ":")).encode("utf-8"),
            headers=headers,
            method="POST",
        )
        try:
            with open_no_redirect(req, timeout=30) as resp:
                raw = resp.read()
                self.last_response_bytes = len(raw)
                content_type = resp.headers.get("Content-Type", "")
                if not self.session_id:
                    self.session_id = resp.headers.get("mcp-session-id", "").strip()
        except urllib.error.HTTPError as exc:
            if is_redirect_status(exc.code):
                raise CanaryError(f"{method} HTTP redirect {exc.code}: refusing to follow authenticated redirect") from exc
            body = exc.read()
            raise CanaryError(f"{method} HTTP {exc.code}: body_len={len(body)} body_sha256_12={sha12(body)}") from exc
        except urllib.error.URLError as exc:
            raise CanaryError(f"{method} request failed: reason={safe_identifier(type(exc.reason).__name__)}") from exc

        data = decode_rpc_response(method, request_id, raw, content_type)
        if data.get("error"):
            raise CanaryError(f"{method} RPC error: {json.dumps(sanitized_error_payload(data['error']), sort_keys=True)}")
        return data.get("result", {})

    def tool_call(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        result = self.post_rpc("tools/call", {"name": name, "arguments": arguments})
        if result.get("isError"):
            structured = result.get("structuredContent") if isinstance(result.get("structuredContent"), dict) else {}
            error_payload = structured.get("error") or result
            raise CanaryError(f"{name} tool error: {json.dumps(sanitized_error_payload(error_payload), sort_keys=True)}")
        structured = result.get("structuredContent")
        if not isinstance(structured, dict):
            raise CanaryError(f"{name} missing structuredContent")
        data = structured.get("data")
        if not isinstance(data, dict):
            raise CanaryError(f"{name} missing structuredContent.data")
        return data


def require_plan_fields(plan: dict[str, Any], client: str) -> tuple[str, str]:
    if plan.get("schema") != "lesserbody.agent_local_install_plan.v1":
        raise CanaryError("install plan schema mismatch")
    if plan.get("client") != client or plan.get("profile") != client:
        raise CanaryError("install plan client/profile mismatch")
    resource = plan.get("install_pack_resource") if isinstance(plan.get("install_pack_resource"), dict) else {}
    if resource.get("requires_authorization_header") is not False:
        raise CanaryError("install pack resource must require no Authorization header")
    download_url = str(resource.get("uri") or plan.get("download_url") or "").strip()
    if not download_url:
        raise CanaryError("install plan missing structured download URL")
    pack_checksum = str(plan.get("pack_checksum") or "").strip()
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", pack_checksum):
        raise CanaryError("install plan pack_checksum missing sha256 digest")
    for key in ("grant_id", "pack_id", "pack_digest", "mcp_server_name", "mcp_endpoint_url"):
        if not str(plan.get(key) or "").strip():
            raise CanaryError(f"install plan missing {key}")
    mcp_server_name = str(plan.get("mcp_server_name") or "").strip()
    if not MCP_SERVER_NAME_RE.match(mcp_server_name):
        raise CanaryError(
            f"install plan mcp_server_name {mcp_server_name!r} is off-scheme; "
            "expected lesser_ka[_<environment>]_<actor>"
        )
    return download_url, pack_checksum


def require_safe_download_url(endpoint: str, download_url: str) -> urllib.parse.ParseResult:
    parsed = urllib.parse.urlparse(download_url)
    endpoint_parsed = urllib.parse.urlparse(endpoint)
    if parsed.scheme != "https" or not parsed.netloc:
        raise CanaryError("download URL must be https")
    if parsed.scheme != endpoint_parsed.scheme or parsed.netloc != endpoint_parsed.netloc:
        raise CanaryError(
            "download URL origin mismatch: "
            f"endpoint_origin_sha256_12={sha12(endpoint_parsed.netloc)} download_origin_sha256_12={sha12(parsed.netloc)}"
        )
    if not parsed.path.startswith("/instance/downloads/installer-grants/"):
        raise CanaryError("download URL path is not an installer grant path")
    query = urllib.parse.parse_qs(parsed.query, keep_blank_values=True)
    if not query.get("token", [""])[0]:
        raise CanaryError("download URL missing one-time token query binding")
    return parsed


def download_install_pack(download_url: str) -> bytes:
    req = urllib.request.Request(download_url, headers={"Accept": "application/zip"}, method="GET")
    if any(key.lower() == "authorization" for key in req.header_items()):
        raise CanaryError("internal error: Authorization header attached to header-free download")
    try:
        with open_no_redirect(req, timeout=30) as resp:
            raw = resp.read()
            content_type = resp.headers.get("Content-Type", "")
            status = getattr(resp, "status", 200)
    except urllib.error.HTTPError as exc:
        if is_redirect_status(exc.code):
            raise CanaryError(f"download HTTP redirect {exc.code}: refusing to follow grant redirect") from exc
        body = exc.read()
        raise CanaryError(f"download HTTP {exc.code}: body_len={len(body)} body_sha256_12={sha12(body)}") from exc
    except urllib.error.URLError as exc:
        raise CanaryError(f"download request failed: reason={safe_identifier(type(exc.reason).__name__)}") from exc

    if status != 200:
        raise CanaryError(f"download HTTP {status}: body_len={len(raw)} body_sha256_12={sha12(raw)}")
    if "zip" not in content_type.lower():
        raise CanaryError(f"download returned non-ZIP content: body_len={len(raw)} body_sha256_12={sha12(raw)}")
    if not raw:
        raise CanaryError("download returned an empty ZIP body")
    return raw


def expect_second_download_gone(download_url: str) -> tuple[int, int, str]:
    req = urllib.request.Request(download_url, headers={"Accept": "application/zip"}, method="GET")
    if any(key.lower() == "authorization" for key in req.header_items()):
        raise CanaryError("internal error: Authorization header attached to second header-free download")
    try:
        with open_no_redirect(req, timeout=30) as resp:
            raw = resp.read()
            status = getattr(resp, "status", 200)
    except urllib.error.HTTPError as exc:
        if is_redirect_status(exc.code):
            raise CanaryError(f"second download HTTP redirect {exc.code}: refusing to follow grant redirect") from exc
        body = exc.read()
        if exc.code != 410:
            raise CanaryError(f"second download HTTP {exc.code}: body_len={len(body)} body_sha256_12={sha12(body)}") from exc
        return exc.code, len(body), sha12(body)
    except urllib.error.URLError as exc:
        raise CanaryError(f"second download request failed: reason={safe_identifier(type(exc.reason).__name__)}") from exc

    raise CanaryError(f"second download unexpectedly succeeded: status={status} body_len={len(raw)} body_sha256_12={sha12(raw)}")


def safe_zip_path(name: str) -> pathlib.PurePosixPath:
    path = pathlib.PurePosixPath(name)
    if path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
        raise CanaryError(f"unsafe ZIP entry path: {safe_identifier(name)}")
    return path


def verify_manifest_and_extract(zip_bytes: bytes, plan: dict[str, Any]) -> dict[str, Any]:
    try:
        with zipfile.ZipFile(io.BytesIO(zip_bytes)) as zf:
            names = zf.namelist()
            if MANIFEST_NAME not in names:
                raise CanaryError("downloaded ZIP missing MANIFEST.json")
            for name in names:
                safe_zip_path(name)
            manifest_raw = zf.read(MANIFEST_NAME)
            try:
                manifest = json.loads(manifest_raw.decode("utf-8"))
            except json.JSONDecodeError as exc:
                raise CanaryError(f"MANIFEST.json parse failed: len={len(manifest_raw)} sha256_12={sha12(manifest_raw)}") from exc
            if not isinstance(manifest, dict):
                raise CanaryError("MANIFEST.json was not an object")
            if manifest.get("schema") != PACK_SCHEMA:
                raise CanaryError("MANIFEST.json schema mismatch")
            for key in ("pack_id", "pack_digest", "mcp_server_name", "mcp_endpoint_url"):
                if manifest.get(key) != plan.get(key):
                    raise CanaryError(f"MANIFEST.json {key} mismatch")
            oauth = manifest.get("oauth") if isinstance(manifest.get("oauth"), dict) else {}
            if oauth.get("bearer_token_embedded") is not False or oauth.get("access_lease_embedded") is not False:
                raise CanaryError("MANIFEST.json indicated embedded bearer token or access lease")

            entries = manifest.get("manifest_entries")
            if not isinstance(entries, list) or not entries:
                raise CanaryError("MANIFEST.json missing manifest_entries")
            entry_by_path: dict[str, dict[str, Any]] = {}
            for entry in entries:
                if not isinstance(entry, dict):
                    raise CanaryError("manifest_entries contained a non-object")
                path = str(entry.get("path") or "")
                safe_zip_path(path)
                entry_by_path[path] = entry
            expected_names = set(entry_by_path) | {MANIFEST_NAME}
            if set(names) != expected_names:
                raise CanaryError(
                    "ZIP entry set mismatch: "
                    f"zip_count={len(names)} manifest_count={len(entry_by_path)} names_sha256_12={sha12(json.dumps(sorted(names)))}"
                )

            total_entry_bytes = 0
            for path, entry in entry_by_path.items():
                body = zf.read(path)
                total_entry_bytes += len(body)
                if sha256_prefixed(body) != entry.get("checksum"):
                    raise CanaryError(f"ZIP entry checksum mismatch for {safe_identifier(path)}")
                if int(entry.get("size_bytes") or -1) != len(body):
                    raise CanaryError(f"ZIP entry size mismatch for {safe_identifier(path)}")

            tempdir = tempfile.mkdtemp(prefix="lesser-ba-canary-")
            try:
                root = pathlib.Path(tempdir).resolve()
                for name in names:
                    target = (root / pathlib.Path(*safe_zip_path(name).parts)).resolve()
                    if root not in target.parents and target != root:
                        raise CanaryError(f"unsafe extraction target for {safe_identifier(name)}")
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with target.open("wb") as out:
                        out.write(zf.read(name))
                extracted_files = [p for p in root.rglob("*") if p.is_file()]
                extracted_total = sum(p.stat().st_size for p in extracted_files)
            finally:
                shutil.rmtree(tempdir, ignore_errors=True)
    except zipfile.BadZipFile as exc:
        raise CanaryError(f"downloaded body was not a valid ZIP: len={len(zip_bytes)} sha256_12={sha12(zip_bytes)}") from exc

    return {
        "manifest": manifest,
        "zip_entries": len(names),
        "manifest_entries": len(entry_by_path),
        "entry_bytes": total_entry_bytes,
        "extracted_files": len(extracted_files),
        "extracted_bytes": extracted_total,
        "cleanup": True,
    }


def main() -> int:
    if "--help" in sys.argv or "-h" in sys.argv:
        print(USAGE.strip())
        return 0

    endpoint = canonical_url(env_required("BA_MCP_ENDPOINT", "MCP_ENDPOINT"))
    require_instance_surface(endpoint, "ba")
    authorization = env("BA_MCP_AUTHORIZATION", "MCP_AUTHORIZATION")
    if not authorization:
        token = env_required("BA_MCP_BEARER_TOKEN", "MCP_BEARER_TOKEN")
        authorization = token if token.lower().startswith("bearer ") else f"Bearer {token}"
    if not authorization.lower().startswith("bearer "):
        raise CanaryError("authorization must use Bearer scheme")

    if not env_bool("BA_CANARY_CONFIRM_INSTALL_PLAN"):
        raise CanaryError("BA_CANARY_CONFIRM_INSTALL_PLAN=true is required because this probe mints a one-time grant")
    if not env_bool("BA_CANARY_CONFIRM_DOWNLOAD"):
        raise CanaryError("BA_CANARY_CONFIRM_DOWNLOAD=true is required because this probe consumes a one-time grant")

    agent_id = env_required("BA_AGENT_ID")
    actor_username = env("BA_ACTOR_USERNAME")
    client_name = env("BA_INSTALL_CLIENT", default="codex").lower().replace("-", "_")
    profile = env("BA_INSTALL_PROFILE", default=client_name).lower().replace("-", "_")
    if client_name not in {"codex", "claude_code"}:
        raise CanaryError("BA_INSTALL_CLIENT must be codex or claude_code")
    if profile != client_name:
        raise CanaryError("BA_INSTALL_PROFILE must match BA_INSTALL_CLIENT when supplied")

    metadata_url = protected_resource_metadata_url(endpoint, env("BA_PROTECTED_RESOURCE_METADATA_URL"), "ba")

    log(f"Ba MCP endpoint: {endpoint}")
    log("Authorization: Bearer <redacted>")
    log(
        "canary input "
        f"agent_id={safe_identifier(agent_id)} agent_id_sha256_12={sha12(agent_id)} client={client_name} profile={profile}"
    )

    metadata = fetch_protected_resource_metadata(endpoint, metadata_url)
    log(
        "ok protected-resource metadata "
        f"resource_sha256_12={sha12(str(metadata.get('resource') or ''))} "
        f"authorization_servers={len(metadata.get('authorization_servers') or [])} "
        f"scopes={','.join(str(scope) for scope in metadata.get('scopes_supported', []))}"
    )

    mcp = MCPClient(endpoint, authorization)
    mcp.post_rpc("initialize")
    if not mcp.session_id:
        raise CanaryError("initialize did not return mcp-session-id")
    log(f"ok initialize session={mcp.session_id[:8]}…")

    tools_result = mcp.post_rpc("tools/list")
    tool_names = {tool.get("name") for tool in tools_result.get("tools", []) if isinstance(tool, dict)}
    if "agent_local_install_plan" not in tool_names:
        raise CanaryError("tools/list missing Ba agent_local_install_plan")
    log("ok tools/list Ba install-plan tool present")

    plan_args: dict[str, Any] = {"agent_id": agent_id, "client": client_name, "profile": profile}
    if actor_username:
        plan_args["actor_username"] = actor_username
    plan = mcp.tool_call("agent_local_install_plan", plan_args)
    download_url, pack_checksum = require_plan_fields(plan, client_name)
    require_safe_download_url(endpoint, download_url)
    log(
        "ok agent_local_install_plan "
        f"grant_id={safe_identifier(plan.get('grant_id'))} pack_id_sha256_12={sha12(str(plan.get('pack_id') or ''))} "
        f"pack_digest_sha256_12={sha12(str(plan.get('pack_digest') or ''))} "
        f"pack_checksum={pack_checksum} mcp_server_name={safe_identifier(plan.get('mcp_server_name'))} "
        f"download_url=<redacted> payloadB={mcp.last_response_bytes}"
    )

    zip_bytes = download_install_pack(download_url)
    got_checksum = sha256_prefixed(zip_bytes)
    if got_checksum != pack_checksum:
        raise CanaryError(
            "download checksum mismatch: "
            f"expected_sha256_12={sha12(pack_checksum)} got_sha256_12={sha12(got_checksum)} body_len={len(zip_bytes)}"
        )
    log(f"ok header-free download bytes={len(zip_bytes)} sha256_12={sha12(zip_bytes)} authorization_sent=false")

    summary = verify_manifest_and_extract(zip_bytes, plan)
    manifest = summary["manifest"] if isinstance(summary.get("manifest"), dict) else {}
    log(
        "ok install pack verified/extracted "
        f"manifest_schema={safe_identifier(manifest.get('schema'))} zip_entries={summary['zip_entries']} "
        f"manifest_entries={summary['manifest_entries']} entry_bytes={summary['entry_bytes']} "
        f"extracted_files={summary['extracted_files']} extracted_bytes={summary['extracted_bytes']} cleanup={summary['cleanup']} "
        f"mcp_endpoint_sha256_12={sha12(str(manifest.get('mcp_endpoint_url') or ''))}"
    )

    status, body_len, body_hash = expect_second_download_gone(download_url)
    log(f"ok second header-free download status={status} body_len={body_len} body_sha256_12={body_hash}")

    log("canary passed (Ba install-plan/download consumed through AppTheory MCP/RFC 9728 surfaces; output sanitized)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CanaryError as exc:
        print(f"canary failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
