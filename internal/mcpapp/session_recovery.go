package mcpapp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
)

type sessionNotFoundResponseFactory func(*apptheory.Context, string, string) *apptheory.Response

// WithOAuthSessionRecovery maps AppTheory's session-not-found transport
// response to an OAuth invalid_token challenge, but only after Body has
// authenticated an OAuth principal. Non-OAuth callers keep AppTheory's
// spec-shaped 404 response.
func WithOAuthSessionRecovery(
	next apptheory.Handler,
	resourceMetadataURL func(*apptheory.Context) string,
	scopes string,
) apptheory.Handler {
	return withOAuthSessionRecovery(next, resourceMetadataURL, scopes, genericSessionNotFoundMCPResponse)
}

func withActorOAuthSessionRecovery(next apptheory.Handler) apptheory.Handler {
	return withOAuthSessionRecovery(
		next,
		protectedResourceMetadataURLForRequest,
		MCPAuthorizationScopes,
		actorSessionNotFoundMCPResponse,
	)
}

func withOAuthSessionRecovery(
	next apptheory.Handler,
	resourceMetadataURL func(*apptheory.Context) string,
	scopes string,
	responseFactory sessionNotFoundResponseFactory,
) apptheory.Handler {
	if next == nil {
		return nil
	}
	if responseFactory == nil {
		responseFactory = genericSessionNotFoundMCPResponse
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		resp, err := next(ctx)
		if err != nil || !isOAuthPrincipal(ctx) || !isMCPSessionNotFoundResponse(resp) {
			return resp, err
		}

		metadataURL := ""
		if resourceMetadataURL != nil {
			metadataURL = strings.TrimSpace(resourceMetadataURL(ctx))
		}
		slog.WarnContext(ctx.Context(), "mcp authorization rejected",
			"request_id", strings.TrimSpace(ctx.RequestID),
			"principal_type", string(auth.PrincipalTypeOAuthToken),
			"reason", "mcp_session_not_found",
		)
		return responseFactory(ctx, metadataURL, strings.TrimSpace(scopes)), nil
	}
}

func isOAuthPrincipal(ctx *apptheory.Context) bool {
	principal := auth.PrincipalFromContext(ctx)
	return principal != nil && principal.Type == auth.PrincipalTypeOAuthToken
}

func isMCPSessionNotFoundResponse(resp *apptheory.Response) bool {
	if resp == nil || resp.Status != http.StatusNotFound || resp.BodyReader != nil {
		return false
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &body); err != nil || len(body) != 1 {
		return false
	}
	rawError, ok := body["error"]
	if !ok {
		return false
	}
	var message string
	if err := json.Unmarshal(rawError, &message); err != nil {
		return false
	}
	return message == "session not found"
}
