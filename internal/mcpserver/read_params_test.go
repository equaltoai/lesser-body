package mcpserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseSharedReadParamsNormalizesOptInShape(t *testing.T) {
	params, err := parseSharedReadParams(json.RawMessage(`{
		"view":"Compact",
		"fields":["id","actor.name","id",""],
		"include":"replies, media , replies",
		"preview_chars":120,
		"max_output_bytes":8192,
		"include_diagnostics":true
	}`))
	if err != nil {
		t.Fatalf("parse shared read params: %v", err)
	}
	if params.View != readViewCompact {
		t.Fatalf("view = %q, want %q", params.View, readViewCompact)
	}
	if !reflect.DeepEqual(params.Fields, []string{"id", "actor.name"}) {
		t.Fatalf("fields = %#v", params.Fields)
	}
	if !reflect.DeepEqual(params.Include, []string{"replies", "media"}) {
		t.Fatalf("include = %#v", params.Include)
	}
	if params.PreviewChars != 120 || params.MaxOutputBytes != 8192 || !params.IncludeDiagnostics {
		t.Fatalf("unexpected numeric/diagnostic params: %+v", params)
	}
}

func TestParseSharedReadParamsRejectsInvalidShape(t *testing.T) {
	for name, raw := range map[string]string{
		"view":                `{"view":"dense"}`,
		"view_type":           `{"view":3}`,
		"fields":              `{"fields":["id",3]}`,
		"preview_chars":       `{"preview_chars":-1}`,
		"max_output_bytes":    `{"max_output_bytes":1.25}`,
		"include_diagnostics": `{"include_diagnostics":"yes"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSharedReadParams(json.RawMessage(raw)); err == nil {
				t.Fatalf("expected invalid %s to fail", name)
			}
		})
	}
}

func TestSharedReadParamSchemaPropertiesAreAdditiveFragments(t *testing.T) {
	props := sharedReadParamSchemaProperties(sharedReadParamSchemaOptions{
		Views:              []string{readViewStandard, readViewCompact, "invalid", readViewCompact},
		Fields:             true,
		Include:            true,
		PreviewChars:       true,
		MaxOutputBytes:     true,
		IncludeDiagnostics: true,
	})
	for _, name := range []string{"view", "fields", "include", "preview_chars", "max_output_bytes", "include_diagnostics"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("missing schema property %q in %#v", name, props)
		}
	}
	view, _ := props["view"].(map[string]any)
	enum, _ := view["enum"].([]any)
	if !reflect.DeepEqual(enum, []any{readViewStandard, readViewCompact}) {
		t.Fatalf("view enum = %#v", enum)
	}
}

func TestToolStructuredFirstResultKeepsDataOnBothSurfaces(t *testing.T) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: "2 compact posts",
		Data: map[string]any{
			"items": []any{map[string]any{"id": "post-1", "content": "full text"}},
		},
		Text: map[string]any{
			"items":       []any{map[string]any{"id": "post-1"}},
			"omitted":     []any{"items[].content"},
			"data":        "caller cannot override data locator",
			"diagnostics": "caller cannot emit diagnostics without opt-in",
		},
		Structured: map[string]any{
			"source": "test",
			"data":   "caller cannot override structured data",
		},
		Diagnostics: map[string]any{"responseBytes": 1234},
	})
	if err != nil {
		t.Fatalf("structured-first result: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("unexpected content blocks: %+v", result.Content)
	}
	var text map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &text); err != nil {
		t.Fatalf("text content should remain JSON for text-only clients: %v", err)
	}
	if text["summary"] != "2 compact posts" {
		t.Fatalf("summary = %#v", text["summary"])
	}
	locator, _ := text["data"].(map[string]any)
	if locator["location"] != "structuredContent.data" {
		t.Fatalf("data locator must preserve the structured path: %#v", locator)
	}
	if text["access"] != "payload or structuredContent.data" {
		t.Fatalf("text access guidance must name both result surfaces: %#v", text["access"])
	}
	textData, _ := text["payload"].(map[string]any)
	textItems, _ := textData["items"].([]any)
	textItem, _ := textItems[0].(map[string]any)
	if textItem["content"] != "full text" {
		t.Fatalf("text payload must retain substantive data, got %#v", textData)
	}
	if _, ok := text["diagnostics"]; ok {
		t.Fatalf("diagnostics must be opt-in in text payload: %#v", text)
	}
	if result.StructuredContent["source"] != "test" {
		t.Fatalf("structured source = %#v", result.StructuredContent["source"])
	}
	if _, ok := result.StructuredContent["diagnostics"]; ok {
		t.Fatalf("diagnostics must be opt-in in structured content: %#v", result.StructuredContent)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	items, _ := data["items"].([]any)
	item, _ := items[0].(map[string]any)
	if item["content"] != "full text" {
		t.Fatalf("structuredContent.data must retain full payload, got %#v", data)
	}
}

func TestToolStructuredFirstResultPreservesPreBoundedCompactProjection(t *testing.T) {
	const heavyPostSentinel = "FULL_HEAVY_POST_BODY_SHOULD_NOT_APPEAR_IN_COMPACT_TEXT"
	const heavyAccountSentinel = "FULL_HEAVY_ACCOUNT_NOTE_SHOULD_NOT_APPEAR_IN_COMPACT_TEXT"

	fullData := map[string]any{
		"view": "standard",
		"items": []any{map[string]any{
			"id":        "post-1",
			"postRef":   "post:post-1",
			"createdAt": "2026-05-17T15:00:00Z",
			"content":   heavyPostSentinel + " " + strings.Repeat("post body ", 500),
			"account": map[string]any{
				"id":         "acct-1",
				"accountRef": "account:acct-1",
				"note":       heavyAccountSentinel + " " + strings.Repeat("profile note ", 300),
			},
		}},
		"nextCursor": "cursor-post-1",
	}
	compactItem := map[string]any{
		"postRef":    "post:post-1",
		"accountRef": "account:acct-1",
		"createdAt":  "2026-05-17T15:00:00Z",
		"preview":    "short preview",
	}
	compactOmissions := []any{
		map[string]any{
			"path":   "items[].content",
			"reason": "heavy_text",
			"expand": map[string]any{
				"ref":      "post:post-1",
				"location": toolResultAccessPath("payload.items[0].content", "data.items[0].content"),
			},
		},
		map[string]any{
			"path":   "items[].account.note",
			"reason": "profile_bio",
			"expand": map[string]any{
				"ref":      "account:acct-1",
				"location": toolResultAccessPath("payload.items[0].account.note", "data.items[0].account.note"),
			},
		},
	}
	compactData := map[string]any{
		"view":       readViewCompact,
		"items":      []any{compactItem},
		"omitted":    compactOmissions,
		"nextCursor": "cursor-post-1",
	}
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: "1 compact post",
		Data:    compactData,
		Text: map[string]any{
			"view":    readViewCompact,
			"items":   []any{compactItem},
			"omitted": compactOmissions,
			"budget": map[string]any{
				"maxOutputBytes": 4096,
				"enforcement":    "test_gate_only",
				"truncation":     "none; gate fails instead of silently dropping fields",
			},
		},
	})
	if err != nil {
		t.Fatalf("structured-first compact result: %v", err)
	}

	textJSON := result.Content[0].Text
	for _, heavy := range []string{heavyPostSentinel, heavyAccountSentinel, "post body", "profile note"} {
		if strings.Contains(textJSON, heavy) {
			t.Fatalf("compact text leaked heavy field %q: %s", heavy, textJSON)
		}
	}

	var text map[string]any
	if err := json.Unmarshal([]byte(textJSON), &text); err != nil {
		t.Fatalf("unmarshal compact text: %v", err)
	}
	textData, _ := text["payload"].(map[string]any)
	items, _ := textData["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("compact text data should retain one item ref, got %#v", textData["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["postRef"] != "post:post-1" || item["accountRef"] != "account:acct-1" || item["preview"] != "short preview" {
		t.Fatalf("compact text data lost substantive refs/preview: %#v", item)
	}
	if _, ok := item["content"]; ok {
		t.Fatalf("compact text data must omit full content: %#v", item)
	}
	if _, ok := item["account"]; ok {
		t.Fatalf("compact text data must omit nested account payload: %#v", item)
	}

	omitted, _ := text["omitted"].([]any)
	if len(omitted) != 2 {
		t.Fatalf("expected two omitted-field records, got %#v", text["omitted"])
	}
	wantOmitted := map[string]string{
		"items[].content":      toolResultAccessPath("payload.items[0].content", "data.items[0].content"),
		"items[].account.note": toolResultAccessPath("payload.items[0].account.note", "data.items[0].account.note"),
	}
	for _, raw := range omitted {
		record, _ := raw.(map[string]any)
		path, _ := record["path"].(string)
		expand, _ := record["expand"].(map[string]any)
		location, _ := expand["location"].(string)
		if wantOmitted[path] != location {
			t.Fatalf("unexpected expansion metadata for omitted path %q: %#v", path, record)
		}
		delete(wantOmitted, path)
	}
	if len(wantOmitted) != 0 {
		t.Fatalf("missing omitted-field records: %#v", wantOmitted)
	}

	data, _ := result.StructuredContent["data"].(map[string]any)
	structuredItems, _ := data["items"].([]any)
	structuredItem, _ := structuredItems[0].(map[string]any)
	if structuredItem["preview"] != "short preview" {
		t.Fatalf("structuredContent.data lost compact preview: %#v", structuredItem)
	}
	if _, ok := structuredItem["content"]; ok {
		t.Fatalf("pre-bounded compact projection leaked full content: %#v", structuredItem)
	}

	compactMeasurement, err := measureToolResultPayload(result)
	if err != nil {
		t.Fatalf("measure compact result payload: %v", err)
	}
	standardResult, err := toolJSONResult(fullData, nil)
	if err != nil {
		t.Fatalf("standard tool result: %v", err)
	}
	standardMeasurement, err := measureToolResultPayload(standardResult)
	if err != nil {
		t.Fatalf("measure standard result payload: %v", err)
	}
	const compactTextBudgetBytes = 4096
	if compactMeasurement.ContentTextBytes > compactTextBudgetBytes {
		t.Fatalf("compact text bytes %d exceed explicit budget %d", compactMeasurement.ContentTextBytes, compactTextBudgetBytes)
	}
	if compactMeasurement.JSONRPCEnvelopeBytes >= standardMeasurement.JSONRPCEnvelopeBytes {
		t.Fatalf("bounded compact envelope must be smaller than standard response: compact=%d standard=%d",
			compactMeasurement.JSONRPCEnvelopeBytes,
			standardMeasurement.JSONRPCEnvelopeBytes,
		)
	}
}

func TestToolStructuredFirstResultCanOptInDiagnostics(t *testing.T) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Data:               map[string]any{"ok": true},
		Diagnostics:        map[string]any{"responseBytes": 42},
		IncludeDiagnostics: true,
	})
	if err != nil {
		t.Fatalf("structured-first result: %v", err)
	}
	if _, ok := result.StructuredContent["diagnostics"]; !ok {
		t.Fatalf("expected opt-in diagnostics in structured content: %#v", result.StructuredContent)
	}
	var text map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &text); err != nil {
		t.Fatalf("unmarshal text: %v", err)
	}
	diagnosticsLocator, _ := text["diagnostics"].(map[string]any)
	if diagnosticsLocator["location"] != "structuredContent.diagnostics" {
		t.Fatalf("diagnostics locator must preserve the structured path: %#v", diagnosticsLocator)
	}
	if text["diagnosticsAccess"] != "diagnosticPayload or structuredContent.diagnostics" {
		t.Fatalf("diagnostics guidance must name both result surfaces: %#v", text["diagnosticsAccess"])
	}
	textDiagnostics, _ := text["diagnosticPayload"].(map[string]any)
	if textDiagnostics["responseBytes"] != float64(42) {
		t.Fatalf("diagnostics payload must be available to text-only clients: %#v", textDiagnostics)
	}
}

func TestToolJSONResultCompatibilityRemainsWrappedData(t *testing.T) {
	payload := map[string]any{"hello": "world"}
	result, err := toolJSONResult(payload, nil)
	if err != nil {
		t.Fatalf("toolJSONResult: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, `"hello":"world"`) {
		t.Fatalf("content text changed: %s", result.Content[0].Text)
	}
	data, _ := result.StructuredContent["data"].(map[string]any)
	if data["hello"] != "world" {
		t.Fatalf("structuredContent.data changed: %#v", result.StructuredContent)
	}
}
