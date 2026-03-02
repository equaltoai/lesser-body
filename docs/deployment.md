# Deployment

<!-- AI Training: Operator deployment workflow for lesser-body -->

`lesser-body` is an **optional plugin** for Lesser that deploys an MCP Lambda and integrates it into a Lesser instance’s
API domain as:

- `GET /.well-known/mcp.json` (public discovery)
- `POST /mcp` (authenticated MCP JSON-RPC)

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
- SSM exports used by the Lesser stack to wire routes

Notes:

- The **canonical** client-facing endpoint in the Lesser ecosystem is `https://api.<stageDomain>/mcp` (wired by the
  Lesser stack when `soulEnabled=true`).
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

## Deploy order (avoid the “missing SSM param” trap)

Because Lesser wires `/mcp` by importing `mcp_lambda_arn` from SSM, and `lesser-body` requires Lesser’s SSM exports, the
safe sequence is:

1) Deploy Lesser with `soulEnabled=false` (so it does **not** try to import lesser-body yet)
2) Deploy `lesser-body` (this repo)
3) Re-deploy Lesser with `soulEnabled=true` (so `/mcp` and `/.well-known/mcp.json` route to the lesser-body Lambda)

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
npx cdk deploy --all -c app=lesser -c stage=dev -c baseDomain=example.com
```

Notes:

- `app` must match your Lesser app slug (the same value you deploy Lesser with).
- `stage` must be one of `dev|staging|live`.
- `baseDomain` is used to compute the public MCP endpoint (`https://api.<stageDomain>/mcp`) at synth time.

## Deploy (via `theory app up`)

If you use the Theory deployment contract in `app-theory/app.json`:

```bash
theory app up --aws-profile <profile> --stage dev
```

The contract runs CDK deterministically (`npm ci`) and passes `-c stage=<stage>` to CDK.

## Verify (SSM exports)

After deploy, confirm exports exist:

```bash
aws ssm get-parameter --name "/<app>/<stage>/lesser-body/exports/v1/mcp_lambda_arn"
aws ssm get-parameter --name "/<app>/<stage>/lesser-body/exports/v1/mcp_endpoint_url"
```

## Verify (HTTP)

Once Lesser is deployed with `soulEnabled=true`, verify the public discovery doc:

```bash
curl -sS "https://api.<stageDomain>/.well-known/mcp.json" | jq .
```

MCP calls require auth. See `docs/mcp.md` for examples and auth expectations.

## Destroy

```bash
cd cdk
npm ci
npx cdk destroy --all -c app=<app> -c stage=<stage> -c baseDomain=<baseDomain>
```
