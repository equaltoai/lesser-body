package mcpapp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/mcpapp"
	"github.com/equaltoai/lesser-body/internal/mcpserver"
)

// TestComposedActorBindingAdmission drives the composed production chain —
// WithMCPAuthorization → WithActorBinding → WithToolContext — with a real signed
// OAuth access token and a stubbed Lesser actor-admission endpoint. This is the
// same path production uses; the earlier actor_binding_test.go defect
// constructed the AppTheory context directly and never traversed OAuth
// issuance, which is how a dead direct-read path passed its tests for months.
func TestComposedActorBindingAdmission(t *testing.T) {
	t.Run("owner admitted and no share marker", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@owner")
		var gotPath, gotAuth string
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			writeActorAccessJSON(w, "owner", "owner")
		})

		called := false
		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			called = true
			if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); shared || caller != "" {
				t.Fatalf("owner admission must not thread a share caller, caller=%q shared=%t", caller, shared)
			}
			return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
		})

		resp, err := handler(composedOAuthContext(token))
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("owner admission: response=%+v err=%v", resp, err)
		}
		if !called {
			t.Fatal("owner must reach the handler")
		}
		if gotPath != "/api/v1/agents/arch/access" {
			t.Fatalf("admission path = %q", gotPath)
		}
		if gotAuth != "Bearer "+token {
			t.Fatalf("admission must forward the caller bearer, got %q", gotAuth)
		}
	})

	t.Run("grantee admitted with share marker and act-as threaded", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@alice")
		var gotPath, gotAuth string
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			writeActorAccessJSON(w, "grantee", "alice")
		})

		called := false
		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			called = true
			if caller, shared := mcpserver.ShareCallerFromContext(ctx.Context()); !shared || caller != "alice" {
				t.Fatalf("grantee admission must thread the share caller into the tool context, caller=%q shared=%t", caller, shared)
			}
			return apptheory.MustJSON(200, map[string]bool{"ok": true}), nil
		})

		resp, err := handler(composedOAuthContext(token))
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("grantee admission: response=%+v err=%v", resp, err)
		}
		if !called {
			t.Fatal("grantee must reach the handler")
		}
		if gotPath != "/api/v1/agents/arch/access" {
			t.Fatalf("admission path = %q", gotPath)
		}
		if gotAuth != "Bearer "+token {
			t.Fatalf("admission must forward the caller bearer, got %q", gotAuth)
		}
	})

	t.Run("lesser 403 denied", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@alice")
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("403 must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(token))
		assertComposedForbidden(t, resp, err)
	})

	t.Run("lesser 401 denied", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@alice")
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("401 must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(token))
		assertComposedForbidden(t, resp, err)
	})

	t.Run("lesser unreachable denied", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@alice")
		installActorAccessUnreachable(t)

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("unreachable lesser must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(token))
		assertComposedForbidden(t, resp, err)
	})

	t.Run("lesser times out denied", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@alice")
		t.Setenv("LESSER_API_TIMEOUT_SECONDS", "1")
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
		})

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("timed out lesser must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(token))
		assertComposedForbidden(t, resp, err)
	})

	t.Run("lesser malformed JSON denied", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@alice")
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"relationship":`)
		})

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("malformed JSON must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(token))
		assertComposedForbidden(t, resp, err)
	})

	t.Run("lesser 200 authorized=false denied", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "@alice")
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"actor":"arch","relationship":"owner","authorized":false,"acted_by":"owner"}`)
		})

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("authorized=false must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(token))
		assertComposedForbidden(t, resp, err)
	})

	t.Run("absent DelegatedBy denied without a lesser call", func(t *testing.T) {
		installComposedBaseEnv(t)
		token := composedTestJWT(t, "arch", "")
		calls := 0
		installActorAccessHTTPStub(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusForbidden)
		})

		handler := composedHandler(t, func(ctx *apptheory.Context) (*apptheory.Response, error) {
			t.Fatal("absent DelegatedBy must not reach the handler")
			return nil, nil
		})

		resp, err := handler(composedOAuthContext(token))
		assertComposedForbidden(t, resp, err)
		if calls != 0 {
			t.Fatalf("absent DelegatedBy must fail closed before the lesser call, calls=%d", calls)
		}
	})
}

func assertComposedForbidden(t *testing.T, resp *apptheory.Response, err error) {
	t.Helper()
	if resp != nil {
		t.Fatalf("denial must not return a success response: %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("denial must use the existing 403 error, got %T %+v", err, err)
	}
}

func writeActorAccessJSON(w http.ResponseWriter, relationship, actedBy string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"actor":"arch","relationship":"`+relationship+`","authorized":true,"acted_by":"`+actedBy+`"}`)
}

func installComposedBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MCP_ENDPOINT", "")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_SECRET_ARN", "")
	auth.ResetForTests()
	t.Cleanup(auth.ResetForTests)
}

func installActorAccessHTTPStub(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	restoreAdmission := mcpapp.SetActorAdmissionForTests(nil)
	t.Cleanup(restoreAdmission)

	server := httptest.NewServer(handler)
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
}

func installActorAccessUnreachable(t *testing.T) {
	t.Helper()
	restoreAdmission := mcpapp.SetActorAdmissionForTests(nil)
	t.Cleanup(restoreAdmission)

	t.Setenv("LESSER_API_BASE_URL", "http://127.0.0.1:1")
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
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
