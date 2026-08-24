package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/equaltoai/lesser-body/internal/cmsapi"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

const (
	mediaDefaultBudgetBytes = 24000

	// Envelope states (the theory-report editorial-media lifecycle vocabulary).
	// Each state is derived strictly from Lesser-authoritative fields; the
	// mapping table is pinned in TestMediaEnvelopeStateMapping.
	mediaStateReceived            = "received"              // grant MINTED / unknown upstream state
	mediaStateProcessing          = "processing"            // EditorialMediaState.PROCESSING
	mediaStateReadyInternal       = "ready_internal"        // grant USED / EditorialMediaState.READY, internal
	mediaStateAttached            = "attached"              // bound to a draft usage
	mediaStateAwaitingReview      = "awaiting_review"       // bound + active reviewers, no current verdict
	// Lesser emits Stale as the logical negation of Current
	// (cms_converters.go: Current: isCurrent, Stale: !isCurrent), so a
	// non-current verdict is ALWAYS stale; there is no producible
	// approved-but-superseded lane distinct from stale.
	mediaStateStale               = "stale"                 // verdict marked stale (M2 contentHash mismatch)
	mediaStatePublished           = "published"             // publishedUrl/publishedAt present
	mediaStateRejectedUnsupported = "rejected_unsupported"  // REJECTED / FAILED_DIGEST
	mediaStateUnavailableRemoved  = "unavailable_removed"   // MISSING/WITHDRAWN/SUPERSEDED/UNAVAILABLE
	mediaStateExpired             = "expired"               // upload grant EXPIRED (fresh mint required)
	mediaStateMissing             = "missing"               // binding has no servable asset

	// Envelope error codes for the upload-grant / editorial-media lanes.
	mediaErrorGrantExpired       = "media_grant_expired"
	mediaErrorDigestMismatch     = "media_digest_mismatch"
	mediaErrorGrantNotMinted     = "media_grant_not_minted"
	mediaErrorUploadNotFinalized = "media_upload_not_finalized"
	mediaErrorMintInvalid        = "media_mint_invalid"
	mediaErrorRejected           = "media_rejected"
	mediaErrorNotFound           = "media_not_found"

	// Lesser's mint rule: editorial admission is image/* only, and the declared
	// content type must be canonical (no parameters).
	mediaMaxMintSizeBytes = 50 * 1024 * 1024
)

var mediaSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func registerMediaTools(r *mcpruntime.ToolRegistry) error {
	for _, tool := range []struct {
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: uploadGrantMintDef(), Handler: handleUploadGrantMint},
		{Def: uploadFinalizeDef(), Handler: handleUploadFinalize},
		{Def: mediaStateDef(), Handler: handleMediaState},
		{Def: mediaReadDef(), Handler: handleMediaRead},
		{Def: draftMediaAttachDef(), Handler: handleDraftMediaAttach},
		{Def: draftMediaDetachDef(), Handler: handleDraftMediaDetach},
		{Def: draftMediaReorderDef(), Handler: handleDraftMediaReorder},
	} {
		if err := registerTool(r, tool.Def, tool.Handler); err != nil {
			return err
		}
	}
	return nil
}

func uploadGrantMintDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "upload_grant_mint",
		Description:  "Request a one-time, hash-bound, presigned-companion upload grant from Lesser for image/* editorial media. Step 1 of the two-step upload contract: after this call, PUT the EXACT declared bytes to presigned_url out-of-band, then call upload_finalize. The grant expires at expires_at (15 minutes); an expired grant requires a fresh mint. Lesser owns mint admission (image/* only, no content-type parameters, bounded size, 64-lowercase-hex sha256).",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: uploadGrantMintOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content_type":{"type":"string","description":"Declared media type; image/* only (Lesser's editorial rule), canonical form without parameters, e.g. image/png."},
				"max_size_bytes":{"type":"integer","minimum":1,"maximum":52428800,"description":"Declared size cap in bytes; finalize fails closed beyond it."},
				"sha256":{"type":"string","pattern":"^[0-9a-f]{64}$","description":"Hex-encoded sha256 of the exact bytes you will PUT."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["content_type","max_size_bytes","sha256"],
			"additionalProperties":false
		}`),
	}
}

func uploadFinalizeDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "upload_finalize",
		Description:  "Step 2 of the two-step upload contract: ask Lesser to verify the uploaded bytes against the grant (digest, size cap, SVG safety) and admit the editorial media record. Call only AFTER the client PUT the exact declared bytes to the minted presigned_url. Finalize is one-time: success consumes the grant to USED; a digest failure consumes it to FAILED_DIGEST. An expired grant or a failed verification requires a fresh upload_grant_mint.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: uploadFinalizeOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"grant_id":{"type":"string","description":"Upload grant id returned by upload_grant_mint."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["grant_id"],
			"additionalProperties":false
		}`),
	}
}

func mediaStateDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "media_state",
		Description:  "Inspect the editorial-media lifecycle of one asset. Provide draft_id+media_id to inspect a draft-bound asset's state, provenance summary, review staleness (verdict contentHash vs draft contentHash), and BOUND_MEDIA_* publish blocking reasons (owner or active reviewer); provide grant_id to inspect an upload grant's lifecycle (MINTED/USED/FAILED_DIGEST/EXPIRED). Lesser is the state authority; Body transports it verbatim. State reads include the per-usage short-lived access URL (accessUrl/accessExpiresAt) that Lesser's draftReview projection mints for authorized callers (draft owner or active reviewer) — it is re-minted on each read and expires quickly, not a stable cache-busting URL. For an explicit grant-scoped reviewer read of one exact asset, use media_read.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: mediaStateOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Lesser CMS draft id when inspecting a draft-bound asset."},
				"media_id":{"type":"string","description":"Lesser media id bound to draft_id when inspecting a draft-bound asset."},
				"grant_id":{"type":"string","description":"Upload grant id when inspecting an upload grant lifecycle."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"additionalProperties":false
		}`),
	}
}

func mediaReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "media_read",
		Description:  "Grant-scoped reviewer read: mint Lesser's short-lived exact-asset URL for one media_id bound to an authorized draft (owner or active reviewer). The URL is issued for the EXACT bound asset and expires at expires_at; it is per-request, not a stable cache-busting URL.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: mediaReadOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Lesser CMS draft id authorizing the read (owner or active reviewer)."},
				"media_id":{"type":"string","description":"Lesser media id bound to draft_id."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["draft_id","media_id"],
			"additionalProperties":false
		}`),
	}
}

func draftMediaAttachDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "draft_media_attach",
		Description:  "Bind one editorial asset to an owner-scoped draft with a role (HERO/INLINE/SOCIAL_CARD) and optional per-usage caption/credit/alt/focus through Lesser's setDraftEditorialMedia contract (full-list replace; Body applies the delta and transports the resulting Lesser-authoritative binding list). Re-attaching an already-bound media_id replaces its usage. Lesser validates ownership and media state; a rejected/unavailable asset stays unattached.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: draftMediaBindingsOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Lesser CMS draft id owned by the authenticated actor."},
				"media_id":{"type":"string","description":"Lesser media id (from upload_finalize) to bind."},
				"role":{"type":"string","enum":["HERO","INLINE","SOCIAL_CARD"],"description":"Lesser's canonical editorial media role."},
				"inline_position":{"type":"integer","minimum":0,"description":"Zero-based insertion point; INLINE only. Defaults to the first free inline position (appends at the end when the sequence is dense)."},
				"caption":{"type":"string","description":"Optional per-usage caption."},
				"credit":{"type":"string","description":"Optional reader-facing attribution line."},
				"alt":{"type":"string","description":"Optional per-usage alt text; effectiveAltText falls back to the media-global description."},
				"focus":{"type":"string","description":"Optional focus point hint."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["draft_id","media_id","role"],
			"additionalProperties":false
		}`),
	}
}

func draftMediaDetachDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "draft_media_detach",
		Description:  "Remove one editorial asset from an owner-scoped draft's ordered media association through Lesser's setDraftEditorialMedia contract. Detaching an unbound media_id is a no-op that returns the unchanged binding list.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: draftMediaBindingsOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Lesser CMS draft id owned by the authenticated actor."},
				"media_id":{"type":"string","description":"Lesser media id to unbind."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["draft_id","media_id"],
			"additionalProperties":false
		}`),
	}
}

func draftMediaReorderDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "draft_media_reorder",
		Description:  "Replace the complete ordered editorial-media association of an owner-scoped draft with media_ids in the requested order through Lesser's setDraftEditorialMedia contract. media_ids must name exactly the currently bound assets; INLINE usages are re-indexed to their position in the new order.",
		Annotations:  additiveMutationToolAnnotations(),
		OutputSchema: draftMediaBindingsOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"draft_id":{"type":"string","description":"Lesser CMS draft id owned by the authenticated actor."},
				"media_ids":{"type":"array","items":{"type":"string"},"minItems":1,"description":"The full desired order of the currently bound media ids."},
				"max_output_bytes":{"type":"integer","minimum":0,"description":"Optional MCP response budget. Zero uses the 24000-byte default."}
			},
			"required":["draft_id","media_ids"],
			"additionalProperties":false
		}`),
	}
}

func mediaCMS(ctx context.Context) (*cmsapi.Client, error) {
	_ = ctx
	return cmsapi.Default()
}

func mediaOutputBudget(requested int) int {
	if requested > 0 {
		return requested
	}
	return mediaDefaultBudgetBytes
}

// handleUploadGrantMint transports the mint to Lesser and returns the
// presigned-companion PUT contract explicitly: the URL, grant id, and expiry
// plus the two-step ordering.
func handleUploadGrantMint(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		ContentType    string `json:"content_type"`
		MaxSizeBytes   int    `json:"max_size_bytes"`
		SHA256         string `json:"sha256"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.ContentType = strings.ToLower(strings.TrimSpace(in.ContentType))
	in.SHA256 = strings.ToLower(strings.TrimSpace(in.SHA256))
	if in.ContentType == "" {
		return nil, invalidParams("content_type is required")
	}
	if !strings.HasPrefix(in.ContentType, "image/") {
		return nil, invalidParams("content_type must be image/* (Lesser's editorial rule)")
	}
	if strings.Contains(in.ContentType, ";") || strings.Contains(in.ContentType, " ") {
		return nil, invalidParams("content_type must be canonical, without parameters")
	}
	if in.MaxSizeBytes <= 0 || in.MaxSizeBytes > mediaMaxMintSizeBytes {
		return nil, invalidParams(fmt.Sprintf("max_size_bytes must be between 1 and %d", mediaMaxMintSizeBytes))
	}
	if !mediaSHA256Pattern.MatchString(in.SHA256) {
		return nil, invalidParams("sha256 must be 64 lowercase hex characters")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := mediaCMS(ctx)
	if err != nil {
		return nil, err
	}
	grant, err := client.MintUploadGrant(ctx, token, in.ContentType, in.MaxSizeBytes, in.SHA256)
	if err != nil {
		return mediaToolResultFromError("upload_grant_mint", err)
	}
	return mediaGrantMintResult(grant, mediaOutputBudget(in.MaxOutputBytes))
}

// handleUploadFinalize transports the one-time finalize to Lesser and renders
// the admitted media record or the classified failure lane.
func handleUploadFinalize(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		GrantID        string `json:"grant_id"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.GrantID = strings.TrimSpace(in.GrantID)
	if in.GrantID == "" {
		return nil, invalidParams("grant_id is required")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := mediaCMS(ctx)
	if err != nil {
		return nil, err
	}
	result, err := client.FinalizeUploadGrant(ctx, token, in.GrantID)
	if err != nil {
		return mediaToolResultFromError("upload_finalize", err)
	}
	return mediaFinalizeResult(result, mediaOutputBudget(in.MaxOutputBytes))
}

// handleMediaState inspects either a draft-bound asset or an upload grant.
func handleMediaState(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string `json:"draft_id,omitempty"`
		MediaID        string `json:"media_id,omitempty"`
		GrantID        string `json:"grant_id,omitempty"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if len(strings.TrimSpace(string(args))) > 0 && strings.TrimSpace(string(args)) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, invalidParams("invalid args: " + err.Error())
		}
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	in.MediaID = strings.TrimSpace(in.MediaID)
	in.GrantID = strings.TrimSpace(in.GrantID)
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}
	if in.GrantID != "" && (in.DraftID != "" || in.MediaID != "") {
		return nil, invalidParams("grant_id and draft_id/media_id are mutually exclusive modes")
	}
	if in.GrantID == "" && (in.DraftID == "" || in.MediaID == "") {
		return nil, invalidParams("provide grant_id, or draft_id with media_id")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := mediaCMS(ctx)
	if err != nil {
		return nil, err
	}
	budget := mediaOutputBudget(in.MaxOutputBytes)
	if in.GrantID != "" {
		grant, err := client.GetUploadGrant(ctx, token, in.GrantID)
		if err != nil {
			return mediaToolResultFromError("media_state", err)
		}
		return mediaGrantStateResult(grant, budget)
	}
	state, err := client.ReadDraftEditorialMedia(ctx, token, in.DraftID)
	if err != nil {
		return mediaToolResultFromError("media_state", err)
	}
	return mediaBindingStateResult(state, in.MediaID, budget)
}

// handleMediaRead mints the grant-scoped reviewer read for one exact bound
// asset.
func handleMediaRead(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string `json:"draft_id"`
		MediaID        string `json:"media_id"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	in.MediaID = strings.TrimSpace(in.MediaID)
	if in.DraftID == "" {
		return nil, invalidParams("draft_id is required")
	}
	if in.MediaID == "" {
		return nil, invalidParams("media_id is required")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := mediaCMS(ctx)
	if err != nil {
		return nil, err
	}
	access, err := client.ReadDraftEditorialMediaAccess(ctx, token, in.DraftID, in.MediaID)
	if err != nil {
		return mediaToolResultFromError("media_read", err)
	}
	return mediaReadResult(access, mediaOutputBudget(in.MaxOutputBytes))
}

// handleDraftMediaAttach binds one asset to a draft through the full-list
// setDraftEditorialMedia contract.
func handleDraftMediaAttach(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string  `json:"draft_id"`
		MediaID        string  `json:"media_id"`
		Role           string  `json:"role"`
		InlinePosition *int    `json:"inline_position,omitempty"`
		Caption        *string `json:"caption,omitempty"`
		Credit         *string `json:"credit,omitempty"`
		Alt            *string `json:"alt,omitempty"`
		Focus          *string `json:"focus,omitempty"`
		MaxOutputBytes int     `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	in.MediaID = strings.TrimSpace(in.MediaID)
	in.Role = strings.ToUpper(strings.TrimSpace(in.Role))
	if in.DraftID == "" {
		return nil, invalidParams("draft_id is required")
	}
	if in.MediaID == "" {
		return nil, invalidParams("media_id is required")
	}
	if !validMediaRole(in.Role) {
		return nil, invalidParams("role must be HERO, INLINE, or SOCIAL_CARD")
	}
	if in.InlinePosition != nil && (*in.InlinePosition < 0) {
		return nil, invalidParams("inline_position must not be negative")
	}
	if in.InlinePosition != nil && in.Role != cmsapi.EditorialMediaRoleInline {
		return nil, invalidParams("inline_position is only valid for INLINE role")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := mediaCMS(ctx)
	if err != nil {
		return nil, err
	}
	state, err := client.ReadDraftEditorialMedia(ctx, token, in.DraftID)
	if err != nil {
		return mediaToolResultFromError("draft_media_attach", err)
	}
	usages := currentUsageInputs(state)
	position := in.InlinePosition
	if in.Role == cmsapi.EditorialMediaRoleInline && position == nil {
		// Lesser requires unique inline positions (not contiguity), and detach
		// preserves gaps, so counting usages can collide with an occupied slot.
		// Fill the first free position: it is always unoccupied, keeps the
		// sequence dense after middle detaches, and is deterministic.
		next := firstFreeInlinePosition(usages)
		position = &next
	}
	usage := cmsapi.MediaUsageInput{
		MediaID:        in.MediaID,
		Role:           in.Role,
		InlinePosition: position,
		Caption:        in.Caption,
		CreditLine:     in.Credit,
		AltText:        in.Alt,
		Focus:          in.Focus,
	}
	replaced := false
	for i := range usages {
		if usages[i].MediaID == in.MediaID {
			// Replace on MediaID regardless of role: lesser's contract is one
			// usage per media_id (it rejects duplicate media ids), so a
			// role-switch re-attach must replace the existing usage entirely
			// rather than append a duplicate.
			usages[i] = usage
			replaced = true
			break
		}
	}
	if !replaced {
		usages = append(usages, usage)
	}
	updated, err := client.SetDraftEditorialMedia(ctx, token, in.DraftID, usages)
	if err != nil {
		return mediaToolResultFromError("draft_media_attach", err)
	}
	return mediaBindingsResult("draft_media_attach", "attached", updated, mediaOutputBudget(in.MaxOutputBytes))
}

// handleDraftMediaDetach removes one asset from a draft's media association.
func handleDraftMediaDetach(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string `json:"draft_id"`
		MediaID        string `json:"media_id"`
		MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	in.MediaID = strings.TrimSpace(in.MediaID)
	if in.DraftID == "" {
		return nil, invalidParams("draft_id is required")
	}
	if in.MediaID == "" {
		return nil, invalidParams("media_id is required")
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := mediaCMS(ctx)
	if err != nil {
		return nil, err
	}
	state, err := client.ReadDraftEditorialMedia(ctx, token, in.DraftID)
	if err != nil {
		return mediaToolResultFromError("draft_media_detach", err)
	}
	usages := currentUsageInputs(state)
	filtered := usages[:0]
	for _, usage := range usages {
		if usage.MediaID != in.MediaID {
			filtered = append(filtered, usage)
		}
	}
	updated, err := client.SetDraftEditorialMedia(ctx, token, in.DraftID, filtered)
	if err != nil {
		return mediaToolResultFromError("draft_media_detach", err)
	}
	return mediaBindingsResult("draft_media_detach", "detached", updated, mediaOutputBudget(in.MaxOutputBytes))
}

// handleDraftMediaReorder replaces the full ordered association with the
// requested order.
func handleDraftMediaReorder(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in struct {
		DraftID        string   `json:"draft_id"`
		MediaIDs       []string `json:"media_ids"`
		MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.DraftID = strings.TrimSpace(in.DraftID)
	if in.DraftID == "" {
		return nil, invalidParams("draft_id is required")
	}
	if len(in.MediaIDs) == 0 {
		return nil, invalidParams("media_ids must name at least one bound asset")
	}
	seen := make(map[string]struct{}, len(in.MediaIDs))
	for i := range in.MediaIDs {
		in.MediaIDs[i] = strings.TrimSpace(in.MediaIDs[i])
		if in.MediaIDs[i] == "" {
			return nil, invalidParams("media_ids must not contain empty ids")
		}
		if _, dup := seen[in.MediaIDs[i]]; dup {
			return nil, invalidParams("media_ids must not contain duplicates")
		}
		seen[in.MediaIDs[i]] = struct{}{}
	}
	if in.MaxOutputBytes < 0 {
		return nil, invalidParams("max_output_bytes must not be negative")
	}

	ctx, token, err := requireActAsScopedOAuthBearer(ctx)
	if err != nil {
		return authToolResultFromError(err)
	}
	client, err := mediaCMS(ctx)
	if err != nil {
		return nil, err
	}
	state, err := client.ReadDraftEditorialMedia(ctx, token, in.DraftID)
	if err != nil {
		return mediaToolResultFromError("draft_media_reorder", err)
	}
	current := currentUsageInputs(state)
	bound := make(map[string]struct{}, len(current))
	for _, usage := range current {
		bound[usage.MediaID] = struct{}{}
	}
	if len(seen) != len(bound) {
		return nil, invalidParams("media_ids must name exactly the currently bound assets")
	}
	for mediaID := range seen {
		if _, ok := bound[mediaID]; !ok {
			return nil, invalidParams("media_id " + mediaID + " is not bound to this draft")
		}
	}

	byMediaID := make(map[string]cmsapi.MediaUsageInput, len(current))
	for _, usage := range current {
		byMediaID[usage.MediaID] = usage
	}
	reordered := make([]cmsapi.MediaUsageInput, 0, len(in.MediaIDs))
	for inlineIndex, mediaID := range in.MediaIDs {
		usage := byMediaID[mediaID]
		if usage.Role == cmsapi.EditorialMediaRoleInline {
			position := inlineIndex
			usage.InlinePosition = &position
		} else {
			usage.InlinePosition = nil
		}
		reordered = append(reordered, usage)
	}
	updated, err := client.SetDraftEditorialMedia(ctx, token, in.DraftID, reordered)
	if err != nil {
		return mediaToolResultFromError("draft_media_reorder", err)
	}
	return mediaBindingsResult("draft_media_reorder", "reordered", updated, mediaOutputBudget(in.MaxOutputBytes))
}

func validMediaRole(role string) bool {
	switch role {
	case cmsapi.EditorialMediaRoleHero, cmsapi.EditorialMediaRoleInline, cmsapi.EditorialMediaRoleSocialCard:
		return true
	default:
		return false
	}
}

func currentUsageInputs(state *cmsapi.DraftMediaState) []cmsapi.MediaUsageInput {
	if state == nil {
		return nil
	}
	usages := make([]cmsapi.MediaUsageInput, 0, len(state.EditorialMedia))
	for _, usage := range state.EditorialMedia {
		usages = append(usages, cmsapi.MediaUsageInput{
			MediaID:        usage.MediaID,
			Role:           usage.Role,
			InlinePosition: usage.InlinePosition,
			Caption:        usage.Caption,
			CreditLine:     usage.CreditLine,
			AltText:        usage.AltText,
			Focus:          usage.Focus,
		})
	}
	return usages
}

// firstFreeInlinePosition returns the smallest inline position not currently
// occupied by an inline usage. Non-inline usages (HERO/SOCIAL_CARD) carry no
// position and never occupy a slot.
func firstFreeInlinePosition(usages []cmsapi.MediaUsageInput) int {
	occupied := make(map[int]struct{}, len(usages))
	for _, usage := range usages {
		if usage.Role != cmsapi.EditorialMediaRoleInline || usage.InlinePosition == nil {
			continue
		}
		occupied[*usage.InlinePosition] = struct{}{}
	}
	for position := 0; ; position++ {
		if _, taken := occupied[position]; !taken {
			return position
		}
	}
}

// mediaEnvelopeStateForGrant maps an upload grant's lifecycle onto the envelope
// vocabulary.
func mediaEnvelopeStateForGrant(grant *cmsapi.UploadGrant) string {
	if grant == nil {
		return mediaStateReceived
	}
	switch grant.Status {
	case cmsapi.UploadGrantStatusMinted:
		return mediaStateReceived
	case cmsapi.UploadGrantStatusUsed:
		return mediaStateReadyInternal
	case cmsapi.UploadGrantStatusFailedDigest:
		return mediaStateRejectedUnsupported
	case cmsapi.UploadGrantStatusExpired:
		return mediaStateExpired
	default:
		return mediaStateReceived
	}
}

// latestVerdict returns the most recent draft-review verdict, or nil.
func latestVerdict(state *cmsapi.DraftMediaState) *cmsapi.DraftReviewVerdictRecord {
	if state == nil {
		return nil
	}
	var latest *cmsapi.DraftReviewVerdictRecord
	for i := range state.Verdicts {
		verdict := &state.Verdicts[i]
		if latest == nil || verdict.RecordedAt > latest.RecordedAt {
			latest = verdict
		}
	}
	return latest
}

// mediaEnvelopeStateForUsage maps a draft-bound usage onto the envelope
// vocabulary, folding in review staleness and publish state from Lesser's
// fields.
func mediaEnvelopeStateForUsage(state *cmsapi.DraftMediaState, usage *cmsapi.EditorialMediaUsage) string {
	switch usage.State {
	case cmsapi.EditorialMediaStateProcessing:
		return mediaStateProcessing
	case cmsapi.EditorialMediaStateRejected:
		return mediaStateRejectedUnsupported
	case cmsapi.EditorialMediaStateWithdrawn, cmsapi.EditorialMediaStateSuperseded, cmsapi.EditorialMediaStateUnavailable:
		return mediaStateUnavailableRemoved
	case cmsapi.EditorialMediaStateMissing:
		// Lesser emits MISSING when a binding has no servable asset
		// (cms_converters.go defaults EditorialMediaState to Missing for a nil
		// media record); this is the dedicated envelope state, distinct from
		// the withdrawn/superseded/unavailable lifecycle lane.
		return mediaStateMissing
	case cmsapi.EditorialMediaStateReady:
		if usage.PublishedURL != nil && strings.TrimSpace(*usage.PublishedURL) != "" {
			return mediaStatePublished
		}
		if state != nil {
			if latest := latestVerdict(state); latest != nil {
				// Lesser sets Stale = !Current, so the non-current case is
				// exactly the stale case; no separate approved-for-revision
				// lane is producible.
				if latest.Stale {
					return mediaStateStale
				}
			}
			if len(state.ActiveReviewerIDs) > 0 {
				return mediaStateAwaitingReview
			}
		}
		return mediaStateAttached
	default:
		return mediaStateReceived
	}
}

func bindingBlockingReasons(state *cmsapi.DraftMediaState) []string {
	if state == nil {
		return nil
	}
	var reasons []string
	for _, reason := range state.PublishEligibility.BlockingReasons {
		if strings.HasPrefix(reason, "BOUND_MEDIA_") {
			reasons = append(reasons, reason)
		}
	}
	return reasons
}

func mediaGrantMintResult(grant *cmsapi.UploadGrant, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if grant == nil {
		return nil, fmt.Errorf("upload_grant_mint returned no grant")
	}
	expiresIn := grantTTLSeconds(grant.ExpiresAt)
	payload := map[string]any{
		"tool":             "upload_grant_mint",
		"operation":        "minted",
		"source":           "lesser_cms_graphql",
		"grant":            grant,
		"expiresInSeconds": expiresIn,
		"guidance": "Two-step upload: PUT the exact declared bytes to grant.presignedUrl OUT-OF-BAND now, then call upload_finalize with grant.id. Finalize is one-time; the grant expires at " +
			grant.ExpiresAt + " and an expired grant requires a fresh mint.",
	}
	return mediaStructuredResult("upload_grant_mint", "minted",
		"Upload grant minted; PUT the declared bytes then finalize", payload, maxOutputBytes)
}

func mediaFinalizeResult(result *cmsapi.UploadGrantFinalizeResult, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if result == nil || result.Media == nil {
		return nil, fmt.Errorf("upload_finalize returned no media")
	}
	payload := map[string]any{
		"tool":      "upload_finalize",
		"operation": "finalized",
		"source":    "lesser_cms_graphql",
		"grant":     result.Grant,
		"media":     result.Media,
		"guidance":  "The grant is consumed (USED). Attach the admitted media to a draft with draft_media_attach using media.mediaId.",
	}
	return mediaStructuredResult("upload_finalize", "finalized",
		"Upload verified and admitted as editorial media", payload, maxOutputBytes)
}

func mediaGrantStateResult(grant *cmsapi.UploadGrant, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if grant == nil {
		return nil, fmt.Errorf("media_state returned no grant")
	}
	state := mediaEnvelopeStateForGrant(grant)
	payload := map[string]any{
		"tool":       "media_state",
		"operation":  "state",
		"source":     "lesser_cms_graphql",
		"mode":       "upload_grant",
		"state":      state,
		"grantState": grant.Status,
		"grant":      grant,
	}
	if state == mediaStateExpired {
		payload["guidance"] = "The grant expired; mint a fresh upload_grant_mint grant. The grant TTL is 15 minutes."
	} else if state == mediaStateRejectedUnsupported {
		payload["guidance"] = "The grant failed verification and is consumed (FAILED_DIGEST); mint a fresh grant."
	} else if state == mediaStateReceived && grant.PresignedURL != nil {
		payload["guidance"] = "The grant is MINTED. The presigned URL is re-signed on each state read while minted — do not cache-bust on it. PUT the exact declared bytes then finalize once."
	}
	return mediaStructuredResult("media_state", "state",
		"Upload grant state: "+state, payload, maxOutputBytes)
}

func mediaBindingStateResult(state *cmsapi.DraftMediaState, mediaID string, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if state == nil {
		return nil, fmt.Errorf("media_state returned no draft state")
	}
	var usage *cmsapi.EditorialMediaUsage
	for i := range state.EditorialMedia {
		if state.EditorialMedia[i].MediaID == mediaID {
			usage = &state.EditorialMedia[i]
			break
		}
	}
	if usage == nil {
		return toolErrorResult(mediaErrorNotFound, "media_id is not bound to this draft or is not authorized", http.StatusNotFound, map[string]any{
			"source":  "lesser_cms_graphql",
			"tool":    "media_state",
			"lookup":  "media_id",
			"value":   mediaID,
			"draftId": state.DraftID,
		})
	}
	envelopeState := mediaEnvelopeStateForUsage(state, usage)
	payload := map[string]any{
		"tool":      "media_state",
		"operation": "state",
		"source":    "lesser_cms_graphql",
		"mode":      "draft_binding",
		"state":     envelopeState,
		"draftId":   state.DraftID,
		"mediaId":   mediaID,
		"role":      usage.Role,
		"usage":     usage,
	}
	if len(state.ActiveReviewerIDs) > 0 {
		payload["activeReviewerIds"] = state.ActiveReviewerIDs
	}
	if len(state.Verdicts) > 0 {
		payload["verdicts"] = state.Verdicts
		payload["contentHash"] = state.ContentHash
	}
	if reasons := bindingBlockingReasons(state); len(reasons) > 0 {
		payload["blockingReasons"] = reasons
	}
	if envelopeState == mediaStatePublished && usage.PublishedURL != nil {
		payload["publishedUrl"] = *usage.PublishedURL
	}
	return mediaStructuredResult("media_state", "state",
		"Editorial media state: "+envelopeState, payload, maxOutputBytes)
}

func mediaReadResult(access *cmsapi.EditorialMediaAccess, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if access == nil {
		return nil, fmt.Errorf("media_read returned no access")
	}
	payload := map[string]any{
		"tool":      "media_read",
		"operation": "read",
		"source":    "lesser_cms_graphql",
		"access":    access,
		"guidance":  "This short-lived exact-asset URL is issued to the draft owner or an active reviewer and expires at " + access.ExpiresAt + "; mint it fresh per review rather than caching.",
	}
	return mediaStructuredResult("media_read", "read",
		"Grant-scoped asset read issued for the exact bound asset", payload, maxOutputBytes)
}

func mediaBindingsResult(toolName, operation string, state *cmsapi.DraftMediaState, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	if state == nil {
		return nil, fmt.Errorf("%s returned no draft state", toolName)
	}
	payload := map[string]any{
		"tool":           toolName,
		"operation":      operation,
		"source":         "lesser_cms_graphql",
		"draftId":        state.DraftID,
		"contentHash":    state.ContentHash,
		"revision":       state.Revision,
		"editorialMedia": state.EditorialMedia,
		"count":          len(state.EditorialMedia),
	}
	return mediaStructuredResult(toolName, operation,
		fmt.Sprintf("Draft %s %d editorial media associations", operation, len(state.EditorialMedia)),
		payload, maxOutputBytes)
}

func mediaStructuredResult(toolName, operation, summary string, payload map[string]any, maxOutputBytes int) (*mcpruntime.ToolResult, error) {
	result, err := toolStructuredFirstResult(structuredFirstResultOptions{
		Summary: summary,
		Data:    payload,
		Text: map[string]any{
			"tool":      toolName,
			"operation": operation,
		},
	})
	if err != nil {
		return nil, err
	}
	measurement, err := measureToolResultPayload(result)
	if err != nil {
		return nil, err
	}
	if measurement.JSONRPCEnvelopeBytes <= maxOutputBytes {
		return result, nil
	}
	return toolErrorResult("response_too_large", toolName+" response exceeds max_output_bytes", 413, map[string]any{
		"tool":                   toolName,
		"operation":              operation,
		"measuredBytes":          measurement.JSONRPCEnvelopeBytes,
		"maxOutputBytes":         maxOutputBytes,
		"contentTextBytes":       measurement.ContentTextBytes,
		"structuredContentBytes": measurement.StructuredContentBytes,
		"guidance":               "increase max_output_bytes",
	})
}

// mediaToolResultFromError maps media-surface failures onto the shared
// structured-error envelope.
func mediaToolResultFromError(toolName string, err error) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return nil, nil
	}
	if failure := mcpAuthFailureFromError(err); failure != nil {
		return toolErrorResult(failure.Code, failure.Message, failure.Status, failure.Details)
	}
	var grantNotFound *cmsapi.UploadGrantNotFoundError
	if errors.As(err, &grantNotFound) {
		return toolErrorResult(mediaErrorNotFound, "Upload grant not found or not owned by the caller", http.StatusNotFound, map[string]any{
			"source": "lesser_cms_graphql",
			"tool":   toolName,
			"lookup": grantNotFound.Lookup,
			"value":  grantNotFound.Value,
		})
	}
	var bindingNotFound *cmsapi.MediaBindingNotFoundError
	if errors.As(err, &bindingNotFound) {
		return toolErrorResult(mediaErrorNotFound, "Media is not bound to this draft or is not authorized", http.StatusNotFound, map[string]any{
			"source": "lesser_cms_graphql",
			"tool":   toolName,
			"lookup": bindingNotFound.Lookup,
			"value":  bindingNotFound.Value,
		})
	}
	var draftNotFound *cmsapi.DraftNotFoundError
	if errors.As(err, &draftNotFound) {
		return toolErrorResult(mediaErrorNotFound, "Article draft not found or not authorized", http.StatusNotFound, map[string]any{
			"source": "lesser_cms_graphql",
			"tool":   toolName,
			"lookup": draftNotFound.Lookup,
			"value":  draftNotFound.Value,
		})
	}
	var reviewNotFound *cmsapi.DraftReviewNotFoundError
	if errors.As(err, &reviewNotFound) {
		return toolErrorResult(mediaErrorNotFound, "Draft not found or not authorized", http.StatusNotFound, map[string]any{
			"source": "lesser_cms_graphql",
			"tool":   toolName,
			"lookup": reviewNotFound.Lookup,
			"value":  reviewNotFound.Value,
		})
	}
	var classified *cmsapi.UploadGrantClassifiedError
	if errors.As(err, &classified) {
		return mediaClassifiedErrorResult(toolName, classified)
	}
	var gqlErr *cmsapi.GraphQLErrors
	if errors.As(err, &gqlErr) {
		details := map[string]any{"source": "lesser_cms_graphql", "tool": toolName}
		if len(gqlErr.Errors) > 0 {
			details["graphqlErrors"] = gqlErr.Errors
		}
		code, status := articleDraftGraphQLErrorContract(gqlErr)
		return toolErrorResult(code, "Lesser CMS GraphQL returned errors", status, details)
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		return toolErrorResult("lesser_cms_http_error", "Lesser CMS API request failed", apiErr.Status, map[string]any{
			"source":       "lesser_cms_graphql",
			"tool":         toolName,
			"upstreamCode": apiErr.Status,
		})
	}
	return normalizedToolResultFromError(toolName, err)
}

func mediaClassifiedErrorResult(toolName string, classified *cmsapi.UploadGrantClassifiedError) (*mcpruntime.ToolResult, error) {
	switch classified.Class {
	case cmsapi.UploadGrantErrorExpired:
		return toolErrorResult(mediaErrorGrantExpired,
			"Upload grant has expired; mint a fresh grant", http.StatusGone, map[string]any{
				"source":   "lesser_cms_graphql",
				"tool":     toolName,
				"guidance": "Mint a fresh upload_grant_mint grant. Grant TTL is 15 minutes.",
			})
	case cmsapi.UploadGrantErrorDigestMismatch:
		return toolErrorResult(mediaErrorDigestMismatch,
			"Uploaded bytes failed grant verification; the grant is consumed (FAILED_DIGEST)", http.StatusUnprocessableEntity, map[string]any{
				"source":        "lesser_cms_graphql",
				"tool":          toolName,
				"failureReason": classified.Message,
				"guidance":      "Mint a fresh upload_grant_mint grant and PUT the exact declared bytes.",
			})
	case cmsapi.UploadGrantErrorNotMinted:
		return toolErrorResult(mediaErrorGrantNotMinted,
			"Upload grant is not minted (already consumed); finalize is one-time", http.StatusConflict, map[string]any{
				"source":   "lesser_cms_graphql",
				"tool":     toolName,
				"guidance": "Mint a fresh upload_grant_mint grant.",
			})
	case cmsapi.UploadGrantErrorObjectMissing:
		// Residual N1: Lesser surfaces over-cap/size-abort finalize failures as
		// object-missing-class errors. Body cannot distinguish a never-PUT
		// object from a size-abort, so present re-mint guidance rather than an
		// infinite missing-object retry loop.
		return toolErrorResult(mediaErrorUploadNotFinalized,
			"Finalize found no verified uploaded object", http.StatusConflict, map[string]any{
				"source":   "lesser_cms_graphql",
				"tool":     toolName,
				"guidance": "If the PUT has not happened, PUT the exact declared bytes to the presigned URL then retry finalize once; if the PUT completed, this failure is the size-abort lane — re-mint a fresh upload_grant_mint grant.",
			})
	case cmsapi.UploadGrantErrorObjectEmpty:
		return toolErrorResult(mediaErrorUploadNotFinalized,
			"Uploaded object is empty; PUT the declared bytes then retry finalize", http.StatusConflict, map[string]any{
				"source":   "lesser_cms_graphql",
				"tool":     toolName,
				"guidance": "PUT the exact declared bytes to the presigned URL, then retry finalize once.",
			})
	case cmsapi.UploadGrantErrorNotFound:
		return toolErrorResult(mediaErrorNotFound,
			"Upload grant not found or not owned by the caller", http.StatusNotFound, map[string]any{
				"source": "lesser_cms_graphql",
				"tool":   toolName,
				"lookup": "grant_id",
			})
	case cmsapi.UploadGrantErrorValidation:
		return toolErrorResult(mediaErrorMintInvalid,
			"Lesser rejected the upload grant request", http.StatusUnprocessableEntity, map[string]any{
				"source":   "lesser_cms_graphql",
				"tool":     toolName,
				"message":  classified.Message,
				"guidance": "Correct the request (image/* content type, bounded size, 64-lowercase-hex sha256) and retry.",
			})
	default:
		details := map[string]any{
			"source":   "lesser_cms_graphql",
			"tool":     toolName,
			"upstream": classified.Message,
		}
		return toolErrorResult("lesser_cms_graphql_error", "Lesser CMS GraphQL returned errors", http.StatusBadGateway, details)
	}
}

// grantTTLSeconds returns the seconds until the grant expires, clamped at zero.
func grantTTLSeconds(expiresAt string) int64 {
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return 0
	}
	remaining := time.Until(parsed)
	if remaining <= 0 {
		return 0
	}
	return int64(remaining / time.Second)
}
