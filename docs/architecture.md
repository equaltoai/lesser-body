# Architecture Overview

<!-- AI Training: Architecture and code map for lesser-body -->

`lesser-body` is an MCP server implemented as a Go Lambda. It is designed to run in the same AWS account as a Lesser
stage and reuse that stage’s existing resources.

## High-level flow

```
MCP client (AgentCore / other)
  └── HTTPS POST /mcp (api.<stageDomain>)
        └── API Gateway (Lesser REST API)
              └── Lambda (lesser-body)
                    ├── MCP server (tools/resources/prompts)
                    ├── Calls Lesser REST API for social tools
                    └── Reads/writes Lesser DynamoDB table for memory events
```

Notes:

- The `lesser-body` CDK stack currently provisions its own API Gateway REST API v1 (AppTheory “Remote MCP server”).
  In the Lesser ecosystem, the intended client-facing path is still **through the Lesser API custom domain**
  (`https://api.<stageDomain>/mcp`) when `soulEnabled=true`.

## Components

### Lambda entrypoint

- `cmd/lesser-body/main.go`
  - boots an AppTheory app
  - mounts:
    - `GET /.well-known/mcp.json` (discovery)
    - `/mcp` (MCP JSON-RPC handler; auth required)

### MCP server (tool registry)

- `internal/mcpserver/`
  - registers tools, resources, and prompts
  - optional DynamoDB-backed session store when `MCP_SESSION_TABLE` is set

### Auth

- `internal/auth/`
  - validates HS256 JWTs (Lesser OAuth access tokens)
  - optionally validates managed instance key (operator automation)
  - enforces tool-call scope policy (`read|write|admin`)

### Social tools (calls Lesser API)

- `internal/lesserapi/`
  - `LESSER_API_BASE_URL` or `MCP_ENDPOINT`-derived base URL
  - calls Mastodon-compatible endpoints (for example: `/api/v1/accounts/verify_credentials`)

### Memory store (writes to Lesser DynamoDB)

- `internal/memory/`
  - default: DynamoDB-backed store in the Lesser stage table (`LESSER_TABLE_NAME`)
  - test/dev option: in-memory store (`LESSER_BODY_MEMORY_STORE=memory`)

## Infra & wiring (SSM-first)

The intended integration is:

1) `lesser-body` publishes `mcp_lambda_arn` to SSM
2) Lesser imports that ARN when `soulEnabled=true` and wires:
   - `POST /mcp` (streaming integration)
   - `GET /.well-known/mcp.json`

See:

- `docs/deployment.md`
- `docs/configuration.md`
- `ROADMAP.md` (implementation sequencing and constraints)
