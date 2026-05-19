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
                    ├── Calls lesser-host Soul Comm APIs for email/SMS/voice mailbox and send/reply
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
  - optional DynamoDB-backed stream replay store when `MCP_STREAM_TABLE` is set; AppTheory spills large logical stream
    events to the private `MCP_STREAM_SPILL_BUCKET` without changing the client-visible SSE / `Last-Event-ID` contract

### Auth

- `internal/auth/`
  - validates HS256 JWTs (Lesser OAuth access tokens)
  - carries a temporary inbound compatibility branch for managed instance key auth during migration
  - enforces tool-call scope policy (`read|write|admin`)
  - logs a deprecation warning whenever inbound auth falls back to the managed instance key

### Social tools (calls Lesser API)

- `internal/lesserapi/`
  - `LESSER_API_BASE_URL` or `MCP_ENDPOINT`-derived base URL
  - calls Mastodon-compatible endpoints (for example: `/api/v1/accounts/verify_credentials`)

### CMS / Article client path (internal, calls Lesser API)

- `internal/cmsapi/`
  - wraps the Lesser API client for `POST /api/graphql`
  - forwards the caller's OAuth bearer token to Lesser and preserves Lesser HTTP errors through `lesserapi.APIError`
  - preserves GraphQL `data`, `errors`, and `extensions` without encoding Article-specific operations yet
  - exists so future Article/Draft MCP tools can use Lesser's CMS contract instead of Mastodon status APIs for long-form authoring

### Communication tools (delegate to lesser-host)

- `internal/soulapi/`
  - `LESSER_SOUL_API_BASE_URL` / managed `TRUST_CONFIG.baseURL` points at lesser-host
  - uses `LESSER_HOST_INSTANCE_KEY` / `LESSER_HOST_INSTANCE_KEY_ARN` as the server-to-server credential
  - verifies bound-body capability plus caller access/payment policy from the Host effective policy contract before
    invoking private communication operations, loading the policy from Soul Comm contactability when it is not embedded
    in the registration payload
  - reads mailbox metadata/content/state through `/api/v1/soul/comm/mailbox/*`
  - sends and replies through lesser-host so body never becomes mailbox authority or delivery provider

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
- `docs/oauth-migration.md`
- `docs/operator-auth-replacement.md`
- `ROADMAP.md` (implementation sequencing and constraints)
