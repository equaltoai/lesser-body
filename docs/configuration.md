# Configuration Reference

<!-- AI Training: Runtime + infrastructure configuration for lesser-body. The staging git branch is distinct from deploy-stage staging. -->

`lesser-body` configuration comes from:

- **Environment variables** (injected by CDK into the Lambda)
- **SSM parameters** (cross-stack contract between Lesser and lesser-body)

This doc focuses on the implemented configuration surface.

## Deploy stages

`lesser-body` follows Lesser’s deploy-stage convention:

- `dev`
- `staging`
- `live`

The deploy-stage `staging` above is **not** the long-lived `staging` git branch used for feature → staging → main source-control integration. Do not rename or re-point deploy-stage tooling when changing branch protection or PR flow.

Deploy stage is used in:

- SSM parameter names
- resource naming
- API domain computation (`https://api.<stageDomain>`)

## Runtime environment variables

### Auth

`lesser-body` accepts `Authorization: Bearer <token>` and validates it using one of:

- **HS256 JWT** (Lesser OAuth access token; canonical inbound MCP client path)
- **Managed instance key** (deprecated inbound compatibility path; still required for outbound lesser-host service auth)

Variables:

- `JWT_SECRET` (string, optional for local dev)
  - If set, used directly as the HS256 secret.
- `JWT_SECRET_ARN` (string, optional)
  - If set, fetched from AWS Secrets Manager (value may be plaintext or JSON like `{"secret":"..."}`).
  - If not set, defaults to secret id `lesser/jwt-secret` (matches Lesser’s default).
- `LESSER_HOST_INSTANCE_KEY` (string, optional)
  - If set, supports three distinct roles:
    - deprecated inbound MCP compatibility when legacy instance-key auth is enabled
    - lesser-host service auth for host-backed communication tools (mailbox reads/state plus outbound send/reply)
    - lesser-host service auth for scoped public x402 invocation grant consume/verification
- `LESSER_HOST_INSTANCE_KEY_ARN` (string, optional)
  - If set, fetches the managed instance key from Secrets Manager.
  - Managed CDK deploys can inject this directly via the optional `LesserHostInstanceKeyARN` template/context input.
  - If not set in a managed deployment, lesser-body falls back to the persisted Lesser `TRUST_CONFIG.instanceKeySecretARN`
    record in `LESSER_TABLE_NAME`.
  - Do not remove this just because inbound MCP clients migrate to OAuth; host-backed communication tools still use it.
  - The long-term inbound replacement for operator automation is documented in `docs/operator-auth-replacement.md`.
- `LESSER_SOUL_BINDING_INTEGRATION_BEARER` (string, optional; local/manual only)
  - Dedicated Body/Ptah → Lesser server-to-server bearer for `agent_bind_soul`.
  - This is not caller OAuth, not `LESSER_HOST_INSTANCE_KEY`, and not a lesser-host communication delegation key.
  - Managed deployments should not inject this raw value directly.
- `LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN` (string, optional but required to use `agent_bind_soul` safely in managed Ptah)
  - Exact AWS Secrets Manager ARN containing the dedicated Body/Ptah → Lesser soul-binding bearer. The secret value may be
    plaintext or JSON like `{"secret":"..."}`.
  - CDK can inject this on the instance-plane Lambda through the `soulBindingIntegrationBearerArn` context value or the
    managed-template `LesserSoulBindingIntegrationBearerSecretARN` parameter; the release helper forwards
    `--soul-binding-integration-bearer-secret-arn` or `$LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN`.
  - The instance Lambda role receives `secretsmanager:GetSecretValue`/`DescribeSecret` only for that exact ARN.
  - The resolved value must match Lesser's receiving-side `SOUL_BINDING_INTEGRATION_KEY` /
    `SOUL_BINDING_INTEGRATION_KEY_ARN` configuration. If neither the direct env nor ARN-backed path resolves,
    `agent_bind_soul` fails closed with `not_configured`.

### Instance-plane storage

Ptah/Ba instance-plane state uses body-owned DynamoDB tables provisioned by this repo's CDK stack. These tables
are separate from Lesser's actor data table (`LESSER_TABLE_NAME`).

- `INSTANCE_CONTENT_TABLE` (string, required for the instance-plane Lambda)
  - Body-owned table for instance-plane content state.
- `INSTANCE_REGISTRY_TABLE` (string, required for the instance-plane Lambda)
  - Body-owned table for Ptah-created account-scoped agent registry records keyed by `(account, agentID)`.
    Internal stores must use this table rather than `LESSER_TABLE_NAME`.
- `INSTANCE_GRANT_TABLE` (string, required for the instance-plane Lambda)
  - Body-owned table for instance-plane grant state.
