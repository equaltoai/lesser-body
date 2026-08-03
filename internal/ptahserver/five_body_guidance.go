package ptahserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"embed"

	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

const (
	fiveBodySchemaVersion   = "soul-five-body-schema.v2"
	fiveBodyGuidanceVersion = "soul-five-body-guidance.v2"
	fiveBodyHostPR          = 980
	fiveBodyHostHeadSHA     = "5f873e184ba70e662ed2c945a71357385ac196bc"

	resourceSoulSchemaV2              = "soul-schema-v2"
	resourceGenesisInterviewGuide     = "genesis-interview-guide"
	resourceAgentSideGenesisPlaybook  = "agent-side-genesis-playbook"
	resourceGenesisRubric             = "genesis-rubric"
	promptDraftGenesisTurn            = "draft-genesis-turn"
	promptReviewGenesisCandidate      = "review-genesis-candidate"
	ptahGenesisGuidanceResourcePrefix = "ptah://genesis/"
)

//go:embed testdata/host-contract/pr-978/soul-five-body-schema.md
var hostFiveBodyContractDoc []byte

//go:embed testdata/host-contract/pr-978/soul-five-body.schema.v2.json
var hostFiveBodySchemaJSON []byte

//go:embed testdata/host-contract/pr-978/soul-five-body.example.v2.json
var hostFiveBodyExampleJSON []byte

//go:embed testdata/host-contract/pr-978/metadata.json
var hostFiveBodyMetadataJSON []byte

//go:embed testdata/host-contract/pr-978/*
var hostContractMirrorFS embed.FS

type fiveBodyContractMetadata struct {
	SourceRepository  string                     `json:"source_repository"`
	SourcePullRequest int                        `json:"source_pull_request"`
	SourceIssue       int                        `json:"source_issue"`
	SourceHeadSHA     string                     `json:"source_head_sha"`
	SchemaVersion     string                     `json:"schema_version"`
	GuidanceVersion   string                     `json:"guidance_version"`
	Artifacts         []fiveBodyContractArtifact `json:"artifacts"`
	Notes             string                     `json:"notes"`
}

type fiveBodyContractArtifact struct {
	SourcePath string `json:"source_path"`
	MirrorFile string `json:"mirror_file"`
	SHA256     string `json:"sha256"`
}

// RegisterResources registers Ptah's static AppTheory MCP guidance resources.
// The content mirrors Host PR #980 contract artifacts; Body renders guidance
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
		{def: fiveBodyResourceDef(resourceGenesisOperatorSkill, "Genesis operator skill bundle"), handler: fiveBodyGenesisOperatorSkillResource},
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
					{Name: "phase", Description: "Current Host candidate phase/section: identity, philosophy, discipline, boundaries, soul, or review."},
					{Name: "current_status", Description: "Host conversation status from agent_genesis_read/advance."},
					{Name: "owner_intent", Description: "What the account-holder wants this turn to accomplish."},
					{Name: "known_facts", Description: "Facts already established in the Host conversation."},
				},
			},
			handler: promptDraftFiveBodyGenesisTurn,
		},
		{
			def: mcpruntime.PromptDef{
				Name:        promptReviewGenesisCandidate,
				Title:       "Review Host genesis candidate",
				Description: "Inspect Host's exact owner review and prepare a structural affirm or edit action without treating prose as authority.",
				Arguments: []mcpruntime.PromptArgument{
					{Name: "review_text", Description: "Exact lossless declaration_candidate.review.review_text returned by Host.", Required: true},
					{Name: "candidate_revision", Description: "Exact Host candidate revision binding.", Required: true},
					{Name: "candidate_hash", Description: "Exact Host candidate hash binding.", Required: true},
					{Name: "review_hash", Description: "Exact Host owner-review hash binding.", Required: true},
					{Name: "owner_intent", Description: "Either affirm, or edit with the exact section and requested revision."},
				},
			},
			handler: promptReviewGenesisCandidateAction,
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
		"consumer_boundary": "Body renders the Host-owned five-body schema/example as read-only reference while Host remains typed-candidate and persistence authority. Current action/response shapes are pinned separately in contract.checksums.",
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
			"satellites": []string{"capabilities: concrete self-declared capabilities only", "transparency: model/provider uncertainty, operational notes, and self-declared notice"},
			"host_owned": "Host owns HostedGenesisSession, typed candidate construction, candidate persistence, and the five provider declaration tools inside its AppTheory MicroVM; Body only relays the bounded public projection.",
			"review_protocol": map[string]any{
				"inspect":   "Read the exact declaration_candidate.review.review_text without substituting the bounded transcript message.",
				"guidance":  "Select one of structuredContent.data.guidance.candidate_actions: one affirm plus five exact per-section edits; pass only its nested candidate_action unchanged.",
				"affirm":    "Call agent_genesis_advance with action=affirm, no section, and the exact candidate_revision/candidate_hash/review_hash bindings.",
				"edit":      "Call agent_genesis_advance with action=edit, one exact section from the five-body enum, the exact bindings, and an owner revision message.",
				"authority": "Only structural candidate_action has authority; free-form affirmation phrases have zero authority.",
			},
		},
	})
}

