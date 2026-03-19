# Configuration Reference

<!-- AI Training: Runtime + infrastructure configuration for lesser-body -->

`lesser-body` configuration comes from:

- **Environment variables** (injected by CDK into the Lambda)
- **SSM parameters** (cross-stack contract between Lesser and lesser-body)

This doc focuses on the implemented configuration surface.

## Stages

`lesser-body` follows Lesser’s stage convention:

- `dev`
- `staging`
- `live`

Stage is used in:

- SSM parameter names
- resource naming
- API domain computation (`https://api.<stageDomain>`)

## Runtime environment variables

### Auth

`lesser-body` accepts `Authorization: Bearer <token>` and validates it using one of:

- **HS256 JWT** (Lesser OAuth access token)
- **Managed instance key** (operator automation)

Variables:

- `JWT_SECRET` (string, optional for local dev)
  - If set, used directly as the HS256 secret.
- `JWT_SECRET_ARN` (string, optional)
  - If set, fetched from AWS Secrets Manager (value may be plaintext or JSON like `{"secret":"..."}`).
  - If not set, defaults to secret id `lesser/jwt-secret` (matches Lesser’s default).
- `LESSER_HOST_INSTANCE_KEY` (string, optional)
  - If set, enables bearer-token auth for the managed instance key (timing-safe compare).
- `LESSER_HOST_INSTANCE_KEY_ARN` (string, optional)
  - If set, fetches the managed instance key from Secrets Manager.
  - If not set in a managed deployment, lesser-body falls back to the persisted Lesser `TRUST_CONFIG.instanceKeySecretARN`
    record in `LESSER_TABLE_NAME`.

### MCP session persistence

- `MCP_SESSION_TABLE` (string, optional)
  - If set, enables DynamoDB-backed MCP sessions.
- `MCP_SESSION_TTL_MINUTES` (string, optional)
  - Session TTL in minutes (default is runtime-defined; deployments typically use `60`).

### Endpoints

- `MCP_ENDPOINT` (string, optional but recommended)
  - The public MCP endpoint URL clients should use (for example: `https://api.dev.example.com/mcp`).
  - Used by `GET /.well-known/mcp.json` and by the `agent://config` resource.
  - When set, discovery validates that inbound requests are arriving on the same public MCP URL instead of emitting
    mismatched `resource` metadata.
- `MCP_ALLOWED_ORIGINS` (string, optional but recommended for browser clients)
  - Comma-separated list of allowed browser origins for discovery and MCP responses.
  - Deployed CDK defaults include `https://claude.ai`, `https://claude.com`, and the stage domains.
  - Example: `https://claude.ai,https://app.dev.example.com,https://api.dev.example.com`
- `LESSER_API_BASE_URL` (string, optional)
  - Base URL used by social tools when calling the Lesser REST API (for example: `https://api.dev.example.com`).
  - If not set, it is derived from `MCP_ENDPOINT` by stripping `/mcp`.
- `LESSER_SOUL_API_BASE_URL` (string, optional)
  - Base URL used by identity and communication tools when calling the soul API (for example: `https://lab.lesser.host`).
  - In managed deployments, if not set, lesser-body resolves it from the persisted Lesser `TRUST_CONFIG.baseURL` record
    in `LESSER_TABLE_NAME` and fails closed if that managed config is missing.
  - For local/manual runs, it falls back to `LESSER_HOST_URL`, then `LESSER_API_BASE_URL`, then `MCP_ENDPOINT` with
    `/mcp` stripped.
- `LESSER_HOST_URL` (string, optional)
  - Manual override for the lesser-host control-plane base URL.
  - Primarily useful for local/manual runs; managed deployments should prefer persisted `TRUST_CONFIG`.
- `LESSER_API_TIMEOUT_SECONDS` (string, optional)
  - HTTP timeout for Lesser API calls (default: `10`).

### Memory store

`lesser-body` stores memory events in the **existing Lesser DynamoDB table** by default.

- `LESSER_BODY_MEMORY_STORE` (string, optional)
  - `dynamo` (default): store memory in DynamoDB (requires `LESSER_TABLE_NAME`)
  - `memory`: in-memory store (useful for unit tests / local deterministic runs)
- `LESSER_TABLE_NAME` (string, required for `dynamo`)
  - The Lesser stage DynamoDB table name.
  - Also used to resolve managed trust configuration (`TRUST_CONFIG`) for soul API base URL and instance-key secret ARN.
  - The managed `TRUST_CONFIG.baseURL` / `TrustBaseURL` must resolve to a reachable Lesser OAuth metadata surface at
    `/.well-known/oauth-authorization-server` for protected-resource discovery to start.

### Misc

- `AWS_REGION` (string, required in AWS)
  - Used by AWS SDK clients (Secrets Manager, DynamoDB via TableTheory).
- `SERVICE_VERSION` (string, optional)
  - Included in discovery/config resources; defaults to `dev` if unset.

## SSM parameter contract

### Inputs (from Lesser)

Published by the Lesser shared/stage stacks:

- `/<app>/shared/secrets/jwt-secret-arn`
  - Used to set `JWT_SECRET_ARN` in the lesser-body Lambda.
- `/<app>/<stage>/lesser/exports/v1/table_name`
  - Used to set `LESSER_TABLE_NAME`.
- `/<app>/<stage>/lesser/exports/v1/domain`
  - Used to compute `MCP_ENDPOINT` when `baseDomain` is not provided to CDK.

### Outputs (from lesser-body)

Published by this repo’s CDK stack:

- `/<app>/<stage>/lesser-body/exports/v1/mcp_lambda_arn`
  - Imported by Lesser to wire `POST /mcp`, `GET /.well-known/mcp.json`, and
    `GET /.well-known/oauth-protected-resource`.
- `/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url`
  - Convenience value intended to equal `https://api.<stageDomain>/mcp`.
- `/<app>/<stage>/lesser-body/exports/v1/mcp_session_table_name`
  - Session table name (if provisioned).
