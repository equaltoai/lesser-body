---
name: create-github-project
description: Use after plan-roadmap is approved, if the roadmap warrants a tracked GitHub Project at the equaltoai org level. Translates a roadmap document into a Projects v2 kanban board with issues across the affected repos. Follows equaltoai's established project pattern.
---

# Create a GitHub Project

equaltoai tracks initiative-level work in **GitHub Projects v2** at the org level (`github.com/orgs/equaltoai/projects/<N>`), cross-repo by default. body's roadmaps often span multiple equaltoai repos (body for the tool surface, lesser for soul-enabled wiring, host for comm-API contract, sim for client-side validation).

This skill turns an approved roadmap into a project board with a clear README, status-kanban, and issues in the right repos.

## Check what tools you have

- **`gh` CLI** with project scope: `gh project create`, `gh project field-list`, `gh project item-add`, `gh issue create`, `gh issue edit --add-project`.
- If not available, produce a well-shaped markdown draft for user to adapt.

Surface which mode you're in at the start.

## When this skill runs

Invoke when:

- Roadmap is large enough to benefit from tracking (multiple phases, cross-repo coordination, multi-week cadence)
- Roadmap is an initiative (MCP runtime upgrade, soul-binding rollout, communication-tool expansion) rather than a single bug fix
- The principal has asked for a project created

Skip when:

- Roadmap is small enough for a handful of issues on body's repo
- No kanban discipline adds value

## The equaltoai project shape (reference)

From org practice (e.g., Project 20 — "Federation Readiness — Second Instance Proof"):

- **Title**: short, specific, initiative-named. `<Initiative> — <qualifier>`.
- **Short description**: one-sentence scope.
- **README**: structured by **Goal / Repos involved / Non-goals / Success means / Working method**. Explicit statement: "Treat this as a kanban. Move issues through explicit status as evidence is gathered and blockers become concrete."
- **Status field**: simple three-state — `Todo` / `In Progress` / `Done`.
- **Standard fields (10)**: Title, Assignees, Status, Labels, Linked pull requests, Milestone, Repository, Reviewers, Parent issue, Sub-issues progress.
- **Items**: GitHub Issues in one or more in-scope repos. Cross-repo is default.
- **Milestones**: separate from Status — aggregate issues into delivery groupings.
- **Parent / sub-issue hierarchy**: used to break non-trivial work into trackable children.
- **Cross-repo**: list the in-scope repos in README; create issues where each change lands.

## The create walk

### Step 1: Draft the project README

```markdown
## <Initiative title>

<Brief paragraph on what this initiative proves / ships / unblocks.>

### Goal

<The specific outcome — what "done" looks like.>

### Repos involved

- **lesser-body**: <specific work scoped to body>
- **lesser**: <specific work if soul-enabled-side wiring affected>
- **lesser-host**: <specific work if comm-API / managed-deploy affected>
- **simulacrum**: <specific work if client-side validation>

### Non-goals

- <explicit out-of-scope items>
- <things this project does not claim to do>

### Success means

- <observable, testable condition>
- <MCP client X can invoke new tool Y with expected result>
- <scope / profile gate enforces correctly for new case Z>
- <lesser↔body integration preserved through the change>

### Working method

Treat this as a kanban. Move issues through explicit status as evidence is gathered and blockers become concrete.
```

### Step 2: Create the project

```bash
gh project create \
  --owner equaltoai \
  --title "<initiative title>"
```

Capture the returned project number `<N>`.

### Step 3: Populate the README

```bash
gh project edit <N> --owner equaltoai \
  --readme "$(cat readme-draft.md)" \
  --description "<short-description>"
```

### Step 4: Confirm default fields

```bash
gh project field-list <N> --owner equaltoai --format json
```

Expected: Title, Assignees, Status (Todo / In Progress / Done), Labels, Linked pull requests, Milestone, Repository, Reviewers, Parent issue, Sub-issues progress.

### Step 5: Create issues and link them

For each enumerated change, create an issue in the repo where it lands:

```bash
gh issue create \
  --repo equaltoai/<repo> \
  --title "<title>" \
  --body "$(cat issue-body.md)" \
  --label "<labels>" \
  --milestone "<milestone>"
```

Issue body template:

```markdown
**Source**: Roadmap <roadmap name>, Phase <phase-short-name>
**Enumerated item**: #<N>

## Paths
<files or directories touched>

## Surface
<cmd/lesser-body / internal/auth / internal/mcpserver / internal/mcpapp / internal/lesserapi / internal/memory / internal/soulbinding / internal/trustconfig / internal/soulapi / internal/runtimepolicy / internal/lambdaentry / cdk / docs / deps>

## Classification
<security / scope-profile / MCP-contract / tool-surface / lesser-integration / host-delegation / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Specialist walks referenced
- Tool surface: <none / completed via evolve-tool-surface>
- MCP contract: <none / backward-compatible / breaking — coordination plan>
- Lesser integration: <none / completed via coordinate-with-lesser>
- Framework: <idiomatic / reported via coordinate-framework-feedback>

## Acceptance criterion
<one sentence>

## Validation commands
<go test ./..., scripts/build.sh, cdk synth -c app=... -c stage=... -c baseDomain=...>

## Stage rollout checkpoints
- [ ] Merged to main
- [ ] Deployed to lab / dev
- [ ] Lab soak complete
- [ ] Deployed to staging (if used)
- [ ] Staging soak complete
- [ ] Deployed to live

## Planned commit subject
<type(scope): subject>

## Parent issue
<link to parent if sub-issue>
```

Then link into the project:

```bash
gh project item-add <N> --owner equaltoai --url <issue-url>
```

### Step 6: Set project fields on each item

For each item, set:

- **Status**: `Todo` initially (move to `In Progress` / `Done` as work progresses)
- **Milestone**: the roadmap phase
- **Labels**: scope labels consistent with repo conventions
- **Parent issue**: for sub-tasks of larger work

### Step 7: Parent / sub-issue hierarchy

Use the Parent issue field for non-trivial work. Example for an MCP client expansion initiative:

- Parent: `equaltoai/lesser-body#XXX — "Add Paze identity verification tool"`
- Sub-issues:
  - `equaltoai/lesser-body#YYY — tool registration + scope declaration`
  - `equaltoai/lesser-body#ZZZ — lesserapi client method`
  - `equaltoai/lesser-body#AAA — docs/mcp.md update`
  - `equaltoai/lesser#BBB — if lesser-side API change needed`

## Labels

Apply consistently:

- `body-security` — CVE response or security-sensitive
- `body-scope-profile` — scope / profile gate work
- `body-mcp-contract` — `.well-known/mcp.json` / OAuth metadata / JSON-RPC shape
- `body-tool-surface` — tool add / modify / remove
- `body-lesser-integration` — JWT / DynamoDB / REST API / SSM exports
- `body-host-delegation` — comm-API / communication-tool
- `body-reliability` — operational-reliability
- `body-bugfix` — narrow bug fix
- `body-coverage` — test-coverage expansion
- `body-deps` — dependency bumps
- `body-docs` — documentation
- `body-framework-feedback` — AppTheory MCP runtime / TableTheory upstream signal
- `body-agpl` — license discipline
- Surface scopes: `body-auth`, `body-mcpserver`, `body-mcpapp`, `body-lesserapi`, `body-memory`, `body-cdk`, etc.
- `breaking` — breaking changes requiring coordination
- `advisor-brief` — originated from advisor dispatch

## Priority and sequencing

Status field drives kanban; Milestone field groups items into roadmap phases; priority by label and project ordering.

## The markdown-draft fallback

If `gh` CLI unavailable, produce:

```markdown
# GitHub Project draft: <initiative title>

## Project README
<README draft>

## Default fields
Status: Todo / In Progress / Done
Milestones: <phase names>
Labels: <list>

## Issues

### In equaltoai/lesser-body
1. **<issue title>** — [`<labels>`]
   - Paths: ...
   - Specialist walks: ...
   - Acceptance: ...
   - Validation: ...
   - Stage rollout checkpoints: ...
   - Parent: <link if applicable>

### In equaltoai/lesser (if cross-repo)
1. ...

### In equaltoai/lesser-host (if cross-repo)
1. ...
```

## Persist

When the project exists, append memory with the project URL and scope. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- Project exists and issues are linked → invoke `implement-milestone` with the first in-scope item.
- User wants to revise roadmap → back to `plan-roadmap`.
- Cross-repo coordination surfaces → sibling stewards looped in before their issues begin.
- Roadmap too small for a project → skip this skill; roadmap document drives `implement-milestone` directly.