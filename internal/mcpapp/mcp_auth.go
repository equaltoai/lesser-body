package mcpapp

import (
	"encoding/json"
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-body/internal/auth"
)

// MCPAuthorizationScopes is the OAuth scope set needed to use Body's
// authenticated MCP transport surfaces.
const MCPAuthorizationScopes = "read write"

type mcpAuthorizationChallengeOptions struct {
	Error            string
	ResourceMetadata string
	Scope            string
	ErrorDescription string
}

func WithMCPAuthorization(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	authHook := auth.Hook(nil)

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		expectedAudience, err := validatedMcpEndpointForRequest(ctx)
		if err != nil {
			return invalidDiscoveryConfigResponse(err), nil
		}

		if identity, attempted, resp, err := tryAuthorizeX402InvocationGrant(ctx); attempted {
			if err != nil {
				return nil, err
			}
			if resp != nil {
				return resp, nil
			}
			if strings.TrimSpace(identity) == "" {
				return unauthorizedMCPResponse(ctx), nil
			}
			return next(ctx)
		}

		identity, err := authHook(ctx)
		if err != nil {
			return unauthorizedMCPResponse(ctx), nil
		}
		if strings.TrimSpace(identity) == "" {
			return unauthorizedMCPResponse(ctx), nil
		}
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.Type == auth.PrincipalTypeOAuthToken {
			if err := auth.ValidateTokenAudience(principal.Claims, expectedAudience); err != nil {
				return unauthorizedMCPResponse(ctx), nil
			}
		}
		ctx.AuthIdentity = identity
		return next(ctx)
	}
}

func unauthorizedMCPResponse(ctx *apptheory.Context) *apptheory.Response {
	return unauthorizedMCPResponseWithOptions(ctx, mcpAuthorizationChallengeOptions{
		ResourceMetadata: protectedResourceMetadataURLForRequest(ctx),
		Scope:            MCPAuthorizationScopes,
	}, map[string]any{
		"source":          "lesser_body",
		"reauthorize":     true,
		"authAction":      "authorize",
		"refreshRequired": false,
		"reason":          "missing_or_invalid_bearer",
	})
}

func actorSessionNotFoundMCPResponse(ctx *apptheory.Context, resourceMetadataURL string, scopes string) *apptheory.Response {
	return unauthorizedMCPResponseWithOptions(ctx, mcpAuthorizationChallengeOptions{
		Error:            "invalid_token",
		ResourceMetadata: resourceMetadataURL,
		Scope:            scopes,
	}, map[string]any{
		"source":          "lesser_body",
		"reauthorize":     true,
		"authAction":      "reauthorize",
		"refreshRequired": true,
		"reason":          "mcp_session_not_found",
	})
}

func genericSessionNotFoundMCPResponse(ctx *apptheory.Context, resourceMetadataURL string, scopes string) *apptheory.Response {
	return unauthorizedMCPResponseWithOptions(ctx, mcpAuthorizationChallengeOptions{
		Error:            "invalid_token",
		ResourceMetadata: resourceMetadataURL,
		Scope:            scopes,
	}, nil)
}

func unauthorizedMCPResponseWithOptions(
	ctx *apptheory.Context,
	challengeOptions mcpAuthorizationChallengeOptions,
	details map[string]any,
) *apptheory.Response {
	headers := map[string][]string{
		"www-authenticate": {mcpAuthorizationChallenge(challengeOptions)},
		"content-type":     {"application/json; charset=utf-8"},
	}

	errBody := map[string]any{
		"code":    "app.unauthorized",
		"message": "unauthorized",
	}
	if len(details) > 0 {
		errBody["details"] = details
	}
	if ctx != nil && strings.TrimSpace(ctx.RequestID) != "" {
		errBody["request_id"] = ctx.RequestID
	}

	body, err := json.Marshal(map[string]any{"error": errBody})
	if err != nil {
		body = []byte(`{"error":{"code":"app.internal","message":"internal error"}}`)
		headers["content-type"] = []string{"application/json; charset=utf-8"}
		return &apptheory.Response{
			Status:  500,
			Headers: headers,
			Body:    body,
		}
	}

	return &apptheory.Response{
		Status:  401,
		Headers: headers,
		Body:    body,
	}
}

