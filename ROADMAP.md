# lesser-body roadmap (AgentCore MCP + AppTheory)

This roadmap converts `SPEC.md` into an **AppTheory-first**, **AgentCore-compatible** implementation plan.

## Hard constraints (non-negotiable)

- **AppTheory:** `github.com/theory-cloud/apptheory@v0.11.0`
- **TableTheory:** `github.com/theory-cloud/tabletheory@v1.4.0`
- **MCP transport:** AppTheory MCP runtime (`github.com/theory-cloud/apptheory/runtime/mcp`)
- **No Lambda Function URLs.**
- **No CloudFront required for MCP routing** (AgentCore calls API Gateway directly).
- **Reuse the existing lesser API custom domain** (`api.<stageDomain>`). We add a **path** (no new domains).
- **No CDK exports/imports.** If stacks need to share values, use **SSM parameters** with **well-known names** including **project slug + stage**.
- `lesser-body` is **optional**, but for `lesser-host` managed instances it should be **enabled by default** and deployed **alongside** a lesser install.

## Current correction vs `SPEC.md`

`SPEC.md` assumes `/soul/*` path routing via CloudFront to a Lambda URL origin. That design is a leftover and **not compliant with our constraints**.

**Roadmap baseline:**

- MCP endpoint is served from the **existing lesser API Gateway** on:
  - `POST https://api.<stageDomain>/mcp`
- It is implemented as an **AppTheory Go Lambda** using AppTheory’s MCP runtime (`runtime/mcp`), which supports:
  - `initialize`
  - `tools/list`, `tools/call`
  - `resources/list`, `resources/read`
  - `prompts/list`, `prompts/get`

AgentCore calls the tools methods today, but we can also ship resources/prompts for non-AgentCore MCP clients.

## Architecture baseline

### Runtime

- **Lambda binary:** `cmd/lesser-body` (Go)
- **HTTP framework:** `github.com/theory-cloud/apptheory/runtime`
- **MCP handler:** `github.com/theory-cloud/apptheory/runtime/mcp`
- **Session persistence (prod):** DynamoDB-backed session store via TableTheory (`mcp.NewDynamoSessionStore(db)`).

### Infra & wiring (no exports/imports)

- `lesser-body` stack deploys:
  - MCP Lambda
  - (optional, recommended) MCP session table
  - **SSM exports** (Lambda ARN, session table name, etc.)
- `lesser` stack (existing API Gateway + custom domain) reads the MCP Lambda ARN from SSM and wires:
  - `POST /mcp` → lesser-body Lambda (enabled when `soulEnabled=true`)

### Security posture

- MCP endpoint is **authenticated** using **lesser’s existing credentials**.
- Auth is enforced using AppTheory’s `WithAuthHook` + route `RequireAuth()` so `/mcp` fails closed if misconfigured.

## SSM parameter contract (well-known names)

All SSM parameters are **stage + project slug** keyed and versioned.

Terminology:
- `app` = lesser project slug (the `--app` slug used by `lesser up`)
- `stage` = `dev|staging|live` (same mapping as lesser CDK `StageForEnvironment`)

### lesser → exports (required by lesser-body)

- `/${app}/${stage}/lesser/exports/v1/table_name`
- `/${app}/${stage}/lesser/exports/v1/media_bucket_name` (only if media tools ship in v1)
- `/${app}/${stage}/lesser/exports/v1/domain` (the stage domain, used for discovery docs + challenge domains)

### lesser-body → exports (required by lesser)

- `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`
- `/${app}/${stage}/lesser-body/exports/v1/mcp_endpoint_url` (convenience; should equal `https://api.<stageDomain>/mcp`)
- `/${app}/${stage}/lesser-body/exports/v1/mcp_session_table_name` (if enabled)

### lesser-soul / lesser-host (optional integration)

Keep these names stable but treat the values/shape as evolving while `lesser-soul` is under active development:

- `/${app}/${stage}/lesser-soul/exports/v1/api_base_url`
- `/${app}/${stage}/lesser-soul/exports/v1/public_keys_url` (if needed for verification flows)

Acceptance criteria for the contract:
- Every parameter above is created by exactly one stack.
- No stack depends on CloudFormation exports/imports.
- A stack can be re-deployed independently as long as required SSM inputs exist.

---

## Milestones

### M0 — Alignment + version pins (AppTheory 0.11.0 / TableTheory 1.4.0)

Deliverables:
- `ROADMAP.md` (this document) is the source of truth for implementation sequencing.
- Repo pins match required framework versions.
- SSM parameter names are agreed and documented (section above).
- Decide the canonical MCP URL:
  - `https://api.<stageDomain>/mcp` (primary)

Acceptance criteria:
- No files in `lesser-body/` reference AppTheory != `v0.11.0` or TableTheory != `v1.4.0`.
- The roadmap contains enough detail to implement without re-interpreting `SPEC.md`.

---

### M1 — Repo bootstrap: Go module + MCP Lambda skeleton

Deliverables:
- Go module for `lesser-body` with pinned deps:
  - `github.com/theory-cloud/apptheory@v0.11.0`
  - `github.com/theory-cloud/tabletheory@v1.4.0`
- Lambda entrypoint `cmd/lesser-body/main.go`:
  - AppTheory app
  - `POST /mcp` handler via `mcp.NewServer(...)`
  - minimal “hello” tool (e.g. `echo`) to validate end-to-end
- Deterministic unit tests using `github.com/theory-cloud/apptheory/testkit/mcp`.

Acceptance criteria:
- `go test ./...` passes locally.
- MCP behavior matches AgentCore expectations:
  - `initialize` returns capabilities containing `tools`
  - `tools/list` includes `echo`
  - `tools/call` succeeds for `echo`
  - server issues/echoes `mcp-session-id` header

---

### M2 — lesser-body infra stack: deploy Lambda + session store + SSM exports

Deliverables:
- CDK app under `cdk/` (stage-aware stack names).
- Stack provisions:
  - lesser-body MCP Lambda (ARM64, timeouts aligned with lesser API defaults)
  - **AppTheoryMcpServer** (API Gateway **HTTP API v2** `POST /mcp` → Lambda)
  - DynamoDB session table with TTL attribute (via `AppTheoryMcpServer.enableSessionTable`)
  - IAM permissions:
    - session table read/write
    - read-only access to required SSM params
- Stack writes `lesser-body` exports to SSM:
  - `mcp_lambda_arn`
  - `mcp_session_table_name` (if enabled)
  - `mcp_endpoint_url`

Acceptance criteria:
- `theory app up --stage dev` deploys deterministically (lockfile committed; `npm ci`).
- SSM exports exist after deploy and match the deployed resources.
- MCP Lambda uses DynamoDB-backed sessions when session table is enabled.

---

### M3 — lesser API integration: wire `POST /mcp` on the existing `api.<stageDomain>` domain

Deliverables (minimal changes to `lesser`):
- Add a `POST /mcp` integration on the existing `AppTheoryRestApiRouter`:
  - only when `soulEnabled=true`
  - Lambda is imported by ARN from `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`
- Publish required lesser → SSM exports (`table_name`, `domain`, etc.) if not already present.
- Remove/disable any `/soul/*` CloudFront → Lambda URL routing assumptions when `soulEnabled=true`.

Acceptance criteria:
- With `soulEnabled=false`, no `/mcp` route is available.
- With `soulEnabled=true` and SSM param present:
  - `POST https://api.<stageDomain>/mcp` returns a valid MCP JSON-RPC response.
- No Lambda Function URL is required anywhere in the flow.

---

### M4 — Authentication + authorization (reuse lesser credentials)

