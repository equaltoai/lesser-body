package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

func TestPostGetReturnsStatusOnceWithoutSelfExpansion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/statuses/post-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"post-1",
			"url":"https://example.com/@alice/post-1",
			"created_at":"2026-05-17T17:00:00Z",
			"visibility":"public",
			"content":"full status content",
			"account":{"id":"acct-1","acct":"alice@example.com"}
		}`))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	ctx := auth.InjectToolContext(
		context.Background(),
		&auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "alice"},
		"oauth-token",
	)
	result, err := handlePostGet(ctx, json.RawMessage(`{"id":"post-1","view":"standard"}`))
	if err != nil {
		t.Fatalf("handlePostGet: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("unexpected post_get result: %+v", result)
	}

	data := normalizedToolResultData(t, result.StructuredContent)
	if _, ok := data["statusRef"]; ok {
		t.Fatalf("post_get must not duplicate status as statusRef: %+v", data)
	}
	status, ok := data["status"].(map[string]any)
	if !ok || status["id"] != "post-1" || status["content"] != "full status content" {
		t.Fatalf("post_get status = %+v", data["status"])
	}
	if containsPostGetExpansion(data) {
		t.Fatalf("post_get must not point back at itself: %+v", data)
	}

	text := toolResultTextJSON(t, result)
	if _, ok := text["statusRef"]; ok || containsPostGetExpansion(text) {
		t.Fatalf("post_get text result contains duplicate/self expansion: %+v", text)
	}
}

func TestPostGetOutputSchemaAdvertisesStatusOnce(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(postGetOutputSchema(), &schema); err != nil {
		t.Fatalf("unmarshal post_get output schema: %v", err)
	}
	properties, _ := schema["properties"].(map[string]any)
	dataSchema, _ := properties["data"].(map[string]any)
	dataProperties, _ := dataSchema["properties"].(map[string]any)
	if _, ok := dataProperties["status"]; !ok {
		t.Fatalf("post_get schema must advertise status: %+v", dataProperties)
	}
	if _, ok := dataProperties["statusRef"]; ok {
		t.Fatalf("post_get schema must not advertise duplicate statusRef: %+v", dataProperties)
	}
	required, _ := dataSchema["required"].([]any)
	for _, name := range required {
		if name == "statusRef" {
			t.Fatalf("post_get schema still requires statusRef: %+v", required)
		}
	}
}

func normalizedToolResultData(t *testing.T, structured map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(structured["data"])
	if err != nil {
		t.Fatalf("marshal structured data: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("unmarshal structured data: %v", err)
	}
	return data
}

func containsPostGetExpansion(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if typed["tool"] == "post_get" {
			return true
		}
		for _, child := range typed {
			if containsPostGetExpansion(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsPostGetExpansion(child) {
				return true
			}
		}
	}
	return false
}
