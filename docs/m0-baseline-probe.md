# Project 21 M0 baseline MCP usability probe

Run this after the M0 baseline fixes are deployed to the target dev/lab body endpoint. Deployment itself stays in the
`deploy-body` workflow; this probe is the Ops closure gate for issue #166.

## Required inputs

```bash
export MCP_ENDPOINT='https://api.<stage-domain>/mcp/<actor>'
export MCP_BEARER_TOKEN='<OAuth token for that actor>'
# or: export MCP_AUTHORIZATION='Bearer <OAuth token>'
```

Recommended identity/message fixtures:

```bash
export PROBE_LOCAL_ID='agent-local-id'
export PROBE_ENS='agent.lessersoul.eth'
export PROBE_REMOTE_AP_HANDLE='@user@remote.example'
export PROBE_ACTOR_URL='https://remote.example/users/user'
export PROBE_EMAIL='sender@example.com'
export PROBE_PHONE='+15550142'
export PROBE_MESSAGE_REF='comm-delivery-...'
export PROBE_NOTIFICATION_SINCE='2026-05-10T00:00:00Z'
```

Optional safe self-email probe:

```bash
export PROBE_SAFE_SEND_EMAIL=true
export PROBE_SELF_EMAIL_TO='self@example.com'
```

Optional closure-gate notification workflow probe, enabled only after `equaltoai/lesser#944` is deployed:

```bash
export PROBE_M0_CLOSURE=true
export PROBE_LESSER_API_BASE_URL='https://api.<stage-domain>'
# Optional: target a specific list-returned ID or run one workflow per type filter.
export PROBE_NOTIFICATION_WORKFLOW_ID='notification-id-from-list'
export PROBE_NOTIFICATION_WORKFLOW_TYPES='mention,reply'
# Future-only: set true only when the probe intentionally uses an explicit unread-only notification view.
export PROBE_NOTIFICATION_WORKFLOW_UNREAD_ONLY_VIEW=false
# Optional wrong-user negative controls, where a safe second actor token is available.
export PROBE_WRONG_USER_BEARER_TOKEN='<OAuth token for a different actor>'
```

## Command

```bash
scripts/m0_baseline_mcp_probe.py | tee m0-baseline-probe.jsonl
```

The script prints one line per probe with pass/fail/skip, elapsed time, HTTP response size, and compact metadata. It
prints a final `SUMMARY` JSON object suitable for attaching to the Project 21 issue or deploy notes. The summary includes
`mode`, `closureRequired`, `closureReady`, and `closureProbeNames` so smoke/read-surface passes cannot be mistaken for
full M0 closure evidence.

The probe treats semantic failure payloads as failures, not successful MCP transport:

- message-scoped `identity_verify` must return `messageFound:true` and `verified:true`;
- `notifications_read` probe calls request `include_diagnostics=true` and must not expose `raw` or `_raw` notification payloads;
- `email_read`, `sms_read`, and `voicemail_read` must not return an empty page with `hasMore:true`, and any
  `hasMore:true` page must include `nextCursor`.
- when `PROBE_M0_CLOSURE=true`, a list-returned notification ID must pass the semantic workflow
  list -> same-user single-get -> optional wrong-user negative control -> MCP `notification_dismiss` ->
  follow-up list/read-state. Under the current all-notifications default, if the same notification ID remains visible in
  `notifications_read`, it must expose `read:true`. A 404 or semantic error in the same-user state transition remains a
  failure; the probe does not add body-side fallback behavior for `lesser#944`.

## Required evidence

The report must include:

- pass/fail/skip per probe;
- elapsed time per probe;
- approximate payload size for large read paths;
- `notifications_read.diagnostics` timing/size fields when probes request `include_diagnostics=true`;
- whether default notification output omitted raw/debug payloads;
- whether the run is smoke-only or closure-mode (`SUMMARY.mode` and `SUMMARY.closureReady`);
- for closure-mode runs, notification workflow details: selected notification type/id (redacted), direct Lesser
  single-get status, MCP dismiss result, follow-up `notifications_read` `read` state, direct follow-up read-state
  evidence, and whether wrong-user negative controls ran;
- whether failures appear to be Lesser API latency, body shaping/serialization, or MCP transport timeout.

## Closure expectations

M0 is not closed until Ops reports repeatable baseline usability in the deployed environment:

- `notifications_read({"since":"", "limit":30, "include_diagnostics":true})` no longer times out;
- Host mailbox messageRef verification works where authoritative sender provenance exists;
- email/SMS/voicemail filtered pagination is sane;
- identity lookup/verify and timestamp-since notification reads do not regress;
- no baseline read-tool output is truncated under realistic mailbox/notification state;
- `SUMMARY.closureReady` is `true` from a closure-mode run after `lesser#944` is merged and deployed, with same-ID
  `notifications_read` follow-up showing `read:true` under the current all-notifications default.

Smoke/read-surface runs without `PROBE_M0_CLOSURE=true` are useful deploy evidence, but they are not M0 closure. They
should be attached as smoke evidence and followed by a closure-mode run once Lesser's user-scoped canonical notification
identity fix is available.

## Post-M0 cleanup checks

For the Project 21 post-M0 cleanup issues, Ops can verify these narrow contract points without reopening M0:

- `notifications_read({"types":["communication:inbound"],"limit":5})` must be accepted. Returned rows, if any, must have
  `type:"communication:inbound"` and preserve the compact `communication` summary; the call must not expose `raw` /
  `_raw` unless `include_raw=true`.
- A normal `email_send` without reply fields must still queue through lesser-host.
- `email_send` with legacy `messageId` or `inReplyTo` arguments must fail locally as a structured `invalid_request`
  tool error that directs callers to `email_reply`; the failure must occur before lesser-host returns a conversation
  boundary `403`.
