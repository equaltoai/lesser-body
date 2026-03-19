package auth

import (
	"bytes"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"log/slog"
)

func TestHook_WarnsWhenManagedInstanceKeyFallbackIsUsed(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "legacy-instance-key")
	t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
	t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "true")
	ResetForTests()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	ctx := &apptheory.Context{
		Request: apptheory.Request{
			Path: "/mcp",
			Headers: map[string][]string{
				"authorization": {"Bearer legacy-instance-key"},
			},
		},
	}

	identity, err := Hook(logger)(ctx)
	if err != nil {
		t.Fatalf("auth hook returned error: %v", err)
	}
	if identity != "instance" {
		t.Fatalf("expected instance identity, got %q", identity)
	}
	principal := PrincipalFromContext(ctx)
	if principal == nil || principal.Type != PrincipalTypeInstanceKey {
		t.Fatalf("expected instance-key principal, got %+v", principal)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "managed-instance-key inbound MCP auth is deprecated") {
		t.Fatalf("expected deprecation warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "docs/oauth-migration.md") {
		t.Fatalf("expected migration doc reference in log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "MCP_ALLOW_LEGACY_INSTANCE_KEY") {
		t.Fatalf("expected compatibility flag reference in log, got %q", logOutput)
	}
}

func TestHook_RejectsManagedInstanceKeyFallbackByDefault(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	t.Setenv("JWT_SECRET_ARN", "")
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "legacy-instance-key")
	t.Setenv("LESSER_HOST_INSTANCE_KEY_ARN", "")
	t.Setenv("MCP_ALLOW_LEGACY_INSTANCE_KEY", "")
	ResetForTests()

	ctx := &apptheory.Context{
		Request: apptheory.Request{
			Path: "/mcp",
			Headers: map[string][]string{
				"authorization": {"Bearer legacy-instance-key"},
			},
		},
	}

	identity, err := Hook(nil)(ctx)
	if err == nil {
		t.Fatalf("expected unauthorized error, got identity %q", identity)
	}
	if identity != "" {
		t.Fatalf("expected empty identity, got %q", identity)
	}
	if principal := PrincipalFromContext(ctx); principal != nil {
		t.Fatalf("expected no principal, got %+v", principal)
	}
}
