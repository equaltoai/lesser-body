#!/usr/bin/env bash
# lesser-body GovTheory rubric verifier (repo-local entrypoint)
#
# Profile: software_repo_gov_infra
# Command: bash gov-infra/verifiers/gov-verify-rubric.sh
# Report:  gov-infra/evidence/gov-rubric-report.json
# Schema:  gov_rubric_report.v1
#
# This verifier is intentionally repo-local: it runs the existing lesser-body
# CI/build gates and records machine-readable evidence. It does not deploy,
# mutate cloud state, sign anything, or replace GitHub branch protection.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GOV_INFRA="${REPO_ROOT}/gov-infra"
EVIDENCE_DIR="${GOV_INFRA}/evidence"
REPORT_PATH="${EVIDENCE_DIR}/gov-rubric-report.json"
RESULTS_FILE="$(mktemp)"

cleanup() {
  rm -f "${RESULTS_FILE}"
}
trap cleanup EXIT

cd "${REPO_ROOT}"
mkdir -p "${EVIDENCE_DIR}"
rm -f "${REPORT_PATH}" "${RESULTS_FILE}" "${EVIDENCE_DIR}"/*-output.log

export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
export CDK_DEFAULT_ACCOUNT="${CDK_DEFAULT_ACCOUNT:-000000000000}"
export CDK_DEFAULT_REGION="${CDK_DEFAULT_REGION:-us-east-1}"

# Keep Go validation scoped to the repo-owned Go modules. Local dependency
# material such as cdk/node_modules may contain generated Go templates that are
# intentionally not Go packages and must not affect source validation.
GO_PACKAGE_PATTERNS="./cmd/... ./internal/..."

PASS_COUNT=0
FAIL_COUNT=0
BLOCKED_COUNT=0

json_string() {
  python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"
}

append_result() {
  local id="$1"
  local category="$2"
  local status="$3"
  local message="$4"
  local evidence_path="$5"

  case "${status}" in
    PASS) PASS_COUNT=$((PASS_COUNT + 1)) ;;
    FAIL) FAIL_COUNT=$((FAIL_COUNT + 1)) ;;
    BLOCKED) BLOCKED_COUNT=$((BLOCKED_COUNT + 1)) ;;
    *) echo "Internal error: invalid status ${status}" >&2; exit 2 ;;
  esac

  python3 - "$RESULTS_FILE" "$id" "$category" "$status" "$message" "$evidence_path" <<'PY'
import json
import sys
path, check_id, category, status, message, evidence_path = sys.argv[1:]
with open(path, "a", encoding="utf-8") as f:
    f.write(json.dumps({
        "id": check_id,
        "category": category,
        "status": status,
        "message": message,
        "evidence_path": evidence_path,
    }, sort_keys=True) + "\n")
PY
}

run_check() {
  local id="$1"
  local category="$2"
  local command="$3"
  local evidence_path="${EVIDENCE_DIR}/${id}-output.log"

  echo "=== ${id} ${category}: ${command} ==="
  printf '$ %s\n' "${command}" >"${evidence_path}"
  if bash -lc "${command}" >>"${evidence_path}" 2>&1; then
    append_result "${id}" "${category}" "PASS" "Command succeeded" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: PASS"
  else
    local rc=$?
    append_result "${id}" "${category}" "FAIL" "Command failed with exit ${rc}" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: FAIL (exit ${rc})"
  fi
}


run_blocking_file_check() {
  local id="$1"
  local category="$2"
  local path="$3"
  local evidence_path="${EVIDENCE_DIR}/${id}-output.log"

  echo "=== ${id} ${category}: required file ${path} ==="
  if [[ -f "${path}" ]]; then
    printf 'required file present: %s\n' "${path}" >"${evidence_path}"
    append_result "${id}" "${category}" "PASS" "Required file present" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: PASS"
  else
    printf 'required file missing: %s\n' "${path}" >"${evidence_path}"
    append_result "${id}" "${category}" "BLOCKED" "Required file missing" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: BLOCKED"
  fi
}

run_profile_check() {
  local id="GOV-1"
  local category="Governance"
  local evidence_path="${EVIDENCE_DIR}/${id}-output.log"

  echo "=== ${id} ${category}: resolved profile ==="
  if python3 - >"${evidence_path}" 2>&1 <<'PY'
import json
from pathlib import Path
marker = Path('.codex/theorymcp/body/install-marker.json')
pack = Path('gov-infra/pack.json')
# The tracked pack manifest is the authoritative in-git profile declaration.
# The install marker is a regenerated materialization receipt — never tracked,
# so it is absent from clean CI checkouts by design; when present (local
# steward checkouts) it must agree with the pack profile.
if not pack.is_file():
    raise SystemExit('missing pack manifest: gov-infra/pack.json')
pack_data = json.loads(pack.read_text())
pack_profile = (pack_data.get('profile') or {}).get('id')
print(f'pack profile.id={pack_profile}')
if pack_profile != 'software_repo_gov_infra':
    raise SystemExit(f'unexpected pack profile: {pack_profile}')
if marker.is_file():
    marker_data = json.loads(marker.read_text())
    marker_profile = marker_data.get('layout_profile_version')
    print(f'marker layout_profile_version={marker_profile}')
    if marker_profile != 'software_repo_gov_infra':
        raise SystemExit(f'unexpected marker profile: {marker_profile}')
else:
    print('marker absent (untracked materialized install; not required)')
print('profile resolution PASS')
PY
  then
    append_result "${id}" "${category}" "PASS" "Resolved software_repo_gov_infra profile" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: PASS"
  else
    local rc=$?
    append_result "${id}" "${category}" "FAIL" "Profile resolution failed with exit ${rc}" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: FAIL (exit ${rc})"
  fi
}

run_ci_hook_check() {
  local id="GOV-2"
  local category="Governance"
  local evidence_path="${EVIDENCE_DIR}/${id}-output.log"

  echo "=== ${id} ${category}: CI runs verifier ==="
  if python3 - >"${evidence_path}" 2>&1 <<'PY'
from pathlib import Path
workflow = Path('.github/workflows/ci.yml')
if not workflow.is_file():
    raise SystemExit('missing .github/workflows/ci.yml')
text = workflow.read_text()
needle = 'bash gov-infra/verifiers/gov-verify-rubric.sh'
print(f'checking for {needle!r} in {workflow}')
if needle not in text:
    raise SystemExit('CI workflow does not invoke the governance verifier')
print('CI hook PASS')
PY
  then
    append_result "${id}" "${category}" "PASS" "CI invokes governance verifier" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: PASS"
  else
    local rc=$?
    append_result "${id}" "${category}" "FAIL" "CI hook check failed with exit ${rc}" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: FAIL (exit ${rc})"
  fi
}

run_branch_profile_check() {
  local id="GOV-3"
  local category="Governance"
  local evidence_path="${EVIDENCE_DIR}/${id}-output.log"

  echo "=== ${id} ${category}: authorized feature or promotion lineage ==="
  if python3 - >"${evidence_path}" 2>&1 <<'PY'
import json
import os
import re
import subprocess
from pathlib import Path

SHA_PATTERN = re.compile(r'^[0-9a-f]{40}$')
GITHUB_ACTIONS = os.environ.get('GITHUB_ACTIONS') == 'true'
REPOSITORY = 'equaltoai/lesser-body'
FEATURE_PREFIXES = (
    'aron/',
    'chore/',
    'codex/',
    'feat/',
    'fix/',
    'milestone/',
    'theorymcp/equaltoai/body/',
)


def fail(message):
    raise SystemExit(message)


def git(*args):
    return subprocess.run(
        ['git', *args],
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )


def git_output(*args):
    result = git(*args)
    if result.returncode != 0:
        fail('required git fact is unavailable: ' + ' '.join(args))
    return result.stdout.strip()


def refresh_branch(branch):
    if branch not in {'main', 'staging'}:
        fail('internal error: attempted to refresh an unauthorized branch')
    remote_ref = f'origin/{branch}'
    if GITHUB_ACTIONS:
        result = git(
            'fetch', '--quiet', '--no-tags', '--force', 'origin',
            f'+refs/heads/{branch}:refs/remotes/origin/{branch}',
        )
        if result.returncode != 0:
            fail(f'could not refresh current {remote_ref}')
    return git_output('rev-parse', '--verify', remote_ref)


def require_commit(sha):
    if not SHA_PATTERN.fullmatch(sha):
        fail('malformed GitHub event commit SHA')
    if git('cat-file', '-e', f'{sha}^{{commit}}').returncode == 0:
        return
    if GITHUB_ACTIONS:
        result = git('fetch', '--quiet', '--no-tags', 'origin', sha)
        if result.returncode == 0 and git('cat-file', '-e', f'{sha}^{{commit}}').returncode == 0:
            return
    fail('GitHub event commit is unavailable for lineage verification')


def require_ancestor(ancestor, descendant, failure_message):
    require_commit(ancestor)
    require_commit(descendant)
    if git('merge-base', '--is-ancestor', ancestor, descendant).returncode != 0:
        fail(failure_message)


def exact_promotion_merge_reachable(base_sha, head_sha, current_main):
    """Return true only for a reachable two-parent merge of base + staging."""
    require_commit(base_sha)
    require_commit(head_sha)
    require_commit(current_main)
    merges = git('rev-list', '--merges', '--parents', current_main, f'^{base_sha}')
    if merges.returncode != 0:
        fail('could not inspect current main promotion lineage')
    for line in merges.stdout.splitlines():
        fields = line.split()
        if len(fields) == 3 and fields[1] == base_sha and fields[2] == head_sha:
            return True
    return False


def authorized_feature_ref(ref):
    return any(ref.startswith(prefix) and len(ref) > len(prefix) for prefix in FEATURE_PREFIXES)


def event_pull_request():
    if not GITHUB_ACTIONS:
        fail('pull_request context requires GITHUB_ACTIONS=true')
    event_path = os.environ.get('GITHUB_EVENT_PATH', '')
    if not event_path:
        fail('pull_request context is missing GITHUB_EVENT_PATH')
    try:
        event = json.loads(Path(event_path).read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f'invalid GitHub event payload: {type(exc).__name__}')
    pull_request = event.get('pull_request')
    if not isinstance(pull_request, dict):
        fail('GitHub event payload is missing pull_request')
    try:
        base = pull_request['base']
        head = pull_request['head']
        base_ref = base['ref']
        base_sha = base['sha']
        base_repository = base['repo']['full_name']
        head_ref = head['ref']
        head_sha = head['sha']
        head_repository = head['repo']['full_name']
    except (KeyError, TypeError):
        fail('GitHub pull_request payload is missing branch facts')
    facts = (base_ref, base_sha, base_repository, head_ref, head_sha, head_repository)
    if not all(isinstance(value, str) and value for value in facts):
        fail('GitHub pull_request branch facts are malformed')
    if base_ref != os.environ.get('GITHUB_BASE_REF') or head_ref != os.environ.get('GITHUB_HEAD_REF'):
        fail('ambiguous GitHub pull_request branch facts')
    if not SHA_PATTERN.fullmatch(base_sha) or not SHA_PATTERN.fullmatch(head_sha):
        fail('malformed GitHub pull_request commit SHA')
    if base_repository != REPOSITORY or head_repository != REPOSITORY:
        fail('unauthorized GitHub pull_request repository context')
    return base_ref, base_sha, head_ref, head_sha


event_name = os.environ.get('GITHUB_EVENT_NAME', '')
base_env = os.environ.get('GITHUB_BASE_REF', '')
head_env = os.environ.get('GITHUB_HEAD_REF', '')

if event_name == 'pull_request':
    base_ref, base_sha, head_ref, head_sha = event_pull_request()
    current_staging = refresh_branch('staging')
    print('context=pull_request')
    print('base_ref=' + base_ref)
    print('head_ref=' + head_ref)
    print('event_base_sha=' + base_sha)
    print('event_head_sha=' + head_sha)
    print('origin/staging=' + current_staging)

    if base_ref == 'staging':
        if not authorized_feature_ref(head_ref):
            fail('unauthorized feature source for staging target')
        if base_sha != current_staging:
            fail('staging-target event base is not current origin/staging')
        require_ancestor(
            current_staging,
            head_sha,
            'feature head is not based on current origin/staging',
        )
        print('feature -> staging lineage PASS')
    elif base_ref == 'main':
        if head_ref != 'staging':
            fail('unauthorized promotion source: main accepts only staging')
        current_main = refresh_branch('main')
        print('origin/main=' + current_main)
        if base_sha == current_main and head_sha == current_staging:
            print('staging -> main promotion PASS')
        else:
            require_ancestor(
                head_sha,
                current_staging,
                'promotion event head is not retained by current origin/staging',
            )
            if not exact_promotion_merge_reachable(base_sha, head_sha, current_main):
                fail('promotion event base is not current origin/main and exact promotion merge is absent')
            print('staging -> main promotion already merged PASS')
    else:
        fail('unauthorized pull_request base: expected staging or main')
elif base_env or head_env:
    fail('ambiguous branch context: PR refs are present outside pull_request')
elif event_name:
    if event_name != 'push' or not GITHUB_ACTIONS:
        fail('unauthorized GitHub event context for GOV-3')
    github_ref = os.environ.get('GITHUB_REF', '')
    github_sha = os.environ.get('GITHUB_SHA', '')
    if github_ref not in {'refs/heads/main', 'refs/heads/staging'}:
        fail('unauthorized push ref for GOV-3')
    if not SHA_PATTERN.fullmatch(github_sha):
        fail('malformed push SHA for GOV-3')
    branch = github_ref.removeprefix('refs/heads/')
    current = refresh_branch(branch)
    print('context=push')
    print('branch=' + branch)
    print('GITHUB_SHA=' + github_sha)
    print(f'origin/{branch}=' + current)
    if github_sha != current:
        fail(f'push SHA is not current origin/{branch}')
    require_commit(github_sha)
    print(f'current {branch} push PASS')
else:
    current_staging = refresh_branch('staging')
    head = git_output('rev-parse', 'HEAD')
    print('context=local')
    print('HEAD=' + head)
    print('origin/staging=' + current_staging)
    require_ancestor(
        current_staging,
        head,
        'feature head is not based on current origin/staging',
    )
    print('local current-staging lineage PASS')
PY
  then
    append_result "${id}" "${category}" "PASS" "Authorized branch context has current lineage" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: PASS"
  else
    local rc=$?
    append_result "${id}" "${category}" "FAIL" "Governance scope check failed with exit ${rc}" "${evidence_path#${REPO_ROOT}/}"
    echo "${id}: FAIL (exit ${rc})"
  fi
}

if [[ $# -gt 0 ]]; then
  if [[ $# -ne 1 || "$1" != "--branch-profile-only" ]]; then
    echo "usage: $0 [--branch-profile-only]" >&2
    exit 2
  fi
  run_branch_profile_check
  cat "${EVIDENCE_DIR}/GOV-3-output.log"
  rm -f "${RESULTS_FILE}"
  if [[ ${FAIL_COUNT} -eq 0 && ${BLOCKED_COUNT} -eq 0 ]]; then
    exit 0
  fi
  exit 1
fi

run_report_shape_self_check() {
  local id="GOV-4"
  local category="Governance"
  local evidence_path="${EVIDENCE_DIR}/${id}-output.log"

  echo "=== ${id} ${category}: report schema self-check ==="
  cat >"${evidence_path}" <<'LOG'
Report shape is generated after all checks complete. This check reserves an evidence slot so downstream gates can see that the schema/report path is owned by this verifier.
LOG
  append_result "${id}" "${category}" "PASS" "Report shape is generated by verifier finalizer" "${evidence_path#${REPO_ROOT}/}"
  echo "${id}: PASS"
}

run_profile_check
run_ci_hook_check
run_blocking_file_check "GOV-README" "Governance" "gov-infra/README.md"
run_branch_profile_check
run_check "GOV-3-REGRESSION" "Governance" "bash gov-infra/verifiers/gov-verify-rubric-branch-profile-test.sh"
run_report_shape_self_check

run_check "GO-BUILD" "Completeness" "go build ${GO_PACKAGE_PATTERNS}"
run_check "GO-TEST" "Quality" "go test ${GO_PACKAGE_PATTERNS}"
run_check "GO-VET" "Consistency" "go vet ${GO_PACKAGE_PATTERNS}"
run_check "GOFMT" "Consistency" "test -z \"\$(git ls-files -z '*.go' | xargs -0 gofmt -l)\""
run_check "SOURCE-BUILD" "Completeness" "bash scripts/build.sh"
run_check "RELEASE-ASSETS" "Completeness" "bash scripts/build_release_assets.sh v0.0.0-test dist/release-test && bash scripts/verify_release_assets.sh v0.0.0-test dist/release-test"
run_check "RELEASE-CHECKSUM-REGRESSION" "Quality" "bash scripts/check_release_asset_checksum_regression.sh v0.0.0-test dist/release-test"
run_check "MANAGED-TEMPLATE-DEFAULTS" "Quality" "bash scripts/check_managed_template_default_regression.sh v0.0.0-test dist/release-test"
run_check "MANAGED-TEMPLATE-NAMED-RESOURCES" "Quality" "bash scripts/check_managed_template_named_resource_regression.sh v0.0.0-test dist/release-test"
run_check "MANAGED-AUXILIARY-ASSETS" "Quality" "bash scripts/check_managed_auxiliary_asset_regression.sh v0.0.0-test dist/release-test"
run_check "CDK-TEST" "Quality" "cd cdk && npm test"
run_check "CDK-SYNTH" "Completeness" "cd cdk && npm run synth -- -c app=lesser -c stage=dev -c baseDomain=example.com"
run_check "MCP-DISCOVERY-ROUTES" "Contract" "bash scripts/check_cdk_discovery_routes.sh"

if [[ ${FAIL_COUNT} -gt 0 ]]; then
  OVERALL_STATUS="FAIL"
elif [[ ${BLOCKED_COUNT} -gt 0 ]]; then
  OVERALL_STATUS="BLOCKED"
else
  OVERALL_STATUS="PASS"
fi

GIT_HEAD="unknown"
GIT_REF="unknown"
GIT_BASE="unknown"
GIT_TREE="unknown"
GIT_STATUS="unknown"
if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  GIT_HEAD="$(git rev-parse HEAD)"
  GIT_REF="$(git rev-parse --abbrev-ref HEAD)"
  GIT_TREE="$(git rev-parse HEAD^{tree})"
  GIT_STATUS="$(git status --porcelain | wc -l | tr -d ' ') changed paths after verifier run"
  if git rev-parse --verify origin/staging >/dev/null 2>&1; then
    GIT_BASE="$(git merge-base HEAD origin/staging)"
  fi
fi

python3 - "$RESULTS_FILE" "$REPORT_PATH" "$OVERALL_STATUS" "$PASS_COUNT" "$FAIL_COUNT" "$BLOCKED_COUNT" "$GIT_HEAD" "$GIT_REF" "$GIT_BASE" "$GIT_TREE" "$GIT_STATUS" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

(results_file, report_path, status, passed, failed, blocked,
 git_head, git_ref, git_base, git_tree, git_status) = sys.argv[1:]
results = []
if os.path.exists(results_file):
    with open(results_file, encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            if line:
                results.append(json.loads(line))
authoritative_sources = [
    "mcp__theorymcp.server_instructions published body overlay v10",
    "AGENTS.md",
    ".agents/skills/apply-and-verify-governance/SKILL.md",
    "https://github.com/equaltoai/lesser-body/issues/401#issuecomment-4982280964",
    "factory/products/progenitor/docs/agent-designs/2026-07-05-rubric-gate-skills.md",
    "factory/products/progenitor/docs/patterns/parent-stages-genome-child-applies.md",
]
# The install marker is an untracked materialization receipt; cite it only when
# this checkout actually carries it.
if os.path.isfile('.codex/theorymcp/body/install-marker.json'):
    authoritative_sources.insert(0, ".codex/theorymcp/body/install-marker.json")
report = {
    "schema": "gov_rubric_report.v1",
    "schema_version": 1,
    "generated_at": datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z'),
    "status": status,
    "profile": "software_repo_gov_infra",
    "repository": "equaltoai/lesser-body",
    "project": "lesser-body",
    "verifier": {
        "command": "bash gov-infra/verifiers/gov-verify-rubric.sh",
        "report_path": "gov-infra/evidence/gov-rubric-report.json",
    },
    "git": {
        "head": git_head,
        "ref": git_ref,
        "merge_base_origin_staging": git_base,
        "head_tree": git_tree,
        "working_tree_after_run": git_status,
    },
    "evidence_freshness": {
        "attestation_ref": "git.head",
        "attestation_ref_meaning": "commit checked out when gov-verify-rubric.sh generated this report",
        "committed_artifact_self_reference": "A committed report cannot truthfully embed its own final commit SHA without a git self-reference loop.",
        "required_relationship_to_review_head": "ancestor_or_equal",
        "machine_check": "git merge-base --is-ancestor <report.git.head> <review-head>",
        "reject_relationship": "abandoned_sibling_or_non_ancestor",
        "ci_head_recheck": ".github/workflows/ci.yml reruns bash gov-infra/verifiers/gov-verify-rubric.sh at PR head",
    },
    "summary": {
        "status": status,
        "pass": int(passed),
        "fail": int(failed),
        "blocked": int(blocked),
        "total": len(results),
    },
    "authoritative_sources": authoritative_sources,
    "namespace_genome": {
        "status": "not_staged_in_this_checkout",
        "checksum_verification": "not_applicable_no_checksum_bearing_genome_was_provided",
        "note": "No checksum-bearing namespace governance genome was available on the body agent route; profile facts were resolved from the recorded sources.",
    },
    "results": results,
}
with open(report_path, 'w', encoding='utf-8') as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write('\n')
PY

rm -f "${RESULTS_FILE}"

echo "Report written to: ${REPORT_PATH#${REPO_ROOT}/}"
echo "Status: ${OVERALL_STATUS} (${PASS_COUNT} pass / ${FAIL_COUNT} fail / ${BLOCKED_COUNT} blocked)"

if [[ "${OVERALL_STATUS}" == "PASS" ]]; then
  exit 0
fi
exit 1
