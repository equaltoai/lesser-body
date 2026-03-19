package mcpserver

import (
	"errors"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

type lesserAuthFailure struct {
	Code    string
	Message string
	Status  int
	Details map[string]any
}

func lesserAuthFailureFromError(err error) *lesserAuthFailure {
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
		return &lesserAuthFailure{
			Code:    "unauthorized",
			Message: "OAuth token expired or invalid; refresh or re-authorize and retry",
			Status:  apiErr.Status,
			Details: details,
		}
	case 403:
		details["authAction"] = "reauthorize"
		details["refreshRequired"] = false
		details["insufficientAccess"] = true
		return &lesserAuthFailure{
			Code:    "forbidden",
			Message: "OAuth token is authenticated but lacks access to this Lesser API surface",
			Status:  apiErr.Status,
			Details: details,
		}
	default:
		return nil
	}
}

func (f *lesserAuthFailure) payload() map[string]any {
	if f == nil {
		return nil
	}
	payload := map[string]any{
		"code":    f.Code,
		"message": f.Message,
		"status":  f.Status,
	}
	if len(f.Details) > 0 {
		payload["details"] = f.Details
	}
	return payload
}

func lesserToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	failure := lesserAuthFailureFromError(err)
	if failure == nil {
		return nil, err
	}
	return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
}

func lesserResourceContentsFromError(uri string, err error) ([]mcpruntime.ResourceContent, error) {
	failure := lesserAuthFailureFromError(err)
	if failure == nil {
		return nil, err
	}
	return resourceJSON(uri, map[string]any{
		"error": failure.payload(),
	})
}
