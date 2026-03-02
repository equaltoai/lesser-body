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