func fiveBodyPlaybookResource(context.Context) ([]mcpruntime.ResourceContent, error) {
	return fiveBodyResourceJSON(resourceAgentSideGenesisPlaybook, map[string]any{
		"kind":     resourceAgentSideGenesisPlaybook,
		"contract": fiveBodyContractDescriptor(),
		"playbook": map[string]any{
			"skill": map[string]string{
				"tool":        toolAgentGenesisSkillGet,
				"resource":    fiveBodyResourceURI(resourceGenesisOperatorSkill),
				"instruction": "Fetch the client-native genesis operator skill bundle first and use its SKILL.md as the operating playbook. Ptah serves content only; clients decide materialization and Body never installs or writes anything for it.",
			},
			"authority": []string{
				"Use explicit instance owner/operator OAuth only; ordinary read/write tokens and x402 evidence are not owner authority.",
				"Treat Host HostedGenesisSession as the sole state authority. Body must not create a declaration builder, extractor, candidate store, local state machine, or fabricated Lesser directory entry.",
				"The five provider section tools execute only inside Host's AppTheory MicroVM and are never public Body/Ptah tools.",
			},
			"wait_only_processing_states": map[string]any{
				"states":              []any{"in_progress"},
				"next_tool":           toolAgentGenesisRead,
				"forbidden_next_tool": toolAgentGenesisAdvance,
				"wait":                true,
				"poll_after_seconds":  "Use the Host-provided poll_after_seconds/expected_wait_seconds value when present.",
				"instruction":         "Host is processing. Do not call " + toolAgentGenesisAdvance + " again and do not nudge; wait poll_after_seconds when present, then call " + toolAgentGenesisRead + ". Only advance after Host reports assistant_turn_ready.",
			},
			"tool_sequence": []map[string]any{
				{"step": "skill", "tool": toolAgentGenesisSkillGet, "instruction": "Fetch and read the read-only genesis operator skill bundle before beginning."},
				{"step": "begin", "tool": toolAgentGenesisBegin, "instruction": "Start the Host registration lane for the intended managed domain/local_id."},
				{"step": "list", "tool": toolAgentGenesisList, "instruction": "When resuming or ids are unclear, list Host-backed summaries and follow recommended_start exactly."},
				{"step": "section", "tool": toolAgentGenesisAdvance, "instruction": "When assistant_turn_ready and candidate phase is section, submit the next normal owner message for current_section; model is optional on the first turn, with omission selecting Host's configured default alias; Host invokes its private provider section tool."},
				{"step": "read", "tool": toolAgentGenesisRead, "instruction": "Poll Host status and follow structuredContent.data.guidance.next_tool; if guidance.wait=true, wait poll_after_seconds when present and never nudge with agent_genesis_advance."},
				{"step": "review", "tool": toolAgentGenesisAdvance, "instruction": "Inspect exact review_text, select one of guidance.candidate_actions, then pass only its nested structural candidate_action unchanged: affirm has no section; the five edits each have one exact section plus owner revision message; all carry exact returned bindings."},
				{"step": "preflight", "tool": toolAgentGenesisFinalizePreflight, "instruction": "Check Host readiness before finalization."},
				{"step": "finalize", "tool": toolAgentGenesisFinalize, "instruction": "Body deterministically hash-verifies and transforms Host's finalized declaration before publication, then writes the Host-derived registry row, published Panonomous v2 soul seed, and create-only default agent_instructions draft after Host publication. Declaration application invokes no MicroVM or model."},
				{"step": "verify", "tool": toolAgentGet, "instruction": "Verify account-scoped Body/Ptah registry visibility plus published soul_seed and draft instructions_seed; use agent_list for the merged registry/live view. Ba needs no manual content-authoring step."},
			},
			"recovery": "Follow Host's exact failure.recovery.action: retry_same_step waits the bounded retry delay then calls agent_genesis_recover exactly once; refresh_state reads exactly once; restart_soul_bootstrap begins a fresh lane and forbids recover (its exact terminal untyped/stale hard-cut projection may omit declaration_candidate); operator_action stops automatic calls and requires operator contact.",
			"recovery_actions": map[string]any{
				"retry_same_step": map[string]any{
					"next_tool":   toolAgentGenesisRecover,
					"fresh_lane":  false,
					"instruction": "Wait retry_after_seconds when present, then call agent_genesis_recover exactly once for the same registration_id/conversation_id.",
				},
				"restart_soul_bootstrap": map[string]any{
					"next_tool":           toolAgentGenesisBegin,
					"forbidden_next_tool": toolAgentGenesisRecover,
					"fresh_lane":          true,
					"instruction":         "Start a fresh lane with agent_genesis_begin; never call agent_genesis_recover for this action.",
				},
				"refresh_state": map[string]any{
					"next_tool":   toolAgentGenesisRead,
					"fresh_lane":  false,
					"instruction": "Call agent_genesis_read exactly once, then follow the newly returned Host status/recovery action; do not write or poll endlessly.",
				},
				"operator_action": map[string]any{
					"fresh_lane":  false,
					"instruction": "Stop automatic Genesis tool calls and contact the instance operator; no next write tool is selected.",
				},
			},
			"listing": map[string]string{
				"tool":        toolAgentGenesisList,
				"status":      "Host-backed summary-only recovery index.",
				"instruction": "Start with list when registration_id/conversation_id are unclear, then follow recommended_start.recommended_next_tool and recommended_arguments. Failed lanes must be read first for typed failure.recovery; review bindings and exact review_text are available only from read/advance, not the summary list.",
			},
			"candidate_protocol": map[string]any{
				"section":           "Normal owner message through agent_genesis_advance; provider section tools stay inside Host's AppTheory MicroVM.",
				"review":            "Inspect exact declaration_candidate.review.review_text and use exact candidate_revision/candidate_hash/review_hash with structural candidate_action. Free-form phrases have no authority.",
				"declaration_ready": toolAgentGenesisFinalizePreflight,
				"published":         []string{toolAgentGet, toolAgentList},
			},
			"model_guidance": "The mirrored Host contract does not publish a Body-consumable model allowlist artifact or endpoint. agent_genesis_advance model is optional: omit it for Host's configured default alias, or pass an explicit Host alias unchanged and preserve Host's typed validation error.",
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
				"The owner inspects Host's exact lossless review and submits a structural affirm or edit action bound to the exact revision and hashes.",
				"Free-form or canonical affirmation phrases are treated as having zero authority.",
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
			"fail_closed": "If Host PR #980's exact deployed merge commit changes any mirrored candidate request/response artifact or checksum, Body fixtures/tests fail and must be synchronized before deploy proof.",
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
		"If current_status is in_progress, do not draft an owner advance/nudge. Tell the caller to wait poll_after_seconds when present and then call " + toolAgentGenesisRead + ".",
		"Current phase: " + phase + ". Current Host status: " + status + ". Owner intent: " + intent + ". Known facts: " + facts,
		"Draft one concise turn that advances the current phase and asks only for missing information. The staged bodies are identity, philosophy, discipline, boundaries, and soul; capabilities/transparency are satellites.",
		"Preserve the refusal floor: by the soul phase, require concrete bypass, invariant, and closest safe path rows. Define the named cadence once, then refer to it rather than repeating it in every body.",
		"If phase is review, do not draft affirmation prose. Tell the caller to inspect the exact Host review_text and use review-genesis-candidate; only structural candidate_action bound to the exact revision and hashes has authority.",
	}, "\n\n")

	return &mcpruntime.PromptResult{
		Description: "Draft the next Host-backed five-body genesis turn.",
		Messages: []mcpruntime.PromptMessage{{
			Role:    "user",
			Content: mcpruntime.ContentBlock{Type: "text", Text: text},
		}},
	}, nil
}

func promptReviewGenesisCandidateAction(_ context.Context, args json.RawMessage) (*mcpruntime.PromptResult, error) {
	values := promptArgs(args)
	reviewText := values["review_text"]
	if strings.TrimSpace(reviewText) == "" {
		reviewText = "<missing exact Host review_text; stop without proposing an action>"
	}
	revision := firstNonEmpty(values["candidate_revision"], "<missing>")
	candidateHash := firstNonEmpty(values["candidate_hash"], "<missing>")
	reviewHash := firstNonEmpty(values["review_hash"], "<missing>")
	ownerIntent := firstNonEmpty(values["owner_intent"], "inspect before choosing affirm or edit")

	text := strings.Join([]string{
		"Inspect the exact Host-owned declaration candidate review. Do not reconstruct candidate JSON, infer truth from transcript text, or treat prose as an affirmation action.",
		"Exact bindings: candidate_revision=" + revision + "; candidate_hash=" + candidateHash + "; review_hash=" + reviewHash + ". Owner intent: " + ownerIntent + ".",
		"Check identity, philosophy, discipline, boundaries, soul, the refusal floor, cadence references, capabilities, and transparency in the exact review below.",
		"If accepted, propose agent_genesis_advance candidate_action with action=affirm, the exact three bindings, and no section. If revision is needed, propose action=edit with one exact section (identity, philosophy, discipline, boundaries, or soul), the exact bindings, and require an owner revision message.",
		"Free-form or canonical affirmation phrases have zero authority. Do not expose or invoke Host's private provider section tools.",
		"Exact Host review_text:\n" + reviewText,
	}, "\n\n")

	return &mcpruntime.PromptResult{
		Description: "Inspect Host's exact declaration-candidate review and prepare a structural owner action.",
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
	checksums := make(map[string]string, len(meta.Artifacts))
	for _, artifact := range meta.Artifacts {
		checksums[artifact.MirrorFile] = artifact.SHA256
	}
	return map[string]any{
		"schema_version":      meta.SchemaVersion,
		"guidance_version":    meta.GuidanceVersion,
		"source_repository":   meta.SourceRepository,
		"source_pull_request": meta.SourcePullRequest,
		"source_issue":        meta.SourceIssue,
		"source_head_sha":     meta.SourceHeadSHA,
		"checksums":           checksums,
		"mirror_policy":       "checked Body fixture; refresh only from Host-owned artifacts, never by editing a Body-local schema",
	}
}

func mustFiveBodyMetadata() fiveBodyContractMetadata {
	var meta fiveBodyContractMetadata
	if err := json.Unmarshal(hostFiveBodyMetadataJSON, &meta); err != nil {
		panic("invalid embedded Host five-body metadata: " + err.Error())
	}
	return meta
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
