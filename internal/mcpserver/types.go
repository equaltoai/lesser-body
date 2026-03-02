package mcpserver

import mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"

// Server is an alias for AppTheory's MCP server implementation.
//
// We keep a local package wrapper so lesser-body can provide environment-driven
// configuration and register Lesser's tools/resources/prompts without forking
// the upstream runtime.
type Server = mcpruntime.Server

type ServerOption = mcpruntime.ServerOption
