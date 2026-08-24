package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

// mediaTestContext returns a tool context carrying an OAuth bearer; tests
// override the LESSER_API_BASE_URL stub.
func mediaTestContext() context.Context {
	return auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "alice",
	}, "test-token")
}

func mediaTestContextWithShareCaller(caller string) context.Context {
	// Share-grant admission: the actor route value names the agent, and the
	// share caller is the grantee human admitted by the actor-binding
	// middleware. The act-as seam forwards the actor route value to Lesser.
	ctx := auth.WithToolActor(mediaTestContext(), "agent1")
	return WithShareCaller(ctx, caller)
}

// mediaGraphQLStub serves /api/graphql against a canned per-operation response.
type mediaGraphQLStub struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	byOp    map[string]string
	ops     []string
	headers []http.Header
}

func newMediaGraphQLStub(t *testing.T, byOp map[string]string) *mediaGraphQLStub {
	t.Helper()
	h := &mediaGraphQLStub{t: t, byOp: byOp}
	h.server = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(func() {
		h.server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", h.server.URL)
	lesserapi.ResetForTests()
	return h
}

func (h *mediaGraphQLStub) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/graphql" {
		http.NotFound(w, r)
		return
	}
	var op cmsapi.Operation
	if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
		h.t.Errorf("decode operation: %v", err)
		http.Error(w, "invalid operation", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.ops = append(h.ops, op.OperationName)
	h.headers = append(h.headers, r.Header.Clone())
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	body, ok := h.byOp[op.OperationName]
	if !ok {
		h.t.Errorf("unexpected operation %q", op.OperationName)
		http.Error(w, "unexpected operation", http.StatusBadRequest)
		return
	}
	_, _ = w.Write([]byte(body))
}

func (h *mediaGraphQLStub) operations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.ops...)
}

func (h *mediaGraphQLStub) lastHeader(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.headers) == 0 {
		return ""
	}
	return h.headers[len(h.headers)-1].Get(name)
}

func mediaGrantJSON(status, presignedURL string) string {
	urlPart := "null"
	if presignedURL != "" {
		urlPart = fmt.Sprintf("%q", presignedURL)
	}
	mediaPart := "null"
	if status == cmsapi.UploadGrantStatusMinted {
		mediaPart = `"media-1"`
	}
	return fmt.Sprintf(`{"id":"grant-1","ownerId":"alice","contentType":"image/png","maxSizeBytes":5242880,"declaredSha256":%q,"status":%q,"presignedUrl":%s,"mediaId":%s,"grantedAt":"2026-08-24T12:00:00Z","expiresAt":"2026-08-24T12:15:00Z","usedAt":null,"failureReason":null}`,
		strings.Repeat("a", 64), status, urlPart, mediaPart)
}

func mediaFixtureUploadGrantJSON(status, presignedURL string) string {
	return mediaGrantJSON(status, presignedURL)
}

func TestMediaToolsRegisterAndClassify(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := registerMediaTools(registry); err != nil {
		t.Fatalf("registerMediaTools: %v", err)
	}

	wantScopes := map[string][]string{
		"upload_grant_mint":   {ScopeWrite},
		"upload_finalize":     {ScopeWrite},
		"media_state":         {ScopeRead},
		"media_read":          {ScopeRead},
		"draft_media_attach":  {ScopeWrite},
		"draft_media_detach":  {ScopeWrite},
		"draft_media_reorder": {ScopeWrite},
	}
	registered := map[string]struct{}{}
	for _, def := range registry.List() {
		registered[def.Name] = struct{}{}
		scopes, ok := wantScopes[def.Name]
		if !ok {
			continue
		}
		delete(wantScopes, def.Name)
		if got := RequiredScopesForTool(def.Name); !sameScopes(got, scopes) {
			t.Errorf("%s scopes = %v, want %v", def.Name, got, scopes)
		}
		if len(def.OutputSchema) == 0 {
			t.Errorf("%s has no output schema", def.Name)
		}
		if def.Annotations == nil {
			t.Errorf("%s has no annotations", def.Name)
		}
	}
	for name := range wantScopes {
		t.Errorf("media tool %s was not registered", name)
	}
	if len(registered) != 7 {
		t.Errorf("expected 7 media tools, got %d", len(registered))
	}
}

