# OAuth Migration Guide

<!-- AI Training: Migration guide for moving lesser-body clients from legacy bearer-token auth to OAuth connectors -->

`lesser-body` is converging on one canonical inbound MCP client auth story:

- OAuth connector flow against Lesser

These legacy inbound patterns are deprecated:

- hardcoded bearer tokens in `.mcp.json` or equivalent client config
- Simulacrum runtime credentials issued via `delegateToAgent()`
- managed instance key as a direct `/mcp` bearer token

This guide shows how to migrate without removing the outbound `LESSER_HOST_INSTANCE_KEY` that communication tools still
need for server-to-server calls into lesser-host.

## Before you start

Confirm the public discovery surfaces are healthy:

```bash
curl -sS "https://api.<stageDomain>/.well-known/mcp.json" | jq .
curl -sS "https://api.<stageDomain>/.well-known/oauth-protected-resource" | jq .
curl -sS "https://api.<stageDomain>/.well-known/oauth-authorization-server" | jq .
```

If browser MCP clients are involved, verify CORS too:

```bash
curl -sSI \
  -H "Origin: https://claude.ai" \
  "https://api.<stageDomain>/.well-known/oauth-protected-resource"
```

## Step 1: register an OAuth connector in Lesser

Lesser already exposes public app registration at `POST /api/v1/apps`.

Claude-oriented redirect URI examples from the current project rollout:

- Claude Code local redirect: `http://localhost:<port>/callback`
- Claude.ai web redirect: `https://claude.ai/api/mcp/auth_callback`

Example registration:

```bash
curl -s -X POST "https://api.<stageDomain>/api/v1/apps" \
  -H "Content-Type: application/json" \
  -d '{
    "client_name": "lesser-body-mcp",
    "redirect_uris": "http://localhost:45454/callback https://claude.ai/api/mcp/auth_callback",
    "scopes": "read write follow",
    "website": "https://app.<stageDomain>"
  }' | jq .
```

Store the returned `client_id` and `client_secret`. Lesser only returns the secret at registration time.

## Step 2: point the MCP client at the Lesser OAuth discovery chain

For Lesser-backed MCP, the auth discovery URL is:

```text
https://api.<stageDomain>/.well-known/oauth-protected-resource
```

The MCP transport endpoint remains:

```text
https://api.<stageDomain>/mcp
```

Clients should discover auth from the protected-resource metadata and then complete the OAuth redirect flow against the
authorization server it declares.

## Step 3: remove hardcoded bearer tokens from client config

Legacy static-token shape:

```json
{
  "mcpServers": {
    "lesser": {
      "type": "http",
      "url": "https://api.<stageDomain>/mcp",
      "headers": {
        "Authorization": "Bearer <legacy-runtime-token>"
      }
    }
  }
}
```

OAuth-first target shape:

```json
{
  "mcpServers": {
    "lesser": {
      "type": "http",
      "url": "https://api.<stageDomain>/mcp"
    }
  }
}
```

The exact config envelope varies by MCP client, but the migration rule is the same: keep the MCP transport URL and
remove the static bearer header so the client can run the OAuth flow instead.

## Step 4: keep outbound service auth intact

Do not delete `LESSER_HOST_INSTANCE_KEY` or `LESSER_HOST_INSTANCE_KEY_ARN` from the lesser-body deployment just because
inbound MCP clients moved to OAuth.

That secret is still used when lesser-body calls lesser-host for:

- `email_send`
- `email_reply`
- `sms_send`

Inbound client auth and outbound service auth are separate concerns.

## Step 5: coordinate Simulacrum rollout

Simulacrum still has a transitional `Issue runtime credentials` flow tracked in `equaltoai/simulacrum#54`.

Until that UI is migrated:

- OAuth connectors are the recommended client path
- runtime credentials remain a compatibility flow that must be coordinated before legacy inbound auth is removed

Operator automation that historically used the managed instance key has a separate target state: move it to a dedicated
OAuth operator client as described in `docs/operator-auth-replacement.md`.

## Compatibility and rollback

- If a deployment still depends on managed-instance-key inbound auth, keep the temporary `MCP_ALLOW_LEGACY_INSTANCE_KEY`
  compatibility flag explicit and time-boxed.
- Treat any usage of that flag as rollback-only; the target state is OAuth-first inbound MCP auth.
