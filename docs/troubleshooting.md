# Troubleshooting

<!-- AI Training: Common failures and fixes for lesser-body -->

## 401 `app.unauthorized`

Symptoms:

- MCP calls return HTTP `401`.

Common causes:

- Missing `Authorization: Bearer ...` header
- JWT secret not configured (neither `JWT_SECRET` nor a resolvable `JWT_SECRET_ARN`)
- Invalid HS256 signature / wrong secret
- Using a non-HS256 JWT

Fix:

- For local/unit runs, set `JWT_SECRET` and mint a token with HS256.
- For deployed runs, ensure `JWT_SECRET_ARN` points to the same secret Lesser uses.
- If the deployment still relies on a managed instance key or a hardcoded bearer token, follow `docs/oauth-migration.md`
  to move the client onto the OAuth connector path.

## Warning log: managed-instance-key inbound MCP auth is deprecated

Symptoms:

- Logs include `managed-instance-key inbound MCP auth is deprecated`.

Cause:

- A client is still authenticating inbound `/mcp` traffic with `LESSER_HOST_INSTANCE_KEY` instead of an OAuth token.

Fix:

- Register or reuse a Lesser OAuth app and migrate the client to the connector flow described in
  `docs/oauth-migration.md`.
- Keep `LESSER_HOST_INSTANCE_KEY` only for outbound communication tooling unless the temporary compatibility flag is
  intentionally enabled.
- If a rollback is unavoidable, set `MCP_ALLOW_LEGACY_INSTANCE_KEY=true` explicitly and treat it as temporary.

## Mid-session OAuth failures on tools or resources

Symptoms:

- `initialize` succeeds, but later social or inbox-backed MCP operations fail.
- Tool calls return `isError=true` with `structuredContent.error.code=unauthorized|forbidden`.
- Resource reads return JSON content whose top-level payload is `{ "error": ... }`.

Cause:

- `lesser-body` passes the caller bearer token through to Lesser and does not own refresh.
- Lesser rejected the token later in the flow with `401` (expired/revoked) or `403` (insufficient access).

Fix:

- Refresh or re-authorize the OAuth token in the MCP client, then retry the request.
- Inspect the returned error details for `authAction`, `refreshRequired`, and any parsed upstream `apiError`.
- Route-level `401` responses, tool errors, and resource payloads all use the same detail fields so client logic can make
  the same retry decision in each surface.

## 403 `app.forbidden` on `tools/call`

Symptoms:

- `tools/list` works, but calling a tool returns HTTP `403`.

Cause:

- Your token is authenticated, but does not have the required scope for the tool.

Fix:

- For read-only tools, include `read` (or `write` / `admin`) in JWT scopes.
- For write tools, include `write` (or `admin`) in JWT scopes.

See `docs/mcp.md` for the scope map.

## Social tools fail (Lesser API errors / 404)

Symptoms:

- Tools like `timeline_read` / `post_create` fail with “lesser api error (status=404)” or other REST failures.

Common causes:

- `LESSER_API_BASE_URL` points to the wrong host, or is missing.
- `MCP_ENDPOINT` is configured to a non-Lesser host (social tools derive API base URL from it if `LESSER_API_BASE_URL` is unset).

Fix:

- Ensure `MCP_ENDPOINT` is `https://api.<stageDomain>/mcp`.
- Or set `LESSER_API_BASE_URL` explicitly to `https://api.<stageDomain>`.

## Discovery fails with `500 app.config_invalid`

Symptoms:

- `GET /.well-known/mcp.json` or `GET /.well-known/oauth-protected-resource` returns HTTP `500`.
- The response mentions `MCP_ENDPOINT` or `TRUST_CONFIG.TrustBaseURL`.

Common causes:

- `MCP_ENDPOINT` is malformed or does not end in `/mcp`.
- Clients are reaching a different public host than the one configured in `MCP_ENDPOINT`.
- Managed `TRUST_CONFIG.baseURL` / `TrustBaseURL` is empty or points at the wrong Lesser environment.
- Lesser OAuth metadata is not reachable on the Lesser API host derived from `MCP_ENDPOINT`.

Fix:

- Ensure `MCP_ENDPOINT` exactly matches the public URL clients use, for example `https://api.<stageDomain>/mcp`.
- Verify `curl -sS "https://api.<stageDomain>/.well-known/oauth-authorization-server"` returns `200`.
- In managed deployments, confirm `PK=INSTANCE#CONFIG, SK=TRUST_CONFIG` contains the correct Lesser trust base URL for
  soul API fallback routing and instance-key secret resolution.
- If a browser client is involved, ensure `MCP_ALLOWED_ORIGINS` includes the browser origin (for example `https://claude.ai`).

## Identity / communication tools fail (`not_found`, `not_configured`, or soul API 404)

Symptoms:

- `tools/list` works, but soul-backed tools like `identity_whoami`, `identity_lookup`, `identity_verify`, `email_send`,
  or `sms_send` fail.
- Errors include public `app.not_found` for `/api/v1/soul/*` or configuration messages mentioning
  `LESSER_SOUL_API_BASE_URL` / managed `TRUST_CONFIG`.

Common causes:

- `LESSER_SOUL_API_BASE_URL` points at the Lesser instance API (`https://api.<stageDomain>`) instead of lesser-host.
- In a managed deployment, Lesser `TRUST_CONFIG` is missing `baseURL` and/or `instanceKeySecretARN`.

Fix:

- In managed AWS deployments, make sure `PK=INSTANCE#CONFIG, SK=TRUST_CONFIG` in `LESSER_TABLE_NAME` has:
  - `managed.baseURL`
  - `managed.instanceKeySecretARN` for outbound communication tools
- For local/manual runs, set:
  - `LESSER_SOUL_API_BASE_URL=https://<stage>.lesser.host`
  - optionally `LESSER_HOST_INSTANCE_KEY` or `LESSER_HOST_INSTANCE_KEY_ARN` for outbound comm tools

## Memory tools fail (`LESSER_TABLE_NAME is required`)

Symptoms:

- `memory_append` / `memory_query` fails with a configuration error.

Cause:

- Default memory store is DynamoDB and requires `LESSER_TABLE_NAME`.

Fix:

- In AWS, ensure the Lambda has `LESSER_TABLE_NAME` set (normally injected from SSM by CDK).
- For local deterministic runs, set `LESSER_BODY_MEMORY_STORE=memory`.

## MCP session issues (“invalid session”, missing continuity)

Symptoms:

- Server issues a new `mcp-session-id` frequently.

Common causes:

- Client is not preserving and sending `mcp-session-id`.
- Session table is not enabled (`MCP_SESSION_TABLE` unset) and cold starts reset in-memory state.

Fix:

- Always call `initialize` first and store the returned `mcp-session-id`.
- Enable session table in infra (recommended for production).

## CDK deploy fails: missing SSM parameters

Symptoms:

- CDK deploy rolls back with errors referencing missing SSM params like:
  - `/<app>/shared/secrets/jwt-secret-arn`
  - `/<app>/<stage>/lesser/exports/v1/table_name`

Cause:

- Lesser shared/stage stacks haven’t been deployed yet.

Fix:

- Deploy Lesser first (shared + stage). Then deploy lesser-body.
- Only enable `soulEnabled=true` in Lesser after `mcp_lambda_arn` exists.
