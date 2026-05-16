# Managed Deploy Inventory

<!-- AI Training: Inventory of lesser-body release outputs versus managed deploy inputs -->

This document records both:

- the historical release/deploy gap identified in `#92`
- the current managed deploy asset set and consumer inputs after `#91` and `#98`

Use `docs/managed-deploy-contract.md` as the canonical consumer-facing contract. This inventory exists to show how the
current release model differs from the earlier source-tarball workflow.

## Current release outputs

Today `bash scripts/build_release_assets.sh <version> dist/release` publishes:

- `dist/release/lesser-body.zip`
- `dist/release/lesser-body-deploy.json`
- `dist/release/lesser-body-managed-dev.template.json`
- `dist/release/lesser-body-managed-staging.template.json`
- `dist/release/lesser-body-managed-live.template.json`
- `dist/release/deploy-lesser-body-from-release.sh`
- `dist/release/checksums.txt`
- `dist/release/lesser-body-release.json`

For AppTheory v1.5.0 and later, managed releases may also publish auxiliary assets declared in
`lesser-body-deploy.json` `auxiliary_assets[]`. They are checksum-covered like every other managed release artifact and
may be flat or nested relative paths; the current Body release builder emits the AppTheory S3 auto-delete custom resource
provider for the MCP stream-spill bucket as a flat
`apptheory-s3-auto-delete-objects-provider-<cdk-source-hash>.zip` asset.

Those assets are sufficient for managed consumers to deploy `lesser-body` without reconstructing a source checkout.
`checksums.txt` checksum-covers every published managed asset except `checksums.txt` itself, including the canonical
`lesser-body-release.json` manifest.

## Current managed deploy workflow

The managed deploy path now consumes release-produced assets directly:

1. Download the published release assets for the requested tag.
2. Verify the published asset set with `checksums.txt`, `lesser-body-release.json`, and `lesser-body-deploy.json`.
3. Upload `lesser-body.zip` and every required `auxiliary_assets[]` file to the target account's staging bucket.
4. Deploy the stage-specific CloudFormation template with `deploy-lesser-body-from-release.sh` or equivalent consumer logic,
   passing every derived auxiliary asset object-key parameter.

That path does not require a repo tarball, `npm ci`, or CDK synthesis in the deploy environment.

## Historical gap before #91

Before the immutable deploy work landed, the managed runner reconstructed a source checkout at deploy time:

1. Download the repo source tarball for the requested tag.
2. Extract it into a temporary working tree.
3. Run `npm ci` in the CDK directory.
4. Rebuild the Lambda artifact as part of CDK synthesis.
5. Run `cdk deploy` from that reconstructed source tree.

That historical path depended on mutable source layout, a live Node install path, and repo-local synthesis behavior.

## True deploy-time inputs

These values are chosen by the deploy consumer or target environment and remain deploy-time inputs:

- AWS credentials, account, and region for the target Lesser instance account
- CloudFormation stack name for the `lesser-body` deployment
- Lesser app slug (`app`)
- target stage (`dev`, `staging`, or `live`)
- an artifact staging location in the target account for the Lambda zip
- optional `baseDomain` override when the deploy should not rely on Lesser's published `/<app>/<stage>/lesser/exports/v1/domain`
  SSM parameter

## Release-time build products

These are deterministic artifacts produced once at release time and then consumed as immutable inputs:

- the Lambda zip for `cmd/lesser-body`
- auxiliary AppTheory/CDK file assets required by release-produced templates
- stage-specific deploy templates that can provision the stack without a source checkout
- AppTheory Remote MCP storage assets, including the session table, durable stream table/spill bucket, and the internal
  task table prepared for a future task runtime
- machine-readable metadata describing the deploy asset contract
- checksums that let consumers verify every published deploy asset automatically
- documentation describing the managed deploy contract and exported SSM values

## Gap summary

The original `#92` gap was that releases only published the Lambda zip plus minimal metadata, while the managed runner
still needed:

- a deployable infrastructure template
- explicit deploy parameter inventory
- a stable description of the SSM exports that the stack writes
- a no-source deployment entrypoint
- checksums and schema details for the deploy-specific artifacts

The managed template now also pins the internal MCP task-table baseline (`McpServerTaskTable72DDFBBB`,
`...-mcp-tasks`, `MCP_TASK_TTL_MINUTES=10`). That table is a release-time infrastructure artifact but not a new
managed-consumer input or SSM export while the MCP `tasks` capability remains disabled.

That gap was closed by `#91`, and `#98` tightened the remaining producer-side blind spots by requiring checksum coverage
for `lesser-body-release.json` and verifying the checksum-root descriptor embedded in the canonical manifest.
