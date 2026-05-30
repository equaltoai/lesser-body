# body_lab MCP routing trust model

Date: 2026-05-30

`lesser-body` intentionally carries a project-scoped MCP client configuration for the routed body steward endpoint:

```text
body_lab = https://lab.theorymcp.ai/equaltoai/agents/body/mcp
```

This note documents the trust model for that endpoint and the accepted mitigations for LB-02.

## Ownership and role

`lab.theorymcp.ai` is first-party EqualToAI/TheoryMCP infrastructure. The `body_lab` route is the governed steward-routing
service for the `equaltoai/body` agent, not an arbitrary third-party MCP server. It exposes this steward's routed tools,
including memory, mailbox, knowledge, and bounded GitHub operations for `equaltoai/lesser-body`.

The route is preferred for GitHub activity because it preserves body-steward provenance:

- GitHub reads and writes are routed through the EqualToAI/TheoryMCP binding for this body steward.
- Branches are agent-scoped (`theorymcp/equaltoai/body/...`) and easier to attribute than local personal-token pushes.
- Server-side policy narrows which repositories and actions the steward endpoint can use.
- PRs, comments, reviews, commits, and check runs can carry the routed agent identity and trace material.
- The local `github-provenance` skill still requires normal repo discipline: no unrequested writes, no force-pushes,
  no bypassing review, no production deploys, and no noisy provenance-only comments.

This trust decision is specific to EqualToAI/TheoryMCP-controlled routing. It does not imply trust in arbitrary remote
MCP servers.

## Trust assumptions

Using `body_lab` assumes all of the following remain true:

1. `lab.theorymcp.ai` DNS resolves to EqualToAI/TheoryMCP-controlled infrastructure.
2. TLS terminates with a valid certificate for `lab.theorymcp.ai` issued through the expected public WebPKI path.
3. The TheoryMCP service enforces the routed body steward's server-side mailbox, memory, and GitHub policies.
4. OAuth tokens used by developer tooling are least-privilege for this endpoint.
5. GitHub writes remain bounded to user-requested work and repository policy; the route is not a shortcut around review.
6. The steward keeps a live memory connection before and after non-trivial work; loss of memory access is a stop condition.

If the endpoint, DNS/TLS path, or route operator were compromised, the expected blast radius is repository/issue/PR data
visible to the steward endpoint and bounded repository writes available through the configured GitHub App binding. It is
not intended to grant AWS production access, lesser deployment credentials, `LESSER_HOST_INSTANCE_KEY`, JWT signing
material, or a bypass around GitHub branch protection.

## Repo-local configuration posture

`.mcp.json` keeps the `body_lab` project-scoped remote MCP server because Aron accepted this first-party routing model.
It also pins the OAuth scopes Claude Code should request for that server to the same least-privilege set used by
`.codex/config.toml`:

```text
mcp:tools ai.kb.query memory.append
```

`.codex/config.toml` additionally records the same endpoint as both `url` and `oauth_resource`, requests only
`["mcp:tools", "ai.kb.query", "memory.append"]`, and sets `approval_mode = "approve"` for `memory_append`. That
memory approval posture is intentionally limited to steward memory writes. Do not add per-tool interactive approval gates
to body_lab GitHub write tools; those gates break non-interactive agent operation and are not the accepted LB-02
remediation.

## Endpoint pinning and allowlist note

`.mcp.json` can name the exact HTTPS URL and pin OAuth scopes, but it is not an endpoint/TLS pinning policy file. Do not
invent unsupported keys in `.mcp.json` for certificate fingerprints, SPKI pins, or approval prompts.

Recommended operational controls for environments that need stronger DNS/TLS subversion resistance:

- Allow only this routed MCP endpoint for this repo's body steward tooling:
  `https://lab.theorymcp.ai/equaltoai/agents/body/mcp`.
- If using Claude Code managed MCP policy, enforce an allowlist entry by exact `serverUrl` and/or `serverName=body_lab`,
  and set the managed-only mode so user/project settings cannot broaden the allowlist.
- Monitor certificate transparency / issuance policy for `lab.theorymcp.ai` and maintain normal DNS change controls for
  the TheoryMCP zone.
- Treat any redirect, host mismatch, unexpected certificate chain, or endpoint URL drift as a security incident for
  steward tooling.

The non-interactive repo-local mitigation is therefore scope pinning plus documentation. Authoritative endpoint allowlist
or TLS pinning belongs in client/fleet policy outside `.mcp.json` until the client schema supports it directly without
interactive approval prompts.

## Rejected mitigations

- Removing `.mcp.json` or disabling `body_lab`: rejected because it discards first-party steward provenance and breaks
  the accepted workflow.
- Removing the `github-provenance` preference for routed tools: rejected for the same provenance reason.
- Adding per-tool interactive approval gates for GitHub writes: rejected because non-interactive agent operation is a
  project requirement.
- Trusting the endpoint silently without documentation: rejected; this note and the ADR record the governance decision,
  assumptions, residual risk, and mitigations.

## References

- ADR: `docs/adr/0001-accept-body-lab-routing.md`
- GitHub provenance workflow: `AGENTS.md` and `.codex/skills/github-provenance/SKILL.md`
- Claude Code MCP project-scoped config and OAuth-scope support: <https://code.claude.com/docs/en/mcp>
- Claude Code managed MCP allowlists: <https://code.claude.com/docs/en/managed-mcp>
