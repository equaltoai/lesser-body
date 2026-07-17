#!/usr/bin/env python3
"""Canary Lesser Body Ptah instance-plane MCP tools end-to-end.

Required environment:
  PTAH_MCP_ENDPOINT or MCP_ENDPOINT
      Ptah instance MCP endpoint, for example https://api.dev.example.com/instance/ptah/mcp.
  PTAH_MCP_BEARER_TOKEN or MCP_BEARER_TOKEN
      Lesser owner/operator OAuth access token for the exact Ptah instance resource. The token must carry the
      explicit operator claim issued by Lesser; an ordinary public write token is expected to be rejected. Alternatively set
      PTAH_MCP_AUTHORIZATION or MCP_AUTHORIZATION to a complete "Bearer ..." header value.
  PTAH_GENESIS_DOMAIN
      Managed Lesser/Host domain for the new agent registration.
  PTAH_GENESIS_LOCAL_ID
      New local id to mint. This must not be an existing-agent delegation input.
  PTAH_GENESIS_MODEL
      Host genesis model identifier for the first conversation turn.
  PTAH_GENESIS_MESSAGES_JSON
      JSON array of owner messages, in order. The canary submits each turn through Host and never prints the messages.

Optional environment:
  PTAH_PROTECTED_RESOURCE_METADATA_URL  Override the derived RFC 9728 metadata URL.
  PTAH_GENESIS_MAX_POLLS                Bounded read/recovery polls per Host checkpoint (default: 20, max: 60).
  PTAH_GENESIS_POLL_SECONDS              Delay between polls (default: 2, max: 30).
  PTAH_GENESIS_IDEMPOTENCY_KEY           Optional first-turn idempotency key; otherwise a canary key is generated.

The canary consumes the published RFC 9728 protected-resource metadata and AppTheory MCP tools/list surface, then proves
owner connects to Ptah -> Host-backed genesis begin/advance/read/recover/complete -> finalize -> agent_list visibility.
`agent_create` is intentionally not used as minting proof: it remains a compatibility-only existing-agent delegation
tool. The canary refuses authenticated redirects, redacts bearer tokens, never prints owner messages, Host declarations,
wallet material, raw RPC payloads, or upstream error bodies, and emits only bounded statuses, sizes, hashes, and opaque ids.

Local deterministic probe:
  scripts/canary_ptah_mcp.py --self-test-redaction
      Verifies the genesis text redaction guard rejects transcript, declaration, wallet-signature, and credential leaks.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import Any


class CanaryError(RuntimeError):
    pass


class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Refuse redirects so Authorization never leaves the configured endpoint."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


NO_REDIRECT_OPENER = urllib.request.build_opener(NoRedirectHandler)
SAFE_IDENTIFIER_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]{0,255}$")
FORBIDDEN_CREDENTIAL_FIELD_RE = re.compile(
    r"(?<![A-Za-z0-9_])(access_token|refresh_token)(?![A-Za-z0-9_])",
    re.IGNORECASE,
)


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


def tool_text_blocks(result: dict[str, Any]) -> list[str]:
    blocks = result.get("content") if isinstance(result.get("content"), list) else []
    texts: list[str] = []
    for block in blocks:
        if isinstance(block, dict) and isinstance(block.get("text"), str):
            texts.append(block["text"])
    return texts


def result_text(result: dict[str, Any]) -> str:
    texts = tool_text_blocks(result)
    return "\n".join(texts)


def required_string(value: Any, *, context: str) -> str:
    text = str(value or "").strip()
    if not text:
        raise CanaryError(f"{context} missing required string")
    return text


def parse_genesis_messages(raw: str) -> list[str]:
    if not raw.strip():
        raise CanaryError("PTAH_GENESIS_MESSAGES_JSON is required")
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise CanaryError("PTAH_GENESIS_MESSAGES_JSON must be valid JSON") from exc
    if not isinstance(value, list) or not value:
        raise CanaryError("PTAH_GENESIS_MESSAGES_JSON must be a non-empty JSON array")
    if len(value) > 64:
        raise CanaryError("PTAH_GENESIS_MESSAGES_JSON may contain at most 64 messages")
    messages: list[str] = []
    for index, item in enumerate(value):
        message = str(item).strip() if isinstance(item, str) else ""
        if not message or len(message) > 8192:
            raise CanaryError(f"PTAH_GENESIS_MESSAGES_JSON message {index} is empty or too long")
        messages.append(message)
    return messages


def genesis_conversation(data: dict[str, Any], *, context: str) -> dict[str, Any]:
    conversation = data.get("conversation")
    if not isinstance(conversation, dict):
        raise CanaryError(f"{context} missing Host conversation projection")
    return conversation


def genesis_status(data: dict[str, Any], *, context: str) -> str:
    status = str(genesis_conversation(data, context=context).get("status") or "").strip().lower()
    if not status:
        raise CanaryError(f"{context} missing Host conversation status")
    return status


def genesis_registration_id(data: dict[str, Any], *, context: str) -> str:
    return required_string(data.get("registration_id"), context=f"{context} registration_id")


def genesis_conversation_id(data: dict[str, Any], *, context: str) -> str:
    conversation = genesis_conversation(data, context=context)
    return required_string(conversation.get("conversation_id") or data.get("conversation_id"), context=f"{context} conversation_id")


def genesis_agent_id(data: dict[str, Any], *, context: str) -> str:
    agent_id = str(data.get("agent_id") or "").strip()
    if not agent_id:
        conversation = data.get("conversation") if isinstance(data.get("conversation"), dict) else {}
        agent_id = str(conversation.get("agent_id") or "").strip()
    if not agent_id:
        publication = data.get("publication") if isinstance(data.get("publication"), dict) else {}
        agent_id = str(publication.get("agent_id") or "").strip()
    return required_string(agent_id, context=f"{context} agent_id")


FORBIDDEN_GENESIS_TEXT_FIELDS = frozenset(
    {
        "access_token",
        "refresh_token",
        "wallet_signature",
        "wallet_address",
        "grant_token",
        "declaration",
        "produced_declarations",
        "transcript",
    }
)


def genesis_text_field_names(text: str) -> set[str]:
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        return {
            match.group(1).lower()
            for match in FORBIDDEN_CREDENTIAL_FIELD_RE.finditer(text)
        }
    found: set[str] = set()

    def visit(value: Any) -> None:
        if isinstance(value, dict):
            for key, child in value.items():
                normalized = str(key).strip().lower()
                if normalized in FORBIDDEN_GENESIS_TEXT_FIELDS:
                    found.add(normalized)
                visit(child)
        elif isinstance(value, list):
            for child in value:
                visit(child)

    visit(parsed)
    return found


def require_genesis_text_safety(result: dict[str, Any], secret_values: list[str]) -> None:
    text = result_text(result)
    for secret in secret_values:
        if secret and len(secret) >= 8 and secret in text:
            raise CanaryError("genesis text leaked a protected value")
    forbidden_fields: set[str] = set()
    for block_text in tool_text_blocks(result):
        forbidden_fields.update(genesis_text_field_names(block_text))
    if forbidden_fields:
        raise CanaryError("genesis text exposed protected fields: " + ",".join(sorted(forbidden_fields)))


def expect_redaction_failure(name: str, result: dict[str, Any], secret_values: list[str]) -> None:
    try:
        require_genesis_text_safety(result, secret_values)
    except CanaryError:
        return
    raise CanaryError(f"redaction self-test expected failure for {name}")


def self_test_redaction_guard() -> int:
    protected = [
        "self-test-owner-bearer-value",
        "self-test-private-transcript-value",
        "self-test-wallet-signature-value",
    ]
    safe_result = {
        "content": [
            {
                "type": "text",
                "text": json.dumps(
                    {
                        "summary": "Host-backed Ptah genesis state updated",
                        "operation": "read",
                        "source": "lesser_host",
                        "state_authority": "Host HostedGenesisSession",
                        "data": {"location": "structuredContent.data"},
                    },
                    sort_keys=True,
                ),
            }
        ]
    }
    require_genesis_text_safety(safe_result, protected)
    expect_redaction_failure(
        "raw owner bearer",
        {"content": [{"type": "text", "text": protected[0]}]},
        protected,
    )
    expect_redaction_failure(
        "full transcript value",
        {"content": [{"type": "text", "text": protected[1]}]},
        protected,
    )
    expect_redaction_failure(
        "wallet signature field",
        {"content": [{"type": "text", "text": json.dumps({"wallet_signature": "<redacted>"})}]},
        protected,
    )
    log("ok genesis redaction self-test rejected bearer, transcript, and wallet-signature leaks")
    return 0


def poll_genesis(
    client: MCPClient,
    registration_id: str,
    conversation_id: str,
    *,
    max_polls: int,
    poll_seconds: int,
    protected_values: list[str],
    context: str,
) -> tuple[dict[str, Any], str]:
    for attempt in range(max_polls):
        data, result = client.tool_call(
            "agent_genesis_read",
            {"registration_id": registration_id, "conversation_id": conversation_id},
        )
        require_genesis_text_safety(result, protected_values)
        status = genesis_status(data, context=context)
        log(
            f"ok {context} poll={attempt + 1}/{max_polls} status={safe_identifier(status)} "
            f"conversation_sha256_12={sha12(conversation_id)} payloadB={client.last_response_bytes}"
        )
        if status == "failed":
            recovered, recovery_result = client.tool_call(
                "agent_genesis_recover",
                {"registration_id": registration_id, "conversation_id": conversation_id},
            )
            require_genesis_text_safety(recovery_result, protected_values)
            log(
                f"ok {context} recovery poll={attempt + 1}/{max_polls} "
                f"payloadB={client.last_response_bytes}"
            )
            recovered_conversation_id = genesis_conversation_id(recovered, context=f"{context} recovery")
            if recovered_conversation_id != conversation_id:
                conversation_id = recovered_conversation_id
            continue
        if status == "declaration_ready" or status in {"finalized", "published", "completed"}:
            return data, status
        if status == "assistant_turn_ready":
            return data, status
        if attempt + 1 < max_polls:
            time.sleep(poll_seconds)
    raise CanaryError(f"{context} did not reach a usable Host checkpoint within {max_polls} polls")


def visible_agent_matches(item: Any, agent_id: str, local_id: str) -> bool:
    if not isinstance(item, dict):
        return False
    candidates: list[str] = []
    for key in ("agent_id", "id", "username", "acct", "url"):
        value = item.get(key)
        if isinstance(value, str):
            candidates.append(value.strip())
    for nested_key in ("live_agent", "registry"):
        nested = item.get(nested_key)
        if isinstance(nested, dict):
            for key in ("agent_id", "id", "username", "acct", "url"):
                value = nested.get(key)
                if isinstance(value, str):
                    candidates.append(value.strip())
    wanted_local = local_id.casefold()
    for candidate in candidates:
        if candidate == agent_id or candidate.casefold() == wanted_local:
            return True
    return False


def main() -> int:
    if "--self-test-redaction" in sys.argv:
        return self_test_redaction_guard()
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

    domain = env_required("PTAH_GENESIS_DOMAIN")
    local_id = env_required("PTAH_GENESIS_LOCAL_ID")
    model = env_required("PTAH_GENESIS_MODEL")
    messages = parse_genesis_messages(env_required("PTAH_GENESIS_MESSAGES_JSON"))
    max_polls = env_int("PTAH_GENESIS_MAX_POLLS", minimum=1, maximum=60) or 20
    poll_seconds = env_int("PTAH_GENESIS_POLL_SECONDS", minimum=1, maximum=30) or 2
    idempotency_key = env(
        "PTAH_GENESIS_IDEMPOTENCY_KEY",
        default=f"ptah-canary-{sha12(domain + ':' + local_id)}-{secrets.token_hex(4)}",
    )

    metadata_url = protected_resource_metadata_url(endpoint, env("PTAH_PROTECTED_RESOURCE_METADATA_URL"), "ptah")

    log(f"Ptah MCP endpoint: {endpoint}")
    log("Authorization: Bearer <redacted>")
    log(
        "canary input "
        f"domain_sha256_12={sha12(domain)} local_id_sha256_12={sha12(local_id)} "
        f"model_sha256_12={sha12(model)} message_count={len(messages)} "
        f"message_bytes={sum(len(message.encode('utf-8')) for message in messages)} "
        f"idempotency_key_sha256_12={sha12(idempotency_key)} max_polls={max_polls} poll_seconds={poll_seconds}"
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
        "agent_genesis_begin",
        "agent_genesis_read",
        "agent_genesis_advance",
        "agent_genesis_recover",
        "agent_genesis_complete",
        "agent_genesis_finalize_preflight",
        "agent_genesis_finalize",
        "agent_list",
    }
    missing = sorted(required_tools - tool_names)
    if missing:
        raise CanaryError(f"tools/list missing Ptah tools: {missing}")
    log("ok tools/list Ptah Host-backed genesis and visibility tools present")

    authorization_parts = authorization.split(None, 1)
    protected_values = [authorization_parts[1].strip()] if len(authorization_parts) == 2 else []
    begin, begin_result = client.tool_call(
        "agent_genesis_begin",
        {"domain": domain, "local_id": local_id},
    )
    require_genesis_text_safety(begin_result, protected_values)
    registration_id = genesis_registration_id(begin, context="agent_genesis_begin")
    log(
        "ok agent_genesis_begin "
        f"registration_sha256_12={sha12(registration_id)} authority=instance_trust "
        f"existing_agent_create=false payloadB={client.last_response_bytes}"
    )

    conversation_id = ""
    current_status = ""
    for index, message in enumerate(messages):
        advance_args: dict[str, Any] = {
            "registration_id": registration_id,
            "message": message,
            "idempotency_key": idempotency_key if index == 0 else f"{idempotency_key}-{index + 1}",
        }
        if index == 0:
            advance_args["model"] = model
        if conversation_id:
            advance_args["conversation_id"] = conversation_id
        advance, advance_result = client.tool_call("agent_genesis_advance", advance_args)
        require_genesis_text_safety(advance_result, protected_values)
        conversation_id = genesis_conversation_id(advance, context=f"agent_genesis_advance turn {index + 1}")
        _, current_status = poll_genesis(
            client,
            registration_id,
            conversation_id,
            max_polls=max_polls,
            poll_seconds=poll_seconds,
            protected_values=protected_values,
            context=f"agent_genesis_advance turn {index + 1}",
        )
        log(
            f"ok agent_genesis_advance turn={index + 1}/{len(messages)} "
            f"status={safe_identifier(current_status)} conversation_sha256_12={sha12(conversation_id)} "
            f"payloadB={client.last_response_bytes}"
        )
        if current_status == "declaration_ready":
            break

    if not conversation_id:
        raise CanaryError("Host did not return a conversation_id")
    if current_status != "declaration_ready":
        complete, complete_result = client.tool_call(
            "agent_genesis_complete",
            {"registration_id": registration_id, "conversation_id": conversation_id},
        )
        require_genesis_text_safety(complete_result, protected_values)
        complete_conversation_id = genesis_conversation_id(complete, context="agent_genesis_complete")
        if complete_conversation_id != conversation_id:
            conversation_id = complete_conversation_id
        _, current_status = poll_genesis(
            client,
            registration_id,
            conversation_id,
            max_polls=max_polls,
            poll_seconds=poll_seconds,
            protected_values=protected_values,
            context="agent_genesis_complete",
        )
        if current_status != "declaration_ready":
            raise CanaryError(
                "Host did not reach declaration_ready after the supplied owner messages; "
                "add the next required message to PTAH_GENESIS_MESSAGES_JSON"
            )
    log(
        "ok agent_genesis_complete "
        f"status={safe_identifier(current_status)} conversation_sha256_12={sha12(conversation_id)} "
        f"payloadB={client.last_response_bytes}"
    )

    _, preflight_result = client.tool_call(
        "agent_genesis_finalize_preflight",
        {"registration_id": registration_id, "conversation_id": conversation_id},
    )
    require_genesis_text_safety(preflight_result, protected_values)
    log(
        "ok agent_genesis_finalize_preflight "
        f"conversation_sha256_12={sha12(conversation_id)} payloadB={client.last_response_bytes}"
    )

    finalized, finalized_result = client.tool_call(
        "agent_genesis_finalize",
        {"registration_id": registration_id, "conversation_id": conversation_id},
    )
    require_genesis_text_safety(finalized_result, protected_values)
    agent_id = genesis_agent_id(finalized, context="agent_genesis_finalize")
    log(
        "ok agent_genesis_finalize "
        f"agent_id={safe_identifier(agent_id)} status=published_hosted_offchain "
        f"payloadB={client.last_response_bytes}"
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
        listed, listed_result = client.tool_call("agent_list", list_args)
        require_genesis_text_safety(listed_result, protected_values)
        list_pages += 1
        agents = listed.get("agents") if isinstance(listed.get("agents"), list) else []
        total_seen += len(agents)
        for item in agents:
            if visible_agent_matches(item, agent_id, local_id):
                seen = True
                break
        pagination = listed.get("pagination") if isinstance(listed.get("pagination"), dict) else {}
        has_more = pagination.get("has_more")
        cursor = str(pagination.get("next_cursor") or "").strip()
        if seen or not has_more or not cursor or list_pages >= 10:
            break
    if not seen:
        raise CanaryError(
            "agent_list did not expose the Host-published agent within 10 pages; "
            "check Lesser public-directory propagation after Host finalize"
        )
    log(
        "ok agent_list "
        f"pages={list_pages} seen_entries={total_seen} has_more={safe_identifier(has_more)} "
        f"host_published_agent_visible=true payloadB={client.last_response_bytes}"
    )

    log("canary passed (owner-operated Ptah Host genesis conversation finalized and visible through AppTheory MCP/RFC 9728 surfaces; output sanitized)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CanaryError as exc:
        print(f"canary failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
