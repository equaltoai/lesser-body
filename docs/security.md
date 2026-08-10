# Security Notes

<!-- AI Training: Security posture and auth model for lesser-body -->

This doc describes the implemented security posture of `lesser-body`.

## Public surface

- **Public:** `GET /.well-known/mcp.json`
- **Public:** `GET /.well-known/oauth-protected-resource/mcp/{actor}`
- **Public:** `GET /.well-known/oauth-protected-resource/instance/ptah/mcp`
- **Public:** `GET /.well-known/oauth-protected-resource/instance/ba/mcp`
- **Public one-time download:** `GET /instance/downloads/installer-grants/{grantId}` with the opaque token and full
  binding query issued by Ba
- **Auth required:** `POST /mcp/{actor}` (also `GET /mcp/{actor}`, `DELETE /mcp/{actor}`)
- **Auth required:** `POST /instance/ptah/mcp`, `POST /instance/ba/mcp`

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

### Instance-plane authentication and threat model

Ptah (`/instance/ptah/mcp`) and Ba (`/instance/ba/mcp`) use separate AppTheory MCP server instances and separate RFC
9728 protected-resource metadata documents. Their OAuth `resource` identifiers are the exact instance endpoint URLs
derived from `INSTANCE_MCP_ENDPOINT`, for example `https://api.<stageDomain>/instance/ptah/mcp` and
`https://api.<stageDomain>/instance/ba/mcp`. Operators must not replace this with local OAuth metadata, raw Host-header
inference, or an MCP-client-specific shortcut.

Instance-plane MCP requests require a Lesser OAuth JWT bearer token for an account-holder principal:

- missing or invalid bearer credentials return an RFC 9728 `WWW-Authenticate` discovery challenge for the exact
  Ptah/Ba protected-resource metadata URL;
- agent-delegated principals are rejected before tool dispatch;
- legacy managed-instance-key principals are rejected before tool dispatch;
- `actor_username` arguments, when present, must match the authenticated account-holder username;
- read surfaces require read-capable scope and write/minting surfaces require write-capable scope; and
- configured endpoint templates are canonical. Request Host / forwarded Host values can only be validated against
  configuration, never used as authority when configuration is missing or mismatched.

Ptah/Ba tools are not Ka actor tools. They are discovered with authenticated `tools/list` on the corresponding instance
MCP endpoint, not by broadening `/.well-known/mcp.json` beyond its Ka tool catalog.

### Scope enforcement (MCP calls)

JWT callers are authorized by scope on `tools/call`, `resources/read`, `prompts/get`, `completion/complete`, and
MCP task methods:

- `read`: read tools and read-scoped MCP methods only
- `write`: write tools plus everything `read` can do
- `admin`: every `write` and `read` surface

The hierarchy is `read` ⊂ `write` ⊂ `admin`: a `write` token satisfies read requirements, and an `admin` token
satisfies both read and write requirements. Data-bearing resources, prompts, completions, and task methods require at
least `read` scope. Tool-specific write operations require `write` scope (or `admin`). `tasks/cancel` remains
read-scoped because the Phase 6 task pilot only cancels session-scoped execution of the read-only `skill_bundle_get`
tool.

The authoritative per-tool classification is `internal/mcpserver/tool_scopes.go` (`toolScopes` and
`RequiredScopesForTool`). Documentation tables are descriptive; the code classifier is the gate. The classifier is
fail-closed:

- every registered tool must have an explicit classification;
- a registered tool that has no classification resolves to `admin` (`StrictestToolScope`), not `read`;
- if the registered tool surface cannot be derived, the classifier also resolves to `admin`; and
- an unregistered tool name has no handler, carries no scope requirement, and is left to the MCP runtime's normal
  tool-not-found path.

The M0.1 regression locks are `internal/mcpserver/tool_scopes_test.go` and
`internal/mcpapp/scope_classification_test.go`. They assert classification exhaustiveness, no stale entries, known
scope values, annotation consistency where annotations exist, a pinned write-tool set, fail-closed default behavior,
and read-token rejection for write tools before downstream calls.