- `INSTANCE_SESSION_TABLE` (string, required for the instance-plane Lambda)
  - Body-owned table for instance-plane session state; CDK configures `expiresAt` as its TTL attribute.

### MCP session and stream persistence

- `MCP_SESSION_TABLE` (string, optional)
  - If set, enables DynamoDB-backed MCP sessions.
- `MCP_SESSION_TTL_MINUTES` (string, optional)
  - Session TTL in minutes. AppTheory defaults to `60`; Body CDK sets `1440` on every MCP-serving handler as a
    recoverability mitigation for sessionful clients.
- `MCP_STREAM_TABLE` (string, optional)
  - If set, enables AppTheory's durable DynamoDB-backed MCP stream replay store.
- `MCP_STREAM_TTL_MINUTES` (string, optional)
  - Stream event TTL in minutes (default is runtime-defined; deployments typically use `60`).
- `MCP_STREAM_SPILL_BUCKET` (string, optional)
  - Private S3 bucket used by AppTheory's Dynamo stream store for logical stream events too large to keep inline in
    DynamoDB. The CDK stack sets this when the stream table is enabled.
- `MCP_STREAM_SPILL_PREFIX` (string, optional)
  - S3 key prefix for spilled MCP stream payloads. CDK deployments use `mcp-stream-events`.
- `MCP_STREAM_SPILL_INLINE_MAX_BYTES` (string, optional)
  - Inline DynamoDB byte threshold before AppTheory spills the logical event payload to S3. CDK deployments use
    AppTheory's default `32768`.
- `MCP_STREAM_MAX_EVENT_BYTES` (string, optional)
  - Hard maximum size for one logical MCP stream event before AppTheory fails the event closed. CDK deployments use
    AppTheory's default `10485760`.
- `MCP_TASK_TABLE` (string, optional)
  - If set by CDK, names the DynamoDB table used by AppTheory's MCP task runtime. Setting it wires
    `mcp.WithTaskRuntime(...)`, advertises the MCP `tasks` capability for MCP 2025-11-25 sessions, and enables the
    read-only `skill_bundle_get` task pilot. Leave it unset to fail closed with no task capability.
- `MCP_TASK_TTL_MINUTES` (string, optional)
  - Default MCP task lifetime in minutes. CDK deployments set `10` so task state remains short-lived and does not
    outlive the session persistence window; body also caps caller-requested task TTLs at one hour.

`MCP_STREAM_TTL_MINUTES` is the runtime replay window: AppTheory rejects expired stream event records before reading
inline or S3-spilled payloads. DynamoDB TTL and S3 lifecycle cleanup are best-effort cleanup backstops, not access
enforcement.

`MCP_TASK_TABLE` enables AppTheory's task runtime over the canonical `sessionId`/`taskId`/`expiresAt` table. Body uses
that runtime only for the current read-only `skill_bundle_get` pilot; task state remains transient and session-scoped.

### Endpoints

- `MCP_ENDPOINT` (string, required for public discovery/OAuth metadata)
  - The public MCP endpoint template clients should use (for example: `https://api.dev.example.com/mcp/{actor}`).
  - Used by `GET /.well-known/mcp.json` and by the `agent://config` resource.
  - Also used by public discovery to derive the `instance_surfaces` locator for Ptah/Ba.
  - Discovery validates that inbound requests are arriving on the same public MCP URL instead of emitting
    mismatched `resource` metadata.
  - Public discovery fails closed when unset; lesser-body does not infer OAuth resource metadata from `Host` or
    `X-Forwarded-Host` headers.
- `INSTANCE_MCP_ENDPOINT` (string, required for the instance-plane Lambda)
  - The canonical instance-plane endpoint template, for example:
    `https://api.dev.example.com/instance/{surface}/mcp`.
  - `{surface}` is replaced with `ptah` or `ba` for RFC 9728 protected-resource metadata and Ba install-plan URLs.
  - Used by Ptah/Ba protected-resource metadata to publish exact `resource` URLs and by Ba to derive the stage domain,
    actor MCP endpoint, and one-time install-pack download origin.
  - The configured value is canonical. Instance discovery may compare request-derived host/protocol values against it,
    but raw `Host` / `X-Forwarded-Host` headers are never a substitute when configuration is missing or mismatched.
- `MCP_ALLOWED_ORIGINS` (string, optional but recommended for browser clients)
  - Comma-separated list of allowed browser origins for discovery and MCP responses.
  - Deployed CDK defaults include `https://claude.ai`, `https://claude.com`, and the stage domains.
  - Example: `https://claude.ai,https://app.dev.example.com,https://api.dev.example.com`
  - API Gateway CORS also permits the scoped public x402 invocation headers `lesser-x402-grant`,
    `x-lesser-x402-grant`, `lesser-x402-grant-id`, `x-lesser-x402-grant-id`, `lesser-x402-capability`,
    `x-lesser-x402-capability`, `payment-signature`, and `x-payment` for browser-based paid callers.
