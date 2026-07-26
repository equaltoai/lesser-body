# Panonomous soul-document v2 in Body

Body validates and stores the stable public schema at
`https://spec.lessersoul.ai/contracts/panonomous/soul-document/v2/schema.json`. No JSON-schema dependency is added:
`internal/agentcontent` implements the closed Go shape and the schema's normative UTF-8 byte bounds.

## Validation and lifecycle

- Required author fields are `agent_id` and non-empty `body`; body-only documents remain valid.
- `body` is limited to 49,152 UTF-8 bytes. Optional trimmed `summary` is limited to 2,048 UTF-8 bytes.
- Optional `structure` is closed at `structure.five_bodies`; all five bodies are required and `soul.refusals` is
  non-empty.
- Optional `provenance` is all-or-nothing and validates the lowercase `sha256:` candidate hash and source enum.
- Lifecycle/audit fields are server-owned.
- Every upsert creates a new draft `soul_version`.
- `agent_soul_publish` is the explicit, idempotent `draft -> published` owner act.
- A pre-v2 opaque row returns typed `agent_soul_rewrite_required`; rewrite it with `agent_soul_upsert`, then publish the
  validated v2 draft with `agent_soul_publish`. Lifecycle transitions never synthesize v2 history from opaque rows.
- Only `published -> archived` is valid. Archived snapshots are never eligible for Ba rendering.
- Each soul write updates the mutable current projection and appends a write-once history row in one TableTheory
  transaction. Published content therefore remains immutable when a later edit creates a new draft.
- Lifecycle history records the transition actor/time separately; document `updated_by_subject_id` and `updated_at`
  continue to identify the subject/time of the immutable draft write described by the public schema.

The storage `version` is the optimistic record version and is distinct from `soul_version`.

## Deterministic Hosted Genesis application

`agent_genesis_finalize` keeps declaration application entirely Body-side:

1. read the exact Host conversation with the existing `ReadConversation` contract;
2. require `declaration_candidate.phase="finalized"` and a declaration-ready or published conversation;
3. verify the complete owner-review hash;
4. extract the exact delimited canonical JSON and verify its candidate hash;
5. decode the closed five-body overlay and the hash-authenticated canonical minting model;
6. render one deterministic Markdown template;
7. after Host publication and registry projection, idempotently seed that document as `published`.

This path performs only Go JSON decoding, SHA-256 checks, validation, and template rendering. It never invokes a
MicroVM, LLM, provider, or sibling repository. A retry repairs a matching partial draft and does not overwrite
different owner-authored content.

Template excerpt:

```text
# Agent soul

## Identity

{{.Identity.Summary}}

...

### Refusals
{{range $index, $refusal := .Soul.Refusals}}
{{addOne $index}}. **Bypass:** {{$refusal.Bypass}}
   **Invariant:** {{$refusal.Invariant}}
   **Closest safe path:** {{$refusal.ClosestSafePath}}
{{end}}
```

`internal/agentcontent.RenderFiveBodiesMarkdown` owns this single template. Ptah uses it to create the canonical body;
Ba uses it when typed five-body structure is present. Ba otherwise renders the canonical body unchanged.

## Identifier vocabulary

The soul document and Body content-store key use the account-scoped registry `agent_id`. Host `local_id` (also used as
the local agent username) remains a separate registry field, and Lesser Soul `soul_agent_id` remains the separate
binding/identity identifier. Finalize seeding does not substitute one identifier for another.
