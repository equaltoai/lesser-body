# Enumerated changes: actor-initiated Ptah soul recovery

### 1. Synchronize the Host contract mirror used by the baseline gate

- **Paths:** `internal/ptahserver/testdata/host-contract/pr-978/*`
- **Surface:** Host contract fixture
- **Classification:** test-coverage / integration correctness
- **Impact:** no scope/profile or MCP change
- **Acceptance:** the mirror includes Host's recovery separation paragraph and its checksum guard passes with the current sibling Host checkout.
- **Validation:** targeted Ptah fixture test plus full Go gates
- **Commit:** `test(ptah): sync hosted genesis contract mirror`

### 2. Add a strict Host recovery client and declaration materializer

- **Paths:** `internal/soulapi`, `internal/hostapi`, new shared recovery materialization package, `internal/agentcontent`
- **Surface:** Host client and Body content persistence
- **Classification:** security / Host integration / operational correctness
- **Impact:** no gate loosening; no Lesser or existing MCP contract change
- **Acceptance:** exact raw declaration bytes are digest-verified; the closed Host envelope/version/provenance contract is enforced; published and legacy declarations produce deterministic Body soul/instruction seeds without fabricating legacy publication; replay never overwrites owner content.
- **Validation:** package tests, full Go gates
- **Commit:** `feat(recovery): verify and materialize host declarations`

### 3. Persist recovery provenance in the Ptah registry and fix live-directory joining

- **Paths:** `internal/agentregistry`, `internal/ptahserver`
- **Surface:** Body-owned registry and Ptah read projection
- **Classification:** operational correctness / bug-fix
- **Impact:** additive Ptah output metadata; no scope/profile change
- **Acceptance:** recovered rows retain classification/digest/source/version metadata, replay is local-ID conditional, and registry rows merge with Lesser live agents by verified local ID without duplicates.
- **Validation:** registry and Ptah tests, full Go gates
- **Commit:** `fix(ptah): reconcile recovered registry identities`

### 4. Add the actor-authorized self-recovery tool

- **Paths:** `internal/mcpserver`, `internal/runtimepolicy`
- **Surface:** Ka tool surface and authorization
- **Classification:** tool-surface / scope-profile / MCP-contract additive
- **Impact:** explicit write scope; souled-only; additive discovery
- **Acceptance:** `{}` self-recovers only the OAuth caller's bound agent and returns bounded lifecycle/provenance summaries; every mismatch fails before writes.
- **Validation:** registration, scope, profile, handler, idempotency, and error tests plus full Go gates
- **Commit:** `feat(mcp): add bound soul self recovery`

### 5. Wire Ka to Body-owned Ptah tables

- **Paths:** `cdk/lib`, `cdk/test`, generated managed templates if required by repo build
- **Surface:** CDK
- **Classification:** infrastructure / least privilege
- **Impact:** no SSM change; Ka receives instance account ID, table names, and read/write IAM only for content/registry tables
- **Acceptance:** synth templates contain the required environment and IAM without grant/session-table access.
- **Validation:** CDK tests and representative synth
- **Commit:** `feat(cdk): grant ka recovery table access`

### 6. Document and canary the additive recovery workflow

- **Paths:** `README.md`, `docs/mcp.md`, `docs/architecture.md`, `docs/configuration.md`, `docs/security.md`, `docs/deployment.md`, recovery canary/tests as needed
- **Surface:** public/operator contract
- **Classification:** docs / validation
- **Impact:** additive tool documentation only
- **Acceptance:** docs describe self authority, two classifications, idempotency/conflicts, configuration, rollout, and no Host/Lesser mutation.
- **Validation:** documentation checks, full build/test/vet/gofmt, release build, synth, governance rubric
- **Commit:** `docs(mcp): document actor soul self recovery`

## Self-check

All items are in mission, preserve or tighten gates, keep registration static, preserve existing OAuth/SSM/Lesser contracts, add no dependency, and leave deployment/merge authority unchanged.
