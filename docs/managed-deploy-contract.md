# Managed Deploy Contract

<!-- AI Training: Canonical managed deploy contract for lesser-body release assets -->

This document defines what a `lesser-body` release publishes for managed deploy consumers and what those consumers can
rely on when deploying from release assets instead of a source checkout.

## Release-produced deploy assets

Every `lesser-body` release now publishes these managed deploy assets:

- `lesser-body.zip`
- `lesser-body-deploy.json`
- `lesser-body-managed-dev.template.json`
- `lesser-body-managed-staging.template.json`
- `lesser-body-managed-live.template.json`
- `deploy-lesser-body-from-release.sh`
- `checksums.txt`
- `lesser-body-release.json`

The canonical manifest is `lesser-body-release.json`. It contains per-asset paths, sizes, and sha256 values so consumers
can discover and verify the deploy assets automatically.

`lesser-body-release.json` is a checksum-covered published asset. It is not unsigned metadata that happens to ship
alongside the release.

`checksums.txt` is the checksum root for the published asset set. It must include every other published managed asset,
including `lesser-body-release.json`.

`lesser-body-deploy.json` is the deploy-specific contract. It describes:

- which stage-specific CloudFormation templates are published
- which script inputs a managed consumer must provide
- which CloudFormation parameters the templates expect
- which `lesser-body` SSM exports are written on successful deploy

## Managed deploy inputs

Managed consumers must still choose these deploy-time values:

- CloudFormation stack name
- target stage (`dev`, `staging`, or `live`)
- Lesser app slug (`app`)
- artifact staging bucket in the target account (also used to upload CloudFormation templates for large-template deploys)
- optional artifact key prefix
- optional `baseDomain` override

Everything else needed for the deploy path is release-produced.

## No-source deploy path

The managed deploy helper uses only release assets:

```bash
bash ./deploy-lesser-body-from-release.sh \
  --stack-name lesser-dev-lesser-body \
  --asset-bucket my-artifact-bucket \
  --app lesser \
  --stage dev \
  --base-domain example.com
```

What the helper does:

1. Upload `lesser-body.zip` to the requested S3 bucket and prefix.
2. Select the stage-specific CloudFormation template for `dev`, `staging`, or `live`.
3. Deploy the stack with `aws cloudformation deploy --s3-bucket <assetBucket>` so templates can grow beyond the AWS CLI
   local-template size limit (51,200 bytes).

This path does not require:

- a repo source tarball
- `npm ci`
- CDK synthesis in the deploy environment

## Template contract

Each stage-specific template accepts these CloudFormation parameters:

- `AppName`
- `BaseDomain`
- `LesserBodyCodeBucketName`
- `LesserBodyCodeObjectKey`
- `JWTSecretArnParamPath`
- `JWTSecretKeyArnParamPath`
- `LesserStageDomainParamPath`
- `LesserTableNameParamPath`

Template behavior:

- `AppName` defaults to `lesser`.
- `BaseDomain` is optional.
- Every parameter `Default` emitted in the managed templates is a plain string. Intrinsics and object-valued defaults are not
  CloudFormation-legal for this deploy path.
- `JWTSecretArnParamPath` defaults to `/<app>/shared/secrets/jwt-secret-arn`.
- `JWTSecretKeyArnParamPath` defaults to `/<app>/shared/kms/encryption-key-arn`.
- `LesserStageDomainParamPath` defaults to `/<app>/<stage>/lesser/exports/v1/domain`.
- `LesserTableNameParamPath` defaults to `/<app>/<stage>/lesser/exports/v1/table_name`.
- When `BaseDomain` is empty, the stack resolves `/<app>/<stage>/lesser/exports/v1/domain` from SSM.
- The Lambda code location is always provided from the staged release asset in S3.
- The release helper derives the SSM path parameters from `--app` plus `--stage` and passes them explicitly as legal
  string parameter overrides.

## Published exports

On deploy, `lesser-body` writes these SSM parameters:

- `/<app>/<stage>/lesser-body/exports/v1/mcp_lambda_arn`
- `/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url`
- `/<app>/<stage>/lesser-body/exports/v1/mcp_session_table_name`
- `/<app>/<stage>/lesser-body/exports/v1/mcp_stream_table_name`

Managed consumers can rely on those names as the stable exported surface of the `lesser-body` stack.

## MCP-facing expectations

The release-produced templates preserve the existing MCP endpoint contract:

- public MCP endpoint template: `https://api.<stageDomain>/mcp/{actor}`
- public discovery doc: `GET /.well-known/mcp.json`
- public protected-resource metadata: `GET /.well-known/oauth-protected-resource/mcp/{actor}`

After deploying `lesser-body`, Lesser can continue wiring the canonical public routes using the exported Lambda ARN and
endpoint metadata.

## Verification rules

Managed consumers should verify release assets in this order:

1. Confirm the published asset set is present: `lesser-body.zip`, `lesser-body-deploy.json`, the three stage-specific
   templates, `deploy-lesser-body-from-release.sh`, `checksums.txt`, and `lesser-body-release.json`.
2. Check that `checksums.txt` includes every published managed asset except `checksums.txt` itself.
3. Run `sha256sum -c checksums.txt`.
4. Check `lesser-body-release.json` for the expected deploy asset paths and sha256 values.
5. Use `lesser-body-deploy.json` to confirm the stage template, helper script, and exported SSM contract.

Repo-local verification is implemented by:

```bash
bash scripts/verify_release_assets.sh v1.2.3 dist/release
```

Exact published-release verification is implemented by:

```bash
bash scripts/verify_published_release_assets.sh --version v1.2.3
```

That verification fails if:

- a required deploy asset is missing
- `checksums.txt` omits any published managed asset, including `lesser-body-release.json`
- a checksum does not match
- a managed template contains a non-string `Parameters.*.Default`
- a template regresses to CDK asset/bootstrap assumptions
- the helper script no longer operates purely from the produced release directory

Managed consumers should reject the release before trusting `lesser-body-release.json` or `lesser-body-deploy.json` if
checksum coverage is incomplete. The repo-local verifier surfaces that failure as:

```text
checksums.txt is missing published managed asset: lesser-body-release.json
```
