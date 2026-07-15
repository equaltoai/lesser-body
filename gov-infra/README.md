# lesser-body governance infrastructure

This directory contains the repo-local governance rubric materialization for the
`software_repo_gov_infra` profile.

Authoritative profile facts inspected for this materialization:

- `.codex/theorymcp/body/install-marker.json` resolves this steward install to
  `layout_profile_version: software_repo_gov_infra`.
- The routed TheoryMCP body agent instructions publish the body branch profile
  and CI expectations for feature -> `staging` PRs.
- Factory issue #401 and its assignment comment define the required report path,
  verifier command, schema name, PASS status, and governance-only write scope.
- Progenitor governance design notes define the steward-lane profile contract:
  verifier `bash gov-infra/verifiers/gov-verify-rubric.sh`, evidence
  `gov-infra/evidence/gov-rubric-report.json`, schema
  `gov_rubric_report.v1`, and `ci_hook_required=true`.
- The repo-local `apply-and-verify-governance` skill defines the steward-side
  apply/verify cadence and fail-closed behavior for missing checks.

No checksum-able namespace governance genome was staged in this checkout. The
body agent route does not expose the namespace `govern_lifecycle_turn` /
`namespace_governance_profile_get` tools, so this materialization records the
available profile sources above and keeps the verifier bounded to existing
repo-local CI/build commands. If a future parent stages a checksum-bearing genome,
verify that manifest before replacing this tree.

## Verifier

Run from the repository root:

```bash
bash gov-infra/verifiers/gov-verify-rubric.sh
```

The verifier writes per-check logs and the machine-readable report to
`gov-infra/evidence/`. A missing command or required file is BLOCKED/FAIL, never
simulated as green.

## Evidence freshness invariant

`gov-infra/evidence/gov-rubric-report.json` records `git.head` as the
commit checked out when `bash gov-infra/verifiers/gov-verify-rubric.sh`
generated the report. A committed report cannot truthfully embed the final
commit that contains itself without a Git object self-reference loop.

Factory freshness review should therefore reject reports that point at an
abandoned sibling and accept only a report whose `git.head` is equal to or an
ancestor of the PR head under review:

```bash
git merge-base --is-ancestor <report.git.head> <review-head>
```

The CI workflow also reruns the verifier at PR head, so the committed artifact
provides repo-local evidence from the live branch lineage while CI proves the
same verifier still passes at the exact checked head.
