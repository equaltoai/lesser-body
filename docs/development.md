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

