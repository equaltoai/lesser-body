# Deployment

<!-- AI Training: Operator deployment workflow for lesser-body -->

`lesser-body` is an **optional plugin** for Lesser that deploys an MCP Lambda and integrates it into a Lesser instance’s
API domain as:

- `GET /.well-known/mcp.json` (public discovery)
- `GET /.well-known/oauth-protected-resource/mcp/{actor}` (public OAuth protected-resource metadata)
- `POST /mcp/{actor}` (authenticated MCP JSON-RPC)
- `GET /.well-known/oauth-protected-resource/instance/ptah/mcp` and
  `GET /.well-known/oauth-protected-resource/instance/ba/mcp` (public Ptah/Ba OAuth protected-resource metadata)
- `POST /instance/ptah/mcp` and `POST /instance/ba/mcp` (authenticated account-holder instance-plane MCP)
- `GET /instance/downloads/installer-grants/{grantId}` (Ba one-time install-pack download grants)

## Prerequisites

- A deployed Lesser app (shared stack + at least one stage stack)
- AWS credentials for the Lesser instance account
- Go `1.26+`
- Node.js `24+` (for CDK)

## What gets deployed

This repo’s CDK stack deploys:

- `lesser-body` MCP Lambda (`cmd/lesser-body`)
- `lesser-body` instance-plane Lambda (`cmd/lesser-body-instance`) for Ptah/Ba
- A standalone **Remote MCP gateway** (API Gateway REST API v1) via AppTheory CDK (`AppTheoryRemoteMcpServer`)
- (Recommended) DynamoDB session table for MCP sessions
- DynamoDB stream table for MCP streaming state
- Private S3 stream-spill bucket for large logical MCP stream events
- DynamoDB task table for future MCP task runtime state, with the `tasks` capability still disabled
- Body-owned DynamoDB tables for Ptah/Ba content, registry, one-time grants, and instance-plane MCP sessions
- SSM exports used by the Lesser stack to wire routes

Notes:

- The **canonical** client-facing endpoint in the Lesser ecosystem is `https://api.<stageDomain>/mcp/{actor}` (wired by
  the Lesser stack when `soulEnabled=true`).
- The canonical instance-plane endpoint template is `https://api.<stageDomain>/instance/{surface}/mcp` (wired by the
  Lesser stack when its instance-plane routing flag is enabled).
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
- `/<app>/<stage>/lesser-body/exports/v1/instance_mcp_lambda_arn`
- `/<app>/<stage>/lesser-body/exports/v1/instance_mcp_endpoint_url`
- `/<app>/<stage>/lesser-body/exports/v1/instance_content_table_name`
- `/<app>/<stage>/lesser-body/exports/v1/instance_registry_table_name`
- `/<app>/<stage>/lesser-body/exports/v1/instance_grant_table_name`
- `/<app>/<stage>/lesser-body/exports/v1/instance_session_table_name`

The stream-spill bucket is internal to body's AppTheory-backed MCP transport. It is not an SSM export and does not
change the public MCP endpoint contract; clients still resume by logical `Last-Event-ID`.

The MCP task table is also internal in the current release shape. When `MCP_TASK_TABLE` is configured, it backs
AppTheory's task runtime for the read-only `skill_bundle_get` task pilot and the public MCP `tasks` capability for
2025-11-25 sessions. It still does not publish an `mcp_task_table_name` SSM export.

## Deploy order (avoid the “missing SSM param” trap)

Because Lesser wires `/mcp/{actor}` and `/instance/{surface}/mcp` by importing Body Lambda ARNs from SSM, and
`lesser-body` requires Lesser's SSM exports, the safe first-time sequence is:

1) Deploy Lesser with Body routing disabled (`soulEnabled=false`; keep the Lesser-side `instancePlaneEnabled` routing
   flag off as well) so it does **not** try to import Body SSM exports yet.
2) Deploy `lesser-body` (this repo). This publishes the Ka exports plus the instance-plane exports and provisions the
   Ptah/Ba state tables.
