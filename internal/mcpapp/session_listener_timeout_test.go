package mcpapp

import (
	"context"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestWithSessionListenerTimeoutBudget_UsesConfiguredTimeoutForInitialGET(t *testing.T) {
	t.Setenv(envMcpSessionListenerTimeout, "75ms")

	deadlineSet := false
	var timeout time.Duration

	handler := WithSessionListenerTimeoutBudget(func(ctx *apptheory.Context) (*apptheory.Response, error) {
		deadline, ok := ctx.Context().Deadline()
		deadlineSet = ok
		if ok {
			timeout = time.Until(deadline)
		}
		return &apptheory.Response{Status: 204}, nil
	})

	reqCtx := &apptheory.Context{
		Request: apptheory.Request{
			Method: "GET",
			Path:   "/mcp/agent1",
		},
	}
	setRequestContext(reqCtx, context.Background())

	if _, err := handler(reqCtx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !deadlineSet {
		t.Fatalf("expected bounded context deadline")
	}
	if timeout < 40*time.Millisecond || timeout > 90*time.Millisecond {
		t.Fatalf("expected timeout near 75ms, got %v", timeout)
	}
}

func TestWithSessionListenerTimeoutBudget_SkipsResumedOrNonGETRequests(t *testing.T) {
	t.Setenv(envMcpSessionListenerTimeout, "75ms")

	cases := []struct {
		name    string
		method  string
		headers map[string][]string
	}{
		{
			name:   "resumed get",
			method: "GET",
			headers: map[string][]string{
				"last-event-id": {"stream-1:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
			},
		},
		{
			name:   "post request",
			method: "POST",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deadlineSet := false
			handler := WithSessionListenerTimeoutBudget(func(ctx *apptheory.Context) (*apptheory.Response, error) {
				_, deadlineSet = ctx.Context().Deadline()
				return &apptheory.Response{Status: 204}, nil
			})

			reqCtx := &apptheory.Context{
				Request: apptheory.Request{
					Method:  tc.method,
					Path:    "/mcp/agent1",
					Headers: tc.headers,
				},
				RemainingMS: 30000,
			}
			setRequestContext(reqCtx, context.Background())

			if _, err := handler(reqCtx); err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if deadlineSet {
				t.Fatalf("expected no timeout deadline for %s", tc.name)
			}
		})
	}
}

func TestSessionListenerTimeout_UsesRemainingLambdaBudgetWhenNotConfigured(t *testing.T) {
	reqCtx := &apptheory.Context{
		Request: apptheory.Request{
			Method: "GET",
			Path:   "/mcp/agent1",
		},
		RemainingMS: 30000,
	}

	timeout, ok := sessionListenerTimeout(reqCtx)
	if !ok {
		t.Fatalf("expected lambda-derived timeout")
	}
	if timeout != 25*time.Second {
		t.Fatalf("expected 25s timeout cap, got %v", timeout)
	}
}
