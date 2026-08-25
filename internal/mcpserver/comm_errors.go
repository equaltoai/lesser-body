package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-body/internal/soulapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

type commOAuthErrorPayload struct {
	Error            string
	ErrorDescription string
	Scope            string
	Raw              map[string]any
}

func toolErrorResultPayload(payload map[string]any) (*mcpruntime.ToolResult, error) {
	if payload == nil {
		payload = map[string]any{
			"code":    "unknown_error",
			"message": "error",
		}
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tool error: %w", err)
	}

	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: string(b),
		}},
		IsError: true,
		StructuredContent: map[string]any{
			"error": payload,
		},
	}, nil
}

func toolErrorResult(code string, message string, status int, details map[string]any) (*mcpruntime.ToolResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "unknown_error"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "error"
	}

	payload := map[string]any{
		"code":    code,
		"message": message,
	}
	if status != 0 {
		payload["status"] = status
	}
	if len(details) > 0 {
		payload["details"] = details
	}

	return toolErrorResultPayload(payload)
}

func commToolResultFromError(err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}

	var apiErr *soulapi.APIError
	if errors.As(err, &apiErr) {
		if res, handled, resErr := commToolResultFromOAuthContract(apiErr); handled {
			return res, resErr
		}
	}

	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}

	if errors.As(err, &apiErr) {
		code := commErrorCodeForStatus(apiErr.Status)
		message, parsed := commExtractAPIErrorMessage(apiErr.Body)

		details := map[string]any{}
		if retryAfter := apiErr.RetryAfterSeconds(); retryAfter > 0 {
			details["retryAfterSeconds"] = retryAfter
		}
		if parsed != nil {
			details["apiError"] = parsed
		}

		return toolErrorResult(code, message, apiErr.Status, details)
	}

	return toolErrorResult("upstream_error", err.Error(), 0, nil)
}

func commToolResultFromOAuthContract(apiErr *soulapi.APIError) (*mcpruntime.ToolResult, bool, error) {
	oauthPayload := parseCommOAuthErrorPayload(apiErr.Body)
	if oauthPayload == nil {
		return nil, false, nil
	}

	action := commClientActionForOAuthError(apiErr.Status, oauthPayload.Error)
	details := map[string]any{
		"source":          "soul_api",
		"authAction":      action,
		"refreshRequired": action == "refresh",
		"reauthorize":     action == "reauth",
		"upstreamCode":    apiErr.Status,
		"apiError":        oauthPayload.Raw,
	}
	if retryAfter := apiErr.RetryAfterSeconds(); retryAfter > 0 {
		details["retryAfterSeconds"] = retryAfter
	}

	message := oauthPayload.ErrorDescription
	if message == "" {
		message = oauthPayload.Error
	}

	payload := map[string]any{
		"code":    oauthPayload.Error,
		"message": message,
		"status":  apiErr.Status,
		"error":   oauthPayload.Error,
		"details": details,
	}
	if oauthPayload.ErrorDescription != "" {
		payload["error_description"] = oauthPayload.ErrorDescription
	}
	if oauthPayload.Scope != "" {
		payload["scope"] = oauthPayload.Scope
	}

	res, err := toolErrorResultPayload(payload)
	return res, true, err
}

func parseCommOAuthErrorPayload(body []byte) *commOAuthErrorPayload {
	raw := strings.TrimSpace(string(body))
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return nil
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}

	errorCode := extractString(parsed, "error")
	if errorCode == "" {
		return nil
	}

	return &commOAuthErrorPayload{
		Error:            errorCode,
		ErrorDescription: extractString(parsed, "error_description"),
		Scope:            extractString(parsed, "scope"),
		Raw:              parsed,
	}
}

func commClientActionForOAuthError(status int, errorCode string) string {
	switch {
	case status == 401 && errorCode == "invalid_token":
		return "refresh"
	case status == 403 && errorCode == "insufficient_scope":
		return "fail"
	case status == 400 && errorCode == "invalid_grant":
		return "reauth"
	case status == 401 && errorCode == "invalid_client":
		return "reconfigure"
	case status == 400 && (errorCode == "unauthorized_client" || errorCode == "invalid_scope"):
		return "reconfigure"
	case status == 429 && errorCode == "slow_down":
		return "backoff"
	case status >= 500 && errorCode == "server_error":
		return "retry"
	case status == 401:
		return "refresh"
	case status == 403:
		return "fail"
	case status == 429:
		return "backoff"
	case status >= 500:
		return "retry"
	default:
		return "fail"
	}
}

func commErrorCodeForStatus(status int) string {
	switch status {
	case 400, 422:
		return "invalid_request"
	case 401:
		return "unauthorized"
	case 403:
		// For comm/send this usually indicates a boundary or policy block.
		return "boundary_violation"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 429:
		return "rate_limited"
	default:
		if status >= 500 {
			return "upstream_error"
		}
		return "unknown_error"
	}
}

func commExtractAPIErrorMessage(body []byte) (string, map[string]any) {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return "request failed", nil
	}

	if strings.HasPrefix(raw, "{") {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			// Common shapes:
			// { "error": { "message": "..." } }
			// { "message": "..." }
			if msg := extractString(parsed, "message"); msg != "" {
				return msg, parsed
			}
			if errObj, ok := parsed["error"].(map[string]any); ok {
				if msg := extractString(errObj, "message"); msg != "" {
					return msg, parsed
				}
				if msg := extractString(errObj, "error"); msg != "" {
					return msg, parsed
				}
			}
			return raw, parsed
		}
	}

	if len(raw) > 512 {
		raw = raw[:512] + "…"
	}
	return raw, nil
}

func extractString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}
