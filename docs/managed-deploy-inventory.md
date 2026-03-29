# Managed Deploy Inventory

<!-- AI Training: Inventory of lesser-body release outputs versus managed deploy inputs -->

This document inventories what `lesser-body` releases publish today, what the managed runner currently needs in order to
deploy the stack, and which values are true deploy-time inputs versus release-time build products.

## Current release outputs

Today `bash scripts/build_release_assets.sh <version> dist/release` publishes:

- `dist/release/lesser-body.zip`
- `dist/release/checksums.txt`
- `dist/release/lesser-body-release.json`

Those assets are enough to distribute the Lambda binary itself, but they are not yet sufficient to deploy the
`lesser-body` stack without reconstructing repo-local CDK inputs.

## Current managed runner workflow

The managed runner path currently reconstructs a source checkout at deploy time:

1. Download the repo source tarball for the requested tag.
2. Extract it into a temporary working tree.
3. Run `npm ci` in the CDK directory.
4. Rebuild the Lambda artifact as part of CDK synthesis.
5. Run `cdk deploy` from that reconstructed source tree.

That means the managed path still depends on mutable source layout, a live Node install path, and repo-local synthesis
behavior instead of release-produced deploy assets.

## True deploy-time inputs

These values are chosen by the deploy consumer or target environment and should remain deploy-time inputs:

- AWS credentials, account, and region for the target Lesser instance account
- CloudFormation stack name for the `lesser-body` deployment
- Lesser app slug (`app`)
- target stage (`dev`, `staging`, or `live`)
- an artifact staging location in the target account for the Lambda zip
- optional `baseDomain` override when the deploy should not rely on Lesser's published `/<app>/<stage>/lesser/exports/v1/domain`
  SSM parameter

## Release-time build products

These are deterministic artifacts that should be produced once at release time and then consumed as immutable inputs:

- the Lambda zip for `cmd/lesser-body`
- a deploy template that can provision the stack without a source checkout
- machine-readable metadata describing the deploy asset contract
- checksums that let consumers verify every deploy asset automatically
- documentation describing the managed deploy contract and exported SSM values

## Gap summary

The current release only publishes the Lambda zip plus minimal metadata, while the managed runner still needs:

- a deployable infrastructure template
- explicit deploy parameter inventory
- a stable description of the SSM exports that the stack writes
- a no-source deployment entrypoint
- checksums and schema details for the deploy-specific artifacts

Closing that gap is what the `#91` tracker and its subissues implement.
