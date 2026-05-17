package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

type structuredFirstResultOptions struct {
	Summary            string
	Data               any
	Text               map[string]any
	Structured         map[string]any
	Diagnostics        map[string]any
	IncludeDiagnostics bool
}

func toolStructuredFirstResult(opts structuredFirstResultOptions) (*mcpruntime.ToolResult, error) {
	data := opts.Data
	if data == nil {
		data = map[string]any{}
	}

	structured := map[string]any{"data": data}
	for key, value := range opts.Structured {
		key = strings.TrimSpace(key)
		if key == "" || key == "data" || key == "diagnostics" {
			continue
		}
		structured[key] = value
	}
	if opts.IncludeDiagnostics && opts.Diagnostics != nil {
		structured["diagnostics"] = opts.Diagnostics
	}
	if _, err := json.Marshal(structured); err != nil {
		return nil, fmt.Errorf("marshal structured-first tool result: %w", err)
	}

	textPayload := map[string]any{}
	if summary := strings.TrimSpace(opts.Summary); summary != "" {
		textPayload["summary"] = summary
	} else {
		textPayload["summary"] = "Result available in structuredContent.data"
	}
	for key, value := range opts.Text {
		key = strings.TrimSpace(key)
		if key == "" || key == "data" || key == "diagnostics" {
			continue
		}
		textPayload[key] = value
	}
	textPayload["data"] = map[string]any{"location": "structuredContent.data"}
	if opts.IncludeDiagnostics && opts.Diagnostics != nil {
		textPayload["diagnostics"] = map[string]any{"location": "structuredContent.diagnostics"}
	}

	b, err := json.Marshal(textPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal structured-first tool text: %w", err)
	}
	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: string(b),
		}},
		StructuredContent: structured,
	}, nil
}
