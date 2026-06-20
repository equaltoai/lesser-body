---
name: evolve-tool-surface
description: Use when a change touches the 27-tool surface — adding a tool, modifying a tool's behavior, removing a tool, adjusting a tool's declared scope (`read`/`write`/`admin`) or declared profile (`drone`/`souled`), or touching the static registration mechanism in `internal/mcpserver/`. Walks the tool-surface impact with scope / profile correctness, communication-tool delegation discipline, per-actor session handling, and static-registration integrity.
---

# Evolve the tool surface

body's 27 tools are its product. Every MCP client that connects invokes some subset of them. The surface composition — which tools exist, what scope gates them, what profile permits them, what they actually do — is the contract between body and every caller. This skill walks every tool-surface-affecting change.

Tools are registered **statically** at Lambda startup via `internal/mcpserver/mcpserver.go`'s `registerTools()`, which calls group-specific registrars (`registerSocialTools()`, `registerMemoryTools()`, `registerCommunicationTools()`, identity tool registration). **There is no dynamic runtime registration.** Changes to the tool surface are code changes that require redeploy.

## The tool groups (memorize)

- **Social tools (13)** — timeline read, post create, follow, boost, like, reply, profile read, and related. Consumed via lesser's REST API client (`internal/lesserapi/`).
- **Memory tools (2)** — `memory_append`, `memory_recent` (or similar). Backed by DynamoDB session/memory table (`internal/memory/`).
- **Communication tools (7)** — 5 email + 2 SMS. **Delegate to lesser-host's comm APIs** at `/api/v1/soul/comm/*` with `LESSER_HOST_INSTANCE_KEY`. `souled`-profile only.
- **Identity tools (5)** — identity lookup, verification, ENS fallback, current-instance local-id handling, dotted-local-id support. Consult lesser-soul / lesser-host for authoritative resolution.

## When this skill runs

Invoke when:

- A change **adds** a new tool to any group
- A change **modifies** an existing tool's behavior, input schema, output schema, or side effects
- A change **removes** a tool from the registered set
- A change **adjusts scope declaration** (`read`/`write`/`admin`) for an existing tool
- A change **adjusts profile declaration** (`drone`/`souled` availability) for an existing tool
- A change touches the static registration mechanism (`registerTools()`, group registrars)
- A change affects per-actor MCP session handling that tools rely on
- A change affects communication-tool delegation to lesser-host
- `scope-need` flags the change as "tool-surface"

## Preconditions

- **The change is described concretely.** "Add an identity tool" is too vague; "add `identity_verify_ens` tool that resolves an ENS name to a lesser actor via lesser-soul, requires `read` scope, available in `souled` profile only, no side effects, idempotent" is concrete.
- **MCP tools healthy**, `memory_recent` first.
- **The existing tool catalog is loaded in mind** — `docs/mcp.md` is the reference.

## The five-dimension walk

### Dimension 1: Tool identity and scope declaration

For each tool being added / modified / removed:

- **Tool name** — globally unique within body's registry. Naming convention: `snake_case`, descriptive, verb-noun pattern where natural (`post_create`, `memory_append`, `identity_verify`).
- **Group assignment** — social / memory / communication / identity. Tools belong to one group.
- **Scope declaration** — `read` / `write` / `admin`:
  - **`read`** — tools that retrieve information without side effects (timeline reads, profile reads, memory reads, identity lookups)
  - **`write`** — tools that produce side effects on the actor's own resources (post create, memory append, follow, send email, send SMS)
  - **`admin`** — tools that produce side effects across actors or affect platform state. **Very rare.** Most "admin" concerns actually belong elsewhere (lesser's backoffice, lesser-host's control plane).
- **Defaults fail closed** — a tool that forgets its scope declaration defaults to `admin` (strictest). Never commit a tool that relies on this fallback; declare explicitly.
- **Scope escalation is a contract change.** Moving a tool from `read` to `write` (because the behavior genuinely changed) requires client coordination. Moving from `write` to `read` (loosening) is refused unless the side effects actually went away.

### Dimension 2: Profile declaration

For each tool:

- **Profile availability** — `drone` / `souled` / both:
  - **`souled`-only** — requires a soul-bound caller. Communication tools (email, SMS) and the `agent://channels` resource are `souled`-only. Tools that persist long-term memory or affect the actor's federated identity are often `souled`-only.
  - **`drone`-available** — lightweight callers without soul binding can invoke. Read tools, short-lived memory, and identity lookups are typically available in both profiles.
- **Defaults fail closed** — a tool that forgets its profile declaration defaults to `souled`-only (stricter).
- **Profile widening is a contract change.** Moving a tool from `souled`-only to `drone`-available unlocks it for lightweight agents; this is meaningful and requires explicit reasoning.
- **Profile narrowing** (removing `drone` availability) affects connected agents running in drone mode; coordinate.

### Dimension 3: Behavior, input, output, idempotency

For each tool:

- **Input schema** — every parameter with type, nullability, default, validation rule. MCP clients rely on the schema; discoverability flows from it.
- **Output schema** — shape of success response, shape of error responses, error codes.
- **Side effects** — what the tool does beyond reading / returning data. For `write` / `admin` tools, enumerate side effects explicitly.
- **Idempotency** — for tools with side effects (especially communication tools), is replay safe? Communication tools require caller-supplied `messageId` for idempotency; memory append tools may require caller-supplied event IDs.
- **Rate-limiting** — if the tool calls downstream services (lesser REST API, lesser-host comm API), what rate limits apply?
- **Audit emission** — every `write` / `admin` tool invocation emits an audit event with the actor identity, tool name, scope gate result, profile gate result, timestamp, and sanitized metadata.

### Dimension 4: Communication-tool discipline (if applicable)

For communication tools specifically:

