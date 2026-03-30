#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <version> [release-dir]" >&2
  exit 1
fi

VERSION="$1"
SOURCE_DIR="${2:-dist/release-test}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ ! -d "${SOURCE_DIR}" ]]; then
  echo "release directory does not exist: ${SOURCE_DIR}" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

REGRESSION_DIR="${TMP_DIR}/release-missing-manifest-checksum"
cp -R "${SOURCE_DIR}" "${REGRESSION_DIR}"

grep -v ' lesser-body-release.json$' "${REGRESSION_DIR}/checksums.txt" > "${REGRESSION_DIR}/checksums.txt.tmp"
mv "${REGRESSION_DIR}/checksums.txt.tmp" "${REGRESSION_DIR}/checksums.txt"

VERIFY_ERR="${TMP_DIR}/verify.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${REGRESSION_DIR}" > /dev/null 2> "${VERIFY_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted missing checksum coverage for lesser-body-release.json" >&2
  exit 1
fi

if ! grep -Fqx 'checksums.txt is missing published managed asset: lesser-body-release.json' "${VERIFY_ERR}"; then
  echo "verify_release_assets.sh did not report the expected missing-manifest checksum error" >&2
  cat "${VERIFY_ERR}" >&2
  exit 1
fi

DESCRIPTOR_DIR="${TMP_DIR}/release-bad-checksum-descriptor"
cp -R "${SOURCE_DIR}" "${DESCRIPTOR_DIR}"

python3 - "${DESCRIPTOR_DIR}/lesser-body-release.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
release = json.loads(path.read_text())
release["artifacts"]["checksums"]["algorithm"] = "sha512"
path.write_text(json.dumps(release, indent=2) + "\n")
PY

VERIFY_ERR="${TMP_DIR}/verify-descriptor.err"
if bash "${ROOT_DIR}/scripts/verify_release_assets.sh" "${VERSION}" "${DESCRIPTOR_DIR}" > /dev/null 2> "${VERIFY_ERR}"; then
  echo "verify_release_assets.sh unexpectedly accepted an unsupported checksum algorithm in lesser-body-release.json" >&2
  exit 1
fi

if ! grep -Fqx 'lesser-body-release.json has unsupported checksums.algorithm: sha512' "${VERIFY_ERR}"; then
  echo "verify_release_assets.sh did not report the expected checksum-descriptor error" >&2
  cat "${VERIFY_ERR}" >&2
  exit 1
fi

echo "Regression confirmed: verifier rejects missing checksum coverage and bad checksum descriptors for lesser-body-release.json"
