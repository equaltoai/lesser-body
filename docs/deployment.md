# Deployment

<!-- AI Training: Operator deployment workflow for lesser-body -->

`lesser-body` is an **optional plugin** for Lesser that deploys an MCP Lambda and integrates it into a Lesser instance’s
API domain as:

- `GET /.well-known/mcp.json` (public discovery)
- `GET /.well-known/oauth-protected-resource/mcp/{actor}` (public OAuth protected-resource metadata)
- `POST /mcp/{actor}` (authenticated MCP JSON-RPC)

## Prerequisites

- A deployed Lesser app (shared stack + at least one stage stack)
- AWS credentials for the Lesser instance account
- Go `1.26+`
- Node.js `24+` (for CDK)

## What gets deployed

This repo’s CDK stack deploys:

- `lesser-body` MCP Lambda (`cmd/lesser-body`)
- A standalone **Remote MCP gateway** (API Gateway REST API v1) via AppTheory CDK (`AppTheoryRemoteMcpServer`)
- (Recommended) DynamoDB session table for MCP sessions
- DynamoDB stream table for MCP streaming state
- Private S3 stream-spill bucket for large logical MCP stream events
- DynamoDB task table for future MCP task runtime state, with the `tasks` capability still disabled
- SSM exports used by the Lesser stack to wire routes

Notes:

- The **canonical** client-facing endpoint in the Lesser ecosystem is `https://api.<stageDomain>/mcp/{actor}` (wired by
  the Lesser stack when `soulEnabled=true`).
- The standalone execute-api endpoint exists as part of the current `lesser-body` stack implementation; treat it as an
  implementation detail unless you are intentionally using it for isolated testing.

## Dependency contract (SSM)

`lesser-body` expects these to already exist (published by Lesser):

- `/<app>/shared/secrets/jwt-secret-arn`
- `/<app>/<stage>/lesser/exports/v1/table_name`
- `/<app>/<stage>/lesser/exports/v1/domain`

And it publishes these (consumed by Lesser when `soulEnabled=true`):

- `/<app>/<stage>/lesser-body/exports/v1/mcp_lambda_arn`
- `/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url`
- `/<app>/<stage>/lesser-body/exports/v1/mcp_session_table_name` (when session table is enabled)
- `/<app>/<stage>/lesser-body/exports/v1/mcp_stream_table_name`

The stream-spill bucket is internal to body's AppTheory-backed MCP transport. It is not an SSM export and does not
change the public MCP endpoint contract; clients still resume by logical `Last-Event-ID`.

The MCP task table is also internal in the current release shape. When `MCP_TASK_TABLE` is configured, it backs
AppTheory's task runtime for the read-only `skill_bundle_get` task pilot and the public MCP `tasks` capability for
2025-11-25 sessions. It still does not publish an `mcp_task_table_name` SSM export.

## Deploy order (avoid the “missing SSM param” trap)

Because Lesser wires `/mcp/{actor}` by importing `mcp_lambda_arn` from SSM, and `lesser-body` requires Lesser’s SSM exports, the
safe sequence is:

1) Deploy Lesser with `soulEnabled=false` (so it does **not** try to import lesser-body yet)
2) Deploy `lesser-body` (this repo)
3) Re-deploy Lesser with `soulEnabled=true` (so `/mcp/{actor}`, `/.well-known/mcp.json`, and
   `/.well-known/oauth-protected-resource/mcp/{actor}` route to the lesser-body Lambda)

If you already have `mcp_lambda_arn` present for the target stage, you can deploy Lesser with `soulEnabled=true`
immediately.

## Build the Lambda artifact

The CDK app runs the build automatically, but it’s useful to know the explicit artifact build:

```bash
bash scripts/build.sh
```

Output:

- `dist/lesser-body.zip`

## Deploy (CDK directly)

From repo root:

```bash
cd cdk
npm ci
npx cdk deploy --all -c app=lesser -c stage=dev -c baseDomain=example.com \
  -c lesserHostInstanceKeyArn="$LESSER_HOST_INSTANCE_KEY_ARN"
```

Notes:

- `app` must match your Lesser app slug (the same value you deploy Lesser with).
- `stage` must be one of `dev|staging|live`.
- `baseDomain` is used to compute the public MCP endpoint template (`https://api.<stageDomain>/mcp/{actor}`) at synth
  time.
- `lesserHostInstanceKeyArn` is optional but recommended for managed instances. If omitted, lesser-body still grants the
  managed `lesser-host/<stage>/instances/<app>/instance-key*` secret namespace and can fall back to `TRUST_CONFIG`.

## Deploy (via `theory app up`)

If you use the Theory deployment contract in `app-theory/app.json`:

```bash
theory app up --aws-profile <profile> --stage dev
```