func sameScopes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMediaTwoStepUploadContract drives the mint → (PUT out-of-band) → finalize
// ordering and pins the explicit two-step response contract.
func TestMediaTwoStepUploadContract(t *testing.T) {
	stub := newMediaGraphQLStub(t, map[string]string{
		"BodyMintUploadGrant":     `{"data":{"mintUploadGrant":` + mediaFixtureUploadGrantJSON(cmsapi.UploadGrantStatusMinted, "https://presign.example.com/put/media-1.png") + `}}`,
		"BodyFinalizeUploadGrant": `{"data":{"finalizeUploadGrant":{"grant":` + mediaFixtureUploadGrantJSON(cmsapi.UploadGrantStatusUsed, "") + `,"media":{"mediaId":"media-1","contentType":"image/png","size":1024,"contentHash":"sha256:` + strings.Repeat("a", 64) + `","status":"ready","visibility":"internal"}}}}`,
	})

	mint, err := handleUploadGrantMint(mediaTestContext(), json.RawMessage(`{"content_type":"image/png","max_size_bytes":5242880,"sha256":"`+strings.Repeat("a", 64)+`"}`))
	if err != nil || mint == nil || mint.IsError {
		t.Fatalf("mint failed: result=%+v err=%v", mint, err)
	}
	data := structuredData(t, mint)
	if got := data["operation"]; got != "minted" {
		t.Fatalf("mint operation = %v", got)
	}
	grant, _ := data["grant"].(map[string]any)
	if grant == nil || grant["presignedUrl"] == nil || grant["id"] == nil {
		t.Fatalf("mint result missing grant/presignedUrl: %+v", data)
	}
	if data["expiresInSeconds"] == nil {
		t.Fatalf("mint result must surface grant TTL prominently: %+v", data)
	}
	guidance, _ := data["guidance"].(string)
	for _, marker := range []string{"PUT", "out-of-band", "upload_finalize", "one-time", "expired"} {
		if !strings.Contains(strings.ToLower(guidance), strings.ToLower(marker)) {
			t.Fatalf("mint guidance must mention %q, got %q", marker, guidance)
		}
	}

	// The client PUT happens out-of-band between the two calls; finalize then
	// admits the media record.
	final, err := handleUploadFinalize(mediaTestContext(), json.RawMessage(`{"grant_id":"grant-1"}`))
	if err != nil || final == nil || final.IsError {
		t.Fatalf("finalize failed: result=%+v err=%v", final, err)
	}
	finalData := structuredData(t, final)
	if got := finalData["operation"]; got != "finalized" {
		t.Fatalf("finalize operation = %v", got)
	}
	if finalData["media"] == nil {
		t.Fatalf("finalize result missing media: %+v", finalData)
	}

	ops := stub.operations()
	if len(ops) != 2 || ops[0] != "BodyMintUploadGrant" || ops[1] != "BodyFinalizeUploadGrant" {
		t.Fatalf("operations = %v, want mint then finalize", ops)
	}
}

