# lesser-body Documentation

<!-- AI Training: This is the documentation index for lesser-body. The staging git branch is distinct from deploy-stage staging. -->

This directory contains the canonical operator + developer documentation for `lesser-body`.

✅ CORRECT: treat `docs/` as the source of truth for how to deploy, operate, and integrate with this repo.

❌ INCORRECT: rely on stale planning notes without cross-checking the implemented code (especially around MCP routing and auth).

## Start here (operators)

`lesser-body` is an **optional MCP server** that integrates with a deployed Lesser instance.

1) Deploy the plugin (this repo): `docs/deployment.md`
2) Managed release contract: `docs/managed-deploy-contract.md`
3) Configure and verify: `docs/configuration.md`, `docs/mcp.md`
4) Managed deploy inventory/history: `docs/managed-deploy-inventory.md`
5) Managed multi-asset fixture: `docs/managed-deploy-fixtures/app-theory-v1.5.0-multi-asset/`
6) Migrate legacy bearer-token clients: `docs/oauth-migration.md`

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
- OAuth protected-resource doc: `GET https://api.<stageDomain>/.well-known/oauth-protected-resource/mcp/<actor>`
- MCP endpoint: `POST https://api.<stageDomain>/mcp/<actor>`

Protocol + tool catalog: `docs/mcp.md`
- Skills client flow: `docs/skills-mcp.md`

## Docs Conventions

- Prefer `kebab-case.md` for new operator/developer docs.
- Keep “spec/plan” docs (like `SPEC.md` and `ROADMAP.md`) as design references; keep `docs/` current for “what to do”.

## Docs Map

### Operators

- Deploy: `docs/deployment.md`
- Managed deploy contract: `docs/managed-deploy-contract.md`
- Managed deploy inventory/history: `docs/managed-deploy-inventory.md`
- Managed multi-asset fixture: `docs/managed-deploy-fixtures/app-theory-v1.5.0-multi-asset/`
- Configure: `docs/configuration.md`
- OAuth migration guide: `docs/oauth-migration.md`
- Operator auth replacement: `docs/operator-auth-replacement.md`
- Security posture: `docs/security.md`
- body_lab steward-routing trust model: `docs/body-lab-trust.md`
- ADR 0001 body_lab routing decision: `docs/adr/0001-accept-body-lab-routing.md`
- Troubleshoot: `docs/troubleshooting.md`
- Release artifacts: `docs/release.md`
- Release branching and branch protection: `docs/release-branching.md`
- Project 21 M0 baseline probe: `docs/m0-baseline-probe.md`

### Developers

- Local dev: `docs/development.md`
- Architecture overview: `docs/architecture.md`
- MCP surface: `docs/mcp.md`
- Skills MCP client flow: `docs/skills-mcp.md`

## What is lesser-body?

`lesser-body` exposes a Lesser agent’s capabilities through **MCP (Model Context Protocol)**:

- **Tools**: actions like reading timelines and creating posts (via Lesser’s REST API) and appending/querying memory.
- **Resources**: read-only JSON snapshots (profile, timeline, memory, config).
- **Prompts**: reusable prompt templates for client UIs/agents.

It is implemented as a Go Lambda using:

- AppTheory runtime + MCP server: `github.com/theory-cloud/apptheory/runtime` and `.../runtime/mcp`
- TableTheory (DynamoDB access): `github.com/theory-cloud/tabletheory/v2`
