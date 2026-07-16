#!/usr/bin/env python3
"""Canary Lesser Body Ptah instance-plane MCP tools end-to-end.

Required environment:
  PTAH_MCP_ENDPOINT or MCP_ENDPOINT
      Ptah instance MCP endpoint, for example https://api.dev.example.com/instance/ptah/mcp.
  PTAH_MCP_BEARER_TOKEN or MCP_BEARER_TOKEN
      Account-holder Lesser OAuth access token for the Ptah instance resource. Alternatively set
      PTAH_MCP_AUTHORIZATION or MCP_AUTHORIZATION to a complete "Bearer ..." header value.
  PTAH_AGENT_USERNAME
      Existing Lesser local agent account username to delegate with agent_create. The canary does not create
      the Lesser account; use a fresh canary agent account to avoid agent_create registry duplicates.
  PTAH_CANARY_CONFIRM_AGENT_CREATE=true
      Required because agent_create delegates runtime credentials and creates a Ptah registry entry.
  PTAH_CANARY_CONFIRM_CONTENT_UPSERT=true
      Required because the canary writes draft agent_soul and agent_instructions content.

Optional environment:
  PTAH_PROTECTED_RESOURCE_METADATA_URL  Override the derived RFC 9728 metadata URL.
  PTAH_ACTOR_USERNAME                   Explicit account-holder username; must match the token principal.
  PTAH_AGENT_SCOPES                     Comma/space-separated delegated scopes (default: read).
  PTAH_AGENT_DISPLAY_NAME               Optional display name passed through to Lesser delegation.
  PTAH_AGENT_BIO                        Optional bio passed through to Lesser delegation.
  PTAH_AGENT_EXPIRES_IN                 Optional delegated token TTL seconds.
  PTAH_AGENT_DEVICE_LABEL               Optional device label (default: generated canary label).
  PTAH_AGENT_INFO_JSON                  Optional JSON object passed as agent_info.
  PTAH_AGENT_SOUL                       Draft agent_soul content (default: generated minimal canary text).
  PTAH_AGENT_INSTRUCTIONS               Draft agent_instructions content (default: generated minimal canary text).

The canary consumes the published RFC 9728 protected-resource metadata and AppTheory MCP tools/list surface, then runs
agent_create -> agent_soul_upsert -> agent_instructions_upsert -> agent_get/content gets -> agent_list. It refuses
authenticated redirects, redacts bearer tokens, never prints delegated token values, full soul/instructions content, raw
RPC payloads, or upstream error bodies, and emits only bounded statuses, sizes, hashes, and opaque ids.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import secrets
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from typing import Any


class CanaryError(RuntimeError):
    pass


class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Refuse redirects so Authorization never leaves the configured endpoint."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


NO_REDIRECT_OPENER = urllib.request.build_opener(NoRedirectHandler)
SAFE_IDENTIFIER_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]{0,255}$")


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


def env_int(name: str, *, minimum: int, maximum: int) -> int | None:
    raw = os.environ.get(name, "").strip()
    if raw == "":
        return None
    try:
        value = int(raw)
    except ValueError as exc:
        raise CanaryError(f"{name} must be an integer") from exc
    if value < minimum or value > maximum:
        raise CanaryError(f"{name} must be between {minimum} and {maximum}")
    return value


def sha12(value: str | bytes) -> str:
    if isinstance(value, str):
        value = value.encode("utf-8", errors="replace")
    return hashlib.sha256(value).hexdigest()[:12]


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

    def tool_call(self, name: str, arguments: dict[str, Any]) -> tuple[dict[str, Any], dict[str, Any]]:
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
        return data, result


def result_text(result: dict[str, Any]) -> str:
    blocks = result.get("content") if isinstance(result.get("content"), list) else []
    texts: list[str] = []
    for block in blocks:
        if isinstance(block, dict) and isinstance(block.get("text"), str):
            texts.append(block["text"])
    return "\n".join(texts)


def required_string(value: Any, *, context: str) -> str:
    text = str(value or "").strip()
    if not text:
        raise CanaryError(f"{context} missing required string")
    return text


def parse_scopes(raw: str) -> list[str]:
    if not raw.strip():
        return ["read"]
    scopes = [part.strip() for part in re.split(r"[\s,]+", raw) if part.strip()]
    if not scopes:
        raise CanaryError("PTAH_AGENT_SCOPES produced no scopes")
    return scopes


def parse_agent_info(raw: str) -> Any:
    if not raw:
        return None
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise CanaryError("PTAH_AGENT_INFO_JSON must be valid JSON") from exc
    if not isinstance(value, dict):
        raise CanaryError("PTAH_AGENT_INFO_JSON must be a JSON object")
    return value


def registry_agent_id(data: dict[str, Any], *, context: str) -> str:
    registry = data.get("registry") if isinstance(data.get("registry"), dict) else {}
    agent_id = str(registry.get("agent_id") or "").strip()
    if not agent_id:
        account = data.get("account_summary") if isinstance(data.get("account_summary"), dict) else {}
        agent_id = str(account.get("id") or "").strip()
    if not agent_id:
        raise CanaryError(f"{context} missing registry/account agent id")
    return agent_id


def require_tool_text_omits_secret_values(result: dict[str, Any], token_map: dict[str, Any]) -> None:
    text = result_text(result)
    for key in ("access_token", "refresh_token"):
        value = token_map.get(key)
        if isinstance(value, str) and len(value) >= 8 and value in text:
            raise CanaryError(f"agent_create text leaked delegated {key}")
    for forbidden in ("access_token", "refresh_token"):
        if forbidden in text.lower():
            raise CanaryError("agent_create text mentioned delegated credential field names")


def require_tool_text_omits_content(result: dict[str, Any], content: str, *, context: str) -> None:
    if content and content in result_text(result):
        raise CanaryError(f"{context} text duplicated full content")


def content_record(data: dict[str, Any], key: str, *, context: str) -> dict[str, Any]:
    record = data.get(key)
    if not isinstance(record, dict):
        raise CanaryError(f"{context} missing {key}")
    return record


def main() -> int:
    if "--help" in sys.argv or "-h" in sys.argv:
        print(USAGE.strip())
        return 0

    endpoint = canonical_url(env_required("PTAH_MCP_ENDPOINT", "MCP_ENDPOINT"))
    require_instance_surface(endpoint, "ptah")
    authorization = env("PTAH_MCP_AUTHORIZATION", "MCP_AUTHORIZATION")
    if not authorization:
        token = env_required("PTAH_MCP_BEARER_TOKEN", "MCP_BEARER_TOKEN")
        authorization = token if token.lower().startswith("bearer ") else f"Bearer {token}"
    if not authorization.lower().startswith("bearer "):
        raise CanaryError("authorization must use Bearer scheme")

    if not env_bool("PTAH_CANARY_CONFIRM_AGENT_CREATE"):
        raise CanaryError("PTAH_CANARY_CONFIRM_AGENT_CREATE=true is required because agent_create delegates credentials")
    if not env_bool("PTAH_CANARY_CONFIRM_CONTENT_UPSERT"):
        raise CanaryError("PTAH_CANARY_CONFIRM_CONTENT_UPSERT=true is required because this probe writes draft content")

    agent_username = env_required("PTAH_AGENT_USERNAME")
    actor_username = env("PTAH_ACTOR_USERNAME")
    scopes = parse_scopes(env("PTAH_AGENT_SCOPES", default="read"))
    expires_in = env_int("PTAH_AGENT_EXPIRES_IN", minimum=60, maximum=60 * 60 * 24 * 365)
    nonce = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S") + "-" + secrets.token_hex(4)
    device_label = env("PTAH_AGENT_DEVICE_LABEL", default=f"lesser-body-ptah-canary-{nonce}")
    default_soul = f"Ptah canary provisional agent_soul draft. nonce={nonce}\n"
    default_instructions = f"Ptah canary agent instructions draft. nonce={nonce}\n"
    soul_content = os.environ.get("PTAH_AGENT_SOUL", default_soul)
    instructions_content = os.environ.get("PTAH_AGENT_INSTRUCTIONS", default_instructions)
    agent_info = parse_agent_info(env("PTAH_AGENT_INFO_JSON"))

    metadata_url = protected_resource_metadata_url(endpoint, env("PTAH_PROTECTED_RESOURCE_METADATA_URL"), "ptah")

    log(f"Ptah MCP endpoint: {endpoint}")
    log("Authorization: Bearer <redacted>")
    log(
        "canary input "
        f"agent_username_sha256_12={sha12(agent_username)} scopes={','.join(scopes)} "
        f"device_label_sha256_12={sha12(device_label)} soul_bytes={len(soul_content.encode('utf-8'))} "
        f"soul_sha256_12={sha12(soul_content)} instructions_bytes={len(instructions_content.encode('utf-8'))} "
        f"instructions_sha256_12={sha12(instructions_content)}"
    )

    metadata = fetch_protected_resource_metadata(endpoint, metadata_url)
    log(
        "ok protected-resource metadata "
        f"resource_sha256_12={sha12(str(metadata.get('resource') or ''))} "
        f"authorization_servers={len(metadata.get('authorization_servers') or [])} "
        f"scopes={','.join(str(scope) for scope in metadata.get('scopes_supported', []))}"
    )

    client = MCPClient(endpoint, authorization)
    client.post_rpc("initialize")
    if not client.session_id:
        raise CanaryError("initialize did not return mcp-session-id")
    log(f"ok initialize session={client.session_id[:8]}…")

    tools_result = client.post_rpc("tools/list")
    tool_names = {tool.get("name") for tool in tools_result.get("tools", []) if isinstance(tool, dict)}
    required_tools = {
        "agent_create",
        "agent_get",
        "agent_list",
        "agent_soul_get",
        "agent_soul_upsert",
        "agent_instructions_get",
        "agent_instructions_upsert",
    }
    missing = sorted(required_tools - tool_names)
    if missing:
        raise CanaryError(f"tools/list missing Ptah tools: {missing}")
    log("ok tools/list Ptah create/content/read tools present")

    create_args: dict[str, Any] = {
        "agent_username": agent_username,
        "scopes": scopes,
        "device_label": device_label,
    }
    for key, value in (
        ("actor_username", actor_username),
        ("display_name", env("PTAH_AGENT_DISPLAY_NAME")),
        ("bio", env("PTAH_AGENT_BIO")),
    ):
        if value:
            create_args[key] = value
    if expires_in is not None:
        create_args["expires_in"] = expires_in
    if agent_info is not None:
        create_args["agent_info"] = agent_info

    created, create_result = client.tool_call("agent_create", create_args)
    token_map = created.get("token") if isinstance(created.get("token"), dict) else {}
    token_metadata = created.get("token_metadata") if isinstance(created.get("token_metadata"), dict) else {}
    require_tool_text_omits_secret_values(create_result, token_map)
    agent_id = registry_agent_id(created, context="agent_create")
    log(
        "ok agent_create "
        f"agent_id={safe_identifier(agent_id)} token_type={safe_identifier(token_metadata.get('token_type'))} "
        f"expires_in={safe_identifier(token_metadata.get('expires_in'))} "
        f"has_refresh_token={bool(token_metadata.get('has_refresh_token'))} payloadB={client.last_response_bytes}"
    )

    soul_upsert, soul_result = client.tool_call(
        "agent_soul_upsert",
        {"agent_id": agent_id, **({"actor_username": actor_username} if actor_username else {}), "content": soul_content},
    )
    require_tool_text_omits_content(soul_result, soul_content, context="agent_soul_upsert")
    soul_record = content_record(soul_upsert, "agent_soul", context="agent_soul_upsert")
    if soul_record.get("content") != soul_content:
        raise CanaryError("agent_soul_upsert content echo did not match submitted content")
    log(
        "ok agent_soul_upsert "
        f"agent_id={safe_identifier(soul_record.get('agent_id'))} version={safe_identifier(soul_record.get('version'))} "
        f"lifecycle={safe_identifier(soul_record.get('lifecycle_state'))} content_bytes={safe_identifier(soul_record.get('content_bytes'))} "
        f"content_sha256_12={sha12(soul_content)} payloadB={client.last_response_bytes}"
    )

    instructions_upsert, instructions_result = client.tool_call(
        "agent_instructions_upsert",
        {"agent_id": agent_id, **({"actor_username": actor_username} if actor_username else {}), "content": instructions_content},
    )
    require_tool_text_omits_content(instructions_result, instructions_content, context="agent_instructions_upsert")
    instructions_record = content_record(instructions_upsert, "agent_instructions", context="agent_instructions_upsert")
    if instructions_record.get("content") != instructions_content:
        raise CanaryError("agent_instructions_upsert content echo did not match submitted content")
    log(
        "ok agent_instructions_upsert "
        f"agent_id={safe_identifier(instructions_record.get('agent_id'))} version={safe_identifier(instructions_record.get('version'))} "
        f"lifecycle={safe_identifier(instructions_record.get('lifecycle_state'))} "
        f"content_bytes={safe_identifier(instructions_record.get('content_bytes'))} "
        f"content_sha256_12={sha12(instructions_content)} payloadB={client.last_response_bytes}"
    )

    got_agent, _ = client.tool_call("agent_get", {"agent_id": agent_id, **({"actor_username": actor_username} if actor_username else {})})
    got_agent_id = registry_agent_id(got_agent, context="agent_get")
    if got_agent_id != agent_id:
        raise CanaryError("agent_get returned a different agent id")
    log(f"ok agent_get agent_id={safe_identifier(got_agent_id)} payloadB={client.last_response_bytes}")

    got_soul, _ = client.tool_call("agent_soul_get", {"agent_id": agent_id, **({"actor_username": actor_username} if actor_username else {})})
    got_soul_record = content_record(got_soul, "agent_soul", context="agent_soul_get")
    if got_soul_record.get("content") != soul_content:
        raise CanaryError("agent_soul_get content did not match upserted content")
    log(
        "ok agent_soul_get "
        f"version={safe_identifier(got_soul_record.get('version'))} lifecycle={safe_identifier(got_soul_record.get('lifecycle_state'))} "
        f"content_sha256_12={sha12(str(got_soul_record.get('content') or ''))} payloadB={client.last_response_bytes}"
    )

    got_instructions, _ = client.tool_call(
        "agent_instructions_get", {"agent_id": agent_id, **({"actor_username": actor_username} if actor_username else {})}
    )
    got_instructions_record = content_record(got_instructions, "agent_instructions", context="agent_instructions_get")
    if got_instructions_record.get("content") != instructions_content:
        raise CanaryError("agent_instructions_get content did not match upserted content")
    log(
        "ok agent_instructions_get "
        f"version={safe_identifier(got_instructions_record.get('version'))} "
        f"lifecycle={safe_identifier(got_instructions_record.get('lifecycle_state'))} "
        f"content_sha256_12={sha12(str(got_instructions_record.get('content') or ''))} payloadB={client.last_response_bytes}"
    )

    list_pages = 0
    total_seen = 0
    has_more: Any = False
    cursor = ""
    seen = False
    while True:
        list_args: dict[str, Any] = {"limit": 20}
        if cursor:
            list_args["cursor"] = cursor
        listed, _ = client.tool_call("agent_list", list_args)
        list_pages += 1
        agents = listed.get("agents") if isinstance(listed.get("agents"), list) else []
        total_seen += len(agents)
        for item in agents:
            if not isinstance(item, dict):
                continue
            registry = item.get("registry") if isinstance(item.get("registry"), dict) else {}
            if registry.get("agent_id") == agent_id:
                seen = True
                break
        pagination = listed.get("pagination") if isinstance(listed.get("pagination"), dict) else {}
        has_more = pagination.get("has_more")
        cursor = str(pagination.get("next_cursor") or "").strip()
        if seen or not has_more or not cursor or list_pages >= 10:
            break
    if not seen:
        raise CanaryError("agent_list did not include the created registry entry within 10 pages")
    log(
        "ok agent_list "
        f"pages={list_pages} seen_entries={total_seen} has_more={safe_identifier(has_more)} "
        f"created_agent_present=true payloadB={client.last_response_bytes}"
    )

    log("canary passed (Ptah instance-plane tools consumed through AppTheory MCP/RFC 9728 surfaces; output sanitized)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CanaryError as exc:
        print(f"canary failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
