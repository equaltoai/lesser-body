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

JWT callers are authorized by scope on `tools/call`, `resources/read`, `prompts/get`, `completion/complete`, and
MCP task methods:

- `admin`: all tools and task methods
- `write`: write tools + read tools + task methods
- `read`: read tools and task methods only

Data-bearing resources, prompts, completions, and task methods require at least `read` scope. Tool-specific write
operations require `write` scope (or `admin`). `tasks/cancel` remains read-scoped because the Phase 6 task pilot only
cancels session-scoped execution of the read-only `skill_bundle_get` tool.

Write tools include:

- `post_create`, `post_boost`, `post_favorite`, `follow`, `unfollow`, `profile_update`, `memory_append`
- `article_draft_create`, `article_draft_update`, `article_draft_publish`, `article_update`
- `email_send`, `email_reply`, `email_delete`, `email_mark_read`, `email_mark_unread`, `sms_send`

## Bound-body operation authorization

For private communication and self-channel operations, the souled runtime profile is necessary but not sufficient.
`lesser-body` checks the actor's binding through Lesser, then requires Host's effective `hosted-bound-soul/v1` policy
or an equivalent explicit operation policy for the operation and caller class before calling lesser-host. Channel
presence or channel `capabilities` alone do not grant private operation authority. The modeled caller classes are
`principal_operator`, `bound_body`, `instance_key`, `allowlisted_peer`, and `public_paid`; `public_paid` requires a
validated scoped x402 invocation grant plus explicit caller-access/payment policy allowance and never receives
principal/operator authority.

Scoped x402 grant consume/verification is independent from OAuth principal sessions. Requests carrying a Host grant are
rejected if they also carry OAuth `Authorization`, if the Host grant id or capability is missing, if payment evidence is
missing, or if the Host consume response does not bind the actor-resolved agent, capability/tool, MCP resource URL, request hash,
payment evidence hash, caller subject hash, expiry, scoped-invocation authority, issued status, usage limit, and
supported policy version. Logs and error payloads include only sanitized grant/payment hashes and policy-safe denial
reasons.

Denied operation-policy checks fail closed with a sanitized `operation_not_allowed` result. The details include only
policy-safe fields such as `reason`, `operation`, `callerClass`, and optional `policyVersion`; they must not include
private reachability, provider details, payment evidence, tenant data, wallet material, message bodies, or unresolved
security details. Policy denial happens before lesser-host communication endpoints are invoked.

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
- whether task-backed execution was requested

It also logs MCP task method invocations (`tasks/list`, `tasks/get`, `tasks/result`, `tasks/cancel`) with request id,
identity, method, and task id when present. It does not log bearer tokens, tool arguments, task request bodies, task
results, or communication payloads by default.

## Private soul self-scope reads

Project 21 M2 private mint-conversation reads use a Lesser-mediated trust seam:

```text
MCP caller -> lesser-body /mcp/{actor}
lesser-body -> Lesser /api/v1/souls/bound/me/...
Lesser -> lesser-host with managed instance trust
```

`soul_read` may request bounded private mint-conversation data only when the caller explicitly sends `self=true` and
`include_private:["mintConversations"]`. `self=true` alone remains public-only. For this path, lesser-body forwards the
MCP caller bearer only to Lesser's self-scope routes. It does **not** call lesser-host directly, does **not** use
`LESSER_HOST_INSTANCE_KEY`, and does **not** pass MCP caller bearer tokens to lesser-host control-plane auth.
When callers request `include_raw=true`, raw public Host/Soul endpoint payloads are sanitized before they are returned;
private reachability fields such as email/phone channels and contact preferences remain redacted.

The rejected unsafe pattern is direct MCP bearer forwarding to lesser-host. MCP bearers are issued by Lesser for an
actor-scoped MCP resource; lesser-host control-plane auth cannot derive the local account, current instance domain, or
soul/body binding proof from that token. Lesser owns that self-scope proof, derives the bound Host `agentId`, and then
uses managed instance trust to call Host.

Large read-tool diagnostics are opt-in. User-facing default reads must not emit timing, byte-count, upstream-count, or
other operational diagnostics unless the caller explicitly requests the tool's advertised diagnostic flag such as
`include_diagnostics=true`. Diagnostic payloads remain sanitized operational metadata only; they must not include bearer
tokens, instance keys, message bodies, private reachability details, or raw upstream objects.

Private mint-conversation list responses are compact summaries only. Full `messages` and `producedDeclarations` content
is returned only by explicit single-conversation reads and must not be logged. For explicit single reads, full private
fields are preserved in `structuredContent.data`; the text `content` block omits those verbose fields to avoid
duplicating private content into MCP stream events. Body measures the resulting MCP delivery envelope against a budget
derived from `MCP_STREAM_MAX_EVENT_BYTES` with headroom and returns a small `response_too_large` tool error before
stream-store persistence if the private single-read response exceeds the delivery budget. Error details preserve
machine-readable upstream reason codes without logging tokens, instance keys, message bodies, produced declarations, or
raw private conversation content.

## IAM (least privilege)

At a minimum, the MCP Lambda needs:

- `secretsmanager:GetSecretValue` for `JWT_SECRET_ARN` (and `LESSER_HOST_INSTANCE_KEY_ARN` if used)
- DynamoDB access on scoped Lesser stage table partition keys used by lesser-body. Read-only access covers
  `LBMEMORY#*` memory events, `SOUL_BODY_BINDING_USERNAME#*` soul-binding records, and `INSTANCE#CONFIG` managed
  trust configuration. Write access is limited to `LBMEMORY#*` memory events. CDK enforces these prefixes with
  `dynamodb:LeadingKeys` conditions and splits table description, scoped reads, and memory-only writes into separate
  policy statements.
- DynamoDB read/write on the MCP session table (if enabled)
- DynamoDB read/write on the MCP stream table and S3 read/write on the private MCP stream-spill bucket when durable
  stream replay is enabled. The spill bucket holds transient MCP transport payloads only; AppTheory enforces stream TTL
  before reading spilled data.
- DynamoDB read/write on the MCP task table when task storage is provisioned. This is transient task runtime state used
  for session-scoped MCP task records; body advertises the MCP `tasks` capability only when `MCP_TASK_TABLE` is set and
  the read-only `skill_bundle_get` task pilot is registered.
- `ssm:GetParameter*` to read cross-stack parameters (Lesser exports, optional lesser-soul exports)

## Client considerations

- Treat `/mcp` as a powerful tool surface. Only grant tokens with the minimum scopes required.
- Prefer short-lived OAuth tokens and avoid embedding long-lived secrets in client apps.
- Treat hardcoded bearer tokens and runtime credentials as temporary migration aids, not the canonical integration path.
- Treat operator automation as a separate OAuth client design problem; the replacement direction is documented in
  `docs/operator-auth-replacement.md`.
- Operator probes and canaries must not follow authenticated HTTP redirects. Redirects are treated as failures so bearer
  tokens are never replayed to a different endpoint. Diagnostic output must redact bearer tokens and summarize upstream
  mailbox/RPC error payloads without logging raw message bodies, addresses, phone numbers, or provider details.
