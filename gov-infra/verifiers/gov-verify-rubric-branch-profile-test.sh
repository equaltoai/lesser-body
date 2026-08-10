#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERIFIER="${SCRIPT_DIR}/gov-verify-rubric.sh"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

REMOTE="${TMP_ROOT}/remote.git"
SEED="${TMP_ROOT}/seed"
RUNNER="${TMP_ROOT}/runner"

git init --quiet --bare "${REMOTE}"
git init --quiet "${SEED}"
git -C "${SEED}" config user.name "GOV-3 regression"
git -C "${SEED}" config user.email "gov-3-regression@example.invalid"
git -C "${SEED}" config commit.gpgsign false

printf 'main\n' >"${SEED}/state.txt"
git -C "${SEED}" add state.txt
git -C "${SEED}" commit --quiet -m "main"
git -C "${SEED}" branch -M main
git -C "${SEED}" remote add origin "${REMOTE}"
git -C "${SEED}" push --quiet -u origin main
MAIN_SHA="$(git -C "${SEED}" rev-parse HEAD)"
git --git-dir="${REMOTE}" symbolic-ref HEAD refs/heads/main

git -C "${SEED}" switch --quiet -c staging
printf 'staging-v1\n' >>"${SEED}/state.txt"
git -C "${SEED}" commit --quiet -am "staging v1"

git -C "${SEED}" switch --quiet -c fix/stale
printf 'feature-stale\n' >>"${SEED}/state.txt"
git -C "${SEED}" commit --quiet -am "stale feature"
FEATURE_STALE_SHA="$(git -C "${SEED}" rev-parse HEAD)"
git -C "${SEED}" push --quiet -u origin fix/stale

git -C "${SEED}" switch --quiet staging
printf 'staging-v2\n' >>"${SEED}/state.txt"
git -C "${SEED}" commit --quiet -am "staging v2"
STAGING_SHA="$(git -C "${SEED}" rev-parse HEAD)"
git -C "${SEED}" push --quiet -u origin staging

git -C "${SEED}" switch --quiet -c feat/current
printf 'feature-current\n' >>"${SEED}/state.txt"
git -C "${SEED}" commit --quiet -am "current feature"
FEATURE_CURRENT_SHA="$(git -C "${SEED}" rev-parse HEAD)"
git -C "${SEED}" push --quiet -u origin feat/current

git clone --quiet "${REMOTE}" "${RUNNER}"
mkdir -p "${RUNNER}/gov-infra/verifiers"
cp "${VERIFIER}" "${RUNNER}/gov-infra/verifiers/gov-verify-rubric.sh"

run_case() {
  local name="$1"
  local expected_exit="$2"
  local expected_text="$3"
  local checkout_sha="$4"
  local base_ref="$5"
  local head_ref="$6"
  local base_sha="$7"
  local head_sha="$8"
  local event_path="${TMP_ROOT}/${name}.json"
  local output_path="${TMP_ROOT}/${name}.log"

  git -C "${RUNNER}" checkout --quiet --detach "${checkout_sha}"
  python3 - "${event_path}" "${base_ref}" "${head_ref}" "${base_sha}" "${head_sha}" <<'PY'
import json
import sys

path, base_ref, head_ref, base_sha, head_sha = sys.argv[1:]
with open(path, "w", encoding="utf-8") as stream:
    json.dump({
        "repository": {"full_name": "equaltoai/lesser-body"},
        "pull_request": {
            "base": {
                "ref": base_ref,
                "sha": base_sha,
                "repo": {"full_name": "equaltoai/lesser-body"},
            },
            "head": {
                "ref": head_ref,
                "sha": head_sha,
                "repo": {"full_name": "equaltoai/lesser-body"},
            },
        }
    }, stream)
PY

  set +e
  (
    cd "${RUNNER}"
    env \
      GITHUB_ACTIONS=true \
      GITHUB_EVENT_NAME=pull_request \
      GITHUB_BASE_REF="${base_ref}" \
      GITHUB_HEAD_REF="${head_ref}" \
      GITHUB_EVENT_PATH="${event_path}" \
      bash gov-infra/verifiers/gov-verify-rubric.sh --branch-profile-only
  ) >"${output_path}" 2>&1
  local actual_exit=$?
  set -e

  if [[ "${actual_exit}" -ne "${expected_exit}" ]]; then
    cat "${output_path}" >&2
    echo "${name}: expected exit ${expected_exit}, got ${actual_exit}" >&2
    return 1
  fi
  if ! grep -Fq "${expected_text}" "${output_path}"; then
    cat "${output_path}" >&2
    echo "${name}: missing expected text: ${expected_text}" >&2
    return 1
  fi
  echo "${name}: PASS"
}

run_case \
  feature-current 0 "feature -> staging lineage PASS" \
  "${FEATURE_CURRENT_SHA}" staging feat/current "${STAGING_SHA}" "${FEATURE_CURRENT_SHA}"
run_case \
  feature-stale 1 "feature head is not based on current origin/staging" \
  "${FEATURE_STALE_SHA}" staging fix/stale "${STAGING_SHA}" "${FEATURE_STALE_SHA}"
run_case \
  promotion-valid 0 "staging -> main promotion PASS" \
  "${STAGING_SHA}" main staging "${MAIN_SHA}" "${STAGING_SHA}"

# A promotion workflow can start while the PR is open and reach GOV-3 after
# GitHub has merged it. First prove that an unrelated main advance does not
# make the stale event acceptable.
git -C "${SEED}" switch --quiet main
printf 'main-advanced-without-promotion\n' >>"${SEED}/state.txt"
git -C "${SEED}" commit --quiet -am "main advanced without promotion"
ADVANCED_MAIN_SHA="$(git -C "${SEED}" rev-parse HEAD)"
git -C "${SEED}" push --quiet origin main

run_case \
  promotion-stale-unmerged 1 "promotion event base is not current origin/main" \
  "${STAGING_SHA}" main staging "${MAIN_SHA}" "${STAGING_SHA}"

# Restore the fixture's remote main to the pinned event base, then reproduce
# GitHub's exact two-parent staging promotion merge without force-pushing a
# real repository branch.
git --git-dir="${REMOTE}" update-ref refs/heads/main "${MAIN_SHA}" "${ADVANCED_MAIN_SHA}"
git -C "${SEED}" switch --quiet -C promotion-merged-during-check "${MAIN_SHA}"
git -C "${SEED}" merge --quiet --no-ff staging -m "merge staging promotion"
git -C "${SEED}" push --quiet origin HEAD:main

run_case \
  promotion-merged-during-check 0 "staging -> main promotion already merged PASS" \
  "${STAGING_SHA}" main staging "${MAIN_SHA}" "${STAGING_SHA}"
run_case \
  promotion-invalid-source 1 "unauthorized promotion source" \
  "${FEATURE_CURRENT_SHA}" main feat/current "${MAIN_SHA}" "${FEATURE_CURRENT_SHA}"

echo "GOV-3 branch-profile regression: PASS"
