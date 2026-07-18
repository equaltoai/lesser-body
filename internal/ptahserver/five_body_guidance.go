package ptahserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	_ "embed"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
	fiveBodySchemaVersion   = "soul-five-body-schema.v2"
	fiveBodyGuidanceVersion = "soul-five-body-guidance.v2"
	fiveBodyHostPR          = 928
	fiveBodyHostHeadSHA     = "8b8bb3014908af3c4838da50d50aa0f72aee3406"

	resourceSoulSchemaV2              = "soul-schema-v2"
	resourceGenesisInterviewGuide     = "genesis-interview-guide"
	resourceAgentSideGenesisPlaybook  = "agent-side-genesis-playbook"
	resourceGenesisRubric             = "genesis-rubric"
	promptDraftGenesisTurn            = "draft-genesis-turn"
	promptReviewSoulDraft             = "review-soul-draft"
	ptahGenesisGuidanceResourcePrefix = "ptah://genesis/"
)

//go:embed testdata/host-contract/pr-928/soul-five-body-schema.md
var hostFiveBodyContractDoc []byte

//go:embed testdata/host-contract/pr-928/soul-five-body.schema.v2.json
var hostFiveBodySchemaJSON []byte

//go:embed testdata/host-contract/pr-928/soul-five-body.example.v2.json
var hostFiveBodyExampleJSON []byte

//go:embed testdata/host-contract/pr-928/metadata.json
var hostFiveBodyMetadataJSON []byte

type fiveBodyContractMetadata struct {
	SourceRepository  string `json:"source_repository"`
	SourcePullRequest int    `json:"source_pull_request"`
	SourceIssue       int    `json:"source_issue"`
	SourceHeadSHA     string `json:"source_head_sha"`
	SchemaVersion     string `json:"schema_version"`
	GuidanceVersion   string `json:"guidance_version"`
	ContractDocSHA256 string `json:"contract_doc_sha256"`
	SchemaSHA256      string `json:"schema_sha256"`
	ExampleSHA256     string `json:"example_sha256"`
	Notes             string `json:"notes"`
}

// RegisterResources registers Ptah's static AppTheory MCP guidance resources.
// The content mirrors Host PR #928 contract artifacts; Body renders guidance
// from the pinned Host contract instead of defining a competing schema.
func RegisterResources(srv *mcpruntime.Server) error {
	if srv == nil || srv.Resources() == nil {
		return fmt.Errorf("ptah resource registry is nil")
	}
	r := srv.Resources()
	for _, res := range []struct {
		def     mcpruntime.ResourceDef
		handler mcpruntime.ResourceHandler
	}{
		{def: fiveBodyResourceDef(resourceSoulSchemaV2, "Host soul five-body schema v2"), handler: fiveBodySchemaResource},
		{def: fiveBodyResourceDef(resourceGenesisInterviewGuide, "Genesis interview guide"), handler: fiveBodyInterviewGuideResource},
		{def: fiveBodyResourceDef(resourceAgentSideGenesisPlaybook, "Agent-side genesis playbook"), handler: fiveBodyPlaybookResource},
		{def: fiveBodyResourceDef(resourceGenesisRubric, "Genesis rubric"), handler: fiveBodyRubricResource},
	} {
		if err := r.RegisterResource(res.def, res.handler); err != nil {
			return err
		}
	}
	return r.RegisterResourceTemplate(mcpruntime.ResourceTemplateDef{
		URITemplate: ptahGenesisGuidanceResourcePrefix + "{resource}",
		Name:        "genesis-guidance-resource",
		Title:       "Genesis guidance resource",
		Description: "Template for Ptah's static Host-contract-backed five-body genesis guidance resources.",
		MimeType:    "application/json",
	})
}

