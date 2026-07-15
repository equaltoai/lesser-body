package lesserapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSoulAgentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCreateSoulBindingSendsContractAndDecodesSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/souls/bindings" {
			t.Fatalf("path = %s, want /api/v1/souls/bindings", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Fatalf("query = %q, want empty", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer binding-secret" {
			t.Fatalf("Authorization = %q, want dedicated bearer", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "bind-key-1" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q", got)
		}

		var body SoulBindingRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.ActorUsername != "drone-ada" || body.SoulAgentID != testSoulAgentID {
			t.Fatalf("required body fields = %+v", body)
		}
		if body.BodyActorID != "body://ptah/drone-ada" || body.HostRegistrationID != "hreg_01JZPTHOSTREG" || body.HostConversationID != "hconv_01JZPTHOSTCONV" {
			t.Fatalf("host/body fields = %+v", body)
		}
		if body.AuthorityModel != SoulAuthorityModelInstanceTrust || body.AnchorState != SoulAnchorStateHostedOffchain || body.OperationalBinding != SoulOperationalBindingHostedBound {
			t.Fatalf("binding hints = %+v", body)
		}
		if body.PrincipalAddress != "0x2222222222222222222222222222222222222222" {
			t.Fatalf("principal_address = %q", body.PrincipalAddress)
		}
		if body.Evidence.Source != "ptah" || body.Evidence.HostRequestID != "hreq_01JZPTHOSTREQ" || body.Evidence.DeclarationHash == "" || body.Evidence.IssuedAt != "2026-07-14T16:20:00Z" {
			t.Fatalf("evidence = %+v", body.Evidence)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successSoulBindingResponse(false)))
	}))
	defer server.Close()

	client := newSoulBindingTestClient(t, server)
	resp, err := client.CreateSoulBinding(context.Background(), " binding-secret ", " bind-key-1 ", SoulBindingRequest{
		ActorUsername:      "drone-ada",
		SoulAgentID:        testSoulAgentID,
		BodyActorID:        "body://ptah/drone-ada",
		HostRegistrationID: "hreg_01JZPTHOSTREG",
		HostConversationID: "hconv_01JZPTHOSTCONV",
		AuthorityModel:     SoulAuthorityModelInstanceTrust,
		AnchorState:        SoulAnchorStateHostedOffchain,
		OperationalBinding: SoulOperationalBindingHostedBound,
		PrincipalAddress:   "0x2222222222222222222222222222222222222222",
		Evidence: SoulBindingEvidence{
			Source:          "ptah",
			HostRequestID:   "hreq_01JZPTHOSTREQ",
			DeclarationHash: "sha256:4c5835f5c2c84bcaadc17af3c5a5700fdd7f39fb7f61305b02d1a02a0e6c7c56",
			IssuedAt:        "2026-07-14T16:20:00Z",
		},
	})
	if err != nil {
		t.Fatalf("CreateSoulBinding: %v", err)
	}
	assertSoulBindingSuccess(t, resp)
	if resp.Idempotency == nil || resp.Idempotency.Key != "bind-key-1" || resp.Idempotency.Replayed {
		t.Fatalf("idempotency = %+v", resp.Idempotency)
	}
	if resp.Links == nil || resp.Links.Status != "/api/v1/souls/bindings/"+testSoulAgentID {
		t.Fatalf("links = %+v", resp.Links)
	}
}

func TestCreateSoulBindingDecodesIdempotentReplay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got != "bind-key-1" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successSoulBindingResponse(true)))
	}))
	defer server.Close()

	client := newSoulBindingTestClient(t, server)
	resp, err := client.InitiateSoulBinding(context.Background(), "binding-secret", "bind-key-1", SoulBindingRequest{ActorUsername: "drone-ada", SoulAgentID: testSoulAgentID})
	if err != nil {
		t.Fatalf("InitiateSoulBinding: %v", err)
	}
	if resp.Idempotency == nil || !resp.Idempotency.Replayed || resp.Idempotency.PayloadHash != "sha256:handler-payload" {
		t.Fatalf("idempotency replay = %+v", resp.Idempotency)
	}
}

