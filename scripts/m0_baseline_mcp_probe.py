#!/usr/bin/env python3
"""Run the Project 21 M0 baseline MCP usability probe.

Required environment:
  MCP_ENDPOINT        Actor-scoped endpoint, e.g. https://api.dev.example.com/mcp/agent
  MCP_BEARER_TOKEN   OAuth token for that actor (or MCP_AUTHORIZATION="Bearer ...")

Recommended probe inputs:
  PROBE_LOCAL_ID              Current-instance local id for identity_lookup
  PROBE_ENS                   ENS name for identity_lookup / identity_verify
  PROBE_REMOTE_AP_HANDLE      Remote ActivityPub handle, e.g. @user@example.social
  PROBE_ACTOR_URL             Canonical remote actor URL
  PROBE_EMAIL                 Sender email for fail-closed and message-scoped verification
  PROBE_PHONE                 Sender phone for fail-closed and message-scoped verification
  PROBE_MESSAGE_REF           Host mailbox messageRef / comm-delivery-* for identity_verify
  PROBE_NOTIFICATION_SINCE    RFC3339 timestamp for typed notification reads (default: now-24h)
  PROBE_M0_CLOSURE            Set true to require mutating notification workflow closure checks
  PROBE_LESSER_API_BASE_URL   Lesser API base URL for direct notification single-get probes
  PROBE_NOTIFICATION_WORKFLOW_ID Optional specific list-returned notification ID to exercise
  PROBE_NOTIFICATION_WORKFLOW_TYPES Optional comma-separated notification types to exercise separately
  PROBE_WRONG_USER_BEARER_TOKEN Optional wrong-user token for negative notification controls
  PROBE_POST_SEARCH_QUERY      Query for the compact post_search probe (default: mcp)
  PROBE_SAFE_SEND_EMAIL       Set true to run self-email send/search/readback
  PROBE_SELF_EMAIL_TO         Recipient for the safe self-email send

The script prints only probe status, timing, sizes, compact/summary omission counts, and expansion tool names. It never
prints bearer tokens, message bodies, full tool JSON, raw upstream payloads, private reachability details, or full
recipient lists.
"""

from __future__ import annotations

import datetime as dt
import json
import os
import sys
import time
import uuid
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any


class ProbeError(RuntimeError):
    pass


class NoAuthenticatedRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Refuse redirects so Authorization never leaves the configured endpoint."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


NO_REDIRECT_OPENER = urllib.request.build_opener(NoAuthenticatedRedirectHandler)


def env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def require_env(name: str) -> str:
    value = env(name)
    if not value:
        raise ProbeError(f"{name} is required")
    return value


def env_bool(name: str) -> bool:
    return env(name).lower() in ("1", "true", "yes", "on")


ENDPOINT = require_env("MCP_ENDPOINT")
AUTHORIZATION = env("MCP_AUTHORIZATION")
if not AUTHORIZATION:
    token = require_env("MCP_BEARER_TOKEN")
    AUTHORIZATION = "Bearer " + token


@dataclass
class ProbeResult:
    name: str
    status: str
    elapsed_ms: int = 0
    response_bytes: int = 0
    details: dict[str, Any] = field(default_factory=dict)


results: list[ProbeResult] = []
closure_required_names: set[str] = set()
_session_id = ""
_next_id = 1


def redact(value: str) -> str:
    value = value.strip()
    if not value:
        return ""
    if "@" in value:
        local, _, domain = value.partition("@")
        return (local[:2] + "…@" + domain) if local else "…@" + domain
    if value.startswith("+") and len(value) > 5:
        return value[:3] + "…" + value[-2:]
    if len(value) > 16:
        return value[:8] + "…" + value[-4:]
    return value


def authenticated_open(req: urllib.request.Request, *, timeout: int):  # type: ignore[no-untyped-def]
    return NO_REDIRECT_OPENER.open(req, timeout=timeout)


def is_redirect_status(status: int) -> bool:
    return 300 <= int(status) <= 399


def redirect_error(context: str, status: int) -> ProbeError:
    return ProbeError(f"{context} returned HTTP redirect {status}; refusing to follow authenticated redirect")


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