// RegisterPrompts registers Ptah's static AppTheory MCP genesis prompts.
func RegisterPrompts(srv *mcpruntime.Server) error {
	if srv == nil || srv.Prompts() == nil {
		return fmt.Errorf("ptah prompt registry is nil")
	}
	r := srv.Prompts()
	for _, prompt := range []struct {
		def     mcpruntime.PromptDef
		handler mcpruntime.PromptHandler
	}{
		{
			def: mcpruntime.PromptDef{
				Name:        promptDraftGenesisTurn,
				Title:       "Draft genesis turn",
				Description: "Draft the next owner/operator turn for Host-backed five-body genesis without inventing Body-local state.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "phase", Description: "Current five-body interview phase: identity, philosophy, discipline, boundaries, soul, review, or affirmation."},
					{Name: "current_status", Description: "Host conversation status from agent_genesis_read/advance."},
					{Name: "owner_intent", Description: "What the account-holder wants this turn to accomplish."},
					{Name: "known_facts", Description: "Facts already established in the Host conversation."},
				},
			},
			handler: promptDraftFiveBodyGenesisTurn,
		},
		{
			def: mcpruntime.PromptDef{
				Name:        promptReviewSoulDraft,
				Title:       "Review soul draft",
				Description: "Review a proposed five-body soul declaration against the Host-owned schema, refusal floor, cadence, and rubric.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "draft", Description: "Draft declaration or summary to review.", Required: true},
					{Name: "focus", Description: "Optional review focus such as refusal_floor, cadence, capabilities, transparency, or schema."},
				},
			},
			handler: promptReviewFiveBodySoulDraft,
		},
	} {
		if err := r.RegisterPrompt(prompt.def, prompt.handler); err != nil {
			return err
		}
	}
	return nil
}

func fiveBodyResourceDef(name string, title string) mcpruntime.ResourceDef {
	return mcpruntime.ResourceDef{
		URI:         fiveBodyResourceURI(name),
		Name:        name,
		Title:       title,
		Description: "Ptah five-body genesis guidance rendered from the Host-owned " + fiveBodySchemaVersion + " / " + fiveBodyGuidanceVersion + " contract.",
		MimeType:    "application/json",
	}
}

func fiveBodyResourceURI(name string) string {
	return ptahGenesisGuidanceResourcePrefix + name
}

func fiveBodySchemaResource(context.Context) ([]mcpruntime.ResourceContent, error) {
	var schema any
	if err := json.Unmarshal(hostFiveBodySchemaJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode mirrored Host five-body schema: %w", err)
	}
	var example any
	if err := json.Unmarshal(hostFiveBodyExampleJSON, &example); err != nil {
		return nil, fmt.Errorf("decode mirrored Host five-body example: %w", err)
	}
	return fiveBodyResourceJSON(resourceSoulSchemaV2, map[string]any{
		"kind":              resourceSoulSchemaV2,
		"contract":          fiveBodyContractDescriptor(),
		"schema":            schema,
		"example":           example,
		"host_contract_doc": string(hostFiveBodyContractDoc),
		"consumer_boundary": "Body mirrors and renders Host's contract; Host remains schema/guidance owner and produced-declaration authority.",
	})
}

func fiveBodyInterviewGuideResource(context.Context) ([]mcpruntime.ResourceContent, error) {
	return fiveBodyResourceJSON(resourceGenesisInterviewGuide, map[string]any{
		"kind":     resourceGenesisInterviewGuide,
		"contract": fiveBodyContractDescriptor(),
		"interview": map[string]any{
			"stages": []map[string]any{
				{
					"body":          "identity",
					"purpose":       "Establish who the agent is, the domain/local-id fit, voice, purpose, and the single definition of its named cadence.",
					"read_back":     true,
					"must_not_skip": "Do not advance with an empty identity body or an unowned identity assertion.",
				},
				{
					"body":          "philosophy",
					"purpose":       "Elicit values, trade-offs, commitments, decision posture, and how uncertainty is handled.",
					"read_back":     true,
					"must_not_skip": "Do not treat a generic mission statement as philosophy; require explicit trade-offs.",
				},
				{
					"body":          "discipline",
					"purpose":       "Capture operating discipline, evidence habits, escalation rules, and references to the named cadence without redefining it.",
					"read_back":     true,
					"must_not_skip": "Do not repeat the cadence definition in every body; define once and refer to it.",
				},
				{
					"body":          "boundaries",
					"purpose":       "Draw scope limits, safety invariants, handoff triggers, and refusal categories.",
					"read_back":     true,
					"must_not_skip": "Do not accept vague safety claims; name concrete invariants and handoff triggers.",
				},
				{
					"body":          "soul",
					"purpose":       "Record load-bearing commitments and the refusal floor: at least three concrete bypass/invariant/closest-safe-path refusals.",
					"read_back":     true,
					"must_not_skip": "Do not finalize until the refusal floor is concrete and complete.",
				},
			},
			"satellites":            []string{"capabilities: concrete self-declared capabilities only", "transparency: model/provider uncertainty, operational notes, and self-declared notice"},
			"canonical_affirmation": canonicalGenesisAffirmation(),
			"host_owned":            "Host performs the durable extraction/validation and owns the produced declarations; Body only guides MCP callers.",
		},
	})
}

