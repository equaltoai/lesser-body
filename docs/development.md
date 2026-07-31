# Development

<!-- AI Training: Local development workflow for lesser-body -->

This doc describes how to work on `lesser-body` locally (tests, builds, CDK synth). The CDK app is TypeScript; Go is used for the Lambda runtime artifact.

## Prerequisites

- Go `1.26+`
- Node.js `24+` (for CDK)

## Run tests

```bash
go test ./...
```

## Build the Lambda artifact

```bash
bash scripts/build.sh
```

Output:

- `dist/lesser-body.zip`

## CDK synth (sanity check)

CDK synth requires account/region env vars (the CDK CLI normally sets these). For a deterministic local synth:

```bash
export CDK_DEFAULT_ACCOUNT="000000000000"
export CDK_DEFAULT_REGION="us-east-1"

cd cdk
npm ci
npm test
npm run synth -- -c app=lesser -c stage=dev -c baseDomain=example.com
bash ../scripts/check_cdk_discovery_routes.sh
```

## Local MCP invocation (deterministic)

The repo uses AppTheory’s testkit to invoke the `/mcp` handler without AWS.

Reference tests:

- Auth + session behavior: `internal/mcpapp/app_test.go`
- Tool coverage: `internal/mcpserver/mcpserver_test.go`, `internal/mcpserver/*_test.go`

### Notes on local auth

Unit tests typically set:

- `JWT_SECRET=test`
- `MCP_SESSION_TABLE=` (empty) to avoid DynamoDB

And then mint HS256 JWTs in-process for deterministic auth.

## Testing against a real Lesser instance

Social tools (timeline reads, post create, follow, etc.) call Lesser’s REST API. To test them end-to-end you need:

- A reachable Lesser API base URL (`LESSER_API_BASE_URL`)
- A valid Lesser OAuth access token (bearer token)

For example:

```bash
export LESSER_API_BASE_URL="https://api.dev.example.com"
export JWT_SECRET="..." # only if you're minting test tokens locally; deployed auth uses Secrets Manager
```

See `docs/mcp.md` for the MCP request format.

### Host mailbox canary

After lesser-host Soul Comm Mailbox v1 is deployed for a lab instance, validate body's MCP facade with:

```bash
MCP_ENDPOINT="https://api.dev.example.com/mcp/<actor>" \
MCP_BEARER_TOKEN="<oauth-access-token>" \
EXPECTED_IDENTITY_EMAIL="<agent-local-id>.<instance-slug>@lessersoul.ai" \
LEGACY_ALIAS_EMAIL="<agent-local-id>@lessersoul.ai" \
scripts/canary_host_mailbox_mcp.py
```

The canary checks `tools/list`, `identity_whoami`, `identity_lookup`, default/standard `email_read`, explicit
`email_read(view=compact)` expansion refs, `email_get`, `email_get_content`, `email_search`, `email_mark_read`,
`email_mark_unread`, `sms_read`, `voicemail_read`, and a not-found error path. Optional `email_send` / `email_reply`
checks require `MAILBOX_CONFIRM_MUTATIONS=true` plus the relevant `CANARY_*` variables because they queue real
messages through lesser-host. It never prints full message bodies, full recipient addresses, bearer tokens, or raw
upstream error payloads; error logs keep only stable codes/status plus hashed payload summaries where details are
needed.

### Article MCP canary

After the Lesser Article/CMS GraphQL contract is deployed for a lab or staging instance, validate the MCP Article
workflow with an actor-scoped OAuth token that has write scope:

```bash
MCP_ENDPOINT="https://api.dev.example.com/mcp/<actor>" \
MCP_BEARER_TOKEN="<oauth-access-token>" \
ARTICLE_CANARY_CONFIRM_DRAFT_CREATE=true \
scripts/canary_article_mcp.py
```

The canary is a no-public-side-effects probe. It calls `article_draft_list`, `article_list`,
`article_draft_create`, and `article_draft_preview` using compact views, proving the depth-safe list contracts and the
unpublished draft → Lesser-rendered preview path. It refuses `ARTICLE_CANARY_CONFIRM_PUBLISH=true` because Article
publishing requires a separate, explicitly authorized manual validation path. The probe never publishes Articles, creates
public/unlisted posts, follows, boosts, favorites, dismisses notifications, deploys, signs, or mutates cloud/on-chain
state. It refuses authenticated redirects and prints only compact validation signals: ids/cursors, payload sizes,
omission/expansion metadata, booleans, and hashes. It never prints bearer tokens, draft content, rendered HTML, full tool
payloads, or raw upstream error payloads. Use `ARTICLE_CANARY_TITLE`, `ARTICLE_CANARY_SLUG`, `ARTICLE_CANARY_CONTENT`,
`ARTICLE_CANARY_CONTENT_FORMAT`, `ARTICLE_CANARY_PREVIEW_CHARS`, and `ARTICLE_CANARY_MAX_OUTPUT_BYTES` only when a run
needs deterministic inputs or tighter response budgets.

### Article review MCP canary

After the three review tools are available on a dev instance, run the two-actor live proof with author and reviewer
OAuth tokens that carry both `read` and `write` scopes:

```bash
ARTICLE_REVIEW_AUTHOR_MCP_ENDPOINT="https://api.dev.example.com/mcp/<author>" \
ARTICLE_REVIEW_AUTHOR_BEARER_TOKEN="<author-oauth-token>" \
ARTICLE_REVIEW_REVIEWER_MCP_ENDPOINT="https://api.dev.example.com/mcp/<reviewer>" \
ARTICLE_REVIEW_REVIEWER_BEARER_TOKEN="<reviewer-oauth-token>" \
ARTICLE_REVIEW_REVIEWER_USERNAME="<reviewer>" \
ARTICLE_REVIEW_CANARY_CONFIRM_MUTATIONS=true \
scripts/canary_article_review_mcp.py
```

The probe creates an unpublished draft unless `ARTICLE_REVIEW_DRAFT_ID` is supplied, then proves submit → reviewer
queue → reviewer state → verdict → author-observed state. It never publishes. The explicit confirmation is required
because review grants and verdicts are durable Lesser state. Output contains only bounded identifiers, counts,
booleans, response sizes, and hashes; tokens, draft content, notes, and raw error payloads remain redacted. Optional
`ARTICLE_REVIEW_VERDICT`, `ARTICLE_REVIEW_NOTES`, and `ARTICLE_REVIEW_MAX_OUTPUT_BYTES` tune the proof.
Prefer `ARTICLE_REVIEW_DRAFT_ID` with a reused fixture draft; canary-created drafts, grants, and verdicts are durable by
design and accumulate.
