package mcpserver

import (
	"context"
	"errors"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func resourceChannels(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	if _, err := requireOAuthBearer(ctx); err != nil {
		return authResourceContentsFromError("agent://channels", err)
	}

	payload, err := whoamiChannelsPayload(ctx)
	if err != nil {
		return resourceJSON("agent://channels", map[string]any{
			"error": identityErrorPayload(err),
		})
	}

	return resourceJSON("agent://channels", payload)
}

func resourceChannelPreferences(ctx context.Context) ([]mcpruntime.ResourceContent, error) {
	if _, err := requireOAuthBearer(ctx); err != nil {
		return authResourceContentsFromError("agent://channels/preferences", err)
	}

	payload, err := whoamiChannelsPayload(ctx)
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
		return authErrorPayload(nil)
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

	return authErrorPayload(err)
}