func fiveBodyPlaybookResource(context.Context) ([]mcpruntime.ResourceContent, error) {
	return fiveBodyResourceJSON(resourceAgentSideGenesisPlaybook, map[string]any{
		"kind":     resourceAgentSideGenesisPlaybook,
		"contract": fiveBodyContractDescriptor(),
		"playbook": map[string]any{
			"authority": []string{
				"Use explicit instance owner/operator OAuth only; ordinary read/write tokens and x402 evidence are not owner authority.",
				"Treat Host HostedGenesisSession as the state authority. Body must not create a local genesis state machine or fabricate Lesser directory entries.",
			},
			"tool_sequence": []map[string]string{
				{"step": "begin", "tool": toolAgentGenesisBegin, "instruction": "Start the Host registration lane for the intended managed domain/local_id."},
				{"step": "interview", "tool": toolAgentGenesisAdvance, "instruction": "Advance identity, philosophy, discipline, boundaries, and soul stages; persist Host conversation_id."},
				{"step": "read", "tool": toolAgentGenesisRead, "instruction": "Poll Host status and follow structuredContent.data.guidance.next_tool."},
				{"step": "complete", "tool": toolAgentGenesisComplete, "instruction": "Ask Host to extract and validate produced declarations; callers do not submit declarations."},
				{"step": "preflight", "tool": toolAgentGenesisFinalizePreflight, "instruction": "Check Host readiness before finalization."},
				{"step": "finalize", "tool": toolAgentGenesisFinalize, "instruction": "Finalize through Host; Body writes a Host-derived Ptah registry row only after Host publication."},
				{"step": "verify", "tool": toolAgentGet, "instruction": "Verify account-scoped Body/Ptah registry visibility; use agent_list for the merged registry/live view."},
			},
			"recovery": "If Host returns failure.recovery.action=restart_soul_bootstrap, start a fresh lane with agent_genesis_begin; do not call recover for that action.",
			"listing": map[string]string{
				"tool":   toolAgentGenesisList,
				"status": "not_available until Body grows a Host list client surface for the Host instance mint-conversation summary endpoint.",
			},
			"model_guidance": "Host PR #928 records mintingModel in declaration evidence but does not publish a Body-consumable model allowlist artifact or endpoint; operators must use Host-configured models and watch Host contract follow-up.",
		},
	})
}

func fiveBodyRubricResource(context.Context) ([]mcpruntime.ResourceContent, error) {
	return fiveBodyResourceJSON(resourceGenesisRubric, map[string]any{
		"kind":     resourceGenesisRubric,
		"contract": fiveBodyContractDescriptor(),
		"rubric": map[string]any{
			"pass_conditions": []string{
				"identity, philosophy, discipline, boundaries, and soul bodies are present and non-empty.",
				"capabilities and transparency are satellites, not replacements for any body.",
				"soul.refusals has at least three concrete bypass/invariant/closestSafePath rows.",
				"The named cadence is defined once in identity and carried as a commitment/reference elsewhere.",
				"The canonical affirmation is asked exactly before final declaration acceptance.",
				"Independent review finds, refutes, and reports unresolved weaknesses before declaration_ready.",
			},
			"host_validation_codes": []string{
				"five_body.identity.required",
				"five_body.philosophy.required",
				"five_body.discipline.required",
				"five_body.boundaries.required",
				"five_body.soul.required",
				"soul.refusals.required",
				"soul.refusals.invalid",
				"adversarial_review.required",
			},
			"refusal_floor": map[string]any{
				"min_items":       3,
				"max_items":       8,
				"required_fields": []string{"bypass", "invariant", "closestSafePath"},
				"reject_generic":  []string{"unsafe requests", "policy violations", "bad things", "be safe", "n/a"},
			},
			"fail_closed": "If Host PR #928 changes schema/guidance versions or checksums, Body fixtures/tests fail and must be synchronized before deploy proof.",
		},
	})
}

