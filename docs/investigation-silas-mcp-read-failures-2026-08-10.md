# Investigation: Silas MCP read failures (2026-08-10)

## Reported symptom

> Inbox-wide `conversations_read` fails with `-32603 Internal error`. Incoming-notifications read fails with the same
> error. Only the Della-specific lookup completed normally, returning `404 conversation not found`.

## Dimensions

- Surface: authenticated Ka JSON-RPC at `POST /mcp/{actor}`.
- Tools: `conversations_read`, `notifications_read`, and `direct_messages_read`; all are read-scoped social tools.
- Caller: reported as Silas Vane on TheoryLive, souled profile. The client-side correlation ids were not available.
- Deployment: TheoryLive Body `v1.6.12`, deployed at 2026-08-10 14:51 UTC.

## Verified evidence

- The Silas Lesser soul/body binding exists and maps `silas-vane` to its bound agent id.
- At 15:51:27, 15:51:34, 15:51:47, and 15:51:50 UTC, API Gateway returned HTTP 502 for Ka POST requests before
  tool dispatch.
- Matching Body cold starts panicked while synchronously probing
  `/.well-known/oauth-authorization-server`; the probe received HTTP 503 from the saturated Lesser API.
- The AWS account concurrency quota is 10. Body reached 10 concurrent executions in the same window and Lambda
  throttled additional requests. Seven long-lived initial Ka GET listeners were consuming most of that pool for about
  25 seconds at a time.
- No failed broad read reached Body's tool-call audit boundary or Lesser's `/api/v1/conversations` or
  `/api/v1/notifications` handlers. The failure therefore preceded tool behavior and did not depend on Silas's social
  content.
- The only correlated focused lookup in that window reached Lesser as `della-marlowe` and returned the expected 404.
  Without the client's correlation id, that request cannot be proven to be the exact lookup described in the report.

## Fix-locus verdict

Fix in Body. Two Body availability choices combined into a feedback loop:

1. Actor Lambda process startup depended on a public OAuth metadata request through the same constrained Lambda pool.
2. Body opted AppTheory's otherwise optional idle session listener into a 25-second hold, allowing connected clients to
   consume nearly the entire stage concurrency pool.

The social read handlers and Lesser response normalizers are not the fix locus for this incident.

## MCP contract audit

- `.well-known/mcp.json`: unchanged.
- OAuth protected-resource metadata: response shape, per-actor resource identifier, issuer, scopes, and cache contract
  remain unchanged on success. Issuer resolution moves from process startup to the metadata request. A temporary
  authorization-server failure now returns sanitized HTTP 503 with `Cache-Control: no-store` rather than crashing the
  entire Ka process.
- JSON-RPC request/response shapes: unchanged.
- Initial session GET: remains a successful `text/event-stream` response with a keepalive, but closes immediately as
  MCP permits. Resumable GET requests carrying `Last-Event-ID` still reach AppTheory's durable stream replay path.
- Compatibility: semantic availability refinement; no endpoint, schema, scope, profile, or protocol version changes.
  Claude/Codex, AgentCore, and other conforming Streamable HTTP clients may reconnect after the server closes the
  optional idle stream. POST request/response streaming and resumability are retained.

## Lesser-integration audit

- JWT validation and shared-secret handling: unchanged.
- Lesser DynamoDB access: only the existing read-only trust configuration load remains; no new access pattern or write.
- Lesser REST API: social endpoint requests and response shapes are unchanged.
- SSM exports, `soulEnabled`, and first-deploy order: unchanged.
- Coordination implication: Lesser need not change. Its OAuth metadata can be temporarily unavailable without taking
  authenticated Body tool calls down; the protected-resource route still fails closed until the authoritative issuer is
  resolved.

## Enumerated changes

1. Resolve and cache the authoritative OAuth issuer on the protected-resource request instead of during Lambda startup;
   cover transient dependency failure and cache reuse with regression tests.
2. Remove Body's opt-in 25-second idle listener budget so AppTheory emits one keepalive and closes immediately; retain
   `Last-Event-ID` replay and cover the short-lived listener behavior with an integration test.
   Documentation and operator diagnostics ride with the two behavior changes above.

## Rollout and verification

- Merge through a feature PR to git branch `staging`.
- Deploy and soak in lab/dev before promotion.
- Canary authenticated initialize, `conversations_read`, `notifications_read`, and `direct_messages_read`.
- Open multiple actor clients and verify Body concurrency returns to baseline between requests, with no cold-start OAuth
  probe panics, API Gateway integration throttles, or JSON-RPC `-32603` failures.
- Roll back if OAuth metadata returns an incorrect issuer, `Last-Event-ID` replay regresses, or clients fail to reconnect
  after the short-lived idle GET response.
