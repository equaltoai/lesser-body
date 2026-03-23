package mcpapp

import (
	"encoding/json"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	oauthruntime "github.com/theory-cloud/apptheory/runtime/oauth"

	"github.com/equaltoai/lesser-body/internal/auth"
)

func WithMCPAuthorization(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	authHook := auth.Hook(nil)

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		identity, err := authHook(ctx)
		if err != nil {
			return unauthorizedMCPResponse(ctx), nil
		}
		if strings.TrimSpace(identity) == "" {
			return unauthorizedMCPResponse(ctx), nil
		}
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.Type == auth.PrincipalTypeOAuthToken {
			if err := auth.ValidateTokenAudience(principal.Claims, mcpEndpointForRequest(ctx)); err != nil {
				return unauthorizedMCPResponse(ctx), nil
			}
		}
		ctx.AuthIdentity = identity
		return next(ctx)
	}
}

func unauthorizedMCPResponse(ctx *apptheory.Context) *apptheory.Response {
	headers := map[string][]string{}
	if resourceMetadataURL := protectedResourceMetadataURLForRequest(ctx); resourceMetadataURL != "" {
		headers["www-authenticate"] = []string{oauthruntime.ProtectedResourceWWWAuthenticate(resourceMetadataURL)}
	}
	headers["content-type"] = []string{"application/json; charset=utf-8"}

	errBody := map[string]any{
		"code":    "app.unauthorized",
		"message": "unauthorized",
		"details": map[string]any{
			"source":          "lesser_body",
			"reauthorize":     true,
			"authAction":      "authorize",
			"refreshRequired": false,
			"reason":          "missing_or_invalid_bearer",
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
		Status:  401,
		Headers: headers,
		Body:    body,
	}
}
