package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	"github.com/equaltoai/lesser-body/internal/memory"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

// Strict MCP clients validate tools/call structuredContent against the tool's
// declared OutputSchema. Keep the test validator deliberately scoped to the
// JSON Schema vocabulary used by Ka's output schemas: anyOf, type, required,
// properties, additionalProperties, items, enum, and descriptive annotations.
func assertKaResultMatchesDeclaredOutputSchema(t *testing.T, label string, schema json.RawMessage, result *mcpruntime.ToolResult) {
	t.Helper()
	if result == nil {
		t.Fatalf("%s: handler returned a nil result", label)
	}
	if len(schema) == 0 {
		t.Fatalf("%s: registered tool has no declared output schema", label)
	}

	var declared any
	if err := json.Unmarshal(schema, &declared); err != nil {
		t.Fatalf("%s: declared output schema is invalid JSON: %v", label, err)
	}
	var unsupported []string
	validateKaOutputSchemaVocabulary("outputSchema", declared, &unsupported)
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		t.Fatalf("%s: declared output schema uses vocabulary the conformance guard does not validate:\n  %s",
			label, strings.Join(unsupported, "\n  "))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("%s: structuredContent is not JSON-encodable: %v", label, err)
	}
	var produced any
	if err := json.Unmarshal(encoded, &produced); err != nil {
		t.Fatalf("%s: structuredContent did not round-trip through JSON: %v", label, err)
	}

	var violations []string
	validateKaOutputSchema("structuredContent", declared, produced, &violations)
	if len(violations) == 0 {
		return
	}
	sort.Strings(violations)
	t.Fatalf("%s: structuredContent does not match the declared output schema:\n  %s\nproduced: %s",
		label, strings.Join(violations, "\n  "), string(encoded))
}

func validateKaOutputSchemaVocabulary(path string, schema any, unsupported *[]string) {
	node, ok := schema.(map[string]any)
	if !ok {
		*unsupported = append(*unsupported, fmt.Sprintf("%s: schema node is %T, want object", path, schema))
		return
	}

	for keyword, value := range node {
		switch keyword {
		case "anyOf":
			branches, ok := value.([]any)
			if !ok {
				*unsupported = append(*unsupported, fmt.Sprintf("%s.anyOf: %T is not supported", path, value))
				continue
			}
			for i, child := range branches {
				validateKaOutputSchemaVocabulary(fmt.Sprintf("%s.anyOf[%d]", path, i), child, unsupported)
			}
		case "type":
			if _, ok := value.(string); !ok {
				*unsupported = append(*unsupported, fmt.Sprintf("%s.type: %T is not supported", path, value))
			}
		case "required":
			if _, ok := value.([]any); !ok {
				*unsupported = append(*unsupported, fmt.Sprintf("%s.required: %T is not supported", path, value))
			}
		case "properties":
			properties, ok := value.(map[string]any)
			if !ok {
				*unsupported = append(*unsupported, fmt.Sprintf("%s.properties: %T is not supported", path, value))
				continue
			}
			for name, child := range properties {
				validateKaOutputSchemaVocabulary(path+".properties."+name, child, unsupported)
			}
		case "additionalProperties":
			if child, ok := value.(map[string]any); ok {
				validateKaOutputSchemaVocabulary(path+".additionalProperties", child, unsupported)
				continue
			}
			if _, ok := value.(bool); !ok {
				*unsupported = append(*unsupported, fmt.Sprintf("%s.additionalProperties: %T is not supported", path, value))
			}
		case "items":
			validateKaOutputSchemaVocabulary(path+".items", value, unsupported)
		case "enum":
			if _, ok := value.([]any); !ok {
				*unsupported = append(*unsupported, fmt.Sprintf("%s.enum: %T is not supported", path, value))
			}
		case "description":
			if _, ok := value.(string); !ok {
				*unsupported = append(*unsupported, fmt.Sprintf("%s.description: %T is not supported", path, value))
			}
		default:
			*unsupported = append(*unsupported, fmt.Sprintf("%s.%s: unsupported JSON Schema keyword", path, keyword))
		}
	}
}

