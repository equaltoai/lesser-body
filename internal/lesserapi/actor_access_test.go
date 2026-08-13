package lesserapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetActorAccessSendsContractAndDecodesOwner(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"actor":"arch","relationship":"owner","authorized":true,"acted_by":"owner"}`))
	}))
	defer server.Close()

	client := newActorAccessTestClient(t, server)
	resp, err := client.GetActorAccess(context.Background(), "arch", "caller-token")
	if err != nil {
		t.Fatalf("GetActorAccess: %v", err)
	}
	if resp == nil || resp.Relationship != "owner" || !resp.Authorized || resp.Actor != "arch" || resp.ActedBy != "owner" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if gotPath != "/api/v1/agents/arch/access" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer caller-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestGetActorAccessDecodesGrantee(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"actor":"arch","relationship":"grantee","authorized":true,"acted_by":"alice"}`))
	}))
	defer server.Close()

	resp, err := newActorAccessTestClient(t, server).GetActorAccess(context.Background(), "arch", "caller-token")
	if err != nil {
		t.Fatalf("GetActorAccess: %v", err)
	}
	if resp.Relationship != "grantee" || !resp.Authorized || resp.ActedBy != "alice" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGetActorAccessReturnsAPIErrorStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"denied"}`))
			}))
			defer server.Close()

			_, err := newActorAccessTestClient(t, server).GetActorAccess(context.Background(), "arch", "caller-token")
			if err == nil {
				t.Fatalf("expected error for status %d", status)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError, got %T: %v", err, err)
			}
			if apiErr.Status != status {
				t.Fatalf("status = %d, want %d", apiErr.Status, status)
			}
		})
	}
}

func TestGetActorAccessFailsOnMalformedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"relationship":`))
	}))
	defer server.Close()

	_, err := newActorAccessTestClient(t, server).GetActorAccess(context.Background(), "arch", "caller-token")
	if err == nil || !strings.Contains(err.Error(), "decode actor access response") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestGetActorAccessRequiresActorAndBearer(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newActorAccessTestClient(t, server)
	cases := []struct {
		name  string
		actor string
		token string
		want  string
	}{
		{name: "empty actor", actor: " ", token: "caller-token", want: "actor username is required"},
		{name: "empty bearer", actor: "arch", token: " ", want: "caller bearer token is required"},
	}
	for _, tc := range cases {
		_, err := client.GetActorAccess(context.Background(), tc.actor, tc.token)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s error = %v, want %q", tc.name, err, tc.want)
		}
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func newActorAccessTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	base, err := parseBaseURL(server.URL)
	if err != nil {
		t.Fatalf("parseBaseURL: %v", err)
	}
	return &Client{baseURL: base, http: server.Client()}
}
