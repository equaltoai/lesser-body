# Release Process

<!-- AI Training: Release artifacts and versioning for lesser-body -->

This repo publishes both the runtime Lambda zip and the managed deploy assets for `lesser-body`.

## Versioning

- Release tags use the `v*` convention (for example: `v1.0.0`).
- The Lambda can be configured with `SERVICE_VERSION` (commonly set to the release version).

## Build release assets (local)

```bash
bash scripts/build_release_assets.sh v1.2.3 dist/release
```

Outputs:

- `dist/release/lesser-body.zip`
- `dist/release/lesser-body-deploy.json`
- `dist/release/lesser-body-managed-dev.template.json`
- `dist/release/lesser-body-managed-staging.template.json`
- `dist/release/lesser-body-managed-live.template.json`
- `dist/release/deploy-lesser-body-from-release.sh`
- `dist/release/checksums.txt` (sha256 coverage for every published managed asset except `checksums.txt` itself)
- `dist/release/lesser-body-release.json` (canonical metadata, including MCP protocol version and deploy asset checksums)

The canonical published asset set is kept in sync by `scripts/list_release_assets.sh`. The release workflow verifies the
exact built `dist/release` directory and uploads that same asset list to GitHub.

Verify the produced release directory:

```bash
bash scripts/verify_release_assets.sh v1.2.3 dist/release
```

## GitHub Actions release

The workflow `.github/workflows/release.yml`:

- runs on tag push `v*` (or manual dispatch)
- builds release assets
- verifies the release assets from the produced release directory
- rejects the release before upload if `checksums.txt` omits any published managed asset, including `lesser-body-release.json`
- publishes a GitHub Release with the runtime zip, managed deploy templates, helper script, checksums, and metadata

See `docs/managed-deploy-contract.md` for the consumer-facing deploy contract.