// TestMediaFinalizeErrorLanes pins the classified failure envelope for each
// two-step lane: expired, failed digest, not-minted, and the residual N1
// object-missing class (size-abort / never-PUT) rendered as re-mint guidance.
func TestMediaFinalizeErrorLanes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		gqlResponse string
		wantCode    string
		wantStatus  int
		guidance    []string
		wantMessage string
	}{
		{
			name:        "expired",
			gqlResponse: `{"data":null,"errors":[{"message":"upload grant has expired","path":["finalizeUploadGrant"]}]}`,
			wantCode:    mediaErrorGrantExpired,
			wantStatus:  http.StatusGone,
			guidance:    []string{"fresh", "mint", "15 minutes"},
		},
		{
			name:        "failed_digest",
			gqlResponse: `{"data":null,"errors":[{"message":"uploaded bytes do not match the declared upload grant","path":["finalizeUploadGrant"]}]}`,
			wantCode:    mediaErrorDigestMismatch,
			wantStatus:  http.StatusUnprocessableEntity,
			guidance:    []string{"fresh"},
			wantMessage: "FAILED_DIGEST",
		},
		{
			name:        "not_minted",
			gqlResponse: `{"data":null,"errors":[{"message":"upload grant is not minted","path":["finalizeUploadGrant"]}]}`,
			wantCode:    mediaErrorGrantNotMinted,
			wantStatus:  http.StatusConflict,
			guidance:    []string{"fresh"},
		},
		{
			name:        "object_missing_residual_n1",
			gqlResponse: `{"data":null,"errors":[{"message":"uploaded object not found; PUT the declared bytes before finalizing","path":["finalizeUploadGrant"]}]}`,
			wantCode:    mediaErrorUploadNotFinalized,
			wantStatus:  http.StatusConflict,
			guidance:    []string{"re-mint", "size-abort", "retry finalize once"},
		},
		{
			name:        "not_found_unowned",
			gqlResponse: `{"data":null,"errors":[{"message":"upload grant not found","path":["finalizeUploadGrant"]}]}`,
			wantCode:    mediaErrorNotFound,
			wantStatus:  http.StatusNotFound,
		},
		{
			name:        "size_abort_download",
			gqlResponse: `{"data":null,"errors":[{"message":"uploaded object not found; PUT the declared bytes before finalizing","path":["finalizeUploadGrant"]}]}`,
			wantCode:    mediaErrorUploadNotFinalized,
			wantStatus:  http.StatusConflict,
			guidance:    []string{"size-abort"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newMediaGraphQLStub(t, map[string]string{
				"BodyFinalizeUploadGrant": tc.gqlResponse,
			})
			result, err := handleUploadFinalize(mediaTestContext(), json.RawMessage(`{"grant_id":"grant-1"}`))
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("expected an error result, got %+v", result)
			}
			errorPayload, _ := result.StructuredContent["error"].(map[string]any)
			if errorPayload == nil {
				t.Fatalf("structuredContent.error missing: %#v", result.StructuredContent)
			}
			if got := errorPayload["code"]; got != tc.wantCode {
				t.Fatalf("error code = %v, want %v", got, tc.wantCode)
			}
			if got := normalizeErrorStatus(errorPayload["status"]); got != tc.wantStatus {
				t.Fatalf("error status = %v, want %v", got, tc.wantStatus)
			}
			details, _ := errorPayload["details"].(map[string]any)
			if details == nil {
				t.Fatalf("error details missing")
			}
			if tc.wantMessage != "" {
				message, _ := errorPayload["message"].(string)
				if !strings.Contains(message, tc.wantMessage) {
					t.Fatalf("error message must mention %q, got %q", tc.wantMessage, message)
				}
			}
			guidance, _ := details["guidance"].(string)
			for _, marker := range tc.guidance {
				if !strings.Contains(guidance, marker) {
					t.Fatalf("guidance must mention %q, got %q", marker, guidance)
				}
			}
		})
	}
}

