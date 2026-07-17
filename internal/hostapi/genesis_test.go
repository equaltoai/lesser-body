package hostapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/soulapi"
)

type fakeJSONDoer struct {
	calls []jsonDoerCall
	err   error
}

type jsonDoerCall struct {
	method string
	path   string
	bearer string
	body   any
}

func (f *fakeJSONDoer) DoJSON(_ context.Context, method string, path string, _ url.Values, bearerToken string, body any) (any, error) {
	f.calls = append(f.calls, jsonDoerCall{method: method, path: path, bearer: bearerToken, body: body})
	if f.err != nil {
		return nil, f.err
	}
	return map[string]any{"conversation": map[string]any{"status": "in_progress"}}, nil
}

func TestGenesisClientUsesInstanceTrustHostRoutesAndKey(t *testing.T) {
	doer := &fakeJSONDoer{}
	client := New(doer)
	ctx := context.Background()
	const hostKey = "host-instance-key-test-only"

	if _, err := client.BeginRegistration(ctx, hostKey, RegistrationBeginRequest{
		Domain:       "example.com",
		LocalID:      "ptah-canary",
		Capabilities: []string{"post", " memory "},
	}); err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if _, err := client.AdvanceConversation(ctx, hostKey, "reg-123", MintConversationRequest{
		Model:          "model:test",
		Message:        "begin genesis",
		IdempotencyKey: "idem-1",
	}); err != nil {
		t.Fatalf("AdvanceConversation: %v", err)
	}
	if _, err := client.ReadConversation(ctx, hostKey, "reg-123", "conv-456"); err != nil {
		t.Fatalf("ReadConversation: %v", err)
	}
	if _, err := client.RecoverConversation(ctx, hostKey, "reg-123", "conv-456"); err != nil {
		t.Fatalf("RecoverConversation: %v", err)
	}
	if _, err := client.CompleteConversation(ctx, hostKey, "reg-123", "conv-456"); err != nil {
		t.Fatalf("CompleteConversation: %v", err)
	}
	if _, err := client.FinalizePreflight(ctx, hostKey, "reg-123", "conv-456"); err != nil {
		t.Fatalf("FinalizePreflight: %v", err)
	}
	if _, err := client.FinalizeConversation(ctx, hostKey, "reg-123", "conv-456"); err != nil {
		t.Fatalf("FinalizeConversation: %v", err)
	}

	wantPaths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/soul/instance/agents/register/begin"},
		{http.MethodPost, "/api/v1/soul/instance/agents/register/reg-123/mint-conversation"},
		{http.MethodGet, "/api/v1/soul/instance/agents/register/reg-123/mint-conversation/conv-456"},
		{http.MethodPost, "/api/v1/soul/instance/agents/register/reg-123/mint-conversation/conv-456/recover"},
		{http.MethodPost, "/api/v1/soul/instance/agents/register/reg-123/mint-conversation/conv-456/complete"},
		{http.MethodPost, "/api/v1/soul/instance/agents/register/reg-123/mint-conversation/conv-456/finalize/preflight"},
		{http.MethodPost, "/api/v1/soul/instance/agents/register/reg-123/mint-conversation/conv-456/finalize"},
	}
	if len(doer.calls) != len(wantPaths) {
		t.Fatalf("calls = %d, want %d", len(doer.calls), len(wantPaths))
	}
	for i, want := range wantPaths {
		got := doer.calls[i]
		if got.method != want.method || got.path != want.path {
			t.Errorf("call %d = %s %s, want %s %s", i, got.method, got.path, want.method, want.path)
		}
		if got.bearer != hostKey {
			t.Errorf("call %d bearer = %q, want Host instance key", i, got.bearer)
		}
	}

	beginBody, ok := doer.calls[0].body.(map[string]any)
	if !ok {
		t.Fatalf("begin body type = %T", doer.calls[0].body)
	}
	if beginBody["authority_model"] != AuthorityModelInstanceTrust || beginBody["wallet_address"] != nil {
		t.Fatalf("begin body authority/wallet = %+v", beginBody)
	}
	if got := beginBody["capabilities"]; got == nil {
		t.Fatalf("begin body omitted capabilities: %+v", beginBody)
	}

	advanceBody, ok := doer.calls[1].body.(MintConversationRequest)
	if !ok || advanceBody.Message != "begin genesis" || advanceBody.ConversationID != "" {
		t.Fatalf("advance body = %#v", doer.calls[1].body)
	}
	for _, call := range doer.calls[3:] {
		if body, ok := call.body.(map[string]any); !ok || len(body) != 0 {
			t.Errorf("state transition body = %#v, want empty instance-trust body", call.body)
		}
	}
}

func TestGenesisClientSanitizesHostErrorBody(t *testing.T) {
	const secret = "private-host-error-transcript"
	doer := &fakeJSONDoer{err: &soulapi.APIError{
		Status: http.StatusConflict,
		Body:   []byte(`{"error":{"code":"soul_instance.conflict","message":"` + secret + `"}}`),
	}}
	_, err := New(doer).ReadConversation(context.Background(), "host-instance-key", "reg-123", "conv-456")
	if err == nil {
		t.Fatal("expected Host error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Host error leaked response body: %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		t.Fatalf("error = %T %v, want hostapi.APIError", err, err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != "soul_instance.conflict" {
		t.Fatalf("sanitized Host error = %+v", apiErr)
	}
}

func TestGenesisClientRejectsMissingIdentifiersAndBearer(t *testing.T) {
	client := New(&fakeJSONDoer{})
	if _, err := client.BeginRegistration(context.Background(), "", RegistrationBeginRequest{Domain: "example.com", LocalID: "agent"}); err == nil {
		t.Fatal("expected missing Host instance key error")
	}
	if _, err := client.ReadConversation(context.Background(), "host-key", "", "conv"); err == nil {
		t.Fatal("expected missing registration id error")
	}
	if _, err := client.AdvanceConversation(context.Background(), "host-key", "reg", MintConversationRequest{}); err == nil {
		t.Fatal("expected missing conversation message error")
	}
}