func validateKaOutputSchema(path string, schema any, value any, violations *[]string) {
	node, ok := schema.(map[string]any)
	if !ok {
		return
	}

	if branches, ok := node["anyOf"].([]any); ok {
		matches := 0
		branchFailures := make([]string, 0, len(branches))
		for i, branch := range branches {
			var branchViolations []string
			validateKaOutputSchema(path, branch, value, &branchViolations)
			if len(branchViolations) == 0 {
				matches++
				continue
			}
			branchFailures = append(branchFailures, fmt.Sprintf("branch %d: %s", i, strings.Join(branchViolations, "; ")))
		}
		if matches == 0 {
			*violations = append(*violations, fmt.Sprintf("%s: matched no anyOf branches (%s)", path, strings.Join(branchFailures, " | ")))
		}
		return
	}

	if allowed, ok := node["enum"].([]any); ok {
		matched := false
		for _, candidate := range allowed {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			*violations = append(*violations, fmt.Sprintf("%s: value %#v is outside the declared enum %v", path, value, allowed))
		}
	}

	if want, ok := node["type"].(string); ok && !kaJSONTypeMatches(want, value) {
		*violations = append(*violations, fmt.Sprintf("%s: value has JSON type %s, want %s", path, kaJSONType(value), want))
		return
	}

	switch typed := value.(type) {
	case map[string]any:
		properties, _ := node["properties"].(map[string]any)
		if required, ok := node["required"].([]any); ok {
			for _, raw := range required {
				key, ok := raw.(string)
				if !ok {
					*violations = append(*violations, fmt.Sprintf("%s: declared required member %#v is not a string", path, raw))
					continue
				}
				if _, present := typed[key]; !present {
					*violations = append(*violations, fmt.Sprintf("%s.%s: missing required property", path, key))
				}
			}
		}
		for key, child := range typed {
			if childSchema, present := properties[key]; present {
				validateKaOutputSchema(path+"."+key, childSchema, child, violations)
				continue
			}
			switch additional := node["additionalProperties"].(type) {
			case bool:
				if !additional {
					*violations = append(*violations, fmt.Sprintf("%s.%s: undeclared key under additionalProperties:false", path, key))
				}
			case map[string]any:
				validateKaOutputSchema(path+"."+key, additional, child, violations)
			}
		}
	case []any:
		items, ok := node["items"]
		if !ok {
			return
		}
		for i, child := range typed {
			validateKaOutputSchema(fmt.Sprintf("%s[%d]", path, i), items, child, violations)
		}
	}
}

func kaJSONTypeMatches(want string, value any) bool {
	switch want {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
	case "null":
		return value == nil
	default:
		return false
	}
}

func kaJSONType(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		if math.Trunc(typed) == typed {
			return "integer"
		}
		return "number"
	default:
		return fmt.Sprintf("%T", value)
	}
}

type kaOutputSchemaFixture struct {
	name  string
	build func(*testing.T) *mcpruntime.ToolResult
}

// Every registered Ka tool is accounted for here. Tools that do not publish an
// output schema are pinned separately because there is no client-side schema
// contract to validate. Every schema-bearing tool must have one or more
// representative success results, and adding a tool or adding/removing its
// output schema fails this guard until its coverage is explicit.
func TestEveryRegisteredKaToolResultMatchesDeclaredOutputSchema(t *testing.T) {
	fixtures := kaOutputSchemaFixtures()
	withoutOutputSchema := map[string]struct{}{
		"describe_interface": {},
		"echo":               {},
		"profile_read":       {},
		"timeline_read":      {},
		"post_search":        {},
		"followers_list":     {},
		"following_list":     {},
		"conversations_read": {},
		"notifications_read": {},
	}

	registered := map[string]struct{}{}
	for _, def := range registeredToolDefsForTest(t) {
		registered[def.Name] = struct{}{}
		cases, hasFixtures := fixtures[def.Name]
		_, intentionallyUnschematized := withoutOutputSchema[def.Name]

		if len(def.OutputSchema) == 0 {
			if !intentionallyUnschematized {
				t.Errorf("%s: registered tool has no output schema and is not pinned in withoutOutputSchema", def.Name)
			}
			if hasFixtures {
				t.Errorf("%s: has conformance fixtures but no declared output schema", def.Name)
			}
			continue
		}
		if intentionallyUnschematized {
			t.Errorf("%s: now declares an output schema but remains pinned in withoutOutputSchema", def.Name)
		}
		if !hasFixtures || len(cases) == 0 {
			t.Errorf("%s: registered schema-bearing tool has no representative result fixture", def.Name)
			continue
		}
		for _, fixture := range cases {
			fixture := fixture
			t.Run(def.Name+"/"+fixture.name, func(t *testing.T) {
				if fixture.build == nil {
					t.Fatal("fixture has no result builder")
				}
				result := fixture.build(t)
				if result == nil {
					t.Fatal("fixture returned a nil result")
				}
				if result.IsError {
					t.Fatalf("representative success fixture returned a tool error: %#v", result.StructuredContent)
				}
				assertKaResultMatchesDeclaredOutputSchema(t, def.Name+"/"+fixture.name, def.OutputSchema, result)
			})
		}
	}

	for name := range fixtures {
		if _, ok := registered[name]; !ok {
			t.Errorf("%s: output-schema fixtures are stale; tool is not registered", name)
		}
	}
	for name := range withoutOutputSchema {
		if _, ok := registered[name]; !ok {
			t.Errorf("%s: withoutOutputSchema entry is stale; tool is not registered", name)
		}
	}
}

