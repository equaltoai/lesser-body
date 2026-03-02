# Security Notes

<!-- AI Training: Security posture and auth model for lesser-body -->

This doc describes the implemented security posture of `lesser-body`.

## Public surface

- **Public:** `GET /.well-known/mcp.json`
- **Auth required:** `POST /mcp` (also `GET /mcp`, `DELETE /mcp`)

## Authentication model

`lesser-body` enforces auth at the AppTheory route layer (`RequireAuth()`), using an auth hook that accepts:

1) **Lesser OAuth access token** (HS256 JWT)
2) **Managed instance key** (for automation/operator workflows)

### JWT validation

- Only HS256 is accepted.
- The signing secret is loaded from:
  - `JWT_SECRET` (local/dev), or
  - `JWT_SECRET_ARN` (Secrets Manager)
  - default secret id fallback: `lesser/jwt-secret`
- Tokens must include a non-empty `username` claim (used as the request identity).
- Tokens are rejected if `iat` is older than 24 hours (a safety check independent of `exp`).

### Scope enforcement (tool calls)

JWT callers are authorized by scope on `tools/call`:

- `admin`: all tools
- `write`: write tools + read tools
- `read`: read tools only

Write tools include:

- `post_create`, `post_boost`, `post_favorite`, `follow`, `unfollow`, `profile_update`, `memory_append`

The managed instance key bypasses scope checks (treat as `admin`).

## Secrets handling

✅ CORRECT: use Secrets Manager + `JWT_SECRET_ARN` in deployed environments.

❌ INCORRECT: store plaintext `JWT_SECRET` in repo, CI logs, or long-lived env vars.

## Audit logging

`lesser-body` logs MCP `tools/call` invocations with:

- request id
- authenticated identity (agent username or `instance`)
- tool name

It does not log bearer tokens or tool arguments by default.

## IAM (least privilege)

At a minimum, the MCP Lambda needs:

- `secretsmanager:GetSecretValue` for `JWT_SECRET_ARN` (and `LESSER_HOST_INSTANCE_KEY_ARN` if used)
- DynamoDB read/write on the Lesser stage table (for memory events)
- DynamoDB read/write on the MCP session table (if enabled)
- `ssm:GetParameter*` to read cross-stack parameters (Lesser exports, optional lesser-soul exports)

## Client considerations

- Treat `/mcp` as a powerful tool surface. Only grant tokens with the minimum scopes required.
- Prefer short-lived OAuth tokens and avoid embedding long-lived secrets in client apps.

