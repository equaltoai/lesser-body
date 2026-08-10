package mcpserver

import (
	"testing"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
)

func TestPendingMessageRequestForCounterpartMatchesRecipientVisibleIdentity(t *testing.T) {
	requests := []lesserapi.MessageRequestConversation{
		{
			ID: "accepted",
			Accounts: []lesserapi.MessageRequestAccount{{
				ID:       "acct-accepted",
				Username: "accepted-user",
			}},
			ViewerMetadata: lesserapi.MessageRequestViewerMetadata{RequestState: "ACCEPTED"},
		},
		{
			ID: "pending",
			Accounts: []lesserapi.MessageRequestAccount{{
				ID:       "acct-della",
				Username: "Della-Marlowe",
				Domain:   "theory.greater.website",
			}},
			ViewerMetadata: lesserapi.MessageRequestViewerMetadata{RequestState: "pending"},
		},
	}

	for _, selector := range []string{"acct-della", "della-marlowe", "@DELLA-MARLOWE", "della-marlowe@theory.greater.website"} {
		request := pendingMessageRequestForCounterpart(requests, selector)
		if request == nil || request.ID != "pending" {
			t.Fatalf("selector %q matched %+v, want pending request", selector, request)
		}
	}
	if request := pendingMessageRequestForCounterpart(requests, "accepted-user"); request != nil {
		t.Fatalf("accepted conversation must not be reported as pending: %+v", request)
	}
	if request := pendingMessageRequestForCounterpart(requests, "missing"); request != nil {
		t.Fatalf("missing counterpart matched unexpected request: %+v", request)
	}
}