def decode_rpc_response(method: str, request_id: int, raw: bytes, status: int, content_type: str) -> dict[str, Any]:
    text = raw.decode("utf-8", errors="replace") if raw else ""
    is_sse = "text/event-stream" in content_type.lower() or text.lstrip().startswith(("event:", "data:", "id:"))
    if not is_sse:
        try:
            return json.loads(text) if raw else {}
        except json.JSONDecodeError as exc:
            raise ProbeError(f"{method} returned non-JSON HTTP {status}: {exc}") from exc

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
    raise ProbeError(
        f"{method} returned SSE HTTP {status} without a final JSON-RPC response "
        f"for id {request_id}; parsed_events={parsed_events}"
    )


def rpc(method: str, params: dict[str, Any] | None = None) -> tuple[dict[str, Any], int, int]:
    global _next_id, _session_id
    request_id = _next_id
    _next_id += 1
    payload = {"jsonrpc": "2.0", "id": request_id, "method": method}
    if params is not None:
        payload["params"] = params
    body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "Authorization": AUTHORIZATION,
    }
    if _session_id:
        headers["Mcp-Session-Id"] = _session_id
    req = urllib.request.Request(ENDPOINT, data=body, headers=headers, method="POST")
    started = time.perf_counter()
    content_type = ""
    try:
        with authenticated_open(req, timeout=130) as resp:
            raw = resp.read()
            content_type = resp.headers.get("Content-Type", "")
            if resp.headers.get("Mcp-Session-Id"):
                _session_id = resp.headers["Mcp-Session-Id"].strip()
            status = resp.status
    except urllib.error.HTTPError as exc:
        if is_redirect_status(exc.code):
            raise redirect_error(method, exc.code) from exc
        raw = exc.read()
        content_type = exc.headers.get("Content-Type", "") if exc.headers else ""
        status = exc.code
    elapsed_ms = round((time.perf_counter() - started) * 1000)
    parsed = decode_rpc_response(method, request_id, raw, status, content_type)
    return parsed, elapsed_ms, len(raw)


def lesser_api_base() -> str:
    return env("PROBE_LESSER_API_BASE_URL").rstrip("/")


def api_json(
    method: str,
    path: str,
    authorization: str = AUTHORIZATION,
    body_value: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], int, int, int]:
    base = lesser_api_base()
    if not base:
        raise ProbeError("PROBE_LESSER_API_BASE_URL is required for notification workflow closure probes")
    body = None
    if body_value is not None:
        body = json.dumps(body_value, separators=(",", ":")).encode("utf-8")
    headers = {
        "Accept": "application/json",
        "Authorization": authorization,
    }
    if body is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(base + path, data=body, headers=headers, method=method)
    started = time.perf_counter()
    try:
        with authenticated_open(req, timeout=130) as resp:
            raw = resp.read()
            status = resp.status
    except urllib.error.HTTPError as exc:
        if is_redirect_status(exc.code):
            elapsed_ms = round((time.perf_counter() - started) * 1000)
            return {}, exc.code, elapsed_ms, 0
        raw = exc.read()
        status = exc.code
    elapsed_ms = round((time.perf_counter() - started) * 1000)
    try:
        parsed = json.loads(raw.decode("utf-8")) if raw else {}
    except json.JSONDecodeError:
        parsed = {}
    return parsed, status, elapsed_ms, len(raw)


def tool_result(parsed: dict[str, Any]) -> dict[str, Any]:
    if parsed.get("error"):
        raise ProbeError(f"JSON-RPC error: {parsed['error']}")
    result = parsed.get("result")
    if not isinstance(result, dict):
        raise ProbeError(f"missing tool result: {parsed}")
    return result


def structured(result: dict[str, Any]) -> dict[str, Any]:
    value = result.get("structuredContent")
    if not isinstance(value, dict):
        return {}
    data = value.get("data")
    if isinstance(data, dict):
        return data
    return value


def tool(name: str, arguments: dict[str, Any] | None = None) -> tuple[dict[str, Any], dict[str, Any], int, int]:
    parsed, elapsed_ms, size = rpc("tools/call", {"name": name, "arguments": arguments or {}})
    result = tool_result(parsed)
    return result, structured(result), elapsed_ms, size


def record(name: str, status: str, elapsed_ms: int = 0, response_bytes: int = 0, **details: Any) -> None:
    clean = {k: v for k, v in details.items() if v not in (None, "", [], {})}
    results.append(ProbeResult(name=name, status=status, elapsed_ms=elapsed_ms, response_bytes=response_bytes, details=clean))
    suffix = ""
    if clean:
        suffix = " " + json.dumps(clean, sort_keys=True, separators=(",", ":"))
    print(f"{status.upper():7} {name:48} {elapsed_ms:5d}ms {response_bytes:7d}B{suffix}")


