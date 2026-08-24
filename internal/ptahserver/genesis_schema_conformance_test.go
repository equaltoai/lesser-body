package ptahserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

// Strict MCP clients validate tools/call structuredContent against the tool's
// declared OutputSchema and surface a mismatch as a tool error, even when the
// server already applied the side effect. These tests hold Body's genesis
// output schemas to what the handlers actually emit so a successful,
// side-effecting call can never fail its own contract at the envelope.

// assertMatchesDeclaredOutputSchema checks produced structuredContent against
// the schema vocabulary used by Ptah's output contracts, including required
// properties, alternatives, enums, and closed objects. It is deliberately not
// a general JSON Schema engine.
func assertMatchesDeclaredOutputSchema(t *testing.T, label string, schema json.RawMessage, structured map[string]any) {
	t.Helper()
	var declared any
	if err := json.Unmarshal(schema, &declared); err != nil {
		t.Fatalf("%s: declared output schema is invalid JSON: %v", label, err)
	}
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("%s: structuredContent is not JSON-encodable: %v", label, err)
	}
	var produced any
	if err := json.Unmarshal(encoded, &produced); err != nil {
		t.Fatalf("%s: structuredContent did not round-trip through JSON: %v", label, err)
	}
	var violations []string
	walkDeclaredOutputSchema("structuredContent", declared, produced, &violations)
	if len(violations) == 0 {
		return
	}
	sort.Strings(violations)
	t.Fatalf("%s: structuredContent does not match the declared output schema:\n  %s\nproduced: %s",
		label, joinLines(violations), string(encoded))
}

func walkDeclaredOutputSchema(path string, schema any, value any, violations *[]string) {
	node, ok := schema.(map[string]any)
	if !ok {
		return
	}
	if branches, ok := node["anyOf"].([]any); ok {
		matches := 0
		branchFailures := make([]string, 0, len(branches))
		for i, branch := range branches {
			var branchViolations []string
			walkDeclaredOutputSchema(path, branch, value, &branchViolations)
			if len(branchViolations) == 0 {
				matches++
				continue
			}
			branchFailures = append(branchFailures, fmt.Sprintf("branch %d: %s", i, joinLines(branchViolations)))
		}
		if matches == 0 {
			*violations = append(*violations, fmt.Sprintf("%s: matched no anyOf branches (%s)", path, joinLines(branchFailures)))
		}
		return
	}
	if allowed, ok := node["enum"].([]any); ok {
		match := false
		for _, candidate := range allowed {
			if candidate == value {
				match = true
				break
			}
		}
		if !match {
			*violations = append(*violations, fmt.Sprintf("%s: value %#v is outside the declared enum %v", path, value, allowed))
		}
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
		if open, declared := node["additionalProperties"].(bool); declared && !open {
			for key := range typed {
				if _, ok := properties[key]; !ok {
					*violations = append(*violations, fmt.Sprintf("%s.%s: undeclared key under additionalProperties:false", path, key))
				}
			}
		}
		for key, child := range typed {
			if childSchema, ok := properties[key]; ok {
				walkDeclaredOutputSchema(path+"."+key, childSchema, child, violations)
			}
		}
	case []any:
		items, ok := node["items"]
		if !ok {
			return
		}
		for i, child := range typed {
			walkDeclaredOutputSchema(fmt.Sprintf("%s[%d]", path, i), items, child, violations)
		}
	}
}

// Every schema-bearing Ptah tool uses the same structured error envelope as
// Ka. Pin the registered contract so strict clients preserve the underlying
// error instead of replacing it with -32602.
func TestEverySchemaBearingPtahToolErrorMatchesDeclaredOutputSchema(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	result, err := toolErrorResult("fixture_error", "representative tool failure", 400, map[string]any{
		"source": "conformance_fixture",
	})
	if err != nil {
		t.Fatalf("toolErrorResult: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("toolErrorResult returned %#v, want an MCP error result", result)
	}

	for _, def := range registry.List() {
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
			assertMatchesDeclaredOutputSchema(t, def.Name+"/error", def.OutputSchema, result.StructuredContent)
		})
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += line
	}
	return out
}

