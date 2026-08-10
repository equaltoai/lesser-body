# MCP-contract audit: actor-initiated Ptah soul recovery

## Contract delta

`tools/list` and `/.well-known/mcp.json` gain the additive `soul_self_recover` definition. Its request schema is `{ "type": "object", "additionalProperties": false }`. Its success/error results use Body's existing tool-result and shared structured error conventions.

## Stability assessment

- Endpoint paths: unchanged.
- OAuth protected-resource metadata, issuer, resource identifiers, and advertised public scopes: unchanged.
- JSON-RPC and Streamable HTTP envelopes: unchanged.
- Existing tools/resources/prompts and protocol-version support: unchanged.
- Profile filtering: existing clients see the new tool only for souled actors.
- Backward compatibility: additive; clients that ignore unknown tools continue unchanged.

## Client classes

Claude, AgentCore, Codex/rmcp, and other MCP clients require no migration. Clients may discover and invoke the tool after deployment with an actor OAuth token carrying `write`.

## Error behavior

Authorization/profile denials remain transport-level Body gate responses. Handler validation and recovery failures use existing MCP `isError` results with stable, sanitized codes; Host response bodies are never passed through.

## Verdict

Backward-compatible additive change. No versioned endpoint, deprecation window, or client-side coordination is required; release notes and discovery tests are sufficient.
