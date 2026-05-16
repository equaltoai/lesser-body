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
- any auxiliary release assets declared in `lesser-body-deploy.json` `auxiliary_assets[]` (for example the
  AppTheory S3 auto-delete custom resource provider zip used by stream-spill buckets)

The canonical base asset set is kept in sync by `scripts/list_release_assets.sh`. After a release directory is built,
`scripts/list_release_assets.sh dist/release` expands that list with manifest-declared auxiliary assets. The release
workflow verifies the exact built `dist/release` directory and uploads that same manifest-expanded asset list to GitHub.

The managed templates now use only CloudFormation-legal plain-string parameter defaults. Stage-specific SSM lookup paths
are carried through explicit string parameters instead of intrinsic defaults.

The current managed-template baseline also pins the named MCP DynamoDB resources:

- session table logical ID: `McpServerSessionTable469EA0FB`
- stream table logical ID: `McpServerStreamTableC6A2DC7E`
- task table logical ID: `McpServerTaskTable72DDFBBB`
- stream table physical-name suffix: `mcp-streams-v2`
- task table physical-name suffix: `mcp-tasks`

That stream-table reset is a one-time lab-era infrastructure correction. The table stores transient MCP stream/session
transport state, so active sessions may reset during rollout, but durable Lesser actor data remains in Lesser's own
table.

The task table is transient MCP runtime-readiness state. Managed templates inject `MCP_TASK_TABLE` and
`MCP_TASK_TTL_MINUTES=10`, but body does not advertise or serve MCP `tasks` until a later task-runtime milestone wires
an explicit AppTheory task runtime and task-capable read-only tool.

Verify the produced release directory:

```bash
bash scripts/verify_release_assets.sh v1.2.3 dist/release
```

Exercise the named-resource regression harness locally:

```bash
bash scripts/check_managed_template_named_resource_regression.sh v1.2.3 dist/release
```

Exercise the auxiliary-asset regression harness locally:

```bash
bash scripts/check_managed_auxiliary_asset_regression.sh v1.2.3 dist/release
```

Verify the exact published GitHub release assets:

```bash
bash scripts/verify_published_release_assets.sh --version v1.2.3
```

If you have AWS credentials for a target account and want to exercise the real deploy CLI surface against the published
assets, pass stack inputs as well:

```bash
bash scripts/verify_published_release_assets.sh \
  --version v1.2.3 \
  --stack-name lesser-dev-lesser-body \
  --asset-bucket my-artifact-bucket \
  --stage dev \
  --app lesser \
  --base-domain example.com
```

That runs the downloaded `deploy-lesser-body-from-release.sh` helper with `--no-execute-changeset`.

Note: the helper always passes `--s3-bucket <assetBucket>` to `aws cloudformation deploy` so managed templates can grow
beyond the AWS CLI local-template size limit (51,200 bytes).
If `LESSER_HOST_INSTANCE_KEY_ARN` is present in the shell environment, the helper now forwards it into the managed
template automatically so the deploy can inject the exact outbound comm secret ARN as well as the fallback namespace
grants.

## GitHub Actions release

The workflow `.github/workflows/release.yml`:

- runs on tag push `v*` (or manual dispatch)
- builds release assets
- verifies the release assets from the produced release directory
- rejects managed templates with non-string CloudFormation parameter defaults before publish
- rejects managed templates that drift the pinned MCP table logical IDs or the `mcp-streams-v2` stream-table baseline
- rejects managed templates that drop AppTheory's stream-spill bucket wiring while stream storage is enabled
- rejects managed templates that reference hidden CDK bootstrap assets instead of manifest-declared staged assets
- rejects the release before upload if `checksums.txt` omits any published managed asset, including `lesser-body-release.json`
- publishes a GitHub Release with the runtime zip, managed deploy templates, helper script, checksums, metadata, and any
  manifest-declared auxiliary assets

See `docs/managed-deploy-contract.md` for the consumer-facing deploy contract.
