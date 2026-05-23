# Repository Guidelines

## GitHub provenance

When Codex handles GitHub activity for `equaltoai/lesser-body`—reading issues or PRs, commenting, reviewing, creating branches, committing bounded changes, opening PRs, or publishing check runs—use the local `github-provenance` skill (`.codex/skills/github-provenance/SKILL.md`). Prefer the routed `mcp__body_lab__` GitHub tools whenever they support the needed action so activity preserves body steward provenance.

Fallback to the general GitHub plugin or `gh` only when the routed tools do not expose the required capability, such as diffs, inline review comments, unresolved threads, Actions logs, labels, search, approvals, or large local git pushes. If a fallback performs a GitHub write, explain why and keep routed provenance where useful without adding noisy provenance-only comments.