// TestMediaEnvelopeStateMapping pins the state-machine mapping table: every
// lesser grant state and usage/review state maps onto the envelope vocabulary.
func TestMediaEnvelopeStateMapping(t *testing.T) {
	t.Run("grant states", func(t *testing.T) {
		for _, tc := range []struct {
			status string
			want   string
		}{
			{cmsapi.UploadGrantStatusMinted, mediaStateReceived},
			{cmsapi.UploadGrantStatusUsed, mediaStateReadyInternal},
			{cmsapi.UploadGrantStatusFailedDigest, mediaStateRejectedUnsupported},
			{cmsapi.UploadGrantStatusExpired, mediaStateExpired},
			{"UNKNOWN", mediaStateReceived},
		} {
			grant := &cmsapi.UploadGrant{Status: tc.status}
			if got := mediaEnvelopeStateForGrant(grant); got != tc.want {
				t.Errorf("grant %s → %q, want %q", tc.status, got, tc.want)
			}
		}
		if got := mediaEnvelopeStateForGrant(nil); got != mediaStateReceived {
			t.Errorf("nil grant → %q, want received", got)
		}
	})

	t.Run("usage states", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			usage func() *cmsapi.EditorialMediaUsage
			draft *cmsapi.DraftMediaState
			want  string
		}{
			{"processing", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateProcessing}
			}, nil, mediaStateProcessing},
			{"rejected", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateRejected}
			}, nil, mediaStateRejectedUnsupported},
			{"withdrawn", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateWithdrawn}
			}, nil, mediaStateUnavailableRemoved},
			{"superseded", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateSuperseded}
			}, nil, mediaStateUnavailableRemoved},
			{"unavailable", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateUnavailable}
			}, nil, mediaStateUnavailableRemoved},
			{"missing", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateMissing}
			}, nil, mediaStateUnavailableRemoved},
			{"published", func() *cmsapi.EditorialMediaUsage {
				url := "https://cdn.example.com/media-1.png"
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateReady, PublishedURL: &url}
			}, nil, mediaStatePublished},
			{"attached", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateReady}
			}, &cmsapi.DraftMediaState{}, mediaStateAttached},
			{"awaiting_review", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateReady}
			}, &cmsapi.DraftMediaState{ActiveReviewerIDs: []string{"reviewer"}}, mediaStateAwaitingReview},
			{"stale_verdict", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateReady}
			}, &cmsapi.DraftMediaState{Verdicts: []cmsapi.DraftReviewVerdictRecord{{
				Verdict: cmsapi.DraftReviewVerdictApproved, Current: false, Stale: true, RecordedAt: "2026-08-24T12:00:00Z",
			}}}, mediaStateStale},
			{"approved_for_revision", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateReady}
			}, &cmsapi.DraftMediaState{Verdicts: []cmsapi.DraftReviewVerdictRecord{{
				Verdict: cmsapi.DraftReviewVerdictApproved, Current: false, Stale: false, RecordedAt: "2026-08-24T12:00:00Z",
			}}}, mediaStateApprovedForRevision},
			{"current_verdict_stays_attached", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: cmsapi.EditorialMediaStateReady}
			}, &cmsapi.DraftMediaState{Verdicts: []cmsapi.DraftReviewVerdictRecord{{
				Verdict: cmsapi.DraftReviewVerdictApproved, Current: true, Stale: false, RecordedAt: "2026-08-24T12:00:00Z",
			}}}, mediaStateAttached},
			{"unknown_state_fails_toward_received", func() *cmsapi.EditorialMediaUsage {
				return &cmsapi.EditorialMediaUsage{State: "SOMETHING_NEW"}
			}, nil, mediaStateReceived},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := mediaEnvelopeStateForUsage(tc.draft, tc.usage()); got != tc.want {
					t.Fatalf("state → %q, want %q", got, tc.want)
				}
			})
		}
	})
}

