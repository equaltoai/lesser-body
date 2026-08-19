# MCP contract audit: bearer challenge and dead-session classification

## Proposed change

Refine the authenticated MCP transport contract so an absent bearer keeps the bare OAuth discovery challenge, a
generically rejected bearer adds RFC 6750 `invalid_token` plus refresh-oriented details, an actor-plane valid-signature
bearer for the wrong actor resource uses `invalid_token` without inviting refresh, and a dead session on an SSE `GET`
retains AppTheory's `404 {"error":"session not found"}` lifecycle signal.

| Challenge class | Plane | RFC 6750 error | `details.reason` | `details.authAction` | `details.refreshRequired` |
|-----------------|-------|----------------|------------------|----------------------|---------------------------|
| Absent bearer | Actor and instance | omitted | `missing_or_invalid_bearer` | `authorize` | `false` |
| Generic rejected bearer | Actor and instance | `invalid_token` | `invalid_oauth_bearer` | `refresh_or_reauthorize` | `true` |
| Audience mismatch | Actor only | `invalid_token` | `audience_mismatch` | `reauthorize` | `false` |

The Ptah/Ba instance plane retains its pre-existing principal gate: a valid-signature bearer with the wrong
instance-resource audience returns HTTP `403`, not an RFC 6750 `audience_mismatch` challenge:

```json
{
  "error": {
    "code": "instance_principal_not_allowed",
    "message": "instance-plane MCP requires an account-holder OAuth token for this instance resource"
  }
}
```

## Surfaces affected

- `/.well-known/mcp.json` discovery: unchanged.
- `/.well-known/oauth-protected-resource/mcp/<actor>` metadata: unchanged.
- Actor MCP transport authorization responses: semantic refinement of rejected-bearer `401` challenges, including the
  actor-resource `audience_mismatch` class.
- Instance MCP transport authorization responses: semantic refinement of absent and generically rejected-bearer `401`
  challenges; valid-signature wrong-audience tokens continue to return the pre-existing `403`
  `instance_principal_not_allowed` response documented above.
- Actor MCP session transport: dead-session `GET` changes from a credential-shaped `401` to the runtime's spec-shaped
  `404`; non-stream requests keep transparent session rebind.
- JSON-RPC method names, request envelopes, success envelopes, tools, resources, prompts, scopes, and profiles:
  unchanged.

## Compatibility classification

Additive, backward-compatible RFC 6750 refinement for bearer rejection, plus restoration of the MCP session-lifecycle
signal. No challenge parameter is removed or re-meaninged. Clients that only react to HTTP status continue to receive a
failure; clients that inspect the challenge or session response can now choose refresh, re-authorization for the correct
actor resource, or re-initialization correctly. Instance-plane clients continue to receive the existing `403`
principal/resource rejection for a valid-signature wrong-audience token.

## MCP client class enumeration

### Codex

- Affected: yes.
- Surface used: OAuth protected-resource discovery and Streamable HTTP MCP transport.
- Expected impact: rejected credentials can enter refresh/re-authorization, actor-plane wrong-resource tokens request
  re-authorization without a futile refresh loop, instance-plane wrong-audience tokens retain the documented `403`
  principal rejection, and dead SSE sessions can re-initialize.
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
- Expected impact: the same standards-based refresh, actor-resource re-authorization, instance-plane `403` principal
  rejection, and re-initialize distinction.
- Coordination: release-note advisory and staged interoperability probe.

### Other MCP clients

- Affected: only clients receiving rejected bearer or dead-session responses.
- Expected impact: RFC 6750-aware clients gain a usable refresh signal on `401`; actor-plane clients gain the
  wrong-resource re-authorization signal; instance-plane clients retain the `403` `instance_principal_not_allowed`
  signal; session-aware clients gain the standard re-initialize signal. Clients that ignore the added `error` parameter
  remain compatible.
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
- Rollback signals: missing bare discovery challenge, missing `invalid_token` on rejected bearer, actor-plane audience
  mismatch inviting refresh, instance-plane audience mismatch no longer returning `403`
  `instance_principal_not_allowed`, a dead-session SSE request returning credential-shaped `401`, or loss of either
  challenge header/no-store directive.
- Discovery and OAuth metadata regeneration: not required because neither published shape changes.

## Audit-log implications

Non-stream rebind keeps the sanitized `mcp session rebound` event. Dead-session SSE responses use a sanitized
`mcp session not found` lifecycle event rather than an authorization-rejection event. Neither event records actor
identity, session ids, bearer tokens, or request bodies.

## Release-notes content

MCP authorization challenges now distinguish missing credentials, generically rejected bearer tokens, and tokens for
the wrong actor resource. Generic rejections include RFC 6750 `invalid_token` and refresh-oriented details;
actor-plane audience mismatches retain `invalid_token` but request re-authorization without refresh. Instance-plane
valid-signature wrong-audience tokens retain HTTP `403` with `error.code=instance_principal_not_allowed`. Dead SSE
sessions return the MCP runtime's `404 session not found` signal so clients re-initialize instead of re-authorizing.

## Proposed next skill

The audit is clean and additive. Implement the issue's enumerated auth helper, challenge response, session response,
contract tests, and documentation as one isolated auth/contract commit through `implement-milestone`.
