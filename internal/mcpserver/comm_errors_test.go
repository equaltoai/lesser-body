package mcpserver

import (
	"net/http"
	"testing"

	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestCommToolResultFromError_MapsCommAPIErrors(t *testing.T) {
	res, err := commToolResultFromError(&soulapi.APIError{
		Status: 403,
		Body:   []byte(`{"error":{"message":"blocked by policy"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected isError tool result, got %+v", res)
	}
	errPayload, ok := res.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent.error, got %+v", res.StructuredContent)
	}
	if errPayload["code"] != "boundary_violation" {
		t.Fatalf("expected boundary_violation code, got %v", errPayload["code"])
	}
	if errPayload["status"] != 403 {
		t.Fatalf("expected status 403, got %v", errPayload["status"])
	}

	res, err = commToolResultFromError(&soulapi.APIError{
		Status:  429,
		Body:    []byte(`rate limited`),
		Headers: http.Header{"Retry-After": []string{"12"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errPayload, ok = res.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent.error, got %+v", res.StructuredContent)
	}
	if errPayload["code"] != "rate_limited" {
		t.Fatalf("expected rate_limited code, got %v", errPayload["code"])
	}
	details, _ := errPayload["details"].(map[string]any)
	if details["retryAfterSeconds"] != 12 {
		t.Fatalf("expected retryAfterSeconds=12, got %v", details["retryAfterSeconds"])
	}
}