def run_probe(name: str, fn) -> None:  # type: ignore[no-untyped-def]
    try:
        fn()
    except ProbeError as exc:
        record(name, "fail", error=str(exc))
    except Exception as exc:  # noqa: BLE001 - top-level probe isolation
        record(name, "fail", error=f"{type(exc).__name__}: {exc}")


def run_closure_probe(name: str, fn) -> None:  # type: ignore[no-untyped-def]
    closure_required_names.add(name)
    run_probe(name, fn)


def skip(name: str, reason: str) -> None:
    record(name, "skip", reason=reason)


def require_input(probe_name: str, env_name: str) -> str | None:
    value = env(env_name)
    if not value:
        skip(probe_name, f"missing {env_name}")
        return None
    return value


def assert_tool_ok(result: dict[str, Any]) -> None:
    if result.get("isError") is True:
        raise ProbeError(f"tool returned isError: {structured(result).get('error', {})}")


def expect_tool_error_code(result: dict[str, Any], want_code: str) -> None:
    if result.get("isError") is not True:
        raise ProbeError(f"expected tool error {want_code}, got success")
    err = structured(result).get("error")
    if not isinstance(err, dict) or err.get("code") != want_code:
        raise ProbeError(f"expected tool error {want_code}, got {err}")


def first_message_id(data: dict[str, Any]) -> str:
    messages = data.get("messages")
    if not isinstance(messages, list) or not messages:
        return ""
    first = messages[0]
    if not isinstance(first, dict):
        return ""
    for key in ("messageId", "messageRef", "deliveryId"):
        value = str(first.get(key, "")).strip()
        if value:
            return value
    return ""


def read_count(data: dict[str, Any], key: str) -> int:
    value = data.get(key)
    if isinstance(value, list):
        return len(value)
    count = data.get("count")
    return int(count) if isinstance(count, (int, float)) else 0


def assert_verified_identity(data: dict[str, Any], context: str) -> None:
    if data.get("verified") is not True:
        reason = data.get("reason", "verified was not true")
        raise ProbeError(f"{context} did not verify: {reason}")


def assert_verified_message_identity(data: dict[str, Any], context: str) -> None:
    if data.get("messageFound") is not True:
        reason = data.get("reason", "messageFound was not true")
        raise ProbeError(f"{context} did not find the message: {reason}")
    assert_verified_identity(data, context)


def assert_notification_baseline(data: dict[str, Any], context: str) -> None:
    diagnostics = data.get("diagnostics")
    if not isinstance(diagnostics, dict):
        raise ProbeError(f"{context} missing notifications_read diagnostics")
    missing = [
        key
        for key in ("lesserAPIMs", "normalizationMs", "responseBytes", "mcpPayloadBytes")
        if key not in diagnostics
    ]
    if missing:
        raise ProbeError(f"{context} diagnostics missing fields: {', '.join(missing)}")

    notifications = data.get("notifications")
    if not isinstance(notifications, list):
        raise ProbeError(f"{context} missing notifications list")
    for index, item in enumerate(notifications):
        if not isinstance(item, dict):
            continue
        if "raw" in item or "_raw" in item:
            raise ProbeError(f"{context} notification {index} exposed raw payload by default")


def assert_mailbox_page_sane(data: dict[str, Any], context: str) -> None:
    messages = data.get("messages")
    if not isinstance(messages, list):
        raise ProbeError(f"{context} missing messages list")
    count = read_count(data, "messages")
    has_more = data.get("hasMore") is True
    next_cursor = str(data.get("nextCursor", "")).strip()
    if has_more and count == 0:
        raise ProbeError(f"{context} returned count=0 with hasMore=true")
    if has_more and not next_cursor:
        raise ProbeError(f"{context} returned hasMore=true without nextCursor")


def list_of_dicts(data: dict[str, Any], key: str) -> list[dict[str, Any]]:
    values = data.get(key)
    if not isinstance(values, list):
        return []
    return [item for item in values if isinstance(item, dict)]


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


def assert_forbidden_keys_absent(value: Any, keys: set[str], context: str) -> None:
    if isinstance(value, dict):
        for key in keys:
            if key in value:
                raise ProbeError(f"{context} exposed forbidden key {key}")
        for nested in value.values():
            assert_forbidden_keys_absent(nested, keys, context)
    elif isinstance(value, list):
        for nested in value:
            assert_forbidden_keys_absent(nested, keys, context)


