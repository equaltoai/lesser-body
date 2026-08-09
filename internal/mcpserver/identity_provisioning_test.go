package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

func TestAgentChannelsPayloadDistinguishesAbsentFromEmptyProvisioning(t *testing.T) {
	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name             string
		registration     string
		wantChannelState string
		wantPrefsState   string
	}{
		{name: "absent", registration: `{}`, wantChannelState: "absent", wantPrefsState: "absent"},
		{name: "healthy empty", registration: `{"channels":{},"contactPreferences":{}}`, wantChannelState: "empty", wantPrefsState: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/registration") {
					_, _ = w.Write([]byte(tc.registration))
					return
				}
				_, _ = w.Write([]byte(`{"agent":{"domain":"example.com","local_id":"alice","status":"active"}}`))
			}))
			defer server.Close()
			t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
			soulapi.ResetForTests()
			client, err := soulapi.Default()
			if err != nil {
				t.Fatalf("soulapi.Default: %v", err)
			}
			payload, _, err := agentChannelsPayloadWithRegistration(context.Background(), client, agentID)
			if err != nil {
				t.Fatalf("agentChannelsPayloadWithRegistration: %v", err)
			}
			provisioning, _ := payload["provisioning"].(map[string]any)
			channels, _ := provisioning["channels"].(map[string]any)
			preferences, _ := provisioning["contactPreferences"].(map[string]any)
			if channels["state"] != tc.wantChannelState || preferences["state"] != tc.wantPrefsState {
				t.Fatalf("provisioning = %+v", provisioning)
			}
			if provisioning["communications"] != "unprovisioned" {
				t.Fatalf("communications = %v", provisioning["communications"])
			}
		})
	}
}

func TestWithAgentContactabilityChannelsFiltersAndMergesSelfProjection(t *testing.T) {
	payload := map[string]any{
		"channels": map[string]any{
			"ens":   map[string]any{"name": "della.eth"},
			"email": map[string]any{"legacy": "preserved"},
		},
		"provisioning": map[string]any{
			"channels":           objectProvisioningMetadata(nil, false),
			"contactPreferences": objectProvisioningMetadata(nil, false),
			"communications":     "unprovisioned",
		},
	}
	contactability := map[string]any{
		"instanceSlug": "theory",
		"policy":       map[string]any{"private": true},
		"mailbox":      map[string]any{"contentAllowed": true},
		"channels": []any{
			map[string]any{
				"channelType":    "email",
				"address":        "della-marlowe.theory@lessersoul.ai",
				"provider":       "migadu",
				"capabilities":   []any{"receive", "send"},
				"protocols":      []any{"smtp", "imap"},
				"verified":       true,
				"status":         "active",
				"receiveAllowed": true,
				"sendAllowed":    true,
				"secret":         "must-not-project",
			},
			map[string]any{
				"channelType": "phone",
				"number":      "+15555550100",
				"verified":    false,
				"status":      "active",
			},
			map[string]any{
				"channelType": "push",
				"address":     "device-token",
				"verified":    true,
				"status":      "active",
			},
		},
	}

	got := withAgentContactabilityChannels(payload, contactability)
	channels, _ := got["channels"].(map[string]any)
	if _, ok := channels["ens"]; !ok {
		t.Fatalf("existing ENS channel was not preserved: %+v", channels)
	}
	email, _ := channels["email"].(map[string]any)
	if email["address"] != "della-marlowe.theory@lessersoul.ai" || email["legacy"] != "preserved" {
		t.Fatalf("filtered Host email was not merged: %+v", email)
	}
	if _, ok := email["secret"]; ok {
		t.Fatalf("non-contract Host field was projected: %+v", email)
	}
	if _, ok := channels["phone"]; ok {
		t.Fatalf("unverified Host phone was projected: %+v", channels)
	}
	if _, ok := channels["push"]; ok {
		t.Fatalf("unsupported Host channel was projected: %+v", channels)
	}
	if _, ok := got["policy"]; ok {
		t.Fatalf("Host policy was projected: %+v", got)
	}
	if _, ok := got["mailbox"]; ok {
		t.Fatalf("Host mailbox policy was projected: %+v", got)
	}

	provisioning, _ := got["provisioning"].(map[string]any)
	channelProvisioning, _ := provisioning["channels"].(map[string]any)
	if channelProvisioning["state"] != "present" || channelProvisioning["configuredCount"] != 2 || provisioning["communications"] != "configured" {
		t.Fatalf("merged provisioning = %+v", provisioning)
	}
}

func TestWithAgentContactabilityChannelsPreservesAbsentAndEmptyWhenHostHasNoActiveChannels(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{
			name: "absent",
			payload: map[string]any{
				"channels": map[string]any{},
				"provisioning": map[string]any{
					"channels":       objectProvisioningMetadata(nil, false),
					"communications": "unprovisioned",
				},
			},
			want: "absent",
		},
		{
			name: "empty",
			payload: map[string]any{
				"channels": map[string]any{},
				"provisioning": map[string]any{
					"channels":       objectProvisioningMetadata(map[string]any{}, true),
					"communications": "unprovisioned",
				},
			},
			want: "empty",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withAgentContactabilityChannels(tc.payload, map[string]any{
				"channels": []any{map[string]any{
					"channelType": "email",
					"address":     "stale@example.com",
					"verified":    true,
					"status":      "inactive",
				}},
			})
			provisioning, _ := got["provisioning"].(map[string]any)
			channels, _ := provisioning["channels"].(map[string]any)
			if channels["state"] != tc.want || provisioning["communications"] != "unprovisioned" {
				t.Fatalf("provisioning = %+v", provisioning)
			}
		})
	}
}

func TestDescribeInterfaceDeclaresUnprovisionedCommsAndFullPublishGate(t *testing.T) {
	original := describeInterfaceChannelsPayload
	describeInterfaceChannelsPayload = func(context.Context) (map[string]any, error) {
		return map[string]any{"provisioning": map[string]any{"communications": "unprovisioned"}}, nil
	}
	t.Cleanup(func() { describeInterfaceChannelsPayload = original })

	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Identity: "alice"}, "token")
	ctx = runtimepolicy.WithContext(ctx, runtimepolicy.Resolved{Profile: runtimepolicy.ProfileSouled, Determined: true, BoundSoul: true})
	text := renderDescribeInterface(ctx)
	if !strings.Contains(text, "communications: `degraded_unprovisioned`") {
		t.Fatalf("missing dynamic comm degradation: %s", text)
	}
	for _, step := range []string{"article_draft_review_submit", "article_draft_review_read", "article_draft_review_verdict", "principal approval", "publish eligibility", "article_draft_publish"} {
		if !strings.Contains(text, step) {
			t.Fatalf("publication recipe missing %q", step)
		}
	}
}

func TestMemoryAppendSchemaDocumentsULIDTimestampCoupling(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(memoryAppendDef().InputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	description := schema.Properties["event_id"].Description
	for _, want := range []string{"embedded timestamp", "occurred_at", "millisecond"} {
		if !strings.Contains(description, want) {
			t.Fatalf("event_id description %q missing %q", description, want)
		}
	}
}
