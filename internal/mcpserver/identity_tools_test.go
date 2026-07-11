package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
)

type fakeTableTheoryDB struct {
	firstFn func(dest any, where map[string]any) error
}

func (f *fakeTableTheoryDB) Model(any) tablecore.Query {
	return &fakeTableTheoryQuery{
		where:   map[string]any{},
		firstFn: f.firstFn,
	}
}

func (f *fakeTableTheoryDB) Migrate() error                           { return nil }
func (f *fakeTableTheoryDB) AutoMigrate(...any) error                 { return nil }
func (f *fakeTableTheoryDB) Close() error                             { return nil }
func (f *fakeTableTheoryDB) WithContext(context.Context) tablecore.DB { return f }

type fakeTableTheoryQuery struct {
	where   map[string]any
	firstFn func(dest any, where map[string]any) error
}

func (q *fakeTableTheoryQuery) Where(field string, _ string, value any) tablecore.Query {
	q.where[field] = value
	return q
}

func (q *fakeTableTheoryQuery) Index(string) tablecore.Query                        { return q }
func (q *fakeTableTheoryQuery) Filter(string, string, any) tablecore.Query          { return q }
func (q *fakeTableTheoryQuery) OrFilter(string, string, any) tablecore.Query        { return q }
func (q *fakeTableTheoryQuery) FilterGroup(func(tablecore.Query)) tablecore.Query   { return q }
func (q *fakeTableTheoryQuery) OrFilterGroup(func(tablecore.Query)) tablecore.Query { return q }
func (q *fakeTableTheoryQuery) IfNotExists() tablecore.Query                        { return q }
func (q *fakeTableTheoryQuery) IfExists() tablecore.Query                           { return q }
func (q *fakeTableTheoryQuery) WithCondition(string, string, any) tablecore.Query   { return q }
func (q *fakeTableTheoryQuery) WithConditionExpression(string, map[string]any) tablecore.Query {
	return q
}
func (q *fakeTableTheoryQuery) OrderBy(string, string) tablecore.Query       { return q }
func (q *fakeTableTheoryQuery) Limit(int) tablecore.Query                    { return q }
func (q *fakeTableTheoryQuery) Offset(int) tablecore.Query                   { return q }
func (q *fakeTableTheoryQuery) Select(...string) tablecore.Query             { return q }
func (q *fakeTableTheoryQuery) ConsistentRead() tablecore.Query              { return q }
func (q *fakeTableTheoryQuery) WithRetry(int, time.Duration) tablecore.Query { return q }
func (q *fakeTableTheoryQuery) All(any) error                                { return nil }
func (q *fakeTableTheoryQuery) AllPaginated(any) (*tablecore.PaginatedResult, error) {
	return nil, nil
}
func (q *fakeTableTheoryQuery) Count() (int64, error)                     { return 0, nil }
func (q *fakeTableTheoryQuery) Create() error                             { return nil }
func (q *fakeTableTheoryQuery) CreateOrUpdate() error                     { return nil }
func (q *fakeTableTheoryQuery) Update(...string) error                    { return nil }
func (q *fakeTableTheoryQuery) UpdateBuilder() tablecore.UpdateBuilder    { return nil }
func (q *fakeTableTheoryQuery) Delete() error                             { return nil }
func (q *fakeTableTheoryQuery) Scan(any) error                            { return nil }
func (q *fakeTableTheoryQuery) ParallelScan(int32, int32) tablecore.Query { return q }
func (q *fakeTableTheoryQuery) ScanAllSegments(any, int32) error          { return nil }
func (q *fakeTableTheoryQuery) BatchGet([]any, any) error                 { return nil }
func (q *fakeTableTheoryQuery) BatchGetWithOptions([]any, any, *tablecore.BatchGetOptions) error {
	return nil
}
func (q *fakeTableTheoryQuery) BatchGetBuilder() tablecore.BatchGetBuilder { return nil }
func (q *fakeTableTheoryQuery) BatchCreate(any) error                      { return nil }
func (q *fakeTableTheoryQuery) BatchDelete([]any) error                    { return nil }
func (q *fakeTableTheoryQuery) BatchWrite([]any, []any) error              { return nil }
func (q *fakeTableTheoryQuery) BatchUpdateWithOptions([]any, []string, ...any) error {
	return nil
}
func (q *fakeTableTheoryQuery) Cursor(string) tablecore.Query               { return q }
func (q *fakeTableTheoryQuery) SetCursor(string) error                      { return nil }
func (q *fakeTableTheoryQuery) WithContext(context.Context) tablecore.Query { return q }

