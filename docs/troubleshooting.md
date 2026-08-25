# Troubleshooting

## Several connected actors see MCP internal errors at once

**Symptoms**

- Multiple actor clients report JSON-RPC `-32603`, HTTP `500`, or HTTP `502` in the same short window.
- Body tool-call audit events are absent for the failed calls.
- API Gateway reports Lambda integration `429`, or Body concurrency reaches the account quota.

**Checks**

1. Compare API Gateway failures with Body Lambda `Throttles` and `ConcurrentExecutions`.
2. Confirm initial `GET /mcp/{actor}` listeners complete quickly instead of holding executions near the Lambda timeout.
3. Search Body cold-start logs for OAuth metadata reachability panics. Releases containing the 2026-08-10 availability
   fix must not probe the public authorization-server endpoint during process startup.
4. Correlate by request id before attributing the failure to `conversations_read`, `notifications_read`, or Lesser data;
   a request that never reaches Body's tool-call audit boundary did not execute the social handler.

**Resolution**

- Upgrade Body to a release that resolves the OAuth issuer on the protected-resource request and uses AppTheory's
  short-lived initial session listener behavior.
- Independently ensure the AWS account Lambda concurrency quota is appropriate for the complete Lesser stage. A larger
  quota is operational headroom, not a substitute for removing idle Body concurrency retention.

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
- For route-level `401`, a bare challenge means no bearer was presented. A generic rejected bearer returns
  `error="invalid_token"` with `refreshRequired=true`. On actor-plane `/mcp/{actor}` only,
  `reason="audience_mismatch"` keeps `invalid_token` but returns `authAction="reauthorize"` and
  `refreshRequired=false` because refreshing cannot change the token resource.
- On `/instance/ptah/mcp` or `/instance/ba/mcp`, a valid-signature bearer with the wrong instance-resource audience does
  not produce the actor-plane `audience_mismatch` challenge. It returns HTTP `403` with this response shape:

  ```json
  {
    "error": {
      "code": "instance_principal_not_allowed",
      "message": "instance-plane MCP requires an account-holder OAuth token for this instance resource"
    }
  }
  ```

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

- Ensure `MCP_ENDPOINT` is `https://api.<stageDomain>/mcp/{actor}`.
- Or set `LESSER_API_BASE_URL` explicitly to `https://api.<stageDomain>`.

## Discovery fails with `500 app.config_invalid`

Symptoms:

- `GET /.well-known/mcp.json` or `GET /.well-known/oauth-protected-resource/mcp/<actor>` returns HTTP `500`.
- The response mentions `MCP_ENDPOINT` or `TRUST_CONFIG.TrustBaseURL`.

Common causes:

- `MCP_ENDPOINT` is unset.
- `MCP_ENDPOINT` is malformed or does not include `/mcp` as its terminal resource path.
- Clients are reaching a different public host than the one configured in `MCP_ENDPOINT`.
- Managed `TRUST_CONFIG.baseURL` / `TrustBaseURL` is empty or points at the wrong Lesser environment.
- Lesser OAuth metadata is not reachable on the Lesser API host derived from `MCP_ENDPOINT`.

Fix:

- Ensure `MCP_ENDPOINT` exactly matches the public URL template clients use, for example `https://api.<stageDomain>/mcp/{actor}`.
- Verify `curl -sS "https://api.<stageDomain>/.well-known/oauth-authorization-server"` returns `200`.
- In managed deployments, confirm `PK=INSTANCE#CONFIG, SK=TRUST_CONFIG` contains the correct Lesser trust base URL for
  soul API fallback routing and instance-key secret resolution.
- If a browser client is involved, ensure `MCP_ALLOWED_ORIGINS` includes the browser origin (for example `https://claude.ai`).

## Identity / communication tools fail (`not_found`, `not_configured`, or soul API 404)

Symptoms:

- `tools/list` works, but soul-backed tools like `identity_whoami`, `identity_lookup`, `identity_verify`, `email_send`,
  `email_read`, `email_get_content`, or `sms_send` fail.