Write tools include:

- `post_create`, `post_boost`, `post_favorite`, `follow`, `unfollow`, `profile_update`, `memory_append`
- `notification_dismiss`
- `article_draft_create`, `article_draft_update`, `article_draft_publish`, `article_update`
- `email_send`, `email_reply`, `email_delete`, `email_mark_read`, `email_mark_unread`, `sms_send`

The Body scope gate runs before AppTheory MCP tool dispatch. For `memory_append` and host-backed communication write
tools, this is the single scope gate before the local memory write or lesser-host delegation; there is no later Lesser
server-side scope re-check for those side effects. For social write tools, Body still gates first and then calls
Lesser's REST API with the caller's bearer token, so Lesser can apply its normal server-side authorization as a second
check. Body must not bypass Lesser's authorizer for social actions.

Scoped public x402 invocation grants use the same per-tool classifier. `grant.scope` is normalized as `read`, `write`,
or `admin` and must authorize the requested tool's required scope under the same hierarchy. Missing, unknown, or
insufficient `grant.scope` fails closed with `x402_grant_scope_mismatch` before MCP tool dispatch. The M0.2 regression
locks live in `internal/mcpapp/x402_grants_test.go`.

Instance-plane x402 capability grants are a separate Host-authored contract for the OAuth-authenticated install-plan
tool. `agent_local_install_plan` consumes `capabilityVersion="instance-capability/v1"` /
`capability="instance:install_plan"`. Body hashes payment evidence before Host consume, rejects actor/scoped grants
(`scoped-invocation/v1`, `tools.invoke`, and mismatched tool/resource bindings), and performs no instance tool side
effect until Host accepts the grant. Explicit operator OAuth authority is exempt; ordinary OAuth connector sessions are
not.

Ba also treats actor-endpoint derivation as a fail-closed authority boundary. After the x402 and content-readiness gates
but before renderer or download-grant side effects, `agent_local_install_plan` reads Lesser's existing soul-binding
surface by registry `agent_id` with the dedicated integration bearer and compares the registry `local_id` with
Lesser's authoritative bound actor username. Missing authority, response/agent mismatch, or identifier divergence
returns a typed tool error and no pack. Ptah applies the same identifier comparison to successful binding responses and
refuses a finalize replay that would overwrite a divergent existing `local_id`.

`internal/mcpapp/audit.go` uses AppTheory's MCP JSON-RPC parser for scope authorization before dispatch. Parser
failures intentionally fall through to the AppTheory runtime and remain safe only because the runtime uses the same
parser and fails before tool dispatch. The M0.3 regression locks in `internal/mcpapp/parser_equivalence_test.go` assert
parser-equivalence for single requests, batches, notification-form `tools/call`, malformed payloads, and empty tool
names. `internal/mcpapp/no_side_effect_403_test.go` asserts read-scoped 403s make zero lesser-host calls for
communication writes and zero memory writes for `memory_append`.

The managed instance key compatibility path bypasses scope checks (treat as `admin`) only when
`MCP_ALLOW_LEGACY_INSTANCE_KEY=true`. Keep that path rollback-only and migrate inbound clients to OAuth; see the
[OAuth migration guide](oauth-migration.md).

### Project 48 M0 scope-enforcement notes