Deliverables:
- Auth hook in MCP Lambda (`apptheory.WithAuthHook`) that validates `Authorization: Bearer ...` using lesser’s credential model:
  - agent OAuth token
  - instance API key (for ops/automation)
  - (optional) self-sovereign signature challenge (see M7)
- Route definition uses `RequireAuth()` so `/mcp` fails closed if auth is not configured.
- Tool-level authorization checks (capability gating) based on the authenticated agent’s capabilities (when available).

Acceptance criteria:
- Requests without valid auth fail with a deterministic error (no tool execution).
- A valid agent credential is mapped to an immutable identity (`agentId`) for the request context.
- Audit logging includes request ID + agent identity + tool name (no secrets).

---

### M5 — Core toolset (AgentCore-first)

Deliverables (phased tool rollout; all tools are MCP tools, not resources/prompts):
- Social read tools:
  - `profile_read`
  - `timeline_read` (home/local/federated as supported)
  - `post_search`
  - `followers_list`
  - `following_list`
  - `notifications_read`
- Social write tools:
  - `post_create`
  - `post_boost`
  - `post_favorite`
  - `follow`
  - `unfollow`
  - `profile_update` (optional for v1 if media wiring is ready)

Implementation guidance:
- Prefer reusing lesser’s existing storage/auth packages over re-implementing table access logic.
- Use TableTheory for direct DynamoDB access where reuse is not available.

Acceptance criteria (for each tool shipped):
- Tool appears in `tools/list` with correct JSON schema.
- Tool rejects invalid params with JSON-RPC `Invalid params`.
- Tool respects auth scope (cannot act as a different agent).
- Unit tests exist for happy path + one failure path.

---

### M6 — Memory timeline (optional but common)

Deliverables:
- `memory_append` and `memory_query` tools backed by DynamoDB (preferably the existing lesser table; otherwise a dedicated table).
- TTL support for ephemeral memory (`expiresAt`).
- (later) retention sweeper to archive warm/cold memories to S3.

Acceptance criteria:
- Appending a memory event is idempotent (eventId-based) or clearly documented as append-only.
- Query supports time range + text filtering within practical limits.
- TTL removal works in dev (short TTL test).

---

### M7 — Soul registration + discovery docs (integrate with lesser-soul)

Deliverables:
- `GET /.well-known/mcp.json` published by the lesser API (or by lesser-body behind the same domain) describing:
  - MCP server URL
  - supported capabilities (tools; resources/prompts when enabled)
  - auth hints (OAuth endpoints / self-sovereign challenge endpoint)
- Registration update flow to lesser-soul (when API stabilizes):
  - update agent registration file `endpoints.mcp` to the deployed MCP URL

Acceptance criteria:
- `.well-known/mcp.json` is reachable at `https://<stageDomain>/.well-known/mcp.json` and `https://api.<stageDomain>/.well-known/mcp.json`.
- When lesser-soul integration is enabled, the agent registration file reflects the MCP endpoint within one deploy cycle.

---

### M8 — Managed provisioning: default-on in lesser-host

Deliverables:
- lesser-host provisioning sequence deploys:
  1) lesser (as today)
  2) lesser-body (new, default-on)
  3) lesser API update with `soulEnabled=true` (wires `/mcp`)
- Instance-level toggle remains supported (operator can disable).

Acceptance criteria:
- A newly provisioned managed instance has a working MCP endpoint without manual steps.
- Disabling the add-on removes the `/mcp` route (or returns a deterministic “disabled” response).

---

### M9 — Full MCP (resources + prompts) (available in AppTheory v0.11.0; optional for non-AgentCore clients)

Deliverables:
- Implement `SPEC.md` resources/prompts using AppTheory’s `runtime/mcp` registries:
  - `srv.Resources()` for `resources/*`
  - `srv.Prompts()` for `prompts/*`

Acceptance criteria:
- A non-AgentCore MCP client can list + read resources and list + fetch prompts.
- `initialize` advertises `resources`/`prompts` capabilities when enabled.
