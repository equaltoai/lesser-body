package ptahserver

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
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
// guidance version and the pinned Host contract head. A Host PR #928 refresh
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
		Description: "Fetch the read-only, client-native genesis operator skill bundle before calling agent_genesis_begin. Returns deterministic structuredContent with a SKILL.md operating playbook, bounded references, and provenance pinned to the mirrored Host PR #928 five-body contract. Ptah serves content only: no local installation, no filesystem write, no publish, and no cloud/on-chain mutation. Requires explicit instance owner/operator OAuth authority and read scope.",
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
						"provenance":{"type":"object","description":"Host PR #928 head plus Body's mirrored contract versions and checksums."},
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
	text.WriteString("- `in_progress` and `declaration_extraction_pending` are wait-only: Do not call `" + toolAgentGenesisAdvance + "` again and do not nudge; wait `poll_after_seconds` when present, then call `" + toolAgentGenesisRead + "`.\n")
	text.WriteString("- Call `" + toolAgentGenesisComplete + "`; never submit declarations as source of truth.\n")
	text.WriteString("- Call `" + toolAgentGenesisFinalizePreflight + "` then `" + toolAgentGenesisFinalize + "`.\n")
	text.WriteString("- Verify with `" + toolAgentGet + "` / `" + toolAgentList + "`.\n")
	text.WriteString("- `restart_soul_bootstrap` means fresh `" + toolAgentGenesisBegin + "`, not `" + toolAgentGenesisRecover + "`.\n")
	text.WriteString("- Host is source of truth; never fabricate genesis state, declarations, model allowlists, or directory entries.\n\n")
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
description: Operate Body/Ptah's Host-backed agent_genesis_* tools to mint the next agent through lesser-host's durable five-body genesis flow.
version: ` + genesisSkillVersion() + `
---

# Ptah genesis operator skill

You are an LLM client operating Body/Ptah's instance-plane MCP surface to create the next agent. Host
(equaltoai/lesser-host) owns the durable HostedGenesisSession state machine and the ` + fiveBodySchemaVersion + ` /
` + fiveBodyGuidanceVersion + ` contract. Body is a guidance and registry consumer: it relays Host state and never
fabricates business state. This skill is served read-only; Ptah performs no installation, filesystem write, publish,
or cloud/on-chain mutation. You decide whether and how to materialize this content.

## Authority

- Use an explicit instance owner/operator OAuth token (account-holder principal). Agent-delegated principals are
  rejected, and ordinary read/write tokens or x402/user payment evidence are never owner authority.
- Every genesis response's ` + "`structuredContent.data`" + ` is the Host-backed truth; treat your own transcript as
  scratch, not state.

## Operating sequence

