# Release, branch, and stage discipline

body uses a **single-main branch model** with feature branches and **CDK-driven deployment**. Each deployment is parameterized by `<app>` (matching lesser's `<slug>`) and `<stage>` (matching lesser's stage), and runs alongside a lesser instance in the same AWS account.

## Branch model

Observed pattern:

- **`main`** — canonical, mainline. Every merge lands here. No staging or premain branch.
- **Feature branches**:
  - `aron/issue-<N>-<topic>` — Aron-driven topic work (e.g. `aron/issue-36-discovery-token-passthrough`, `aron/issue-58-per-actor-mcp-bundles`)
  - `codex/<topic>` — codex-driven exploration / milestone work (e.g. `codex/apptheory-v0.22.0-mcp-upgrade`, `codex/drones-runtime-boundaries`)
  - `chore/<maintenance>` — dependency bumps, CI bumps, toolchain maintenance
- **Release tags** — `v<major>.<minor>.<patch>` (e.g. `v0.2.29`). Tags cut at merges on `main`.

Branch protection on `main` enforces required review and status checks.

## The three stages (per `<app>`)

body deploys to the same stages lesser does, per the same `<app>`:

- **`lab` / `dev`** — development integration. `dev.<base-domain>` subdomain behavior.
- **`staging`** (optional) — sandbox / integration-partner validation.
- **`live`** — production. Real agents connecting over MCP.

Stage naming convention follows lesser's conventions for the same `<app>`. Where lesser uses `paytheorylab` / `paytheory` / `paytheorystudy` for a given partner, body's stage matches.

## The three-step deploy contract with lesser

For **first-time deploys** to a new `(<app>, <stage>)`, the deploy order is:

1. **Deploy lesser without `soulEnabled`.** lesser's API Gateway / CloudFront come up without a proxy for `/mcp/*`.
2. **Deploy body.** body's CDK stack deploys the Lambda, the optional DynamoDB session table (if `MCP_SESSION_TABLE` is configured), and publishes SSM parameter exports at stable names under `/<app>/<stage>/lesser-body/exports/v1/`:
   - `mcp_lambda_arn`
   - `mcp_endpoint_url`
   - `mcp_session_table_name`
3. **Deploy lesser with `soulEnabled=true`.** lesser's CDK reads body's SSM exports and wires API Gateway / CloudFront to proxy `/mcp/*` into body's Lambda Function URL.

**Never alter this order for first-time deploys.** Attempting to deploy lesser with `soulEnabled=true` before body's exports are published produces a CloudFormation failure (SSM parameter not found).

For **subsequent deploys** to an existing `(<app>, <stage>)`, body can be deployed independently of lesser — each service updates its own stack without re-ordering. The SSM contract remains stable.

## The CDK deploy command

Canonical deploy:

```bash
cdk deploy -c app=<slug> -c stage=<stage> -c baseDomain=<domain>
```

Alternative via AppTheory `theory` CLI contract (`app-theory/app.json`):

```bash
theory app up --stage <stage>
```

Deploys the body CDK stack, which includes:

- The Lambda function (`lesser-body` runtime)
- An optional DynamoDB table for per-actor MCP session persistence (when `MCP_SESSION_TABLE` env var is referenced)
- IAM role with permissions to read lesser's DynamoDB table, read Secrets Manager for the JWT secret, call lesser-host's comm APIs
- SSM parameter exports under `/<app>/<stage>/lesser-body/exports/v1/`

**Never set timeouts on CDK deploy commands.** A deploy that feels stuck is almost always waiting on a CloudFormation resource, a stack rollback, or SSM parameter-update propagation. Aborting leaves CloudFormation in a half-migrated state.

Run deploys to completion. Capture full output.

## Rollout discipline

Standard rollout for a change:

1. **Feature branch merges to `main`** via PR with required review.
2. **Deploy to `lab` / `dev`** via CDK. Observe MCP endpoint responds correctly, discovery metadata is current, OAuth flow works, tool invocations succeed.
3. **Soak in `lab`.** Evidence that tool calls work correctly, scope / profile gates enforce as expected, communication-tool delegation to lesser-host works, session persistence (if enabled) retains across invocations.
4. **Deploy to `staging`** if the deployment uses one. Integration partners exercise real MCP flows.
5. **Soak in `staging`.** Typically multiple days for non-trivial changes; longer for MCP-contract or scope/profile changes.
6. **Deploy to `live`** with explicit operator authorization.
7. **Post-deploy monitoring.** CloudWatch error rate for the body Lambda, MCP invocation success rate by tool, scope/profile-rejection rate (should be stable), DynamoDB session-table capacity metrics, JWT-validation failure rate, SNS error-topic messages (where configured).

Skipping stages requires explicit operator authorization. Default cadence is `lab → (staging →) live` with soak between each.

## Hotfix discipline

