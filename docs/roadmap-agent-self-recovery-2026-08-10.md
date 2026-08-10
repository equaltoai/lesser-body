# Roadmap: actor-initiated Ptah soul recovery

## Goal

Let an authenticated, already soul-bound Lesser agent recover its own Host-retained declaration into Body's Ptah registry/content plane without operator selection, cross-agent selectors, or fabricated Host history.

## Classification and surfaces

Tool-surface, security/scope-profile, additive MCP contract, Host integration, Body persistence, CDK, test coverage, and docs.

## Coordination

- **Lesser:** no code change; existing OAuth actor binding is the authority proof.
- **Host:** coordination sent to align the consumer-rule wording with bound-actor self-adoption; API/schema change not requested.
- **Soul/greater/sim:** no code change.
- **AppTheory/TableTheory:** idiomatic consumption; no framework change.
- **MCP clients:** additive discovery/release-note advisory only.

## Phases

### Phase 1: trustworthy recovery substrate

- Items: 1–3
- Dependencies: Host live recovery detail contract
- Risks: raw declaration digest drift, schema ambiguity, legacy publication misrepresentation, concurrent owner writes
- Mitigation: exact raw response parsing, closed envelope validation, classification-specific lifecycle, conditional/create-only stores, exhaustive tests

### Phase 2: actor tool and infrastructure

- Items: 4–5
- Dependencies: Phase 1
- Risks: cross-actor adoption, drone/profile bypass, overbroad IAM, partial writes
- Mitigation: no input selectors, OAuth+subject+binding checks, souled/write gates, least-privilege table grants, replay repair semantics

### Phase 3: contract evidence and documentation

- Items: 6
- Dependencies: Phases 1–2
- Risks: clients misunderstanding Host versus Body publication semantics
- Mitigation: explicit output fields/docs/canary and additive discovery fixtures

## Stage rollout

### Lab/dev

Deploy through `deploy-body` after factory merges the feature PR to git branch `staging`. Do not set a CDK timeout. Prove discovery, write/souled filtering, wrong-scope and unbound rejection, published recovery, legacy draft recovery, replay, Ptah `agent_get/list/soul_get/instructions_get`, and no Host business-state mutation. Soak at least one complete recovery/replay observation cycle.

### Deploy-stage staging (where used)

Repeat with non-production bound fixtures and verify CloudWatch recovery error/audit rates and DynamoDB rows. No stage skipping.

### Live

Operator authorization, promotion, release, and deployment are required. Recover one affected agent first, verify Ptah visibility/content and Host immutability, then allow the remaining agents to self-recover. Monitor invocation failures, binding mismatches, Host rate limits, Lambda errors, and table throttling.

## Deploy ordering

Existing Theory stages are subsequent updates and require no Lesser redeploy order. New stages retain the mandatory unsouled Lesser → Body → soul-enabled Lesser order.

## Rollback

Roll back the Body Lambda/CDK version without deleting retained tables, SSM exports, or Lambda versions. Materialized rows are durable and idempotent; never manually edit or delete them as rollback.

## Definition of done

All six changes pass local validation and the governance rubric, every commit is DCO-signed, the governed PR targets git branch `staging`, CI is green, Host coordination is recorded, and no merge or deploy is performed by Body stewardship.
