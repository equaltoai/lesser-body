---
name: scope-need
description: Use when a user brings a new capability, feature request, or enhancement need for body in vague terms. Interviews conversationally and produces a scoped-need document. Applies Gate 1 (MCP-mission alignment), Gate 2 (narrowest scope), and Gate 3 (specialist routing) before producing output.
---

# Scope a need

A need arrives fuzzy. A feature arrives sharp. This skill is the conversation that turns fuzzy into sharp, with three specific filters: MCP-mission alignment, narrowest-scope discipline, and specialist-skill routing.

## Your posture

You are interviewing, not pitching. body is the MCP capabilities runtime that extends lesser. Scope is bounded to that mission. The scoping question is always three-part:

1. **Is this MCP-capabilities work, scope/profile-correctness work, lesser-integration work, host-delegation work, operational-reliability work, AGPL work, or framework-feedback work — or is it scope growth outside the mission?**
2. **If it's in-mission, what is the narrowest possible scope that preserves MCP contract stability, scope / profile correctness, lesser integration, host delegation, AGPL coverage, and idiomatic framework consumption?**
3. **Does the change touch tool registration, the MCP contract, lesser integration, host delegation, deploy, or framework consumption? If yes, route to the appropriate specialist skill before enumeration.**

The default answer to Gate 1 for security, scope/profile-correctness, MCP-contract-stability, lesser-integration, host-delegation, operational-reliability, AGPL, and framework-feedback work is "yes, evaluate at Gate 2." The default for net-new capability outside the mission is "no."

## Start with memory and the architecture

- **Read `README.md` and `docs/`** for canonical architecture and contract before proposing scope.
- `memory_recent` — has this need or adjacent work been scoped before?
- `query_knowledge` — do AppTheory's MCP runtime, TableTheory, or sibling equaltoai repos already cover this concept?

If tools are unavailable, surface it and ask the user to re-auth.

## The interview

Ask, one or two at a time:

1. **Who is asking and why now?** MCP client maintainer report, operator request, CVE response, advisor-dispatched brief, or Aron-direct scope?
2. **What problem does it solve?** Current pain, not speculative improvement.
3. **Which surface does it touch?** Tool surface (add / modify / remove a tool), MCP contract (discovery / OAuth metadata / JSON-RPC shape), scope / profile gates, lesser integration (JWT / DynamoDB / REST API / SSM), host delegation (communication tools), CDK / deploy, AGPL.
4. **Which consumers are affected?** MCP clients (Claude, AgentCore, other), operators deploying body, sibling repos (lesser for integration, host for delegation).
5. **Is this a new tool, modified tool, or removed tool?** Tool-surface changes are contract changes for every caller.
6. **Does this affect scope or profile semantics?** Any silent loosening is refused.
7. **Does this affect lesser integration?** JWT secret handling, DynamoDB access patterns, REST API usage, SSM export shape.
8. **Does this affect host delegation?** Communication-tool contract, idempotency, thread resolution.
9. **Is this growth or correctness?** Both welcome with different framing.
10. **What does success look like?** Observable. For tool work, "this MCP client can invoke the tool and get the expected result." For contract work, "this client's discovery succeeds with the new shape." For integration work, "body and lesser deploy in coordination without SSM drift."
11. **What is explicitly out of scope?**

## The three gating questions

### Gate 1: Is this body-mission work?

Six possible verdicts:

1. **Yes — security / scope-profile correctness work.** CVE response, authorization gate fix, JWT validation bug. Always accepted. Proceed to Gate 2. Route through `evolve-tool-surface` for gate-specific concerns.
2. **Yes — MCP contract stability work.** Discovery metadata correctness, OAuth protected-resource metadata compliance, JSON-RPC shape refinement. Accepted. Route through `preserve-mcp-contract`.
3. **Yes — tool surface work.** New tool addition, tool behavior refinement, scope / profile declaration. Accepted. Route through `evolve-tool-surface`.
4. **Yes — lesser integration or host delegation work.** JWT handling, DynamOdB access, REST API consumption, SSM export shape, communication-tool delegation. Accepted. Route through `coordinate-with-lesser` or integrate with host-delegation discipline in `evolve-tool-surface`.
5. **Yes — operational-reliability or AGPL-discipline work.** Latency fix, observability addition, rate-limiting tuning, AGPL-compliance fix. Accepted.
6. **Yes — framework-feedback work.** A body concern surfaces AppTheory MCP runtime awkwardness; the scoped output is an upstream signal. Route through `coordinate-framework-feedback`.
7. **No — out-of-scope growth.** Net-new capability outside MCP-for-lesser-agents, dynamic tool registration, per-caller ACL, local email / SMS delivery, general-purpose agent framework, non-lesser integration. Produces a redirect document.

