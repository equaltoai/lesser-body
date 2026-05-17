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

func TestToolStructuredFirstResultKeepsFullDataStructured(t *testing.T) {
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
	dataLocator, _ := text["data"].(map[string]any)
	if dataLocator["location"] != "structuredContent.data" {
		t.Fatalf("data locator = %#v", dataLocator)
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
	diagLocator, _ := text["diagnostics"].(map[string]any)
	if diagLocator["location"] != "structuredContent.diagnostics" {
		t.Fatalf("diagnostics locator = %#v", diagLocator)
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
