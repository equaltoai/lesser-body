#!/usr/bin/env python3
"""Canary body MCP host-backed Soul Comm Mailbox tools.

Required environment:
  MCP_ENDPOINT        Actor-scoped MCP endpoint, for example https://api.dev.example.com/mcp/agent
  MCP_BEARER_TOKEN   OAuth access token for that actor (or set MCP_AUTHORIZATION="Bearer ...")

Optional environment:
  MAILBOX_MESSAGE_ID Message ref to use for get/content/state checks. If omitted, the
                     first email_read message is used.
  MAILBOX_QUERY      Bounded metadata/preview query for email_search (default: subject/from preview from message, else "canary")
  EXPECTED_IDENTITY_EMAIL Canonical managed soul email, for example agent.instance@lessersoul.ai.
  LEGACY_ALIAS_EMAIL Legacy bare alias that must not be exposed as the current identity email.
  IDENTITY_LOOKUP_QUERY identity_lookup query to verify; defaults to identity_whoami localId when available.
  MAILBOX_CONFIRM_MUTATIONS Set true before running optional email_send/email_reply checks.
  CANARY_SEND_EMAIL_TO Recipient for optional email_send check.
  CANARY_CONFIRM_EMAIL_REPLY Set true before running optional email_reply against the selected mailbox message.
  CANARY_REPLY_MESSAGE_ID Optional mailbox messageRef for email_reply; defaults to MAILBOX_MESSAGE_ID/first message.
  LEGACY_ALIAS_INBOUND_CONFIRMED Set true when separate Host/provider evidence confirmed legacy alias inbound delivery.

The script intentionally redacts bearer tokens and never prints message bodies, raw upstream payloads, or full recipient
addresses. Compact-view checks print only payload sizes, opaque message refs, state booleans, and content hashes.
"""

from __future__ import annotations

import hashlib
import json
import os
import sys
import time
import urllib.error
import urllib.request
from typing import Any


class CanaryError(RuntimeError):
    pass


class NoAuthenticatedRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Refuse redirects so Authorization never leaves the configured endpoint."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


NO_REDIRECT_OPENER = urllib.request.build_opener(NoAuthenticatedRedirectHandler)


def env_required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise CanaryError(f"{name} is required")
    return value


def env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def env_bool(name: str) -> bool:
    return env(name).lower() in ("1", "true", "yes", "on")


ENDPOINT = env_required("MCP_ENDPOINT")
AUTHORIZATION = env("MCP_AUTHORIZATION")
if not AUTHORIZATION:
    token = env_required("MCP_BEARER_TOKEN")
    AUTHORIZATION = token if token.lower().startswith("bearer ") else f"Bearer {token}"

session_id = ""
next_id = 1
last_response_bytes = 0


def log(message: str) -> None:
    print(message, flush=True)


def redacted_payload_summary(value: Any) -> str:
    try:
        raw = json.dumps(value, sort_keys=True, separators=(",", ":"))
    except (TypeError, ValueError):
        raw = str(value)
    digest = hashlib.sha256(raw.encode("utf-8", errors="replace")).hexdigest()[:12]
    return f"len={len(raw)} sha256_12={digest}"


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
    if not safe:
        safe["payload"] = redacted_payload_summary(value)
    return safe


def authenticated_open(req: urllib.request.Request, *, timeout: int):  # type: ignore[no-untyped-def]
    return NO_REDIRECT_OPENER.open(req, timeout=timeout)


def is_redirect_status(status: int) -> bool:
    return 300 <= int(status) <= 399


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
            digest = hashlib.sha256(raw).hexdigest()[:12]
            raise CanaryError(f"{method} returned non-JSON body: len={len(raw)} sha256_12={digest}") from exc

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


