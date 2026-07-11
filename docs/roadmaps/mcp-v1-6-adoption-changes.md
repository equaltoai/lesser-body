# Enumerated changes: MCP v1.6 adoption

### 1. Bump AppTheory, TableTheory, and CDK pins

- **Paths**: `go.mod`, `go.sum`, `cdk/package.json`, `cdk/package-lock.json`, `app-theory/app.json`, `app-theory/init.md`
- **Surface**: deps, cdk, docs
- **Classification**: dependency-maintenance, framework-consumption
- **Scope / profile impact**: none
- **MCP contract impact**: semantic refinement via AppTheory strict Streamable HTTP enforcement; no body-specific shape change
- **Lesser integration impact**: none
- **Framework consumption**: idiomatic AppTheory v1.6.0 / TableTheory v1.8.3 consumption
- **Acceptance**: root and CDK modules pin AppTheory v1.6.0 and TableTheory v1.8.3 where applicable; npm CDK CLI is current; no direct/transitive FaceTheory pin exists in body.
- **Validation**: `go test ./...`, `cd cdk && npm test`, `go vet ./...`, `govulncheck ./...`, `cd cdk && npm audit`, `scripts/build.sh`
- **Conventional Commit subject**: `chore(deps): update theory framework pins`

### 2. Align MCP transport docs, probes, and tests with strict Streamable HTTP

- **Paths**: `docs/mcp.md`, `scripts/m0_baseline_mcp_probe.py`, `scripts/canary_host_mailbox_mcp.py`, `internal/mcpapp/*_test.go`, `internal/mcpserver/mcpserver_test.go`
- **Surface**: docs, scripts, internal/mcpapp, internal/mcpserver
- **Classification**: MCP-contract, test-coverage, operational-reliability
- **Scope / profile impact**: none
- **MCP contract impact**: semantic refinement; clients using standard Streamable HTTP continue to work, clients omitting strict `Accept` headers receive transport-level 400 from AppTheory v1.6
- **Lesser integration impact**: none
- **Framework consumption**: idiomatic; conforms to AppTheory v1.6 strict header and SSE priming behavior
- **Acceptance**: examples/probes send `Accept: application/json, text/event-stream` for `POST /mcp`; streaming tests tolerate the initial SSE priming frame and drain BodyReader when strict Accept selects SSE.
- **Validation**: `go test ./...`, Python compile for changed probes, `git diff --check`
- **Conventional Commit subject**: `test(mcp): align streamable http transport expectations`

### 3. Pin AppTheory MCP capability advertisement explicitly

- **Paths**: `internal/mcpserver/mcpserver.go`, `internal/mcpapp/*_test.go`, `docs/mcp.md`
- **Surface**: internal/mcpserver, internal/mcpapp, docs
- **Classification**: MCP-contract, operational-reliability, test-coverage
- **Scope / profile impact**: preserves existing gates
- **MCP contract impact**: none if configured to match current advertised capabilities; regression tests prevent accidental additive capabilities
- **Lesser integration impact**: none
- **Framework consumption**: idiomatic use of `mcp.WithCapabilityConfig(...)`
- **Acceptance**: `initialize` explicitly advertises tools/resources/prompts only when registered; completions/tasks/logging/resources.subscribe remain absent until their own milestones enable them.
- **Validation**: targeted initialize/capability tests, `go test ./...`, `go vet ./...`, `scripts/build.sh`
- **Conventional Commit subject**: `fix(mcp): pin advertised capability surfaces`

### 4. Harden cancellation handling for long-running MCP requests

- **Paths**: `internal/mcpapp/`, `internal/mcpserver/`, relevant HTTP client tests
- **Surface**: internal/mcpapp, internal/mcpserver, internal/lesserapi, internal/soulapi
- **Classification**: operational-reliability, test-coverage, MCP-contract
- **Scope / profile impact**: none
- **MCP contract impact**: semantic refinement; `notifications/cancelled` is already accepted by AppTheory and body verifies handlers respect cancellation
- **Lesser integration impact**: preserves existing REST API contracts; only context propagation and tests
- **Framework consumption**: idiomatic use of AppTheory cancellation propagation
- **Acceptance**: representative long-running read and streaming-tool paths stop work promptly when context is cancelled and do not leak goroutines or continue host/lesser calls unnecessarily.
- **Validation**: targeted cancellation tests, `go test ./...`, `go vet ./...`, `scripts/build.sh`
- **Conventional Commit subject**: `test(mcp): cover cancellation-aware tool execution`

### 5. Add output schema support for low-risk read tools

- **Paths**: `internal/mcpserver/*`, `internal/mcpapp/*_test.go`, `docs/mcp.md`
- **Surface**: internal/mcpserver, internal/mcpapp, docs
- **Classification**: tool-surface, MCP-contract, docs
- **Scope / profile impact**: preserves existing scope/profile declarations
- **MCP contract impact**: additive metadata in `tools/list` / discovery; no request/response behavior change
- **Lesser integration impact**: none
- **Framework consumption**: idiomatic use of `ToolDef.OutputSchema`
- **Acceptance**: selected read tools expose output schemas that match observed structuredContent and are regression-tested in tools/list/discovery.
- **Validation**: tool metadata tests, targeted tool tests, `go test ./...`, `go vet ./...`, `scripts/build.sh`
- **Conventional Commit subject**: `feat(mcp): add output schemas for read tools`

