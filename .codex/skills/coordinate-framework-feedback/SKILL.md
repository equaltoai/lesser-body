---
name: coordinate-framework-feedback
description: Use when building or maintaining body surfaces framework awkwardness — an AppTheory MCP runtime limitation, a TableTheory query-builder friction, a CDK construct gap. Produces a cleanly-shaped signal for the relevant Theory Cloud framework steward rather than a local patch. body's role as flagship consumer of AppTheory's MCP runtime means framework-consumption awkwardness here is scope-evidence for framework evolution.
---

# Coordinate framework feedback

body is a flagship consumer of AppTheory's **MCP server runtime** — the newer surface in AppTheory (v0.22.0) that formalizes how Lambda-hosted MCP servers are built, authenticated, discovered, and dispatched. body's consumption informs how that runtime matures. When consuming AppTheory's MCP runtime (or TableTheory's session-persistence surface, or CDK constructs) is awkward here, that awkwardness is the canonical signal for the framework's evolution — not license to patch locally.

This skill handles the signal cleanly. It walks the awkwardness, separates "body is expressing the concern wrong" from "the framework has a genuine gap," and produces a shaped report for the relevant framework steward.

## The frameworks body consumes

- **AppTheory v0.22.0** — especially the **MCP server runtime** (how Lambda-hosted MCP servers are structured, how tools / resources / prompts register, how `.well-known/mcp.json` is generated, how OAuth protected-resource metadata is served, how JSON-RPC dispatch works). Steward: Theory Cloud AppTheory steward.
- **TableTheory v1.5.3** — DynamoDB ORM, single-table tag semantics, used for per-actor MCP session persistence and memory tools. Steward: Theory Cloud TableTheory steward.
- **CDK constructs** (if body uses AppTheory's CDK constructs) — for Lambda function URLs, SSM parameter publishing, session-table provisioning.

## When this skill runs

Invoke when:

- A tool-registration pattern would require an AppTheory MCP runtime feature or lifecycle hook that doesn't exist
- A JSON-RPC dispatch pattern would require an AppTheory middleware or error-handling capability that isn't supported
- A session-persistence model would require a TableTheory tag, query-builder capability, or lifecycle semantic that isn't supported
- A CDK construct pattern would force a workaround or duplication that feels like a framework gap
- `scope-need` flags a change as "framework-awkward"
- `investigate-issue` surfaces a root cause traced to a framework limitation rather than body's own code

## Preconditions

- **The awkwardness is described concretely.** "AppTheory MCP runtime is hard to use here" is too vague; "to register a tool whose input schema depends on runtime-resolved config (fetched from lesser-host at startup), AppTheory's tool-registration API requires static schemas at init, forcing body to register a placeholder schema and validate dynamically downstream — 30 lines of custom code where idiomatic support would be 5" is concrete.
- **The idiomatic attempt is captured.** What would the code look like if the framework supported the concern cleanly?
- **The current workaround (if any) is captured.** What does body currently do? Cost of the workaround?
- **MCP tools healthy**, `memory_recent` first — prior framework-feedback signals matter (avoid duplicating already-reported signal).

## The three-step walk

### Step 1: Is body expressing the concern wrong?

Before assuming framework limitation, check:

- **Idiomatic framework usage**: what does AppTheory's MCP runtime / TableTheory offer for this concern? Consult `query_knowledge` against the framework's knowledge base.
- **Alternative patterns**: is there a different way to express the same concern?
- **Recent framework versions**: the pinned version may lag current capability.

If body's usage is bent rather than idiomatic, the fix is local: reshape body's code. No framework-feedback signal needed. Proceed to `scope-need` for the local change.

### Step 2: Is the framework genuinely limiting?

If body's usage is idiomatic and the framework still doesn't fit, characterize the gap:

- **The concern, concretely** — what body is trying to do
- **The ideal framework support** — what would the framework offer if it covered this cleanly?
- **The current gap** — specifically what is missing (a middleware hook, a tool-registration API, a session lifecycle event, a CDK construct property, a TableTheory tag semantic)?
- **The workaround shape (if any)** — what body currently does, and the cost (code complexity, test burden, performance impact, maintenance drag)
- **The scope of the gap** — specific to body, or would other framework consumers benefit? Check `query_knowledge` for sibling-framework-consumer signals.

### Step 3: Shape the signal for the framework steward

The signal is a report, not a PR against the framework. The Theory Cloud framework steward scopes whether and how the framework grows.

Produce:

```markdown
## Framework-feedback signal: <short name>

### Target framework
<AppTheory (MCP runtime) / AppTheory (middleware) / AppTheory (CDK constructs) / TableTheory>

### Framework version in use
<pinned version from body's go.mod>

### The concern
<one-to-two sentences>

### The idiomatic code body would write if the framework supported it
```<language>
// Code sketch — illustrative, not a PR
```

### The current workaround in body (or "blocked")
```<language>
// Current code, with comments on why this is awkward
```

### Cost of the workaround
- Code complexity: <...>
- Test burden: <...>
- Performance impact: <...>
- Maintenance drag: <...>

### Scope of the gap
- Specific to body: <yes / likely broader>
- Other known consumers affected: <list if known from query_knowledge>

### Body's workaround posture
- Continue workaround while framework evolves: <yes / no>
- Workaround is temporary / awaits framework: <yes / no>

### Proposed next step
<the framework steward scopes the framework change via the framework's own scope-need flow; body's steward does not patch the framework locally>
```

This report goes to the framework steward through the user — you do not edit the framework repo. You surface the signal.

## The explicit refusal to patch locally

The discipline is absolute:

- **No monkey-patches** to AppTheory's MCP runtime, middleware, or CDK constructs in body's tree
- **No forked copies** of TableTheory's query builder or tag handling
- **No "temporary" framework overrides** that accumulate over time
- **No pinning to an unreleased framework commit** that hasn't been published
- **No vendoring** of framework code into body's `internal/`

If the framework genuinely blocks critical work, escalate to the user. The user may decide to prioritize framework evolution, accept a workaround, or rethink body's approach. These are scope-level decisions, not steward-level ones.

## The continuity discipline

Framework-feedback signals accumulate. When a signal is sent:

- **Record in memory** — which framework, what concern, what signal was sent, when
- **Track the framework's response** — when the framework steward responds (via scoped need, feature release, decline, redirect), update the memory entry
- **Revisit on framework version bumps** — when body bumps AppTheory or TableTheory, check whether pending signals are now addressed

## Refusal cases

- **"Just patch this AppTheory MCP runtime middleware locally; the framework steward will get around to it."** Refuse. Local patches degrade framework coherence.
- **"Fork TableTheory's query builder for a one-off optimization."** Refuse. The optimization belongs in the query builder.
- **"Skip the framework-feedback signal; we need this to ship."** Refuse. The signal doesn't block body's work — body documents the workaround and proceeds. The signal goes to the framework steward for their scoping.
- **"Send a framework-feedback signal for every minor awkwardness."** Refuse. Signals are for genuine gaps, not taste differences.
- **"Copy an AppTheory construct into body's tree and modify it."** Refuse. Vendoring creates silent drift.
- **"The framework steward isn't responsive, so we should fork."** Escalate to the user. Forking is a scope-level decision.

## Persist

Append every framework-feedback signal sent. High-signal memory material — the record of what body has reported to framework stewards is part of the flagship-example feedback-loop's artifact. Include: target framework, concern summary, date, framework steward's response (when received), whether the signal resulted in framework evolution.

Five meaningful entries is the right scale; routine "slightly awkward" observations don't belong here.

## Handoff

- **Signal shaped and sent to framework steward (via the user)** — stop. Record and continue body's local work (documenting the workaround if applicable) through normal `scope-need` → `enumerate-changes` → `implement-milestone` → `deploy-body`.
- **Signal reveals body is using the framework wrong** — route through `scope-need` for the local change; no framework-feedback signal needed.
- **Signal is a duplicate of a prior one** — don't re-send; update memory with the additional data point and inform the framework steward via the user.
- **Signal reveals a framework bug rather than a gap** — framework bugs are investigation work for the framework steward. Report as a bug, not a scoping request.
- **Signal reveals cross-framework impact** (AppTheory + TableTheory together) — scope via both framework stewards in coordinated conversation.
