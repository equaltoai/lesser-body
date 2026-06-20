---
name: enumerate-changes
description: Use after scope-need and relevant specialist skills approve work. Takes the scoped-need document and produces a flat, ordered list of discrete changes required. Each change is scoped to be a single commit.
---

# Enumerate changes

A scoped need describes *what* is being delivered. An enumerated change list describes *what must move in the repo*. This skill is the transformation.

body's change lists are typically smaller than lesser's because the service has a tighter surface: one Lambda, one optional table, CDK stack with SSM exports, and 13 docs files. A narrow bug fix might be two to three commits; a tool addition with contract regeneration might be five to eight; an MCP-contract-evolving change with client-coordination might be more. The single-commit rule holds regardless of total count.

## Input required

An approved scoped-need document from `scope-need`. If the scope touches tool surface, MCP contract, lesser integration, host delegation, deploy, or framework consumption, the relevant specialist skill's findings also apply. Load prior context with `memory_recent`.

## The walk

Walk the scoped need against every surface of body:

1. **`cmd/lesser-body/main.go`** — Lambda entrypoint. App lifecycle, Lift / AppTheory bootstrap, signal handling.
2. **`internal/auth/`** — JWT validation, scope enforcement, principal context. Security-critical.
3. **`internal/mcpserver/`** — MCP tool / resource / prompt registration. 22 files. `registerTools()` is the load-bearing function.
4. **`internal/mcpapp/`** — AppTheory app lifecycle, runtime policy integration, audit emission. 33 files.
5. **`internal/lesserapi/`** — HTTP client calling lesser's REST API.
6. **`internal/memory/`** — DynamoDB-backed memory event store (memory tools).
7. **`internal/soulbinding/`** — soul binding lookup; determines runtime profile (drone vs souled).
8. **`internal/trustconfig/`** — managed-instance trust config and override precedence.
9. **`internal/soulapi/`** — HTTP client to lesser-soul / lesser-host for identity resolution.
10. **`internal/runtimepolicy/`** — runtime profile enforcement (drone vs souled tool filtering).
11. **`internal/lambdaentry/`** — Lambda entry handler and MCP session management.
12. **`cdk/`** — CDK TypeScript stacks (and any Go pinning). `stacks/main.go` or equivalent defines the Lambda, session table, SSM exports.
13. **`docs/`** — 13 operator / developer guides. Updates ride with behavior / contract changes.
14. **`scripts/build.sh`** — build artifact script.
15. **`go.mod` / `go.sum`** — dependency changes.
16. **`app-theory/app.json`** (if present) — AppTheory deployment contract.
17. **`AGENTS.md`** (if present) — repository guidelines. Rarely touched; governance-level.
18. **`README.md`** — top-level overview. Rides with integration or tool-surface changes.

A change that touches none of these isn't really a change.

## The ordering rules

1. **Test-first for bug fixes.** Add regression test first (fails against current code), then land the fix. Especially important for JWT-validation, scope-gate, profile-gate, and lesser-integration fixes.
2. **Auth / scope / profile changes land in isolated commits.** Authorization is security-critical; isolation matters for review and rollback.
3. **Tool-registration changes in `internal/mcpserver/` land together as the registration of a given tool** — registration happens at startup and a half-registered tool in a commit is a broken commit.
4. **CDK changes land separately from Go code changes.** CDK changes affect every deployment; isolation matters for bisect.
5. **SSM-export changes land alongside the Go code that publishes them** and ahead of any lesser-side change that reads the new shape.
6. **Documentation rides with the behavior it describes** — tool changes update `docs/mcp.md`, OAuth changes update `docs/oauth-migration.md`, deploy changes update `docs/deployment.md`, security changes update `docs/security.md`.
7. **Dependency bumps land in isolated commits** for bisect clarity.
8. **Framework-consumption changes** (AppTheory MCP runtime, TableTheory) that reflect idiomatic consumption of a new framework version land alongside the bump; framework awkwardness refers to `coordinate-framework-feedback`.

## The mission-scope rule

Every enumerated item must answer: **is this body-mission work, or is it scope growth outside?**

- **In-mission**: tool registration / refinement / removal, MCP contract preservation / evolution, scope / profile correctness, lesser-integration maintenance, host-delegation discipline, operational-reliability / security / observability, AGPL, docs, framework-consumption work.
- **Scope growth (refuse)**: dynamic tool registration, per-caller ACL, local email / SMS delivery, general-purpose agent framework, framework patches, non-lesser integration.