// lesser-body#480: Host returns SoulAgentRegistration.status (contract enum
// [pending, completed]) from the begin endpoint and Body promotes it to
// data.status, but the declared enum omitted both — so every real
// agent_genesis_begin failed its own output schema in a strict client.
func TestAgentGenesisBeginResultMatchesDeclaredOutputSchemaForHostRegistrationStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		hostStatus string
		wantStatus string
	}{
		{"host_reports_pending", "pending", "pending"},
		{"host_reports_completed", "completed", "completed"},
		{"host_omits_status", "", "begin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
			registration := map[string]any{
				"id":              "reg-123",
				"agent_id":        "agent-123",
				"domain":          "dev.trenchcoat.greater.website",
				"local_id":        "casekeeper",
				"authority_model": "instance_trust",
			}
			if tc.hostStatus != "" {
				registration["status"] = tc.hostStatus
			}
			registry := mcpruntime.NewToolRegistry()
			fake := &fakeGenesisClient{beginResponse: map[string]any{"registration": registration}}
			if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			ctx := operatorToolContext("owner", []string{"read", "write"}, "owner-oauth-bearer-test-only")

			result := callGenesisTool(t, registry, ctx, toolAgentGenesisBegin,
				`{"domain":"dev.trenchcoat.greater.website","local_id":"casekeeper"}`)
			data := structuredGenesisData(t, result)
			if got := data["status"]; got != tc.wantStatus {
				t.Fatalf("begin data.status = %#v, want %q", got, tc.wantStatus)
			}
			assertMatchesDeclaredOutputSchema(t, toolAgentGenesisBegin, agentGenesisBeginDef().OutputSchema, result.StructuredContent)
		})
	}
}

// A Host registration status outside the declared contract must not leak into
// data.status and re-break the envelope: the successful begin is preserved and
// reported as unknown, with the raw Host value still visible under
// data.registration.status.
func TestAgentGenesisBeginClampsUnknownHostRegistrationStatus(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	registry := mcpruntime.NewToolRegistry()
	fake := &fakeGenesisClient{beginResponse: map[string]any{
		"registration": map[string]any{
			"id":       "reg-123",
			"agent_id": "agent-123",
			"status":   "awaiting_dns_verification",
		},
	}}
	if err := RegisterTools(registry, WithGenesisClient(fake)); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := operatorToolContext("owner", []string{"read", "write"}, "owner-oauth-bearer-test-only")

	result := callGenesisTool(t, registry, ctx, toolAgentGenesisBegin,
		`{"domain":"dev.trenchcoat.greater.website","local_id":"casekeeper"}`)
	data := structuredGenesisData(t, result)
	if got := data["status"]; got != "unknown" {
		t.Fatalf("begin data.status = %#v, want unknown for an unclassifiable Host status", got)
	}
	registration, _ := data["registration"].(map[string]any)
	if got := registration["status"]; got != "awaiting_dns_verification" {
		t.Fatalf("data.registration.status = %#v, want the raw Host value preserved", got)
	}
	if got := data["registration_id"]; got != "reg-123" {
		t.Fatalf("begin dropped the successful registration: %#v", data)
	}
	assertMatchesDeclaredOutputSchema(t, toolAgentGenesisBegin, agentGenesisBeginDef().OutputSchema, result.StructuredContent)
}

