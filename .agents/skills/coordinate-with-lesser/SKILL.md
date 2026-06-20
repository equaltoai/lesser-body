---
name: coordinate-with-lesser
description: Use when a change touches the body↔lesser integration surface — JWT secret handling, DynamoDB data-table reads, lesser REST API consumption, SSM parameter exports, `soulEnabled` CDK context, or the three-step first-deploy order. Walks the integration impact and produces a coordination plan with the `lesser` steward before the change proceeds. body extends lesser; the integration is architecture, not plumbing.
---

# Coordinate with lesser

body is an extension of lesser, not a standalone service. The integration is load-bearing on five surfaces: shared JWT signing secret, shared DynamoDB data table, lesser's REST API (as a client), SSM parameter exports (body publishes, lesser reads), and the `soulEnabled` CDK-context-driven wiring that makes `/mcp/*` route through lesser's API Gateway into body's Lambda.

A change that touches any of these surfaces is an integration-contract change. Coordination with the `lesser` steward is required before the change lands.

## The integration surfaces (memorize)

1. **Shared JWT signing secret** — body validates JWTs signed by lesser's authentication surface. Both services read the same HS256 secret from AWS Secrets Manager. Secret rotation requires coordinated handling.
2. **Shared DynamoDB data table** — body reads from `lesser-{stage}` for memory tools and some social / identity tool implementations. body does not own the schema; it observes it via TableTheory models that mirror lesser's tags.
3. **Lesser REST API client** (`internal/lesserapi/`) — body's social tools (and some identity tools) call lesser's `/api/v1/*` endpoints. Base URL configured via `LESSER_API_BASE_URL`. Changes in lesser's API shape require body-side adaptation.
4. **SSM parameter exports** — body publishes at stable names under `/<app>/<stage>/lesser-body/exports/v1/`:
   - `mcp_lambda_arn` — the body Lambda ARN lesser's CloudFront proxy targets
   - `mcp_endpoint_url` — the MCP endpoint URL for discovery metadata
   - `mcp_session_table_name` — the session table name (optional, when used)
   lesser reads these SSM parameters when `soulEnabled=true`.
5. **`soulEnabled` CDK context** — lesser's CDK conditionally wires `/mcp/*` proxy when `soulEnabled=true`. body's deploy must precede lesser's soul-enabled deploy for first-time setups (the three-step order).

## When this skill runs

Invoke when:

- A change affects how body validates JWTs (secret handling, algorithm, claim requirements)
- A change affects how body reads lesser's DynamoDB (new access pattern, new model, different tag interpretation)
- A change affects how body calls lesser's REST API (new endpoint, modified request shape, response parsing)
- A change affects SSM export names, shapes, or contents
- A change affects `soulEnabled`-gated behavior in lesser's CDK
- A change affects the three-step first-deploy order
- A change modifies `LESSER_API_BASE_URL` resolution or related configuration
- `scope-need` or `investigate-issue` flags a change as lesser-integration-touching

## Preconditions

- **The change is described concretely.** "Fix lesser integration" is too vague; "update `internal/lesserapi/` client to handle the new `edited_at` field lesser returns on status responses, falling back to null for pre-field items, without breaking consumers that ignore the field" is concrete.
- **MCP tools healthy**, `memory_recent` first — prior integration decisions matter.
- **The lesser-side context is available** — what version of lesser's REST API or DynamoDB schema body is integrating against.

## The five-dimension walk

### Dimension 1: JWT secret handling

- **Does the change affect JWT validation?** Algorithm selection, claim requirements, signature verification, clock-skew tolerance.
- **Does the change affect how the secret is retrieved?** Secrets Manager path, caching, rotation handling.
- **Does the change require secret rotation?** If so, both services must be coordinated — body rotates its validation to accept both old and new secrets during the transition window; lesser issues new-signed tokens; transition window closes by dropping old-secret acceptance.
- **Defaults fail closed** — invalid, missing, or expired JWTs are rejected. Never relax.

### Dimension 2: DynamoDB data-table reads

- **Which entities does body read?** Enumerate by PK / SK pattern (AccountKeys, AgentGovernanceState, Note, Activity, etc.).
- **Does the change require a new access pattern?** If so, is there an existing GSI that serves it, or is a new one needed? New GSI additions are **lesser's** responsibility — coordinate, don't assume.
- **Does the change depend on TableTheory tag semantics?** Changes to lesser's tag structure cascade into body's model definitions; body's models must stay aligned.
- **Does the change require a new entity type?** Adding a new entity-type SK pattern is lesser's schema responsibility; body reads, lesser owns.
- **Does the change affect write paths?** body does not write to lesser's data table except via lesser's REST API. Direct writes are refused. Memory tools write to body's own session table, which is body's to own.

### Dimension 3: Lesser REST API consumption

- **Which endpoints does body call?** Enumerate by path.
- **Does the change require a new endpoint?** Adding a new endpoint is **lesser's** responsibility; body consumes. Coordinate with `lesser` steward for the endpoint addition, then add body-side client code.
- **Does the change adapt to lesser's API evolution?** If lesser adds a new optional field, body's client can start consuming it once released. If lesser plans a breaking change, body coordinates the consumer migration.
- **Does the change affect `LESSER_API_BASE_URL` resolution?** Usually no — the env var points at the current deployment's lesser API. Changes here affect operator deployment configuration.
- **Error handling** — body's client must handle lesser's error responses gracefully; retry semantics, backoff, circuit-breaker-like behavior for transient failures.