func (q *fakeTableTheoryQuery) First(dest any) error {
	if q.firstFn == nil {
		return nil
	}
	return q.firstFn(dest, q.where)
}

func setStructFields(dest any, values map[string]string) {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return
	}
	for field, value := range values {
		fv := elem.FieldByName(field)
		if !fv.IsValid() || !fv.CanSet() || fv.Kind() != reflect.String {
			continue
		}
		fv.SetString(value)
	}
}

func TestCurrentManagedSoulEmailAddressDetection(t *testing.T) {
	tests := []struct {
		name    string
		address string
		localID string
		want    bool
	}{
		{
			name:    "instance scoped managed address is current",
			address: "agent-alice.simulacrum@lessersoul.ai",
			localID: "agent-alice",
			want:    true,
		},
		{
			name:    "dotted local id remains opaque",
			address: "ops.v2.simulacrum@lessersoul.ai",
			localID: "ops.v2",
			want:    true,
		},
		{
			name:    "legacy bare managed address is inbound-only",
			address: "agent-alice@lessersoul.ai",
			localID: "agent-alice",
			want:    false,
		},
		{
			name:    "legacy bare managed address fails closed without local id",
			address: "agent-alice@lessersoul.ai",
			localID: "",
			want:    false,
		},
		{
			name:    "legacy bare managed address fails closed with invalid local id",
			address: "agent-alice@lessersoul.ai",
			localID: "agent/alice",
			want:    false,
		},
		{
			name:    "external private email is not a public managed channel",
			address: "agent-alice@example.com",
			localID: "agent-alice",
			want:    false,
		},
		{
			name:    "missing local id fails closed",
			address: "agent-alice.simulacrum@lessersoul.ai",
			localID: "",
			want:    false,
		},
		{
			name:    "invalid local id fails closed",
			address: "agent-alice.simulacrum@lessersoul.ai",
			localID: "agent/alice",
			want:    false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isCurrentManagedSoulEmailAddress(tc.address, tc.localID); got != tc.want {
				t.Fatalf("isCurrentManagedSoulEmailAddress(%q, %q) = %v, want %v", tc.address, tc.localID, got, tc.want)
			}
		})
	}
}

func installSoulBindingLookup(t *testing.T, username string, agentID string) {
	t.Helper()
	t.Setenv("LESSER_TABLE_NAME", "test-main-table")
	soulbinding.ResetForTests()
	t.Cleanup(soulbinding.ResetForTests)
	soulbinding.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{
			firstFn: func(dest any, where map[string]any) error {
				setStructFields(dest, map[string]string{
					"AgentID":  agentID,
					"Username": username,
				})
				return nil
			},
		}, nil
	})
}

func boundSelfResponse(agentID string, username string, domain string, localID string) string {
	return fmt.Sprintf(`{"agent":{"agent_id":%q,"domain":%q,"local_id":%q,"status":"active","lifecycle_status":"active"},"binding_state":"bound","binding":{"agent_username":%q}}`, agentID, domain, localID, username)
}

func TestVerifyAuthenticatedAgentWithLesserAcceptsBoundSelfRuntimeAgent(t *testing.T) {
	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/souls/mine" {
			t.Fatalf("must not fall back to wallet inventory endpoint")
		}
		if r.URL.Path != "/api/v1/souls/bound/me" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("expected OAuth bearer passthrough, got %q", got)
		}
		_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)

	if err := verifyAuthenticatedAgentWithLesser(context.Background(), "oauth-token", agentID, "Agent1"); err != nil {
		t.Fatalf("verifyAuthenticatedAgentWithLesser: %v", err)
	}
}