// Audit guard for the same mismatch class across the conversation-backed
// genesis tools. Their Host vocabularies are validated by
// sanitizeGenesisConversation before reaching the envelope, so these assert the
// declared enums stay aligned with what the handlers emit.
func TestGenesisConversationToolResultsMatchDeclaredOutputSchema(t *testing.T) {
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "host-instance-key-test-only")
	fake := &fakeGenesisClient{
		beginResponse: map[string]any{
			"registration": map[string]any{"id": "reg-123", "agent_id": "agent-123", "status": "pending"},
		},
		advanceResponse: genesisConversationResponse("assistant_turn_ready", "transcript"),
		readResponses: []map[string]any{
			genesisConversationResponse("assistant_turn_ready", "transcript"),
			seedableGenesisConversationResponse(t, "declaration_ready", "reg-123", "conv-456", "agent-123"),
		},
		recoverResponse: genesisConversationResponse("assistant_turn_ready", "transcript"),
		preflightResponse: map[string]any{
			"conversation": map[string]any{
				"registration_id":       "reg-123",
				"conversation_id":       "conv-456",
				"status":                "declaration_ready",
				"declaration_candidate": genesisFinalizedCandidateProjection("preflight owner review"),
			},
			"authority_model": "instance_trust",
		},
		finalizeResponse: map[string]any{
			"agent_id": "agent-123",
			"publication": map[string]any{
				"agent_id":          "agent-123",
				"published_version": 1,
				"stage":             "hosted_offchain",
			},
		},
	}
	registry := mcpruntime.NewToolRegistry()
	if err := RegisterTools(registry, WithGenesisClient(fake), WithAgentRegistryStore(newMemoryAgentRegistry()), WithAgentContentStore(newVersionedFakeAgentContentStore())); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	ctx := operatorToolContext("owner", []string{"read", "write"}, "owner-oauth-bearer-test-only")

	for _, tc := range []struct {
		tool string
		args string
	}{
		{toolAgentGenesisAdvance, `{"registration_id":"reg-123","message":"Start the genesis conversation."}`},
		{toolAgentGenesisRead, `{"registration_id":"reg-123","conversation_id":"conv-456"}`},
		{toolAgentGenesisRecover, `{"registration_id":"reg-123","conversation_id":"conv-456"}`},
		{toolAgentGenesisFinalizePreflight, `{"registration_id":"reg-123","conversation_id":"conv-456"}`},
		{toolAgentGenesisFinalize, `{"registration_id":"reg-123","conversation_id":"conv-456"}`},
	} {
		result := callGenesisTool(t, registry, ctx, tc.tool, tc.args)
		assertMatchesDeclaredOutputSchema(t, tc.tool, genesisOutputSchema(), result.StructuredContent)
	}
}

// Pin the declared status enum to lesser-host's SoulAgentRegistration
// vocabulary so a Host contract change is caught here rather than by a client.
func TestGenesisStatusEnumCoversHostRegistrationVocabulary(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(genesisOutputSchema(), &schema); err != nil {
		t.Fatalf("genesis output schema invalid JSON: %v", err)
	}
	statusSchema := schema["properties"].(map[string]any)["data"].(map[string]any)["properties"].(map[string]any)["status"].(map[string]any)
	declared := map[string]bool{}
	for _, value := range statusSchema["enum"].([]any) {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("genesis status enum has a non-string member: %#v", value)
		}
		declared[text] = true
	}
	for _, want := range []string{"pending", "completed"} {
		if !declared[want] {
			t.Fatalf("genesis status enum missing Host registration status %q: %v", want, statusSchema["enum"])
		}
		if !validGenesisRegistrationStatus(want) {
			t.Fatalf("validGenesisRegistrationStatus rejects declared Host registration status %q", want)
		}
	}
	for _, want := range []string{"created", "in_progress", "assistant_turn_ready", "declaration_ready", "published", "failed"} {
		if !declared[want] {
			t.Fatalf("genesis status enum missing Host conversation status %q: %v", want, statusSchema["enum"])
		}
		if !validGenesisConversationStatus(want) {
			t.Fatalf("validGenesisConversationStatus rejects declared Host conversation status %q", want)
		}
	}
	if !declared["unknown"] {
		t.Fatalf("genesis status enum must declare the unknown fallback: %v", statusSchema["enum"])
	}
}
