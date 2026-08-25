package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

func TestRegisteredToolNormalizesEveryHandlerError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantCode   string
		wantStatus int
		forbidText string
	}{
		{
			name:       "invalid params",
			err:        invalidParams("missing account_id"),
			wantCode:   "invalid_params",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "lesser not found",
			err: &lesserapi.APIError{
				Status: http.StatusNotFound,
				Body:   []byte(`{"error":"account not found","error_code":"NOT_FOUND"}`),
			},
			wantCode:   "NOT_FOUND",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unclassified internal failure",
			err:        errors.New("raw transport secret must not escape"),
			wantCode:   "internal_error",
			wantStatus: http.StatusInternalServerError,
			forbidText: "raw transport secret",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := mcpruntime.NewToolRegistry()
			if err := registerTool(registry, mcpruntime.ToolDef{
				Name:        "normalization_fixture",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}, func(context.Context, json.RawMessage) (*mcpruntime.ToolResult, error) {
				return nil, tc.err
			}); err != nil {
				t.Fatalf("registerTool: %v", err)
			}

			result, err := registry.Call(context.Background(), "normalization_fixture", json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("Call returned raw handler error: %v", err)
			}
			payload := toolErrorPayloadForTest(t, result)
			if payload["code"] != tc.wantCode || intFromAny(payload["status"]) != tc.wantStatus {
				t.Fatalf("error payload = %#v, want code=%q status=%d", payload, tc.wantCode, tc.wantStatus)
			}
			if tc.forbidText != "" {
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), tc.forbidText) {
					t.Fatalf("raw handler text escaped in result: %s", encoded)
				}
			}
		})
	}
}

func TestArticleDraftGraphQLErrorUsesLesserExtensions(t *testing.T) {
	result, err := articleDraftToolResultFromError("article_draft_update", &cmsapi.GraphQLErrors{Errors: []cmsapi.Error{{
		Message: "title is required",
		Extensions: map[string]any{
			"code":        "VALIDATION_FAILED",
			"http_status": float64(http.StatusBadRequest),
		},
	}}})
	if err != nil {
		t.Fatalf("articleDraftToolResultFromError: %v", err)
	}
	payload := toolErrorPayloadForTest(t, result)
	if payload["code"] != "VALIDATION_FAILED" || intFromAny(payload["status"]) != http.StatusBadRequest {
		t.Fatalf("error payload = %#v, want Lesser GraphQL code/status", payload)
	}
}

func toolErrorPayloadForTest(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("result = %#v, want MCP tool error", result)
	}
	payload, _ := result.StructuredContent["error"].(map[string]any)
	if payload == nil {
		t.Fatalf("structuredContent.error = %#v, want object", result.StructuredContent["error"])
	}
	return payload
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}