If any item is scope growth, stop and revisit `scope-need`.

## The scope-profile rule

Every enumerated item must also answer: **does this touch scope or profile gates?**

- **No** — default.
- **Yes — tightening or preserving** — enumerate normally with `evolve-tool-surface` findings referenced.
- **Yes — loosening** — refuse unless explicitly authorized with a documented reason (CVE remediation, explicit client coordination).

## The MCP contract rule

Every enumerated item must also answer: **does this touch the MCP contract (discovery, OAuth metadata, JSON-RPC shape)?**

- **No** — default.
- **Yes — additive / backward-compatible** — proceed with contract-regeneration commit.
- **Yes — breaking** — the `preserve-mcp-contract` walk must be complete; enumeration references coordination plan.

## The lesser-integration rule

Every enumerated item must also answer: **does this touch lesser integration (JWT, DynamoDB, REST API, SSM exports)?**

- **No** — default.
- **Yes — preserves contract** — enumerate with `coordinate-with-lesser` findings.
- **Yes — changes contract** — coordination with `lesser` steward required before enumeration finalizes.

## The framework-consumption rule

Every enumerated item must also answer: **does this consume AppTheory (especially MCP runtime) / TableTheory idiomatically?**

- **Idiomatic** — proceed.
- **Workaround** — stop. Route through `coordinate-framework-feedback`. The change may belong in the framework.

## The single-commit rule

Each enumerated item fits in one commit:

- One logical intent
- `go build ./...` succeeds
- `go test ./...` passes
- `go vet ./...` passes
- `gofmt -l .` / `goimports` leave the tree clean
- For CDK: `cdk synth -c app=<slug> -c stage=<stage> -c baseDomain=<domain>` succeeds
- `scripts/build.sh` produces a valid `dist/lesser-body.zip`
- No commit depends on a later item to compile or pass tests

## Output format

```markdown
### N. <imperative title>

- **Paths**: <files or directories touched>
- **Surface**: <cmd/lesser-body / internal/auth / internal/mcpserver / internal/mcpapp / internal/lesserapi / internal/memory / internal/soulbinding / internal/trustconfig / internal/soulapi / internal/runtimepolicy / internal/lambdaentry / cdk / docs / deps>
- **Classification**: <security / scope-profile / MCP-contract / tool-surface / lesser-integration / host-delegation / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>
- **Scope / profile impact**: <none / preserves / tightens — refuse if loosens>
- **MCP contract impact**: <none / additive / breaking — coordination referenced>
- **Lesser integration impact**: <none / preserves / changes — coordination referenced>
- **Framework consumption**: <idiomatic / reported via coordinate-framework-feedback>
- **Acceptance**: <one sentence: what makes this commit done>
- **Validation**: <`go test ./...`, `go vet ./...`, `gofmt -l .`, `scripts/build.sh`, `cdk synth` for representative context>
- **Conventional Commit subject**: `<type(scope): subject>`
```

## Self-check before handing off

- [ ] Every item is in-mission
- [ ] No item loosens scope or profile gates
- [ ] No item silently breaks MCP contract
- [ ] No item silently changes lesser-integration contract (JWT, DynamoDB, REST API, SSM exports)
- [ ] No item adds dynamic tool registration
- [ ] Tool additions / removals update `docs/mcp.md`
- [ ] Contract changes regenerate `.well-known/mcp.json` shape correctly
- [ ] CDK changes isolated from Go code
- [ ] SSM-export changes coordinated with lesser side
- [ ] Dependency bumps isolated
- [ ] Framework awkwardness routed to `coordinate-framework-feedback`
- [ ] Every item has a test or synth-smoke validation
- [ ] No hardcoded secrets or JWT material
- [ ] No full-JWT / full-recipient / raw-credential logging
- [ ] No AGPL-incompatible dependencies or proprietary blobs
- [ ] No deletion of Lambda versions or SSM exports
- [ ] Full list satisfies the scoped need's success criteria

## Persist

Append only if enumeration surfaces something unusual — a tool-registration interaction subtlety, a scope / profile edge case, a lesser-integration coordination subtlety, a CDK / SSM wiring gotcha, a framework-awkwardness pattern. Routine enumerations aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

Invoke `plan-roadmap` to sequence the flat list into phases and identify the per-stage rollout plan.