1. **Fetch this skill first.** Call ` + "`" + toolAgentGenesisSkillGet + "`" + ` (or read
   ` + "`ptah://genesis/" + resourceGenesisOperatorSkill + "`" + `) and use this SKILL.md as the operating playbook.
2. **Begin.** Call ` + "`" + toolAgentGenesisBegin + "`" + ` with the managed instance domain and a new local_id.
   Persist the returned registration_id.
3. **Interview the five bodies.** Call ` + "`" + toolAgentGenesisAdvance + "`" + ` to run Host's staged interview:
   staged five bodies identity → philosophy → discipline → boundaries → soul, with capabilities and transparency as
   satellites. Persist ` + "`conversation_id`" + ` immediately when the first advance returns it and after every call;
   also keep the ` + "`registration_id`" + `/` + "`conversation_id`" + ` pair together.
4. **Recover/navigation index when ids are unclear.** If you are resuming, see multiple stuck/broken lanes, or do not
   know the current ` + "`registration_id`" + `/` + "`conversation_id`" + ` pair, call ` + "`" + toolAgentGenesisList + "`" + ` with
   the ` + "`agent_id`" + ` first. Follow ` + "`structuredContent.data.recommended_start`" + ` exactly; it includes the next tool and
   arguments. The list is Host summary-only and does not expose transcripts or declarations.
5. **Read and follow guidance.** Poll ` + "`" + toolAgentGenesisRead + "`" + ` for the durable Host projection and
   always follow ` + "`structuredContent.data.guidance.next_tool`" + ` for the next step; do not improvise ordering.
   ` + "`in_progress`" + ` and ` + "`declaration_extraction_pending`" + ` are Host-processing states, not owner-input
   states. When status is one of those states or guidance has ` + "`wait=true`" + `, do not treat it as owner input.
   Do not call ` + "`" + toolAgentGenesisAdvance + "`" + ` again and do not nudge while Host is processing; wait for ` + "`poll_after_seconds`" + ` when present, then call ` + "`" + toolAgentGenesisRead + "`" + `. Only advance after Host reports
   ` + "`assistant_turn_ready`" + `, ` + "`awaiting_owner`" + `, or ` + "`needs_owner_turn`" + `.
6. **Complete.** When Host reports declaration readiness, call ` + "`" + toolAgentGenesisComplete + "`" + `. Host
   extracts and validates its own produced declarations; never submit declarations as source of truth.
7. **Preflight, then finalize.** Call ` + "`" + toolAgentGenesisFinalizePreflight + "`" + ` then, only after Host
   readiness, ` + "`" + toolAgentGenesisFinalize + "`" + `. Body then writes its Host-derived Ptah registry row.
8. **Verify.** Verify with ` + "`" + toolAgentGet + "`" + ` / ` + "`" + toolAgentList + "`" + ` for the account-scoped
   registry or merged registry/live view.

## Failure recovery

- If a response carries ` + "`failure.recovery.action=restart_soul_bootstrap`" + `, ` + "`restart_soul_bootstrap`" + `
  means fresh ` + "`" + toolAgentGenesisBegin + "`" + `, not ` + "`" + toolAgentGenesisRecover + "`" + `. Recover is only
  for the other typed recoverable states Host names.
- Start with ` + "`" + toolAgentGenesisList + "`" + ` whenever ids are unclear. It returns Host-backed summaries,
  ` + "`recommended_start`" + `, exact next-tool arguments, and failed-lane instructions. For ` + "`failed`" + ` summaries,
  read first to load typed ` + "`failure.recovery`" + `; do not guess recover vs restart from list output alone.

## Invariants

- Host is source of truth for genesis state, produced declarations, and validation; Body only relays and registers
  Host-derived results.
- Never fabricate conversation state, declarations, model allowlists, or directory entries.
- Before final acceptance Host uses the canonical affirmation exactly: ` + canonicalGenesisAffirmation() + `
- Consult references/genesis-guidance-map.md for the resource, prompt, and state-to-next-tool map.
`
}

func genesisSkillGuidanceMap() string {
	return `# Genesis guidance map

Bounded reference for LLM clients operating Body/Ptah Host-backed genesis. Contract:
` + fiveBodySchemaVersion + ` / ` + fiveBodyGuidanceVersion + ` pinned at equaltoai/lesser-host PR #` +
		strconv.Itoa(fiveBodyHostPR) + ` head ` + fiveBodyHostHeadSHA + `.

## Ptah resources

- ` + fiveBodyResourceURI(resourceSoulSchemaV2) + ` — mirrored Host schema, golden example, and contract document.
- ` + fiveBodyResourceURI(resourceGenesisInterviewGuide) + ` — staged five-body interview guide with satellites and
  the canonical affirmation.
- ` + fiveBodyResourceURI(resourceAgentSideGenesisPlaybook) + ` — operator/client playbook for the agent_genesis_*
  tools.
- ` + fiveBodyResourceURI(resourceGenesisRubric) + ` — review rubric, refusal floor, and Host validation codes.
- ` + fiveBodyResourceURI(resourceGenesisOperatorSkill) + ` — this skill bundle, also served by ` +
		toolAgentGenesisSkillGet + `.

## Ptah prompts

- ` + promptDraftGenesisTurn + ` — draft the next owner/operator interview turn.
- ` + promptReviewSoulDraft + ` — review a soul draft against the Host-owned rubric.

## State to next tool

| Observed state | Next tool |
| --- | --- |
| before any genesis call | ` + toolAgentGenesisSkillGet + ` |
| skill fetched | ` + toolAgentGenesisBegin + ` |
| resuming / ids unclear / multiple lanes | ` + toolAgentGenesisList + ` (then follow recommended_start) |
| begin success | ` + toolAgentGenesisAdvance + ` (persist conversation_id) |
| assistant_turn_ready / awaiting_owner / needs_owner_turn | ` + toolAgentGenesisAdvance + ` |
| in_progress / declaration_extraction_pending | ` + toolAgentGenesisRead + ` (wait-only; never ` + toolAgentGenesisAdvance + ` to nudge) |
| declaration_ready | ` + toolAgentGenesisComplete + ` |
| complete success / finalization-ready | ` + toolAgentGenesisFinalizePreflight + ` |
| preflight-ready | ` + toolAgentGenesisFinalize + ` |
| finalize success / published / finalized | ` + toolAgentGet + ` or ` + toolAgentList + ` |
| failure.recovery.action=restart_soul_bootstrap | ` + toolAgentGenesisBegin + ` (fresh lane; never ` + toolAgentGenesisRecover + `) |
| other typed recoverable failure | ` + toolAgentGenesisRecover + ` |

Always prefer the live ` + "`structuredContent.data.guidance.next_tool`" + ` from the latest Host-backed response
over this static table. If live guidance has ` + "`wait=true`" + `, use any ` + "`poll_after_seconds`" + ` /
` + "`expected_wait_seconds`" + ` value as the expected delay, call ` + "`" + toolAgentGenesisRead + "`" + ` after that
delay, and do not call ` + "`" + toolAgentGenesisAdvance + "`" + ` until Host reports an owner-input state.
`
}
