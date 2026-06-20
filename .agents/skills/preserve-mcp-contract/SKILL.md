---
name: preserve-mcp-contract
description: Use when a change affects the MCP contract surface — `.well-known/mcp.json` discovery, `.well-known/oauth-protected-resource/mcp/<actor>` OAuth metadata, or JSON-RPC request/response shape for `tools/call` / `resources/read` / `prompts/get`. Walks contract impact across every MCP client class and produces a coordination plan.
---

# Preserve the MCP contract

body's MCP contract is consumed by external MCP clients — Claude, Anthropic AgentCore, and any other MCP-speaking system. These clients cannot be directly coordinated with; they depend on body honoring the MCP specification and its own stated capabilities.

This skill is the walk that turns "the contract is changing" into "the coordination plan that makes the change safe."

## The contract surfaces (memorize)

1. **`GET /.well-known/mcp.json`** — public discovery. Unauthenticated. Lists registered tools (with input schemas), resources (with URIs and descriptions), prompts (with parameters), scopes, and supported runtime profiles. Clients cache this and use it to decide which tools to invoke and what schema to validate against.
2. **`GET /.well-known/oauth-protected-resource/mcp/<actor>`** — OAuth 2.0 protected-resource metadata per **RFC 9728**. Unauthenticated. Returns the authorization-server URL, the resource identifier (per-actor), and supported scopes. Clients use this to discover where to obtain tokens.
3. **`POST /mcp/<actor>`** — authenticated MCP JSON-RPC. Clients send:
   - `tools/call` — invoke a registered tool
   - `resources/read` — read a registered resource
   - `prompts/get` — retrieve a registered prompt template
   - Associated JSON-RPC error envelope for failures (invalid scope, invalid profile, tool not found, validation failure, etc.)

## When this skill runs

Invoke when:

- A change modifies the `.well-known/mcp.json` shape (new top-level field, changed field semantics, tool-schema shape change, resource/prompt shape change)
- A change modifies the OAuth protected-resource metadata shape (scope list change, resource-identifier change, authorization-server reference change)
- A change modifies JSON-RPC request-envelope expectations (new required parameter, changed method name, changed id semantics)
- A change modifies JSON-RPC response shape (success envelope, error envelope, error-code assignment)
- `evolve-tool-surface` has added, modified, or removed a tool (which cascades into `.well-known/mcp.json`)
- A change affects per-actor session persistence that MCP clients can observe
- `scope-need` flags a change as "MCP contract"

## Preconditions

- **The change is described concretely.** "Improve discovery" is too vague; "add optional `deprecated` boolean field on each tool entry in `.well-known/mcp.json` so clients can show deprecation in UI, default false, backward-compatible for clients that ignore unknown fields" is concrete.
- **MCP tools healthy**, `memory_recent` first.
- **The surfaces affected are identified.**

## The four-dimension walk

### Dimension 1: Compatibility classification

Classify every contract change:

- **Additive / backward-compatible** — new optional field in response, new optional tool parameter, new tool in discovery (which is itself additive), new optional error-code alongside existing. Clients continue to work unchanged.
- **Semantic refinement** — same shape, more precise behavior (tighter input validation, stricter error-condition detection). Clients using the contract as documented continue to work; clients relying on lax behavior may break.
- **Additive observable** — new required field in request or response, new error-code replacing an existing one, tool-schema field made mandatory. Clients must update eventually but can interoperate for a window.
- **Breaking** — removed field, renamed field, changed type, removed tool, changed method name, changed OAuth metadata shape in a way that breaks RFC 9728 compliance, changed scope semantics, changed profile semantics, removed support for a previously-accepted input shape.

Breaking changes require:

- MCP client class enumeration by name
- Coordination plan per class (advisory, release notes, optional versioned endpoint)
- Versioning strategy (new endpoint alongside old, protocol-version negotiation, deprecation window)
- Rollout plan with validation stages

### Dimension 2: MCP client class enumeration

For each MCP client class, answer:

- **Which surface does this class consume?** Discovery, OAuth metadata, JSON-RPC methods.
- **Is this class affected by the change?** Based on compatibility classification.
- **How is the class reachable for coordination?**
  - **Claude / Anthropic** — via Anthropic's MCP documentation and developer channels; coordination is possible through release-note advisories.
  - **Anthropic AgentCore** — similar channel; may also be coordinated through Anthropic's AgentCore team.
  - **Other MCP clients** (community-built, vendor-built) — via general ecosystem channels (MCP spec community, GitHub discussions).
- **What's the class's contract-testing posture?** Strict schema validation? Lenient extra-field tolerance?

Enumerate explicitly. "All MCP clients will adapt" is not a plan.

### Dimension 3: Versioning and transition

For non-trivial changes:

- **Can old and new coexist?** Adding a new tool to discovery is trivial; replacing the JSON-RPC error envelope requires coexistence.
- **What's the transition mechanism?**
  - **Additive field** — old clients ignore new field; new clients use it.
  - **Versioned endpoint** — `/mcp/v2/<actor>` alongside `/mcp/<actor>` for substantial breaking changes. (MCP spec may also support protocol-version negotiation via JSON-RPC initialize.)
  - **Protocol-version negotiation** — clients declare supported MCP protocol version in `initialize`; body responds with compatible surface.
  - **Dual-shape response** — during a transition, responses include both old and new field forms.
- **What's the deprecation timeline?** Named conditions ("after two release cycles," "after Claude v5 adopts this"), not vague "eventually."
- **What's the signal that the old path can be removed?** Monitoring showing zero traffic, explicit ecosystem sign-off, or operational decision.

### Dimension 4: RFC 9728 compliance (for OAuth metadata changes)

For changes affecting `.well-known/oauth-protected-resource/mcp/<actor>`:

- **Confirm the resulting metadata is RFC 9728-compliant.** Resource identifier, authorization-server URL, supported scopes, token-handling hints.
- **Confirm the per-actor resource identifier is stable.** Changing how `<actor>` maps to resource identifier breaks OAuth flows that cached it.
- **Confirm scope values are consistent with discovery metadata.** Scopes listed in OAuth metadata must match scopes declared on tools.

## The audit output

```markdown
## MCP contract audit: <change name>

### Proposed change
<concrete description>

### Surfaces affected
- `.well-known/mcp.json` discovery: <...>
- `.well-known/oauth-protected-resource/mcp/<actor>` OAuth metadata: <...>
- JSON-RPC request/response shape: <...>

### Compatibility classification
<additive backward-compatible / semantic refinement / additive observable / breaking>

### MCP client class enumeration

#### Claude / Anthropic
- Affected: <yes / no>
- Surface used: <discovery, OAuth metadata, tools/call, resources/read, prompts/get>
- Expected impact: <none / client must update X / breaking>
- Coordination channel: <release-note advisory, Anthropic developer channel>
- Timeline flexibility: <...>

#### Anthropic AgentCore
- Affected: <...>
- Coordination channel: <...>

#### Other MCP clients
- Known integrations: <list>
- Expected impact: <...>
- Release-notes advisory: <required / not required>

### Versioning strategy
- Transition mechanism: <additive / versioned endpoint / protocol-version negotiation / dual-shape response / big-bang>
- Transition window: <expected duration>
- Deprecation signal: <named condition>

### RFC 9728 compliance (if OAuth metadata touched)
- Resulting metadata RFC 9728-compliant: <verified>
- Resource identifier stable: <verified>
- Scope values consistent with discovery: <verified>

### MCP protocol-version consideration
- Requires protocol-version bump: <no / yes — to version X>
- Existing protocol-version supported: <yes — backward-compat>

### Rollout mechanics
- Consumer validation stages: <dev → staging → live, per client class>
- Rollback signal: <...>
- SLA impact: <none / latency / error-rate>
- `.well-known/mcp.json` regeneration: <completed>
- OAuth metadata regeneration: <completed if touched>

### Audit-log implications
<what audit events are emitted; retention>

### Release-notes content
<the specific notice operators and MCP client maintainers will read>

### Proposed next skill
<enumerate-changes if clean; evolve-tool-surface if tool-surface is also the driver; scope-need if audit surfaces scope growth>
```

## Refusal cases

- **"Change the JSON-RPC error envelope to match our internal convention."** Refuse. MCP clients code against MCP conventions.
- **"Drop support for MCP protocol version X-1; the spec moved on."** Requires explicit deprecation window; immediate removal strands clients.
- **"Skip RFC 9728 compliance for OAuth metadata to ship faster."** Refuse. OAuth clients depend on the standard.
- **"Return extra fields in discovery that aren't backed by actual tools." / "Advertise tools that aren't registered."** Refuse. Discovery must match registration.
- **"Remove a registered tool without client-side deprecation coordination."** Refuse.
- **"Change the OAuth resource identifier for existing actors."** Refuse without coordinated client migration; breaks token caching.
- **"Change the `/mcp/<actor>` path to something else."** Breaks every connected client. Requires explicit coordination.
- **"Skip emitting the new protocol-version negotiation behavior; old clients will just work."** Evaluate carefully; MCP protocol versioning has specific semantics.

## Persist

Append when the walk surfaces a recurring pattern — a specific client's schema strictness, a protocol-version negotiation subtlety, an RFC 9728 compliance consideration worth remembering, a coordination channel that worked well. Routine audits that resolve cleanly aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **Audit clean, additive change** — invoke `enumerate-changes`.
- **Audit clean, breaking with coordination plan** — after client-class sign-off, `enumerate-changes`.
- **Audit overlaps with tool-surface walk** — ensure `evolve-tool-surface` is also complete.
- **Audit reveals lesser-integration concern** — invoke `coordinate-with-lesser`.
- **Audit surfaces scope growth** — revisit `scope-need`.
- **Audit reveals a client-side bug** — report cleanly to user; no body-side change needed.
- **Audit reveals framework awkwardness** (AppTheory MCP runtime contract-handling) — `coordinate-framework-feedback`.