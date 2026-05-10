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

## Command

```bash
scripts/m0_baseline_mcp_probe.py | tee m0-baseline-probe.jsonl
```

The script prints one line per probe with pass/fail/skip, elapsed time, HTTP response size, and compact metadata. It
prints a final `SUMMARY` JSON object suitable for attaching to the Project 21 issue or deploy notes.

## Required evidence

The report must include:

- pass/fail/skip per probe;
- elapsed time per probe;
- approximate payload size for large read paths;
- `notifications_read.diagnostics` timing/size fields when present;
- whether default output omitted raw/debug payloads;
- whether failures appear to be Lesser API latency, body shaping/serialization, or MCP transport timeout.

## Closure expectations

M0 is not closed until Ops reports repeatable baseline usability in the deployed environment:

- `notifications_read({"since":"", "limit":30})` no longer times out;
- Host mailbox messageRef verification works where authoritative sender provenance exists;
- email/SMS/voicemail filtered pagination is sane;
- identity lookup/verify and timestamp-since notification reads do not regress;
- no baseline read-tool output is truncated under realistic mailbox/notification state.
