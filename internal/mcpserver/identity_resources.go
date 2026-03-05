package mcpserver

import (
	"context"
	"errors"

	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func resourceChannels(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return resourceJSON("agent://channels", map[string]any{
			"error": map[string]any{
				"code":    "unauthorized",
				"message": err.Error(),
				"status":  401,
			},
		})
	}

	payload, err := whoamiChannelsPayload(ctx, token)
	if err != nil {
		return resourceJSON("agent://channels", map[string]any{
			"error": identityErrorPayload(err),
		})
	}

	return resourceJSON("agent://channels", payload)
}

func resourceChannelPreferences(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return resourceJSON("agent://channels/preferences", map[string]any{
			"error": map[string]any{
				"code":    "unauthorized",
				"message": err.Error(),
				"status":  401,
			},
		})
	}

	payload, err := whoamiChannelsPayload(ctx, token)
	if err != nil {
		return resourceJSON("agent://channels/preferences", map[string]any{
			"error": identityErrorPayload(err),
		})
	}

	prefs, _ := payload["contactPreferences"].(map[string]any)
	if prefs == nil {
		prefs = map[string]any{}
	}
	return resourceJSON("agent://channels/preferences", prefs)
}

func identityErrorPayload(err error) map[string]any {
	if err == nil {
		return map[string]any{
			"code":    "upstream_error",
			"message": "error",
			"status":  500,
		}
	}

	var userErr *toolUserError
	if errors.As(err, &userErr) {
		payload := map[string]any{
			"code":    userErr.Code,
			"message": userErr.Message,
			"status":  userErr.Status,
		}
		if len(userErr.Details) > 0 {
			payload["details"] = userErr.Details
		}
		return payload
	}

	var apiErr *soulapi.APIError
	if errors.As(err, &apiErr) {
		code := identityErrorCodeForStatus(apiErr.Status)
		message, parsed := commExtractAPIErrorMessage(apiErr.Body)
		payload := map[string]any{
			"code":    code,
			"message": message,
			"status":  apiErr.Status,
		}
		details := map[string]any{}
		if parsed != nil {
			details["apiError"] = parsed
		}
		if retryAfter := apiErr.RetryAfterSeconds(); retryAfter > 0 {
			details["retryAfterSeconds"] = retryAfter
		}
		if len(details) > 0 {
			payload["details"] = details
		}
		return payload
	}

	return map[string]any{
		"code":    "upstream_error",
		"message": err.Error(),
		"status":  500,
	}
}