- **Delegation to lesser-host** — all email / SMS delivery goes through `/api/v1/soul/comm/*`. Never implement delivery locally in body.
- **`LESSER_HOST_INSTANCE_KEY` authentication** — the separate credential body uses to authenticate to lesser-host. Never log it. Never embed it in code.
- **Caller-supplied `messageId`** — the idempotency key. Replay of the same `messageId` produces no duplicate send.
- **Thread resolution** — reply tools fetch thread context from lesser-host before enqueuing the reply. Thread-id format is lesser-host's contract.
- **PII discipline** — recipient addresses and phone numbers are sanitized on every log path. Message bodies are never logged in full.
- **Rate limits and capacity** — communication tools respect lesser-host's rate limits; delegation is not a way around them.

### Dimension 5: Static-registration integrity

For tool add / modify / remove:

- **Registration call** lives in `internal/mcpserver/mcpserver.go`'s group registrars. New tools are added there alongside existing ones.
- **Tool-handler implementation** lives in `internal/mcpserver/` or the group's handler file.
- **Per-actor session** — if the tool depends on persistent session state (`MCP_SESSION_TABLE` enabled), handler accesses the session via the established lookup pattern.
- **Discovery metadata** — `.well-known/mcp.json` is generated from the registered toolset. New / modified / removed tools appear or disappear there at next deploy.
- **`docs/mcp.md`** — the canonical tool catalog document. Updates ride with registration changes in the same commit.

## The audit output

```markdown
## Tool-surface audit: <change name>

### Proposed change
<concrete description>

### Tool(s) affected
- **<tool name>**: <add / modify / remove>
  - Group: <social / memory / communication / identity>
  - Scope: <read / write / admin>
  - Profile: <drone / souled / both>
  - Input schema: <...>
  - Output schema: <...>
  - Side effects: <enumerated>
  - Idempotency: <yes — via X / not applicable>
  - Rate-limiting considerations: <...>
  - Audit event: <emitted with fields X, Y, Z>

### Scope / profile impact
- Escalation (tightening): <none / list changes>
- Loosening: <none — default; if present, refuse unless authorized>
- Fail-closed defaults preserved: <yes>

### Communication-tool discipline (if applicable)
- Delegates to lesser-host: <confirmed>
- messageId idempotency: <implemented>
- PII log sanitization: <confirmed>
- `LESSER_HOST_INSTANCE_KEY` handling: <confirmed — never logged>

### Static-registration integrity
- Registration in `registerTools()`: <added / updated / removed>
- Handler implementation: <added / updated / removed>
- `docs/mcp.md` updated: <yes — same commit>
- `.well-known/mcp.json` shape change: <additive / breaking — coordinate via preserve-mcp-contract>

### MCP contract impact
<none / additive / breaking — coordination plan>

### Lesser-integration impact (for social / memory / identity tools)
<no change / coordination via coordinate-with-lesser>

### Consumer impact
- Claude: <no impact / new capability / breaking>
- AgentCore: <...>
- Other MCP clients: <...>

### Test coverage
- Scope-gate test: <added — rejects insufficient scope>
- Profile-gate test: <added — rejects incorrect profile>
- Happy path: <added>
- Side-effect / idempotency tests: <added for write tools>
- Audit-event emission test: <added for write/admin tools>

### Proposed next skill
<enumerate-changes if audit clean; preserve-mcp-contract if contract-breaking; coordinate-with-lesser if lesser-integration-touching; scope-need if audit surfaces scope growth>
```

## Refusal cases

- **"Add this tool without declaring scope."** Refuse. Fail-closed default is `admin`; relying on default defeats auditability.
- **"Add this tool without declaring profile."** Refuse. Fail-closed default is `souled`-only; declare explicitly.
- **"Loosen this tool from `write` to `read` to get around authorization."** Refuse. Side effects don't vanish by re-declaring scope.
- **"Make this new tool available in drone profile; the agents need it."** Evaluate carefully. If the tool is communication-adjacent, `souled`-only is correct. If it's a read tool, drone availability may be fine.
- **"Let this tool have side effects at `read` scope because it's a helpful convenience."** Refuse. Side effects require `write` or `admin`.
- **"Implement email delivery locally in body for this one use case."** Refuse. Delegation to lesser-host is the contract.
- **"Log the messageId and recipient for a specific communication tool to debug."** MessageId is fine to log; recipient address / phone is sanitized per PII discipline.
- **"Add dynamic tool registration so operators can plug in custom tools."** Refuse. Static registration is the architecture.
- **"Skip updating `docs/mcp.md`; we'll sync it later."** It rides with the registration change.
- **"Add a new tool group for a new category."** Evaluate. If the category is genuinely distinct (not a fifth generic group), escalate. Most "new categories" fit within social / memory / communication / identity.

## Persist

Append when the walk surfaces a recurring pattern — a scope / profile interaction subtlety, a communication-tool idempotency edge case, an MCP-client tool-discovery quirk, a registration-mechanism detail worth remembering. Routine audits that resolve cleanly aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **Audit clean, additive, no contract break** — invoke `enumerate-changes`.
- **Audit clean, contract-affecting** — invoke `preserve-mcp-contract` before enumeration.
- **Audit clean, lesser-integration-touching** — invoke `coordinate-with-lesser` before enumeration.
- **Audit surfaces scope / profile loosening** — refuse or escalate for explicit authorization.
- **Audit surfaces scope growth** — revisit `scope-need`.
- **Audit reveals an existing tool bug** — route through `investigate-issue`, then back here.
- **Audit reveals host-delegation friction** (lesser-host comm API gap) — coordinate with `host` steward.
- **Audit reveals framework awkwardness** (AppTheory MCP runtime tool-registration API) — `coordinate-framework-feedback`.