### Gate 2: What is the narrowest possible scope?

Prefer:

- Tool additions that serve specific agent workflows
- Tool refinements scoped to the specific reported behavior
- Additive MCP contract changes (new tool in discovery, new optional JSON-RPC parameter) over breaking changes
- Scope / profile tightening over loosening
- Lesser-integration changes that preserve SSM contract shape
- Host-delegation changes that preserve message idempotency and PII discipline
- Dependency bumps within current major versions
- Static tool-registration patterns (never dynamic)

Avoid:

- Refactors "while we're in there"
- Tools that generalize beyond a specific need
- Scope or profile loosening
- Bypassing lesser's REST API for data that lesser's API exposes idiomatically
- Bypassing lesser-host's comm APIs for local delivery
- Adding dependencies with AGPL-incompatible licenses
- Changing SSM export names or shapes
- Changing the three-step first-deploy order

### Gate 3: Specialist routing

If the change touches any of these, the specialist skill runs before enumeration:

- **Tool registration, scope declaration, profile declaration, per-tool behavior** → `evolve-tool-surface`
- **MCP contract (discovery, OAuth metadata, JSON-RPC shape)** → `preserve-mcp-contract`
- **Lesser integration (JWT, DynamoDB, REST API, SSM exports)** → `coordinate-with-lesser`
- **CDK deploy, SSM publishing, three-step order** → `deploy-body`
- **Framework awkwardness (AppTheory MCP runtime, TableTheory)** → `coordinate-framework-feedback`
- **Advisor-dispatched brief** → `review-advisor-brief`

Specialist findings feed back into enumeration. Skipping them is scope shortcut that routinely becomes expense.

## Output: the scoped-need document

### For Gate 1 verdict "body-mission work":

```markdown
# Scoped Need: <short name>

## Background
<one paragraph>

## Driver
<MCP client / operator / CVE / advisor-brief / Aron-direct>

## Problem
<what is broken, missing, or painful today>

## Surface affected
<tool surface / MCP contract / scope-profile / lesser integration / host delegation / CDK / docs>

## Tool(s) affected (if tool-surface work)
<named — from social / memory / communication / identity groups>

## Classification
<security / scope-profile-correctness / MCP-contract / tool-surface / lesser-integration / host-delegation / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Narrowest-scope proposal
<smallest change that addresses the need>

## What this need explicitly does not cover
<bounded scope>

## Success criteria
<observable, testable>

## Specialist routing
- Tool surface: <not touched / walk required via evolve-tool-surface>
- MCP contract: <not touched / walk required via preserve-mcp-contract>
- Lesser integration: <not touched / walk required via coordinate-with-lesser>
- Framework consumption: <idiomatic / awkwardness reported via coordinate-framework-feedback>
- Deploy: <not touched / walk required via deploy-body>
- Advisor brief: <n/a / review required via review-advisor-brief>

## Consumer impact
<MCP clients / operators / sibling repos>

## AGPL posture
<no change / confirmed AGPL-compatible / decision required>

## Open questions
<unresolved>
```

### For Gate 1 verdict "out-of-scope growth":

```markdown
# Redirect: <short name>

## Background
<one paragraph — what was asked>

## Why this doesn't belong in body
<scope bounded to MCP capabilities for lesser agents; this is X, which belongs in Y>

## Appropriate owner
<lesser / host / Theory Cloud framework / separate new repo / scoping with Aron>

## Path for the requesting user
<rough outline — escalate to steward Y, defer, start new repo>

## Recommended next step
<specific handoff>
```

## Persist before handoff

Append only if scoping surfaces a recurring pattern. Routine scope completions aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **In-mission, tool surface** — invoke `evolve-tool-surface` before enumeration.
- **In-mission, MCP contract** — invoke `preserve-mcp-contract` before enumeration.
- **In-mission, lesser integration** — invoke `coordinate-with-lesser` before enumeration.
- **In-mission, framework-feedback** — invoke `coordinate-framework-feedback`; output may be an upstream report rather than local enumeration.
- **In-mission, deploy-adjacent** — invoke `deploy-body` for CDK / SSM discipline.
- **Advisor-dispatched** — `review-advisor-brief` already ran; output includes Aron's authorization.
- **In-mission, none of the above** — invoke `enumerate-changes` directly.
- **Out-of-scope** — redirect document *is* the handoff.
- **Resolved to "no change needed"** — record and stop.
- **User defers** — record and stop.
