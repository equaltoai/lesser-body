package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
)

func TestDescribeInterfaceCoversEveryRegisteredTool(t *testing.T) {
	registered := make(map[string]struct{})
	for _, def := range registeredToolDefsForTest(t) {
		registered[def.Name] = struct{}{}
	}

	catalog := make(map[string]struct{})
	var duplicates []string
	for _, domain := range describeInterfaceDomains {
		if strings.TrimSpace(domain.Name) == "" {
			t.Fatal("describe_interface domain name must not be empty")
		}
		for _, tool := range domain.Tools {
			if strings.TrimSpace(tool.Name) == "" || strings.TrimSpace(tool.Use) == "" {
				t.Fatalf("describe_interface domain %q has an incomplete tool entry: %+v", domain.Name, tool)
			}
			if _, ok := catalog[tool.Name]; ok {
				duplicates = append(duplicates, tool.Name)
			}
			catalog[tool.Name] = struct{}{}
		}
	}
	sort.Strings(duplicates)
	if len(duplicates) > 0 {
		t.Fatalf("describe_interface catalog contains duplicate tools: %v", duplicates)
	}

	var missing, stale, absentFromText []string
	text := renderDescribeInterface(context.Background())
	for name := range registered {
		if _, ok := catalog[name]; !ok {
			missing = append(missing, name)
		}
		if !strings.Contains(text, fmt.Sprintf("- `%s` —", name)) {
			absentFromText = append(absentFromText, name)
		}
	}
	for name := range catalog {
		if _, ok := registered[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(absentFromText)
	if len(missing) > 0 || len(stale) > 0 || len(absentFromText) > 0 {
		t.Fatalf("describe_interface drift: missing=%v stale=%v absent_from_text=%v", missing, stale, absentFromText)
	}
	if got, max := len([]byte(text)), 16*1024; got > max {
		t.Fatalf("describe_interface text is too large for a bootstrap result: got %d bytes, max %d", got, max)
	}
}

func TestDescribeInterfaceReturnsIdentityAndCurrentReadGuidanceInText(t *testing.T) {
	t.Setenv("MCP_ENDPOINT", "https://api.dev.example.com/mcp/{actor}")

	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
	}, "")
	ctx = runtimepolicy.WithContext(ctx, runtimepolicy.Resolved{
		Profile:             runtimepolicy.ProfileSouled,
		Determined:          true,
		DeterminationSource: "soulbinding_present",
		BoundSoul:           true,
		SoulAgentID:         "agent1.dev.example.com",
	})

	result, err := handleDescribeInterface(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handleDescribeInterface: %v", err)
	}
	if result == nil || len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("describe_interface must return one text block, got %+v", result)
	}

	text := result.Content[0].Text
	for _, want := range []string{
		"actor: `agent1`",
		"instance: `dev.example.com`",
		"runtime_profile: `souled`",
		"soul_binding: `bound`",
		"soul_agent_id: `agent1.dev.example.com`",
		"`timeline_read({\"timeline\":\"home\",\"limit\":5,\"view\":\"compact\"})`",
		"`post_get({\"id\":\"<status-id>\",\"view\":\"standard\"})`",
		"`conversations_read({\"limit\":10,\"view\":\"compact\"})`",
		"`conversation_get({\"conversationId\":\"<conversation-id>\",\"limit\":20,\"view\":\"compact\"})`",
		"`notifications_read({\"limit\":10,\"view\":\"compact\"})`",
		"`notification_get({\"id\":\"<notification-id>\",\"view\":\"standard\"})`",
		"`article_draft_preview`",
		"`article_draft_review_submit`",
		"`article_draft_review_read`",
		"`article_draft_review_verdict`",
		"`article_draft_publish`",
		"`preview_chars`",
		"`max_output_bytes`",
		"nested `payload` value (never a JSON-encoded string)",
		"`structuredContent.data`",
		"`expand.resultPath=\"content[0].text\"`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("describe_interface text missing %q", want)
		}
	}
}

func TestDescribeInterfaceIsReadOnlyAndAvailableToBothProfiles(t *testing.T) {
	def := describeInterfaceDef()
	if def.Annotations == nil || def.Annotations.ReadOnlyHint == nil || !*def.Annotations.ReadOnlyHint {
		t.Fatalf("describe_interface must carry an explicit read-only annotation: %+v", def.Annotations)
	}
	if got := RequiredScopesForTool(def.Name); len(got) != 1 || got[0] != ScopeRead {
		t.Fatalf("describe_interface scope = %v, want [%s]", got, ScopeRead)
	}
	for _, profile := range []runtimepolicy.Profile{runtimepolicy.ProfileDrone, runtimepolicy.ProfileSouled} {
		if !runtimepolicy.ToolAllowed(profile, def.Name) {
			t.Errorf("describe_interface must be available to %s profile", profile)
		}
	}
}

func TestDescribeInterfaceRejectsArguments(t *testing.T) {
	if _, err := handleDescribeInterface(context.Background(), json.RawMessage(`{"view":"compact"}`)); err == nil {
		t.Fatal("describe_interface should reject unexpected arguments")
	}
}
