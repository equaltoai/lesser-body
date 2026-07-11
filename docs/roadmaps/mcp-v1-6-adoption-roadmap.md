# Roadmap: MCP v1.6 adoption

## Goal

Adopt AppTheory v1.6.0 MCP runtime enhancements in lesser-body deliberately: first by landing the framework baseline and strict Streamable HTTP compatibility, then by pinning advertised capabilities, improving client-facing metadata/completions, and only later enabling task-backed execution for read-only long-running tools under quota, audit, cancellation, and managed-deploy discipline.

## Classification

MCP-contract, tool-surface metadata, operational-reliability, test-coverage, dependency-maintenance, docs, CDK/managed-deploy for task phases.

## Surfaces affected

- `go.mod`, `go.sum`, npm CDK lockfiles
- `app-theory/app.json`, `app-theory/init.md`
- `internal/mcpserver/`
- `internal/mcpapp/`
- `docs/mcp.md`, `docs/configuration.md`, `docs/deployment.md`, `docs/security.md`, `docs/troubleshooting.md`
- `scripts/m0_baseline_mcp_probe.py`, `scripts/canary_host_mailbox_mcp.py`
- later task-runtime phases: `cdk/`, managed deploy docs/scripts if new storage/export surfaces are added

## Sibling-repo coordination

- lesser: not required for phases 1-4. Required before any task-storage SSM export, first-deploy contract change, or soul-enabled wiring expectation changes.
- host: not required for phases 1-4. Required before managed-deploy artifact/contract changes in task-storage phases, and before any future communication-tool tasking.
- soul: not required; no namespace shape changes.
- greater: not relevant.
- sim: optional validation consumer after completions/tasks are available.

## Framework coordination

- AppTheory: no upstream change required for the planned work. If task runtime or completion hooks lack a needed authorization/profile hook, route through `coordinate-framework-feedback` rather than patching locally.
- TableTheory: no upstream change required.

## MCP client coordination

- Claude: release-note advisory for strict Streamable HTTP headers, completions, and task capability when enabled.
- AgentCore: release-note advisory for additive capabilities and task behavior.
- Other MCP clients: release notes and docs updates; no breaking path/shape changes planned.

## Phases

### Phase 1: Framework baseline and strict transport compatibility

- Items: 1, 2
- Dependencies: none; already implemented on `codex/update-theory-pins-dependabot`
- Risks:
  - Older MCP clients that omit strict `Accept` headers receive AppTheory v1.6 transport 400s.
  - SSE parsers must tolerate initial empty priming frames.
- Mitigation: docs/canary/test updates and release-note advisory.

### Phase 2: Capability fail-closed hardening

- Items: 3, 4
- Dependencies: Phase 1
- Risks:
  - Misconfigured `CapabilityConfig` could accidentally hide resources/prompts.
  - Cancellation tests could reveal tool handlers that ignore context.
- Mitigation: initialize regression tests and targeted cancellation coverage.

### Phase 3: Tool metadata schemas

- Items: 5, 6
- Dependencies: Phase 2
- Risks:
  - Output schemas may drift from real `structuredContent`.
  - Communication schemas may accidentally expose PII-sensitive fields as normative.
- Mitigation: schema tests against actual tool results; sanitize/PII review for communication tools.

### Phase 4: Prompt and resource completions

- Items: 7
- Dependencies: Phase 2; may proceed before Phase 3 if output-schema work grows
- Risks:
  - Drone callers might receive souled-only resource or channel hints.
  - Completion responses could leak private reachability or mailbox-derived data.
- Mitigation: profile-aware tests; static/bounded completions only for first release.

### Phase 5: Task runtime storage readiness

- Items: 8
- Dependencies: Phase 2; coordination with lesser/host if SSM exports or managed-release artifact shape changes
- Risks:
  - CDK/managed-deploy drift.
  - Task table becomes provisioned but unused.
  - Operators mistake storage readiness for advertised task capability.
- Mitigation: keep `Tasks` disabled; document storage as preparatory; update managed-release verification if artifacts change.

### Phase 6: Read-only task runtime pilot

- Items: 9
- Dependencies: Phase 5, selected tool decision, quota/audit/cancellation policy
- Risks:
  - Task support could bypass scope/profile authorization if wired outside existing middleware.
  - Async work can outlive client connections and amplify expensive downstream reads.
  - Cancellation may be cooperative only.
- Mitigation: task support only after normal auth/profile middleware; read-only pilot only; session-scoped task store; TTL bounds; per-actor/tool quotas; audit events; cancellation tests.

## Stage rollout plan

### Lab / dev

- Command: `cdk deploy -c app=<slug> -c stage=<stage> -c baseDomain=<domain>` or `theory app up --stage <stage>`.
- Soak duration: at least one lab cycle per phase; longer for completions/tasks.
- Soak criteria:
  - discovery and OAuth protected-resource metadata respond correctly
  - initialize capabilities match expected phase
  - strict Streamable HTTP canaries pass
  - scope/profile rejections remain stable
  - representative MCP clients can connect and invoke tools

### Staging (where used)

- Command: same CDK/theory pattern with staging context.
- Soak duration: multiple days for completions/tasks; shorter for dependency/capability hardening if lab is clean.
- Soak criteria: integration partner/client validation and CloudWatch error-rate stability.

### Live

- Command: operator-run CDK deploy only after explicit authorization.
- Authorization: release notes + Arch-reviewed PR + successful lab/staging soak.
- Post-deploy monitoring:
  - Lambda error rate
  - MCP invocation success by method/tool
  - transport 400 rate for strict header failures
  - scope/profile rejection rate
  - JWT validation failures
  - DynamoDB session/stream/task table metrics where enabled
  - host comm-API delegation success where communication tools are exercised

## Deploy ordering

- Three-step first-time order required: unchanged for body deployments to a new `(app, stage)`.
- Existing deployments: subsequent deploy independence preserved.
- If Phase 5 adds SSM exports, coordinate with lesser/host before release and preserve `/v1/` additive semantics.

## Release artifact plan

- GitHub Release notes should call out:
  - AppTheory v1.6 strict Streamable HTTP headers
  - capability pinning behavior
  - output schema/completion/task additive capabilities as they land
  - operator config for task storage if enabled
- Managed-consumer impact: none for phases 1-4; coordinate with host for phases 5-6 if release artifacts/templates change.

## Rollback plan

- Lambda rollback: redeploy prior commit or retarget Lambda alias/version.
- CDK rollback: revert milestone commit and redeploy; never delete stack or published SSM exports.
- SSM-export rollback: only if a future task export is added; rollback deploy writes prior values, and additive exports should not break older lesser.
- Task-table rollback: if Phase 5/6 lands, preserve in-flight task data until TTL expiry; do not delete tables as rollback.

## AGPL posture

- No proprietary blobs added.
- No new dependencies planned beyond AppTheory/TableTheory/CDK updates already license-compatible with current stack.

## Advisor-brief authorization

Not applicable. This is Aron-direct scope.

## Open questions

- First task pilot tool selection remains open until Phase 6.
- Whether to publish an MCP task table SSM export remains open and requires lesser/host coordination if proposed.
- Output schema coverage can be staged by tool group if a single metadata milestone becomes too large.
