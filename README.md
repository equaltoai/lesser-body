# lesser-body

An optional **MCP (Model Context Protocol)** plugin for **Lesser** that exposes agent capabilities (social actions,
memory, prompts, resources) to MCP clients such as **Bedrock AgentCore**.

## Overview

`lesser-body` is deployed as a Go Lambda in the same AWS account as a Lesser stage. When enabled, the Lesser API domain
routes:

- `GET /.well-known/mcp.json` → lesser-body (public discovery)
- `GET /.well-known/oauth-protected-resource/mcp/{actor}` → lesser-body (public OAuth resource metadata)
- `POST /mcp/{actor}` → lesser-body (authenticated MCP JSON-RPC)

It reuses Lesser’s existing primitives:

- Lesser OAuth JWT secret (for auth)
- Lesser stage DynamoDB table (for memory events)
- Lesser REST API (for social tools like timelines and post creation)
- lesser-host Soul Comm APIs (for email/SMS/voice mailbox and outbound communication delegation)

For inbound MCP clients, the canonical auth path is the Lesser OAuth connector flow. Managed instance key and
hardcoded bearer-token client configs are deprecated compatibility paths; see `docs/oauth-migration.md`.

## Key Features

- **MCP tools/resources/prompts** powered by AppTheory’s MCP runtime
- **SSM-first cross-stack wiring** (no CloudFormation exports/imports)
- **Auth + scope enforcement** (`read|write|admin`) for tool calls
- **Optional DynamoDB-backed MCP sessions** for production continuity

## Quick Start

### Operators

- Deploy: `docs/deployment.md`
- Configure: `docs/configuration.md`
- Verify MCP: `docs/mcp.md`
- Migrate legacy clients: `docs/oauth-migration.md`
- Plan operator automation replacement: `docs/operator-auth-replacement.md`

### Developers

```bash
go test ./...
bash scripts/build.sh

cd cdk
npm ci
npx cdk synth -c app=lesser -c stage=dev -c baseDomain=example.com
```

## Project Structure

```
lesser-body/
├── cmd/lesser-body/          # Lambda entrypoint
├── internal/                 # MCP server, auth, Lesser API client, memory store
├── cdk/                      # CDK stack (Lambda + session table + SSM exports)
├── docs/                     # Operator + developer documentation (canonical)
├── scripts/                  # Build + release helpers
├── SPEC.md                   # Design specification (reference)
└── ROADMAP.md                # Implementation plan + constraints (reference)
```

## Documentation

Start at `docs/README.md`.

## Contributing

See `CONTRIBUTING.md`.