// Error results are part of a schema-bearing tool's structured-content
// contract too. Validate the shared Body error envelope against every declared
// output schema so a strict MCP client cannot replace the underlying tool error
// with its own -32602 schema-validation failure.
func TestEverySchemaBearingKaToolErrorMatchesDeclaredOutputSchema(t *testing.T) {
	result, err := toolErrorResult("fixture_error", "representative tool failure", http.StatusBadRequest, map[string]any{
		"source": "conformance_fixture",
	})
	if err != nil {
		t.Fatalf("toolErrorResult: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("toolErrorResult returned %#v, want an MCP error result", result)
	}

	for _, def := range registeredToolDefsForTest(t) {
		if len(def.OutputSchema) == 0 {
			continue
		}
		def := def
		t.Run(def.Name, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal(def.OutputSchema, &schema); err != nil {
				t.Fatalf("outputSchema is invalid JSON: %v", err)
			}
			if got := schema["type"]; got != "object" {
				t.Fatalf("outputSchema.type = %#v, want object for MCP 2025-11-25 compatibility", got)
			}
			assertKaResultMatchesDeclaredOutputSchema(t, def.Name+"/error", def.OutputSchema, result)
		})
	}
}

func kaOutputSchemaFixtures() map[string][]kaOutputSchemaFixture {
	fixtures := map[string][]kaOutputSchemaFixture{}
	add := func(tool string, name string, build func(*testing.T) *mcpruntime.ToolResult) {
		fixtures[tool] = append(fixtures[tool], kaOutputSchemaFixture{name: name, build: build})
	}

	for _, tool := range []string{
		"notification_dismiss",
		"post_create",
		"post_boost",
		"post_favorite",
		"follow",
		"unfollow",
		"profile_update",
	} {
		add(tool, "success", func(t *testing.T) *mcpruntime.ToolResult {
			return mustKaToolResult(toolJSONResult(map[string]any{"id": "fixture-id"}, nil))
		})
	}
	add("account_resolve", "account", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"selector":   "alice@example.com",
			"source":     "lesser-api",
			"accountRef": map[string]any{"id": "account-1", "acct": "alice@example.com"},
			"follow":     map[string]any{"tool": "follow", "arguments": map[string]any{"account_id": "account-1"}},
			"unfollow":   map[string]any{"tool": "unfollow", "arguments": map[string]any{"account_id": "account-1"}},
		}, nil))
	})

	add("post_get", "standard", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"id":     "post-1",
			"view":   readViewStandard,
			"source": "lesser-api",
			"status": map[string]any{"id": "post-1"},
		}, nil))
	})
	add("notification_get", "standard", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"id":              "notification-1",
			"view":            readViewStandard,
			"source":          "lesser-api",
			"notification":    map[string]any{"id": "notification-1"},
			"notificationRef": map[string]any{"id": "notification-1"},
		}, nil))
	})
	add("conversation_get", "standard", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(socialConversationGetStructuredResult(map[string]any{
			"id":           "conversation-1",
			"view":         readViewStandard,
			"source":       "lesser-api",
			"conversation": map[string]any{"id": "conversation-1"},
			"limit":        20,
		}, sharedReadParams{View: readViewStandard}))
	})
	add("conversation_get", "compact", func(t *testing.T) *mcpruntime.ToolResult {
		conversation := map[string]any{"id": "conversation-1", "messages": []any{}}
		return mustKaToolResult(socialCompactConversationGetResult(map[string]any{
			"id":           "conversation-1",
			"source":       "lesser-api",
			"conversation": conversation,
			"limit":        20,
		}, conversation, sharedReadParams{View: readViewCompact}))
	})
	add("direct_messages_read", "standard", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(socialDirectMessagesReadStructuredResult(map[string]any{
			"counterpart":  "counterpart",
			"id":           "conversation-1",
			"view":         readViewStandard,
			"source":       "lesser-api",
			"conversation": map[string]any{"id": "conversation-1"},
			"messages":     []any{},
			"count":        0,
			"limit":        20,
			"unreadOnly":   false,
		}, sharedReadParams{View: readViewStandard}))
	})
	add("direct_messages_read", "compact", func(t *testing.T) *mcpruntime.ToolResult {
		conversation := map[string]any{"id": "conversation-1", "messages": []any{}}
		return mustKaToolResult(socialCompactDirectMessagesReadResult(map[string]any{
			"counterpart":  "counterpart",
			"id":           "conversation-1",
			"source":       "lesser-api",
			"conversation": conversation,
			"limit":        20,
			"unreadOnly":   false,
		}, conversation, sharedReadParams{View: readViewCompact}))
	})

	add("message_requests_list", "requests", func(t *testing.T) *mcpruntime.ToolResult {
		return callMessageRequestHandlerForSchema(t, "BodyMessageRequests",
			`{"data":{"conversations":[{"id":"conversation-1","unread":true,"accounts":[],"viewerMetadata":{"requestState":"PENDING"}}]}}`,
			handleMessageRequestsList, `{}`)
	})
	add("message_request_accept", "accepted_enum_members", func(t *testing.T) *mcpruntime.ToolResult {
		return callMessageRequestHandlerForSchema(t, "BodyAcceptMessageRequest",
			`{"data":{"acceptMessageRequest":{"id":"conversation-1","unread":true,"accounts":[],"viewerMetadata":{"requestState":"ACCEPTED"}}}}`,
			handleMessageRequestAccept, `{"conversationId":"conversation-1"}`)
	})
	add("message_request_decline", "declined_enum_members", func(t *testing.T) *mcpruntime.ToolResult {
		return callMessageRequestHandlerForSchema(t, "BodyDeclineMessageRequest",
			`{"data":{"declineMessageRequest":true}}`,
			handleMessageRequestDecline, `{"conversationId":"conversation-1"}`)
	})

	draftFixture := func() *cmsapi.Draft {
		title := "Draft title"
		slug := "draft-title"
		return &cmsapi.Draft{
			ID:            "draft-1",
			Title:         &title,
			Slug:          &slug,
			ContentType:   cmsapi.ObjectTypeArticle,
			Content:       "Draft body",
			ContentFormat: cmsapi.ContentFormatMarkdown,
			Status:        cmsapi.DraftStatusDraft,
		}
	}
	for tool, operation := range map[string]string{
		"article_draft_create": "created",
		"article_draft_update": "updated",
		"article_draft_get":    "read",
	} {
		tool, operation := tool, operation
		add(tool, "compact", func(t *testing.T) *mcpruntime.ToolResult {
			return mustKaToolResult(articleDraftSingleResult(tool, operation, draftFixture(), articleDraftViewParams{
				View:         readViewCompact,
				PreviewRunes: articleDraftPreviewRunes,
			}, nil))
		})
		add(tool, "standard", func(t *testing.T) *mcpruntime.ToolResult {
			return mustKaToolResult(articleDraftSingleResult(tool, operation, draftFixture(), articleDraftViewParams{
				View:         readViewStandard,
				PreviewRunes: articleDraftPreviewRunes,
			}, nil))
		})
	}
	add("article_draft_list", "compact", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(articleDraftListResult(&cmsapi.DraftConnection{}, 20, articleDraftViewParams{
			View:         readViewCompact,
			PreviewRunes: articleDraftPreviewRunes,
		}))
	})
	add("article_draft_list", "standard", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(articleDraftListResult(&cmsapi.DraftConnection{}, 20, articleDraftViewParams{
			View:         readViewStandard,
			PreviewRunes: articleDraftPreviewRunes,
		}))
	})
	reviewFixture := func() *cmsapi.DraftReview {
		return &cmsapi.DraftReview{
			DraftID:       "draft-1",
			ContentFormat: cmsapi.ContentFormatMarkdown,
			Status:        cmsapi.DraftStatusDraft,
			UpdatedAt:     "2026-07-31T12:00:00Z",
			CreatedAt:     "2026-07-31T11:00:00Z",
			Verdicts:      []cmsapi.DraftReviewVerdictRecord{},
		}
	}
	for tool, operation := range map[string]string{
		"article_draft_review_submit":  "submitted",
		"article_draft_review_verdict": "verdict_submitted",
	} {
		tool, operation := tool, operation
		add(tool, "review", func(t *testing.T) *mcpruntime.ToolResult {
			return mustKaToolResult(articleDraftReviewSingleResult(tool, operation, reviewFixture(), articleDraftReviewBudgetBytes))
		})
	}
	add("article_draft_review_read", "state", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(articleDraftReviewStateResult(reviewFixture(), articleDraftReviewBudgetBytes))
	})
	add("article_draft_review_read", "queue", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(articleDraftReviewQueueResult(&cmsapi.DraftReviewConnection{Edges: []cmsapi.DraftReviewEdge{}}, 20, articleDraftReviewBudgetBytes))
	})
	for _, fixture := range articleDraftPreviewOutputSchemaFixtures() {
		fixture := fixture
		add("article_draft_preview", fixture.name, fixture.build)
	}

	articleFixture := func() *cmsapi.Article {
		return &cmsapi.Article{
			ID:            "https://example.com/articles/article-1",
			Slug:          "article-1",
			Title:         "Article title",
			Content:       "Article body",
			ContentFormat: cmsapi.ContentFormatMarkdown,
		}
	}
	for tool, operation := range map[string]string{
		"article_draft_publish": "published",
		"article_update":        "updated",
		"article_get":           "read",
	} {
		tool, operation := tool, operation
		add(tool, "compact", func(t *testing.T) *mcpruntime.ToolResult {
			return mustKaToolResult(articleSingleResult(tool, operation, articleFixture(), articleDraftViewParams{
				View:         readViewCompact,
				PreviewRunes: articleDraftPreviewRunes,
			}, nil))
		})
		add(tool, "standard", func(t *testing.T) *mcpruntime.ToolResult {
			return mustKaToolResult(articleSingleResult(tool, operation, articleFixture(), articleDraftViewParams{
				View:         readViewStandard,
				PreviewRunes: articleDraftPreviewRunes,
			}, nil))
		})
	}
	add("article_list", "compact", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(articleListResult(&cmsapi.ArticleConnection{}, 20, "actor-1", articleDraftViewParams{
			View:         readViewCompact,
			PreviewRunes: articleDraftPreviewRunes,
		}))
	})
	add("article_list", "standard", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(articleListResult(&cmsapi.ArticleConnection{}, 20, "actor-1", articleDraftViewParams{
			View:         readViewStandard,
			PreviewRunes: articleDraftPreviewRunes,
		}))
	})

	add("memory_append", "created", func(t *testing.T) *mcpruntime.ToolResult {
		payload := &memory.AppendResult{
			Event: memory.Event{
				EventID:    "01J00000000000000000000000",
				OccurredAt: time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
				Content:    "remembered",
				Tags:       []string{"schema"},
			},
			Created: true,
		}
		structured, err := toolStructuredContent(payload)
		if err != nil {
			t.Fatalf("toolStructuredContent: %v", err)
		}
		return mustKaToolResult(toolJSONResult(payload, structured))
	})
	add("memory_query", "events", func(t *testing.T) *mcpruntime.ToolResult {
		payload := &memory.QueryResult{
			Events: []memory.Event{{
				EventID:    "01J00000000000000000000000",
				OccurredAt: time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
				Content:    "remembered",
			}},
			NextCursor: "next",
		}
		structured, err := toolStructuredContent(payload)
		if err != nil {
			t.Fatalf("toolStructuredContent: %v", err)
		}
		return mustKaToolResult(toolJSONResult(payload, structured))
	})

	add("skills_catalog", "catalog", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"authority":  map[string]any{"source": "lesser"},
			"items":      []any{map[string]any{"skill_id": "skill-1"}},
			"bundles":    []any{map[string]any{"bundle_id": "bundle-1"}},
			"nextCursor": "next",
		}, nil))
	})
	add("skill_bundle_get", "bundle", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"authority":    map[string]any{"source": "lesser"},
			"bundle":       map[string]any{"bundle_id": "bundle-1"},
			"content":      map[string]any{"mode": "metadata"},
			"verification": map[string]any{"state": skillInstallStateNotInstalled},
		}, nil))
	})

	for _, tool := range []string{"email_send", "email_reply", "sms_send"} {
		add(tool, "queued", func(t *testing.T) *mcpruntime.ToolResult {
			payload := normalizeCommSendResult(map[string]any{
				"messageRef": "message-1",
				"status":     "queued",
				"channel":    "fixture",
			}, nil)
			payload["idempotencyKey"] = "idempotency-1"
			return mustKaToolResult(toolJSONResult(payload, nil))
		})
	}

	mailboxRaw := func() map[string]any {
		return map[string]any{
			"messages": []any{map[string]any{
				"messageRef":  "message-1",
				"channelType": "email",
				"direction":   "inbound",
				"status":      "received",
				"preview":     "bounded preview",
				"state":       map[string]any{"read": false},
				"content":     map[string]any{"available": true},
				"createdAt":   "2026-07-28T12:00:00Z",
			}},
			"count":      1,
			"hasMore":    false,
			"nextCursor": "next",
		}
	}
	mailboxStandardResult := func(t *testing.T) *mcpruntime.ToolResult {
		payload := normalizeMailboxListResult(mailboxRaw(), false)
		return mustKaToolResult(toolJSONResult(payload, payload))
	}
	for _, tool := range []string{"email_read", "email_search", "sms_read", "voicemail_read"} {
		add(tool, "standard", mailboxStandardResult)
	}
	add("email_read", "compact", func(t *testing.T) *mcpruntime.ToolResult {
		payload := mailboxCompactListResult(normalizeMailboxListResult(mailboxRaw(), false))
		return mustKaToolResult(compactMailboxListToolResult("email_read", payload))
	})
	add("email_get", "message", func(t *testing.T) *mcpruntime.ToolResult {
		payload := map[string]any{
			"message": normalizeMailboxMessage(mailboxRaw()["messages"].([]any)[0], false),
			"source":  "lesser-host-mailbox",
		}
		return mustKaToolResult(toolJSONResult(payload, payload))
	})
	add("email_get_content", "content", func(t *testing.T) *mcpruntime.ToolResult {
		payload := normalizeMailboxContentResult(map[string]any{
			"messageRef":  "message-1",
			"contentType": "text/plain",
			"bytes":       12,
			"body":        "full content",
		})
		return mustKaToolResult(toolJSONResult(payload, payload))
	})
	for _, tool := range []string{"email_delete", "email_mark_read", "email_mark_unread"} {
		tool := tool
		add(tool, "mutation", func(t *testing.T) *mcpruntime.ToolResult {
			action := "read"
			if tool == "email_delete" {
				action = "archive"
			} else if tool == "email_mark_unread" {
				action = "unread"
			}
			message := normalizeMailboxMessage(mailboxRaw()["messages"].([]any)[0], false)
			payload := map[string]any{
				"messageId":  "message-1",
				"messageRef": "message-1",
				"action":     action,
				"message":    message,
				"state":      message["state"],
				"source":     "lesser-host-mailbox",
			}
			return mustKaToolResult(toolJSONResult(payload, payload))
		})
	}

	add("identity_whoami", "identity", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"agentId":            "agent-1",
			"domain":             "example.com",
			"localId":            "actor",
			"status":             "active",
			"channels":           map[string]any{},
			"contactPreferences": map[string]any{},
			"provisioning": map[string]any{
				"channels":           map[string]any{"state": "empty", "present": true, "configuredCount": 0},
				"contactPreferences": map[string]any{"state": "empty", "present": true, "configuredCount": 0},
				"communications":     "unprovisioned",
			},
		}, nil))
	})
	add("soul_read", "souls", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(soulReadToolResult(map[string]any{
			"query":  "agent-1",
			"count":  1,
			"access": map[string]any{"mode": "public"},
			"souls":  []any{map[string]any{"agentId": "agent-1"}},
		}, soulReadPrivateRequest{}))
	})
	add("identity_lookup", "matches", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"query":   "agent-1",
			"matches": []any{map[string]any{"agentId": "agent-1"}},
			"count":   1,
		}, nil))
	})
	add("identity_verify", "verified", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolJSONResult(map[string]any{
			"channel":           "ens",
			"identifier":        "agent.eth",
			"verificationScope": "identifier",
			"identityResolved":  true,
			"verified":          true,
			"agent":             map[string]any{"agentId": "agent-1"},
		}, nil))
	})
	add("soul_self_recover", "recovered", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(toolStructuredFirstResult(structuredFirstResultOptions{
			Summary: "Bound soul recovered into Ptah",
			Data:    map[string]any{"status": "recovered", "classification": "published_artifact_verified"},
		}))
	})

	// Editorial media tools: representative success results built through the
	// same result builders the handlers use.
	mediaGrant := func() *cmsapi.UploadGrant {
		url := "https://presign.example.com/put/media-1.png"
		mediaID := "media-1"
		return &cmsapi.UploadGrant{
			ID: "grant-1", OwnerID: "alice", ContentType: "image/png", MaxSizeBytes: 5 * 1024 * 1024,
			DeclaredSHA256: strings.Repeat("a", 64), Status: cmsapi.UploadGrantStatusMinted,
			PresignedURL: &url, MediaID: &mediaID,
			GrantedAt: "2026-08-24T12:00:00Z", ExpiresAt: "2026-08-24T12:15:00Z",
		}
	}
	add("upload_grant_mint", "minted", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(mediaGrantMintResult(mediaGrant(), mediaDefaultBudgetBytes))
	})
	add("upload_finalize", "finalized", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(mediaFinalizeResult(&cmsapi.UploadGrantFinalizeResult{
			Grant: &cmsapi.UploadGrant{
				ID: "grant-1", OwnerID: "alice", ContentType: "image/png", MaxSizeBytes: 5 * 1024 * 1024,
				DeclaredSHA256: strings.Repeat("a", 64), Status: cmsapi.UploadGrantStatusUsed,
				MediaID: stringPtr("media-1"), GrantedAt: "2026-08-24T12:00:00Z", ExpiresAt: "2026-08-24T12:15:00Z",
				UsedAt: stringPtr("2026-08-24T12:05:00Z"),
			},
			Media: &cmsapi.UploadGrantMedia{
				MediaID: "media-1", ContentType: "image/png", Size: 1024,
				ContentHash: "sha256:" + strings.Repeat("a", 64), Status: "ready", Visibility: "internal",
			},
		}, mediaDefaultBudgetBytes))
	})
	add("media_state", "upload_grant", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(mediaGrantStateResult(mediaGrant(), mediaDefaultBudgetBytes))
	})
	add("media_state", "draft_binding", func(t *testing.T) *mcpruntime.ToolResult {
		usage := mediaFixtureUsage()
		state := &cmsapi.DraftMediaState{
			DraftID: "draft-1", ContentHash: "sha256:" + strings.Repeat("b", 64), Revision: 3,
			EditorialMedia:     []cmsapi.EditorialMediaUsage{*usage},
			PublishEligibility: cmsapi.DraftPublishEligibility{Eligible: true},
		}
		return mustKaToolResult(mediaBindingStateResult(state, usage.MediaID, mediaDefaultBudgetBytes))
	})
	add("media_read", "access", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(mediaReadResult(&cmsapi.EditorialMediaAccess{
			MediaID: "media-1", URL: "https://media.example.com/exact-asset.png?signature=review",
			ExpiresAt: "2026-08-24T12:30:00Z", ContentHash: "sha256:" + strings.Repeat("a", 64),
		}, mediaDefaultBudgetBytes))
	})
	add("draft_media_attach", "attached", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(mediaBindingsResult("draft_media_attach", "attached", mediaFixtureDraftState(), mediaDefaultBudgetBytes))
	})
	add("draft_media_detach", "detached", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(mediaBindingsResult("draft_media_detach", "detached", mediaFixtureDraftState(), mediaDefaultBudgetBytes))
	})
	add("draft_media_reorder", "reordered", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(mediaBindingsResult("draft_media_reorder", "reordered", mediaFixtureDraftState(), mediaDefaultBudgetBytes))
	})
	add("promo_compose", "composed", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(promoComposeResult(promoFixturePackage(cmsapi.PromoPackageStatusDraft, false, ""), promoDefaultBudgetBytes))
	})
	add("promo_review_share", "shared", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(promoReviewShareResult(&cmsapi.PromoPackageReview{
			PackageID:   "pkg-1",
			ContentHash: promoContentHash("a"),
		}, promoDefaultBudgetBytes))
	})
	add("promo_review_submit", "verdict_submitted", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(promoReviewSubmitResult(&cmsapi.PromoPackageReview{
			PackageID:   "pkg-1",
			ContentHash: promoContentHash("a"),
		}, promoDefaultBudgetBytes))
	})
	add("promo_state", "draft", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(promoStateResult(promoFixturePackage(cmsapi.PromoPackageStatusDraft, false, ""), promoDefaultBudgetBytes))
	})
	add("promo_state", "releasing", func(t *testing.T) *mcpruntime.ToolResult {
		// The releasing reservation injects PACKAGE_RELEASING into blockingReasons
		// even when the review projection omits it, so this fixture also exercises
		// the blockingReasons/guidance members of the state schema.
		pkg := promoFixturePackage(cmsapi.PromoPackageStatusReleasing, true, "")
		pkg.Review.ReleaseBlockingReasons = nil
		return mustKaToolResult(promoStateResult(pkg, promoDefaultBudgetBytes))
	})
	add("promo_state", "unknown_status", func(t *testing.T) *mcpruntime.ToolResult {
		// Lesser's status is transported verbatim and can be a value outside the
		// DRAFT/RELEASING/RELEASED enum (the envelope state mapping still fails
		// closed to "unknown"). The status property must stay enum-free so a
		// strict MCP client does not reject an unrecognized upstream status.
		return mustKaToolResult(promoStateResult(promoFixturePackage("SOMETHING_NEW", false, ""), promoDefaultBudgetBytes))
	})
	add("promo_release", "released", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(promoReleaseResult(&cmsapi.PromoPackageReleaseResult{
			Package:  promoFixturePackage(cmsapi.PromoPackageStatusReleased, true, "status-1"),
			StatusID: "status-1",
			URL:      strPtr("status-1"),
		}, promoDefaultBudgetBytes))
	})
	add("promo_read", "released", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(promoReadResult(promoFixturePackage(cmsapi.PromoPackageStatusReleased, true, "status-1"), promoDefaultBudgetBytes))
	})
	add("promo_read", "unknown_status", func(t *testing.T) *mcpruntime.ToolResult {
		return mustKaToolResult(promoReadResult(promoFixturePackage("SOMETHING_NEW", false, ""), promoDefaultBudgetBytes))
	})

	return fixtures
}

