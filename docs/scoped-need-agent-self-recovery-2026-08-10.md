# Scoped Need: actor-initiated Ptah soul recovery

## Background

Several active Theory agents have authoritative Lesser soul/body bindings and Host-retained Hosted Genesis declarations, but predate Body's Ptah registry/content materialization. Host now exposes an integrity-checked, read-only recovery detail contract. Body has no actor tool that can safely adopt that state.

## Driver

Principal-direct. The required path is agent self-recovery, not operator-selected adoption.

## Problem

An authenticated, already soul-bound agent cannot materialize its own Host-retained declaration into Body's Ptah registry and content tables. Ptah therefore cannot consistently list or author Silas, Della, Iris, Mags, and other affected agents.

## Surface affected

Ka tool surface, scope/profile policy, Host API consumption, Body-owned Ptah registry/content persistence, CDK IAM/environment wiring, additive MCP discovery, and operator documentation.

## Tool affected

Add `soul_self_recover` to the identity group. It accepts an empty closed object; all identity and account selectors are server-derived.

## Classification

Tool-surface, security/scope-profile, Host integration, operational correctness, MCP-contract additive change, CDK, and documentation.

## Narrowest-scope proposal

Add one `write`-scoped, `souled`-only Ka tool. It requires an OAuth principal, derives the actor's bound Host agent ID from Lesser, fetches only that Host recovery detail with Body's managed InstanceKey, verifies the full recovery envelope and declaration digest, then idempotently materializes Body-owned Ptah registry, soul, and instructions state under the deploy-configured instance account. Published Host history yields a published Body soul seed; legacy-declarations-only history yields draft Body content and never fabricates Host publication.

Also correct Ptah `agent_list` identity joining so a Host agent-ID registry row merges with Lesser's username-keyed live directory by verified `local_id` rather than appearing twice.

## Explicitly out of scope

- No Host, Lesser, Soul, on-chain, signing, publication, or genesis writes.
- No operator approval or caller-supplied agent/account selector.
- No drone, instance-key, or x402 access.
- No dynamic tool registration and no exposure of the Ptah operator surface on Ka.
- No SSM export changes.

## Success criteria

1. A write-scoped OAuth caller on `/mcp/{actor}` whose Lesser binding matches the Host detail can invoke `soul_self_recover` with `{}` and receive the recovered registry/content lifecycle summary.
2. Replay is idempotent and never overwrites owner-authored/differently-provenanced content.
3. Actor, agent ID, domain/local ID, provenance, classification, version chain, and migration digest mismatches fail before writes.
4. Drone, unbound, wrong-scope, non-OAuth, and cross-actor callers fail closed.
5. `published_artifact_verified` produces published Body content; `legacy_declarations_only` remains draft.
6. Ptah `agent_list` returns one merged entry for recovered agents.

## Specialist routing

- Tool surface: `evolve-tool-surface` required.
- MCP contract: `preserve-mcp-contract` required (additive discovery only).
- Lesser integration: `coordinate-with-lesser` required (existing binding read only).
- Host integration: coordination email sent to the Host steward; no API change requested.
- Framework: idiomatic AppTheory static registration and TableTheory stores; no framework patch.
- Deploy: CDK change requires `deploy-body` after merge.

## Consumer impact

MCP clients gain one additive tool. Existing clients, endpoints, OAuth metadata, JSON-RPC envelopes, scopes, resources, and prompts are unchanged.

## AGPL posture

No dependency or license change.

## Open questions

Host documentation currently describes operator approval for adoption. Body has asked Host to recognize an authenticated, already-bound actor as adoption authority while preserving InstanceKey as server trust only.
