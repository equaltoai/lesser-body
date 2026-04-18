---
name: investigate-issue
description: Use when a user reports a bug, regression, or unexpected behavior in body — MCP client failing to connect, tool invocation returning wrong result, scope / profile rejection when it shouldn't, JWT validation failure, OAuth metadata shape issue, communication-tool delivery failure, lesser-integration failure, SSM export drift, session persistence bug, CDK deploy issue. Runs before any fix is proposed. Produces an investigation note, not a patch.
---

# Investigate an issue

Investigation comes before implementation. body has specific dimensions to investigation: 27 tools with scope / profile declarations, MCP JSON-RPC contract with external clients, OAuth 2.0 compliance with RFC 9728 protected-resource metadata, tight integration with lesser (JWT secret, DynamoDB, REST API, SSM exports), delegation to lesser-host for communication, and the three-step deploy coordination. A fix against a misunderstood symptom can strand connected MCP clients, bypass a security gate, or break the lesser↔body integration contract.

## Start with memory

Call `memory_recent` first. Scan for prior investigations in the same area — MCP client quirks, scope / profile interactions, lesser-integration edge cases, OAuth metadata compliance findings, communication-tool idempotency patterns. MCP-runtime investigations are context-dense.

## Capture the claim precisely

Record the user's report literally, then extract:

- **Symptom** — what was observed, verbatim where possible
- **Surface** — MCP discovery (`.well-known/mcp.json`) / OAuth metadata (`.well-known/oauth-protected-resource/mcp/<actor>`) / MCP JSON-RPC (`POST /mcp/<actor>`) / session persistence / SSM exports / CDK deploy
- **Tool implicated** (if tool invocation) — which of the 27 tools, from which group (social / memory / communication / identity)
- **Caller context** — MCP client identity (Claude, AgentCore, other), actor on whose behalf the call is made, scope declared in JWT, claimed runtime profile (drone / souled)
- **Instance context** — `(<app>, <stage>)` body is deployed against; lesser's soul-enabled state
- **Request shape** — JSON-RPC method, params (redact sensitive fields), correlation ID
- **Expected vs actual response**
- **Recent deploys** — body deploy, lesser deploy, any SSM parameter changes
- **Config state** — `LESSER_API_BASE_URL`, `LESSER_HOST_INSTANCE_KEY` presence, `MCP_SESSION_TABLE` state

## Ground the investigation

Your first structural questions are always:

1. **Is this a security / authorization issue?** If the symptom suggests scope bypass, profile bypass, JWT validation skipped, or a caller reaching a tool they shouldn't have — elevate. Route through `evolve-tool-surface` (for scope / profile correctness) or escalate to the user as security-suspected.
2. **Is this an MCP contract issue?** If the symptom is that a specific MCP client (Claude, AgentCore, other) is seeing wrong shape in discovery, OAuth metadata, or JSON-RPC, route through `preserve-mcp-contract`.
3. **Is this a lesser-integration issue?** If the symptom involves JWT validation (secret mismatch), DynamoDB read failures, lesser REST API response mismatches, SSM export drift, or `soulEnabled` wiring issues, route through `coordinate-with-lesser`.
4. **Is this a host-delegation issue?** If the symptom involves a communication tool (email, SMS) failing, message duplication, thread-resolution errors, or `LESSER_HOST_INSTANCE_KEY` authentication failures, this is host-delegation-specific.
5. **Is the symptom in body, in lesser, in lesser-host, in AppTheory's MCP runtime, or in the MCP client?** Many reported "body bugs" turn out to be lesser's API behavior, lesser-host's comm API behavior, MCP-client-side parsing, or AppTheory runtime issues. Confirm before accepting.
6. **Is this deploy-specific or config-specific?** A symptom on one `(<app>, <stage>)` that doesn't reproduce elsewhere often traces to config / SSM drift rather than code.

## Evidence before hypotheses

Gather before theorizing:

