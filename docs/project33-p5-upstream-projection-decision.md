# Project 33 P5 upstream projection decision

Date: 2026-05-17

## Decision

No upstream Lesser or lesser-host compact projection is justified **now** from the available Project 33 evidence.

Keep the deferred upstream issues in backlog/deferred state:

- `equaltoai/lesser#987` — deferred compact Lesser social/notification projections if metrics justify.
- `equaltoai/lesser-host#322` — deferred compact Host mailbox API if metrics justify.

Do not implement cross-repo changes, do not add new upstream contracts, and do not close off future work. Reopen the
upstream projection question only when live backend/network/Lambda measurements show that body-side response shaping no
longer addresses the dominant cost.

## Body-side measurement summary

Project 33 reduced MCP response size by adding explicit compact/summary projections and deterministic expansion routes
inside body. The current evidence measures **MCP JSON-RPC response size**, not upstream API response bytes, backend
query cost, or Lambda CPU/memory cost.

| Surface | Opt-in compact/summary measurement | Compatibility / expanded measurement | P5 read |
| --- | ---: | ---: | --- |
| `timeline_read(view=compact, limit=5)` | 4,400 bytes; under 6 KB target | `view=standard`: 30,165 bytes | Body-side compact response solved the MCP context bloat for timeline reads. |
| `post_search(view=compact, limit=10)` | 7,806 bytes; under 8 KB target | `view=standard`: 69,433 bytes | Body-side compact response solved the MCP context bloat for search reads. |
| `notifications_read(view=compact, limit=10)` | 7,825 bytes; under 8 KB target | omitted/default: 18,517 bytes | Body-side compact response solved the MCP context bloat for notification reads. |
| `conversations_read(view=compact, limit=10)` | 5,619 bytes; under 6 KB target | omitted/default: 20,969 bytes | Body-side compact response solved the MCP context bloat for conversation reads. |
| `soul_read(self=true, view=summary)` | 2,207 bytes; under 8 KB target | omitted/default and `view=standard`: 11,285 bytes; `view=full`: 18,587 bytes | Body-side summary solved the MCP context bloat for public soul reads. |
| `email_read(folder=inbox, limit=10, view=compact)` | 7,504 bytes; under 8 KB target | omitted/default: 18,072 bytes; `view=standard`: 18,073 bytes; `view=full`: 54,407 bytes; `include_raw=true`: 54,375 bytes | Body-side compact mailbox refs solved the MCP context bloat for mailbox list reads. |

These measurements are intentionally compatibility-preserving. Omitted/default reads remain standard/upstream-shaped
where Project 33 decided to keep them that way, and explicit `view=standard` / `view=full` paths remain available.

## Cost accounting

The measured cost that Project 33 set out to reduce was MCP agent-context payload size. Body-side projection now keeps
the major compact/summary read paths within their target budgets while preserving explicit expansion tools:

- `post_get` for compact timeline/search status refs and target post refs.
- `notification_get` for compact notification refs.
- `soul_read(..., view=standard|full)` for summary soul refs.
- `email_get` / `email_get_content` for compact mailbox refs.

The available evidence does **not** yet identify an unsolved upstream cost:

- no measured Lesser response byte budget over the body↔Lesser hop after compaction;
- no measured lesser-host mailbox response byte budget over the body↔Host hop after compaction;
- no measured Lambda duration, memory, or serialization CPU cost showing upstream payload normalization as the next
  bottleneck;
- no measured downstream DynamoDB/API fanout cost attributable to still-fetching full upstream payloads;
- no live Ops evidence that compact MCP responses still exceed client context budgets after P4.2 probe guidance.

Because the current measurements prove body-side MCP response shaping is effective, upstream projections would be an
architecture/contract expansion without a named backend/network/Lambda cost. That is not justified under P5.

## Trigger metrics to reopen upstream work

Reconsider `equaltoai/lesser#987` or `equaltoai/lesser-host#322` only if live or staging measurements identify a
specific cost that body-side shaping cannot reduce. Useful reopening evidence would include one or more of:

- body↔Lesser or body↔Host upstream response byte measurements that remain large enough to dominate latency or Lambda
  network transfer despite compact MCP output;
- Lambda duration/CPU/memory profiles showing normalization of full upstream payloads as the primary remaining cost;
- CloudWatch/X-Ray/API client timing showing upstream payload fetch/deserialize/serialize work as the next bottleneck;
- host mailbox list calls where full upstream metadata transfer is materially larger or slower than compact MCP output
  and cannot be mitigated by body-local omission;
- social/notification list calls where upstream Lesser response size or fanout cost dominates after MCP response
  compaction;
- a measured client-facing SLA regression tied to upstream transfer, not MCP response size.

Any reopened upstream issue should name:

1. the exact measured cost;
2. the target repo and endpoint/contract affected;
3. why body-side projection is insufficient;
4. the compatibility plan for `view=standard` / `view=full` fallbacks and existing Mastodon, ActivityPub, and
   lesser-host mailbox contracts.

## Recommendations for deferred issues

- `equaltoai/lesser#987`: leave deferred/open. Do not start compact Lesser social/notification projections until
  timeline/search/notification upstream response bytes, latency, or Lambda CPU/memory cost are measured as the next
  bottleneck.
- `equaltoai/lesser-host#322`: leave deferred/open. Do not start a compact Host mailbox API until body↔Host mailbox
  response bytes, latency, or Lambda CPU/memory cost are measured as the next bottleneck.

If coordination comments are desired after this body PR merges, link to this decision record and state that the issues
remain deferred pending the trigger metrics above. Do not close the sibling issues as "not needed forever"; the P5
decision is "not justified now."

## Guardrails preserved

- No Lesser or lesser-host code changes.
- No new upstream API contract.
- No AppTheory/runtime redesign.
- No agent hot-context / `AGENTS.md` slimming.
- No compact default flip.
- No `view=standard` alias removal.
- Private reachability fail-closed semantics remain explicit: `private_reachability_unavailable`, source/contract/status
  and reason fields, and private mint-conversation boundaries are not compressed into generic denials.
