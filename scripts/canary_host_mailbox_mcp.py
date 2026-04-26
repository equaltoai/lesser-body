#!/usr/bin/env python3
"""Canary body MCP host-backed Soul Comm Mailbox tools.

Required environment:
  MCP_ENDPOINT        Actor-scoped MCP endpoint, for example https://api.dev.example.com/mcp/agent
  MCP_BEARER_TOKEN   OAuth access token for that actor (or set MCP_AUTHORIZATION="Bearer ...")

Optional environment:
  MAILBOX_MESSAGE_ID Message ref to use for get/content/state checks. If omitted, the
                     first email_read message is used.
  MAILBOX_QUERY      Bounded metadata/preview query for email_search (default: subject/from preview from message, else "canary")

The script intentionally redacts bearer tokens and never prints message bodies or full recipient addresses.
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


def env_required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise CanaryError(f"{name} is required")
    return value


ENDPOINT = env_required("MCP_ENDPOINT")
AUTHORIZATION = os.environ.get("MCP_AUTHORIZATION", "").strip()
if not AUTHORIZATION:
    token = env_required("MCP_BEARER_TOKEN")
    AUTHORIZATION = token if token.lower().startswith("bearer ") else f"Bearer {token}"

session_id = ""
next_id = 1


def log(message: str) -> None:
    print(message, flush=True)


def post_rpc(method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
    global next_id, session_id
    payload: dict[str, Any] = {"jsonrpc": "2.0", "id": next_id, "method": method}
    next_id += 1
    if params is not None:
        payload["params"] = params

    headers = {
        "Accept": "application/json",
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
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read().decode("utf-8")
            if not session_id:
                session_id = resp.headers.get("mcp-session-id", "").strip()
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise CanaryError(f"{method} HTTP {exc.code}: {body[:500]}") from exc
    except urllib.error.URLError as exc:
        raise CanaryError(f"{method} request failed: {exc}") from exc

    try:
        data = json.loads(body)
    except json.JSONDecodeError as exc:
        raise CanaryError(f"{method} returned non-JSON body: {body[:500]}") from exc
    if data.get("error"):
        raise CanaryError(f"{method} RPC error: {json.dumps(data['error'], sort_keys=True)}")
    return data.get("result", {})


def tool_call(name: str, arguments: dict[str, Any], *, expect_error: bool = False) -> dict[str, Any]:
    result = post_rpc("tools/call", {"name": name, "arguments": arguments})
    is_error = bool(result.get("isError"))
    structured = result.get("structuredContent") or {}
    if expect_error:
        if not is_error:
            raise CanaryError(f"{name} expected tool error, got success")
        error_payload = structured.get("error") or {}
        log(f"ok {name} error_path code={error_payload.get('code', 'unknown')} status={error_payload.get('status', 'n/a')}")
        return error_payload
    if is_error:
        error_payload = structured.get("error") or result
        raise CanaryError(f"{name} tool error: {json.dumps(error_payload, sort_keys=True)[:500]}")
    data = structured.get("data")
    if not isinstance(data, dict):
        raise CanaryError(f"{name} missing structuredContent.data")
    return data


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

    email_list = tool_call("email_read", {"folder": "inbox", "limit": 5, "includeArchived": False})
    messages = email_list.get("messages") if isinstance(email_list.get("messages"), list) else []
    log(f"ok email_read count={len(messages)} hasMore={email_list.get('hasMore')} nextCursor_present={bool(email_list.get('nextCursor'))}")

    message_ref = os.environ.get("MAILBOX_MESSAGE_ID", "").strip()
    message = first_message(email_list)
    if not message_ref and message:
        message_ref = str(message.get("messageId") or message.get("messageRef") or "").strip()
    if not message_ref:
        raise CanaryError("no mailbox message found; set MAILBOX_MESSAGE_ID to validate get/content/state paths")
    log(f"using mailbox message {summarize_message(message or {'messageId': message_ref})}")

    get_data = tool_call("email_get", {"messageId": message_ref})
    got_message = get_data.get("message") if isinstance(get_data.get("message"), dict) else {}
    log(f"ok email_get {summarize_message(got_message)}")

    content_available = True
    content = got_message.get("content") if isinstance(got_message.get("content"), dict) else {}
    if content and content.get("available") is False:
        content_available = False
    if content_available:
        content_data = tool_call("email_get_content", {"messageId": message_ref})
        body = str(content_data.get("body") or "")
        log(
            "ok email_get_content "
            f"bytes={content_data.get('bytes', len(body.encode('utf-8')))} "
            f"body_len={len(body)} body_sha256_12={hashlib.sha256(body.encode('utf-8')).hexdigest()[:12]}"
        )
    else:
        log("skip email_get_content content.available=false")

    search_query = os.environ.get("MAILBOX_QUERY", "").strip()
    if not search_query:
        search_query = str(got_message.get("subject") or "").strip()[:64] or "canary"
    search_data = tool_call("email_search", {"query": search_query, "folder": "inbox", "limit": 5})
    search_messages = search_data.get("messages") if isinstance(search_data.get("messages"), list) else []
    log(f"ok email_search query_sha256_12={hashlib.sha256(search_query.encode('utf-8')).hexdigest()[:12]} count={len(search_messages)}")

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

    log("canary passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CanaryError as exc:
        print(f"canary failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
