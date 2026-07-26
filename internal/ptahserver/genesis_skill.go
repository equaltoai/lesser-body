package ptahserver

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

const (
	toolAgentGenesisSkillGet     = "agent_genesis_skill_get"
	resourceGenesisOperatorSkill = "genesis-operator-skill"

	genesisSkillID   = "lesser-body.ptah.genesis-operator"
	genesisSkillName = "ptah-genesis-operator"
)

type genesisSkillFile struct {
	path      string
	mediaType string
	content   string
}

// genesisSkillVersion derives the stable skill version from the Host-owned
// guidance version and the pinned Host contract commit. A Host PR #980 refresh
// changes this version and the bundle id together with the mirrored fixtures.
func genesisSkillVersion() string {
	head := fiveBodyHostHeadSHA
	if len(head) > 12 {
		head = head[:12]
	}
	return fiveBodyGuidanceVersion + "+host.pr" + strconv.Itoa(fiveBodyHostPR) + "." + head
}

func genesisSkillFiles() []genesisSkillFile {
	return []genesisSkillFile{
		{path: "SKILL.md", mediaType: "text/markdown", content: genesisSkillMarkdown()},
		{path: "references/genesis-guidance-map.md", mediaType: "text/markdown", content: genesisSkillGuidanceMap()},
	}
}

func genesisSkillBundleID(files []genesisSkillFile) string {
	var manifest strings.Builder
	manifest.WriteString(genesisSkillVersion())
	manifest.WriteByte('\n')
	for _, file := range files {
		manifest.WriteString(file.path)
		manifest.WriteByte('\n')
		manifest.WriteString(sha256Hex([]byte(file.content)))
		manifest.WriteByte('\n')
	}
	return "sha256:" + sha256Hex([]byte(manifest.String()))
}

func genesisSkillBundleData() map[string]any {
	files := genesisSkillFiles()
	entries := make([]map[string]any, 0, len(files))
	totalBytes := 0
	for _, file := range files {
		totalBytes += len(file.content)
		entries = append(entries, map[string]any{
			"path":       file.path,
			"media_type": file.mediaType,
			"bytes":      len(file.content),
			"sha256":     sha256Hex([]byte(file.content)),
			"content":    file.content,
		})
	}
	return map[string]any{
		"source":          "lesser_body_ptah",
		"state_authority": "Host HostedGenesisSession",
		"operation":       "skill_get",
		"status":          "skill_ready",
		"skill": map[string]any{
			"id":          genesisSkillID,
			"name":        genesisSkillName,
			"version":     genesisSkillVersion(),
			"title":       "Ptah genesis operator skill",
			"description": "Client-native operating playbook for minting the next agent through Body/Ptah's Host-backed agent_genesis_* tools.",
		},
		"bundle_id": genesisSkillBundleID(files),
		"content": map[string]any{
			"mode":        "inline_files",
			"file_count":  len(files),
			"total_bytes": totalBytes,
		},
		"files": entries,
		"provenance": map[string]any{
			"producer":      "equaltoai/lesser-host",
			"host_pr":       fiveBodyHostPR,
			"host_head_sha": fiveBodyHostHeadSHA,
			"consumer":      "equaltoai/lesser-body Ptah instance-plane MCP",
			"host_contract": fiveBodyContractDescriptor(),
		},
		"semantics": map[string]any{
			"read_only":       true,
			"install":         "none",
			"writes":          "none",
			"materialization": "client_decides",
			"instruction":     "Ptah serves skill content only. Body performs no local installation, filesystem write, publish, signing, or cloud/on-chain mutation for this surface; the calling client decides whether and how to materialize or use the files.",
		},
		"guidance": map[string]any{
			"next_tool":   toolAgentGenesisBegin,
			"status":      "skill_ready",
			"instruction": "Read SKILL.md as the operating playbook, then start the Host-backed genesis lane with agent_genesis_begin and follow structuredContent.data.guidance.next_tool on every subsequent call.",
		},
	}
}

func agentGenesisSkillGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolAgentGenesisSkillGet,
		Title:       "Get Ptah genesis operator skill",
		Description: "Fetch the read-only, client-native genesis operator skill bundle before calling agent_genesis_begin. Returns AppTheory MCP structuredContent with a SKILL.md operating playbook, bounded references, and provenance pinned to Host PR #980's exact deployed merge commit. Ptah serves content only: no local installation, filesystem write, publish, or cloud/on-chain mutation. Requires explicit instance owner/operator OAuth authority and read scope.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{},
			"additionalProperties":false
		}`),
		OutputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"data":{
					"type":"object",
					"description":"Deterministic Host-contract-backed genesis operator skill bundle.",
					"properties":{
						"operation":{"type":"string","enum":["skill_get"]},
						"status":{"type":"string","enum":["skill_ready"]},
						"skill":{"type":"object","properties":{
							"id":{"type":"string"},
							"name":{"type":"string"},
							"version":{"type":"string","description":"Derived from soul-five-body-guidance.v2 and the pinned Host contract head."}
						}},
						"bundle_id":{"type":"string","description":"Deterministic sha256 bundle identifier over the skill version and file checksums."},
						"content":{"type":"object","properties":{
							"mode":{"type":"string","enum":["inline_files"]},
							"file_count":{"type":"integer"},
							"total_bytes":{"type":"integer"}
						}},
						"files":{"type":"array","items":{"type":"object","properties":{
							"path":{"type":"string"},
							"media_type":{"type":"string"},
							"bytes":{"type":"integer"},
							"sha256":{"type":"string"},
							"content":{"type":"string"}
						}}},
							"provenance":{"type":"object","description":"Host PR #980 exact deployed merge commit plus Body's mirrored contract versions and checksums."},
						"semantics":{"type":"object","description":"Explicit no-write/no-install semantics: Ptah serves content only; clients decide materialization."},
						"guidance":{"type":"object","properties":{"next_tool":{"type":"string","enum":["agent_genesis_begin"]},"status":{"type":"string"},"instruction":{"type":"string"}}}
					}
				},
				"error":{"type":"object","description":"Structured authorization/input error when isError=true."}
			}
		}`),
	}
}

func (cfg config) handleAgentGenesisSkillGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	actor, result, err := authorizeGenesis(ctx, toolAgentGenesisSkillGet, false)
	if result != nil || err != nil {
		return result, err
	}
	var in struct{}
	if result := decodeGenesisInput(args, &in); result != nil {
		return result, nil
	}
	genesisAudit(ctx, toolAgentGenesisSkillGet, actor, true)
	data := genesisSkillBundleData()
	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{
			Type: "text",
			Text: genesisSkillVisibleText(data),
		}},
		StructuredContent: map[string]any{"data": data},
	}, nil
}

