# Scoped Need: MCP v1.6 adoption for lesser-body

## Background

AppTheory v1.6.0 adds several MCP runtime capabilities that can improve body as the MCP capabilities runtime for lesser actors: explicit capability configuration, completion hooks, resource subscription hooks, logging-level hooks, protocol-aware capability advertisement, cancellation propagation, and task-backed tool calls. The dependency bump to AppTheory v1.6.0 / TableTheory v1.8.3 also surfaced strict Streamable HTTP transport behavior that body has already adapted to in the dependency-baseline branch.

## Driver

Aron-direct scope after the AppTheory, TableTheory, and FaceTheory update train. The question was whether body should implement anything to take advantage of AppTheory's enhanced MCP tooling.

## Problem

Without an adoption plan, body can consume the new AppTheory runtime passively while missing useful improvements for MCP clients, or worse, accidentally advertise new protocol capabilities without body-specific scope/profile, audit, rate-limit, and managed-deploy discipline. The work needs to adopt the useful pieces deliberately, in phases, while preserving body's public MCP contract.

## Surface affected

- Dependency-maintenance baseline: `go.mod`, `go.sum`, `cdk/package.json`, `cdk/package-lock.json`, `app-theory/app.json`.
- MCP contract: `initialize` capability advertisement, `tools/list` metadata, optional `completion/complete`, optional `tasks/*`, Streamable HTTP transport docs/canaries.
- Tool surface: tool metadata (`outputSchema`, execution/task support) without changing existing tool names, scopes, or profiles unless explicitly scoped later.
- MCP app lifecycle: AppTheory server options in `internal/mcpserver/`, test helpers in `internal/mcpapp/`.
- CDK / managed deploy: possible future MCP task table and managed-release inventory, if task runtime is enabled.
- Docs/scripts: `docs/mcp.md`, canary scripts, roadmap/project artifacts.

## Tool(s) affected

- Baseline / capability hardening: all registered tools indirectly through `initialize` and `tools/list` metadata.
- Output schemas: staged by tool group; no behavioral side effects.
- Completions: prompts/resources first; no tool invocation behavior changes.
- Task runtime pilot: read-only, long-running tools only; candidate tools are `skill_bundle_get` and selected `soul_read` read expansions. Outbound communication tools are explicitly excluded from the first task-runtime pilot.

## Classification

MCP-contract, tool-surface metadata, operational-reliability, test-coverage, dependency-maintenance, docs, framework-consumption. Later task-runtime phases also touch CDK and managed-deploy coordination.

## Narrowest-scope proposal

Adopt AppTheory v1.6 MCP features in a phased, fail-closed sequence:

1. Land the dependency baseline and strict Streamable HTTP compatibility updates already prepared on `codex/update-theory-pins-dependabot`.
2. Add explicit capability configuration so body advertises only the capabilities it intentionally supports today.
3. Add cancellation and transport conformance tests around the AppTheory v1.6 behavior body relies on.
4. Add tool output schemas in staged batches to improve client validation without changing tool behavior.
5. Add `completion/complete` hooks for prompt/resource references where suggestions can be generated without leaking souled-only or PII-sensitive data.
6. Prepare task runtime infrastructure behind disabled capability flags, then pilot task-backed execution only for read-only long-running tools after quota/audit/cancellation policy is in place.

## What this need explicitly does not cover

- No new MCP tools.
- No dynamic tool registration.
- No scope or profile loosening.
- No change to `/mcp/{actor}`, `.well-known/mcp.json`, or OAuth protected-resource paths.
- No outbound email/SMS task runtime in the first task pilot.
- No local delivery of email/SMS; host delegation remains the contract.
- No AppTheory or TableTheory local patches.
- No FaceTheory integration; body does not currently import FaceTheory directly or transitively.
- No deployment to live; deployment remains a `deploy-body` handoff after PR merge and review.

## Success criteria

- AppTheory/TableTheory/CDK pins are updated and validated with tests, vet, govulncheck, npm audit, and build.
- `initialize` capability advertisement is explicitly configured and regression-tested so body does not accidentally advertise completions/tasks/logging/resources.subscribe before implementation.
- MCP transport docs and canaries use strict Streamable HTTP headers and tolerate AppTheory v1.6 SSE priming frames.
- Tool metadata additions are backward-compatible and covered by `tools/list`/discovery tests.
- Completion hooks, if enabled, are scope/profile-aware and do not leak souled-only channels or private reachability data to drone callers.
- Task runtime, if enabled, is session-scoped, read-only for the first pilot, quota/rate-limited, cancellation-aware, audited, documented, and backed by CDK/managed-deploy storage with coordinated SSM/export handling.
- Every milestone lands through a PR to `main`, receives Arch review by email, and addresses review feedback before moving to the next milestone.

## Specialist routing

- Tool surface: walk required via `evolve-tool-surface` for output schemas and task execution metadata.
- MCP contract: walk required via `preserve-mcp-contract` for capability advertisement, completion hooks, task runtime, and strict transport behavior.
- Lesser integration: not touched for baseline/capability/output-schema/completion phases; walk required before any task-table SSM export or changes to lesser-proxied deployment contract.
- Framework consumption: idiomatic AppTheory v1.6 consumption. No local framework patches. If implementation surfaces a missing AppTheory hook, route through `coordinate-framework-feedback`.
- Deploy: deploy walk required after merged milestones; no deploy is part of implementation PRs.
- Advisor brief: n/a. This is Aron-direct scope.

## Consumer impact

- MCP clients: additive or semantic-refinement changes only. Capability additions (`completions`, `tasks`) must be documented and staged because clients may adapt their behavior after seeing them in `initialize`.
- Operators: task runtime may add table/config/managed-release concerns; earlier phases are code/docs only.
- lesser: no action for early phases. Coordination required if adding or exporting task table names or changing first-deploy/SSM behavior.
- host: no action for early phases. Coordination required if managed-deploy artifact shape changes or if communication-tool tasking is ever proposed.

## AGPL posture

No proprietary blobs or new AGPL-incompatible dependencies are planned. Dependency additions, if any, require license vetting before inclusion.

## Open questions

- Which read-only tool should be the first task runtime pilot: `skill_bundle_get`, `soul_read`, or another long-running read path?
- Should task runtime storage be exported via a new SSM parameter, or remain internal to body until lesser/host have a concrete consumer need?
- How much output-schema coverage is required before the first release note: all tools or staged by group?
