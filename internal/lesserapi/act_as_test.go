package lesserapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestWithActAsRoundTrip(t *testing.T) {
	ctx := WithActAs(context.Background(), "arch")
	if got := ActAsFromContext(ctx); got != "arch" {
		t.Fatalf("ActAsFromContext = %q", got)
	}
	if got := ActAsFromContext(context.Background()); got != "" {
		t.Fatalf("bare context act-as = %q", got)
	}
	if got := ActAsFromContext(WithActAs(context.Background(), "  ")); got != "" {
		t.Fatalf("blank username must not attach, got %q", got)
	}
	if got := ActAsFromContext(nil); got != "" {
		t.Fatalf("nil context act-as = %q", got)
	}
}

func TestDoJSONSendsActAsHeaderOnlyWhenContextCarriesIt(t *testing.T) {
	type recorded struct {
		authorization string
		actAs         string
	}
	var calls []recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recorded{
			authorization: r.Header.Get("Authorization"),
			actAs:         r.Header.Get(ActAsHeader),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test url: %v", err)
	}
	client := &Client{baseURL: base, http: server.Client()}

	if _, err := client.DoJSON(WithActAs(context.Background(), "arch"), "GET", "/api/v1/accounts/verify_credentials", nil, "oauth-token", nil); err != nil {
		t.Fatalf("act-as request: %v", err)
	}
	if _, err := client.DoJSON(context.Background(), "GET", "/api/v1/accounts/verify_credentials", nil, "oauth-token", nil); err != nil {
		t.Fatalf("owner request: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].authorization != "Bearer oauth-token" || calls[0].actAs != "arch" {
		t.Fatalf("act-as call headers = %+v", calls[0])
	}
	if calls[1].authorization != "Bearer oauth-token" || calls[1].actAs != "" {
		t.Fatalf("owner call must never carry the act-as header, headers = %+v", calls[1])
	}
}