func genesisSkillVisibleText(data map[string]any) string {
	var text strings.Builder
	text.WriteString("# Ptah genesis operator skill bundle\n\n")
	text.WriteString("This is the MCP-visible copy of the genesis operator skill. The identical deterministic bundle remains available at `structuredContent.data` for structured clients.\n\n")
	text.WriteString("## Bundle identity and provenance\n\n")
	text.WriteString("- skill_id: `" + genesisSkillID + "`\n")
	text.WriteString("- skill_name: `" + genesisSkillName + "`\n")
	text.WriteString("- version: `" + genesisSkillVersion() + "`\n")
	if bundleID, _ := data["bundle_id"].(string); bundleID != "" {
		text.WriteString("- bundle_id: `" + bundleID + "`\n")
	}
	text.WriteString("- source: `lesser_body_ptah`\n")
	text.WriteString("- state_authority: Host HostedGenesisSession\n")
	text.WriteString("- producer: equaltoai/lesser-host PR #" + strconv.Itoa(fiveBodyHostPR) + " head `" + fiveBodyHostHeadSHA + "`\n")
	text.WriteString("- semantics: read-only skill exposure; install=none; writes=none; materialization=client_decides. Body/Ptah performs no local installation, filesystem write, publish, signing, cloud mutation, or on-chain mutation for this surface.\n\n")
	text.WriteString("## Operating directives quick check\n\n")
	text.WriteString("- Fetch `" + toolAgentGenesisSkillGet + "`, then call `" + toolAgentGenesisBegin + "`.\n")
	text.WriteString("- Interview the staged five bodies identity → philosophy → discipline → boundaries → soul.\n")
	text.WriteString("- Persist `conversation_id` immediately and after every call.\n")
	text.WriteString("- Follow `structuredContent.data.guidance.next_tool` from every Host-backed response.\n")
	text.WriteString("- `in_progress` is wait/read-only: Do not call `" + toolAgentGenesisAdvance + "` again and do not nudge; wait `poll_after_seconds` when present, then call `" + toolAgentGenesisRead + "`.\n")
	text.WriteString("- Candidate section phase uses a normal owner message through `" + toolAgentGenesisAdvance + "`; Host's provider section tools stay private to its AppTheory MicroVM.\n")
	text.WriteString("- Candidate review requires inspecting the exact lossless `review_text`, selecting one of guidance's six `candidate_actions` entries, and passing only its nested structural `candidate_action` unchanged. affirm has no section; `edit` requires one exact section, supplied by each of the five edit entries, plus owner revision message. Prose has zero authority.\n")
	text.WriteString("- `declaration_ready` goes directly to `" + toolAgentGenesisFinalizePreflight + "`, then `" + toolAgentGenesisFinalize + "`.\n")
	text.WriteString("- Verify with `" + toolAgentGet + "` / `" + toolAgentList + "`.\n")
	text.WriteString("- `restart_soul_bootstrap` means fresh `" + toolAgentGenesisBegin + "`, not `" + toolAgentGenesisRecover + "`; Host's exact terminal hard-cut response for an untyped/stale lane may omit declaration_candidate, and Body never reconstructs it.\n")
	text.WriteString("- `retry_same_step` means wait `retry_after_seconds` when present, then call `" + toolAgentGenesisRecover + "` exactly once on the same lane.\n")
	text.WriteString("- `refresh_state` means call `" + toolAgentGenesisRead + "` exactly once; do not write or poll endlessly.\n")
	text.WriteString("- `operator_action` means stop automatic Genesis tool calls and contact the instance operator; no automatic write is selected.\n")
	text.WriteString("- Host is the sole candidate/state authority; never fabricate genesis state, persist candidate data, expose private provider tools, or recompute candidate truth.\n\n")
	for _, file := range genesisSkillFiles() {
		text.WriteString("## File: `" + file.path + "`\n\n")
		text.WriteString("media_type: `" + file.mediaType + "`  \n")
		text.WriteString("sha256: `" + sha256Hex([]byte(file.content)) + "`\n\n")
		text.WriteString("```markdown\n")
		text.WriteString(file.content)
		if !strings.HasSuffix(file.content, "\n") {
			text.WriteByte('\n')
		}
		text.WriteString("```\n\n")
	}
	return text.String()
}

func fiveBodyGenesisOperatorSkillResource(context.Context) ([]mcpruntime.ResourceContent, error) {
	return fiveBodyResourceJSON(resourceGenesisOperatorSkill, map[string]any{
		"kind":         resourceGenesisOperatorSkill,
		"contract":     fiveBodyContractDescriptor(),
		"tool":         toolAgentGenesisSkillGet,
		"skill_bundle": genesisSkillBundleData(),
	})
}

