package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

func TestStructuredFirstReadToolsExposeSubstantiveTextData(t *testing.T) {
	const sentinel = "ka-data"

	payload := func() map[string]any {
		return map[string]any{
			"marker": sentinel,
			"items":  []any{map[string]any{"id": "item-1", "preview": sentinel}},
		}
	}
	socialPayload := func(name string) map[string]any {
		data := payload()
		switch name {
		case "timeline_read", "post_search":
			data["statuses"] = []any{map[string]any{"id": "status-1", "contentPreview": sentinel}}
		case "conversations_read":
			data["conversations"] = []any{map[string]any{
				"id":          "conversation-1",
				"lastPostRef": map[string]any{"contentPreview": sentinel},
			}}
		case "conversation_get":
			data["conversation"] = map[string]any{
				"messageRefs": []any{map[string]any{"id": "message-1", "contentPreview": sentinel}},
			}
		case "direct_messages_read":
			data["messages"] = []any{map[string]any{"id": "message-1", "contentPreview": sentinel}}
		case "notifications_read":
			data["notifications"] = []any{map[string]any{
				"id":            "notification-1",
				"targetPostRef": map[string]any{"contentPreview": sentinel},
			}}
		}
		return data
	}
	structuredFirst := func(name string) func() (*mcpruntime.ToolResult, error) {
		return func() (*mcpruntime.ToolResult, error) {
			return toolStructuredFirstResult(structuredFirstResultOptions{
				Summary: name + " result",
				Data:    payload(),
				Text:    map[string]any{"tool": name},
			})
		}
	}
	socialBudgeted := func(name string) func() (*mcpruntime.ToolResult, error) {
		return func() (*mcpruntime.ToolResult, error) {
			return toolStructuredFirstResultWithBudget(name, name+" result", socialPayload(name), nil, false, 64*1024)
		}
	}
	articleBudgeted := func(name string) func() (*mcpruntime.ToolResult, error) {
		return func() (*mcpruntime.ToolResult, error) {
			return articleDraftStructuredResult(name, readViewCompact, name+" result", payload(), map[string]any{"tool": name}, 64*1024)
		}
	}

	tests := []struct {
		name  string
		build func() (*mcpruntime.ToolResult, error)
	}{
		{name: "timeline_read", build: func() (*mcpruntime.ToolResult, error) {
			return socialCompactListToolResult("timeline_read", "timeline result", socialPayload("timeline_read"), 64*1024)
		}},
		{name: "post_search", build: func() (*mcpruntime.ToolResult, error) {
			return socialCompactListToolResult("post_search", "search result", socialPayload("post_search"), 64*1024)
		}},
		{name: "conversations_read", build: socialBudgeted("conversations_read")},
		{name: "conversation_get", build: socialBudgeted("conversation_get")},
		{name: "direct_messages_read", build: socialBudgeted("direct_messages_read")},
		{name: "notifications_read", build: socialBudgeted("notifications_read")},
		{name: "article_draft_get", build: articleBudgeted("article_draft_get")},
		{name: "article_draft_list", build: articleBudgeted("article_draft_list")},
		{name: "article_draft_preview", build: articleBudgeted("article_draft_preview")},
		{name: "article_get", build: articleBudgeted("article_get")},
		{name: "article_list", build: articleBudgeted("article_list")},
		{name: "message_requests_list", build: structuredFirst("message_requests_list")},
		{name: "soul_read", build: func() (*mcpruntime.ToolResult, error) {
			data := payload()
			data["souls"] = []any{map[string]any{"agentId": "agent-1"}}
			data["count"] = 1
			return soulReadSummaryToolResult(data)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scopes := RequiredScopesForTool(tt.name)
			if len(scopes) != 1 || scopes[0] != ScopeRead {
				t.Fatalf("%s is not a registered read tool: scopes=%v", tt.name, scopes)
			}
			result, err := tt.build()
			if err != nil {
				t.Fatalf("build result: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected tool error: %+v", result)
			}
			text := toolResultTextJSON(t, result)
			if !strings.Contains(result.Content[0].Text, sentinel) {
				t.Fatalf("text block omitted substantive sentinel: %s", result.Content[0].Text)
			}
			locator, ok := text["data"].(map[string]any)
			if !ok ||
				locator["location"] != "structuredContent.data" {
				t.Fatalf("text data locator must preserve the legacy location: %#v", locator)
			}
			if text["access"] != "payload or structuredContent.data" {
				t.Fatalf("text guidance must name both result surfaces: %#v", text["access"])
			}
			structuredData, _ := result.StructuredContent["data"].(map[string]any)
			if structuredData["marker"] != sentinel {
				t.Fatalf("structuredContent.data changed: %#v", result.StructuredContent)
			}
		})
	}
}

