---
name: github-via-theorymcp
description: "Use for any GitHub operation on equaltoai/lesser-body — branches, commits, pull requests, reviews, issues, comments, checks, orchestrated closeout. Prefer the governed TheoryMCP github_* tools (agent-scoped, attributable as body-theorymcp[bot], policy-gated) over the gh CLI; fall back to gh only for operations TheoryMCP does not expose (PR body read, Projects v2, delete-branch, resolve-thread, releases, workflow dispatch, secrets, gists, gh api, other repos) and say so explicitly."
---

# GitHub via TheoryMCP

Your agent route exposes governed GitHub tools. Use them first. They act as your agent principal (`body-theorymcp[bot]`), on agent-scoped branches, under server-side policy — repo allowlists, merge and closeout gates, attributable provenance. The `gh` CLI runs under ambient local credentials with none of that governance. The integrated tools are the default; `gh` is the fallback.

## The rule

1. **Default to the integrated tools.** For any GitHub action, reach for the `github_*` tools on your TheoryMCP agent route before anything else.
2. **Fall back to `gh` only when TheoryMCP does not expose the behavior** you need — and when you do, name the gap ("TheoryMCP exposes no X, using gh for this step") so the boundary stays visible.
3. **Never use `gh` to bypass a gate** the integrated tools enforce. A refused governed operation is a signal, not an obstacle to route around — honor the refusal.

## Discover what's exposed

Do not assume the tool set from memory — it grows, and capabilities differ per repo. Before deciding something is missing, call `github_list_repos`: it returns each accessible repository plus its `capability_status` (`allowed_actions`, `missing_required_action_hints`, `project_scope_status`, `installation_grant_status`). If your operation maps to an allowed action, use the integrated tool.

## Scope and attribution

- **Repository scope:** via TheoryMCP, only repositories `github_list_repos` returns are accessible. (Currently `equaltoai/lesser-body`.) Any other repo requires `gh` — and only within a repository you are authorized to touch.
- **Attribution:** integrated writes are made as `body-theorymcp[bot]`, attributable to your principal. `gh` writes are made under the ambient personal account — not equivalent.
- **Agent-scoped branches:** `github_create_branch` and `github_commit_files` operate on branches prefixed `theorymcp/equaltoai/lesser-body/<branch_suffix>` (the server derives the prefix from the route; you supply only the suffix). Arbitrary branch names are not accepted.

## Tool inventory

All tools below are exposed by the TheoryMCP `github_*` family on your agent route (the family was proven live on the progenitor route 2026-07-03; call `github_list_repos` to confirm this route's scope and capabilities). Each returns `outcome` and acts under server-side policy.

- **Branches & commits:** `github_create_branch` (agent-scoped, from `branch_suffix`); `github_commit_files` (bounded file set to that branch).
- **Pull requests:** `github_open_pr`; `github_update_pr` (title/body/base/state/draft); `github_get_pr`; `github_list_pr_files`; `github_list_pr_commits`; `github_merge_pr` (requires exact `expected_head_sha` + `merge_method`); `github_closeout_pr` (gated orchestrator — see below).
- **Reviews:** `github_create_pr_review` (`COMMENT` / `APPROVE` / `REQUEST_CHANGES`; inline comments take `path`/`line`/`side`/`body` and require `pull_request_read`); `github_list_pr_review_threads`; `github_reply_pr_review_thread` (reply by `thread_id`).
- **Issues:** `github_create_issue`; `github_get_issue` (also works on PR numbers — returns `pull_request: true`); `github_update_issue`; `github_close_issue` (`state_reason`); `github_reopen_issue`. The issue *write* tools (close/reopen/update) **deny PR targets** — use `github_update_pr` for PR metadata.
- **Comments:** `github_create_issue_comment`; `github_upsert_issue_comment` — **idempotent by `marker`**: a second call with the same `marker` updates the same comment (`operation: updated`, same `comment_id`), it does not duplicate.
- **Checks:** `github_create_check_run` (`conclusion` ∈ success/failure/neutral/…); `github_get_check_status_summary` (by ref).
- **Discovery:** `github_list_repos`.

## Operational invariants

- **`github_merge_pr` and `github_closeout_pr` require the exact `expected_head_sha`.** A stale head is rejected (`stale_head`). Re-fetch `github_list_pr_commits` or `github_get_pr` before merging if the head may have moved.
- **`github_closeout_pr` is a gated multi-step orchestrator:** preflight (authorization) → `upsert_status_comment` (marker-anchored) → `merge_pull_request`. It can also close a linked issue and set a project status, but **`project_status` is currently unusable** — `project_v2_read`/`project_v2_write` are `missing_policy` (`endpoint_toolset_policy_missing` + `project_v2_scope_policy_missing`). Omit `project_status`; use `gh project` for project-board moves.
- **`github_get_pr` does not return the PR `body`.** To read a PR body, use `gh pr view <n> --repo equaltoai/lesser-body --json body` (a legitimate `gh` gap) or `github_update_pr` only to set it.
- **`github_create_pr_review` inline comments** are validated against the PR diff — `path`, `line`, and `side` must be real diff positions; supply `start_line`/`start_side` only for a same-side range.

## When gh is legitimate

`gh` is appropriate only for an operation with no TheoryMCP equivalent, and only within a repository you are authorized to touch. State the gap in the moment. The current `gh`-only set on this route:

- **PR body read** — `get_pr` returns no `body` → `gh pr view --json body`.
- **Projects v2** — `project_v2_*` missing policy → `gh project` for board/scope/item moves.
- **Delete a branch** — no `github_delete_branch` → `gh api repos/:owner/:repo/git/refs/heads/:branch -X DELETE` (or `git push --delete`).
- **Resolve a review thread** — no `github_resolve_thread` → `gh api graphql` or the PR UI.
- **Releases, workflow dispatch, secrets, gists, generic REST** — not exposed → `gh release`, `gh workflow run`, `gh secret`, `gh gist`, `gh api`.
- **Repositories beyond `equaltoai/lesser-body`** — not in the TheoryMCP binding → `gh` (if authorized).

Prefer read-only `gh` for gaps; treat a `gh` write that duplicates a governed tool as a red flag.

## Red flags

- Reaching for `gh pr merge` / `gh push` / `gh issue comment` / `gh pr review` when `github_merge_pr` / `github_commit_files` / `github_create_issue_comment` / `github_create_pr_review` exist.
- Falling back to `gh` because a governed tool refused you — that refusal is policy; honor it.
- Choosing `gh` without first calling `github_list_repos` to confirm the capability is actually missing.
- Passing a `project_status` to `github_closeout_pr` (it will fail; Projects v2 policy is missing).
- Merging via `github_merge_pr` / `github_closeout_pr` without re-confirming `expected_head_sha`.
- Using `gh` with credentials or in repositories outside your authorized scope.
- Treating `gh` output as governed/attributable — it is ambient personal-account work, not `body-theorymcp[bot]` provenance.