- `MCP_ALLOW_LEGACY_INSTANCE_KEY` (string, optional)
  - Compatibility flag for inbound `/mcp/{actor}` requests that authenticate with `LESSER_HOST_INSTANCE_KEY`.
  - Default: disabled.
  - This flag is temporary and should remain unset for OAuth-first deployments.
  - See `docs/oauth-migration.md` for the rollout sequence.
- `LESSER_API_BASE_URL` (string, optional)
  - Base URL used by social tools when calling the Lesser REST API (for example: `https://api.dev.example.com`).
  - If not set, it is derived from `MCP_ENDPOINT` by stripping `/mcp/{actor}` (or `/mcp`), or on the instance-plane
    Lambda from `INSTANCE_MCP_ENDPOINT` by stripping `/instance/{surface}/mcp`.
- `LESSER_SOUL_API_BASE_URL` (string, optional)
  - Base URL used by identity and communication tools when calling the soul API (for example: `https://lab.lesser.host`).
  - In managed deployments, if not set, lesser-body resolves it from the persisted Lesser `TRUST_CONFIG.baseURL` record
    in `LESSER_TABLE_NAME` and fails closed if that managed config is missing.
  - For local/manual runs, it falls back to `LESSER_HOST_URL`, then `LESSER_API_BASE_URL`, then `MCP_ENDPOINT` with
    `/mcp/{actor}` stripped.
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
  - The instance-plane Lambda receives read-only Lesser-table access for `INSTANCE#CONFIG` and
    `SOUL_BODY_BINDING_USERNAME#*` so Ptah/Ka can observe Lesser-owned binding rows after `agent_bind_soul` succeeds.
    It does not receive `LBMEMORY#*` write access.
  - CDK also grants Secrets Manager read access for both the legacy `<app>/instance-key*` path and the current managed
    `lesser-host/<control-plane-stage>/instances/<app>/instance-key*` namespace so host-backed communication tools keep
    working after managed secret-path migrations.
  - The managed `TRUST_CONFIG.baseURL` / `TrustBaseURL` is still required for soul API fallback routing and
    instance-key secret resolution, but not for the protected-resource `authorization_servers` value.
  - Startup reachability checks for `/.well-known/oauth-authorization-server` run against the Lesser API host derived
    from `MCP_ENDPOINT`, not against `TrustBaseURL`.

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
  - Imported by Lesser to wire `POST /mcp/{actor}`, `GET /.well-known/mcp.json`, and
    `GET /.well-known/oauth-protected-resource/mcp/{actor}`.
- `/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url`
  - Convenience value intended to equal `https://api.<stageDomain>/mcp/{actor}`.
- `/<app>/<stage>/lesser-body/exports/v1/mcp_session_table_name`
  - Session table name (if provisioned).
- `/<app>/<stage>/lesser-body/exports/v1/mcp_stream_table_name`
  - Stream table used for MCP streaming state.
- `/<app>/<stage>/lesser-body/exports/v1/instance_mcp_lambda_arn`
  - Imported by Lesser when its instance-plane routing flag is enabled to wire
    `POST /instance/ptah/mcp`, `POST /instance/ba/mcp`,
    `GET /.well-known/oauth-protected-resource/instance/ptah/mcp`,
    `GET /.well-known/oauth-protected-resource/instance/ba/mcp`, and the Ba installer-grant download route.
- `/<app>/<stage>/lesser-body/exports/v1/instance_mcp_endpoint_url`
  - Convenience value intended to equal `https://api.<stageDomain>/instance/{surface}/mcp`.
- `/<app>/<stage>/lesser-body/exports/v1/instance_content_table_name`
  - Body-owned Ptah/Ba content table name.
- `/<app>/<stage>/lesser-body/exports/v1/instance_registry_table_name`
  - Body-owned Ptah account-scoped agent registry table name.
- `/<app>/<stage>/lesser-body/exports/v1/instance_grant_table_name`
  - Body-owned Ba one-time install/download grant table name.
- `/<app>/<stage>/lesser-body/exports/v1/instance_session_table_name`
  - Body-owned instance-plane MCP session table name.

The MCP task table is intentionally internal while the `tasks` capability is disabled. The current CDK stack does not
publish `/<app>/<stage>/lesser-body/exports/v1/mcp_task_table_name`; adding that SSM export would be a separate
lesser/host coordination point.
