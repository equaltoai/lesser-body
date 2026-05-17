package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	readViewCompact  = "compact"
	readViewStandard = "standard"
	readViewSummary  = "summary"
	readViewFull     = "full"
)

var supportedReadViews = map[string]struct{}{
	readViewCompact:  {},
	readViewStandard: {},
	readViewSummary:  {},
	readViewFull:     {},
}

type sharedReadParams struct {
	View               string   `json:"view,omitempty"`
	Fields             []string `json:"fields,omitempty"`
	Include            []string `json:"include,omitempty"`
	PreviewChars       int      `json:"preview_chars,omitempty"`
	MaxOutputBytes     int      `json:"max_output_bytes,omitempty"`
	IncludeDiagnostics bool     `json:"include_diagnostics,omitempty"`
}

type sharedReadParamSchemaOptions struct {
	Views              []string
	Fields             bool
	Include            bool
	PreviewChars       bool
	MaxOutputBytes     bool
	IncludeDiagnostics bool
}

func parseSharedReadParams(args json.RawMessage) (sharedReadParams, error) {
	var out sharedReadParams
	if len(strings.TrimSpace(string(args))) == 0 || strings.TrimSpace(string(args)) == "null" {
		return out, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(args, &raw); err != nil {
		return out, err
	}
	if raw == nil {
		return out, nil
	}

	out.View = strings.ToLower(strings.TrimSpace(stringFromAny(raw["view"])))
	if out.View != "" {
		if _, ok := supportedReadViews[out.View]; !ok {
			return out, fmt.Errorf("unsupported view %q", out.View)
		}
	}

	var err error
	out.Fields, err = stringListFromAny(raw["fields"], "fields")
	if err != nil {
		return out, err
	}
	out.Include, err = stringListFromAny(raw["include"], "include")
	if err != nil {
		return out, err
	}
	out.PreviewChars, err = nonNegativeIntFromAny(raw["preview_chars"], "preview_chars")
	if err != nil {
		return out, err
	}
	out.MaxOutputBytes, err = nonNegativeIntFromAny(raw["max_output_bytes"], "max_output_bytes")
	if err != nil {
		return out, err
	}
	if b, ok, err := optionalBoolFromAny(raw["include_diagnostics"], "include_diagnostics"); err != nil {
		return out, err
	} else if ok {
		out.IncludeDiagnostics = b
	}
	return out, nil
}

func sharedReadParamSchemaProperties(opts sharedReadParamSchemaOptions) map[string]any {
	props := map[string]any{}
	if len(opts.Views) > 0 {
		views := make([]any, 0, len(opts.Views))
		seen := map[string]struct{}{}
		for _, raw := range opts.Views {
			view := strings.ToLower(strings.TrimSpace(raw))
			if view == "" {
				continue
			}
			if _, ok := supportedReadViews[view]; !ok {
				continue
			}
			if _, ok := seen[view]; ok {
				continue
			}
			seen[view] = struct{}{}
			views = append(views, view)
		}
		if len(views) > 0 {
			props["view"] = map[string]any{
				"type":        "string",
				"enum":        views,
				"description": "Optional read projection. standard preserves the current response shape; compact/summary/full are opt-in per-tool migrations.",
			}
		}
	}
	if opts.Fields {
		props["fields"] = map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Optional allowlist of top-level or dotted fields to return when a tool supports field projection.",
		}
	}
	if opts.Include {
		props["include"] = map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Optional named related blocks to include when they are omitted by the selected view.",
		}
	}
	if opts.PreviewChars {
		props["preview_chars"] = map[string]any{
			"type":        "integer",
			"minimum":     0,
			"description": "Optional maximum characters for text previews. Zero means the tool default.",
		}
	}
	if opts.MaxOutputBytes {
		props["max_output_bytes"] = map[string]any{
			"type":        "integer",
			"minimum":     0,
			"description": "Optional caller budget for the MCP tool result. Tools that honor it report omitted/truncated metadata instead of silently dropping fields.",
		}
	}
	if opts.IncludeDiagnostics {
		props["include_diagnostics"] = map[string]any{
			"type":        "boolean",
			"description": "Opt in to timing/size diagnostics. Defaults to false for user-facing reads.",
		}
	}
	return props
}

func stringListFromAny(v any, name string) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	var rawValues []string
	switch typed := v.(type) {
	case []any:
		rawValues = make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must contain strings", name)
			}
			rawValues = append(rawValues, s)
		}
	case []string:
		rawValues = typed
	case string:
		rawValues = strings.Split(typed, ",")
	default:
		return nil, fmt.Errorf("%s must be a string array or comma-separated string", name)
	}
	out := make([]string, 0, len(rawValues))
	seen := map[string]struct{}{}
	for _, raw := range rawValues {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func nonNegativeIntFromAny(v any, name string) (int, error) {
	if v == nil {
		return 0, nil
	}
	var n int
	switch typed := v.(type) {
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		n = int(typed)
	case int:
		n = typed
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		n = int(parsed)
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return n, nil
}

func optionalBoolFromAny(v any, name string) (bool, bool, error) {
	if v == nil {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, false, fmt.Errorf("%s must be a boolean", name)
	}
	return b, true, nil
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return s
}