func mediaFixtureUsage() *cmsapi.EditorialMediaUsage {
	caption := "Launch artwork"
	credit := "Illustration by Alice"
	alt := "A rocket leaving a violet planet"
	contentHash := "sha256:" + strings.Repeat("a", 64)
	return &cmsapi.EditorialMediaUsage{
		MediaID: "media-1", Role: cmsapi.EditorialMediaRoleHero,
		Caption: &caption, CreditLine: &credit, AltText: &alt, EffectiveAltText: &alt,
		State: cmsapi.EditorialMediaStateReady, ContentHash: &contentHash,
		Provenance: &cmsapi.EditorialMediaProvenance{
			Origin: "ILLUSTRATED", ResponsibleActorID: "alice", SourceReferences: []string{},
			RecordedAt: "2026-08-24T12:00:00Z", ContentIntegrity: contentHash,
		},
	}
}

func mediaFixtureDraftState() *cmsapi.DraftMediaState {
	usage := mediaFixtureUsage()
	return &cmsapi.DraftMediaState{
		DraftID: "draft-1", ContentHash: "sha256:" + strings.Repeat("b", 64), Revision: 3,
		EditorialMedia:     []cmsapi.EditorialMediaUsage{*usage},
		PublishEligibility: cmsapi.DraftPublishEligibility{Eligible: true},
	}
}