func genesisSkillMarkdown() string {
	return `---
name: ` + genesisSkillName + `
description: Operate Body/Ptah's Host-backed typed-candidate genesis protocol without creating local declaration state.
version: ` + genesisSkillVersion() + `
---

# Ptah genesis operator skill

You are an LLM client operating Body/Ptah's instance-plane MCP surface to create the next agent. Host
(equaltoai/lesser-host) owns the durable HostedGenesisSession state machine and the ` + fiveBodySchemaVersion + ` /
` + fiveBodyGuidanceVersion + ` contract, typed declaration candidate, and candidate persistence. The five provider
section tools run only inside Host's AppTheory MicroVM. Body uses AppTheory's MCP ToolDef/InputSchema/OutputSchema/
StructuredContent path to validate and relay Host's bounded public projection. Body has no declaration authoring
engine, private provider tools, or candidate store; its only application step is the deterministic hash/schema/template
transform performed during finalize. This skill is read-only.

## Authority

- Use an explicit instance owner/operator OAuth token (account-holder principal). Agent-delegated principals are
  rejected, and ordinary read/write tokens or x402/user payment evidence are never owner authority.
- Every genesis response's ` + "`structuredContent.data`" + ` is the Host-backed truth; treat your own transcript as
  scratch, not state.
- Free-form or canonical affirmation phrases have zero authority. Only Host's structural candidate_action changes
  candidate state.

## Operating sequence

1. **Fetch this skill first.** Call ` + "`" + toolAgentGenesisSkillGet + "`" + ` (or read
   ` + "`ptah://genesis/" + resourceGenesisOperatorSkill + "`" + `) and use this SKILL.md as the operating playbook.
2. **Begin.** Call ` + "`" + toolAgentGenesisBegin + "`" + ` with the managed instance domain and a new local_id.
   Persist the returned registration_id.
3. **Advance candidate sections.** When Host status is ` + "`assistant_turn_ready`" + ` and candidate phase is
   ` + "`section`" + `, call ` + "`" + toolAgentGenesisAdvance + "`" + ` with the next normal owner message for the
   exact current_section. Host invokes its private section tool. Persist conversation_id after every call.
4. **Recover/navigation index when ids are unclear.** If resuming or the id pair is unknown, call ` + "`" + toolAgentGenesisList + "`" + ` with
   the ` + "`agent_id`" + ` first. Follow ` + "`structuredContent.data.recommended_start`" + ` exactly; it includes the next tool and
   arguments. The summary list does not include candidate review bindings; read the selected lane before owner input.
5. **Read and follow guidance.** Poll ` + "`" + toolAgentGenesisRead + "`" + ` for the durable Host projection and
   always follow ` + "`structuredContent.data.guidance.next_tool`" + ` for the next step; do not improvise ordering.
   ` + "`in_progress`" + ` is wait/read-only. Never call advance to nudge; wait poll_after_seconds when present, then read.
6. **Review structurally.** When status is ` + "`assistant_turn_ready`" + ` and candidate phase is ` + "`review`" + `,
   inspect the exact, lossless ` + "`conversation.declaration_candidate.review.review_text`" + `. Use the exact
   candidate_revision, candidate_hash, and review_hash returned in guidance. Select one of the six
   ` + "`guidance.candidate_actions`" + ` entries and pass only its nested candidate_action unchanged to advance:
   ` + "`affirm`" + ` forbids section; ` + "`edit`" + ` requires one exact section, supplied by each of the five edit
   entries, plus an owner revision message.
7. **Preflight, then finalize.** When Host reports ` + "`declaration_ready`" + `, call
   ` + "`" + toolAgentGenesisFinalizePreflight + "`" + ` directly, then, only after preflight succeeds,
   ` + "`" + toolAgentGenesisFinalize + "`" + `. Body deterministically verifies and transforms Host's exact finalized
   candidate before publication, then writes its Host-derived Ptah registry row and published Panonomous v2 soul seed
   after Host succeeds. This application step invokes no MicroVM or model.
8. **Verify.** Verify with ` + "`" + toolAgentGet + "`" + ` / ` + "`" + toolAgentList + "`" + ` for the account-scoped
   registry or merged registry/live view and confirm soul_seed lifecycle_state=published. ` + "`published`" + ` is terminal.

## Failure recovery

- Treat Host's recovery action as an exact enum. Never normalize ` + "`refresh_state`" + `, ` + "`retry_same_step`" + `,
  ` + "`restart_soul_bootstrap`" + `, or ` + "`operator_action`" + ` into a generic retry/wait/contact state.
- ` + "`retry_same_step`" + ` means wait ` + "`retry_after_seconds`" + ` when present, then call ` + "`" + toolAgentGenesisRecover + "`" + ` exactly once
  with the same registration_id/conversation_id. Keep ` + "`fresh_lane=false`" + `; do not start a new lane or poll read instead.
- ` + "`restart_soul_bootstrap`" + ` means fresh ` + "`" + toolAgentGenesisBegin + "`" + `, not ` + "`" + toolAgentGenesisRecover + "`" + `.
  Recover is explicitly forbidden for that action. Host's exact terminal hard-cut projection for an untyped/stale lane
  may omit declaration_candidate; do not reject it, extract old transcript state, or reconstruct a candidate.
- ` + "`refresh_state`" + ` means call ` + "`" + toolAgentGenesisRead + "`" + ` exactly once, then follow the newly returned Host
  status/recovery action. Do not write and do not turn refresh into an endless read loop.
- ` + "`operator_action`" + ` means stop automatic Genesis tool calls and contact the instance operator with the safe Host reason
  when present. Do not choose an automatic write or continue polling.
- Start with ` + "`" + toolAgentGenesisList + "`" + ` whenever ids are unclear. It returns Host-backed summaries,
  ` + "`recommended_start`" + `, exact next-tool arguments, and failed-lane instructions. For ` + "`failed`" + ` summaries,
  read first to load typed ` + "`failure.recovery`" + `; do not guess recover vs restart from list output alone.

## Invariants

- Host HostedGenesisSession is the sole candidate/state authority. Body does not infer affirmation from message text,
  recompute candidate truth, persist candidate state, or expose provider section tools.
- The full owner review is the exact review_text, up to 65,536 characters. Never substitute the bounded latest
  transcript message.
- Only Host's bounded public declaration_candidate projection may be relayed; canonical candidate internals and
  provider payloads stay private to Host.
- Consult references/genesis-guidance-map.md for the resource, prompt, and state-to-next-tool map.
`
}

