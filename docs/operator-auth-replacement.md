# Operator Auth Replacement

<!-- AI Training: Architecture decision for replacing inbound managed-instance-key auth in lesser-body -->

This document is the repo-local architecture decision for `lesser-body#44`.

## Decision

Inbound managed-instance-key auth for `/mcp` is being replaced with a dedicated OAuth client pattern for operator and
internal automation.

The target state is:

- inbound MCP automation uses OAuth access tokens issued by Lesser
- the OAuth client identity is explicit and auditable through claims like `client_id` and `client_class`
- lesser-body does not invent a second local admin secret model once OAuth is available
- `LESSER_HOST_INSTANCE_KEY` remains a server-to-server credential for lesser-body calls into lesser-host, including host-backed communication and scoped x402 grant consume/verification

## Why this replaces instance-key auth

`PrincipalTypeInstanceKey` currently conflates two jobs:

- inbound MCP client auth
- infrastructure-level admin bypass

That coupling makes every holder of the infrastructure secret look like an all-powerful MCP caller. It is hard to audit,
hard to scope, and easy to confuse with normal client auth.

The replacement direction is an OAuth client with an explicit machine/operator identity instead of a bearer secret that
masquerades as a user session.

## Target replacement shape

The preferred replacement is a dedicated confidential OAuth client registered in Lesser for operator automation.

Expected properties:

- confidential client registration in Lesser
- auditable `client_id`
- explicit `client_class=operator` or equivalent machine-readable identity marker in token claims
- explicit operator/admin authority carried by OAuth rather than inferred from possession of an infrastructure secret
- normal MCP bearer-token transport to `POST /mcp`

The exact authority model depends on `equaltoai/lesser#259`.

Two acceptable ways for Lesser to resolve that dependency:

1. `admin` becomes an OAuth-requestable scope for the operator client class.
2. Lesser defines a different canonical operator privilege model, but still expresses it in OAuth-issued claims that
   lesser-body can evaluate without a local instance-key bypass.

Until one of those exists, lesser-body must not assume that ordinary connector scopes such as `read write follow` are
enough to replace operator automation.

## Non-goals

- Do not keep inbound managed-instance-key auth as a permanent peer to OAuth.
- Do not reuse the outbound `LESSER_HOST_INSTANCE_KEY` as a long-term MCP client credential.
- Do not make lesser-body the source of truth for the upstream OAuth scope or error contract; those remain aligned with
  `equaltoai/lesser#259` and `equaltoai/lesser#249`.

## Rollout sequence

1. Keep OAuth connector auth as the canonical inbound MCP path for end-user clients.
2. Land the operator OAuth privilege model in Lesser (`equaltoai/lesser#259` dependency).
3. Update Simulacrum UX to stop centering runtime credentials and direct bearer-token issuance
   (`equaltoai/simulacrum#54` dependency).
4. Gate legacy inbound managed-instance-key auth behind an explicit compatibility flag in lesser-body.
5. Remove the compatibility flag after operator automation and Simulacrum have both migrated.

## Migration target for operators

Operators that currently automate `/mcp` with the managed instance key should move to:

1. register or provision a dedicated operator OAuth client in Lesser
2. obtain an OAuth token for that operator client using the final Lesser-supported grant
3. call `https://api.<stageDomain>/mcp` with `Authorization: Bearer <oauth_access_token>`
4. retain `LESSER_HOST_INSTANCE_KEY` only inside the lesser-body deployment for host-backed communication calls and scoped x402 grant consume/verification

## Dependency summary

- `equaltoai/lesser#259`: resolves whether operator/admin authority is a requestable scope or another canonical
  OAuth-carried privilege
- `equaltoai/simulacrum#54`: removes the remaining UI emphasis on runtime credentials and direct bearer-token flows
- `equaltoai/lesser#249`: owns upstream auth error payload normalization; lesser-body only translates those failures for
  MCP clients
