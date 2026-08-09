# Architecture Overview

<!-- AI Training: Architecture and code map for lesser-body -->

`lesser-body` is an MCP server implemented as a Go Lambda. It is designed to run in the same AWS account as a Lesser
stage and reuse that stage’s existing resources.

## High-level flow

```
MCP client (AgentCore / other)
  └── HTTPS POST /mcp/{actor} (api.<stageDomain>)
        └── API Gateway (Lesser REST API)
              └── Lambda (lesser-body Ka)
                    ├── AppTheory MCP server (tools/resources/prompts)
                    ├── Calls Lesser REST API for social tools
                    ├── Calls lesser-host Soul Comm APIs for email/SMS/voice mailbox and send/reply
                    └── Reads/writes Lesser DynamoDB table for memory events

Operator client
  ├── HTTPS POST /instance/ptah/mcp (api.<stageDomain>)
  │     └── Lesser API domain proxy
  │           └── Lambda (lesser-body instance plane / Ptah)
  │                 ├── AppTheory MCP server (account-holder orchestration tools)
  │                 ├── Body-owned INSTANCE_CONTENT_TABLE / INSTANCE_REGISTRY_TABLE
  │                 └── Lesser-owned APIs for delegation and hosted soul/body binding
  └── HTTPS POST /instance/ba/mcp (api.<stageDomain>)
        └── Lesser API domain proxy
              └── Lambda (lesser-body instance plane / Ba)
                    ├── AppTheory MCP server (install-pack planning tool)
                    ├── Body-owned INSTANCE_CONTENT_TABLE / INSTANCE_GRANT_TABLE
                    └── GET /instance/downloads/installer-grants/{grantId} for one-time ZIP downloads
```

Notes:

- The `lesser-body` CDK stack currently provisions its own API Gateway REST API v1 (AppTheory “Remote MCP server”).
  In the Lesser ecosystem, the intended client-facing path is still **through the Lesser API custom domain**
  (`https://api.<stageDomain>/mcp/{actor}`) when `soulEnabled=true`.
- The Ptah/Ba instance plane is also reached through the Lesser API custom domain. It uses a separate Lambda entrypoint,
  separate AppTheory MCP server instances, and Body-owned instance tables so Ptah/Ba operator state does not become
  Lesser actor-table writes.

## Components

### Lambda entrypoint

- `cmd/lesser-body/main.go`
  - boots an AppTheory app
  - mounts:
    - `GET /.well-known/mcp.json` (discovery)
    - `GET /.well-known/oauth-protected-resource/mcp/{actor}` (RFC 9728 protected-resource metadata)
    - `/mcp` (MCP JSON-RPC handler; auth required)
- `cmd/lesser-body-instance/main.go`
  - boots the Ptah/Ba AppTheory app
  - mounts:
    - `GET /.well-known/oauth-protected-resource/instance/ptah/mcp`
    - `GET /.well-known/oauth-protected-resource/instance/ba/mcp`
    - `POST /instance/ptah/mcp`
    - `POST /instance/ba/mcp`
    - `GET /instance/downloads/installer-grants/{grantId}`

### MCP server (tool registry)

- `internal/mcpserver/`
  - registers tools, resources, and prompts
  - optional DynamoDB-backed session store when `MCP_SESSION_TABLE` is set
  - optional DynamoDB-backed stream replay store when `MCP_STREAM_TABLE` is set; AppTheory spills large logical stream
    events to the private `MCP_STREAM_SPILL_BUCKET` without changing the client-visible SSE / `Last-Event-ID` contract

### Instance plane (Ptah/Ba)

- `internal/instanceapp/`
  - creates separate AppTheory MCP server instances for `ptah` and `ba`
  - serves Ptah/Ba RFC 9728 protected-resource metadata from configured `INSTANCE_MCP_ENDPOINT`
  - rejects agent-delegated and legacy managed-instance-key principals before instance tool dispatch
  - serves the Ba header-free one-time installer-grant download route
- `internal/ptahserver/`
  - registers account-holder orchestration tools for Host-backed genesis, agent registry, draft content, and binding
  - validates Lesser's authoritative bound actor username against the registry `local_id` when binding succeeds and
    prevents finalize replays from overwriting a divergent corrected registry projection
  - uses Body-owned instance tables and Lesser-owned APIs rather than direct Lesser table writes
- `internal/baserver/`
  - registers `agent_local_install_plan`
  - reads Lesser's authoritative soul binding before rendering and fails closed when its actor username disagrees with
    the selected registry `local_id`
  - derives install-pack stage/domain and download origin from `INSTANCE_MCP_ENDPOINT`, not request Host headers
  - persists only one-time grant hashes and safe binding fields through `internal/downloadgrant`

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
  - preserves GraphQL `data`, `errors`, and `extensions` and layers typed Article draft/publish/read/update operations for MCP tools
  - powers `article_draft_create`, `article_draft_update`, `article_draft_get`, `article_draft_list`, `article_draft_preview`, `article_draft_publish`, `article_update`, `article_get`, and `article_list` without direct DynamoDB access
  - delegates draft preview rendering to Lesser's canonical Article renderer/sanitizer through `draftPreview(id:)`, keeps the canary workflow in `scripts/canary_article_mcp.py`, and avoids Mastodon status APIs for long-form authoring

### Communication tools (delegate to lesser-host)

- `internal/soulapi/`
  - `LESSER_SOUL_API_BASE_URL` / managed `TRUST_CONFIG.baseURL` points at lesser-host
  - uses `LESSER_HOST_INSTANCE_KEY` / `LESSER_HOST_INSTANCE_KEY_ARN` as the server-to-server credential
  - verifies bound-body capability plus caller access/payment policy from the Host effective policy contract before
    invoking private communication operations, loading the policy from Soul Comm contactability when it is not embedded
    in the registration payload
  - hydrates the authenticated agent's `identity_whoami` and `agent://channels` self projections with an allowlisted
    subset of active, verified email/phone channels from Soul Comm contactability; public registration remains the
    source for contact preferences, and Host policy/mailbox internals are never projected
  - reads mailbox metadata/content/state through `/api/v1/soul/comm/mailbox/*`
  - sends and replies through lesser-host so body never becomes mailbox authority or delivery provider

### Memory store (writes to Lesser DynamoDB)

- `internal/memory/`
  - default: DynamoDB-backed store in the Lesser stage table (`LESSER_TABLE_NAME`)
  - test/dev option: in-memory store (`LESSER_BODY_MEMORY_STORE=memory`)

## Infra & wiring (SSM-first)

The intended integration is:

1) `lesser-body` publishes Ka and instance-plane exports to SSM
2) Lesser imports `mcp_lambda_arn` when `soulEnabled=true` and wires:
   - `POST /mcp` (streaming integration)
   - `GET /.well-known/mcp.json`
   - `GET /.well-known/oauth-protected-resource/mcp/{actor}`
3) Lesser imports `instance_mcp_lambda_arn` when its instance-plane routing flag is enabled and wires:
   - `POST /instance/ptah/mcp`
   - `POST /instance/ba/mcp`
   - `GET /.well-known/oauth-protected-resource/instance/ptah/mcp`
   - `GET /.well-known/oauth-protected-resource/instance/ba/mcp`
   - `GET /instance/downloads/installer-grants/{grantId}`

See:

- `docs/deployment.md`
- `docs/configuration.md`
- `docs/mcp.md#instance-plane-operator-chapter-ptahba`
- `docs/oauth-migration.md`
- `docs/operator-auth-replacement.md`
- `ROADMAP.md` (implementation sequencing and constraints)
