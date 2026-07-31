#!/usr/bin/env python3
"""Live submit -> queue -> verdict proof for Body's Lesser-backed CMS review tools.

Required environment:
  ARTICLE_REVIEW_AUTHOR_MCP_ENDPOINT
  ARTICLE_REVIEW_AUTHOR_BEARER_TOKEN
  ARTICLE_REVIEW_REVIEWER_MCP_ENDPOINT
  ARTICLE_REVIEW_REVIEWER_BEARER_TOKEN
  ARTICLE_REVIEW_REVIEWER_USERNAME
  ARTICLE_REVIEW_CANARY_CONFIRM_MUTATIONS=true

Optional environment:
  ARTICLE_REVIEW_DRAFT_ID          Existing author-owned draft (preferred). Omit to create a private canary draft.
  ARTICLE_REVIEW_VERDICT           APPROVED or CHANGES_REQUESTED (default: CHANGES_REQUESTED)
  ARTICLE_REVIEW_NOTES             Default: a generated non-sensitive canary note.
  ARTICLE_REVIEW_MAX_OUTPUT_BYTES  Default: 24000 for explicit non-default calls.

The probe mutates Lesser review state but never publishes. It refuses authenticated
redirects and prints only bounded identifiers, booleans, counts, and hashes. It
never prints bearer tokens, draft content, reviewer notes, or raw error payloads.
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


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


OPENER = urllib.request.build_opener(NoRedirect)
SAFE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]{0,255}$")


def required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise CanaryError(f"{name} is required")
    return value


def env_bool(name: str) -> bool:
    value = os.environ.get(name, "").strip().lower()
    if value in {"1", "true", "yes", "on"}:
        return True
    if value in {"", "0", "false", "no", "off"}:
        return False
    raise CanaryError(f"{name} must be true or false")


def env_int(name: str, default: int, minimum: int, maximum: int) -> int:
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise CanaryError(f"{name} must be an integer") from exc
    if value < minimum or value > maximum:
        raise CanaryError(f"{name} must be between {minimum} and {maximum}")
    return value


def sha12(value: str | bytes) -> str:
    raw = value.encode("utf-8", errors="replace") if isinstance(value, str) else value
    return hashlib.sha256(raw).hexdigest()[:12]


def safe_id(value: Any) -> str:
    text = str(value or "").strip()
    if text and SAFE_ID.match(text):
        return text
    if not text:
        return "<empty>"
    return f"<redacted len={len(text)} sha256_12={sha12(text)}>"


def payload_hash(value: Any) -> str:
    try:
        raw = json.dumps(value, sort_keys=True, separators=(",", ":"))
    except (TypeError, ValueError):
        raw = str(value)
    return f"len={len(raw)} sha256_12={sha12(raw)}"


def sse_events(raw: bytes) -> list[str]:
    events: list[str] = []
    lines: list[str] = []
    for line in raw.decode("utf-8", errors="replace").splitlines():
        if line == "":
            events.append("\n".join(lines))
            lines = []
        elif line.startswith("data:"):
            lines.append(line[5:].lstrip())
    if lines:
        events.append("\n".join(lines))
    return events


def decode_response(method: str, request_id: int, raw: bytes, content_type: str) -> dict[str, Any]:
    text = raw.decode("utf-8", errors="replace")
    if "text/event-stream" not in content_type.lower() and not text.lstrip().startswith(("data:", "event:", "id:")):
        try:
            value = json.loads(text) if raw else {}
        except json.JSONDecodeError as exc:
            raise CanaryError(f"{method} returned non-JSON: bytes={len(raw)} sha256_12={sha12(raw)}") from exc
        return value if isinstance(value, dict) else {}
    for event in sse_events(raw):
        try:
            value = json.loads(event)
        except json.JSONDecodeError:
            continue
        if isinstance(value, dict) and value.get("jsonrpc") == "2.0" and value.get("id") == request_id:
            return value
    raise CanaryError(f"{method} returned SSE without final response id={request_id}")


class MCPClient:
    def __init__(self, label: str, endpoint: str, bearer: str) -> None:
        self.label = label
        self.endpoint = endpoint
        self.authorization = bearer if bearer.lower().startswith("bearer ") else f"Bearer {bearer}"
        self.session_id = ""
        self.next_id = 1
        self.last_bytes = 0

    def rpc(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        request_id = self.next_id
        self.next_id += 1
        payload: dict[str, Any] = {"jsonrpc": "2.0", "id": request_id, "method": method}
        if params is not None:
            payload["params"] = params
        headers = {
            "Accept": "application/json, text/event-stream",
            "Authorization": self.authorization,
            "Content-Type": "application/json",
        }
        if self.session_id:
            headers["mcp-session-id"] = self.session_id
        req = urllib.request.Request(
            self.endpoint,
            data=json.dumps(payload).encode("utf-8"),
            headers=headers,
            method="POST",
        )
        try:
            with OPENER.open(req, timeout=30) as response:
                raw = response.read()
                self.last_bytes = len(raw)
                if not self.session_id:
                    self.session_id = response.headers.get("mcp-session-id", "").strip()
                decoded = decode_response(method, request_id, raw, response.headers.get("Content-Type", ""))
        except urllib.error.HTTPError as exc:
            body = exc.read()
            if 300 <= exc.code <= 399:
                raise CanaryError(f"{self.label} {method} refused authenticated redirect {exc.code}") from exc
            raise CanaryError(
                f"{self.label} {method} HTTP {exc.code}: bytes={len(body)} sha256_12={sha12(body)}"
            ) from exc
        except urllib.error.URLError as exc:
            raise CanaryError(f"{self.label} {method} request failed: {exc.reason}") from exc
        if decoded.get("error"):
            raise CanaryError(f"{self.label} {method} RPC error: {payload_hash(decoded['error'])}")
        result = decoded.get("result", {})
        return result if isinstance(result, dict) else {}

    def initialize(self) -> None:
        self.rpc("initialize")
        if not self.session_id:
            raise CanaryError(f"{self.label} initialize did not issue a session id")

    def tool(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        result = self.rpc("tools/call", {"name": name, "arguments": arguments})
        if result.get("isError"):
            raise CanaryError(f"{self.label} {name} tool error: {payload_hash(result)}")
        structured = result.get("structuredContent")
        if not isinstance(structured, dict) or not isinstance(structured.get("data"), dict):
            raise CanaryError(f"{self.label} {name} missing structuredContent.data")
        return structured["data"]


def review_from_single(data: dict[str, Any], operation: str) -> dict[str, Any]:
    if data.get("operation") != operation or data.get("source") != "lesser_cms_graphql":
        raise CanaryError(f"unexpected {operation} envelope: {payload_hash(data)}")
    review = data.get("review")
    if not isinstance(review, dict):
        raise CanaryError(f"{operation} missing review")
    return review


def find_queue_review(data: dict[str, Any], draft_id: str) -> dict[str, Any]:
    if data.get("mode") != "queue" or not isinstance(data.get("reviews"), list):
        raise CanaryError(f"unexpected queue envelope: {payload_hash(data)}")
    for item in data["reviews"]:
        if not isinstance(item, dict) or not isinstance(item.get("review"), dict):
            continue
        review = item["review"]
        if str(review.get("draftId") or "").strip() == draft_id:
            return review
    raise CanaryError(
        f"new draft absent from reviewer queue: count={data.get('count')} totalCount={data.get('totalCount')}"
    )


AUTHOR_ENDPOINT = required("ARTICLE_REVIEW_AUTHOR_MCP_ENDPOINT")
AUTHOR_TOKEN = required("ARTICLE_REVIEW_AUTHOR_BEARER_TOKEN")
REVIEWER_ENDPOINT = required("ARTICLE_REVIEW_REVIEWER_MCP_ENDPOINT")
REVIEWER_TOKEN = required("ARTICLE_REVIEW_REVIEWER_BEARER_TOKEN")
REVIEWER_USERNAME = required("ARTICLE_REVIEW_REVIEWER_USERNAME")
CONFIRM = env_bool("ARTICLE_REVIEW_CANARY_CONFIRM_MUTATIONS")
MAX_OUTPUT_BYTES = env_int("ARTICLE_REVIEW_MAX_OUTPUT_BYTES", 24000, 4096, 262144)
VERDICT = os.environ.get("ARTICLE_REVIEW_VERDICT", "CHANGES_REQUESTED").strip().upper()
if VERDICT not in {"APPROVED", "CHANGES_REQUESTED"}:
    raise CanaryError("ARTICLE_REVIEW_VERDICT must be APPROVED or CHANGES_REQUESTED")

NONCE = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S") + "-" + secrets.token_hex(4)
NOTES = os.environ.get("ARTICLE_REVIEW_NOTES", "").strip() or f"Body MCP review canary {NONCE}"


def main() -> int:
    if not CONFIRM:
        raise CanaryError(
            "ARTICLE_REVIEW_CANARY_CONFIRM_MUTATIONS=true is required because the probe creates review grants and verdicts"
        )

    author = MCPClient("author", AUTHOR_ENDPOINT, AUTHOR_TOKEN)
    reviewer = MCPClient("reviewer", REVIEWER_ENDPOINT, REVIEWER_TOKEN)
    print("Authorization: author=Bearer <redacted> reviewer=Bearer <redacted>", flush=True)
    print(
        "review canary input "
        f"reviewer={safe_id(REVIEWER_USERNAME)} verdict={VERDICT} notes_sha256_12={sha12(NOTES)} "
        f"max_output_bytes={MAX_OUTPUT_BYTES} publishes=false",
        flush=True,
    )

    author.initialize()
    reviewer.initialize()
    print("ok initialize author=true reviewer=true", flush=True)

    required_tools = {
        "article_draft_review_submit",
        "article_draft_review_read",
        "article_draft_review_verdict",
    }
    for client in (author, reviewer):
        listing = client.rpc("tools/list")
        names = {item.get("name") for item in listing.get("tools", []) if isinstance(item, dict)}
        missing = sorted(required_tools - names)
        if missing:
            raise CanaryError(f"{client.label} tools/list missing {missing}")
    print("ok tools/list review_tools=3 profiles=caller_resolved", flush=True)

    draft_id = os.environ.get("ARTICLE_REVIEW_DRAFT_ID", "").strip()
    if not draft_id:
        content = f"# Body MCP review canary {NONCE}\n\nUnpublished review workflow proof."
        created = author.tool(
            "article_draft_create",
            {
                "title": f"Body MCP review canary {NONCE}",
                "slug": f"body-mcp-review-{NONCE.lower()}",
                "content": content,
                "content_format": "MARKDOWN",
                "max_output_bytes": MAX_OUTPUT_BYTES,
            },
        )
        draft = created.get("draft") if isinstance(created.get("draft"), dict) else {}
        draft_ref = created.get("draftRef") if isinstance(created.get("draftRef"), dict) else {}
        draft_id = str(draft.get("id") or draft_ref.get("id") or "").strip()
        if not draft_id:
            raise CanaryError("article_draft_create did not return a draft id")
        print(
            f"ok article_draft_create draft_id={safe_id(draft_id)} unpublished=true responseB={author.last_bytes}",
            flush=True,
        )

    submitted = author.tool(
        "article_draft_review_submit",
        {"draft_id": draft_id, "reviewer": REVIEWER_USERNAME, "max_output_bytes": MAX_OUTPUT_BYTES},
    )
    submitted_review = review_from_single(submitted, "submitted")
    if str(submitted_review.get("draftId") or "").strip() != draft_id:
        raise CanaryError("submit returned a different draftId")
    print(
        "ok article_draft_review_submit "
        f"draft_id={safe_id(draft_id)} grant_present={isinstance(submitted_review.get('grant'), dict)} "
        f"responseB={author.last_bytes}",
        flush=True,
    )

    # Intentionally omit both limit and max_output_bytes: this live proof must
    # exercise the tool's documented default page and default envelope budget.
    default_queue = reviewer.tool("article_draft_review_read", {})
    if default_queue.get("mode") != "queue" or default_queue.get("limit") != 5:
        raise CanaryError(f"unexpected default queue envelope: {payload_hash(default_queue)}")
    print(
        "ok article_draft_review_read default_budget "
        f"count={default_queue.get('count')} limit={default_queue.get('limit')} responseB={reviewer.last_bytes}",
        flush=True,
    )

    try:
        queued_review = find_queue_review(default_queue, draft_id)
        queue = default_queue
    except CanaryError:
        queue = reviewer.tool(
            "article_draft_review_read", {"limit": 20, "max_output_bytes": MAX_OUTPUT_BYTES}
        )
        queued_review = find_queue_review(queue, draft_id)
    print(
        "ok article_draft_review_read queue "
        f"draft_id={safe_id(draft_id)} found=true count={queue.get('count')} totalCount={queue.get('totalCount')} "
        f"responseB={reviewer.last_bytes}",
        flush=True,
    )

    state = reviewer.tool(
        "article_draft_review_read", {"draft_id": draft_id, "max_output_bytes": MAX_OUTPUT_BYTES}
    )
    state_review = review_from_single(state, "state")
    if state.get("mode") != "state" or str(state_review.get("draftId") or "").strip() != draft_id:
        raise CanaryError("state mode returned a different draftId")
    print(
        "ok article_draft_review_read state "
        f"draft_id={safe_id(draft_id)} grant_present={isinstance(state_review.get('grant'), dict)} "
        f"verdict_count={len(state_review.get('verdicts') or [])} responseB={reviewer.last_bytes}",
        flush=True,
    )

    verdict_data = reviewer.tool(
        "article_draft_review_verdict",
        {
            "draft_id": draft_id,
            "verdict": VERDICT,
            "notes": NOTES,
            "max_output_bytes": MAX_OUTPUT_BYTES,
        },
    )
    verdict_review = review_from_single(verdict_data, "verdict_submitted")
    reviewed_by = verdict_review.get("reviewedBy") if isinstance(verdict_review.get("reviewedBy"), dict) else {}
    if verdict_review.get("reviewStatus") != VERDICT:
        raise CanaryError("verdict response did not carry the submitted reviewStatus")
    if sha12(str(verdict_review.get("editorNotes") or "")) != sha12(NOTES):
        raise CanaryError("verdict response editorNotes did not match submitted notes")
    if str(reviewed_by.get("username") or "").strip() != REVIEWER_USERNAME:
        raise CanaryError("verdict response reviewedBy did not match the reviewer")
    print(
        "ok article_draft_review_verdict "
        f"draft_id={safe_id(draft_id)} reviewStatus={VERDICT} reviewedBy={safe_id(reviewed_by.get('username'))} "
        f"notes_sha256_12={sha12(NOTES)} responseB={reviewer.last_bytes}",
        flush=True,
    )

    final_state = author.tool(
        "article_draft_review_read", {"draft_id": draft_id, "max_output_bytes": MAX_OUTPUT_BYTES}
    )
    final_review = review_from_single(final_state, "state")
    if final_review.get("reviewStatus") != VERDICT:
        raise CanaryError("author final state did not observe the reviewer verdict")
    print(
        "ok round_trip author_state_observed=true "
        f"draft_id={safe_id(draft_id)} reviewStatus={VERDICT} verdict_count={len(final_review.get('verdicts') or [])} "
        "published=false",
        flush=True,
    )
    print("canary passed submit -> queue -> state -> verdict -> author state (no publish)", flush=True)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CanaryError as exc:
        print(f"canary failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
