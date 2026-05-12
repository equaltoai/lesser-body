#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_DIR="${1:-}"

if [[ -n "${RELEASE_DIR}" ]]; then
  python3 "${ROOT_DIR}/scripts/managed_release.py" list-assets "${RELEASE_DIR}"
else
  python3 "${ROOT_DIR}/scripts/managed_release.py" list-assets
fi
