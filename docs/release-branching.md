# Release branching and branch protection

<!-- AI Training: Release branching for lesser-body. The staging git branch is distinct from the deploy-stage staging used in dev/staging/live deployments. -->

`lesser-body` uses the release-alignment branch model:

```text
feature branch -> staging git branch -> main -> manual v* release tag
```

The `staging` git branch is a source-control integration branch. It is **not** the deploy-stage `staging` used by CDK,
managed release templates, or rollout language such as `lab`/`dev` → deploy-stage `staging` → `live`. Do not rename or
re-point deploy-stage tooling (`cdk synth -c stage=...`, `cmd/release-template`, or the dev/staging/live managed
templates) when changing the git branch model.

## Branch roles

- **Feature branches** (`aron/*`, `chore/*`, `codex/*`, `feat/*`, `fix/*`, `milestone/*`) branch from current `main` and
  open PRs to the `staging` git branch.
- **`staging` git branch** is the integration branch. Feature → staging PRs require the existing GitHub Actions
  `ci / verify` job, which runs `go test ./...`, release-asset build/verification, regression harnesses,
  `cdk synth -c app=lesser -c stage=dev -c baseDomain=example.com`, and the discovery-route check. The staging
  protection spec requires branches to be up to date before merge.
- **`main`** is canonical, always deployable, protected, and operator-owned. Main promotion accepts PRs from the
  `staging` git branch only. Do not require `ci / verify` on `main`; staging → main promotion intentionally uses default
  GitHub checks plus branch rules only and must not re-run the staging gate as a required check.
- **Releases** are manual `v*` tags cut from `main`. The release workflow asserts the tagged commit is an ancestor of
  `origin/main` before publishing assets for both tag-push and workflow_dispatch paths.

## Branch-protection specs

The committed specs are:

- `.github/branch-protection/staging.json`
- `.github/branch-protection/main.json`

They contain two layers:

1. `policy`: the human-readable `lesser-body` release policy, including the distinction between the `staging` git branch
   and deploy-stage `staging`.
2. `github_branch_protection.payload`: the exact GitHub REST branch-protection payload to apply.

GitHub classic branch protection does not expose a PR head-branch allowlist field. The `main` spec therefore records
`allowed_pr_sources: ["staging"]` as the operator merge policy, while the API payload enforces the machine-enforceable
parts: required PR, operator-owned branch updates, no direct pushes, no force-pushes, and no required `ci / verify` status
check on `main`.

## Operator apply commands

Branch-protection application is an operator action. Run these from a checkout containing the committed specs after
confirming the operator actor restrictions in `main.json` are still correct. The `staging` named here is the git branch,
not the deploy-stage `staging`.

```bash
# staging git branch: feature -> staging requires existing ci / verify and up-to-date branches.
jq '.github_branch_protection.payload' .github/branch-protection/staging.json \
  | gh api --method PUT \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      /repos/equaltoai/lesser-body/branches/staging/protection \
      --input -

# main: operator-owned; PRs only by policy from staging git branch; no required ci / verify check.
jq '.github_branch_protection.payload' .github/branch-protection/main.json \
  | gh api --method PUT \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      /repos/equaltoai/lesser-body/branches/main/protection \
      --input -
```

## Operator proof commands

After applying, capture the live protection dumps:

```bash
gh api /repos/equaltoai/lesser-body/branches/staging/protection | jq .
gh api /repos/equaltoai/lesser-body/branches/main/protection | jq .
```

For a live negative test, confirm direct pushes to `main` and force-pushes to both protected branches are rejected.
Because classic branch protection cannot machine-enforce the PR source branch, the operator-owned staging-only promotion
rule is verified by review/merge discipline: reject or retarget any PR to `main` whose head branch is not the `staging`
git branch.
