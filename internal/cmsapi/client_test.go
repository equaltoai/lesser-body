package cmsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

func TestExecutePropagatesAuthAndDecodesSuccess(t *testing.T) {
	var gotAuth string
	var gotOperation Operation

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/graphql" {
			t.Fatalf("path = %s, want /api/graphql", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotOperation); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"articleDraft":{"id":"draft-123","title":"Hello"}},"extensions":{"cost":1}}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	resp, err := client.Execute(context.Background(), " token-abc ", Operation{
		Query:         " query Draft($id: ID!) { articleDraft(id: $id) { id title } } ",
		OperationName: "Draft",
		Variables:     map[string]any{"id": "draft-123"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotAuth != "Bearer token-abc" {
		t.Fatalf("Authorization = %q, want Bearer token-abc", gotAuth)
	}
	if gotOperation.Query != "query Draft($id: ID!) { articleDraft(id: $id) { id title } }" {
		t.Fatalf("query = %q", gotOperation.Query)
	}
	if gotOperation.OperationName != "Draft" {
		t.Fatalf("operationName = %q", gotOperation.OperationName)
	}
	if gotOperation.Variables["id"] != "draft-123" {
		t.Fatalf("variables = %#v", gotOperation.Variables)
	}

	var data struct {
		ArticleDraft struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"articleDraft"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data.ArticleDraft.ID != "draft-123" || data.ArticleDraft.Title != "Hello" {
		t.Fatalf("decoded data = %#v", data.ArticleDraft)
	}
	if resp.Extensions["cost"].(float64) != 1 {
		t.Fatalf("extensions = %#v", resp.Extensions)
	}
}

func TestExecuteReturnsGraphQLErrorsWithResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"articleDraft":null},"errors":[{"message":"draft not found","path":["articleDraft"],"extensions":{"code":"NOT_FOUND"}}]}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	resp, err := client.Execute(context.Background(), "token", Operation{Query: "query { articleDraft(id: \"missing\") { id } }"})
	if err == nil {
		t.Fatalf("expected GraphQLErrors")
	}
	var gqlErr *GraphQLErrors
	if !errors.As(err, &gqlErr) {
		t.Fatalf("expected *GraphQLErrors, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "draft not found") {
		t.Fatalf("error string = %q", err.Error())
	}
	if resp == nil || len(resp.Errors) != 1 {
		t.Fatalf("response/errors = %#v", resp)
	}
	if string(gqlErr.Data) != `{"articleDraft":null}` {
		t.Fatalf("error data = %s", string(gqlErr.Data))
	}
}

func TestExecuteReturnsHTTPAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Execute(context.Background(), "token", Operation{Query: "query { viewer { id } }"})
	if err == nil {
		t.Fatalf("expected HTTP error")
	}
	var apiErr *lesserapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *lesserapi.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", apiErr.Status)
	}
}

func TestExecuteValidatesQueryBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Execute(context.Background(), "token", Operation{Query: " \t\n "})
	if err == nil || !strings.Contains(err.Error(), "graphql query is required") {
		t.Fatalf("expected query validation error, got %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestExecuteRejectsMalformedGraphQLResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Execute(context.Background(), "token", Operation{Query: "query { viewer { id } }"})
	if err == nil || !strings.Contains(err.Error(), "unmarshal graphql response") {
		t.Fatalf("expected response unmarshal error, got %v", err)
	}
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	t.Setenv("LESSER_API_BASE_URL", baseURL)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	client, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	return client
}