The contract runs CDK deterministically (`npm ci`) and passes `-c stage=<stage>` to CDK.

## Deploy (from release assets, no source checkout)

Managed consumers can deploy from release assets without a repo tarball or `npm ci`.

Required release assets:

- `lesser-body.zip`
- `lesser-body-deploy.json`
- `lesser-body-managed-<stage>.template.json`
- `deploy-lesser-body-from-release.sh`
- `checksums.txt`
- `lesser-body-release.json`

Example:

```bash
bash ./deploy-lesser-body-from-release.sh \
  --stack-name lesser-dev-lesser-body \
  --asset-bucket my-artifact-bucket \
  --app lesser \
  --stage dev \
  --base-domain example.com \
  --lesser-host-instance-key-arn "$LESSER_HOST_INSTANCE_KEY_ARN"
```

Notes:

- `--stage` selects the matching stage-specific release template (`dev`, `staging`, or `live`).
- `--asset-bucket` must be writable in the target account because the helper stages `lesser-body.zip` there before
  calling CloudFormation. The helper also passes `--s3-bucket <assetBucket>` to `aws cloudformation deploy` so managed
  templates can exceed the AWS CLI local-template limit (51,200 bytes).
- `--base-domain` is optional. When omitted, the template resolves `/<app>/<stage>/lesser/exports/v1/domain` from SSM.
- The helper derives the managed template's SSM path parameters from `--app` plus `--stage` and passes them as explicit
  string overrides. The templates no longer rely on intrinsic expressions in parameter defaults.
- `--lesser-host-instance-key-arn` is optional. If omitted, the helper also checks the shell environment for
  `LESSER_HOST_INSTANCE_KEY_ARN` and forwards it automatically when present.
- Add `--no-execute-changeset` to exercise the real `aws cloudformation deploy` path without executing the change set.
- The corrected MCP stream-table baseline is a versioned physical table (`...-mcp-streams-v2`) while the exported SSM
  parameter name remains `mcp_stream_table_name`. Existing durable Lesser actor data is preserved; only transient MCP
  session/stream state may reset during the update.
- The MCP task table baseline is `...-mcp-tasks` with a 10-minute TTL. It is transient state for the read-only
  `skill_bundle_get` task pilot and remains outside the managed SSM export contract until there is a concrete
  lesser/host consumer.

See `docs/managed-deploy-contract.md` for the full release contract.

## Verify (SSM exports)

After deploy, confirm exports exist:

```bash
aws ssm get-parameter --name "/<app>/<stage>/lesser-body/exports/v1/mcp_lambda_arn"
aws ssm get-parameter --name "/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url"
```

Do not expect an `mcp_task_table_name` export in this phase; the task table remains an internal AppTheory runtime asset
while the public MCP `tasks` capability is disabled.

## Verify (HTTP)

Once Lesser is deployed with `soulEnabled=true`, verify both public discovery docs:

```bash
curl -sS "https://api.<stageDomain>/.well-known/mcp.json" | jq .
curl -sS "https://api.<stageDomain>/.well-known/oauth-protected-resource/mcp/Arch" | jq .
curl -sS "https://api.<stageDomain>/.well-known/oauth-authorization-server" | jq .
```

Expected protected-resource fields:

- `resource`
- `authorization_servers`
- `scopes_supported`
- `bearer_methods_supported`

For browser-based MCP clients, also verify CORS on discovery:

```bash
curl -sSI \
  -H "Origin: https://claude.ai" \
  "https://api.<stageDomain>/.well-known/oauth-protected-resource/mcp/Arch"
```

Expected headers include:

- `access-control-allow-origin: https://claude.ai`
- `vary: origin`

MCP calls require auth. See `docs/mcp.md` for examples and auth expectations.

For host-backed communication validation in lab, run the mailbox canary with an actor-scoped OAuth token. The script
checks default/standard mailbox compatibility plus explicit `email_read(view=compact)` expansion refs. It redacts
credentials and prints hashes/lengths instead of message bodies:

```bash
MCP_ENDPOINT="https://api.<stageDomain>/mcp/<actor>" \
MCP_BEARER_TOKEN="<oauth-access-token>" \
scripts/canary_host_mailbox_mcp.py
```

Set `MAILBOX_MESSAGE_ID=<messageRef>` when the inbox has no recent email message but you have a known host mailbox
reference to validate get/content/state paths.

For production clients, prefer OAuth connector registration rather than a static bearer token in client config.
`docs/oauth-migration.md` includes the step-by-step migration sequence and compatibility notes.

## Destroy

```bash
cd cdk
npm ci
npx cdk destroy --all -c app=<app> -c stage=<stage> -c baseDomain=<baseDomain>
```