def probe_compact_projection(
    name: str,
    tool_name: str,
    args: dict[str, Any],
    item_key: str,
    *,
    expected_tools: set[str] | None = None,
    forbidden_tools: set[str] | None = None,
    forbidden_keys: set[str] | None = None,
) -> None:
    result, data, elapsed, size = tool(tool_name, args)
    assert_tool_ok(result)
    items = list_of_dicts(data, item_key)
    tools = expansion_tool_names(data)
    if forbidden_tools:
        present_forbidden = sorted(tools & forbidden_tools)
        if present_forbidden:
            raise ProbeError(f"{name} exposed forbidden expansion tools: {', '.join(present_forbidden)}")
    if forbidden_keys:
        assert_forbidden_keys_absent(items, forbidden_keys, name)
    missing_tools = sorted((expected_tools or set()) - tools) if items else []
    if missing_tools:
        raise ProbeError(f"{name} missing expansion tools: {', '.join(missing_tools)}")
    record(
        name,
        "pass",
        elapsed,
        size,
        view=data.get("view"),
        count=len(items),
        omitted=omitted_count(data),
        expansionTools=sorted(tools),
    )


def probe_email_compact_projection() -> None:
    result, data, elapsed, size = tool("email_read", {"folder": "inbox", "limit": 10, "view": "compact"})
    assert_tool_ok(result)
    assert_mailbox_page_sane(data, "email_read compact")
    messages = list_of_dicts(data, "messages")
    tools = expansion_tool_names(data)
    assert_forbidden_keys_absent(messages, {"body", "_raw", "raw"}, "email_read compact")
    has_ref = False
    if messages:
        first = messages[0]
        has_ref = bool(str(first.get("messageRef", "")).strip())
        missing = {"email_get", "email_get_content"} - tools
        if not has_ref:
            raise ProbeError("email_read compact first message missing canonical messageRef")
        if missing:
            raise ProbeError(f"email_read compact missing expansion tools: {', '.join(sorted(missing))}")
    record(
        "email_read compact expansion",
        "pass",
        elapsed,
        size,
        count=len(messages),
        messageRef=has_ref,
        omitted=omitted_count(data),
        expansionTools=sorted(tools),
    )


def notifications_list(data: dict[str, Any]) -> list[dict[str, Any]]:
    values = data.get("notifications")
    if not isinstance(values, list):
        return []
    return [item for item in values if isinstance(item, dict)]


def notification_id(item: dict[str, Any]) -> str:
    for key in ("id", "notificationId", "notification_id"):
        value = str(item.get(key, "")).strip()
        if value:
            return value
    return ""


def find_notification(data: dict[str, Any], wanted_id: str) -> dict[str, Any] | None:
    for item in notifications_list(data):
        if notification_id(item) == wanted_id:
            return item
    return None


def extract_notification(value: dict[str, Any]) -> dict[str, Any]:
    for key in ("notification", "data"):
        nested = value.get(key)
        if isinstance(nested, dict):
            return nested
    return value


def notification_readish_state(item: dict[str, Any]) -> dict[str, Any]:
    state: dict[str, Any] = {}
    for key in ("read", "isRead", "is_read", "unread", "isUnread", "is_unread", "dismissed", "isDismissed", "is_dismissed"):
        if key in item:
            state[key] = item.get(key)
    for key in ("readAt", "read_at", "dismissedAt", "dismissed_at"):
        value = str(item.get(key, "")).strip()
        if value:
            state[key] = value
    return state


def notification_read_value(item: dict[str, Any]) -> bool | None:
    for key in ("read", "isRead", "is_read"):
        value = item.get(key)
        if isinstance(value, bool):
            return value
    for key in ("unread", "isUnread", "is_unread"):
        value = item.get(key)
        if isinstance(value, bool):
            return not value
    return None


def is_read_or_dismissed(item: dict[str, Any]) -> bool:
    read = notification_read_value(item)
    if read is True:
        return True
    if read is False:
        return False
    for key in ("dismissed", "isDismissed", "is_dismissed"):
        if item.get(key) is True:
            return True
    for key in ("readAt", "read_at", "dismissedAt", "dismissed_at"):
        value = str(item.get(key, "")).strip()
        if value:
            return True
    return False


def wrong_user_authorization() -> str:
    value = env("PROBE_WRONG_USER_AUTHORIZATION")
    if value:
        return value
    token = env("PROBE_WRONG_USER_BEARER_TOKEN")
    if token:
        return "Bearer " + token
    return ""


