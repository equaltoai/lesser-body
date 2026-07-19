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
	resp  any
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
	if f.resp != nil {
		return f.resp, nil
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

func TestIdentityClientFetchesPublicSoulAgentIdentityWithoutBearer(t *testing.T) {
	const agentID = "0x69cca6855f8c36f486cb082a3bcf3d4815e14612f3353b7f71de47b2c63a96e3"
	doer := &fakeJSONDoer{resp: map[string]any{
		"version":           "1",
		"published_version": float64(3),
		"agent": map[string]any{
			"agent_id":                 agentID,
			"domain":                   "theory.greater.website",
			"local_id":                 "theo-marsh",
			"authority_model":          "instance_trust",
			"anchor_state":             "hosted_offchain",
			"operational_binding":      "hosted_bound_soul",
			"lifecycle_status":         "active",
			"status":                   "active",
			"self_description_version": float64(2),
		},
	}}

	identity, err := New(doer).GetAgentIdentity(context.Background(), " "+agentID+" ")
	if err != nil {
		t.Fatalf("GetAgentIdentity: %v", err)
	}
	if identity.AgentID != agentID || identity.Domain != "theory.greater.website" || identity.LocalID != "theo-marsh" {
		t.Fatalf("identity core fields = %+v", identity)
	}
	if identity.AuthorityModel != "instance_trust" || identity.AnchorState != "hosted_offchain" || identity.OperationalBinding != "hosted_bound_soul" || identity.LifecycleStatus != "active" || identity.Status != "active" {
		t.Fatalf("identity binding fields = %+v", identity)
	}
	if identity.PublishedVersion != 3 || identity.SelfDescriptionVersion != 2 {
		t.Fatalf("identity publication versions = %+v", identity)
	}
	if len(doer.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(doer.calls))
	}
	call := doer.calls[0]
	if call.method != http.MethodGet || call.path != "/api/v1/soul/agents/"+agentID {
		t.Fatalf("identity call = %s %s", call.method, call.path)
	}
	if call.bearer != "" || call.body != nil {
		t.Fatalf("identity call should not forward bearer/body: %+v", call)
	}
}
