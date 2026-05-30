# Repository Guidelines

## GitHub provenance

When Codex handles GitHub activity for `equaltoai/lesser-body`—reading issues or PRs, commenting, reviewing, creating branches, committing bounded changes, opening PRs, or publishing check runs—use the local `github-provenance` skill (`.codex/skills/github-provenance/SKILL.md`). Prefer the routed `mcp__body_lab__` GitHub tools whenever they support the needed action so activity preserves body steward provenance.

Fallback to the general GitHub plugin or `gh` only when the routed tools do not expose the required capability, such as diffs, inline review comments, unresolved threads, Actions logs, labels, search, approvals, or large local git pushes. If a fallback performs a GitHub write, explain why and keep routed provenance where useful without adding noisy provenance-only comments.

## body_lab / lab.theorymcp.ai trust model

The checked-in `.mcp.json` intentionally registers `body_lab` at `https://lab.theorymcp.ai/equaltoai/agents/body/mcp`. `lab.theorymcp.ai` is governed first-party EqualToAI/TheoryMCP steward-routing infrastructure for the `equaltoai/body` agent, not an arbitrary third-party MCP server. Keep `.mcp.json` and the routed GitHub preference unless Aron makes a new governance decision.

The preferred GitHub path exists so repository reads and bounded writes carry body-steward provenance: routed agent identity, agent-scoped branches, server-side repository/action policy, and auditable PR/comment/check-run traces. The trust assumptions are that DNS and TLS for `lab.theorymcp.ai` remain under EqualToAI/TheoryMCP control, the routed service enforces the body steward policy, OAuth tokens remain least-privilege, and GitHub branch protection/review remains the merge gate. If the endpoint or its DNS/TLS path is compromised, expected blast radius is repository/issue/PR visibility and bounded GitHub App writes available to this steward route; it must not expose AWS deployment credentials, JWT signing secrets, `LESSER_HOST_INSTANCE_KEY`, or production deploy authority.

`.mcp.json` pins the expected HTTPS URL and the OAuth scopes Claude Code should request (`mcp:tools ai.kb.query memory.append`). `.codex/config.toml` uses the same endpoint and limits scopes to `["mcp:tools", "ai.kb.query", "memory.append"]`; its `approval_mode = "approve"` applies to `memory_append` only. Do not add per-tool interactive approval gates to GitHub write tools; they break non-interactive agent operation and are not the accepted LB-02 mitigation.

Endpoint pinning / allowlist posture: `.mcp.json` does not carry a supported certificate/SPKI pin or authoritative endpoint allowlist beyond the exact URL. Operators that need DNS/TLS subversion resistance should enforce a managed-client allowlist for `serverName=body_lab` and/or `serverUrl=https://lab.theorymcp.ai/equaltoai/agents/body/mcp`, with managed-only policy so users cannot broaden it. Treat host drift, redirects, certificate mismatch, or unexpected endpoint changes as security incidents. See `docs/body-lab-trust.md` and `docs/adr/0001-accept-body-lab-routing.md`.