func genesisSkillGuidanceMap() string {
	return `# Genesis guidance map

Bounded reference for LLM clients operating Body/Ptah Host-backed genesis. Contract:
` + fiveBodySchemaVersion + ` / ` + fiveBodyGuidanceVersion + ` pinned at equaltoai/lesser-host PR #` +
		strconv.Itoa(fiveBodyHostPR) + ` deployed merge commit ` + fiveBodyHostHeadSHA + `.

## Ptah resources

- ` + fiveBodyResourceURI(resourceSoulSchemaV2) + ` — mirrored Host schema, golden example, and PR #980 provenance/checksums.
- ` + fiveBodyResourceURI(resourceGenesisInterviewGuide) + ` — staged five-body interview and structural review guide.
- ` + fiveBodyResourceURI(resourceAgentSideGenesisPlaybook) + ` — operator/client playbook for the agent_genesis_*
  tools.
- ` + fiveBodyResourceURI(resourceGenesisRubric) + ` — review rubric, refusal floor, and Host validation codes.
- ` + fiveBodyResourceURI(resourceGenesisOperatorSkill) + ` — this skill bundle, also served by ` +
		toolAgentGenesisSkillGet + `.

## Ptah prompts

- ` + promptDraftGenesisTurn + ` — draft the next owner/operator interview turn.
- ` + promptReviewGenesisCandidate + ` — inspect exact Host review_text and prepare a structural affirm/edit action.

## State to next tool

| Observed state | Next tool |
| --- | --- |
| before any genesis call | ` + toolAgentGenesisSkillGet + ` |
| skill fetched | ` + toolAgentGenesisBegin + ` |
| resuming / ids unclear / multiple lanes | ` + toolAgentGenesisList + ` (then follow recommended_start) |
| begin success | ` + toolAgentGenesisAdvance + ` (persist conversation_id) |
| assistant_turn_ready + candidate phase section | ` + toolAgentGenesisAdvance + ` with normal owner message |
| assistant_turn_ready + candidate phase review | inspect exact review_text, then ` + toolAgentGenesisAdvance + ` with structural candidate_action and exact bindings |
| in_progress | ` + toolAgentGenesisRead + ` (wait-only; never ` + toolAgentGenesisAdvance + ` to nudge) |
| declaration_ready | ` + toolAgentGenesisFinalizePreflight + ` directly |
| preflight-ready | ` + toolAgentGenesisFinalize + ` |
| finalize success / published | ` + toolAgentGet + ` or ` + toolAgentList + `; terminal |
| failure.recovery.action=retry_same_step | ` + toolAgentGenesisRecover + ` exactly once after retry_after_seconds when present; same lane |
| failure.recovery.action=restart_soul_bootstrap | ` + toolAgentGenesisBegin + ` (fresh lane; never ` + toolAgentGenesisRecover + `) |
| failure.recovery.action=refresh_state | ` + toolAgentGenesisRead + ` exactly once; no write or endless read loop |
| failure.recovery.action=operator_action | no automatic next tool; stop and contact the instance operator |

Always prefer the live ` + "`structuredContent.data.guidance.next_tool`" + ` from the latest Host-backed response
over this static table. For ` + "`in_progress`" + ` processing guidance, use any
` + "`poll_after_seconds`" + ` / ` + "`expected_wait_seconds`" + ` value as the delay and then call ` + "`" + toolAgentGenesisRead + "`" + `.
For ` + "`retry_same_step`" + ` guidance, the same delay fields precede exactly one ` + "`" + toolAgentGenesisRecover + "`" + ` call instead.
Do not call ` + "`" + toolAgentGenesisAdvance + "`" + ` until Host reports assistant_turn_ready. In review phase,
candidate_action is mandatory and free-form phrases have zero authority.
`
}