func TestVerifyAuthenticatedAgentWithLesserRequiresBoundLocalAgent(t *testing.T) {
	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	tests := []struct {
		name     string
		response string
	}{
		{
			name:     "unbound",
			response: `{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice"},"binding_state":"unbound","binding":{"agent_username":"agent1"}}`,
		},
		{
			name:     "bound to another local agent",
			response: `{"agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice"},"binding_state":"bound","binding":{"agent_username":"agent2"}}`,
		},
		{
			name:     "bound to another soul agent",
			response: `{"agent":{"agent_id":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","domain":"test.example.com","local_id":"agent-alice"},"binding_state":"bound","binding":{"agent_username":"agent1"}}`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			lesserapi.ResetForTests()
			t.Cleanup(lesserapi.ResetForTests)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/api/v1/souls/bound/me" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
					t.Fatalf("expected OAuth bearer passthrough, got %q", got)
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()

			t.Setenv("LESSER_API_BASE_URL", server.URL)

			err := verifyAuthenticatedAgentWithLesser(context.Background(), "oauth-token", agentID, "Agent1")
			if err == nil {
				t.Fatalf("expected stale local binding to be rejected")
			}
			failure := mcpAuthFailureFromError(err)
			if failure == nil {
				t.Fatalf("expected MCP auth failure, got %T %v", err, err)
			}
			if failure.Code != "forbidden" || failure.Status != http.StatusForbidden {
				t.Fatalf("unexpected failure: %+v", failure)
			}
			if failure.Details["reason"] != "soul_binding_not_authorized" {
				t.Fatalf("unexpected failure details: %+v", failure.Details)
			}
		})
	}
}

func TestVerifyAuthenticatedAgentWithLesserDoesNotFallbackToSoulsMine(t *testing.T) {
	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)

	var mineCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no bound soul for authenticated principal","error_description":"no bound soul for authenticated principal","error_code":"soul_not_bound"}`))
		case "/api/v1/souls/mine":
			mineCalled = true
			_, _ = w.Write([]byte(`{"souls":[{"agent":{"agent_id":"` + agentID + `"},"binding_state":"bound","binding":{"agent_username":"agent1"}}],"count":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_API_BASE_URL", server.URL)

	err := verifyAuthenticatedAgentWithLesser(context.Background(), "oauth-token", agentID, "Agent1")
	if err == nil {
		t.Fatalf("expected bound-self 404 to fail closed")
	}
	if mineCalled {
		t.Fatalf("must not fall back to /api/v1/souls/mine after bound-self denial")
	}
	var userErr *toolUserError
	if !errors.As(err, &userErr) {
		t.Fatalf("expected toolUserError for bound-self denial, got %T %v", err, err)
	}
	if userErr.Code != "not_found" || userErr.Status != http.StatusNotFound {
		t.Fatalf("unexpected bound-self denial: %+v", userErr)
	}
}

func TestNormalizeCurrentInstanceLocalLookupQuery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "plain", raw: "medic", want: "medic", ok: true},
		{name: "dot_local", raw: "ops.v2", want: "ops.v2", ok: true},
		{name: "at_prefix", raw: "@Medic", want: "medic", ok: true},
		{name: "trailing_slash", raw: "medic/", want: "medic", ok: true},
		{name: "email", raw: "medic@example.com", ok: false},
		{name: "ens", raw: "medic.lessersoul.eth", ok: false},
		{name: "agent_id", raw: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ok: false},
		{name: "multi_dot_local", raw: "ops.v2.dev", want: "ops.v2.dev", ok: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := normalizeCurrentInstanceLocalLookupQuery(tc.raw, false)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("normalizeCurrentInstanceLocalLookupQuery(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestPrepareSoulLookupSearch_CurrentInstanceLocalQueryUsesAuthenticatedDomain(t *testing.T) {
	const agentID = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	installSoulBindingLookup(t, "Agent1", agentID)
	lesserapi.ResetForTests()
	t.Cleanup(lesserapi.ResetForTests)
	soulapi.ResetForTests()
	t.Cleanup(soulapi.ResetForTests)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/souls/bound/me":
			_, _ = w.Write([]byte(boundSelfResponse(agentID, "agent1", "test.example.com", "agent-alice")))
		case "/api/v1/soul/agents/" + agentID:
			_, _ = w.Write([]byte(`{"version":"1","agent":{"agent_id":"` + agentID + `","domain":"test.example.com","local_id":"agent-alice","status":"active"}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("LESSER_SOUL_API_BASE_URL", server.URL)
	t.Setenv("LESSER_API_BASE_URL", server.URL)

	client, err := soulapi.Default()
	if err != nil {
		t.Fatalf("soulapi.Default: %v", err)
	}

	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "Agent1",
		Claims:   &auth.Claims{Username: "Agent1"},
	}, "test-token")

	search, err := prepareSoulLookupSearch(ctx, client, "medic", false, false)
	if err != nil {
		t.Fatalf("prepareSoulLookupSearch: %v", err)
	}
	if search.Query != "medic" {
		t.Fatalf("query: want medic got %q", search.Query)
	}
	if search.Domain != "test.example.com" {
		t.Fatalf("domain: want test.example.com got %q", search.Domain)
	}

	search, err = prepareSoulLookupSearch(ctx, client, "ops.v2", false, false)
	if err != nil {
		t.Fatalf("prepareSoulLookupSearch dot_local: %v", err)
	}
	if search.Query != "ops.v2" {
		t.Fatalf("dot_local query: want ops.v2 got %q", search.Query)
	}
	if search.Domain != "test.example.com" {
		t.Fatalf("dot_local domain: want test.example.com got %q", search.Domain)
	}

	search, err = prepareSoulLookupSearch(ctx, client, "ops.eth", false, false)
	if err != nil {
		t.Fatalf("prepareSoulLookupSearch ens_like_without_fallback: %v", err)
	}
	if search.Query != "ops.eth" || search.Domain != "" {
		t.Fatalf("ens_like_without_fallback: want generic search query without domain, got %+v", search)
	}

	search, err = prepareSoulLookupSearch(ctx, client, "ops.eth", true, false)
	if err != nil {
		t.Fatalf("prepareSoulLookupSearch ens_like_with_fallback: %v", err)
	}
	if search.Query != "ops.eth" {
		t.Fatalf("ens_like_with_fallback query: want ops.eth got %q", search.Query)
	}
	if search.Domain != "test.example.com" {
		t.Fatalf("ens_like_with_fallback domain: want test.example.com got %q", search.Domain)
	}
}