func stringPtr(value string) *string {
	return &value
}

func mustKaToolResult(result *mcpruntime.ToolResult, err error) *mcpruntime.ToolResult {
	if err != nil {
		panic(fmt.Sprintf("build representative tool result: %v", err))
	}
	if result == nil {
		panic("build representative tool result: nil result")
	}
	return result
}

func callMessageRequestHandlerForSchema(
	t *testing.T,
	wantOperation string,
	response string,
	handler func(context.Context, json.RawMessage) (*mcpruntime.ToolResult, error),
	args string,
) *mcpruntime.ToolResult {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Fatalf("message-request handler request = %s %s, want POST /api/graphql", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("message-request handler Authorization = %q, want bearer passthrough", got)
		}
		var request struct {
			OperationName string `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode message-request GraphQL operation: %v", err)
		}
		if request.OperationName != wantOperation {
			t.Fatalf("message-request GraphQL operation = %q, want %q", request.OperationName, wantOperation)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(func() {
		server.Close()
		lesserapi.ResetForTests()
	})
	t.Setenv("LESSER_API_BASE_URL", server.URL)
	lesserapi.ResetForTests()

	result, err := handler(articleDraftTestContext(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s handler: %v", wantOperation, err)
	}
	if result == nil {
		t.Fatalf("%s handler returned a nil result", wantOperation)
	}
	return result
}

func articleDraftPreviewOutputSchemaFixtures() []kaOutputSchemaFixture {
	rendered := "<article><p>rendered preview</p></article>"
	cases := []struct {
		name    string
		view    string
		preview *cmsapi.DraftPreview
	}{
		{
			name: "compact_success",
			view: readViewCompact,
			preview: &cmsapi.DraftPreview{
				DraftID:       "draft-1",
				Success:       true,
				RenderedHTML:  &rendered,
				SourceFormat:  cmsapi.ContentFormatMarkdown,
				SourceBytes:   12,
				RenderedBytes: len(rendered),
			},
		},
		{
			name: "compact_render_failure",
			view: readViewCompact,
			preview: &cmsapi.DraftPreview{
				DraftID:      "draft-1",
				Success:      false,
				SourceFormat: cmsapi.ContentFormatMarkdown,
				SourceBytes:  12,
				Errors:       []string{"renderer rejected the draft"},
			},
		},
		{
			name: "standard_success",
			view: readViewStandard,
			preview: &cmsapi.DraftPreview{
				DraftID:       "draft-1",
				Success:       true,
				RenderedHTML:  &rendered,
				SourceFormat:  cmsapi.ContentFormatMarkdown,
				SourceBytes:   12,
				RenderedBytes: len(rendered),
			},
		},
	}
	fixtures := make([]kaOutputSchemaFixture, 0, len(cases))
	for _, tc := range cases {
		tc := tc
		fixtures = append(fixtures, kaOutputSchemaFixture{
			name: tc.name,
			build: func(t *testing.T) *mcpruntime.ToolResult {
				return mustKaToolResult(articleDraftPreviewResult(tc.preview, articleDraftViewParams{
					View:         tc.view,
					PreviewRunes: articleDraftPreviewRunes,
				}))
			},
		})
	}
	return fixtures
}

func TestArticleDraftPreviewResultMatchesDeclaredOutputSchema(t *testing.T) {
	t.Run("handler_compact_without_rendered_html", func(t *testing.T) {
		newArticleDraftGraphQLTestServer(t, cmsapi.Draft{
			ID:            "draft-1",
			AuthorID:      "alice",
			ContentType:   cmsapi.ObjectTypeArticle,
			Content:       "Draft body",
			ContentFormat: cmsapi.ContentFormatMarkdown,
			Status:        cmsapi.DraftStatusDraft,
		})
		result, err := handleArticleDraftPreview(
			articleDraftTestContext(),
			json.RawMessage(`{"id":"draft-1","view":"compact"}`),
		)
		if err != nil {
			t.Fatalf("article_draft_preview handler: %v", err)
		}
		assertKaResultMatchesDeclaredOutputSchema(
			t,
			"article_draft_preview/handler_compact_without_rendered_html",
			articleDraftPreviewDef().OutputSchema,
			result,
		)
	})

	for _, fixture := range articleDraftPreviewOutputSchemaFixtures() {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			assertKaResultMatchesDeclaredOutputSchema(t, fixture.name, articleDraftPreviewDef().OutputSchema, fixture.build(t))
		})
	}
}
