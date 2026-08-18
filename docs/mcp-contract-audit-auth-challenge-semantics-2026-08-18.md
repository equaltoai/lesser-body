# MCP contract audit: bearer challenge and dead-session classification

## Proposed change

Refine the authenticated MCP transport contract so an absent bearer keeps the bare OAuth discovery challenge, a
presented-but-rejected bearer adds RFC 6750 `invalid_token` plus refresh-oriented details, and a dead session on an SSE
`GET` retains AppTheory's `404 {"error":"session not found"}` lifecycle signal.

## Surfaces affected

- `/.well-known/mcp.json` discovery: unchanged.
- `/.well-known/oauth-protected-resource/mcp/<actor>` metadata: unchanged.
- Actor and instance MCP transport authorization responses: semantic refinement of rejected-bearer `401` challenges.
- Actor MCP session transport: dead-session `GET` changes from a credential-shaped `401` to the runtime's spec-shaped
  `404`; non-stream requests keep transparent session rebind.
- JSON-RPC method names, request envelopes, success envelopes, tools, resources, prompts, scopes, and profiles:
  unchanged.

## Compatibility classification

Additive, backward-compatible RFC 6750 refinement for bearer rejection, plus restoration of the MCP session-lifecycle
signal. No challenge parameter is removed or re-meaninged. Clients that only react to HTTP status continue to receive a
failure; clients that inspect the challenge or session response can now choose refresh versus re-initialize correctly.

## MCP client class enumeration

### Codex

- Affected: yes.
- Surface used: OAuth protected-resource discovery and Streamable HTTP MCP transport.
- Expected impact: rejected credentials can enter refresh/re-authorization; dead SSE sessions can re-initialize.
- Coordination: release note and contract tests; no client update is required to preserve existing behavior.

### Kimi

- Affected: yes.
- Surface used: sessionful Streamable HTTP, including Body's existing non-stream transparent rebind.
- Expected impact: dead SSE sessions are no longer misclassified as credential death.
- Coordination: release note and staged interoperability probe.

### Claude / Anthropic

- Affected: yes.
- Surface used: protected-resource discovery and Streamable HTTP MCP transport.
- Expected impact: standard `invalid_token` refresh handling becomes available without changing discovery.
- Coordination: release-note advisory; no schema migration.

### Anthropic AgentCore

- Affected: yes where it consumes the authenticated actor or instance transport.
- Expected impact: same standards-based refresh and re-initialize distinction.
- Coordination: release-note advisory and staged interoperability probe.

### Other MCP clients

- Affected: only clients receiving rejected bearer or dead-session responses.
- Expected impact: RFC 6750-aware clients gain a usable refresh signal; session-aware clients gain the standard
  re-initialize signal. Clients that ignore the added `error` parameter remain compatible.
- Coordination: public release notes are sufficient.

## Versioning strategy

- Transition mechanism: additive challenge parameter and semantic refinement of a misclassified lifecycle response.
- Transition window: immediate within the existing endpoints; no dual endpoint is needed.
- Deprecation signal: none; no supported request or response shape is removed.

## RFC 9728 compliance

- Protected-resource metadata shape: unchanged and RFC 9728-compliant.
- Per-actor resource identifier: unchanged.
- Authorization-server URL and scopes: unchanged.
- Challenge bridge: canonical `www-authenticate` and byte-identical `mcp-www-authenticate` remain present with
  `Cache-Control: no-store` on authorization challenges.

## MCP protocol-version consideration

- Protocol-version bump: not required.
- Existing versions: MCP `2025-11-25` session transport and MCP `2026-07-28` stateless transport remain supported.

## Rollout mechanics

- Consumer validation: dev/lab, then deploy-stage staging where used, then live under operator authorization.
- Rollback signals: missing bare discovery challenge, missing `invalid_token` on rejected bearer, a dead-session SSE
  request returning credential-shaped `401`, or loss of either challenge header/no-store directive.
- Discovery and OAuth metadata regeneration: not required because neither published shape changes.

## Audit-log implications

Non-stream rebind keeps the sanitized `mcp session rebound` event. Dead-session SSE responses use a sanitized
`mcp session not found` lifecycle event rather than an authorization-rejection event. Neither event records actor
identity, session ids, bearer tokens, or request bodies.

## Release-notes content

MCP authorization challenges now distinguish missing credentials from rejected bearer tokens. Rejected tokens include
RFC 6750 `invalid_token` and refresh-oriented details; dead SSE sessions return the MCP runtime's `404 session not
found` signal so clients re-initialize instead of re-authorizing.

## Proposed next skill

The audit is clean and additive. Implement the issue's enumerated auth helper, challenge response, session response,
contract tests, and documentation as one isolated auth/contract commit through `implement-milestone`.
