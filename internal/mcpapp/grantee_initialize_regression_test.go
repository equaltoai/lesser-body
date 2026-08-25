package mcpapp_test

import (
	"encoding/json"
	"net/http"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
	"github.com/theory-cloud/apptheory/v4/testkit"
	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
)

// TestGranteeInitializeSucceeds drives the full production app through the
// composed chain (WithMCPAuthorization → WithActorBinding → … → initialize)
// with a grantee-shaped principal: token subject is the agent della-marlowe,
// IsAgent=true, DelegatedBy=aron, and Lesser's actor-admission endpoint reports
// relationship "grantee". The grantee must be admitted and initialize must
// return HTTP 200. This locks the grantee handshake path that the live incident
// reported as an app.internal 500.
func TestGranteeInitializeSucceeds(t *testing.T) {
	t.Setenv("MCP_SESSION_TABLE", "")
	t.Setenv("MCP_STREAM_TABLE", "")
	t.Setenv("MCP_TASK_TABLE", "")
	t.Setenv("MCP_ALLOWED_ORIGINS", "")
	t.Setenv("MCP_ENDPOINT", "")
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)
	installTrustConfigIsolation(t)

	installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
		writeActorAccessJSON(w, "grantee", "aron")
	})

	soulbinding.ResetForTests()
	t.Cleanup(soulbinding.ResetForTests)
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				return setStructFields(dest, map[string]string{
					"PK":       "SOUL_BODY_BINDING_USERNAME#della-marlowe",
					"SK":       "SOUL_BODY_BINDING",
					"Username": "della-marlowe",
					"AgentID":  "0x1234567890abcdef",
				})
			},
		}, nil
	})

	app, err := mcpapp.New("test", "dev")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	token := composedTestJWT(t, "della-marlowe", "@aron")

	env := testkit.New()
	resp := invokeJSONAtPath(t, env, app, "/mcp/della-marlowe", map[string][]string{
		"authorization": {"Bearer " + token},
	}, &mcpruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Status != 200 {
		t.Fatalf("expected grantee initialize to return HTTP 200, got %d (%s)", resp.Status, string(resp.Body))
	}
	var rpc mcpruntime.Response
	if err := json.Unmarshal(resp.Body, &rpc); err != nil {
		t.Fatalf("unmarshal initialize response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("expected grantee initialize to have no JSON-RPC error, got %+v", rpc.Error)
	}
}
