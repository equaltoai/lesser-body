package mcpserver

import (
	"reflect"
	"sort"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

func registeredToolDefsForTest(t *testing.T) []mcpruntime.ToolDef {
	t.Helper()

	r := mcpruntime.NewToolRegistry()
	if err := registerTools(r); err != nil {
		t.Fatalf("registerTools: %v", err)
	}
	return r.List()
}

// A tool that is registered without a classification entry must be unreachable,
// not readable. This is the fail-closed guarantee: it is what stops a future
// side-effecting tool from silently inheriting read scope.
func TestRegisteredToolWithoutClassificationRequiresStrictestScope(t *testing.T) {
	registered := registeredToolSnapshot{
		names: map[string]struct{}{"future_side_effecting_tool": {}},
	}

	got := requiredScopesForTool("future_side_effecting_tool", map[string][]string{}, registered)
	if !reflect.DeepEqual(got, []string{StrictestToolScope}) {
		t.Fatalf("registered-but-unclassified tool must require %q, got %v", StrictestToolScope, got)
	}
	if reflect.DeepEqual(got, []string{ScopeRead}) {
		t.Fatalf("registered-but-unclassified tool must never default to read")
	}
}

// If the registered surface cannot be derived we cannot prove a name is
// unregistered, so the classifier must still fail closed.
func TestUnderivableRegisteredSurfaceFailsClosed(t *testing.T) {
	registered := registeredToolSnapshot{err: errForTest{}}

	got := requiredScopesForTool("anything_at_all", map[string][]string{}, registered)
	if !reflect.DeepEqual(got, []string{StrictestToolScope}) {
		t.Fatalf("undecidable registration must require %q, got %v", StrictestToolScope, got)
	}
}

type errForTest struct{}

func (errForTest) Error() string { return "registry unavailable" }

// An unregistered name has no handler and can cause no side effect, so it carries
// no scope requirement and the MCP runtime answers tool-not-found. This preserves
// the JSON-RPC error contract for clients that mistype a tool name.
func TestUnregisteredToolCarriesNoScopeRequirement(t *testing.T) {
	if got := RequiredScopesForTool("phone_call"); got != nil {
		t.Fatalf("unregistered phone_call must carry no scope requirement, got %v", got)
	}
	if got := RequiredScopesForTool("totally_made_up_tool"); got != nil {
		t.Fatalf("unregistered tool must carry no scope requirement, got %v", got)
	}
}

// Completeness: every tool registerTools actually registers must carry an explicit
// classification. This test fails the moment a tool is added without one.
func TestEveryRegisteredToolIsClassified(t *testing.T) {
	var missing []string
	for _, def := range registeredToolDefsForTest(t) {
		if _, ok := toolScopes[def.Name]; !ok {
			missing = append(missing, def.Name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("registered tools missing an explicit scope classification in toolScopes: %v", missing)
	}
}

// The converse: a classification entry that no longer maps to a registered tool is
// stale and must be removed. phone_call drifted this way once already.
func TestNoStaleToolScopeEntries(t *testing.T) {
	registered := make(map[string]struct{})
	for _, def := range registeredToolDefsForTest(t) {
		registered[def.Name] = struct{}{}
	}

	var stale []string
	for name := range toolScopes {
		if _, ok := registered[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("toolScopes entries for tools that are not registered: %v", stale)
	}
}

// Every classification must be a scope we actually recognise.
func TestToolScopeValuesAreKnown(t *testing.T) {
	for name, scopes := range toolScopes {
		if len(scopes) == 0 {
			t.Fatalf("tool %q has an empty scope classification", name)
		}
		for _, s := range scopes {
			switch s {
			case ScopeRead, ScopeWrite, ScopeAdmin:
			default:
				t.Fatalf("tool %q declares unknown scope %q", name, s)
			}
		}
	}
}

// Cross-check the classification against the tool's own MCP annotation wherever one
// is declared: a tool that advertises ReadOnlyHint=true must not be classified
// write, and a tool that advertises ReadOnlyHint=false must not be classified read.
// Annotation coverage is currently partial, so this checks consistency where an
// annotation exists rather than requiring one.
func TestToolScopesAgreeWithReadOnlyAnnotations(t *testing.T) {
	for _, def := range registeredToolDefsForTest(t) {
		if def.Annotations == nil || def.Annotations.ReadOnlyHint == nil {
			continue
		}
		scopes, ok := toolScopes[def.Name]
		if !ok {
			continue // TestEveryRegisteredToolIsClassified owns this failure.
		}

		readOnly := *def.Annotations.ReadOnlyHint
		classifiedRead := reflect.DeepEqual(scopes, []string{ScopeRead})

		if readOnly && !classifiedRead {
			t.Errorf("tool %q is annotated read-only but classified %v", def.Name, scopes)
		}
		if !readOnly && classifiedRead {
			t.Errorf("tool %q is annotated as mutating but classified read", def.Name)
		}
	}
}

// The declared write surface, pinned. Changing this set is a scope contract change
// for every connected MCP client and must be a deliberate edit to this list.
func TestWriteScopedToolSurfaceIsPinned(t *testing.T) {
	want := []string{
		"article_draft_create",
		"article_draft_publish",
		"article_draft_review_submit",
		"article_draft_review_verdict",
		"article_draft_update",
		"article_update",
		"email_delete",
		"email_mark_read",
		"email_mark_unread",
		"email_reply",
		"email_send",
		"follow",
		"memory_append",
		"message_request_accept",
		"message_request_decline",
		"notification_dismiss",
		"post_boost",
		"post_create",
		"post_favorite",
		"profile_update",
		"sms_send",
		"soul_self_recover",
		"unfollow",
	}

	var got []string
	for name, scopes := range toolScopes {
		if len(scopes) == 1 && scopes[0] == ScopeWrite {
			got = append(got, name)
		}
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("write-scoped tool surface changed.\n got: %v\nwant: %v", got, want)
	}
}
