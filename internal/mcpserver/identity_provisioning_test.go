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