def workflow_read_args(notification_type: str = "") -> dict[str, Any]:
    limit = 20
    raw_limit = env("PROBE_NOTIFICATION_WORKFLOW_LIMIT")
    if raw_limit:
        try:
            limit = max(1, min(80, int(raw_limit)))
        except ValueError as exc:
            raise ProbeError(f"invalid PROBE_NOTIFICATION_WORKFLOW_LIMIT {raw_limit!r}") from exc
    args: dict[str, Any] = {"limit": limit, "include_diagnostics": True}
    if notification_type:
        args["types"] = [notification_type]
    return args


def workflow_uses_unread_only_view() -> bool:
    return env_bool("PROBE_NOTIFICATION_WORKFLOW_UNREAD_ONLY_VIEW")


def probe_verified_identity(name: str, channel: str, identifier: str, message_id: str = "") -> None:
    args = {"channel": channel, "identifier": identifier}
    if message_id:
        args["messageId"] = message_id
    result, data, elapsed, size = tool("identity_verify", args)
    assert_tool_ok(result)
    if message_id:
        assert_verified_message_identity(data, name)
    else:
        assert_verified_identity(data, name)
    details = {
        "verified": data.get("verified"),
        "matchedBy": data.get("matchedBy"),
    }
    message = data.get("message")
    if isinstance(message, dict):
        details["messageSource"] = message.get("source")
    record(name, "pass", elapsed, size, **details)


def probe_notifications_read(name: str, args: dict[str, Any]) -> None:
    result, data, elapsed, size = tool("notifications_read", args)
    assert_tool_ok(result)
    assert_notification_baseline(data, name)
    diagnostics = data.get("diagnostics")
    details: dict[str, Any] = {
        "count": read_count(data, "notifications"),
    }
    if isinstance(diagnostics, dict):
        details["diagnostics"] = {
            k: diagnostics.get(k)
            for k in ("lesserAPIMs", "normalizationMs", "responseBytes", "mcpPayloadBytes")
            if k in diagnostics
        }
    record(name, "pass", elapsed, size, **details)