For urgent production issues — CVE responses, authorization bypasses, MCP-contract regressions:

- **Compressed soak durations**, not skipped stages.
- **Explicit user authorization** for compression is recorded.
- **Elevated post-deploy monitoring.** Less soak means tighter watch.
- **Post-incident review** identifies what gate missed the issue.

## Rollback discipline

- **Lambda-version rollback** — Lambda versions are immutable. Rollback means CDK re-deploying with the prior commit checked out, or manually aliasing body's Lambda back to the prior version.
- **CDK stack rollback** — CloudFormation rollback handles failed deploys automatically. For stable-but-regressed deploys, revert the commit on `main` and redeploy.
- **SSM-parameter rollback** — body's exports are updated on each deploy. A rollback deploy writes the prior version's export values back. This is low-risk because lesser reads the SSM exports on each deploy, not continuously.
- **Session-table rollback** — if the session table's schema changes, rollback must plan for in-flight session data.

- **Never delete Lambda function versions** that could be rollback targets.
- **Never delete the CloudFormation stack.**
- **Never delete published SSM parameter exports** under `/<app>/<stage>/lesser-body/exports/v1/` — lesser reads these; deleting them breaks the soul-enabled lesser deploy.

## Release artifacts

Tags on `main` (`v<version>`) correspond to releases. For managed-consumer ingestion by lesser-host's provisioning worker, release artifacts may be cut similarly to lesser's pattern:

- Git tag on `main` at the release commit
- GitHub Release at `equaltoai/lesser-body/releases/tag/v<version>`
- Release notes documenting tool-surface changes, scope/profile adjustments, contract changes, lesser-integration adjustments

The `docs/release.md` file (or equivalent) describes the managed release cycle for body.

## Managed deployment by lesser-host

For managed lesser instances provisioned by lesser-host, body is deployed as part of the provisioning flow. lesser-host's provisioning worker:

- **Verifies checksums** on body's release artifacts before deploying.
- **Deploys body** alongside the managed lesser instance following the three-step contract.
- **Publishes the SSM exports** and wires lesser's soul-enabled configuration.

Breaking the release-artifact shape or the SSM-export contract without coordinating with the `host` steward breaks managed deployment. Coordinate for any artifact-shape or SSM-contract change.

## Commit and PR discipline

- Clear, present-tense commit subjects. Conventional Commits style welcomed (`feat(mcp): ...`, `fix(auth): ...`, `chore(deps): ...`).
- First line under 72 characters.
- Explain the *why* in the body, especially for MCP-contract, scope / profile, or lesser-integration changes.
- PRs through required review. Review is substantive — authorization and contract-stability changes deserve real scrutiny.

## Security-aware logging discipline

MCP-server logging has specific patterns:

- **Never log full JWT tokens**. Use redacted forms (claims summary or token hash).
- **Never log raw scope / profile authorization decisions with full caller identity** — use structured audit events with redacted fields.
- **Never log full email recipient addresses or phone numbers** — communication-tool logs sanitize these per established patterns.
- **Tainted input fields** (MCP request parameters) are sanitized before emission.
- **Audit events for authorization rejections** — important for operator observability; emit structured events that can be aggregated.

## Rules you do not break

- Never force-push to `main`.
- Never amend a commit that has been pushed.
- Never skip pre-commit hooks (`--no-verify`).
- Never bypass required review.
- Never deploy to `live` without successful `lab` / `staging` soak.
- **Never set a timeout on a CDK deploy command.**
- Never commit secrets, JWT signing material, managed-instance keys, partner credentials, or `.env` files.
- Never log full JWTs, full managed-instance keys, raw passwords, full recipient emails, full phone numbers, or unsanitized MCP request bodies.
- Never delete Lambda function versions that could be rollback targets.
- Never delete published SSM parameter exports under `/<app>/<stage>/lesser-body/exports/v1/`.
- Never alter the three-step first-deploy order (unsouled lesser → body → soul-enabled lesser).
- Never bypass scope or profile authorization gates.
- Never loosen scope or profile semantics silently.
- Never add dynamic tool registration or runtime tool mutation.
- Never patch AppTheory or TableTheory locally. Framework awkwardness is signal; report it upstream via `coordinate-framework-feedback`.
- Never break the MCP contract (`.well-known/mcp.json`, OAuth protected-resource metadata, JSON-RPC shapes) without explicit client-side coordination.
- Never change the SSM export contract (`/<app>/<stage>/lesser-body/exports/v1/`) without coordinating with the `lesser` and `host` stewards.
- Never reach into lesser's DynamoDB schema to bypass lesser's REST API for concerns that belong in lesser's API contract.
- Never bypass lesser-host's comm APIs for outbound communication; delegation is the contract.
- Never introduce proprietary blobs or AGPL-incompatible dependencies.
- Never execute an advisor-dispatched brief without running `review-advisor-brief` and surfacing to Aron for authorization.
