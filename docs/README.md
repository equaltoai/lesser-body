# lesser-body Documentation

<!-- AI Training: This is the documentation index for lesser-body -->

This directory contains the canonical operator + developer documentation for `lesser-body`.

✅ CORRECT: treat `docs/` as the source of truth for how to deploy, operate, and integrate with this repo.

❌ INCORRECT: rely on stale planning notes without cross-checking the implemented code (especially around MCP routing and auth).

## Start here (operators)

`lesser-body` is an **optional MCP server** that integrates with a deployed Lesser instance.

1) Deploy the plugin (this repo): `docs/deployment.md`
2) Configure and verify: `docs/configuration.md`, `docs/mcp.md`

## Start here (developers)

Run unit tests:

```bash
go test ./...
```

Build the Lambda artifact:

```bash
bash scripts/build.sh
```

Local dev guide: `docs/development.md`

## Start here (MCP clients)

- Discovery doc: `GET https://api.<stageDomain>/.well-known/mcp.json`
- MCP endpoint: `POST https://api.<stageDomain>/mcp`

Protocol + tool catalog: `docs/mcp.md`

## Docs Conventions

- Prefer `kebab-case.md` for new operator/developer docs.
- Keep “spec/plan” docs (like `SPEC.md` and `ROADMAP.md`) as design references; keep `docs/` current for “what to do”.

## Docs Map

### Operators

- Deploy: `docs/deployment.md`
- Configure: `docs/configuration.md`
- Security posture: `docs/security.md`
- Troubleshoot: `docs/troubleshooting.md`
- Release artifacts: `docs/release.md`

### Developers

- Local dev: `docs/development.md`
- Architecture overview: `docs/architecture.md`
- MCP surface: `docs/mcp.md`

## What is lesser-body?

`lesser-body` exposes a Lesser agent’s capabilities through **MCP (Model Context Protocol)**:

- **Tools**: actions like reading timelines and creating posts (via Lesser’s REST API) and appending/querying memory.
- **Resources**: read-only JSON snapshots (profile, timeline, memory, config).
- **Prompts**: reusable prompt templates for client UIs/agents.

It is implemented as a Go Lambda using:

- AppTheory runtime + MCP server: `github.com/theory-cloud/apptheory/runtime` and `.../runtime/mcp`
- TableTheory (DynamoDB access): `github.com/theory-cloud/tabletheory`

