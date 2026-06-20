---
name: deploy-body
description: Use to walk a merged change through per-stage CDK deploy — `lab`/`dev` → `staging` → `live` — with SSM parameter-export publication, three-step first-deploy coordination with lesser when applicable, and never-timeout on CDK commands.
---

# Deploy body to a stage

After `implement-milestone` lands a PR to `main`, the change is ready to reach deployed instances. This skill is the discipline for walking a change through stages for a given `(<app>, <stage>)`, respecting the three-step deploy order with lesser (for first-time deploys), publishing SSM exports correctly, and handling release-artifact publication when applicable.

## When this skill runs

Invoke when:

- A change has merged to `main` and is ready for rollout
- An operational change needs to propagate across stages
- A security / authorization fix is ready for compressed-cadence rollout (compression authorized separately)
- A rollback to a prior Lambda version or CDK stack state is required
- A release cut is needed for managed-consumer ingestion by lesser-host's provisioning worker

## Preconditions

- **The change is merged to `main`.**
- **The deployment is identified** — `(<app>, <stage>)` matching a lesser deployment.
- **The stage sequence is planned** — typically `lab/dev → staging → live` or `lab/dev → live`.
- **The roadmap's soak criteria are documented.**
- **MCP tools healthy**, `memory_recent` first.
- **For compressed cadence**, the compression is authorized and recorded.
- **For rollback**, the target commit / Lambda version is identified; still present and undeleted.
- **For first-time deploys**, confirm the three-step order is in play and lesser-side coordination is arranged.

## The canonical deploy sequence

### Per deployment, per stage

For a given `(<app>, <stage>)`:

1. **Lab / dev** — deploy body's CDK stack.
2. **Lab soak** — evidence meets criteria.
3. **Staging** (where used) — deploy.
4. **Staging soak** — evidence meets criteria.
5. **Live** — deploy with operator authorization.
6. **Post-deploy monitoring** — active watch per declared plan.

### The CDK deploy command

Canonical:

```bash
cdk deploy \
  -c app=<slug> \
  -c stage=<stage> \
  -c baseDomain=<domain>
```

Alternative via AppTheory contract (if `app-theory/app.json` is configured):

```bash
theory app up --stage <stage>
```

The CDK stack deploys the Lambda function, the optional DynamoDB session table (when `MCP_SESSION_TABLE` is referenced), IAM roles, and **publishes SSM parameter exports** under `/<app>/<stage>/lesser-body/exports/v1/`:

- `mcp_lambda_arn`
- `mcp_endpoint_url`
- `mcp_session_table_name`

### The CDK timeout rule

**Never set timeouts on CDK deploy commands.** A deploy that feels stuck is almost always waiting on CloudFormation (Lambda update, SSM parameter mutation, IAM-role propagation), a stack rollback, or a dependency resource. Aborting leaves CloudFormation in a half-migrated state.

Run deploys to completion. Capture full output. If genuinely stuck, check CloudFormation console state through the user — don't abort.

### The three-step first-deploy order

For **first-time deploys** to a new `(<app>, <stage>)`:

1. **lesser deploys without `soulEnabled`** (handled by `lesser` steward).
2. **body deploys** (this skill's work) — publishes SSM exports.
3. **lesser deploys with `soulEnabled=true`** (handled by `lesser` steward) — reads body's SSM exports, wires API Gateway / CloudFront proxy for `/mcp/*`.

This order is required for first-time deploys. Attempting step 3 before step 2 produces a CloudFormation failure (SSM parameter not found).

For **subsequent deploys**, body and lesser update independently without re-ordering.

Before executing a body deploy, confirm which case applies:

- First-time: coordinate with `lesser` steward for steps 1 and 3.
- Subsequent: deploy independently.

## Lab / dev soak

After `cdk deploy` to lab / dev completes:

- **Verify deploy success.** CloudFormation stack reaches `UPDATE_COMPLETE` or `CREATE_COMPLETE`. Lambda function version updated. SSM parameters published with expected values.
- **Verify SSM exports.** Check `/<app>/<stage>/lesser-body/exports/v1/{mcp_lambda_arn, mcp_endpoint_url, mcp_session_table_name}` are present and correctly populated.
- **Exercise the MCP surface.** Discovery at `/.well-known/mcp.json` returns expected tool catalog. OAuth metadata at `/.well-known/oauth-protected-resource/mcp/<actor>` returns RFC 9728-compliant content. A test MCP client can authenticate and invoke a tool.
- **Watch CloudWatch for error patterns.**
- **Watch SNS error-topic messages** (where configured).
- **For tool-surface changes**, exercise each affected tool with scope and profile gates.
- **For lesser-integration changes**, exercise JWT validation, DynamoDB reads (via a tool that triggers them), lesser REST API calls.
- **For host-delegation changes**, exercise communication-tool delegation with a test message and confirm `messageId` idempotency.
- **For session persistence changes** (if enabled), invoke tools across multiple calls and confirm session state retains.
- **Soak duration** per roadmap. Non-trivial: hours to a day. MCP-contract / scope / profile changes: longer.

Do not promote to staging until lab / dev soak criteria are met.

## Staging soak (where used)

After `cdk deploy` to staging completes:

- **Verify deploy success.**
- **Integration partners exercise real MCP flows** against the staging MCP endpoint.
- **Watch for client-compatibility signals** via operator / client-maintainer channels.
- **Claude / AgentCore test configurations** connect to staging if arranged.
- **Soak duration** typically multiple days for non-trivial changes; longer for MCP-contract / scope / profile / lesser-integration changes.

Do not promote to live until staging soak criteria are met.

## Live deploy

**Live is production. Real MCP clients. Real agents. Real tool invocations.** Deploy is fast; posture is measured.

- **Operator authorizes live deploy explicitly.**
- **`cdk deploy -c app=... -c stage=live -c baseDomain=...`** is the command.
- **Post-deploy monitoring begins immediately. Watch:**
  - CloudWatch error rate for `lesser-body` Lambda
  - JWT validation failure rate
  - Scope / profile rejection rate (should be stable — spikes signal)
  - MCP invocation success rate by tool
  - Session-table capacity (if used)
  - SNS error-topic messages
  - DynamoDB read-capacity consumption against lesser's table
  - Lesser REST API call latency / failure rate
  - Lesser-host comm-API delegation success rate (communication tools)
- **Watch window** varies. Narrow fix: minutes to hours. MCP-contract / scope / profile changes: days.

## Release-artifact publication (for managed-consumer ingestion)

When a release will be ingested by lesser-host's provisioning worker, the release cut requires:

- **Git tag** on `main` — `v<version>` (e.g. `v0.2.29`)
- **Release manifest** — version, commit SHA, stack list, timestamps
- **Lambda bundle** — compiled Go binary (`dist/lesser-body.zip` from `scripts/build.sh`)
- **Checksums** — SHA256 per asset
- **GitHub Release** at `equaltoai/lesser-body/releases/tag/v<version>`
- **Release notes** — breaking changes, migration guidance, tool-surface changes, MCP-contract changes, SSM-export changes, lesser-integration changes

Managed consumers (lesser-host's provisioning worker) verify checksums before deploying. Breaking the release-artifact shape coordinates with the `host` steward.

## If a stage surfaces a regression

- **Stop.** Do not promote further.
- **Diagnose quickly** — narrow or broad?
- **Decide rollback scope**:
  - **Full rollback**: revert the commit on `main`, redeploy via CDK with prior commit.
  - **Per-stage rollback**: roll back live while keeping staging / dev on the new commit.
  - **Lambda-version alias rollback** (emergency): point alias at prior Lambda version directly.
- **Coordinate with operators through the user.**
- **For SSM export regressions**: rollback re-publishes prior export values. Low risk because lesser reads SSM at deploy time, not continuously.
- **For session-table schema regressions**: plan data remediation if in-flight session data is affected.
- **Never delete the regressed Lambda function version.** Immutable audit history.
- **Never delete the CloudFormation stack.**
- **Record the regression.** High-signal memory material.

## Output: the deploy record

```markdown
## Deploy record: <change name>

### Deployment
- App: <slug>
- Stage: <stage>
- AWS profile / account: <identified>
- Operator: <identified>

### Deploy type
- First-time: <yes / no>
- Three-step order required: <yes — coordinated with lesser / no — subsequent deploy>

### Lab / dev
- Command: `cdk deploy -c app=... -c stage=lab -c baseDomain=...`
- Timestamp: <...>
- Lambda version updated: <...>
- CloudFormation stack ID: <...>
- SSM exports published: `/<app>/<stage>/lesser-body/exports/v1/{mcp_lambda_arn, mcp_endpoint_url, mcp_session_table_name}` — values verified
- Soak criteria met: <...>
- Soak duration: <...>
- Issues observed: <none / described>

### Staging (if used)
- Command: `cdk deploy -c app=... -c stage=staging ...`
- Timestamp: <...>
- Lambda version: <...>
- Soak criteria met: <...>
- Soak duration: <...>
- Issues observed: <none / described>

### Live
- Authorized by: <operator>
- Command: `cdk deploy -c app=... -c stage=live ...`
- Timestamp: <...>
- Lambda version: <...>
- Post-deploy monitoring window: <...>
- Issues observed: <none / described>

### Release artifacts (if cut)
- Git tag: <v<version>>
- GitHub Release URL: <...>
- Assets: release manifest, lambda bundle, checksums
- Release notes: <summary>
- Managed-consumer (lesser-host) coordination: <completed / n/a>

### Rollback (if any)
- Trigger: <...>
- Mechanism: <revert + redeploy / alias rollback / stack rollback>
- Prior commit / version: <...>
- SSM-export rollback: <prior values re-published>
- Session-table data remediation: <none / described>
- Operator coordination: <...>

### Follow-ups
- <subsequent scoping, fix, or monitoring task>
```

## Refusal cases

- **"Set a 10-minute timeout on the CDK deploy."** Never.
- **"Skip publishing the SSM exports this deploy; they haven't changed."** Refuse. SSM exports publish on every deploy so the values reflect the current Lambda version and session table.
- **"Run the live deploy without operator authorization."** Refuse.
- **"Skip lab / dev soak; the change is small."** Refuse.
- **"Deploy to live without a staging soak" (where staging is used).** Refuse without explicit authorization.
- **"Delete this Lambda function version; we're past it."** Refuse. Rollback target.
- **"Delete the old SSM exports under `/v1/`; they're cluttering."** Refuse. lesser reads them.
- **"Deploy body before lesser for a first-time deploy."** Refuse. Unsouled lesser first, then body, then soul-enabled lesser.
- **"Modify SSM exports manually to patch a value."** Refuse unless operationally authorized with documented reason. Values come from CDK.
- **"Abort the CDK deploy; it's been running too long."** Check CloudFormation console state first through the user.

## Persist

Append when the deploy surfaces something worth remembering — a CDK quirk, SSM mutation timing observation, soul-enabled wiring edge case, session-table migration subtlety, operator-reporting pattern. Routine clean deploys aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **All stages clean, release cut if applicable** — stop. Record deploy, append memory if warranted.
- **Regression surfaced** — coordinate rollback, then `investigate-issue`.
- **Deploy-specific anomaly during soak** — route through `investigate-issue` or specialist skill.
- **Operator notification needed post-live** — surface to user.
- **Managed-consumer (lesser-host) coordination issue surfaced** — report cross-repo to the `host` steward via the user.
- **Deploy reveals a scoping question** — `scope-need` once current deploy stable.