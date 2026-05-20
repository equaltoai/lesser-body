#!/usr/bin/env python3
"""Canary body MCP Article tools end-to-end.

Required environment:
  MCP_ENDPOINT                    Actor-scoped MCP endpoint, for example https://api.dev.example.com/mcp/agent
  MCP_BEARER_TOKEN                OAuth access token for that actor (or set MCP_AUTHORIZATION="Bearer ...")
  ARTICLE_CANARY_CONFIRM_PUBLISH  Must be "true" so publishing a canary Article is explicit.

Optional environment:
  ARTICLE_CANARY_TITLE            Canary Article title (default: generated unique canary title)
  ARTICLE_CANARY_SLUG             Canary Article slug hint (default: generated unique slug)
  ARTICLE_CANARY_CONTENT          Canary draft body (default: short generated Markdown body)
  ARTICLE_CANARY_CONTENT_FORMAT   MARKDOWN or HTML (default: MARKDOWN)
  ARTICLE_CANARY_PREVIEW_CHARS    Compact preview rune budget for Article tool calls (default: 80)
  ARTICLE_CANARY_MAX_OUTPUT_BYTES MCP response budget passed to Article tool calls (default: 12000)

The canary creates and publishes a real Article for the authenticated actor. It intentionally uses compact MCP views,
redacts bearer tokens, refuses authenticated redirects, and prints only ids/URLs, payload sizes, booleans, and hashes.
It never prints draft content, rendered HTML, full tool payloads, or raw upstream error payloads.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import secrets
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from typing import Any


class CanaryError(RuntimeError):
    pass


class NoAuthenticatedRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Refuse redirects so Authorization never leaves the configured endpoint."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


NO_REDIRECT_OPENER = urllib.request.build_opener(NoAuthenticatedRedirectHandler)
SAFE_IDENTIFIER_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]{0,255}$")


def env_required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise CanaryError(f"{name} is required")
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


def env_int(name: str, *, default: int, minimum: int, maximum: int) -> int:
    raw = os.environ.get(name, "").strip()
    if raw == "":
        return default
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


ENDPOINT = env_required("MCP_ENDPOINT")
AUTHORIZATION = os.environ.get("MCP_AUTHORIZATION", "").strip()
if not AUTHORIZATION:
    token = env_required("MCP_BEARER_TOKEN")
    AUTHORIZATION = token if token.lower().startswith("bearer ") else f"Bearer {token}"

session_id = ""
next_id = 1
last_response_bytes = 0


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
        raise CanaryError(f"{method} HTTP {exc.code}: body_len={len(body)} body_sha256_12={sha12(body)}") from exc
    except urllib.error.URLError as exc:
        raise CanaryError(f"{method} request failed: {exc}") from exc

    data = decode_rpc_response(method, request_id, raw, content_type)
    if data.get("error"):
        raise CanaryError(f"{method} RPC error: {json.dumps(sanitized_error_payload(data['error']), sort_keys=True)}")
    return data.get("result", {})


def tool_call(name: str, arguments: dict[str, Any]) -> dict[str, Any]:
    result = post_rpc("tools/call", {"name": name, "arguments": arguments})
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


def required_string(value: Any, *, context: str) -> str:
    text = str(value or "").strip()
    if not text:
        raise CanaryError(f"{context} missing required string")
    return text


def require_no_forbidden_content(value: Any, *, context: str) -> None:
    """Ensure compact canary payloads did not include full content/rendered HTML fields."""
    forbidden = {"content", "renderedHtml"}
    if isinstance(value, dict):
        for key, nested in value.items():
            if key in forbidden:
                raise CanaryError(f"{context} compact payload exposed forbidden {key}")
            require_no_forbidden_content(nested, context=context)
    elif isinstance(value, list):
        for nested in value:
            require_no_forbidden_content(nested, context=context)


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


def article_tool_args(extra: dict[str, Any]) -> dict[str, Any]:
    args = dict(extra)
    args.setdefault("view", "compact")
    args.setdefault("preview_chars", PREVIEW_CHARS)
    args.setdefault("max_output_bytes", MAX_OUTPUT_BYTES)
    return args


def article_ref(data: dict[str, Any], *, context: str) -> dict[str, Any]:
    ref = data.get("articleRef")
    if not isinstance(ref, dict):
        raise CanaryError(f"{context} missing articleRef")
    return ref


CONFIRM_PUBLISH = env_bool("ARTICLE_CANARY_CONFIRM_PUBLISH")
PREVIEW_CHARS = env_int("ARTICLE_CANARY_PREVIEW_CHARS", default=80, minimum=1, maximum=2000)
MAX_OUTPUT_BYTES = env_int("ARTICLE_CANARY_MAX_OUTPUT_BYTES", default=12000, minimum=1024, maximum=262144)
CONTENT_FORMAT = os.environ.get("ARTICLE_CANARY_CONTENT_FORMAT", "MARKDOWN").strip().upper() or "MARKDOWN"
if CONTENT_FORMAT not in {"MARKDOWN", "HTML"}:
    raise CanaryError("ARTICLE_CANARY_CONTENT_FORMAT must be MARKDOWN or HTML")

nonce = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S") + "-" + secrets.token_hex(4)
default_title = f"lesser-body Article MCP canary {nonce}"
default_slug = "body-mcp-canary-" + nonce.lower()
TITLE = os.environ.get("ARTICLE_CANARY_TITLE", "").strip() or default_title
SLUG = os.environ.get("ARTICLE_CANARY_SLUG", "").strip() or default_slug
CONTENT = os.environ.get("ARTICLE_CANARY_CONTENT", "")
if not CONTENT:
    if CONTENT_FORMAT == "HTML":
        CONTENT = f"<h1>{TITLE}</h1><p>Automated lesser-body Article MCP canary. nonce={nonce}</p>"
    else:
        CONTENT = f"# {TITLE}\n\nAutomated lesser-body Article MCP canary.\n\nnonce={nonce}\n"


def main() -> int:
    if not CONFIRM_PUBLISH:
        raise CanaryError(
            "ARTICLE_CANARY_CONFIRM_PUBLISH=true is required because this canary creates and publishes a real Article"
        )

    log(f"MCP endpoint: {ENDPOINT}")
    log("Authorization: Bearer <redacted>")
    log(
        "canary input "
        f"slug={safe_identifier(SLUG)} title_sha256_12={sha12(TITLE)} "
        f"content_format={CONTENT_FORMAT} content_bytes={len(CONTENT.encode('utf-8'))} "
        f"content_sha256_12={sha12(CONTENT)} preview_chars={PREVIEW_CHARS} max_output_bytes={MAX_OUTPUT_BYTES}"
    )

    post_rpc("initialize")
    if not session_id:
        raise CanaryError("initialize did not return mcp-session-id")
    log(f"ok initialize session={session_id[:8]}…")

    tools_result = post_rpc("tools/list")
    tool_names = {tool.get("name") for tool in tools_result.get("tools", []) if isinstance(tool, dict)}
    required_tools = {"article_draft_create", "article_draft_preview", "article_draft_publish", "article_get"}
    missing = sorted(required_tools - tool_names)
    if missing:
        raise CanaryError(f"tools/list missing Article tools: {missing}")
    log("ok tools/list Article tools present")

    created = tool_call(
        "article_draft_create",
        article_tool_args(
            {
                "title": TITLE,
                "slug": SLUG,
                "content": CONTENT,
                "content_format": CONTENT_FORMAT,
            }
        ),
    )
    create_payload_bytes = last_response_bytes
    require_no_forbidden_content(created, context="article_draft_create")
    draft = created.get("draft") if isinstance(created.get("draft"), dict) else {}
    draft_ref = created.get("draftRef") if isinstance(created.get("draftRef"), dict) else {}
    draft_id = required_string(draft.get("id") or draft_ref.get("id"), context="article_draft_create draft id")
    policy = created.get("policy") if isinstance(created.get("policy"), dict) else {}
    if policy.get("autoPublishes") is not False:
        raise CanaryError("article_draft_create policy did not preserve autoPublishes=false")
    log(
        "ok article_draft_create "
        f"draft_id={safe_identifier(draft_id)} status={safe_identifier(draft.get('status'))} "
        f"omitted={omitted_count(created)} expansionTools={sorted(expansion_tool_names(created))} "
        f"payloadB={create_payload_bytes}"
    )

    preview = tool_call(
        "article_draft_preview",
        article_tool_args({"id": draft_id}),
    )
    preview_payload_bytes = last_response_bytes
    require_no_forbidden_content(preview, context="article_draft_preview")
    preview_data = preview.get("preview") if isinstance(preview.get("preview"), dict) else {}
    preview_id = required_string(preview_data.get("draftId"), context="article_draft_preview draft id")
    if preview_id != draft_id:
        raise CanaryError("article_draft_preview returned a different draftId")
    if preview_data.get("success") is not True:
        errors = preview_data.get("errors") if isinstance(preview_data.get("errors"), list) else []
        raise CanaryError(f"article_draft_preview renderer failed errors_summary={redacted_payload_summary(errors)}")
    if int(preview_data.get("renderedBytes") or 0) <= 0:
        raise CanaryError("article_draft_preview renderedBytes was empty")
    preview_policy = preview.get("policy") if isinstance(preview.get("policy"), dict) else {}
    if preview_policy.get("rendersLocally") is not False or preview_policy.get("rawDraftContentReturned") is not False:
        raise CanaryError("article_draft_preview policy did not preserve renderer/raw-content constraints")
    log(
        "ok article_draft_preview "
        f"draft_id={safe_identifier(preview_id)} renderedBytes={preview_data.get('renderedBytes')} "
        f"sourceBytes={preview_data.get('sourceBytes')} omitted={omitted_count(preview)} "
        f"expansionTools={sorted(expansion_tool_names(preview))} payloadB={preview_payload_bytes}"
    )

    published = tool_call(
        "article_draft_publish",
        article_tool_args({"id": draft_id}),
    )
    publish_payload_bytes = last_response_bytes
    require_no_forbidden_content(published, context="article_draft_publish")
    article_id = required_string(published.get("canonicalArticleId"), context="article_draft_publish canonicalArticleId")
    canonical_url = required_string(published.get("canonicalArticleUrl"), context="article_draft_publish canonicalArticleUrl")
    published_ref = article_ref(published, context="article_draft_publish")
    if required_string(published_ref.get("id"), context="article_draft_publish articleRef id") != article_id:
        raise CanaryError("article_draft_publish articleRef id did not match canonicalArticleId")
    article_slug = str(published_ref.get("slug") or "").strip()
    log(
        "ok article_draft_publish "
        f"article_id={safe_identifier(article_id)} canonical_url={safe_identifier(canonical_url)} "
        f"slug={safe_identifier(article_slug)} omitted={omitted_count(published)} payloadB={publish_payload_bytes}"
    )

    fetched = tool_call(
        "article_get",
        article_tool_args({"id": article_id}),
    )
    fetch_payload_bytes = last_response_bytes
    require_no_forbidden_content(fetched, context="article_get")
    fetched_id = required_string(fetched.get("canonicalArticleId"), context="article_get canonicalArticleId")
    fetched_url = required_string(fetched.get("canonicalArticleUrl"), context="article_get canonicalArticleUrl")
    if fetched_id != article_id:
        raise CanaryError("article_get canonicalArticleId did not match published Article id")
    if fetched_url != canonical_url:
        raise CanaryError("article_get canonicalArticleUrl did not match published Article URL")
    fetched_ref = article_ref(fetched, context="article_get")
    if required_string(fetched_ref.get("id"), context="article_get articleRef id") != article_id:
        raise CanaryError("article_get articleRef id did not match canonicalArticleId")
    log(
        "ok article_get canonical_fetch "
        f"article_id={safe_identifier(fetched_id)} canonical_url={safe_identifier(fetched_url)} "
        f"omitted={omitted_count(fetched)} expansionTools={sorted(expansion_tool_names(fetched))} "
        f"payloadB={fetch_payload_bytes}"
    )

    log("canary passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CanaryError as exc:
        print(f"canary failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
