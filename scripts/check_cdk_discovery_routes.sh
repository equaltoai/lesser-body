#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

export CDK_DEFAULT_ACCOUNT="${CDK_DEFAULT_ACCOUNT:-000000000000}"
export CDK_DEFAULT_REGION="${CDK_DEFAULT_REGION:-us-east-1}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

TMP_TEMPLATE="$(mktemp)"
cleanup() {
  rm -f "$TMP_TEMPLATE"
}
trap cleanup EXIT

cd "$ROOT_DIR/cdk"

./node_modules/.bin/cdk synth -c app=lesser -c stage=dev -c baseDomain=example.com >"$TMP_TEMPLATE"

grep -q '/.well-known/mcp.json' "$TMP_TEMPLATE"
grep -q '/.well-known/oauth-protected-resource' "$TMP_TEMPLATE"

echo "OK: synthesized template includes both public discovery routes"
