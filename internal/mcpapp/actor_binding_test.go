package mcpapp

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

func TestWithActorBinding_AllowsMatchingActor(t *testing.T) {
	called := false
	handler := WithActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		called = true
		return apptheory.MustJSON(200, map[string]string{"ok": "true"}), nil
	})

	resp, err := handler(&apptheory.Context{
		AuthIdentity: "Arch",
		Params:       map[string]string{"actor": "arch"},
		Request:      apptheory.Request{Path: "/mcp/arch"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !called {
		t.Fatalf("expected next handler to be called")
	}
}

func TestWithActorBinding_RejectsMismatch(t *testing.T) {
	handler := WithActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		t.Fatal("next handler should not be called")
		return nil, nil
	})

	resp, err := handler(&apptheory.Context{
		AuthIdentity: "Medic",
		Params:       map[string]string{"actor": "arch"},
		Request:      apptheory.Request{Path: "/mcp/arch"},
	})
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "app.forbidden" {
		t.Fatalf("unexpected error code: %q", appErr.Code)
	}
}

func TestWithActorBinding_RejectsMissingActor(t *testing.T) {
	handler := WithActorBinding(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		t.Fatal("next handler should not be called")
		return nil, nil
	})

	resp, err := handler(&apptheory.Context{
		AuthIdentity: "Arch",
		Request:      apptheory.Request{Path: "/mcp"},
	})
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "app.forbidden" {
		t.Fatalf("unexpected error code: %q", appErr.Code)
	}
}
