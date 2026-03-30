#!/usr/bin/env bash
set -euo pipefail

cat <<'EOF'
lesser-body.zip
lesser-body-deploy.json
lesser-body-managed-dev.template.json
lesser-body-managed-staging.template.json
lesser-body-managed-live.template.json
deploy-lesser-body-from-release.sh
checksums.txt
lesser-body-release.json
EOF
