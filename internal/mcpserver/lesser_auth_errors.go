package mcpserver

import (
	"errors"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func lesserAuthFailureFromError(err error) *mcpAuthFailure {
	if err == nil {
		return nil
	}

	var apiErr *lesserapi.APIError
	if !errors.As(err, &apiErr) {
		return nil
	}

	details := map[string]any{
		"source":       "lesser_oauth_passthrough",
		"reauthorize":  true,
		"upstreamCode": apiErr.Status,
	}
	if message, parsed := commExtractAPIErrorMessage(apiErr.Body); parsed != nil {
		details["apiError"] = parsed
		details["upstreamMessage"] = message
	}

	switch apiErr.Status {
	case 401:
		details["authAction"] = "refresh_or_reauthorize"
		details["refreshRequired"] = true
		return &mcpAuthFailure{
			Code:    "unauthorized",
			Message: "OAuth token expired or invalid; refresh or re-authorize and retry",
			Status:  apiErr.Status,
			Details: details,
		}
	case 403:
		details["authAction"] = "reauthorize"
		details["refreshRequired"] = false
		details["insufficientAccess"] = true
		return &mcpAuthFailure{
			Code:    "forbidden",
			Message: "OAuth token is authenticated but lacks access to this Lesser API surface",
			Status:  apiErr.Status,
			Details: details,
		}
	default:
		return nil
	}
}

func lesserToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	return authToolResultFromError(err)
}

func lesserResourceContentsFromError(uri string, err error) ([]mcpruntime.ResourceContent, error) {
	return authResourceContentsFromError(uri, err)
}
