package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/equaltoai/lesser-body/internal/actorendpoint"
	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	"github.com/equaltoai/lesser-body/internal/recoverymaterial"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	mcpruntime "github.com/theory-cloud/apptheory/v3/runtime/mcp"
)

const (
	toolSoulSelfRecover  = "soul_self_recover"
	envInstanceAccountID = "INSTANCE_ACCOUNT_ID"
)

type recoveryRegistry interface {
	Get(context.Context, string, string) (*agentregistry.Agent, error)
	UpsertRecovered(context.Context, agentregistry.RecoveredInput) (*agentregistry.Agent, bool, error)
}

type recoveryContent interface {
	SeedDraft(context.Context, agentcontent.SeedDraftInput) (*agentcontent.Record, bool, error)
	SeedPublished(context.Context, agentcontent.SeedPublishedInput) (*agentcontent.Record, bool, error)
	SeedInstructions(context.Context, agentcontent.SeedInstructionsInput) (*agentcontent.Record, bool, error)
}

var (
	newRecoveryHost     = hostapi.DefaultRecovery
	newRecoveryRegistry = func() (recoveryRegistry, error) { return agentregistry.Default() }
	newRecoveryContent  = func() (recoveryContent, error) { return agentcontent.Default() }
)

func soulSelfRecoverDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        toolSoulSelfRecover,
		Title:       "Recover this bound soul into Ptah",
		Description: "Recover the authenticated, already soul-bound actor's exact Host-retained declaration into Body's Ptah registry and authoring content. The caller supplies no identity selector: Body derives the actor and Host agent ID from OAuth plus Lesser's binding. Published Host evidence creates an idempotent published Body soul; legacy declaration-only evidence creates a draft and never fabricates historical publication. Requires write scope and the souled runtime profile.",
		Annotations: idempotentMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{
			"type":"object","required":["data"],"additionalProperties":false,
			"properties":{"data":{"type":"object","additionalProperties":true},"error":{"type":"object"}}
		}`),
	}
}

func handleSoulSelfRecover(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	if !emptyClosedObject(args) {
		return toolErrorResult("invalid_request", "soul_self_recover accepts only an empty object", http.StatusBadRequest, nil)
	}
	principal := auth.PrincipalFromToolContext(ctx)
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken || principal.Claims == nil || strings.TrimSpace(principal.Claims.Subject) == "" {
		return toolErrorResult("self_recovery_authority_required", "soul_self_recover requires an authenticated OAuth actor", http.StatusForbidden, nil)
	}
	actor := strings.TrimSpace(principal.Claims.GetUsername())
	resolved, ok := runtimepolicy.FromContext(ctx)
	if actor == "" || !ok || !resolved.Determined || resolved.Profile != runtimepolicy.ProfileSouled || !resolved.BoundSoul || strings.TrimSpace(resolved.SoulAgentID) == "" {
		return toolErrorResult("soul_binding_required", "soul_self_recover requires an authoritative Lesser soul binding", http.StatusConflict, nil)
	}
	account := strings.ToLower(strings.TrimSpace(os.Getenv(envInstanceAccountID)))
	if account == "" {
		return toolErrorResult("not_configured", envInstanceAccountID+" is required", http.StatusInternalServerError, nil)
	}
	instanceKey, err := auth.LesserHostInstanceKey(ctx)
	if err != nil || strings.TrimSpace(instanceKey) == "" {
		return toolErrorResult("not_configured", "managed Host recovery trust is unavailable", http.StatusInternalServerError, nil)
	}
	client, err := newRecoveryHost()
	if err != nil || client == nil {
		return toolErrorResult("not_configured", "Host recovery client is unavailable", http.StatusInternalServerError, nil)
	}
	detail, err := client.ReadRecoveryAgent(ctx, instanceKey, resolved.SoulAgentID)
	if err != nil {
		return selfRecoveryUpstreamError(err)
	}
	if detail == nil || !strings.EqualFold(strings.TrimSpace(detail.AgentID), strings.TrimSpace(resolved.SoulAgentID)) ||
		actorendpoint.Validate(actor, detail.LocalID) != nil || !recoveryDomainMatchesDeployment(detail.Domain) {
		return toolErrorResult("recovery_binding_mismatch", "Host recovery evidence does not match the authenticated bound actor", http.StatusConflict, nil)
	}
	document, err := recoverymaterial.Soul(detail)
	if err != nil {
		return toolErrorResult("recovery_content_invalid", "Host recovery declarations cannot be represented safely in Ptah", http.StatusConflict, nil)
	}
	instructions, err := recoverymaterial.Instructions(detail)
	if err != nil {
		return toolErrorResult("recovery_content_invalid", "Host recovery instructions could not be materialized", http.StatusConflict, nil)
	}
	registry, err := newRecoveryRegistry()
	if err != nil || registry == nil {
		return toolErrorResult("not_configured", "Ptah registry is unavailable", http.StatusInternalServerError, nil)
	}
	var expectedLocalID *string
	existing, err := registry.Get(ctx, account, detail.AgentID)
	switch {
	case err == nil && existing != nil:
		if strings.TrimSpace(existing.LocalID) != "" && actorendpoint.Validate(actor, existing.LocalID) != nil {
			return toolErrorResult("recovery_registry_conflict", "existing Ptah registry identity conflicts with the bound actor", http.StatusConflict, nil)
		}
		expected := strings.TrimSpace(existing.LocalID)
		expectedLocalID = &expected
	case errors.Is(err, agentregistry.ErrAgentNotFound):
	case err != nil:
		return toolErrorResult("recovery_registry_error", "Body could not inspect the Ptah registry", http.StatusInternalServerError, nil)
	}
	content, err := newRecoveryContent()
	if err != nil || content == nil {
		return toolErrorResult("not_configured", "Ptah content storage is unavailable", http.StatusInternalServerError, nil)
	}
	var soul *agentcontent.Record
	var soulCreated bool
	updatedBy := strings.TrimSpace(principal.Claims.Subject)
	switch detail.Classification {
	case hostapi.RecoveryPublishedArtifactVerified:
		soul, soulCreated, err = content.SeedPublished(ctx, agentcontent.SeedPublishedInput{Account: account, AgentID: detail.AgentID, SoulDocument: document, UpdatedBySubjectID: updatedBy})
	case hostapi.RecoveryLegacyDeclarationsOnly:
		soul, soulCreated, err = content.SeedDraft(ctx, agentcontent.SeedDraftInput{Account: account, AgentID: detail.AgentID, SoulDocument: document, UpdatedBySubjectID: updatedBy})
	default:
		err = fmt.Errorf("unsupported recovery classification")
	}
	if err != nil {
		return selfRecoveryPersistenceError("soul", err)
	}
	instructionsRecord, instructionsCreated, err := content.SeedInstructions(ctx, agentcontent.SeedInstructionsInput{Account: account, AgentID: detail.AgentID, Content: instructions, UpdatedBySubjectID: updatedBy})
	if err != nil {
		return selfRecoveryPersistenceError("instructions", err)
	}
	publishedVersion := int64(0)
	if len(detail.Versions) > 0 {
		publishedVersion = int64(detail.Versions[len(detail.Versions)-1].VersionNumber)
	}
	registryRecord, registryCreated, err := registry.UpsertRecovered(ctx, agentregistry.RecoveredInput{
		Account: account, AgentID: detail.AgentID, HostRegistrationID: detail.Source.RegistrationID,
		HostConversationID: detail.Source.ConversationID, Domain: detail.Domain, LocalID: detail.LocalID,
		LifecycleStatus: detail.Status, PublishedVersion: publishedVersion,
		SelfDescriptionVersion: int64(detail.SelfDescriptionVersion), RecoveryClassification: detail.Classification,
		MigrationReadSHA256: detail.MigrationReadSHA256, RecoveryProducedAt: detail.Source.ProducedAt,
		RecoveryVersionCount: int64(len(detail.Versions)), ExpectedLocalID: expectedLocalID,
	})
	if err != nil {
		return selfRecoveryPersistenceError("registry", err)
	}
	slog.InfoContext(ctx, "soul self recovery completed", "tool", toolSoulSelfRecover, "actor_hash", recoveryIdentityHash(actor),
		"classification", detail.Classification, "registry_created", registryCreated, "soul_created", soulCreated, "instructions_created", instructionsCreated)
	data := map[string]any{
		"status": "recovered", "classification": detail.Classification,
		"migration_read_sha256":      detail.MigrationReadSHA256,
		"source":                     map[string]any{"registration_id": detail.Source.RegistrationID, "conversation_id": detail.Source.ConversationID, "produced_at": detail.Source.ProducedAt},
		"registry":                   map[string]any{"agent_id": registryRecord.AgentID, "local_id": registryRecord.LocalID, "created": registryCreated},
		"agent_soul":                 map[string]any{"lifecycle_state": soul.LifecycleState, "version": soul.Version, "soul_version": soul.SoulVersion, "created": soulCreated},
		"agent_instructions":         map[string]any{"lifecycle_state": instructionsRecord.LifecycleState, "version": instructionsRecord.Version, "created": instructionsCreated},
		"historical_publication_sha": false,
	}
	textSummary := map[string]any{"status": "recovered", "classification": detail.Classification, "agent_id": detail.AgentID,
		"soul_lifecycle_state": soul.LifecycleState, "replay": !registryCreated && !soulCreated && !instructionsCreated,
		"data": map[string]any{"location": "structuredContent.data"}}
	return toolStructuredFirstResult(structuredFirstResultOptions{
		Summary:     "Bound soul recovered into Ptah",
		Data:        data,
		Text:        textSummary,
		TextPayload: textSummary,
	})
}

func emptyClosedObject(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&fields) != nil || fields == nil || len(fields) != 0 {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func recoveryDomainMatchesDeployment(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	endpoint, err := url.Parse(strings.TrimSpace(os.Getenv("MCP_ENDPOINT")))
	if err != nil || endpoint.Hostname() == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(endpoint.Hostname(), "."))
	host = strings.TrimPrefix(host, "api.")
	return domain != "" && domain == host
}

func selfRecoveryUpstreamError(err error) (*mcpruntime.ToolResult, error) {
	var apiErr *hostapi.APIError
	if errors.As(err, &apiErr) && apiErr != nil {
		status := http.StatusBadGateway
		if apiErr.Status == http.StatusNotFound {
			status = http.StatusNotFound
		} else if apiErr.Status == http.StatusConflict || apiErr.Status == http.StatusForbidden {
			status = http.StatusConflict
		} else if apiErr.Status == http.StatusTooManyRequests {
			status = http.StatusTooManyRequests
		}
		return toolErrorResult("host_recovery_unavailable", "Host could not provide verified recovery evidence for this bound actor", status, nil)
	}
	return toolErrorResult("host_recovery_unavailable", "Host recovery is temporarily unavailable", http.StatusBadGateway, nil)
}

func selfRecoveryPersistenceError(surface string, err error) (*mcpruntime.ToolResult, error) {
	if errors.Is(err, agentcontent.ErrContentConflict) || errors.Is(err, agentregistry.ErrFinalizedLocalIDChanged) {
		return toolErrorResult("recovery_content_conflict", "existing Ptah state differs from the verified Host recovery source", http.StatusConflict, map[string]any{"surface": surface})
	}
	return toolErrorResult("recovery_persistence_error", "Body could not persist verified recovery state", http.StatusInternalServerError, map[string]any{"surface": surface})
}

func recoveryIdentityHash(value string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(digest[:])
}