def probe_notification_workflow(name: str, args: dict[str, Any]) -> None:
    total_elapsed = 0
    total_size = 0

    result, listed, elapsed, size = tool("notifications_read", args)
    total_elapsed += elapsed
    total_size += size
    assert_tool_ok(result)
    assert_notification_baseline(listed, name + " list")

    wanted_id = env("PROBE_NOTIFICATION_WORKFLOW_ID")
    selected = find_notification(listed, wanted_id) if wanted_id else None
    if wanted_id and selected is None:
        raise ProbeError(f"configured notification {redact(wanted_id)} was not present in the list result")
    if selected is None:
        notifications = notifications_list(listed)
        if not notifications:
            raise ProbeError("notification workflow requires at least one list-returned notification")
        selected = notifications[0]

    selected_id = notification_id(selected)
    if not selected_id:
        raise ProbeError("selected list-returned notification had no id")
    encoded_id = urllib.parse.quote(selected_id, safe="")
    path = f"/api/v1/notifications/{encoded_id}"

    before, status, elapsed, size = api_json("GET", path)
    total_elapsed += elapsed
    total_size += size
    if status != 200:
        raise ProbeError(f"same-user single-get for list-returned notification returned HTTP {status}")
    single_get_status = status
    before_notification = extract_notification(before)

    wrong_auth = wrong_user_authorization()
    wrong_user_checked = False
    if wrong_auth:
        _wrong_before, wrong_status, elapsed, size = api_json("GET", path, wrong_auth)
        total_elapsed += elapsed
        total_size += size
        if wrong_status not in (403, 404):
            raise ProbeError(f"wrong-user single-get returned HTTP {wrong_status}, expected 403/404")

        _wrong_dismiss, wrong_status, elapsed, size = api_json("POST", path + "/dismiss", wrong_auth, {})
        total_elapsed += elapsed
        total_size += size
        if wrong_status not in (403, 404):
            raise ProbeError(f"wrong-user dismiss returned HTTP {wrong_status}, expected 403/404")

        _same_after_wrong, status, elapsed, size = api_json("GET", path)
        total_elapsed += elapsed
        total_size += size
        if status != 200:
            raise ProbeError("wrong-user dismiss changed same-user single-get visibility")
        wrong_user_checked = True

    dismiss_result, dismiss_data, elapsed, size = tool("notification_dismiss", {"id": selected_id})
    total_elapsed += elapsed
    total_size += size
    assert_tool_ok(dismiss_result)
    if dismiss_data.get("ok") is not True:
        raise ProbeError(f"notification_dismiss did not report ok:true: {dismiss_data}")

    after, after_status, elapsed, size = api_json("GET", path)
    total_elapsed += elapsed
    total_size += size

    result, relisted, elapsed, size = tool("notifications_read", args)
    total_elapsed += elapsed
    total_size += size
    assert_tool_ok(result)
    assert_notification_baseline(relisted, name + " follow-up list")

    relisted_notification = find_notification(relisted, selected_id)
    still_listed = relisted_notification is not None
    follow_up_read: bool | None = None
    transition = ""
    after_state: dict[str, Any] = {}
    if after_status == 200:
        after_notification = extract_notification(after)
        after_state = notification_readish_state(after_notification)
        if not is_read_or_dismissed(after_notification):
            raise ProbeError("direct Lesser single-get did not show read/dismissed state after notification_dismiss")
    elif after_status == 404 and not still_listed:
        raise ProbeError("follow-up same-user single-get returned HTTP 404 after notification_dismiss")
    elif after_status != 200:
        raise ProbeError(f"follow-up same-user single-get returned HTTP {after_status}")

    if still_listed:
        assert relisted_notification is not None
        follow_up_read = notification_read_value(relisted_notification)
        if follow_up_read is not True:
            raise ProbeError(
                "follow-up notifications_read kept the notification visible but did not expose read:true"
            )
        transition = "notifications_read_read_state"
    elif workflow_uses_unread_only_view():
        transition = "unread_view_absence"
    else:
        raise ProbeError(
            "follow-up notifications_read did not include the notification; current all-notifications closure expects the same ID to remain visible with read:true"
        )

    if not transition:
        raise ProbeError("follow-up list/read-state did not reflect notification dismiss/mark-read")

    details = {
        "id": redact(selected_id),
        "type": selected.get("type") or before_notification.get("type"),
        "initialCount": read_count(listed, "notifications"),
        "followUpCount": read_count(relisted, "notifications"),
        "singleGetStatus": single_get_status,
        "dismiss": dismiss_data.get("dismiss"),
        "followUpGetStatus": after_status,
        "followUpInList": still_listed,
        "followUpRead": follow_up_read,
        "transition": transition,
        "beforeState": notification_readish_state(before_notification),
        "afterState": after_state,
        "wrongUserNegativeControl": wrong_user_checked,
    }
    record(name, "pass", total_elapsed, total_size, **details)


def probe_mailbox_read(name: str, tool_name: str, args: dict[str, Any]) -> tuple[dict[str, Any], int, int]:
    result, data, elapsed, size = tool(tool_name, args)
    assert_tool_ok(result)
    assert_mailbox_page_sane(data, name)
    record(
        name,
        "pass",
        elapsed,
        size,
        count=read_count(data, "messages"),
        hasMore=data.get("hasMore"),
        nextCursor=bool(str(data.get("nextCursor", "")).strip()),
    )
    return data, elapsed, size


def notification_since_default() -> str:
    return (dt.datetime.now(dt.UTC) - dt.timedelta(hours=24)).isoformat().replace("+00:00", "Z")


def init() -> None:
    parsed, elapsed, size = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "lesser-body-m0-probe", "version": "1"}})
    if parsed.get("error"):
        raise ProbeError(f"initialize failed: {parsed['error']}")
    record("initialize", "pass", elapsed, size, session=bool(_session_id))


def probe_tool_success(name: str, tool_name: str, args: dict[str, Any] | None = None, count_key: str = "") -> None:
    result, data, elapsed, size = tool(tool_name, args)
    assert_tool_ok(result)
    details: dict[str, Any] = {}
    if count_key:
        details["count"] = read_count(data, count_key)
    diagnostics = data.get("diagnostics")
    if isinstance(diagnostics, dict):
        details["diagnostics"] = {k: diagnostics.get(k) for k in ("lesserAPIMs", "normalizationMs", "responseBytes", "mcpPayloadBytes") if k in diagnostics}
    record(name, "pass", elapsed, size, **details)