- Errors include public `app.not_found` for `/api/v1/soul/*` or configuration messages mentioning
  `LESSER_SOUL_API_BASE_URL` / managed `TRUST_CONFIG`.

Common causes:

- The Lesser instance does not yet expose `GET /api/v1/souls/bound/me`, or the endpoint returns a fail-closed
  `soul_not_bound` / `soul_not_available` response for the authenticated runtime agent.
- `LESSER_SOUL_API_BASE_URL` points at the Lesser instance API (`https://api.<stageDomain>`) instead of lesser-host.
- In a managed deployment, Lesser `TRUST_CONFIG` is missing `baseURL` and/or `instanceKeySecretARN`.

Fix:

- Verify the authenticated runtime-agent OAuth bearer succeeds against the Lesser bound-self endpoint:
  `GET https://api.<stageDomain>/api/v1/souls/bound/me`. A healthy souled runtime agent returns
  `binding_state: "bound"` with `binding.agent_username` matching the token username.
- In managed AWS deployments, make sure `PK=INSTANCE#CONFIG, SK=TRUST_CONFIG` in `LESSER_TABLE_NAME` has:
  - `managed.baseURL`
  - `managed.instanceKeySecretARN` for host-backed communication tools
- For local/manual runs, set:
  - `LESSER_SOUL_API_BASE_URL=https://<stage>.lesser.host`
  - optionally `LESSER_HOST_INSTANCE_KEY` or `LESSER_HOST_INSTANCE_KEY_ARN` for host-backed comm tools

If `identity_whoami` reports `channels: {}` and `provisioning.channels.state: "absent"` after Host channel backfill,
verify the Body deployment can resolve its managed instance key and call
`GET /api/v1/soul/comm/contactability/{agentId}` on the configured Host base URL. For the authenticated agent's own
identity, Body merges active, verified email/phone channels from this instance-authenticated response even when public
registration omits `channels`; it does not expose the response's policy, mailbox, or instance internals. If Host returns
no active verified channels, Body deliberately preserves public registration's `absent` versus `empty` state.

## Identity lookup or verify returns `private_reachability_unavailable`

Symptoms:

- `identity_lookup` with a bare email-like query such as `user@example.com` returns
  `private_reachability_unavailable`.
- `identity_verify` with `channel=email` or `channel=phone` returns `private_reachability_unavailable`.
- `identity_verify` with `channel=ens` succeeds or fails based on public ENS resolution plus authoritative sender
  provenance, without fetching private `/agents/{id}/channels` data.

Common cause:

- lesser-host private reachability hardening makes email/phone reverse lookup and arbitrary agent channel reads
  non-public. lesser-body fails closed instead of calling those routes anonymously.

Fix:

- For public actor lookup, use an ENS name, full agent ID, current-instance local ID, explicit ActivityPub handle
  (`@user@domain`), or canonical actor URL.
- Host's instance-authenticated contactability endpoint is used only to hydrate the authenticated agent's own
  `identity_whoami` / `agent://channels` projection. It is not a reverse resolver for arbitrary email addresses or
  phone numbers. Private identifier lookup therefore remains unavailable until Host exposes a separate bounded,
  instance-scoped resolver contract.

## Host mailbox tools return empty results or 4xx errors

Symptoms:

- `email_read`, `email_search`, `email_get`, `email_get_content`, `email_mark_read`, `email_mark_unread`, `sms_read`, or
  `voicemail_read` returns an MCP tool error sourced from lesser-host.
- Pagination returns no `nextCursor`, or a `messageId` that worked in notification-backed tools no longer resolves.

Common causes:

- The deployment is pointed at a lesser-host build before Soul Comm Mailbox v1.
- The caller is passing a legacy host `messageId` that is ambiguous; mailbox APIs prefer the opaque `messageRef` returned
  as `messageId` by lesser-body.