3) Re-deploy Lesser with `soulEnabled=true` and, when enabling Ptah/Ba, `instancePlaneEnabled=true` so
   `/mcp/{actor}`, `/.well-known/mcp.json`, `/.well-known/oauth-protected-resource/mcp/{actor}`,
   `/.well-known/oauth-protected-resource/instance/ptah/mcp`,
   `/.well-known/oauth-protected-resource/instance/ba/mcp`, `/instance/ptah/mcp`, `/instance/ba/mcp`, and the Ba
   installer-grant download route proxy to Body.

If you already have the relevant Body SSM exports present for the target stage, you can deploy Lesser with the matching
routing flag(s) enabled immediately. Do not rename, delete, or manually patch the `/exports/v1/` SSM parameters; older
Lesser deployments may still read them.

## Build the Lambda artifact

The CDK app runs the build automatically, but it’s useful to know the explicit artifact build:

```bash
bash scripts/build.sh
```

Output:

- `dist/lesser-body.zip`
- `dist/lesser-body-instance.zip`

## Deploy (CDK directly)

From repo root:

```bash
cd cdk
npm ci
npx cdk deploy --all -c app=lesser -c stage=dev -c baseDomain=example.com \
  -c lesserHostInstanceKeyArn="$LESSER_HOST_INSTANCE_KEY_ARN" \
  -c soulBindingIntegrationBearerArn="$LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN"
```

Notes:

- `app` must match your Lesser app slug (the same value you deploy Lesser with).
- `stage` must be one of `dev|staging|live`.
- `baseDomain` is used to compute the public MCP endpoint template (`https://api.<stageDomain>/mcp/{actor}`) at synth
  time.
- `lesserHostInstanceKeyArn` is optional but recommended for managed instances. If omitted, lesser-body still grants the
  managed `lesser-host/<stage>/instances/<app>/instance-key*` secret namespace and can fall back to `TRUST_CONFIG`.
- `soulBindingIntegrationBearerArn` is the exact Secrets Manager ARN for the dedicated Body/Ptah → Lesser
  soul-binding bearer used by `agent_bind_soul`. It must match Lesser's receiving-side
  `SOUL_BINDING_INTEGRATION_KEY_ARN` secret value and must not be a raw bearer value.

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
  --lesser-host-instance-key-arn "$LESSER_HOST_INSTANCE_KEY_ARN" \
  --soul-binding-integration-bearer-secret-arn "$LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN"
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
- `--soul-binding-integration-bearer-secret-arn` is required before rolling out either
  `agent_local_install_plan` or `agent_bind_soul`; both tools fail closed with `not_configured` when Body cannot resolve
  the bearer. Create and populate the dedicated secret before deploying Body, then pass its exact Secrets Manager ARN
  (or set `LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN`) so the Lambda environment and secret-read IAM land in the same
  rollout. Never pass the raw bearer.
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
aws ssm get-parameter --name "/<app>/<stage>/lesser-body/exports/v1/instance_mcp_lambda_arn"
aws ssm get-parameter --name "/<app>/<stage>/lesser-body/exports/v1/instance_mcp_endpoint_url"
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

When Lesser is also deployed with `instancePlaneEnabled=true`, verify the AppTheory/RFC 9728-backed Ptah/Ba metadata
before invoking instance tools:

```bash
curl -sS "https://api.<stageDomain>/.well-known/oauth-protected-resource/instance/ptah/mcp" | jq .
curl -sS "https://api.<stageDomain>/.well-known/oauth-protected-resource/instance/ba/mcp" | jq .
```

Expected instance protected-resource fields are the same `resource`, `authorization_servers`, `scopes_supported`, and
`bearer_methods_supported` fields. `resource` must match the exact instance endpoint
(`https://api.<stageDomain>/instance/ptah/mcp` or `https://api.<stageDomain>/instance/ba/mcp`), and scopes should remain
the public Lesser OAuth catalog (`read`, `write`, `follow`, `push`). `authorization_servers` must contain the exact
`issuer` returned by `https://api.<stageDomain>/.well-known/oauth-authorization-server`; do not substitute the MCP
resource origin when the issuer uses a different host.

