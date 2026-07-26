package lesserapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageRequestClientUsesRecipientBearerAndGraphQLContract(t *testing.T) {
	operations := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Fatalf("request = %s %s, want POST /api/graphql", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer recipient-token" {
			t.Fatalf("Authorization = %q", got)
		}
		var op messageRequestGraphQLOperation
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		operations = append(operations, op.OperationName)
		w.Header().Set("Content-Type", "application/json")
		switch op.OperationName {
		case "BodyMessageRequests":
			if !strings.Contains(op.Query, "conversations(folder: REQUESTS") {
				t.Fatalf("list query does not pin REQUESTS folder: %s", op.Query)
			}
			if got := op.Variables["first"]; got != float64(7) {
				t.Fatalf("first = %#v, want 7", got)
			}
			if got := op.Variables["after"]; got != "cursor-1" {
				t.Fatalf("after = %#v", got)
			}
			_, _ = w.Write([]byte(`{"data":{"conversations":[{"id":"conv-1","unread":true,"accounts":[{"id":"acct-sender","username":"sender","domain":"example.test","displayName":"Sender"}],"viewerMetadata":{"requestState":"PENDING","requestedAt":"2026-07-26T12:00:00Z"},"lastStatus":{"id":"status-1","content":"hello","createdAt":"2026-07-26T12:00:00Z"},"createdAt":"2026-07-26T12:00:00Z","updatedAt":"2026-07-26T12:00:00Z"}]}}`))
		case "BodyAcceptMessageRequest":
			if got := op.Variables["conversationId"]; got != "conv-1" {
				t.Fatalf("accept conversationId = %#v", got)
			}
			_, _ = w.Write([]byte(`{"data":{"acceptMessageRequest":{"id":"conv-1","unread":true,"accounts":[],"viewerMetadata":{"requestState":"ACCEPTED","acceptedAt":"2026-07-26T12:01:00Z"},"createdAt":"2026-07-26T12:00:00Z","updatedAt":"2026-07-26T12:01:00Z"}}}`))
		case "BodyDeclineMessageRequest":
			if got := op.Variables["conversationId"]; got != "conv-2" {
				t.Fatalf("decline conversationId = %#v", got)
			}
			_, _ = w.Write([]byte(`{"data":{"declineMessageRequest":true}}`))
		default:
			t.Fatalf("unexpected operation %q", op.OperationName)
		}
	}))
	defer server.Close()

	client := newAgentListTestClient(t, server)
	requests, err := client.ListMessageRequests(context.Background(), "recipient-token", 7, " cursor-1 ")
	if err != nil {
		t.Fatalf("ListMessageRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].ID != "conv-1" || requests[0].ViewerMetadata.RequestState != "PENDING" {
		t.Fatalf("requests = %+v", requests)
	}
	if requests[0].LastStatus == nil || requests[0].LastStatus.Content != "hello" {
		t.Fatalf("last status = %+v", requests[0].LastStatus)
	}

	accepted, err := client.AcceptMessageRequest(context.Background(), "recipient-token", " conv-1 ")
	if err != nil {
		t.Fatalf("AcceptMessageRequest: %v", err)
	}
	if accepted.ID != "conv-1" || accepted.ViewerMetadata.RequestState != "ACCEPTED" {
		t.Fatalf("accepted = %+v", accepted)
	}

	declined, err := client.DeclineMessageRequest(context.Background(), "recipient-token", " conv-2 ")
	if err != nil {
		t.Fatalf("DeclineMessageRequest: %v", err)
	}
	if !declined {
		t.Fatal("decline returned false")
	}
	if got, want := strings.Join(operations, ","), "BodyMessageRequests,BodyAcceptMessageRequest,BodyDeclineMessageRequest"; got != want {
		t.Fatalf("operations = %q, want %q", got, want)
	}
}

func TestMessageRequestClientReturnsSanitizedGraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"secret recipient detail","extensions":{"code":"FORBIDDEN"}}]}`))
	}))
	defer server.Close()

	client := newAgentListTestClient(t, server)
	_, err := client.ListMessageRequests(context.Background(), "recipient-token", 20, "")
	if err == nil {
		t.Fatal("ListMessageRequests returned nil error")
	}
	var gqlErr *MessageRequestGraphQLErrors
	if !errors.As(err, &gqlErr) {
		t.Fatalf("error = %T %v, want MessageRequestGraphQLErrors", err, err)
	}
	if gqlErr.Count != 1 || len(gqlErr.Codes) != 1 || gqlErr.Codes[0] != "FORBIDDEN" {
		t.Fatalf("graphql error = %+v", gqlErr)
	}
	if strings.Contains(err.Error(), "secret recipient detail") {
		t.Fatalf("graphql error leaked upstream message: %v", err)
	}
}
