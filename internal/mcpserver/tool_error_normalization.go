package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

// normalizedToolResultFromError is the last boundary before a Ka tool handler
// returns to AppTheory. Handler errors must not escape here: AppTheory correctly
// treats such errors as JSON-RPC server failures, while Body's public contract
// requires actionable tool failures in the shared structuredContent.error shape.
func normalizedToolResultFromError(toolName string, err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}

	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}

	var paramsErr *InvalidParamsError
	if errors.As(err, &paramsErr) {
		return toolErrorResult("invalid_params", paramsErr.Error(), http.StatusBadRequest, map[string]any{
			"source": "lesser_body",
			"tool":   toolName,
		})
	}

	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		code, upstreamMessage := lesserAPIErrorFields(apiErr)
		if code == "" {
			code = toolErrorCodeForHTTPStatus(apiErr.Status)
		}
		message := "Lesser API request failed"
		if upstreamMessage != "" {
			message = upstreamMessage
		}
		details := map[string]any{
			"source":       "lesser_api",
			"tool":         toolName,
			"upstreamCode": apiErr.Status,
		}
		return toolErrorResult(code, message, normalizedUpstreamStatus(apiErr.Status), details)
	}

	switch {
	case errors.Is(err, context.Canceled):
		return toolErrorResult("request_cancelled", "Tool request was cancelled", 499, map[string]any{
			"source": "lesser_body",
			"tool":   toolName,
		})
	case errors.Is(err, context.DeadlineExceeded):
		return toolErrorResult("upstream_timeout", "Tool request timed out", http.StatusGatewayTimeout, map[string]any{
			"source": "lesser_body",
			"tool":   toolName,
		})
	default:
		// Do not reflect arbitrary error text. It can contain transport payloads,
		// bearer material, recipient PII, or implementation details.
		return toolErrorResult("internal_error", "Tool request failed", http.StatusInternalServerError, map[string]any{
			"source": "lesser_body",
			"tool":   toolName,
		})
	}
}

func lesserAPIErrorFields(apiErr *lesserapi.APIError) (string, string) {
	if apiErr == nil || len(apiErr.Body) == 0 {
		return "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal(apiErr.Body, &payload); err != nil {
		return "", ""
	}
	code := firstStringValue(payload, "error_code", "code")
	message := firstStringValue(payload, "message", "error")
	if len(message) > 512 {
		message = message[:512] + "…"
	}
	return code, message
}

func firstStringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizedUpstreamStatus(status int) int {
	if status >= 400 && status <= 599 {
		return status
	}
	return http.StatusBadGateway
}

func toolErrorCodeForHTTPStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusGone:
		return "gone"
	case http.StatusRequestEntityTooLarge:
		return "response_too_large"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		return "upstream_error"
	}
}
