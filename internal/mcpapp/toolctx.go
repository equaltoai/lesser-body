package mcpapp

import (
	"context"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

// appTheoryContextKeyRuntimePolicy carries the runtime-policy resolution
// computed by WithRuntimePolicy from the apptheory request middleware into the
// per-POST tool-context hook. The hook re-applies it onto the stdlib context
// handed to MCP method handlers via runtimepolicy.WithContext.
const appTheoryContextKeyRuntimePolicy = "lesser_body_runtime_policy"

// ToolContextHook derives the stdlib context handed to MCP method handlers
// (tools, resources, prompts, completions) from the request's
// *apptheory.Context. It is registered on the MCP server through
// mcpruntime.WithToolContextHook and runs once per POST request after origin
// and header validation.
//
// This is the supported replacement for the former WithToolContext middleware,
// which used reflect+unsafe to overwrite apptheory.Context's private request
// context. Principal, bearer, request-id, and actor propagation are preserved;
// middleware that previously overrode the request context now records values on
// the apptheory.Context (WithRuntimePolicy, WithActorBinding) and the hook
// folds them into the derived context.
func ToolContextHook(c *apptheory.Context, ctx context.Context) context.Context {
	if c == nil {
		return ctx
	}

	principal := auth.PrincipalFromContext(c)
	token := bearerTokenFromHeaders(c.Request.Headers)
	requestID := strings.TrimSpace(c.RequestID)
	actor := actorFromRequestContext(c)

	if principal != nil || token != "" || requestID != "" || actor != "" {
		ctx = auth.InjectToolContext(ctx, principal, token)
		ctx = auth.WithToolRequestID(ctx, requestID)
		ctx = auth.WithToolActor(ctx, actor)
	}

	if resolved, ok := runtimePolicyFromAppTheoryContext(c); ok {
		ctx = runtimepolicy.WithContext(ctx, resolved)
	}

	if caller, ok := mcpserver.ShareCallerFromAppTheoryContext(c); ok {
		ctx = mcpserver.WithShareCaller(ctx, caller)
	}

	return ctx
}

// RegisterToolContextHook applies mcpapp's tool-context derivation to a
// pre-constructed MCP server. mcpruntime.ServerOption is a plain function
// type, so applying the option directly is the supported way to install the
// hook on a server built by another package (mcpserver cannot import mcpapp).
func RegisterToolContextHook(srv *mcpruntime.Server) {
	if srv == nil {
		return
	}
	mcpruntime.WithToolContextHook(ToolContextHook)(srv)
}

func runtimePolicyFromAppTheoryContext(c *apptheory.Context) (runtimepolicy.Resolved, bool) {
	if c == nil {
		return runtimepolicy.Resolved{}, false
	}
	resolved, ok := c.Get(appTheoryContextKeyRuntimePolicy).(runtimepolicy.Resolved)
	return resolved, ok
}

func bearerTokenFromHeaders(headers map[string][]string) string {
	for k, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(k), "authorization") {
			continue
		}
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			parts := strings.Fields(v)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				return ""
			}
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}