func TestStructuredFirstBudgetMeasuresSubstantiveTextPayload(t *testing.T) {
	payload := map[string]any{
		"items": []any{map[string]any{
			"id":      "item-1",
			"preview": strings.Repeat("bounded payload ", 64),
		}},
	}
	result, err := socialCompactListToolResult("timeline_read", "bounded timeline", payload, 0)
	if err != nil {
		t.Fatalf("build unbounded result: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, "bounded payload") {
		t.Fatalf("text payload is not substantive: %s", result.Content[0].Text)
	}
	measurement, err := measureToolResultPayload(result)
	if err != nil {
		t.Fatalf("measure result: %v", err)
	}

	atBudget, err := socialCompactListToolResult("timeline_read", "bounded timeline", payload, measurement.JSONRPCEnvelopeBytes)
	if err != nil {
		t.Fatalf("build result at budget: %v", err)
	}
	if atBudget.IsError {
		t.Fatalf("result at exact budget rejected: %+v", atBudget)
	}

	overBudget, err := socialCompactListToolResult("timeline_read", "bounded timeline", payload, measurement.JSONRPCEnvelopeBytes-1)
	if err != nil {
		t.Fatalf("build result over budget: %v", err)
	}
	if !overBudget.IsError {
		t.Fatalf("expected response_too_large when substantive dual-surface result exceeds max_output_bytes")
	}
}

func TestCompactTimelineTextHonorsPreviewChars(t *testing.T) {
	const fullContent = "abcdefghijklmno"
	result, err := socialCompactTimelineResult("home", "", 1, []any{
		map[string]any{
			"id":      "post-1",
			"content": fullContent,
			"account": map[string]any{"id": "account-1"},
		},
	}, sharedReadParams{
		View:           readViewCompact,
		PreviewChars:   8,
		MaxOutputBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("compact timeline result: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	text := toolResultTextJSON(t, result)
	textPayload, _ := text["payload"].(string)
	if !strings.Contains(textPayload, "abcdefg…") {
		t.Fatalf("preview_chars was not preserved in text payload: %q", textPayload)
	}
	if strings.Contains(result.Content[0].Text, fullContent) {
		t.Fatalf("text data leaked content beyond preview_chars: %s", result.Content[0].Text)
	}
}

func TestStructuredFirstGuidanceNeverNamesStructuredContentAsSoleAccessPath(t *testing.T) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Data: map[string]any{"content": "readable"},
	})
	if err != nil {
		t.Fatalf("structured-first result: %v", err)
	}
	text := toolResultTextJSON(t, result)
	if text["access"] != "payload or structuredContent.data" {
		t.Fatalf("result guidance names only one surface: %#v", text)
	}

	diagnosticResult, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Data:               map[string]any{"content": "readable"},
		Diagnostics:        map[string]any{"responseBytes": 42},
		IncludeDiagnostics: true,
	})
	if err != nil {
		t.Fatalf("structured-first diagnostic result: %v", err)
	}
	diagnosticText := toolResultTextJSON(t, diagnosticResult)
	if diagnosticText["diagnosticsAccess"] != "diagnosticPayload or structuredContent.diagnostics" {
		t.Fatalf("diagnostic guidance names only one surface: %#v", diagnosticText)
	}

	soulExpansion := soulReadExpansionRef("agent-1", readViewStandard, soulReadInput{}, "public")
	if soulExpansion == nil ||
		soulExpansion.ResultPath != "structuredContent.data" ||
		soulExpansion.TextResultPath != "document" ||
		!strings.Contains(soulExpansion.ResultAccess, "content[0].text") ||
		!strings.Contains(soulExpansion.ResultAccess, " or structuredContent.data") {
		t.Fatalf("soul summary expansion must name text and structured access paths: %+v", soulExpansion)
	}
}

func toolResultTextJSON(t *testing.T, result *mcpruntime.ToolResult) map[string]any {
	t.Helper()
	if result == nil || len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("unexpected tool result content: %+v", result)
	}
	var text map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &text); err != nil {
		t.Fatalf("text content is not JSON: %v", err)
	}
	return text
}
