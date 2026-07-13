---
name: implement-milestone
description: Use to execute a single milestone (or GitHub Project phase) of work — feature branch off staging, commits per enumerated task, PR review with existing ci / verify, merge to staging. Runs one milestone at a time. Deploys themselves go through deploy-body.
---

# Implement a milestone

This skill moves body work through code, review, and merge to git branch `staging`. body uses feature → staging → main: feature PRs target `staging` and require the existing `ci / verify` check; `main` accepts PRs only from `staging` and does not rerun the verify gate. Git branch `staging` is distinct from deploy-stage `staging`.

## Hard preconditions

Do not start without all of the following:

- **A specific milestone named**, from `plan-roadmap` or a GitHub Project phase.
- **Clean working tree on `staging`** at a known-green commit.
- **MCP tools healthy.** Call `memory_recent` first.
- **`go test ./...` passes** on `staging`.
- **`go vet ./...` passes.**
- **`gofmt -l .`** returns empty.
- **`scripts/build.sh`** succeeds.
- **`cdk synth -c app=<slug> -c stage=<stage> -c baseDomain=<domain>`** succeeds for a representative context if the milestone touches CDK.
- **The enumerated tasks are in-mission work** — not scope growth.
- **Specialist walks are complete** for tool-surface, MCP-contract, lesser-integration, host-delegation, framework-consumption, or advisor-brief work.
- **Advisor-dispatched milestones** have the principal's authorization from `review-advisor-brief` recorded.

If any precondition fails, stop and surface it.

## Branch and PR setup

One feature branch per milestone. One PR per milestone. One commit per task.

- **Branch name**: descriptive, scoped. Observed patterns: `aron/issue-<N>-<topic>`, `codex/<topic>`, `chore/<dep-or-toolchain>`, `feat/<feature>`, `fix/<symptom>`.
- **Branched from**: `staging` at a known-green commit.
- **PR target**: `staging`.
- **PR title**: clear. Conventional Commits style welcome (`fix(auth): tighten JWT validation for expired tokens`); lowercase present-tense also welcome.
- **Open the PR as a draft** with milestone goal and an unchecked task list.

PR description template:

```markdown
## Milestone
<short-name> — <goal from roadmap or project README>

## Classification
<security / scope-profile / MCP-contract / tool-surface / lesser-integration / host-delegation / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Surfaces affected
<enumerated>

## Specialist walks referenced
- Tool surface: <...>
- MCP contract: <...>
- Lesser integration: <...>
- Host delegation: <...>
- Framework: <idiomatic / reported upstream>

## Consumer impact
<MCP clients / operators / sibling repos>

## Tasks
- [ ] <issue 1 title>
- [ ] <issue 2 title>

## Validation
- `go test ./...`
- `go vet ./...`
- `gofmt -l .` (empty)
- `scripts/build.sh`
- `cdk synth -c app=... -c stage=... -c baseDomain=...` (if CDK changed)
- Targeted: <specific go test ./internal/...>

## Stage rollout plan (handoff to deploy-body)
- [ ] Merged to staging
- [ ] Deployed to lab / dev
- [ ] Lab soak complete
- [ ] Deployed to staging (if used)
- [ ] Staging soak complete
- [ ] Deployed to live

## Cross-repo coordination
<any required coordination or "none">

## Advisor-brief authorization (if applicable)
<summary from review-advisor-brief>
```

## The per-task loop

For each issue in the milestone, in enumerated order:

1. **Read the issue.** Confirm acceptance and planned commit subject. If drifted, stop.
2. **`memory_recent`** — refresh recent context.
3. **For bug fixes: add the regression test first.** Especially for JWT-validation, scope-gate, profile-gate, lesser-integration bugs.
4. **Make the change.** Only files in enumerated paths.
5. **Run validation.** `go test ./...` minimum; targeted `go test ./internal/<pkg>/...` for focused work. `go vet ./...`. `gofmt -l .` empty. `scripts/build.sh`.
6. **For contract-adjacent changes**: verify `.well-known/mcp.json` and OAuth metadata shape match expected; confirm JSON-RPC error responses match RFC 9728 / MCP conventions.
7. **For tool-registration changes**: validate scope and profile declarations in the registration call. Ensure `registerTools()` still produces the expected registry.
8. **For scope / profile changes**: exercise the gate with tests that confirm correct rejection of insufficient scope / incorrect profile, and correct acceptance of sufficient / correct.
9. **For lesser-integration changes**: exercise JWT validation path, DynamoDB read path, REST API call path, SSM export path — as applicable.
10. **For host-delegation changes**: exercise the comm-API call with `LESSER_HOST_INSTANCE_KEY` authentication, message-idempotency, thread resolution.
11. **For CDK changes**: `cdk synth` with representative context. Never set timeouts on synth.
12. **For dependency bumps**: run full suite; AppTheory / TableTheory / AWS SDK bumps affect unexpected areas.
13. **Commit.** Planned subject. First line under 72 characters. Explain *why* in the body for security, scope/profile, MCP-contract, lesser-integration, host-delegation, or framework-adjacent changes. Never `--no-verify`. Never `--amend` a pushed commit.
14. **Push.** Never force-push.
15. **Check task off** in PR description; update linked GitHub Project item status.
16. **`memory_append`** only when worth remembering — MCP-client quirk, scope / profile subtlety, lesser-integration edge, framework awkwardness, advisor-brief pattern. Routine commits aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## The mission rule enforced at commit time

Inside a milestone:

- **Every commit must be in-mission.** Scope growth caught here at latest; if a task is scope growth, revert and invoke `scope-need`.
- **Bug-fix commits follow test-first pattern.**
- **Auth / scope / profile commits are isolated** for security-review clarity.
- **Dependency bumps isolated** for bisect.
- **CDK commits isolated** from Go code.
- **SSM-export changes coordinate with lesser side** — document the lesser-side impact in the commit body.
- **No hardcoded secrets, JWT material, managed-instance keys, partner credentials, or `.env` files.**
- **No full-JWT / full-recipient / full-managed-key / raw-credential logging.**
- **No changes to `AGENTS.md`, branch protection, or CODEOWNERS without explicit governance authorization.**
- **No MCP-contract break without completed `preserve-mcp-contract` walk.**
- **No scope / profile loosening.**
- **No dynamic tool registration.**
- **No framework patches locally.**
- **No AGPL-incompatible dependencies or proprietary blobs.**

## If the test suite goes red mid-milestone

- **Do not** add a "fix tests" commit that touches unrelated code.
- **Do** stop, investigate, surface.
- **Do not** weaken a test to make it pass.
- If failure is caused by your most recent commit, revert with a new revert commit and re-plan.

## Finishing the milestone (PR side)

When all tasks are committed and pushed:

1. Run `go test ./...` on the tip.
2. Run `go vet ./...`, `gofmt -l .`.
3. Run `scripts/build.sh`.
4. Run `cdk synth -c app=<slug> -c stage=<stage> -c baseDomain=<domain>` if CDK changed.
5. Promote PR out of draft.
6. Update PR description: check all task boxes.
7. Request required review.
8. **Leave merging to a reviewer.**

The PR merges to git branch `staging`. Main promotion is operator-owned and PR-only from `staging`; do not deploy or release from this skill.

## Hand off to deploy-body

After operator-owned promotion to `main`, `deploy-body` owns:

- CDK deploys per stage (`lab / dev → staging → live`)
- Soak criteria verification
- SSM-export publication confirmation
- Post-deploy monitoring
- Release artifact production (if managed-consumer release is part of this milestone)

`implement-milestone` does not run deploy commands. Its output is a merged PR on `staging` + handoff.

## What this skill will not do

- Will not implement more than one milestone per run.
- Will not accept scope growth as a task — scope growth → `scope-need`.
- Will not merge PRs — required review is the process.
- Will not skip required review for "small" changes.
- Will not run deploy commands — that's `deploy-body`.
- Will not skip specialist walks for tool-surface, MCP-contract, lesser-integration, host-delegation, framework, or advisor-brief work.
- Will not delete published Lambda function versions.
- Will not force-push, amend pushed commits, or rewrite history.
- Will not bump Go runtime version as part of ordinary milestone.
- Will not add unsanitized logging, raw credentials, or raw signing material.
- Will not set timeouts on CDK commands.
- Will not patch AppTheory / TableTheory locally.
- Will not introduce AGPL-incompatible dependencies.
- Will not act on advisor briefs without `review-advisor-brief` authorization.