### 6. Add output schema support for communication and write tools

- **Paths**: `internal/mcpserver/*`, `internal/mcpapp/*_test.go`, `docs/mcp.md`
- **Surface**: internal/mcpserver, internal/mcpapp, docs
- **Classification**: tool-surface, MCP-contract, host-delegation, docs
- **Scope / profile impact**: preserves existing souled-only communication boundaries and write-scope gates
- **MCP contract impact**: additive metadata only
- **Lesser integration impact**: none
- **Framework consumption**: idiomatic use of `ToolDef.OutputSchema`
- **Acceptance**: communication and mutating tools expose output schemas that include idempotency/status fields without weakening host delegation, PII redaction, or scope/profile gates.
- **Validation**: communication tool tests, tool metadata tests, `go test ./...`, `go vet ./...`, `scripts/build.sh`
- **Conventional Commit subject**: `feat(mcp): add output schemas for write tools`

### 7. Add prompt/resource completion hooks

- **Paths**: `internal/mcpserver/mcpserver.go`, `internal/mcpserver/resources_prompts.go`, `internal/mcpapp/*_test.go`, `docs/mcp.md`
- **Surface**: internal/mcpserver, internal/mcpapp, docs
- **Classification**: MCP-contract, operational-reliability, docs
- **Scope / profile impact**: preserves profile gates; completions must not expose souled-only resources to drone callers
- **MCP contract impact**: additive `completions` capability and `completion/complete` method behavior for supported prompt/resource refs
- **Lesser integration impact**: none unless a future completion source calls Lesser APIs; this item should avoid that
- **Framework consumption**: idiomatic use of `mcp.WithCompletionHooks(...)`
- **Acceptance**: initialize advertises completions only after hooks are configured; completions return bounded, non-PII suggestions for known prompt/resource arguments and fail closed for unsupported refs.
- **Validation**: completion tests for souled and drone profiles, `go test ./...`, `go vet ./...`, `scripts/build.sh`
- **Conventional Commit subject**: `feat(mcp): add prompt and resource completions`

### 8. Prepare task runtime infrastructure behind disabled capability

- **Paths**: `cdk/`, `internal/mcpserver/mcpserver.go`, `docs/configuration.md`, `docs/deployment.md`, `docs/managed-deploy-contract.md`, `docs/managed-deploy-inventory.md`, release scripts/tests if managed assets change
- **Surface**: cdk, internal/mcpserver, docs, managed-deploy
- **Classification**: MCP-contract, lesser-integration, operational-reliability, docs
- **Scope / profile impact**: none while task capability remains disabled
- **MCP contract impact**: none until `Tasks` capability is enabled; CDK/storage preparation only
- **Lesser integration impact**: possible additive SSM export or managed-deploy artifact impact; coordinate before landing if export shape changes
- **Framework consumption**: idiomatic use of AppTheory task table/storage constructs; no local framework patch
- **Acceptance**: task storage can be provisioned and configured without advertising `tasks`; managed-release verification covers any new asset/export shape.
- **Validation**: `cd cdk && npm test`, representative CDK synth, release verifier tests if touched, `go test ./...`, `scripts/build.sh`
- **Conventional Commit subject**: `feat(cdk): prepare mcp task storage`

### 9. Pilot task-backed execution for one read-only long-running tool

- **Paths**: `internal/mcpserver/`, `internal/mcpapp/`, `docs/mcp.md`, `docs/security.md`, `docs/troubleshooting.md`
- **Surface**: internal/mcpserver, internal/mcpapp, docs
- **Classification**: MCP-contract, tool-surface, operational-reliability, test-coverage
- **Scope / profile impact**: preserves existing scope/profile gates; task support must not bypass read/write authorization
- **MCP contract impact**: additive `tasks` capability for protocol `2025-11-25` sessions and task-augmented `tools/call` for the selected tool
- **Lesser integration impact**: preserves existing API usage; selected tool should be read-only and cancellation-aware
- **Framework consumption**: idiomatic use of `mcp.WithTaskRuntime(...)` and `ToolExecution.TaskSupport`
- **Acceptance**: one selected read-only tool supports optional task execution with session-scoped state, TTL bounds, cancellation, audit events, and tests for task get/list/result/cancel and unauthorized access.
- **Validation**: task runtime tests, selected tool tests, `go test ./...`, `go vet ./...`, `scripts/build.sh`, CDK synth if task storage env is required
- **Conventional Commit subject**: `feat(mcp): pilot task-backed read execution`

## Self-check

- Every item is in-mission body work.
- No item loosens scope or profile gates.
- No item removes or renames existing tools, resources, prompts, endpoints, or SSM exports.
- Contract changes are additive or semantic refinements and require release notes.
- Task runtime remains gated until quota/audit/cancellation and storage policy are ready.
- Communication-tool delivery remains delegated to lesser-host.
- No dynamic tool registration or local framework patching.
