# Security Notes

<!-- AI Training: Security posture and auth model for lesser-body -->

This doc describes the implemented security posture of `lesser-body`.

## Public surface

- **Public:** `GET /.well-known/mcp.json`
- **Public:** `GET /.well-known/oauth-protected-resource`
- **Auth required:** `POST /mcp` (also `GET /mcp`, `DELETE /mcp`)

## Authentication model

`lesser-body` enforces auth at the AppTheory route layer (`RequireAuth()`), using an auth hook that accepts:

1) **Lesser OAuth access token** (HS256 JWT; canonical inbound MCP client path)
2) **Managed instance key** (deprecated inbound compatibility path behind `MCP_ALLOW_LEGACY_INSTANCE_KEY=true`, still
   required for outbound lesser-host service auth)

### JWT validation

- Only HS256 is accepted.
- The signing secret is loaded from:
  - `JWT_SECRET` (local/dev), or
  - `JWT_SECRET_ARN` (Secrets Manager)
  - default secret id fallback: `lesser/jwt-secret`
- Tokens must include a non-empty `username` claim (used as the request identity).
- Tokens are rejected if `iat` is older than 24 hours (a safety check independent of `exp`).

### Scope enforcement (MCP calls)

JWT callers are authorized by scope on `tools/call`, `resources/read`, and `prompts/get`:

- `admin`: all tools
- `write`: write tools + read tools
- `read`: read tools only

Data-bearing resources and prompts require at least `read` scope. Tool-specific write operations require `write`
scope (or `admin`).

Write tools include:

- `post_create`, `post_boost`, `post_favorite`, `follow`, `unfollow`, `profile_update`, `memory_append`
- `email_send`, `email_reply`, `email_delete`, `email_mark_read`, `email_mark_unread`, `sms_send`

The managed instance key compatibility path bypasses scope checks (treat as `admin`), which is why it should not
remain the long-term inbound client auth model.

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
- DynamoDB access on scoped Lesser stage table partition keys used by lesser-body. Read-only access covers
  `LBMEMORY#*` memory events, `SOUL_BODY_BINDING_USERNAME#*` soul-binding records, and `INSTANCE#CONFIG` managed
  trust configuration. Write access is limited to `LBMEMORY#*` memory events. CDK enforces these prefixes with
  `dynamodb:LeadingKeys` conditions and splits table description, scoped reads, and memory-only writes into separate
  policy statements.
- DynamoDB read/write on the MCP session table (if enabled)
- `ssm:GetParameter*` to read cross-stack parameters (Lesser exports, optional lesser-soul exports)

## Client considerations

- Treat `/mcp` as a powerful tool surface. Only grant tokens with the minimum scopes required.
- Prefer short-lived OAuth tokens and avoid embedding long-lived secrets in client apps.
- Treat hardcoded bearer tokens and runtime credentials as temporary migration aids, not the canonical integration path.
- Treat operator automation as a separate OAuth client design problem; the replacement direction is documented in
  `docs/operator-auth-replacement.md`.
