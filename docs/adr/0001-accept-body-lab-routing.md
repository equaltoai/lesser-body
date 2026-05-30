# ADR 0001: Accept and document body_lab steward routing

Date: 2026-05-30

## Status

Accepted.

## Context

LB-02 identified that the checked-in `.mcp.json` config points developer agents at a remote MCP server:

```text
https://lab.theorymcp.ai/equaltoai/agents/body/mcp
```

The local GitHub provenance guidance then asks agents to prefer `mcp__body_lab__` for repository reads and bounded writes.
That makes `lab.theorymcp.ai` a privileged supply-chain intermediary for this repo's developer-agent GitHub workflow.

Aron's governance ruling for Project 43 M1 is **ACCEPT & DOCUMENT**:

- accept `lab.theorymcp.ai` / `body_lab` as governed first-party EqualToAI/TheoryMCP infrastructure;
- keep `.mcp.json` and the routed GitHub provenance preference;
- do not add interactive approval gates that break non-interactive agent operation;
- remediate the residual risk with documentation, scope pinning where supported, and this ADR.

## Decision

`lesser-body` will keep the project-scoped `body_lab` MCP server and continue to prefer the routed body steward GitHub
path when tools support the needed action.

The repo documents the trust model in `docs/body-lab-trust.md` and summarizes it in `AGENTS.md`. The accepted posture is:

1. `lab.theorymcp.ai` is first-party EqualToAI/TheoryMCP steward-routing infrastructure.
2. The preferred GitHub path preserves routed-agent provenance and server-side policy narrowing.
3. `.mcp.json` pins the expected server URL and the OAuth scopes requested for this endpoint:
   `mcp:tools ai.kb.query memory.append`.
4. `.codex/config.toml` already uses the same endpoint, the same limited OAuth scopes, and `approval_mode = "approve"`
   only for `memory_append`.
5. No new per-tool interactive approval gates are added for GitHub write tools.
6. Endpoint allowlisting / TLS pinning is recommended as an operator or managed-client control, not invented as
   unsupported `.mcp.json` keys.

## Consequences

Positive outcomes:

- Body steward GitHub activity remains attributable to the routed `equaltoai/body` agent.
- The trust assumptions and residual blast radius are explicit in-repo.
- OAuth scope requests are pinned to the existing least-privilege posture used by Codex tooling.
- Non-interactive implementation assignments can still branch, commit, open PRs, and reply through the routed steward
  path.

Residual risks:

- A compromise of `lab.theorymcp.ai`, its DNS/TLS path, or the routed service operator could expose repository context
  visible to the steward endpoint or induce bounded repository writes through the GitHub App binding.
- Repo-local `.mcp.json` does not provide certificate/SPKI pinning or an authoritative client-enforced server allowlist.
  Operators who need that control must enforce it in managed client/fleet policy.

## Rejected alternatives

### Remove `.mcp.json` or disable `body_lab`

Rejected. This removes the accepted first-party steward route, loses routed GitHub provenance, and conflicts with Aron's
Project 43 M1 governance ruling.

### Remove the prefer-lab GitHub provenance guidance

Rejected. The routed path is preferred precisely because it gives branch, commit, PR, review, and check-run activity the
body steward provenance expected by this repo.

### Add per-tool interactive approval gates for GitHub writes

Rejected. Interactive approval gates break non-interactive agent operation. The accepted controls are server-side routed
policy, least-privilege OAuth scopes, bounded GitHub skills, normal repository review/branch protection, and operator
endpoint allowlisting where needed.

### Add unsupported pinning keys to `.mcp.json`

Rejected. `.mcp.json` should stay parseable by MCP clients. Endpoint allowlisting or TLS/SPKI pinning should be enforced
through client/fleet policy when supported, not by inventing repository-local schema fields.

## Validation expectations

Changes under this ADR should verify:

- `.mcp.json` parses as JSON;
- the routed `body_lab` MCP tools still work non-interactively;
- docs-only changes do not break the Go test/build gates used for body PRs when those gates are run.