func insufficientScopeMCPResponse(ctx *apptheory.Context, grantedScopes []string, requiredScopes []string) *apptheory.Response {
	headers := map[string][]string{}
	headers["www-authenticate"] = []string{mcpAuthorizationChallenge(mcpAuthorizationChallengeOptions{
		Error:            "insufficient_scope",
		ResourceMetadata: protectedResourceMetadataURLForRequest(ctx),
		Scope:            joinScopeChallenge(grantedScopes, requiredScopes),
		ErrorDescription: insufficientScopeDescription(requiredScopes),
	})}
	headers["content-type"] = []string{"application/json; charset=utf-8"}

	errBody := map[string]any{
		"code":    "app.forbidden",
		"message": "forbidden",
		"details": map[string]any{
			"source":          "lesser_body",
			"reauthorize":     true,
			"authAction":      "authorize",
			"refreshRequired": false,
			"reason":          "insufficient_scope",
			"requiredScopes":  append([]string(nil), requiredScopes...),
			"grantedScopes":   append([]string(nil), grantedScopes...),
		},
	}
	if ctx != nil && strings.TrimSpace(ctx.RequestID) != "" {
		errBody["request_id"] = ctx.RequestID
	}

	body, err := json.Marshal(map[string]any{"error": errBody})
	if err != nil {
		body = []byte(`{"error":{"code":"app.internal","message":"internal error"}}`)
		headers["content-type"] = []string{"application/json; charset=utf-8"}
		return &apptheory.Response{
			Status:  500,
			Headers: headers,
			Body:    body,
		}
	}

	return &apptheory.Response{
		Status:  403,
		Headers: headers,
		Body:    body,
	}
}

func mcpAuthorizationChallenge(options mcpAuthorizationChallengeOptions) string {
	params := make([]string, 0, 4)
	if value := strings.TrimSpace(options.Error); value != "" {
		params = append(params, fmt.Sprintf(`error="%s"`, escapeWWWAuthenticateValue(value)))
	}
	if value := strings.TrimSpace(options.ResourceMetadata); value != "" {
		params = append(params, fmt.Sprintf(`resource_metadata="%s"`, escapeWWWAuthenticateValue(value)))
	}
	if value := strings.TrimSpace(options.Scope); value != "" {
		params = append(params, fmt.Sprintf(`scope="%s"`, escapeWWWAuthenticateValue(value)))
	}
	if value := strings.TrimSpace(options.ErrorDescription); value != "" {
		params = append(params, fmt.Sprintf(`error_description="%s"`, escapeWWWAuthenticateValue(value)))
	}
	if len(params) == 0 {
		return "Bearer"
	}
	return "Bearer " + strings.Join(params, ", ")
}

func joinScopeChallenge(grantedScopes []string, requiredScopes []string) string {
	seen := make(map[string]struct{}, len(grantedScopes)+len(requiredScopes))
	ordered := make([]string, 0, len(grantedScopes)+len(requiredScopes))

	for _, scope := range grantedScopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		ordered = append(ordered, scope)
	}
	for _, scope := range requiredScopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		ordered = append(ordered, scope)
	}

	return strings.Join(ordered, " ")
}

func insufficientScopeDescription(requiredScopes []string) string {
	scopes := joinScopeChallenge(nil, requiredScopes)
	switch len(strings.Fields(scopes)) {
	case 0:
		return "Additional authorization required"
	case 1:
		return "Additional " + scopes + " permission required"
	default:
		return "Additional scopes required: " + scopes
	}
}

func escapeWWWAuthenticateValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
