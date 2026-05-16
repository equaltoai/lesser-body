# OAuth Migration Guide

<!-- AI Training: Migration guide for moving lesser-body clients from legacy bearer-token auth to OAuth connectors -->

`lesser-body` is converging on one canonical inbound MCP client auth story:

- OAuth connector flow against Lesser

These legacy inbound patterns are deprecated:

- hardcoded bearer tokens in `.mcp.json` or equivalent client config
- Simulacrum runtime credentials issued via `delegateToAgent()`
- managed instance key as a direct `/mcp/{actor}` bearer token

This guide shows how to migrate without removing the `LESSER_HOST_INSTANCE_KEY` that host-backed communication tools
and scoped x402 grant validation still need for server-to-server calls into lesser-host.

## Before you start

Confirm the public discovery surfaces are healthy:

```bash
curl -sS "https://api.<stageDomain>/.well-known/mcp.json" | jq .
curl -sS "https://api.<stageDomain>/.well-known/oauth-protected-resource/mcp/Arch" | jq .
curl -sS "https://api.<stageDomain>/.well-known/oauth-authorization-server" | jq .
```

If browser MCP clients are involved, verify CORS too:

```bash
curl -sSI \
  -H "Origin: https://claude.ai" \
  "https://api.<stageDomain>/.well-known/oauth-protected-resource/mcp/Arch"
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
https://api.<stageDomain>/.well-known/oauth-protected-resource/mcp/<actor>
```

The MCP transport endpoint becomes:

```text
https://api.<stageDomain>/mcp/<actor>
```

Clients should discover auth from the protected-resource metadata and then complete the OAuth redirect flow against the
authorization server it declares.

Agent URL map:

| Actor | URL |
|------|-----|
| `Arch` | `https://api.dev.simulacrum.greater.website/mcp/Arch` |
| `Medic` | `https://api.dev.simulacrum.greater.website/mcp/Medic` |
| `Scout` | `https://api.dev.simulacrum.greater.website/mcp/Scout` |
| `Pilot` | `https://api.dev.simulacrum.greater.website/mcp/Pilot` |
| `Ops` | `https://api.dev.simulacrum.greater.website/mcp/Ops` |
| `Counsel` | `https://api.dev.simulacrum.greater.website/mcp/Counsel` |
| `Advocate` | `https://api.dev.simulacrum.greater.website/mcp/Advocate` |

## Step 3: remove hardcoded bearer tokens from client config

Legacy static-token shape:

```json
{
  "mcpServers": {
    "simulacrum-arch": {
      "type": "http",
      "url": "https://api.<stageDomain>/mcp/Arch",
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
    "simulacrum-arch": {
      "type": "http",
      "url": "https://api.<stageDomain>/mcp/Arch",
      "oauth": {
        "clientId": "<registered-client-id>",
        "callbackPort": 8787
      }
    }
  }
}
```

The exact config envelope varies by MCP client, but the migration rule is the same:

1. move from the shared `/mcp` URL to the actor-specific `/mcp/{actor}` URL
2. remove the static bearer header
3. let the client run the OAuth flow instead

Arch-specific note: fix the server key from `simulacrum-medic` to `simulacrum-arch`.

## Step 4: keep outbound service auth intact

Do not delete `LESSER_HOST_INSTANCE_KEY` or `LESSER_HOST_INSTANCE_KEY_ARN` from the lesser-body deployment just because
inbound MCP clients moved to OAuth.

That secret is still used when lesser-body calls lesser-host for:

- `email_send`
- `email_read`
- `email_get`
- `email_get_content`
- `email_search`
- `email_reply`
- `email_delete`
- `email_mark_read`
- `email_mark_unread`
- `sms_send`
- `sms_read`
- `voicemail_read`
- scoped public x402 invocation grant validation

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