- `git log` on the affected internal package (`internal/auth`, `internal/mcpserver`, `internal/mcpapp`, `internal/lesserapi`, `internal/soulapi`, `internal/runtimepolicy`, `internal/memory`, `internal/soulbinding`, `internal/trustconfig`) since the last-known-good deploy
- `git blame` on the specific lines implicated
- body's Lambda version and deploy timestamp for the affected deployment
- CloudWatch logs for `lesser-body` Lambda for relevant request / correlation IDs (through the user; no direct AWS access)
- SNS error-topic messages for body (where configured)
- JWT-validation failure metrics for the affected time window
- Scope / profile rejection metrics (should be stable — spikes are signal)
- For lesser-integration: lesser's API response shape, lesser's DynamoDB item state for the affected actor, lesser's SSM-wiring readiness
- For communication tools: lesser-host's comm API response, messageId cache state, threading context
- For MCP contract: the `.well-known/mcp.json` and OAuth metadata actually served vs what the client expects
- For session persistence: DynamoDB session-table item for the affected actor
- For deploy issues: CloudFormation stack status, SSM parameter values under `/<app>/<stage>/lesser-body/exports/v1/`
- `query_knowledge` for cross-repo context — AppTheory MCP runtime patterns, TableTheory models, sibling equaltoai repos

If `memory_recent` or `query_knowledge` returns an auth error, stop — investigating MCP-runtime regressions without context continuity compounds risk.

## The specialist-routing question

Every investigation answers: **which specialist skill, if any, should handle this?**

- **Tool registration / scope / profile / behavior** → `evolve-tool-surface`
- **MCP JSON-RPC contract, discovery metadata, OAuth protected-resource metadata** → `preserve-mcp-contract`
- **Lesser integration** (JWT, DynamoDB, REST API, SSM) → `coordinate-with-lesser`
- **CDK deploy / stack / SSM parameter publishing / three-step order** → `deploy-body`
- **Framework awkwardness** (AppTheory MCP runtime, TableTheory) → `coordinate-framework-feedback`
- **Advisor-originated brief** → `review-advisor-brief`
- **None** — routes through standard `scope-need` → `enumerate-changes` → `plan-roadmap` → `implement-milestone` → `deploy-body`

## Rank hypotheses by evidence

List theories in descending order of support:

1. **Hypothesis** — one sentence
2. **Evidence for** — commits, logs, config state, client report
3. **Evidence against** — what would be true if this were wrong
4. **Verification step** — the cheapest test to prove or disprove it

## Output: the investigation note

```markdown
## Reported symptom
<verbatim>

## Dimensions
- Surface: <discovery / OAuth metadata / JSON-RPC / session / SSM / deploy>
- Tool implicated (if any): <name, group, declared scope, declared profile>
- Caller context: <MCP client, actor, JWT scope, claimed profile>
- Instance: <(app, stage), lesser soulEnabled state>
- Recent deploys: <body, lesser, SSM changes>

## Specialist elevation check
<normal / elevate to evolve-tool-surface / preserve-mcp-contract / coordinate-with-lesser / deploy-body / coordinate-framework-feedback / review-advisor-brief>

## What is definitely true
<verified facts — logs, SSM parameter values, item states, response shapes>

## Fix-locus verdict
<fix here (body) / fix upstream (AppTheory MCP runtime, TableTheory) / fix in sibling (lesser, host) / fix in client / fix in deployment config>

## Hypotheses (ranked)
1. <hypothesis> — evidence: <...>
2. <...>

## Verification step
<the one thing to run next>

## Proposed next skill
<investigate-issue again / fix directly / scope-need / evolve-tool-surface / preserve-mcp-contract / coordinate-with-lesser / deploy-body / coordinate-framework-feedback / review-advisor-brief / none — cross-repo report>
```

## Persist

Append only if the investigation surfaces something worth remembering — an MCP client quirk (Claude-specific parsing, AgentCore-specific discovery caching), a scope / profile interaction that surprised, a lesser-integration edge case (SSM propagation delay, JWT secret rotation timing), a communication-tool idempotency subtlety, a framework awkwardness worth reporting upstream, a deploy-order mistake pattern. Routine "typo" findings aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff rules

- **Authorization-bypass-suspected** — elevate to user; route through `evolve-tool-surface` with security framing.
- **MCP contract issue** — `preserve-mcp-contract`.
- **Lesser integration issue** — `coordinate-with-lesser`.
- **Deploy / stack / SSM issue** — `deploy-body`.
- **Framework awkwardness** — `coordinate-framework-feedback`; report upstream, don't patch locally.
- **Advisor brief** — `review-advisor-brief`.
- **Small, contained fix** — route through `scope-need` → `enumerate-changes` → `implement-milestone` → `deploy-body`.
- **Root cause in sibling or framework** — report cleanly to user; do not cross boundary.
- **Client-side (MCP client) issue** — document the finding, consider whether body needs defensive-handling or tolerance improvements.