func TestNormalizeRemoteActivityPubHandle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                    string
		raw                     string
		allowBareHandleFallback bool
		want                    soulLookupSearch
		ok                      bool
		wantErr                 bool
	}{
		{
			name: "acct_form",
			raw:  "@steward@remote.example",
			want: soulLookupSearch{Query: "remote.example/steward"},
			ok:   true,
		},
		{
			name:                    "bare_form_with_fallback",
			raw:                     "steward@remote.example",
			allowBareHandleFallback: true,
			want:                    soulLookupSearch{Query: "remote.example/steward"},
			ok:                      true,
		},
		{
			name:                    "bare_form_without_fallback",
			raw:                     "steward@remote.example",
			allowBareHandleFallback: false,
			ok:                      false,
		},
		{
			name: "ens_like_local_part_supported",
			raw:  "@ops.eth@remote.example",
			want: soulLookupSearch{Query: "remote.example/ops.eth"},
			ok:   true,
		},
		{
			name:    "invalid_domain",
			raw:     "@steward@remote.example/path",
			ok:      true,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := normalizeRemoteActivityPubHandle(tc.raw, tc.allowBareHandleFallback)
			if got != tc.want || ok != tc.ok || (err != nil) != tc.wantErr {
				t.Fatalf("normalizeRemoteActivityPubHandle(%q, %v) = (%+v, %v, %v), want (%+v, %v, err=%v)", tc.raw, tc.allowBareHandleFallback, got, ok, err, tc.want, tc.ok, tc.wantErr)
			}
		})
	}
}

func TestNormalizeCanonicalRemoteActorURL(t *testing.T) {
	t.Parallel()

	search, handled, err := normalizeCanonicalRemoteActorURL("https://remote.example/users/steward")
	if err != nil || !handled {
		t.Fatalf("canonical actor url: handled=%v err=%v", handled, err)
	}
	if search != (soulLookupSearch{Query: "remote.example/steward"}) {
		t.Fatalf("canonical actor url: got %+v", search)
	}

	search, handled, err = normalizeCanonicalRemoteActorURL("https://remote.example/users/ops.eth/")
	if err != nil || !handled {
		t.Fatalf("canonical actor url with dotted local id: handled=%v err=%v", handled, err)
	}
	if search != (soulLookupSearch{Query: "remote.example/ops.eth"}) {
		t.Fatalf("canonical actor url with dotted local id: got %+v", search)
	}

	_, handled, err = normalizeCanonicalRemoteActorURL("https://remote.example/actors/steward")
	if !handled || err == nil {
		t.Fatalf("unsupported actor url should be handled with an error, got handled=%v err=%v", handled, err)
	}
	userErr, ok := err.(*toolUserError)
	if !ok || userErr.Code != "invalid_request" {
		t.Fatalf("unsupported actor url should return invalid_request tool error, got %#v", err)
	}
}
