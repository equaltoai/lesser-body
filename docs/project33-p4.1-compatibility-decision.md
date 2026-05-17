# Project 33 P4.1 compatibility decision

Date: 2026-05-17

## Decision

Compact defaults remain **opt-in** for P4.1. lesser-body does not flip omitted/default read behavior globally in this
milestone.

The compatibility-preserving behavior remains:

- `timeline_read` omitted `view` stays upstream-shaped; `view=standard` is the same back-compat escape hatch.
- `post_search` omitted `view` stays upstream-shaped; `view=standard` is the same back-compat escape hatch.
- `soul_read` omitted `view` stays the existing public soul bundle shape; `view=standard` is the same back-compat
  escape hatch.
- `email_read` omitted `view` stays the existing mailbox list metadata/preview shape; `view=standard` is the same
  back-compat escape hatch.

Opt-in compact/summary views remain available for agent-context slimming:

- `timeline_read(view=compact)` and `post_search(view=compact)` return bounded `StatusRef` lists with `post_get`
  expansion metadata and omission records.
- `soul_read(view=summary)` returns bounded public soul essentials with `soul_read(..., view=standard|full)`
  expansion metadata and omission records.
- `email_read(view=compact)` returns bounded mailbox refs with `email_get` / `email_get_content` expansion metadata
  and omission records.

Explicit audit/debug expansion remains explicit: `view=full` and `include_raw=true` continue to be opt-in only where
already supported. `email_read(view=full)` and `email_read(include_raw=true)` do not fetch or inline full message bodies;
`email_get_content` remains the full email body path.

## Rationale

The compact views added through P1-P3 are useful and measured, but existing clients and probes still issue some calls
without `view` and parse standard/upstream-shaped fields from `content[0].text` or `structuredContent`. Flipping the
global omitted/default shape before P4.2 docs/probe guidance and Ops live compact-default evidence would make a
context-size improvement by changing a compatibility contract that clients have not yet been taught to rely on safely.

The safe P4.1 outcome is therefore a recorded decision plus compatibility coverage: keep compact views opt-in, preserve
`view=standard` / `view=full` escape and expansion routes, and defer any global/profile/runtime default flip until after
P4.2 and explicit Ops live evidence.

## Rollback and escape path

Because P4.1 does not change runtime defaults, rollback is documentation/test-only. If a later milestone enables compact
defaults by global, profile, or runtime configuration, every compact-capable read surface must keep explicit
`view=standard` as the caller-visible compatibility escape hatch and `view=full` / explicit raw-debug behavior where
already supported.

## Validation scope

P4.1 compatibility tests assert that omitted/default calls still match `view=standard` for:

- `timeline_read`
- `post_search`
- `soul_read`
- `email_read`

The same tests keep opt-in compact/summary paths covered for payload budgets, deterministic expansion refs, and omission
metadata. No Lesser, lesser-host, AppTheory, runtime, CDK, scope/profile, or static-registration changes are part of
this decision.
