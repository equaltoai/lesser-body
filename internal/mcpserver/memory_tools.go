package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/memory"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func registerMemoryTools(r *mcpruntime.ToolRegistry) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	for _, tool := range []struct {
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: memoryAppendDef(), Handler: handleMemoryAppend},
		{Def: memoryQueryDef(), Handler: handleMemoryQuery},
	} {
		if err := r.RegisterTool(tool.Def, tool.Handler); err != nil {
			return err
		}
	}

	return nil
}

func handleMemoryAppend(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Content    string   `json:"content"`
		EventID    string   `json:"event_id,omitempty"`
		OccurredAt string   `json:"occurred_at,omitempty"`
		Tags       []string `json:"tags,omitempty"`
		ExpiresAt  string   `json:"expires_at,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return nil, invalidParams("missing content")
	}

	occurredAt, err := parseRFC3339Optional(in.OccurredAt)
	if err != nil {
		return nil, invalidParams("invalid occurred_at")
	}
	expiresAt, err := parseRFC3339Optional(in.ExpiresAt)
	if err != nil {
		return nil, invalidParams("invalid expires_at")
	}

	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || strings.TrimSpace(p.Identity) == "" {
		return nil, fmt.Errorf("missing identity")
	}

	store, err := memory.Default()
	if err != nil {
		return nil, err
	}

	res, err := store.Append(ctx, strings.TrimSpace(p.Identity), memory.AppendInput{
		EventID:    strings.TrimSpace(in.EventID),
		OccurredAt: occurredAt,
		Content:    in.Content,
		Tags:       in.Tags,
		ExpiresAt:  expiresAt,
		HasExpiry:  strings.TrimSpace(in.ExpiresAt) != "",
	})
	if err != nil {
		if memory.IsValidationError(err) {
			return nil, invalidParams(err.Error())
		}
		return nil, err
	}

	return toolJSONResult(res)
}

func handleMemoryQuery(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		Start  string `json:"start,omitempty"`
		End    string `json:"end,omitempty"`
		Query  string `json:"query,omitempty"`
		Limit  int    `json:"limit,omitempty"`
		Cursor string `json:"cursor,omitempty"`
		Order  string `json:"order,omitempty"` // asc|desc
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	start, err := parseRFC3339Optional(in.Start)
	if err != nil {
		return nil, invalidParams("invalid start")
	}
	end, err := parseRFC3339Optional(in.End)
	if err != nil {
		return nil, invalidParams("invalid end")
	}

	p := auth.PrincipalFromToolContext(ctx)
	if p == nil || strings.TrimSpace(p.Identity) == "" {
		return nil, fmt.Errorf("missing identity")
	}

	store, err := memory.Default()
	if err != nil {
		return nil, err
	}

	res, err := store.Query(ctx, strings.TrimSpace(p.Identity), memory.QueryInput{
		Start:  start,
		End:    end,
		HasEnd: strings.TrimSpace(in.End) != "",
		Query:  strings.TrimSpace(in.Query),
		Limit:  in.Limit,
		Cursor: strings.TrimSpace(in.Cursor),
		Order:  strings.TrimSpace(in.Order),
	})
	if err != nil {
		if memory.IsValidationError(err) {
			return nil, invalidParams(err.Error())
		}
		return nil, err
	}

	return toolJSONResult(res)
}

func parseRFC3339Optional(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func memoryAppendDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "memory_append",
		Description: "Append a memory event to the authenticated agent's memory timeline.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content":{"type":"string"},
				"event_id":{"type":"string","description":"Optional ULID to make the call idempotent."},
				"occurred_at":{"type":"string","description":"RFC3339 timestamp for the memory event (optional)."},
				"tags":{"type":"array","items":{"type":"string"}},
				"expires_at":{"type":"string","description":"RFC3339 timestamp when this memory expires (optional)."}
			},
			"required":["content"]
		}`),
	}
}

func memoryQueryDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "memory_query",
		Description: "Query memory events for the authenticated agent.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"start":{"type":"string","description":"RFC3339 start time (inclusive)."},
				"end":{"type":"string","description":"RFC3339 end time (inclusive)."},
				"query":{"type":"string","description":"Text filter applied to memory content."},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"cursor":{"type":"string","description":"Opaque cursor from a previous memory_query response."},
				"order":{"type":"string","enum":["asc","desc"]}
			}
		}`),
	}
}
