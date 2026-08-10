# Lesser-integration audit: actor-initiated Ptah soul recovery

## Integration surfaces

- **JWT:** unchanged validation and audience contract. The tool accepts only the existing OAuth principal type and stable JWT subject.
- **DynamoDB:** Body continues the established read-only `SOUL_BODY_BINDING_USERNAME#<actor>` lookup in Lesser's table. No Lesser rows are written and no new Lesser access pattern is added.
- **REST API:** no new Lesser endpoint.
- **SSM:** no export name or shape change.
- **Deployment:** existing Body stack gains Ka access to Body-owned instance tables; no Lesser routing change.

## Authority chain

The actor path and OAuth username must match through `WithActorBinding`. `ResolveForActor` must return a determined souled profile and bound Host agent ID. The recovery handler never accepts either value from tool input and requires Host detail `agent_id` and `local_id` to match those authoritative values.

## Ownership boundary

Lesser owns OAuth issuance and the binding row. Host owns retained declarations and recovery integrity. Body owns Ptah registry/content adoption. This change neither writes Lesser's table directly nor alters Lesser's binding semantics.

## Tests

Existing binding-resolution tests remain authoritative; handler tests inject resolved runtime context and prove unbound/lookup-failure states fail closed. CDK tests prove Ka retains read-only binding-table access and receives only Body-owned table write permissions.

## Coordination verdict

No Lesser code or deployment-order change is required. Existing deployed stages are subsequent updates and can update Body independently. First-time stages still use unsouled Lesser → Body → soul-enabled Lesser.
