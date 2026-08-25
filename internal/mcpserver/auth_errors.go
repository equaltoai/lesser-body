package mcpserver

import (
	"errors"

	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

type mcpAuthFailure struct {
	Code    string
	Message string
	Status  int
	Details map[string]any
}

func (f *mcpAuthFailure) Error() string {
	if f == nil || f.Message == "" {
		return "error"
	}
	return f.Message
}

func (f *mcpAuthFailure) payload() map[string]any {
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

func newLocalAuthFailure(message string, reason string) *mcpAuthFailure {
	details := map[string]any{
		"source":          "lesser_body",
		"reauthorize":     true,
		"authAction":      "authorize",
		"refreshRequired": false,
	}
	if reason != "" {
		details["reason"] = reason
	}
	return &mcpAuthFailure{
		Code:    "unauthorized",
		Message: message,
		Status:  401,
		Details: details,
	}
}

func oauthBearerRequiredFailure(reason string) *mcpAuthFailure {
	return newLocalAuthFailure("OAuth bearer token required", reason)
}

func soulAuthFailureFromError(err error) *mcpAuthFailure {
	if err == nil {
		return nil
	}

	var apiErr *soulapi.APIError
	if !errors.As(err, &apiErr) {
		return nil
	}

	details := map[string]any{
		"source":       "soul_api",
		"reauthorize":  true,
		"upstreamCode": apiErr.Status,
	}
	if retryAfter := apiErr.RetryAfterSeconds(); retryAfter > 0 {
		details["retryAfterSeconds"] = retryAfter
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
			Message: "OAuth token expired or invalid for the soul API; refresh or re-authorize and retry",
			Status:  apiErr.Status,
			Details: details,
		}
	default:
		return nil
	}
}

func mcpAuthFailureFromError(err error) *mcpAuthFailure {
	if err == nil {
		return nil
	}

	var failure *mcpAuthFailure
	if errors.As(err, &failure) {
		return failure
	}
	if failure := lesserAuthFailureFromError(err); failure != nil {
		return failure
	}
	if failure := soulAuthFailureFromError(err); failure != nil {
		return failure
	}
	return nil
}

func authToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	failure := mcpAuthFailureFromError(err)
	if failure == nil {
		return nil, err
	}
	return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
}

func authResourceContentsFromError(uri string, err error) ([]mcpruntime.ResourceContent, error) {
	failure := mcpAuthFailureFromError(err)
	if failure == nil {
		return nil, err
	}
	return resourceJSON(uri, map[string]any{
		"error": failure.payload(),
	})
}

func authErrorPayload(err error) map[string]any {
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return failure.payload()
	}
	if err == nil {
		return map[string]any{
			"code":    "upstream_error",
			"message": "error",
			"status":  500,
		}
	}
	return map[string]any{
		"code":    "upstream_error",
		"message": err.Error(),
		"status":  500,
	}
}
