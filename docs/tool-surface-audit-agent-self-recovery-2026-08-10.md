# Tool-surface audit: actor-initiated Ptah soul recovery

## Proposed change

Add statically registered `soul_self_recover` to Ka's identity tools.

## Tool

- **Name:** `soul_self_recover`
- **Group:** identity
- **Scope:** `write`
- **Profile:** `souled` only
- **Input:** closed empty JSON object; no actor, account, agent ID, classification, declaration, or approval input
- **Output:** bounded recovery status, Host classification/provenance metadata, Ptah registry summary, and Body content lifecycle/version summaries; exact recovered declaration remains in Ptah `agent_soul_get`, not duplicated into the tool's text block
- **Side effects:** create/update the authenticated actor's Body-owned Ptah registry projection; create-only soul and instructions seeds; published classification may publish only the identical newly seeded Body soul
- **Idempotency:** deterministic source digest plus create-only/identical-source persistence. Existing owner-authored or differently-provenanced content wins and returns conflict.
- **Rate limiting:** one Host detail read per invocation; Host's existing InstanceKey recovery rate limit applies
- **Audit:** normal Ka write-tool audit plus sanitized recovery outcome; no declaration bytes, full identities, bearer tokens, InstanceKey, or raw Host error bodies

## Scope/profile impact

No loosening. Explicit write classification and souled-only runtime membership are required and tested. The handler additionally requires OAuth principal type, nonempty subject, a determined Lesser soul binding, and exact binding/detail identity agreement.

## Static-registration integrity

The tool is registered synchronously from `registerTools()`, entered in the exhaustive scope map, listed only in the souled runtime contract, and documented in `docs/mcp.md`. Discovery changes are additive.

## Contract and integration impact

The public tool list gains one tool. No existing schema changes. Body reads Host with managed InstanceKey and reads Lesser's existing binding projection; all writes remain in Body-owned instance tables.

## Test coverage

- registration/scope/profile catalog checks
- OAuth/bound happy paths for both Host classifications
- insufficient scope, drone/unbound, non-OAuth, cross-actor/binding mismatch
- strict Host schema/digest/version/provenance validation
- idempotent replay and owner-content conflict
- sanitized errors/audit-safe result

## Verdict

Approved for enumeration. The design preserves fail-closed scope/profile defaults and static registration.