func promptDraftFiveBodyGenesisTurn(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	values := promptArgs(args)
	phase := firstNonEmpty(values["phase"], "identity")
	status := firstNonEmpty(values["current_status"], "unknown")
	intent := firstNonEmpty(values["owner_intent"], "advance the Host-owned five-body genesis interview")
	facts := firstNonEmpty(values["known_facts"], "No prior facts supplied; ask for the minimum missing facts instead of inventing them.")

	text := strings.Join([]string{
		"You are helping an account-holder draft the next owner/operator message for Body/Ptah Host-backed genesis.",
		"Use the AppTheory resources ptah://genesis/genesis-interview-guide, ptah://genesis/agent-side-genesis-playbook, and ptah://genesis/genesis-rubric as the guidance source.",
		"Host owns the durable HostedGenesisSession state and the " + fiveBodySchemaVersion + " / " + fiveBodyGuidanceVersion + " contract; do not invent Body-local state, declarations, model allowlists, or Lesser directory entries.",
		"Current phase: " + phase + ". Current Host status: " + status + ". Owner intent: " + intent + ". Known facts: " + facts,
		"Draft one concise turn that advances the current phase and asks only for missing information. The staged bodies are identity, philosophy, discipline, boundaries, and soul; capabilities/transparency are satellites.",
		"Preserve the refusal floor: by the soul phase, require concrete bypass, invariant, and closest safe path rows. Define the named cadence once, then refer to it rather than repeating it in every body.",
		"Before final acceptance, use the canonical affirmation exactly: " + canonicalGenesisAffirmation(),
	}, "\n\n")

	return &mcpruntime.PromptResult{
		Description: "Draft the next Host-backed five-body genesis turn.",
		Messages: []mcpruntime.PromptMessage{{
			Role:    "user",
			Content: mcpruntime.ContentBlock{Type: "text", Text: text},
		}},
	}, nil
}

func promptReviewFiveBodySoulDraft(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	values := promptArgs(args)
	draft := values["draft"]
	focus := firstNonEmpty(values["focus"], "full five-body rubric")
	if strings.TrimSpace(draft) == "" {
		draft = "<no draft supplied; report that review cannot pass without a draft>"
	}

	text := strings.Join([]string{
		"Review the supplied soul declaration draft against Host's five-body contract " + fiveBodySchemaVersion + " / " + fiveBodyGuidanceVersion + ".",
		"Focus: " + focus + ". Do not rewrite the Host schema and do not treat this review as Host validation proof.",
		"Check for five first-class bodies: identity, philosophy, discipline, boundaries, soul. Check that capabilities and transparency are satellites with self-declared capability claims only.",
		"Check the refusal floor: at least three concrete refusals, each with bypass, invariant, and closestSafePath. Reject generic placeholders.",
		"Check the cadence rule: named cadence defined once, then referenced. Check the canonical affirmation is present exactly when final acceptance is requested: " + canonicalGenesisAffirmation(),
		"Report findings as: finding, refutation if resolved, and report. If unresolved, name the exact missing body or Host validation code when known.",
		"Draft under review:\n" + draft,
	}, "\n\n")

	return &mcpruntime.PromptResult{
		Description: "Review a five-body soul draft against Host-owned guidance.",
		Messages: []mcpruntime.PromptMessage{{
			Role:    "user",
			Content: mcpruntime.ContentBlock{Type: "text", Text: text},
		}},
	}, nil
}

func fiveBodyResourceJSON(name string, payload any) ([]mcpruntime.ResourceContent, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Ptah five-body resource: %w", err)
	}
	return []mcpruntime.ResourceContent{{
		URI:      fiveBodyResourceURI(name),
		MimeType: "application/json",
		Text:     string(b),
	}}, nil
}

func fiveBodyContractDescriptor() map[string]any {
	meta := mustFiveBodyMetadata()
	return map[string]any{
		"schema_version":      meta.SchemaVersion,
		"guidance_version":    meta.GuidanceVersion,
		"source_repository":   meta.SourceRepository,
		"source_pull_request": meta.SourcePullRequest,
		"source_issue":        meta.SourceIssue,
		"source_head_sha":     meta.SourceHeadSHA,
		"checksums": map[string]string{
			"contract_doc_sha256": meta.ContractDocSHA256,
			"schema_sha256":       meta.SchemaSHA256,
			"example_sha256":      meta.ExampleSHA256,
		},
		"mirror_policy": "checked Body fixture; refresh only from Host-owned artifacts, never by editing a Body-local schema",
	}
}

func mustFiveBodyMetadata() fiveBodyContractMetadata {
	var meta fiveBodyContractMetadata
	if err := json.Unmarshal(hostFiveBodyMetadataJSON, &meta); err != nil {
		panic("invalid embedded Host five-body metadata: " + err.Error())
	}
	return meta
}

func canonicalGenesisAffirmation() string {
	return "Do you affirm this declaration as the foundation of your minted soul? If there is anything here you would correct, qualify, or strike before it is inscribed, name it now."
}

func promptArgs(args json.RawMessage) map[string]string {
	var raw map[string]any
	if len(strings.TrimSpace(string(args))) == 0 {
		return map[string]string{}
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		switch v := value.(type) {
		case string:
			out[key] = strings.TrimSpace(v)
		default:
			b, _ := json.Marshal(v)
			out[key] = strings.TrimSpace(string(b))
		}
	}
	return out
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
