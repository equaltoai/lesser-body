---
name: plan-roadmap
description: Use after enumerate-changes. Takes a flat enumerated change list and sequences it with dependencies, risks, and a per-stage rollout plan. Produces a roadmap document, not code or project state.
---

# Plan a roadmap

A flat enumerated list answers "what changes." A roadmap answers "in what order, with what risks, through which stages, with what coordination outside this repo." This skill is the bridge.

body's roadmaps are simpler than lesser's (one Lambda, CDK with SSM exports) but demand attention on the **lesser↔body integration coordination axis** — SSM export changes, JWT secret rotation, DynamoDB schema evolution from lesser's side, and the three-step first-deploy order.

## Input required

An approved enumerated change list from `enumerate-changes`. Specialist-skill findings (from `evolve-tool-surface`, `preserve-mcp-contract`, `coordinate-with-lesser`, `deploy-body`, `coordinate-framework-feedback`, `review-advisor-brief`) if applicable. Load prior context with `memory_recent`.

## Dependency analysis

For each enumerated item, identify:

- **Hard dependencies** — items that must land first to compile or pass tests
- **Soft dependencies** — items that should land first for review coherence
- **Sibling-repo coordination dependencies** — items that require `lesser` steward (for JWT / DynamoDB / REST API / SSM contract changes) or `host` steward (for communication-tool delegation changes) to be aware or to act in parallel
- **Framework coordination dependencies** — items requiring AppTheory (especially MCP runtime) or TableTheory stewards to be aware
- **MCP client coordination dependencies** — items where breaking changes require ecosystem notification (Claude, AgentCore, other clients)
- **Parallelizable siblings** — items with no ordering constraint

## Phase shape

Canonical phase patterns for body:

1. **Infrastructure / dependency baseline** — Go module bumps, CDK foundation changes, AppTheory / TableTheory version bumps. Lands first.
2. **Internal package changes (auth / services / clients)** — Go changes to `internal/*`. Lands before tool-surface or MCP-handler changes that consume them.
3. **Tool-registration changes** — `internal/mcpserver/` additions, modifications, removals. Ride with corresponding `internal/mcpapp/` changes where lifecycle is affected.
4. **Lesser-integration changes** — `internal/lesserapi/`, `internal/memory/`, `internal/soulapi/`, `internal/trustconfig/`. Coordinate with `lesser` steward for contract-touching items.
5. **CDK changes** — infrastructure adjustments. Isolated from Go code.
6. **SSM-export changes** — published alongside the Go code that emits them; lesser-side updates are a separate concern for the `lesser` steward.
7. **Contract regeneration** — `.well-known/mcp.json` shape updates if tool / resource / prompt registration changed.
8. **Documentation** — `docs/mcp.md`, `docs/deployment.md`, `docs/oauth-migration.md`, `docs/security.md`, etc.

Not every roadmap uses all phases. A pure bug fix may be one phase; a tool addition with contract and docs is typically three or four. More than six phases suggests scope crept past the scoped need; revisit `scope-need`.

## Stage rollout discipline

Every roadmap answers: **how does this reach `live` safely, for connected MCP clients, across the deployments that use body?**

Default rollout:

1. **Feature branch work completes.** Tests pass. Required review. Merge to `main`.
2. **Deploy to `lab` / `dev`** via CDK. Observe MCP endpoint responds correctly, discovery metadata current, OAuth flow works, tool invocations succeed with correct scope / profile gating.
3. **Soak in `lab`.** Evidence that MCP clients (test accounts) can connect, discover, authenticate, and invoke tools. Communication-tool delegation to lesser-host works. Session persistence retains across invocations (if enabled).
4. **Deploy to `staging`** if used. Integration partners exercise real MCP flows.
5. **Soak in `staging`.** Typically multiple days for non-trivial changes; longer for MCP-contract or scope / profile changes.
6. **Deploy to `live`** with explicit operator authorization.
7. **Post-deploy monitoring**:
   - CloudWatch error rate for `lesser-body` Lambda
   - MCP invocation success rate by tool
   - Scope / profile rejection rate (should be stable — spikes signal)
   - JWT-validation failure rate
   - DynamoDB session-table capacity (if used)
   - SNS error-topic messages
   - MCP-client-side integration signals (via operators / client maintainers)
   - Host comm-API delegation success rate (for communication tools)

**Never set timeouts on CDK deploys.** Let them run to completion. If stuck, check CloudFormation console state through the user — don't abort.

**Never skip `lab` soak for urgency.** Hotfix compression is possible within stages, not by skipping.

## The three-step first-deploy coordination

For **first-time deploys** to a new `(<app>, <stage>)`, the roadmap explicitly accounts for the three-step order:

1. Deploy lesser **without** `soulEnabled` (handled by `lesser` steward).
2. Deploy body (this roadmap's work).
3. Deploy lesser **with** `soulEnabled=true` (handled by `lesser` steward).

For **subsequent deploys** to existing deployments, body and lesser update independently; no ordering. The roadmap names which case applies.

## Per-deployment dimension

Operators deploy body alongside their lesser instances. The roadmap does not prescribe which deployments upgrade when; instead:

- **Document the release via GitHub Release** with release notes
- **Document breaking changes prominently** (MCP contract shifts, scope / profile semantic changes, SSM contract evolution)
- **Provide migration guidance** for operators upgrading
- **Communicate MCP-client-facing changes** so MCP client maintainers can test ahead

For managed deployments provisioned by lesser-host, coordinate with the `host` steward for release-artifact shape and the provisioning-worker's ingestion.

## Risk register

- **Known unknowns** — things you know you don't know
- **MCP contract risks** — for contract changes, which clients are most likely affected?
- **Scope / profile risks** — for gate changes, what's the failure mode? Fail-closed is the expected default.
- **Lesser-integration risks** — JWT secret rotation timing, DynamoDB schema evolution on lesser's side, REST API response drift, SSM propagation delay
- **Host-delegation risks** — lesser-host comm API availability, `LESSER_HOST_INSTANCE_KEY` rotation, message-idempotency corner cases
- **Framework-compat risks** — AppTheory MCP runtime version assumptions
- **CDK / IaC risks** — stack-update failures, SSM parameter mutation, session-table migration
- **Three-step deploy risks** — first-time deploys must follow the order; regressions here block operator onboarding
- **MCP client risks** — Claude, AgentCore, and others may have different tolerance for contract evolution
- **AGPL-adjacent risks** — new dependencies requiring license vetting
- **Rollback risks** — SSM-export changes that cascade into lesser's soul-enabled wiring

A risk with no mitigation is a blocker. Call it out; do not proceed.

## Output format

```markdown
# Roadmap: <scoped-need name>

## Goal
<one paragraph — what the full roadmap delivers and why>

## Classification
<security / scope-profile / MCP-contract / tool-surface / lesser-integration / host-delegation / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Surfaces affected
<enumerated from the change list>

## Sibling-repo coordination
- lesser: <required / not required, what — specifically for JWT / DynamoDB / REST API / SSM-contract touches>
- host: <required / not required, what — specifically for comm-API / managed-deploy touches>
- soul: <required / not required — namespace shape>
- greater: <unlikely relevant>
- sim: <unlikely relevant>

## Framework coordination
- AppTheory (MCP runtime especially): <required / not required, what>
- TableTheory: <required / not required, what>

## MCP client coordination
- Claude: <no impact / release-note advisory / explicit coordination>
- AgentCore: <...>
- Other MCP clients: <...>

## Phases

### Phase 1: <name>
- Items: <enumerated item numbers>
- Dependencies: <what must land first>
- Risks: <bullet list>

### Phase 2: <name>
...

## Stage rollout plan

### Lab / dev
- Command: `cdk deploy -c app=<slug> -c stage=<stage> -c baseDomain=<domain>` (or `theory app up --stage <stage>`)
- Soak duration: <...>
- Soak criteria: <observable evidence required>

### Staging (where used)
- Command: <...>
- Soak duration: <...>
- Soak criteria: <...>

### Live
- Command: <...>
- Authorization: <operator-run; release notes + migration guidance>
- Post-deploy monitoring plan: <...>

## Deploy ordering (if applicable)
- Three-step first-time order required: <yes / no (subsequent deploy, order independent)>
- If yes: confirm lesser steward is aware and sequenced

## Release artifact plan
- GitHub Release: <version tag>
- Release notes: <breaking changes, migration, contract changes, MCP-client impact>
- Managed-consumer (lesser-host) impact: <none / coordinated with host steward>

## Rollback plan
- Lambda-version rollback: <prior version>
- CDK stack rollback: <revert commit + redeploy>
- SSM-export rollback: <prior export values — lesser-side coordination>
- Session-table rollback: <if schema changed>

## AGPL posture
- No proprietary blobs added: <confirmed>
- Dependency license vetting: <completed if applicable>

## Advisor-brief authorization (if applicable)
- Brief source: <advisor identity, email provenance>
- Aron's authorization: <scope, date, notes>

## Open questions
<unresolved>
```

## Persist

Append only if the roadmap exposes a recurring risk pattern, a lesser-integration coordination subtlety worth remembering, an MCP client rollout pattern, or a release-artifact detail affecting managed consumers. Routine roadmaps aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- If approved, invoke `create-github-project` (or proceed informally if the roadmap is too small).
- If rollout plan surfaces coordination not yet happening (lesser steward uninformed, host steward uninformed, MCP client maintainers not advised), pause and surface first.
- If the roadmap reveals scope growth, revisit `scope-need`.
- If the roadmap is a security / scope-profile response requiring compressed cadence, ensure authorization is explicit.
