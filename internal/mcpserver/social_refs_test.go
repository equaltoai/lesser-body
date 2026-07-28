package mcpserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCompactSocialAccountRefUsesOnlySourceBackedFields(t *testing.T) {
	ref := compactSocialAccountRef(map[string]any{
		"id":           "acct-1",
		"username":     "alice",
		"acct":         "alice@example.com",
		"display_name": "Alice",
		"url":          "https://example.com/@alice",
		"note":         strings.Repeat("profile ", 100),
	})
	if ref == nil {
		t.Fatal("expected account ref")
	}
	if ref.ID != "acct-1" || ref.Acct != "alice@example.com" || ref.DisplayName != "Alice" || ref.URL != "https://example.com/@alice" {
		t.Fatalf("unexpected account ref: %+v", ref)
	}
	if len(ref.MissingFields) != 0 {
		t.Fatalf("did not expect missing fields when source supplied all stable fields: %+v", ref)
	}

	missing := compactSocialAccountRef(map[string]any{
		"id":       "acct-2",
		"username": "bob",
	})
	if missing == nil {
		t.Fatal("expected partial account ref")
	}
	if missing.Acct != "" || missing.DisplayName != "" || missing.URL != "" {
		t.Fatalf("partial account ref must not guess from username or other fields: %+v", missing)
	}
	if !reflect.DeepEqual(missing.MissingFields, []string{"acct", "displayName", "url"}) {
		t.Fatalf("missing fields = %#v", missing.MissingFields)
	}
}

func TestCompactSocialStatusRefNamesOmissionsAndExpansionPath(t *testing.T) {
	ref := compactSocialStatusRef(map[string]any{
		"id":         "post-1",
		"url":        "https://example.com/@alice/post-1",
		"created_at": "2026-05-17T15:00:00Z",
		"visibility": "public",
		"content":    "IMPORTANT " + strings.Repeat("long content ", 120),
		"account": map[string]any{
			"id":           "acct-1",
			"acct":         "alice@example.com",
			"display_name": "Alice",
			"url":          "https://example.com/@alice",
		},
		"debugPayload": map[string]any{"large": strings.Repeat("x", 4096)},
	})
	if ref == nil {
		t.Fatal("expected status ref")
	}
	if ref.ID != "post-1" || ref.URL != "https://example.com/@alice/post-1" || ref.CreatedAt != "2026-05-17T15:00:00Z" || ref.Visibility != "public" {
		t.Fatalf("unexpected stable status fields: %+v", ref)
	}
	if ref.AuthorRef == nil || ref.AuthorRef.ID != "acct-1" || ref.AuthorRef.Acct != "alice@example.com" {
		t.Fatalf("expected compact author ref, got %+v", ref.AuthorRef)
	}
	if ref.ContentPreview == "" || !ref.ContentTruncated || len([]rune(ref.ContentPreview)) > socialStatusContentPreviewRunes {
		t.Fatalf("expected bounded truncated content preview, got len=%d truncated=%v", len([]rune(ref.ContentPreview)), ref.ContentTruncated)
	}
	if strings.Contains(ref.ContentPreview, "debugPayload") || strings.Contains(ref.ContentPreview, strings.Repeat("x", 64)) {
		t.Fatalf("status ref leaked raw debug payload: %+v", ref)
	}
	if ref.Expand == nil || ref.Expand.Tool != "post_get" || ref.Expand.Arguments["id"] != "post-1" || ref.Expand.Arguments["view"] != readViewStandard {
		t.Fatalf("unexpected status expansion metadata: %+v", ref.Expand)
	}
	if len(ref.Omitted) != 1 {
		t.Fatalf("expected omitted content metadata, got %+v", ref.Omitted)
	}
	omitted := ref.Omitted[0]
	if omitted.Path != "content" || omitted.Reason != "content_preview" {
		t.Fatalf("unexpected omitted metadata: %+v", omitted)
	}
	if omitted.Expand.Tool != "post_get" || omitted.Expand.Arguments["id"] != "post-1" || omitted.Expand.ResultPath != socialExpansionResultPath {
		t.Fatalf("unexpected omitted expansion path: %+v", omitted.Expand)
	}

	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal status ref: %v", err)
	}
	for _, heavy := range []string{"debugPayload", strings.Repeat("x", 64)} {
		if strings.Contains(string(b), heavy) {
			t.Fatalf("compact status ref JSON leaked heavy raw field %q: %s", heavy, string(b))
		}
	}
}

func TestSocialStatusStandardPayloadIncludesFullContentForExpansion(t *testing.T) {
	content := "full content " + strings.Repeat("kept ", 120)
	status := socialStatusStandardPayload(map[string]any{
		"id":         "post-2",
		"uri":        "https://example.com/statuses/post-2",
		"created_at": "2026-05-17T16:00:00Z",
		"visibility": "unlisted",
		"content":    content,
		"account": map[string]any{
			"id":   "acct-2",
			"acct": "bob@example.com",
		},
	})
	if status["id"] != "post-2" || status["url"] != "https://example.com/statuses/post-2" || status["visibility"] != "unlisted" {
		t.Fatalf("unexpected standard status fields: %+v", status)
	}
	if status["content"] != strings.TrimSpace(content) {
		t.Fatalf("standard post_get expansion must keep full content, got %+v", status["content"])
	}
	author, _ := status["authorRef"].(*AccountRef)
	if author == nil || author.ID != "acct-2" || author.Acct != "bob@example.com" {
		t.Fatalf("expected standard expansion author ref, got %+v", status["authorRef"])
	}
}

func TestSocialPerItemExpansionsPointAtTextResultSurface(t *testing.T) {
	tests := []struct {
		name      string
		expansion *SocialExpansionRef
	}{
		{
			name:      "post",
			expansion: socialPostGetExpansion("post-1", readViewStandard),
		},
		{
			name:      "post content",
			expansion: socialPostGetExpansion("post-1", readViewStandard),
		},
		{
			name:      "conversation",
			expansion: socialConversationGetExpansion("conversation-1", readViewCompact),
		},
		{
			name:      "notification",
			expansion: socialNotificationGetExpansion("notification-1", readViewStandard),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expansion == nil {
				t.Fatal("expected expansion metadata")
			}
			if tt.expansion.ResultPath != socialExpansionResultPath ||
				strings.Contains(tt.expansion.ResultPath, "structuredContent") {
				t.Fatalf("per-item expansion is structuredContent-only: %+v", tt.expansion)
			}
		})
	}
}