def main() -> int:
    closure_mode = env_bool("PROBE_M0_CLOSURE")
    print("Project 21 M0 baseline MCP usability probe")
    print(f"endpoint={ENDPOINT}")
    print(f"mode={'closure' if closure_mode else 'smoke'}")
    run_probe("initialize", init)
    if not _session_id:
        print("initialize failed before session establishment", file=sys.stderr)
        return 1

    probe_tool_success("identity_whoami", "identity_whoami")

    for probe_name, env_name in [
        ("identity_lookup local ID", "PROBE_LOCAL_ID"),
        ("identity_lookup ENS", "PROBE_ENS"),
        ("identity_lookup remote AP handle", "PROBE_REMOTE_AP_HANDLE"),
        ("identity_lookup actor URL", "PROBE_ACTOR_URL"),
    ]:
        value = require_input(probe_name, env_name)
        if value:
            run_probe(probe_name, lambda value=value, probe_name=probe_name: probe_tool_success(probe_name, "identity_lookup", {"query": value}, "matches"))

    ens = env("PROBE_ENS")
    if ens:
        run_probe("identity_verify ENS identifier", lambda: probe_verified_identity("identity_verify ENS identifier", "ens", ens))
    else:
        skip("identity_verify ENS identifier", "missing PROBE_ENS")

    for channel, env_name in [("email", "PROBE_EMAIL"), ("phone", "PROBE_PHONE")]:
        value = env(env_name)
        if not value:
            skip(f"identity_verify {channel} fail-closed", f"missing {env_name}")
            continue
        def fail_closed(channel: str = channel, value: str = value) -> None:
            result, _data, elapsed, size = tool("identity_verify", {"channel": channel, "identifier": value})
            expect_tool_error_code(result, "private_reachability_unavailable")
            record(f"identity_verify {channel} fail-closed", "pass", elapsed, size, identifier=redact(value))
        run_probe(f"identity_verify {channel} fail-closed", fail_closed)

    message_ref = env("PROBE_MESSAGE_REF")
    if message_ref:
        if ens:
            run_probe("identity_verify ENS messageRef", lambda: probe_verified_identity("identity_verify ENS messageRef", "ens", ens, message_ref))
        for channel, env_name in [("email", "PROBE_EMAIL"), ("phone", "PROBE_PHONE")]:
            value = env(env_name)
            if value:
                run_probe(
                    f"identity_verify {channel} messageRef",
                    lambda channel=channel, value=value: probe_verified_identity(
                        f"identity_verify {channel} messageRef", channel, value, message_ref
                    ),
                )
    else:
        skip("identity_verify messageRef", "missing PROBE_MESSAGE_REF")

    run_probe("notifications_read limit=20", lambda: probe_notifications_read("notifications_read limit=20", {"limit": 20, "include_diagnostics": True}))
    run_probe(
        "notifications_read compact expansion",
        lambda: probe_compact_projection(
            "notifications_read compact expansion",
            "notifications_read",
            {"limit": 10, "view": "compact"},
            "notifications",
            expected_tools={"notification_get"},
            forbidden_keys={"raw", "_raw"},
        ),
    )
    run_probe(
        "notifications_read since empty limit=30",
        lambda: probe_notifications_read("notifications_read since empty limit=30", {"since": "", "limit": 30, "include_diagnostics": True}),
    )
    since = env("PROBE_NOTIFICATION_SINCE", notification_since_default())
    for typ in ("mention", "reply"):
        run_probe(
            f"notifications_read {typ} timestamp",
            lambda typ=typ: probe_notifications_read(
                f"notifications_read {typ} timestamp", {"since": since, "limit": 30, "types": [typ], "include_diagnostics": True}
            ),
        )

    if closure_mode:
        workflow_types = [part.strip() for part in env("PROBE_NOTIFICATION_WORKFLOW_TYPES").split(",") if part.strip()]
        if workflow_types:
            for typ in workflow_types:
                run_closure_probe(
                    f"notification workflow {typ}",
                    lambda typ=typ: probe_notification_workflow(
                        f"notification workflow {typ}", workflow_read_args(typ)
                    ),
                )
        else:
            run_closure_probe(
                "notification workflow list->get->dismiss->read-state",
                lambda: probe_notification_workflow(
                    "notification workflow list->get->dismiss->read-state", workflow_read_args()
                ),
            )
    else:
        skip("notification workflow closure gate", "set PROBE_M0_CLOSURE=true after lesser#944 is deployed")

    inbox_message_id = ""
    for folder in ("inbox", "sent"):
        def email_read(folder: str = folder) -> None:
            nonlocal inbox_message_id
            data, elapsed, size = probe_mailbox_read(f"email_read {folder} limit=20", "email_read", {"folder": folder, "limit": 20})
            if folder == "inbox":
                inbox_message_id = first_message_id(data)
        run_probe(f"email_read {folder} limit=20", email_read)

    if inbox_message_id:
        run_probe("email_get_content listed message", lambda: probe_tool_success("email_get_content listed message", "email_get_content", {"messageId": inbox_message_id}))
    else:
        skip("email_get_content listed message", "email_read inbox returned no messageId")

    run_probe("email_read compact expansion", probe_email_compact_projection)

    if env("PROBE_SAFE_SEND_EMAIL").lower() == "true":
        to = require_input("self-email send/search/readback", "PROBE_SELF_EMAIL_TO")
        if to:
            message_id = "m0-probe-" + uuid.uuid4().hex
            subject = "lesser-body M0 baseline probe " + message_id[-8:]
            run_probe("self-email send", lambda: probe_tool_success("self-email send", "email_send", {"to": to, "subject": subject, "body": "M0 baseline probe self-email.", "messageId": message_id}))
            run_probe("self-email search/readback", lambda: probe_tool_success("self-email search/readback", "email_search", {"query": subject, "limit": 5}, "messages"))
    else:
        skip("self-email send/search/readback", "set PROBE_SAFE_SEND_EMAIL=true to run")

    run_probe("sms_read limit=20", lambda: probe_mailbox_read("sms_read limit=20", "sms_read", {"limit": 20}))
    run_probe("voicemail_read limit=20", lambda: probe_mailbox_read("voicemail_read limit=20", "voicemail_read", {"limit": 20}))

    event_id = "01" + uuid.uuid4().hex[:24].upper()
    run_probe("memory_append", lambda: probe_tool_success("memory_append", "memory_append", {"event_id": event_id, "content": "M0 baseline probe memory event", "tags": ["m0-probe"]}))
    probe_tool_success("memory_query", "memory_query", {"query": "M0 baseline probe", "limit": 5}, "events")

    probe_tool_success("conversations_read", "conversations_read", {"limit": 20}, "conversations")
    run_probe(
        "conversations_read compact expansion",
        lambda: probe_compact_projection(
            "conversations_read compact expansion",
            "conversations_read",
            {"limit": 10, "view": "compact"},
            "conversations",
            expected_tools={"conversation_get"},
            forbidden_keys={"raw", "_raw"},
        ),
    )
    run_probe(
        "soul_read summary expansion",
        lambda: probe_compact_projection(
            "soul_read summary expansion",
            "soul_read",
            {"self": True, "view": "summary"},
            "souls",
            expected_tools={"soul_read"},
            forbidden_keys={"private", "_raw"},
        ),
    )
    probe_tool_success("timeline_read home", "timeline_read", {"timeline": "home", "limit": 20})
    run_probe(
        "timeline_read compact expansion",
        lambda: probe_compact_projection(
            "timeline_read compact expansion",
            "timeline_read",
            {"timeline": "home", "limit": 5, "view": "compact"},
            "statuses",
            expected_tools={"post_get"},
            forbidden_keys={"raw", "_raw"},
        ),
    )
    run_probe(
        "post_search compact expansion",
        lambda: probe_compact_projection(
            "post_search compact expansion",
            "post_search",
            {"query": env("PROBE_POST_SEARCH_QUERY", "mcp"), "limit": 10, "view": "compact"},
            "statuses",
            expected_tools={"post_get"},
            forbidden_keys={"raw", "_raw"},
        ),
    )

    failed = sum(1 for r in results if r.status == "fail")
    skipped = sum(1 for r in results if r.status == "skip")
    passed = sum(1 for r in results if r.status == "pass")
    by_name = {r.name: r for r in results}
    closure_ready = bool(closure_required_names) and all(
        by_name.get(name) is not None and by_name[name].status == "pass"
        for name in closure_required_names
    )
    summary = {
        "passed": passed,
        "failed": failed,
        "skipped": skipped,
        "mode": "closure" if closure_mode else "smoke",
        "closureRequired": closure_mode,
        "closureReady": closure_mode and failed == 0 and closure_ready,
        "closureProbeNames": sorted(closure_required_names),
        "results": [r.__dict__ for r in results],
    }
    print("SUMMARY " + json.dumps(summary, sort_keys=True, separators=(",", ":")))
    if failed:
        return 1
    if closure_mode and not summary["closureReady"]:
        return 1
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ProbeError as exc:
        print(f"probe setup failed: {exc}", file=sys.stderr)
        raise SystemExit(2)