- The caller is trying to pass `messageId` or `inReplyTo` to `email_send`. `email_send` starts a new outbound email and
  rejects those legacy reply fields locally; use `email_reply` with a mailbox `messageId` for replies, or
  `idempotencyKey` for retry-safe new sends.
- The mailbox item is archived/deleted and the read tool was called without `includeArchived` / `includeDeleted` or an
  exact `archived` / `deleted` filter.

Fix:

- Confirm lesser-host exposes `/api/v1/soul/comm/mailbox/{agentId}/messages`.
- Use the `messageId` returned from `email_read` / `email_search` / `email_get` for follow-up get/content/state/reply
  calls.
- For new outbound email, call `email_send` without `messageId` / `inReplyTo`; for reply flows, call `email_reply`.
- Use `cursor`/`nextCursor` for mailbox pagination. `since` remains a legacy alias only.

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
- An SSE `GET` or `Last-Event-ID` resume returns `404` with `{"error":"session not found"}`.

Common causes:

- Client is not preserving and sending `mcp-session-id`.
- Session table is not enabled (`MCP_SESSION_TABLE` unset) and cold starts reset in-memory state.

Fix:

- Always call `initialize` first and store the returned `mcp-session-id`.
- On the spec-shaped dead-session `404`, discard the old session id and re-initialize; do not refresh credentials solely
  because the MCP session died.
- Enable session table in infra (recommended for production).

## MCP tasks are missing, inaccessible, or stuck

Symptoms:

- `initialize` does not include a `tasks` capability.
- `tasks/list`, `tasks/get`, `tasks/result`, or `tasks/cancel` returns method-not-found.
- `tools/call` with a `task` object returns an invalid params error.
- A task created from `skill_bundle_get` cannot be read from a later request.

Common causes:

- `MCP_TASK_TABLE` is unset, so body intentionally omits the task runtime and task-capable tool metadata.
- The client negotiated an MCP protocol version older than `2025-11-25`; AppTheory gates task methods to that protocol.
- The caller token lacks `read` scope. Task methods are read-scoped and return an OAuth insufficient-scope challenge when
  the token has only unrelated scopes such as `follow`.
- The client is using a different `mcp-session-id` than the one that created the task. Task state is session-scoped.
- The requested task TTL is outside body's bounds. CDK sets a 10-minute default (`MCP_TASK_TTL_MINUTES=10`), and body
  caps requested task TTLs at one hour.

Fix:

- Confirm the Lambda environment includes `MCP_TASK_TABLE` and `MCP_TASK_TTL_MINUTES=10`.
- Negotiate MCP protocol `2025-11-25`, then call `initialize` and inspect `capabilities.tasks`.
- Preserve the returned `mcp-session-id` and send it on every `tools/call` / `tasks/*` request.
- Request a token with `read` (or `write` / `admin`) scope.
- Use task-backed execution only for `skill_bundle_get` during the pilot; other tools continue to reject task metadata.
- If `tasks/result` waits longer than expected, check whether the Lesser skills-bundle endpoint is slow or unavailable and
  use `tasks/cancel` to cancel the in-flight read.

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

## Managed deploy fails with stream-table replacement / `AWS::EarlyValidation::ResourceExistenceCheck`

Symptoms:

- Managed `deploy-lesser-body-from-release.sh` fails while creating a change set.
- CloudFormation reports `AWS::EarlyValidation::ResourceExistenceCheck` for the MCP stream table.

Cause:

- An older managed template reused the physical stream-table name while changing the logical ID and schema boundary.
- The corrected baseline uses the versioned physical table suffix `mcp-streams-v2` while keeping the exported SSM name
  `mcp_stream_table_name` stable.

Fix:

- Upgrade to a release that includes the versioned stream-table baseline and the pinned MCP table logical IDs.
- Expect transient MCP session/stream continuity to reset during the update; durable Lesser actor data is unaffected.
- In lab, if you are validating a full repro/reset path, dropping and recreating the instance is acceptable, but it is
  not required for the corrected design.