After metadata verification, use an account-holder OAuth token with an audience for the exact instance resource URL,
send MCP `initialize`, and then call authenticated `tools/list` on the Ptah or Ba endpoint. Do not synthesize local
OAuth metadata, skip AppTheory initialization, or reuse actor-scoped `/mcp/{actor}` tokens against instance resources.

For hosted soul/body binding rollout, verify the instance Lambda configuration without printing secret values:

```bash
aws lambda get-function-configuration \
  --function-name <app>-<stage>-lesser-body-instance-mcp \
  --query 'contains(keys(Environment.Variables), `LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN`)'
```

Expected: `true`. Do not query or log the variable value. The secret value must match Lesser's
`SOUL_BINDING_INTEGRATION_KEY_ARN` receiving-side configuration.

Then verify the instance Lambda role can read only the Lesser binding username-index and managed-config leading keys
needed by runtime policy:

```bash
ROLE_NAME="$(aws lambda get-function-configuration \
  --function-name <app>-<stage>-lesser-body-instance-mcp \
  --query 'Role' --output text | awk -F/ '{print $NF}')"
aws iam list-role-policies --role-name "$ROLE_NAME"
aws iam get-role-policy \
  --role-name "$ROLE_NAME" \
  --policy-name <inline-policy-name> \
  --query 'PolicyDocument.Statement[].Condition."ForAllValues:StringLike"."dynamodb:LeadingKeys"'
```

Expected Lesser table read keys include `INSTANCE#CONFIG` and `SOUL_BODY_BINDING_USERNAME#*` and do not include
`LBMEMORY#*` for the instance handler.

Project 48 status note: the #364 Ptah/Ba lab canary evidence and M10 rollout/soak remain pending. Do not mark lab soak,
deploy-stage staging soak, or live rollout complete from the metadata checks above alone.

For host-backed communication validation in lab, run the mailbox canary with an actor-scoped OAuth token. The script
checks `identity_whoami`, `identity_lookup`, default/standard mailbox compatibility, and explicit
`email_read(view=compact)` expansion refs. It redacts credentials and prints hashes/lengths instead of message bodies:

```bash
MCP_ENDPOINT="https://api.<stageDomain>/mcp/<actor>" \
MCP_BEARER_TOKEN="<oauth-access-token>" \
EXPECTED_IDENTITY_EMAIL="<agent-local-id>.<instance-slug>@lessersoul.ai" \
LEGACY_ALIAS_EMAIL="<agent-local-id>@lessersoul.ai" \
scripts/canary_host_mailbox_mcp.py
```

Set `MAILBOX_MESSAGE_ID=<messageRef>` when the inbox has no recent email message but you have a known host mailbox
reference to validate get/content/state paths.
Set `MAILBOX_CONFIRM_MUTATIONS=true`, `CANARY_SEND_EMAIL_TO=<recipient>`, and `CANARY_CONFIRM_EMAIL_REPLY=true` only
when you explicitly want the canary to queue real `email_send` / `email_reply` messages through lesser-host.

For Article/CMS release validation after the Lesser Article contract is deployed, run the Article canary with a
write-scoped actor OAuth token. The canary creates and publishes a real canary Article, so the explicit confirmation
environment variable is required:

```bash
MCP_ENDPOINT="https://api.<stageDomain>/mcp/<actor>" \
MCP_BEARER_TOKEN="<oauth-access-token>" \
ARTICLE_CANARY_CONFIRM_PUBLISH=true \
scripts/canary_article_mcp.py
```

The output is compact for release notes and soak evidence: no credentials, draft content, rendered HTML, full tool
payloads, or raw upstream errors are printed. Optional `ARTICLE_CANARY_*` inputs can pin a slug/content or tighten
`preview_chars` / `max_output_bytes` for a particular stage run.

For production clients, prefer OAuth connector registration rather than a static bearer token in client config.
`docs/oauth-migration.md` includes the step-by-step migration sequence and compatibility notes.

## Destroy

```bash
cd cdk
npm ci
npx cdk destroy --all -c app=<app> -c stage=<stage> -c baseDomain=<baseDomain>
```