def post_rpc(method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
    global next_id, session_id, last_response_bytes
    request_id = next_id
    payload: dict[str, Any] = {"jsonrpc": "2.0", "id": request_id, "method": method}
    next_id += 1
    if params is not None:
        payload["params"] = params

    headers = {
        "Accept": "application/json, text/event-stream",
        "Content-Type": "application/json",
        "Authorization": AUTHORIZATION,
    }
    if session_id:
        headers["mcp-session-id"] = session_id

    req = urllib.request.Request(
        ENDPOINT,
        data=json.dumps(payload).encode("utf-8"),
        headers=headers,
        method="POST",
    )
    try:
        with authenticated_open(req, timeout=30) as resp:
            raw = resp.read()
            last_response_bytes = len(raw)
            content_type = resp.headers.get("Content-Type", "")
            if not session_id:
                session_id = resp.headers.get("mcp-session-id", "").strip()
    except urllib.error.HTTPError as exc:
        if is_redirect_status(exc.code):
            raise CanaryError(f"{method} HTTP redirect {exc.code}: refusing to follow authenticated redirect") from exc
        body = exc.read()
        digest = hashlib.sha256(body).hexdigest()[:12]
        raise CanaryError(f"{method} HTTP {exc.code}: body_len={len(body)} body_sha256_12={digest}") from exc
    except urllib.error.URLError as exc:
        raise CanaryError(f"{method} request failed: {exc}") from exc

    data = decode_rpc_response(method, request_id, raw, content_type)
    if data.get("error"):
        raise CanaryError(f"{method} RPC error: {json.dumps(sanitized_error_payload(data['error']), sort_keys=True)}")
    return data.get("result", {})


def tool_call(name: str, arguments: dict[str, Any], *, expect_error: bool = False) -> dict[str, Any]:
    result = post_rpc("tools/call", {"name": name, "arguments": arguments})
    is_error = bool(result.get("isError"))
    structured = result.get("structuredContent") or {}
    if expect_error:
        if not is_error:
            raise CanaryError(f"{name} expected tool error, got success")
        error_payload = structured.get("error") or {}
        safe_error = sanitized_error_payload(error_payload)
        log(f"ok {name} error_path code={safe_error.get('code', 'unknown')} status={safe_error.get('status', 'n/a')}")
        return error_payload
    if is_error:
        error_payload = structured.get("error") or result
        raise CanaryError(f"{name} tool error: {json.dumps(sanitized_error_payload(error_payload), sort_keys=True)}")
    data = structured.get("data")
    if isinstance(data, dict):
        return data
    if isinstance(structured, dict):
        return structured
    raise CanaryError(f"{name} missing structuredContent")


def expansion_tool_names(value: Any) -> set[str]:
    names: set[str] = set()
    if isinstance(value, dict):
        tool_name = value.get("tool")
        if isinstance(tool_name, str) and tool_name:
            names.add(tool_name)
        for nested in value.values():
            names.update(expansion_tool_names(nested))
    elif isinstance(value, list):
        for nested in value:
            names.update(expansion_tool_names(nested))
    return names


def omitted_count(value: Any) -> int:
    total = 0
    if isinstance(value, dict):
        omitted = value.get("omitted")
        if isinstance(omitted, list):
            total += len(omitted)
        for nested in value.values():
            total += omitted_count(nested)
    elif isinstance(value, list):
        for nested in value:
            total += omitted_count(nested)
    return total


def summarize_message(message: dict[str, Any]) -> str:
    ref = str(message.get("messageId") or message.get("messageRef") or "")
    channel = str(message.get("channel") or message.get("channelType") or "")
    state = message.get("state") if isinstance(message.get("state"), dict) else {}
    content = message.get("content") if isinstance(message.get("content"), dict) else {}
    preview = str(message.get("preview") or message.get("body") or "")
    preview_hash = hashlib.sha256(preview.encode("utf-8")).hexdigest()[:12] if preview else "none"
    return (
        f"ref={ref} channel={channel} read={state.get('read', 'n/a')} "
        f"archived={state.get('archived', 'n/a')} content_available={content.get('available', 'n/a')} "
        f"preview_sha256_12={preview_hash}"
    )


def first_message(data: dict[str, Any]) -> dict[str, Any] | None:
    messages = data.get("messages")
    if not isinstance(messages, list) or not messages:
        return None
    first = messages[0]
    return first if isinstance(first, dict) else None


def email_address_from_identity(value: dict[str, Any]) -> str:
    email = value.get("email") if isinstance(value.get("email"), dict) else {}
    address = str(email.get("address") or "").strip()
    if address:
        return address
    channels = value.get("channels") if isinstance(value.get("channels"), dict) else {}
    channel_email = channels.get("email") if isinstance(channels.get("email"), dict) else {}
    return str(channel_email.get("address") or "").strip()


def require_expected_identity_email(label: str, address: str, expected: str, legacy: str) -> None:
    if expected and address.lower() != expected.lower():
        raise CanaryError(f"{label} email mismatch: got_present={bool(address)} expected={expected}")
    if legacy and address.lower() == legacy.lower():
        raise CanaryError(f"{label} exposed legacy bare alias as current identity email")


def require_standard_message_shape(message: dict[str, Any], context: str) -> None:
    if not message:
        return
    if not str(message.get("messageId") or message.get("messageRef") or "").strip():
        raise CanaryError(f"{context} missing messageId/messageRef compatibility alias")
    if not str(message.get("channel") or message.get("channelType") or "").strip():
        raise CanaryError(f"{context} missing channel/channelType compatibility alias")
    if "_raw" in message or "raw" in message:
        raise CanaryError(f"{context} exposed raw payload without include_raw")
    if "body" not in message and "preview" not in message:
        raise CanaryError(f"{context} missing preview/body compatibility field")


def compact_message_ref(message: dict[str, Any]) -> str:
    return str(message.get("messageRef") or "").strip()


def require_compact_email_shape(data: dict[str, Any]) -> str:
    messages = data.get("messages") if isinstance(data.get("messages"), list) else []
    dict_messages = [item for item in messages if isinstance(item, dict)]
    if not dict_messages:
        return ""
    for index, item in enumerate(dict_messages):
        for forbidden in ("body", "_raw", "raw"):
            if forbidden in item:
                raise CanaryError(f"email_read compact message {index} exposed forbidden {forbidden}")
    message = dict_messages[0]
    message_ref = compact_message_ref(message)
    if not message_ref:
        raise CanaryError("email_read compact first message missing canonical messageRef")
    tools = expansion_tool_names(message)
    missing = {"email_get", "email_get_content"} - tools
    if missing:
        raise CanaryError(f"email_read compact missing expansion tools: {sorted(missing)}")
    expand = message.get("expand") if isinstance(message.get("expand"), dict) else {}
    for label, tool_name in (("metadata", "email_get"), ("content", "email_get_content")):
        ref = expand.get(label) if isinstance(expand.get(label), dict) else {}
        args = ref.get("arguments") if isinstance(ref.get("arguments"), dict) else {}
        if ref.get("tool") != tool_name or args.get("messageId") != message_ref:
            raise CanaryError(f"email_read compact {label} expansion does not target {tool_name} with messageRef")
    return message_ref


def main() -> int:
    log(f"MCP endpoint: {ENDPOINT}")
    log("Authorization: Bearer <redacted>")

    post_rpc("initialize")
    if not session_id:
        raise CanaryError("initialize did not return mcp-session-id")
    log(f"ok initialize session={session_id[:8]}…")

    tools_result = post_rpc("tools/list")
    tool_names = {tool.get("name") for tool in tools_result.get("tools", []) if isinstance(tool, dict)}
    required_tools = {
        "identity_whoami",
        "identity_lookup",
        "email_read",
        "email_get",
        "email_get_content",
        "email_search",
        "email_mark_read",
        "email_mark_unread",
        "sms_read",
        "voicemail_read",
    }
    missing = sorted(required_tools - tool_names)
    if missing:
        raise CanaryError(f"tools/list missing host mailbox tools: {missing}")
    log("ok tools/list host mailbox tools present")

    body_mcp_evidence: dict[str, Any] = {
        "identity_whoami": False,
        "identity_lookup": False,
        "email_send": False,
        "email_reply": False,
        "email_read": False,
        "email_get": False,
        "email_get_content": False,
        "email_search": False,
        "legacy_alias_inbound": env_bool("LEGACY_ALIAS_INBOUND_CONFIRMED"),
    }
    expected_identity_email = env("EXPECTED_IDENTITY_EMAIL")
    legacy_alias_email = env("LEGACY_ALIAS_EMAIL")

    whoami = tool_call("identity_whoami", {})
    whoami_email = email_address_from_identity(whoami)
    require_expected_identity_email("identity_whoami", whoami_email, expected_identity_email, legacy_alias_email)
    body_mcp_evidence["identity_whoami"] = True
    if whoami_email:
        body_mcp_evidence["identity_whoami_email"] = whoami_email
    log(f"ok identity_whoami email_present={bool(whoami_email)}")

    identity_lookup_query = env("IDENTITY_LOOKUP_QUERY") or str(whoami.get("localId") or "").strip()
    if identity_lookup_query:
        lookup = tool_call("identity_lookup", {"query": identity_lookup_query})
        matches = lookup.get("matches") if isinstance(lookup.get("matches"), list) else []
        lookup_email = ""
        for match in matches:
            if isinstance(match, dict):
                lookup_email = email_address_from_identity(match)
                if lookup_email:
                    break
        require_expected_identity_email("identity_lookup", lookup_email, expected_identity_email, legacy_alias_email)
        body_mcp_evidence["identity_lookup"] = True
        if lookup_email:
            body_mcp_evidence["identity_lookup_email"] = lookup_email
        log(
            "ok identity_lookup "
            f"query_sha256_12={hashlib.sha256(identity_lookup_query.encode('utf-8')).hexdigest()[:12]} "
            f"matches={len(matches)} email_present={bool(lookup_email)}"
        )
    elif expected_identity_email:
        raise CanaryError("IDENTITY_LOOKUP_QUERY is required when EXPECTED_IDENTITY_EMAIL is set and identity_whoami lacks localId")
    else:
        log("skip identity_lookup IDENTITY_LOOKUP_QUERY unavailable")

    email_list = tool_call("email_read", {"folder": "inbox", "limit": 5, "includeArchived": False})
    body_mcp_evidence["email_read"] = True
    default_payload_bytes = last_response_bytes
    messages = email_list.get("messages") if isinstance(email_list.get("messages"), list) else []
    default_message = first_message(email_list)
    require_standard_message_shape(default_message or {}, "email_read default")
    log(
        "ok email_read default "
        f"count={len(messages)} hasMore={email_list.get('hasMore')} "
        f"nextCursor_present={bool(email_list.get('nextCursor'))} payloadB={default_payload_bytes}"
    )

    standard_list = tool_call("email_read", {"folder": "inbox", "limit": 5, "view": "standard", "includeArchived": False})
    standard_payload_bytes = last_response_bytes
    standard_messages = standard_list.get("messages") if isinstance(standard_list.get("messages"), list) else []
    require_standard_message_shape(first_message(standard_list) or {}, "email_read standard")
    log(
        "ok email_read standard "
        f"count={len(standard_messages)} hasMore={standard_list.get('hasMore')} "
        f"payloadB={standard_payload_bytes}"
    )

    compact_list = tool_call("email_read", {"folder": "inbox", "limit": 5, "view": "compact", "includeArchived": False})
    compact_payload_bytes = last_response_bytes
    compact_messages = compact_list.get("messages") if isinstance(compact_list.get("messages"), list) else []
    compact_ref = require_compact_email_shape(compact_list)
    log(
        "ok email_read compact "
        f"count={len(compact_messages)} messageRef_present={bool(compact_ref)} "
        f"omitted={omitted_count(compact_list)} expansionTools={sorted(expansion_tool_names(compact_list))} "
        f"payloadB={compact_payload_bytes}"
    )

    message_ref = os.environ.get("MAILBOX_MESSAGE_ID", "").strip()
    message = default_message
    if not message_ref and message:
        message_ref = str(message.get("messageId") or message.get("messageRef") or "").strip()
    if compact_ref:
        compact_get_data = tool_call("email_get", {"messageId": compact_ref})
        body_mcp_evidence["email_get"] = True
        compact_got_message = compact_get_data.get("message") if isinstance(compact_get_data.get("message"), dict) else {}
        log(f"ok compact expansion email_get {summarize_message(compact_got_message)}")
        compact_content = compact_got_message.get("content") if isinstance(compact_got_message.get("content"), dict) else {}
        if compact_content.get("available") is not False:
            compact_content_data = tool_call("email_get_content", {"messageId": compact_ref})
            body_mcp_evidence["email_get_content"] = True
            compact_body = str(compact_content_data.get("body") or "")
            log(
                "ok compact expansion email_get_content "
                f"bytes={compact_content_data.get('bytes', len(compact_body.encode('utf-8')))} "
                f"body_len={len(compact_body)} body_sha256_12={hashlib.sha256(compact_body.encode('utf-8')).hexdigest()[:12]}"
            )
        else:
            log("skip compact expansion email_get_content content.available=false")
    if not message_ref:
        raise CanaryError("no mailbox message found; set MAILBOX_MESSAGE_ID to validate get/content/state paths")
    log(f"using mailbox message {summarize_message(message or {'messageId': message_ref})}")

    get_data = tool_call("email_get", {"messageId": message_ref})
    body_mcp_evidence["email_get"] = True
    got_message = get_data.get("message") if isinstance(get_data.get("message"), dict) else {}
    log(f"ok email_get {summarize_message(got_message)}")

    content_available = True
    content = got_message.get("content") if isinstance(got_message.get("content"), dict) else {}
    if content and content.get("available") is False:
        content_available = False
    if content_available:
        content_data = tool_call("email_get_content", {"messageId": message_ref})
        body_mcp_evidence["email_get_content"] = True
        body = str(content_data.get("body") or "")
        log(
            "ok email_get_content "
            f"bytes={content_data.get('bytes', len(body.encode('utf-8')))} "
            f"body_len={len(body)} body_sha256_12={hashlib.sha256(body.encode('utf-8')).hexdigest()[:12]}"
        )
    else:
        log("skip email_get_content content.available=false")

    search_query = env("MAILBOX_QUERY")
    if not search_query:
        search_query = str(got_message.get("subject") or "").strip()[:64] or "canary"
    search_data = tool_call("email_search", {"query": search_query, "folder": "inbox", "limit": 5})
    body_mcp_evidence["email_search"] = True
    search_messages = search_data.get("messages") if isinstance(search_data.get("messages"), list) else []
    log(f"ok email_search query_sha256_12={hashlib.sha256(search_query.encode('utf-8')).hexdigest()[:12]} count={len(search_messages)}")

    if env("CANARY_SEND_EMAIL_TO"):
        if not env_bool("MAILBOX_CONFIRM_MUTATIONS"):
            raise CanaryError("MAILBOX_CONFIRM_MUTATIONS=true is required before CANARY_SEND_EMAIL_TO")
        send_to = env("CANARY_SEND_EMAIL_TO")
        send_data = tool_call(
            "email_send",
            {
                "to": send_to,
                "subject": "Project 37 body MCP canary",
                "body": "Project 37 body MCP email_send canary.",
                "idempotencyKey": f"project37-body-email-send-{int(time.time())}",
            },
        )
        body_mcp_evidence["email_send"] = True
        log(f"ok email_send messageRef_present={bool(send_data.get('messageId') or send_data.get('messageRef'))} status={send_data.get('status', 'n/a')}")
    else:
        log("skip email_send CANARY_SEND_EMAIL_TO not set")

    if env_bool("CANARY_CONFIRM_EMAIL_REPLY"):
        if not env_bool("MAILBOX_CONFIRM_MUTATIONS"):
            raise CanaryError("MAILBOX_CONFIRM_MUTATIONS=true is required before CANARY_CONFIRM_EMAIL_REPLY")
        reply_ref = env("CANARY_REPLY_MESSAGE_ID") or message_ref
        if not reply_ref:
            raise CanaryError("CANARY_REPLY_MESSAGE_ID or a readable mailbox message is required for email_reply")
        reply_data = tool_call(
            "email_reply",
            {
                "messageId": reply_ref,
                "body": "Project 37 body MCP email_reply canary.",
                "idempotencyKey": f"project37-body-email-reply-{int(time.time())}",
            },
        )
        body_mcp_evidence["email_reply"] = True
        log(f"ok email_reply messageRef_present={bool(reply_data.get('messageId') or reply_data.get('messageRef'))} status={reply_data.get('status', 'n/a')}")
    else:
        log("skip email_reply CANARY_CONFIRM_EMAIL_REPLY not true")

    read_data = tool_call("email_mark_read", {"messageId": message_ref})
    read_state = read_data.get("state") if isinstance(read_data.get("state"), dict) else {}
    log(f"ok email_mark_read read={read_state.get('read', 'n/a')}")

    unread_data = tool_call("email_mark_unread", {"messageId": message_ref})
    unread_state = unread_data.get("state") if isinstance(unread_data.get("state"), dict) else {}
    log(f"ok email_mark_unread read={unread_state.get('read', 'n/a')}")

    sms_data = tool_call("sms_read", {"limit": 5, "includeArchived": False})
    sms_messages = sms_data.get("messages") if isinstance(sms_data.get("messages"), list) else []
    log(f"ok sms_read count={len(sms_messages)}")

    voice_data = tool_call("voicemail_read", {"limit": 5, "includeArchived": False})
    voice_messages = voice_data.get("messages") if isinstance(voice_data.get("messages"), list) else []
    log(f"ok voicemail_read count={len(voice_messages)}")

    missing_ref = f"canary-missing-{int(time.time())}"
    tool_call("email_get", {"messageId": missing_ref}, expect_error=True)

    log("body_mcp_evidence=" + json.dumps(body_mcp_evidence, sort_keys=True))
    log("canary passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CanaryError as exc:
        print(f"canary failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