### Dimension 4: SSM parameter exports

- **Does the change modify the stable export names** under `/<app>/<stage>/lesser-body/exports/v1/`?
  - **Renames are breaking** — lesser's soul-enabled CDK reads these names. Renames require lesser-side update in coordination.
  - **Additions are additive** — a new export at `/v1/<new_export>` is backward-compatible; lesser can opt into reading it on its own timeline.
  - **Removals are breaking** — refuse without coordinated lesser-side removal of the read.
- **Does the change modify export values?** Values update on each deploy; changes in value format (e.g. URL format, ARN format) affect lesser's parsing.
- **Does the change affect the `/v1/` version segment?** A `/v2/` segment would be a major contract change — coordinate explicitly.

### Dimension 5: `soulEnabled` and three-step deploy order

- **Does the change affect `soulEnabled`-gated behavior in lesser's CDK?** lesser reads body's SSM exports only when `soulEnabled=true`. Changes to that gate affect which lesser deployments connect to body.
- **Does the change require the three-step order?** First-time deploys: unsouled lesser → body → soul-enabled lesser. If the change affects the SSM export body publishes or the lesser-side reads, the order matters.
- **Does the change break subsequent-deploy independence?** Subsequent deploys update body and lesser independently; changes that re-introduce ordering requirements should be scrutinized.

## The audit output

```markdown
## Lesser-integration audit: <change name>

### Proposed change
<concrete description>

### JWT secret impact
- Validation logic affected: <no / yes — describe>
- Secret retrieval / caching: <no change / rotation coordination>
- Rotation window handling: <n/a / coordinated with lesser>

### DynamoDB read impact
- New access pattern: <no / yes — GSI already serves / new GSI needed (lesser's responsibility)>
- Entity types accessed: <list>
- TableTheory tag alignment: <consistent with lesser's schema>
- Write attempts: <none — default; any write goes through lesser's REST API>

### Lesser REST API impact
- Endpoints consumed: <list>
- New endpoint required: <no / yes — coordination with lesser steward>
- Breaking adaptation required (lesser-side contract change): <no / yes — migration plan>
- Error handling: <retry semantics, backoff, graceful degradation>

### SSM export impact
- Export names changed: <no — default; yes — lesser-side read must update>
- Export values / format changed: <no — default; yes — lesser-side parse must update>
- New exports added: <none / list>
- Exports removed: <none — default; yes — refuse without lesser-side coordination>
- Version segment (`/v1/`) change: <no — default>

### soulEnabled and three-step order impact
- soulEnabled-gated behavior affected: <no / yes — describe>
- Three-step first-deploy order required: <no (subsequent deploy independent) / yes (first-time deploy order)>
- Subsequent-deploy independence preserved: <yes / no — coordinate>

### Cross-repo coordination
- lesser steward awareness: <required / not required, what>
- lesser-side changes needed: <none / enumerated>
- Timing: <body first / lesser first / parallel / n/a>

### Test coverage
- JWT validation tests: <existing / added>
- DynamoDB read tests (against test-mode table with shape fixtures): <existing / added>
- Lesser API client tests (with mocked lesser responses): <existing / added>
- SSM export publication tests (via CDK snapshot): <existing / added>
- soulEnabled-gated wiring test (in lesser's own CDK — not body's): <confirmed / coordinate with lesser>

### Proposed next skill
<enumerate-changes if audit clean; preserve-mcp-contract if MCP contract is also touched; deploy-body for the deploy walk; scope-need if audit surfaces scope growth>
```

## Refusal cases

- **"Bypass lesser's REST API for this data; it's faster to read DynamoDB directly."** Refuse unless the access pattern is genuinely body-owned (memory tools). For concerns that lesser's API exposes, going around it creates silent coupling.
- **"Write to lesser's DynamoDB directly for this one tool."** Refuse. Writes go through lesser's REST API.
- **"Sign JWTs in body with a separate key; body owns its own auth."** Refuse. JWT signing is lesser's concern; body validates.
- **"Change the SSM export names; the `/v1/` scheme feels bureaucratic."** Refuse without coordinated lesser-side update.
- **"Deploy body before lesser even for first-time deploys."** Refuse. Order is unsouled lesser → body → soul-enabled lesser.
- **"Skip the `soulEnabled` gate so body is always integrated."** Refuse. The gate exists so operators can opt in.
- **"Hardcode `LESSER_API_BASE_URL` in body for our specific deployment."** Refuse. Env-var configuration is the contract.
- **"Add a new entity type to lesser's DynamoDB from body's migration script."** Refuse. Schema is lesser's responsibility.

## Persist

Append when the walk surfaces a recurring pattern — a lesser-side schema change that affected body's reads, a JWT rotation timing subtlety, an SSM export versioning decision, a three-step-deploy edge case. Routine audits that resolve cleanly aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **Audit clean, no lesser-side change required** — invoke `enumerate-changes`.
- **Audit clean, lesser-side change needed and coordinated** — coordination with `lesser` steward happens through the user; after sign-off, `enumerate-changes`.
- **Audit reveals MCP contract impact** — invoke `preserve-mcp-contract`.
- **Audit reveals scope growth** — revisit `scope-need`.
- **Audit reveals an existing integration bug** — route through `investigate-issue`, then back here.
- **Audit reveals framework awkwardness** (AppTheory client libraries, TableTheory for cross-service tag alignment) — `coordinate-framework-feedback`.
- **Audit requires changes in lesser that lesser's steward hasn't yet seen** — pause; surface to user for cross-repo coordination.