// TestMediaStateModes drives both media_state modes through the GraphQL stub.
func TestMediaStateModes(t *testing.T) {
	t.Run("upload_grant_mode", func(t *testing.T) {
		newMediaGraphQLStub(t, map[string]string{
			"BodyUploadGrant": `{"data":{"uploadGrant":` + mediaFixtureUploadGrantJSON(cmsapi.UploadGrantStatusMinted, "https://presign.example.com/put/media-1.png") + `}}`,
		})
		result, err := handleMediaState(mediaTestContext(), json.RawMessage(`{"grant_id":"grant-1"}`))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("grant state failed: %+v err=%v", result, err)
		}
		data := structuredData(t, result)
		if got := data["state"]; got != mediaStateReceived {
			t.Fatalf("grant state = %v, want received", got)
		}
		if got := data["grantState"]; got != cmsapi.UploadGrantStatusMinted {
			t.Fatalf("grantState = %v, want MINTED", got)
		}
		if data["guidance"] == nil {
			t.Fatalf("minted grant state must carry re-signing guidance")
		}
	})

	t.Run("draft_binding_mode", func(t *testing.T) {
		usage := mediaFixtureUsageJSON()
		newMediaGraphQLStub(t, map[string]string{
			"BodyDraftEditorialMedia": `{"data":{"draftReview":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":3,"editorialMedia":[` + usage + `],"activeReviewerIds":["reviewer"],"verdicts":[],"publishEligibility":{"eligible":false,"blockingReasons":["REVIEW_APPROVAL_REQUIRED"],"reviewersApproved":false,"principalApprovalRequired":true,"principalApproved":false}}}}`,
		})
		result, err := handleMediaState(mediaTestContext(), json.RawMessage(`{"draft_id":"draft-1","media_id":"media-1"}`))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("binding state failed: %+v err=%v", result, err)
		}
		data := structuredData(t, result)
		if got := data["state"]; got != mediaStateAwaitingReview {
			t.Fatalf("binding state = %v, want awaiting_review", got)
		}
		if got := data["mode"]; got != "draft_binding" {
			t.Fatalf("mode = %v", got)
		}
	})

	t.Run("unbound_media_is_not_found", func(t *testing.T) {
		newMediaGraphQLStub(t, map[string]string{
			"BodyDraftEditorialMedia": `{"data":{"draftReview":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":3,"editorialMedia":[],"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
		})
		result, err := handleMediaState(mediaTestContext(), json.RawMessage(`{"draft_id":"draft-1","media_id":"media-9"}`))
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("unbound media must surface media_not_found, got %+v", result)
		}
		errorPayload, _ := result.StructuredContent["error"].(map[string]any)
		if errorPayload == nil || errorPayload["code"] != mediaErrorNotFound {
			t.Fatalf("error code = %v, want %v", errorPayload["code"], mediaErrorNotFound)
		}
	})
}

func TestMediaReadMintsGrantScopedExactAssetURL(t *testing.T) {
	newMediaGraphQLStub(t, map[string]string{
		"BodyDraftEditorialMediaAccess": `{"data":{"draftEditorialMediaAccess":{"mediaId":"media-1","url":"https://media.example.com/exact.png?signature=review","expiresAt":"2026-08-24T12:30:00Z","contentHash":"sha256:` + strings.Repeat("a", 64) + `"}}}`,
	})
	result, err := handleMediaRead(mediaTestContext(), json.RawMessage(`{"draft_id":"draft-1","media_id":"media-1"}`))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("media_read failed: %+v err=%v", result, err)
	}
	data := structuredData(t, result)
	access, _ := data["access"].(map[string]any)
	if access == nil || access["url"] == nil || access["expiresAt"] == nil {
		t.Fatalf("media_read missing access surface: %+v", data)
	}
}

// TestMediaActorIsolation pins that a non-owner caller cannot finalize another
// actor's grant: lesser's owner-scoped finalize returns ErrUploadGrantNotFound
// as a GraphQL error ("upload grant not found") — never data:null — and body
// classifies it to media_not_found/404.
func TestMediaActorIsolation(t *testing.T) {
	newMediaGraphQLStub(t, map[string]string{
		"BodyFinalizeUploadGrant": `{"data":null,"errors":[{"message":"upload grant not found","path":["finalizeUploadGrant"]}]}`,
	})
	result, err := handleUploadFinalize(mediaTestContext(), json.RawMessage(`{"grant_id":"alice-grant"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("cross-actor finalize must fail, got %+v", result)
	}
	errorPayload, _ := result.StructuredContent["error"].(map[string]any)
	if errorPayload == nil {
		t.Fatalf("error envelope missing")
	}
	if got := errorPayload["code"]; got != mediaErrorNotFound {
		t.Fatalf("isolation error code = %v, want %v", got, mediaErrorNotFound)
	}
}

// TestMediaMintValidationFailures pins body-side pre-validation before any
// lesser call for the mint contract (image/*, canonical type, sha256 shape).
func TestMediaMintValidationFailures(t *testing.T) {
	stub := newMediaGraphQLStub(t, map[string]string{})
	for _, tc := range []struct {
		name string
		args string
	}{
		{"non_image", `{"content_type":"video/mp4","max_size_bytes":1024,"sha256":"` + strings.Repeat("a", 64) + `"}`},
		{"parameterized_type", `{"content_type":"image/png; charset=utf-8","max_size_bytes":1024,"sha256":"` + strings.Repeat("a", 64) + `"}`},
		{"bad_sha256", `{"content_type":"image/png","max_size_bytes":1024,"sha256":"not-hex"}`},
		{"over_cap", `{"content_type":"image/png","max_size_bytes":999999999,"sha256":"` + strings.Repeat("a", 64) + `"}`},
		{"missing", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handleUploadGrantMint(mediaTestContext(), json.RawMessage(tc.args))
			// Handlers return InvalidParamsError directly; the registerTool
			// wrapper converts it into the invalid_params envelope on the wire.
			if err == nil && result == nil {
				t.Fatalf("invalid mint must fail client-side")
			}
			var paramsErr *InvalidParamsError
			if err != nil && !errors.As(err, &paramsErr) {
				t.Fatalf("invalid mint error type = %T, want *InvalidParamsError", err)
			}
		})
	}
	if len(stub.operations()) != 0 {
		t.Fatalf("client-side validation must not call lesser, ops=%v", stub.operations())
	}
}

// TestMediaActAsHeaderThreaded pins that a share-grant caller's mint carries
// the X-Lesser-Act-As header naming the actor route.
func TestMediaActAsHeaderThreaded(t *testing.T) {
	stub := newMediaGraphQLStub(t, map[string]string{
		"BodyMintUploadGrant": `{"data":{"mintUploadGrant":` + mediaFixtureUploadGrantJSON(cmsapi.UploadGrantStatusMinted, "https://presign.example.com/put/media-1.png") + `}}`,
	})
	ctx := mediaTestContextWithShareCaller("alice")
	result, err := handleUploadGrantMint(ctx, json.RawMessage(`{"content_type":"image/png","max_size_bytes":1024,"sha256":"`+strings.Repeat("a", 64)+`"}`))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("share-grant mint failed: %+v err=%v", result, err)
	}
	if got := stub.lastHeader(lesserapi.ActAsHeader); got != "agent1" {
		t.Fatalf("X-Lesser-Act-As = %q, want %q", got, "agent1")
	}
}

// TestDraftMediaAttachDetachReorder drives the read-modify-write binding tools
// against the full-list setDraftEditorialMedia contract.
func TestDraftMediaAttachDetachReorder(t *testing.T) {
	usage := mediaFixtureUsageJSON()
	usageList := `[` + usage + `]`
	emptyList := `[]`
	newMediaGraphQLStub(t, map[string]string{
		"BodyDraftEditorialMedia":    `{"data":{"draftReview":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":3,"editorialMedia":[],"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
		"BodySetDraftEditorialMedia": `{"data":{"setDraftEditorialMedia":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":4,"editorialMedia":` + usageList + `,"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
	})

	attach, err := handleDraftMediaAttach(mediaTestContext(), json.RawMessage(`{"draft_id":"draft-1","media_id":"media-1","role":"HERO","caption":"Launch artwork","alt":"A rocket"}`))
	if err != nil || attach == nil || attach.IsError {
		t.Fatalf("attach failed: %+v err=%v", attach, err)
	}
	attachData := structuredData(t, attach)
	if got := attachData["operation"]; got != "attached" {
		t.Fatalf("attach operation = %v", got)
	}

	// Reorder replaces the full ordered association; the read stub reflects the
	// attached state and the set returns the reordered result.
	newMediaGraphQLStub(t, map[string]string{
		"BodyDraftEditorialMedia":    `{"data":{"draftReview":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":3,"editorialMedia":` + usageList + `,"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
		"BodySetDraftEditorialMedia": `{"data":{"setDraftEditorialMedia":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":4,"editorialMedia":` + usageList + `,"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
	})
	reorder, err := handleDraftMediaReorder(mediaTestContext(), json.RawMessage(`{"draft_id":"draft-1","media_ids":["media-1"]}`))
	if err != nil || reorder == nil || reorder.IsError {
		t.Fatalf("reorder failed: %+v err=%v", reorder, err)
	}
	if got := structuredData(t, reorder)["operation"]; got != "reordered" {
		t.Fatalf("reorder operation = %v", got)
	}

	// Detach against a stub whose current bindings list is empty keeps the
	// unchanged empty list through the full-list contract.
	newMediaGraphQLStub(t, map[string]string{
		"BodyDraftEditorialMedia":    `{"data":{"draftReview":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":3,"editorialMedia":[],"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
		"BodySetDraftEditorialMedia": `{"data":{"setDraftEditorialMedia":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":4,"editorialMedia":` + emptyList + `,"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
	})
	detach, err := handleDraftMediaDetach(mediaTestContext(), json.RawMessage(`{"draft_id":"draft-1","media_id":"media-1"}`))
	if err != nil || detach == nil || detach.IsError {
		t.Fatalf("detach failed: %+v err=%v", detach, err)
	}
	if got := structuredData(t, detach)["operation"]; got != "detached" {
		t.Fatalf("detach operation = %v", got)
	}

	// Reorder must reject ids not bound to the draft.
	newMediaGraphQLStub(t, map[string]string{
		"BodyDraftEditorialMedia": `{"data":{"draftReview":{"draftId":"draft-1","contentHash":"sha256:` + strings.Repeat("b", 64) + `","revision":3,"editorialMedia":[],"activeReviewerIds":[],"verdicts":[],"publishEligibility":{"eligible":true,"blockingReasons":[],"reviewersApproved":true,"principalApprovalRequired":false,"principalApproved":false}}}}`,
	})
	bad, err := handleDraftMediaReorder(mediaTestContext(), json.RawMessage(`{"draft_id":"draft-1","media_ids":["media-other"]}`))
	if err == nil && bad == nil {
		t.Fatalf("reorder with unbound media must fail client-side")
	}
	var paramsErr *InvalidParamsError
	if err != nil && !errors.As(err, &paramsErr) {
		t.Fatalf("reorder with unbound media error type = %T, want *InvalidParamsError", err)
	}
}

func structuredData(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	// Round-trip through JSON like the MCP wire: the raw StructuredContent map
	// holds typed pointers; strict clients validate the serialized form.
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structuredContent: %v", err)
	}
	var roundTripped map[string]any
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("unmarshal structuredContent: %v", err)
	}
	data, _ := roundTripped["data"].(map[string]any)
	if data == nil {
		t.Fatalf("structuredContent.data missing: %s", encoded)
	}
	return data
}

func mediaFixtureUsageJSON() string {
	contentHash := "sha256:" + strings.Repeat("a", 64)
	return fmt.Sprintf(`{"mediaId":"media-1","role":"HERO","caption":"Launch artwork","creditLine":"Illustration by Alice","altText":"A rocket leaving a violet planet","effectiveAltText":"A rocket leaving a violet planet","state":"READY","contentHash":%q,"provenance":{"origin":"ILLUSTRATED","responsibleActorId":"alice","sourceReferences":[],"recordedAt":"2026-08-24T12:00:00Z","contentIntegrity":%q}}`, contentHash, contentHash)
}

// normalizeErrorStatus tolerates the int/float64 representation of the status
// field across the in-memory and JSON-round-tripped envelopes.
func normalizeErrorStatus(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return 0
}