func TestGetSoulBindingStatusSendsContractAndDecodesResponse(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/souls/bindings/"+testSoulAgentID {
			t.Fatalf("path = %s", r.URL.Path)
		}
		switch requests {
		case 1:
			if got := r.URL.Query().Get("actor_username"); got != "drone-ada" {
				t.Fatalf("actor_username query = %q", got)
			}
		case 2:
			if got := r.URL.RawQuery; got != "" {
				t.Fatalf("blank actor_username should omit query, got %q", got)
			}
		default:
			t.Fatalf("unexpected request %d", requests)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer binding-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Fatalf("GET must not send Idempotency-Key, got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statusSoulBindingResponse()))
	}))
	defer server.Close()

	client := newSoulBindingTestClient(t, server)
	resp, err := client.GetSoulBindingStatus(context.Background(), " binding-secret ", " "+testSoulAgentID+" ", " drone-ada ")
	if err != nil {
		t.Fatalf("GetSoulBindingStatus: %v", err)
	}
	assertSoulBindingSuccess(t, resp)
	if resp.Idempotency != nil {
		t.Fatalf("GET response idempotency = %+v, want nil", resp.Idempotency)
	}
	if resp.Links != nil {
		t.Fatalf("GET response links = %+v, want nil", resp.Links)
	}

	if _, err := client.GetSoulBinding(context.Background(), "binding-secret", testSoulAgentID, " "); err != nil {
		t.Fatalf("GetSoulBinding without actor_username: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestSoulBindingClientRequiresDedicatedBearerAndIdempotencyBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newSoulBindingTestClient(t, server)
	validReq := SoulBindingRequest{ActorUsername: "drone-ada", SoulAgentID: testSoulAgentID}

	cases := []struct {
		name string
		call func() error
		want string
	}{
		{
			name: "create rejects empty bearer",
			call: func() error {
				_, err := client.CreateSoulBinding(context.Background(), " ", "bind-key-1", validReq)
				return err
			},
			want: "integration bearer is required",
		},
		{
			name: "create rejects empty idempotency key",
			call: func() error {
				_, err := client.CreateSoulBinding(context.Background(), "binding-secret", " ", validReq)
				return err
			},
			want: "idempotency key is required",
		},
		{
			name: "get rejects empty bearer",
			call: func() error {
				_, err := client.GetSoulBinding(context.Background(), " ", testSoulAgentID, "drone-ada")
				return err
			},
			want: "integration bearer is required",
		},
	}

	for _, tc := range cases {
		err := tc.call()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s error = %v, want %q", tc.name, err, tc.want)
		}
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestSoulBindingClientReturnsAPIErrorStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable} {
		status := status
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"error":"soul binding failed","error_description":"status %d","code":"SOUL_BINDING_TEST"}`, status)
			}))
			defer server.Close()

			client := newSoulBindingTestClient(t, server)
			_, err := client.CreateSoulBinding(context.Background(), "binding-secret", "bind-key-1", SoulBindingRequest{ActorUsername: "drone-ada", SoulAgentID: testSoulAgentID})
			if err == nil {
				t.Fatalf("expected API error")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError, got %T: %v", err, err)
			}
			if apiErr.Status != status {
				t.Fatalf("status = %d, want %d", apiErr.Status, status)
			}
			if !strings.Contains(string(apiErr.Body), `"code":"SOUL_BINDING_TEST"`) {
				t.Fatalf("error body = %s", string(apiErr.Body))
			}
		})
	}
}

func newSoulBindingTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	base, err := parseBaseURL(server.URL)
	if err != nil {
		t.Fatalf("parseBaseURL: %v", err)
	}
	return &Client{baseURL: base, http: server.Client()}
}

func successSoulBindingResponse(replayed bool) string {
	return fmt.Sprintf(`{
		"version":"1",
		"status":"bound",
		"binding_state":"bound",
		"agent":{
			"agent_id":"%s",
			"domain":"example.com",
			"local_id":"drone-ada",
			"authority_model":"%s",
			"anchor_state":"%s",
			"operational_binding":"%s",
			"lifecycle_status":"active",
			"published_version":3
		},
		"binding":{
			"agent_username":"drone-ada",
			"principal_address":"0x1111111111111111111111111111111111111111",
			"bound_at":"2026-07-14T16:20:02Z",
			"updated_at":"2026-07-14T16:20:02Z"
		},
		"idempotency":{
			"key":"bind-key-1",
			"replayed":%t,
			"payload_hash":"sha256:handler-payload"
		},
		"links":{"status":"/api/v1/souls/bindings/%s"}
		}`, testSoulAgentID, SoulAuthorityModelInstanceTrust, SoulAnchorStateHostedOffchain, SoulOperationalBindingHostedBound, replayed, testSoulAgentID)
}

func statusSoulBindingResponse() string {
	return fmt.Sprintf(`{
		"version":"1",
		"status":"bound",
		"binding_state":"bound",
		"agent":{
			"agent_id":"%s",
			"domain":"example.com",
			"local_id":"drone-ada",
			"authority_model":"%s",
			"anchor_state":"%s",
			"operational_binding":"%s",
			"lifecycle_status":"active",
			"published_version":3
		},
		"binding":{
			"agent_username":"drone-ada",
			"principal_address":"0x1111111111111111111111111111111111111111",
			"bound_at":"2026-07-14T16:20:02Z",
			"updated_at":"2026-07-14T16:20:02Z"
		}
		}`, testSoulAgentID, SoulAuthorityModelInstanceTrust, SoulAnchorStateHostedOffchain, SoulOperationalBindingHostedBound)
}

func assertSoulBindingSuccess(t *testing.T, resp *SoulBindingResponse) {
	t.Helper()
	if resp == nil {
		t.Fatalf("response is nil")
	}
	if resp.Version != "1" || resp.Status != "bound" || resp.BindingState != "bound" {
		t.Fatalf("response status fields = %+v", resp)
	}
	if resp.Agent.AgentID != testSoulAgentID || resp.Agent.Domain != "example.com" || resp.Agent.LocalID != "drone-ada" {
		t.Fatalf("agent = %+v", resp.Agent)
	}
	if resp.Agent.AuthorityModel != SoulAuthorityModelInstanceTrust || resp.Agent.AnchorState != SoulAnchorStateHostedOffchain || resp.Agent.OperationalBinding != SoulOperationalBindingHostedBound || resp.Agent.LifecycleStatus != "active" || resp.Agent.PublishedVersion != 3 {
		t.Fatalf("agent binding metadata = %+v", resp.Agent)
	}
	if resp.Binding.AgentUsername != "drone-ada" || resp.Binding.PrincipalAddress != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("binding = %+v", resp.Binding)
	}
	if resp.Binding.BoundAt.IsZero() || resp.Binding.UpdatedAt.IsZero() {
		t.Fatalf("binding times were not decoded: %+v", resp.Binding)
	}
}