- M0.1: [#368](https://github.com/equaltoai/lesser-body/issues/368) /
  [PR #389](https://github.com/equaltoai/lesser-body/pull/389) moved tool classification to the exhaustive
  `toolScopes` map, changed the unclassified registered-tool default from read to `admin`, and recorded that
  `phone_call` remains unregistered and therefore tool-not-found rather than dispatchable.
- M0.2: [#369](https://github.com/equaltoai/lesser-body/issues/369) /
  [PR #390](https://github.com/equaltoai/lesser-body/pull/390) enforced Host consumed-grant `grant.scope` against the
  same `RequiredScopesForTool` classifier.
- M0.3: [#370](https://github.com/equaltoai/lesser-body/issues/370) /
  [PR #391](https://github.com/equaltoai/lesser-body/pull/391) added parser-equivalence and no-side-effect-on-403
  regression coverage for the authorization gate.
- M0.4: [#371](https://github.com/equaltoai/lesser-body/issues/371) is this docs truth-up, preserving the verified
  scope-enforcement model in the operator and MCP documentation.

## Bound-body operation authorization

For private communication and self-channel operations, the souled runtime profile is necessary but not sufficient.
`lesser-body` checks the actor's binding through Lesser, then requires Host's effective `hosted-bound-soul/v1` policy
or an equivalent explicit operation policy for the operation and caller class before calling lesser-host. Channel
presence or channel `capabilities` alone do not grant private operation authority. The modeled caller classes are
`principal_operator`, `bound_body`, `instance_key`, `allowlisted_peer`, and `public_paid`; `public_paid` requires a
validated scoped x402 invocation grant plus explicit caller-access/payment policy allowance and never receives
principal/operator authority.

Scoped public x402 grant consume/verification is independent from OAuth principal sessions on `/mcp/{actor}`. Public
actor-scoped requests carrying a Host grant are rejected if they also carry OAuth `Authorization`, if the Host grant id
or capability is missing, if payment evidence is missing, or if the Host consume response does not bind the
actor-resolved agent, capability/tool, MCP resource URL, request hash, payment evidence hash, caller subject hash,
expiry, scoped-invocation authority, issued status, usage limit, and supported policy version. Logs and error payloads
include only sanitized grant/payment hashes and policy-safe denial reasons. Instance-plane capability grants are the
exception to the mixed-auth rule: they require OAuth plus Host instance capability evidence and never grant actor-scoped
public invocation authority.

Denied operation-policy checks fail closed with a sanitized `operation_not_allowed` result. The details include only
policy-safe fields such as `reason`, `operation`, `callerClass`, and optional `policyVersion`; they must not include
private reachability, provider details, payment evidence, tenant data, wallet material, message bodies, or unresolved
security details. Policy denial happens before lesser-host communication endpoints are invoked.

The managed instance key compatibility path bypasses scope checks (treat as `admin`), which is why it should not
remain the long-term inbound client auth model; the [OAuth migration guide](oauth-migration.md) treats it as a
time-boxed rollback bridge only.

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

When an authenticated OAuth caller's non-stream request is rebound from a dead MCP session, Body emits one sanitized
`mcp session rebound` audit event containing only the request id, principal type, and `mcp_session_rebound` reason. It
does not log the actor identity, old or new session id, bearer token, or request body. Dead-session SSE `GET` requests
are not rebound and retain the sanitized authorization-rejection event and `mcp_session_not_found` reason.

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
private reachability fields such as phone channels, contact preferences, and arbitrary non-managed email channels
remain redacted. The one public email projection is Host's current managed `lessersoul.ai` channel, when Host publishes
it as `<agent-local-id>.<instance-slug>@lessersoul.ai`; legacy bare `<agent-local-id>@lessersoul.ai` aliases are
inbound-only and are not current public channels.

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
- `secretsmanager:GetSecretValue`/`DescribeSecret` for the exact
  `LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN` when Ptah `agent_bind_soul` is enabled; this credential is dedicated to
  Body/Ptah → Lesser soul binding and must not be replaced by caller OAuth or `LESSER_HOST_INSTANCE_KEY`
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
- DynamoDB read/write on Body-owned instance-plane tables for the instance Lambda:
  `INSTANCE_CONTENT_TABLE`, `INSTANCE_REGISTRY_TABLE`, `INSTANCE_GRANT_TABLE`, and `INSTANCE_SESSION_TABLE`. Ka also
  receives read/write access to the content and registry tables only for `soul_self_recover`; it receives no
  grant/session access. Recovery requires OAuth actor binding, `write`, the souled profile, exact Host declaration
  digest/provenance validation, and never writes Lesser or Host business state.
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
