package mcpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func WithRuntimePolicy(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		resolved := runtimepolicy.ResolveForActor(contextForRuntimePolicy(ctx), actorFromRequestContext(ctx))
		if ctx != nil {
			setRequestContext(ctx, runtimepolicy.WithContext(ctx.Context(), resolved))
		}

		reqs, batch, err := parseMCPRequestsForRuntimePolicy(ctx)
		if err == nil {
			if denied := enforceRuntimePolicy(reqs, resolved); denied != nil {
				return denied, nil
			}
		}

		resp, nextErr := next(ctx)
		if nextErr != nil || resp == nil || err != nil {
			return resp, nextErr
		}

		filterRuntimeCapabilityLists(reqs, batch, resp, resolved.Profile)
		return resp, nil
	}
}

func contextForRuntimePolicy(ctx *apptheory.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx.Context()
}

func parseMCPRequestsForRuntimePolicy(ctx *apptheory.Context) ([]*mcpruntime.Request, bool, error) {
	if ctx == nil {
		return nil, false, fmt.Errorf("missing request context")
	}

	body := bytes.TrimSpace(ctx.Request.Body)
	if len(body) == 0 {
		return nil, false, fmt.Errorf("empty request body")
	}

	if body[0] == '[' {
		reqs, err := mcpruntime.ParseBatchRequest(ctx.Request.Body)
		return reqs, true, err
	}

	req, err := mcpruntime.ParseRequest(ctx.Request.Body)
	if err != nil {
		return nil, false, err
	}
	return []*mcpruntime.Request{req}, false, nil
}

func enforceRuntimePolicy(reqs []*mcpruntime.Request, resolved runtimepolicy.Resolved) *apptheory.Response {
	if resolved.Profile != runtimepolicy.ProfileDrone {
		return nil
	}

	for _, req := range reqs {
		if req == nil {
			continue
		}

		switch req.Method {
		case "tools/call":
			name := requestedToolName(req)
			if name != "" && !runtimepolicy.ToolAllowed(resolved.Profile, name) {
				return runtimeBoundaryViolationResponse("tool", name, resolved)
			}
		case "resources/read":
			uri := requestedResourceURI(req)
			if uri != "" && !runtimepolicy.ResourceAllowed(resolved.Profile, uri) {
				return runtimeBoundaryViolationResponse("resource", uri, resolved)
			}
		case "prompts/get":
			name := requestedPromptName(req)
			if name != "" && !runtimepolicy.PromptAllowed(resolved.Profile, name) {
				return runtimeBoundaryViolationResponse("prompt", name, resolved)
			}
		}
	}

	return nil
}

func runtimeBoundaryViolationResponse(surface string, name string, resolved runtimepolicy.Resolved) *apptheory.Response {
	name = strings.TrimSpace(name)
	message := fmt.Sprintf("%s %q is unavailable to the %s runtime", surface, name, resolved.Profile)
	return apptheory.MustJSON(403, map[string]any{
		"error": map[string]any{
			"code":    "app.forbidden",
			"message": message,
			"details": map[string]any{
				"reason":                "runtime_boundary",
				"profile":               resolved.Profile,
				"surface":               surface,
				"name":                  name,
				"determined":            resolved.Determined,
				"determinationSource":   resolved.DeterminationSource,
				"communicationsEnabled": resolved.CommunicationsEnabled,
				"walletAccessEnabled":   resolved.WalletAccessEnabled,
			},
		},
	})
}

func requestedToolName(req *mcpruntime.Request) string {
	var params struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)
	return strings.TrimSpace(params.Name)
}

func requestedResourceURI(req *mcpruntime.Request) string {
	var params struct {
		URI string `json:"uri"`
	}
	_ = json.Unmarshal(req.Params, &params)
	return strings.TrimSpace(params.URI)
}

func requestedPromptName(req *mcpruntime.Request) string {
	var params struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)
	return strings.TrimSpace(params.Name)
}

func filterRuntimeCapabilityLists(reqs []*mcpruntime.Request, batch bool, resp *apptheory.Response, profile runtimepolicy.Profile) {
	if resp == nil || resp.Status != 200 || len(bytes.TrimSpace(resp.Body)) == 0 {
		return
	}

	if batch {
		var rpcs []mcpruntime.Response
		if err := json.Unmarshal(resp.Body, &rpcs); err != nil {
			return
		}
		requestsByID := make(map[string]*mcpruntime.Request, len(reqs))
		for _, req := range reqs {
			if req == nil {
				continue
			}
			requestsByID[jsonRPCIDKey(req.ID)] = req
		}

		changed := false
		for i := range rpcs {
			req := requestsByID[jsonRPCIDKey(rpcs[i].ID)]
			if req == nil || rpcs[i].Error != nil {
				continue
			}
			filtered, ok := filteredCapabilityListResult(req.Method, rpcs[i].Result, profile)
			if !ok {
				continue
			}
			rpcs[i].Result = filtered
			changed = true
		}
		if changed {
			if body, err := json.Marshal(rpcs); err == nil {
				resp.Body = body
			}
		}
		return
	}

	if len(reqs) != 1 || reqs[0] == nil {
		return
	}

	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		return
	}
	if rpc.Error != nil {
		return
	}

	filtered, ok := filteredCapabilityListResult(reqs[0].Method, rpc.Result, profile)
	if !ok {
		return
	}
	rpc.Result = filtered
	if body, err := json.Marshal(rpc); err == nil {
		resp.Body = body
	}
}

func filteredCapabilityListResult(method string, raw any, profile runtimepolicy.Profile) (any, bool) {
	result, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}

	switch method {
	case "tools/list":
		filtered, changed := filterNamedResultList(result["tools"], "name", func(name string) bool {
			return runtimepolicy.ToolAllowed(profile, name)
		})
		if !changed {
			return nil, false
		}
		result["tools"] = filtered
		return result, true
	case "resources/list":
		filtered, changed := filterNamedResultList(result["resources"], "uri", func(name string) bool {
			return runtimepolicy.ResourceAllowed(profile, name)
		})
		if !changed {
			return nil, false
		}
		result["resources"] = filtered
		return result, true
	case "prompts/list":
		filtered, changed := filterNamedResultList(result["prompts"], "name", func(name string) bool {
			return runtimepolicy.PromptAllowed(profile, name)
		})
		if !changed {
			return nil, false
		}
		result["prompts"] = filtered
		return result, true
	default:
		return nil, false
	}
}

func filterNamedResultList(raw any, field string, allow func(string) bool) ([]any, bool) {
	items, ok := raw.([]any)
	if !ok {
		return nil, false
	}

	filtered := make([]any, 0, len(items))
	changed := false
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		value, _ := obj[field].(string)
		if allow(strings.TrimSpace(value)) {
			filtered = append(filtered, item)
			continue
		}
		changed = true
	}
	return filtered, changed
}

func jsonRPCIDKey(id any) string {
	body, err := json.Marshal(id)
	if err != nil {
		return ""
	}
	return string(body)
}
