package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSoulReadToolResult_PrivateSingleCompactsTextContent(t *testing.T) {
	payload := soulReadPrivateSingleTestPayload("conv-1", "private-lab-fixture "+strings.Repeat("private ", 9000), map[string]any{"capabilities": []any{}})

	res, err := soulReadToolResult(payload, soulReadPrivateRequest{IncludeMintConversations: true, ConversationID: "conv-1"})
	if err != nil {
		t.Fatalf("soulReadToolResult: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected successful tool result, got %+v", res)
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected one content block, got %+v", res.Content)
	}
	if strings.Contains(res.Content[0].Text, "private-lab-fixture") {
		t.Fatalf("text content duplicated private single-read messages")
	}
	if !strings.Contains(res.Content[0].Text, "available_in_structured_content") {
		t.Fatalf("text content should point clients to structuredContent, got %s", res.Content[0].Text)
	}

	data, _ := res.StructuredContent["data"].(map[string]any)
	conversation := soulReadPrivateSingleConversationFromTestResult(t, data)
	messages, _ := conversation["messages"].(string)
	if !strings.Contains(messages, "private-lab-fixture") {
		t.Fatalf("structured content must preserve explicit private messages, got %+v", conversation)
	}
	if _, ok := conversation["producedDeclarations"]; !ok {
		t.Fatalf("structured content must preserve explicit produced declarations, got %+v", conversation)
	}
}

func TestSoulReadToolResult_PrivateSingleRejectsOversizeDelivery(t *testing.T) {
	t.Setenv("MCP_STREAM_MAX_EVENT_BYTES", "2048")

	const privateSentinel = "oversize-private-sentinel"
	payload := soulReadPrivateSingleTestPayload("conv-oversize", privateSentinel+strings.Repeat("x", 4096), nil)

	res, err := soulReadToolResult(payload, soulReadPrivateRequest{IncludeMintConversations: true, ConversationID: "conv-oversize"})
	if err != nil {
		t.Fatalf("soulReadToolResult: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected oversize tool error, got %+v", res)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), privateSentinel) {
		t.Fatalf("oversize tool error leaked private conversation content")
	}
	errPayload, _ := res.StructuredContent["error"].(map[string]any)
	if errPayload["code"] != "response_too_large" || errPayload["status"] != http.StatusRequestEntityTooLarge {
		t.Fatalf("unexpected oversize error payload: %+v", errPayload)
	}
	details, _ := errPayload["details"].(map[string]any)
	if details["source"] != "lesser_private_self_scope" || details["privateBlock"] != soulReadPrivateMintConversationsBlock || details["mode"] != "single" {
		t.Fatalf("unexpected oversize details: %+v", details)
	}
}

func soulReadPrivateSingleTestPayload(conversationID string, messages string, producedDeclarations any) map[string]any {
	conversation := map[string]any{
		"agentId":        "0x9999999999999999999999999999999999999999999999999999999999999999",
		"conversationId": conversationID,
		"model":          "gpt-test",
		"messages":       messages,
		"status":         "completed",
		"createdAt":      "2026-05-11T21:00:00Z",
	}
	if producedDeclarations != nil {
		conversation["producedDeclarations"] = producedDeclarations
	}
	return map[string]any{
		"query":  "self",
		"count":  1,
		"access": soulReadAccess("self", []any{soulReadPrivateMintConversationsBlock}),
		"souls": []any{
			map[string]any{
				"agentId": "0x9999999999999999999999999999999999999999999999999999999999999999",
				"private": map[string]any{
					soulReadPrivateMintConversationsBlock: map[string]any{
						"mode":         "single",
						"conversation": conversation,
					},
				},
			},
		},
	}
}

func soulReadPrivateSingleConversationFromTestResult(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	souls, _ := data["souls"].([]any)
	if len(souls) != 1 {
		t.Fatalf("expected one soul, got %+v", data)
	}
	soul, _ := souls[0].(map[string]any)
	privateBlocks, _ := soul["private"].(map[string]any)
	mint, _ := privateBlocks[soulReadPrivateMintConversationsBlock].(map[string]any)
	conversation, _ := mint["conversation"].(map[string]any)
	if conversation == nil {
		t.Fatalf("missing private single conversation: %+v", data)
	}
	return conversation
}
