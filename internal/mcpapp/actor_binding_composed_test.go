package mcpapp_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-body/internal/agentshare"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

// TestComposedActorBindingAdmission exercises the composed production chain —
// WithMCPAuthorization → WithActorBinding → WithToolContext — with a real signed
// OAuth access token, so the admission decision is reached through the same path
// production uses (the earlier actor_binding_test.go defect constructed the
// AppTheory context directly and never traversed OAuth issuance).
func TestComposedActorBindingAdmission(t *testing.T) {
	t.Run("grantee with active grant admitted and act-as marker threaded", func(t *testing.T) {
		state := &composedAdmissionState{owner: "owner", grantExists: true, grantRevoked: false}
		installComposedAgentShareDB(t, state)

		called := false
		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			called = true
			if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); !shared || caller != "alice" {
				t.Fatalf("grantee admission must thread the share caller into the tool context, caller=%q shared=%t", caller, shared)
			}
			return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
		})

		resp, err := handler(composedOAuthContext(composedTestJWT(t, "arch", "@alice")))
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("grantee active admission: response=%+v err=%v", resp, err)
		}
		if !called {
			t.Fatal("grantee with active grant must reach the handler")
		}
		if state.grantReads != 1 || state.ownerReads != 1 {
			t.Fatalf("expected one owner read and one grant read, owner=%d grant=%d", state.ownerReads, state.grantReads)
		}
	})

	t.Run("grantee revoked denied on the very next request", func(t *testing.T) {
		state := &composedAdmissionState{owner: "owner", grantExists: true, grantRevoked: true}
		installComposedAgentShareDB(t, state)

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("revoked grantee must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(composedTestJWT(t, "arch", "@alice")))
		if resp != nil {
			t.Fatalf("revoked grantee must not admit, response=%+v", resp)
		}
		appErr, ok := err.(*apptheory.AppError)
		if !ok || appErr.Code != "app.forbidden" {
			t.Fatalf("revoked grantee must use the existing 403 error, got %T %+v", err, err)
		}
		if state.grantReads != 1 {
			t.Fatalf("revocation must be enforced per request, grant reads=%d", state.grantReads)
		}
	})

	t.Run("owner admitted and grant check never consulted", func(t *testing.T) {
		state := &composedAdmissionState{owner: "owner", grantExists: false}
		installComposedAgentShareDB(t, state)

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); shared || caller != "" {
				t.Fatalf("owner admission must not thread a share caller, caller=%q shared=%t", caller, shared)
			}
			return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
		})

		resp, err := handler(composedOAuthContext(composedTestJWT(t, "arch", "@owner")))
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("owner admission: response=%+v err=%v", resp, err)
		}
		if state.grantReads != 0 {
			t.Fatalf("owner path must never consult the share grant, reads=%d", state.grantReads)
		}
		if state.ownerReads != 1 {
			t.Fatalf("owner path must resolve the owner exactly once, reads=%d", state.ownerReads)
		}
	})

	t.Run("absent DelegatedBy denied without owner or grant reads", func(t *testing.T) {
		state := &composedAdmissionState{owner: "owner", grantExists: true, grantRevoked: false}
		installComposedAgentShareDB(t, state)

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("absent DelegatedBy must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(composedTestJWT(t, "arch", "")))
		if resp != nil {
			t.Fatalf("absent DelegatedBy must not admit, response=%+v", resp)
		}
		appErr, ok := err.(*apptheory.AppError)
		if !ok || appErr.Code != "app.forbidden" {
			t.Fatalf("absent DelegatedBy must fail closed with 403, got %T %+v", err, err)
		}
		if state.ownerReads != 0 || state.grantReads != 0 {
			t.Fatalf("absent DelegatedBy must fail closed before owner/grant reads, owner=%d grant=%d", state.ownerReads, state.grantReads)
		}
	})
}

func composedHandler(t *testing.T, next apptheory.Handler) apptheory.Handler {
	t.Helper()
	return mcpapp.WithMCPAuthorization(mcpapp.WithActorBinding(mcpapp.WithToolContext(next)))
}

func composedOAuthContext(token string) *apptheory.Context {
	return &apptheory.Context{
		Params: map[string]string{"actor": "arch"},
		Request: apptheory.Request{
			Path:    "/mcp/arch",
			Headers: map[string][]string{"authorization": {"Bearer " + token}},
		},
	}
}

func composedTestJWT(t *testing.T, username, delegatedBy string) string {
	t.Helper()
	now := time.Now().UTC()
	claims := &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "jti-composed",
		},
		Username:    username,
		Scopes:      []string{"read", "write"},
		ClientID:    "test-client",
		IsAgent:     true,
		DelegatedBy: delegatedBy,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

type composedAdmissionState struct {
	owner        string
	grantExists  bool
	grantRevoked bool
	ownerReads   int
	grantReads   int
}

func installComposedAgentShareDB(t *testing.T, state *composedAdmissionState) {
	t.Helper()
	t.Setenv("MCP_ENDPOINT", "")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)

	agentshare.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				pk, _ := where["PK"].(string)
				sk, _ := where["SK"].(string)
				agent := strings.TrimPrefix(pk, "USER#")
				switch {
				case sk == "METADATA":
					state.ownerReads++
					return setAdmissionFields(dest, map[string]any{
						"PK":         pk,
						"SK":         sk,
						"Username":   agent,
						"IsAgent":    true,
						"AgentOwner": state.owner,
					})
				case strings.HasPrefix(sk, "AGENT_SHARE#GRANTEE#"):
					state.grantReads++
					grantee := strings.TrimPrefix(sk, "AGENT_SHARE#GRANTEE#")
					if !state.grantExists {
						return tableerrors.ErrItemNotFound
					}
					fields := map[string]any{
						"PK":              pk,
						"SK":              sk,
						"AgentUsername":   agent,
						"GranteeUsername": grantee,
						"GrantedBy":       state.owner,
						"GrantedAt":       time.Now().UTC(),
					}
					if state.grantRevoked {
						fields["RevokedAt"] = time.Now().UTC()
					}
					return setAdmissionFields(dest, fields)
				default:
					return tableerrors.ErrItemNotFound
				}
			},
		}, nil
	})
	t.Cleanup(setDefaultAgentShareOwner)
}

func setAdmissionFields(dest any, values map[string]any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return nil
	}
	for key, value := range values {
		field := elem.FieldByName(key)
		if !field.IsValid() || !field.CanSet() {
			continue
		}
		switch typed := value.(type) {
		case string:
			if field.Kind() == reflect.String {
				field.SetString(typed)
			}
		case bool:
			if field.Kind() == reflect.Bool {
				field.SetBool(typed)
			}
		case time.Time:
			switch field.Type() {
			case reflect.TypeOf(time.Time{}):
				field.Set(reflect.ValueOf(typed))
			case reflect.TypeOf((*time.Time)(nil)):
				value := typed
				field.Set(reflect.ValueOf(&value))
			}
		}
	}
	return nil
}
