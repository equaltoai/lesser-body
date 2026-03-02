# Release Process

<!-- AI Training: Release artifacts and versioning for lesser-body -->

This repo publishes a Lambda zip artifact for `lesser-body`.

## Versioning

- Release tags use the `v*` convention (for example: `v1.0.0`).
- The Lambda can be configured with `SERVICE_VERSION` (commonly set to the release version).

## Build release assets (local)

```bash
bash scripts/build_release_assets.sh v1.2.3 dist/release
```

Outputs:

- `dist/release/lesser-body.zip`
- `dist/release/checksums.txt` (sha256)
- `dist/release/lesser-body-release.json` (metadata, including MCP protocol version)

## GitHub Actions release

The workflow `.github/workflows/release.yml`:

- runs on tag push `v*` (or manual dispatch)
- builds release assets
- publishes a GitHub Release with the zip + checksums + metadata

