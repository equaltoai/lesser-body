# Development

<!-- AI Training: Local development workflow for lesser-body -->

This doc describes how to work on `lesser-body` locally (tests, builds, CDK synth).

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
npx cdk synth -c app=lesser -c stage=dev -c baseDomain=example.com
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
scripts/canary_host_mailbox_mcp.py
```

The canary checks `tools/list`, default/standard `email_read`, explicit `email_read(view=compact)` expansion refs,
`email_get`, `email_get_content`, `email_search`, `email_mark_read`, `email_mark_unread`, `sms_read`,
`voicemail_read`, and a not-found error path. It never prints full message bodies, full recipient addresses, bearer
tokens, or raw upstream error payloads; error logs keep only stable codes/status plus hashed payload summaries where
details are needed.

### Article MCP canary

After the Lesser Article/CMS GraphQL contract is deployed for a lab or staging instance, validate the MCP Article
workflow with an actor-scoped OAuth token that has write scope:

```bash
MCP_ENDPOINT="https://api.dev.example.com/mcp/<actor>" \
MCP_BEARER_TOKEN="<oauth-access-token>" \
ARTICLE_CANARY_CONFIRM_PUBLISH=true \
scripts/canary_article_mcp.py
```

The canary intentionally creates and publishes a real canary Article for that actor. It calls
`article_draft_create`, `article_draft_preview`, `article_draft_publish`, and `article_get` using compact views,
proving the draft → Lesser-rendered preview → publish → canonical fetch path. It refuses authenticated redirects and
prints only compact release-validation signals: ids/URLs, payload sizes, omission/expansion metadata, booleans, and
hashes. It never prints bearer tokens, draft content, rendered HTML, full tool payloads, or raw upstream error payloads.
Use `ARTICLE_CANARY_TITLE`, `ARTICLE_CANARY_SLUG`, `ARTICLE_CANARY_CONTENT`,
`ARTICLE_CANARY_CONTENT_FORMAT`, `ARTICLE_CANARY_PREVIEW_CHARS`, and `ARTICLE_CANARY_MAX_OUTPUT_BYTES` only when a
run needs deterministic inputs or tighter response budgets.
