package mcpapp

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

func TestWithErrorBoundary_LogsErrorWithoutSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := WithErrorBoundary(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		return nil, errors.New("dynamodb read soul binding: timeout")
	}, logger)

	ctx := &apptheory.Context{
		RequestID: "req-123",
		Request: apptheory.Request{
			Path:    "/mcp/della-marlowe",
			Headers: map[string][]string{"authorization": {"Bearer super-secret-token"}},
		},
		Params: map[string]string{"actor": "della-marlowe"},
	}
	auth.WithPrincipal(ctx, &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "della-marlowe",
		Claims:   &auth.Claims{DelegatedBy: "aron"},
	})

	resp, err := handler(ctx)
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if err == nil || err.Error() != "dynamodb read soul binding: timeout" {
		t.Fatalf("expected the wrapped error to propagate unchanged, got %v", err)
	}

	out := buf.String()
	for _, want := range []string{"dynamodb read soul binding: timeout", "req-123", "della-marlowe", "oauth_token", "aron", "error"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected boundary log to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "super-secret-token") || strings.Contains(out, "Bearer") || strings.Contains(out, "authorization") {
		t.Fatalf("boundary log leaked bearer material:\n%s", out)
	}
}

func TestWithErrorBoundary_RecoversPanicAndReturnsInternalError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := WithErrorBoundary(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		panic("secret panic value")
	}, logger)

	ctx := &apptheory.Context{
		RequestID: "req-panic",
		Request:   apptheory.Request{Path: "/mcp/arch"},
		Params:    map[string]string{"actor": "arch"},
	}

	resp, err := handler(ctx)
	if resp != nil {
		t.Fatalf("expected nil response after recovered panic, got %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != "app.internal" || appErr.Message != "internal error" {
		t.Fatalf("expected recovered panic to become app.internal, got %T %+v", err, err)
	}

	out := buf.String()
	if !strings.Contains(out, "panic") || !strings.Contains(out, "req-panic") {
		t.Fatalf("expected panic boundary log, got:\n%s", out)
	}
	if strings.Contains(out, "secret panic value") {
		t.Fatalf("boundary log leaked the panic value:\n%s", out)
	}
}
