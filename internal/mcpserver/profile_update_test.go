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

func TestProfileUpdateOmitsProfileFlagsForCanonicalPatch(t *testing.T) {
	var patchBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "PATCH /api/v1/accounts/update_credentials":
			if err := json.NewDecoder(r.Body).Decode(&patchBody); err != nil {
				t.Fatalf("decode patch body: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"alice","display_name":"Della Marlowe","note":"Desk smoke","locked":true,"bot":true,"discoverable":true}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "alice"}, "token-123")
	res, err := handleProfileUpdate(ctx, json.RawMessage(`{"display_name":" Della Marlowe ","bio":" Desk smoke ","avatar_url":"https://example.com/avatar.png"}`))
	if err != nil {
		t.Fatalf("handleProfileUpdate: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("profile_update result = %+v", res)
	}
	if patchBody["display_name"] != "Della Marlowe" || patchBody["note"] != "Desk smoke" || patchBody["avatar"] != "https://example.com/avatar.png" {
		t.Fatalf("patch body text fields = %+v", patchBody)
	}
	for _, key := range []string{"locked", "bot", "discoverable"} {
		if _, ok := patchBody[key]; ok {
			t.Fatalf("patch body should omit Lesser-owned optional flag %s, got %+v", key, patchBody)
		}
	}
}

func TestProfileUpdateMapsUpstreamHTTPFailureToToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "PATCH /api/v1/accounts/update_credentials":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"profile_update_failed","error_description":"upstream rejected profile update"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "alice"}, "token-123")
	res, err := handleProfileUpdate(ctx, json.RawMessage(`{"display_name":"Della"}`))
	if err != nil {
		t.Fatalf("handleProfileUpdate should return MCP tool error result, got err %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected tool error result, got %+v", res)
	}
	errorPayload, ok := res.StructuredContent["error"].(map[string]any)
	if !ok {
		t.Fatalf("structured error = %+v", res.StructuredContent)
	}
	if errorPayload["code"] != "lesser_profile_update_http_error" || errorPayload["status"] != float64(http.StatusInternalServerError) && errorPayload["status"] != http.StatusInternalServerError {
		t.Fatalf("error payload = %+v", errorPayload)
	}
	details, ok := errorPayload["details"].(map[string]any)
	if !ok || details["source"] != "lesser_profile_update" || details["upstreamCode"] != float64(http.StatusInternalServerError) && details["upstreamCode"] != http.StatusInternalServerError {
		t.Fatalf("error details = %+v", errorPayload)
	}
}
