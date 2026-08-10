package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/hostapi"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	"github.com/golang-jwt/jwt/v5"
)

type fakeRecoveryHost struct{ detail *hostapi.RecoveryAgent }

func (f *fakeRecoveryHost) ReadRecoveryAgent(context.Context, string, string) (*hostapi.RecoveryAgent, error) {
	return f.detail, nil
}

type fakeRecoveryRegistry struct{ upsert *agentregistry.RecoveredInput }

func (f *fakeRecoveryRegistry) Get(context.Context, string, string) (*agentregistry.Agent, error) {
	return nil, agentregistry.ErrAgentNotFound
}
func (f *fakeRecoveryRegistry) UpsertRecovered(_ context.Context, in agentregistry.RecoveredInput) (*agentregistry.Agent, bool, error) {
	f.upsert = &in
	return &agentregistry.Agent{Account: in.Account, AgentID: in.AgentID, LocalID: in.LocalID}, true, nil
}

type fakeRecoveryContent struct{ draft, published, instructions int }

func (f *fakeRecoveryContent) SeedDraft(_ context.Context, in agentcontent.SeedDraftInput) (*agentcontent.Record, bool, error) {
	f.draft++
	return &agentcontent.Record{AgentID: in.AgentID, LifecycleState: agentcontent.LifecycleStateDraft, Version: 1, SoulVersion: 1}, true, nil
}
func (f *fakeRecoveryContent) SeedPublished(_ context.Context, in agentcontent.SeedPublishedInput) (*agentcontent.Record, bool, error) {
	f.published++
	return &agentcontent.Record{AgentID: in.AgentID, LifecycleState: agentcontent.LifecycleStatePublished, Version: 2, SoulVersion: 1}, true, nil
}
func (f *fakeRecoveryContent) SeedInstructions(_ context.Context, in agentcontent.SeedInstructionsInput) (*agentcontent.Record, bool, error) {
	f.instructions++
	return &agentcontent.Record{AgentID: in.AgentID, LifecycleState: agentcontent.LifecycleStateDraft, Version: 1}, true, nil
}

func TestSoulSelfRecoverUsesOnlyBoundActorAuthority(t *testing.T) {
	for _, classification := range []string{hostapi.RecoveryPublishedArtifactVerified, hostapi.RecoveryLegacyDeclarationsOnly} {
		t.Run(classification, func(t *testing.T) {
			registry := &fakeRecoveryRegistry{}
			content := &fakeRecoveryContent{}
			detail := recoveryToolDetail(classification)
			installRecoveryToolFakes(t, detail, registry, content)
			ctx := recoveryToolContext("della-marlowe", detail.AgentID)
			result, err := handleSoulSelfRecover(ctx, json.RawMessage(`{}`))
			if err != nil || result == nil || result.IsError {
				t.Fatalf("handleSoulSelfRecover = %+v, %v", result, err)
			}
			if registry.upsert == nil || registry.upsert.Account != "theory" || registry.upsert.AgentID != detail.AgentID || registry.upsert.LocalID != "della-marlowe" {
				t.Fatalf("registry input = %+v", registry.upsert)
			}
			if classification == hostapi.RecoveryPublishedArtifactVerified && (content.published != 1 || content.draft != 0) {
				t.Fatalf("published calls = %d draft = %d", content.published, content.draft)
			}
			if classification == hostapi.RecoveryLegacyDeclarationsOnly && (content.draft != 1 || content.published != 0) {
				t.Fatalf("legacy draft calls = %d published = %d", content.draft, content.published)
			}
		})
	}
}

func TestSoulSelfRecoverFailsBeforeWritesOnBindingMismatch(t *testing.T) {
	registry := &fakeRecoveryRegistry{}
	content := &fakeRecoveryContent{}
	detail := recoveryToolDetail(hostapi.RecoveryPublishedArtifactVerified)
	detail.LocalID = "iris-okonkwo"
	installRecoveryToolFakes(t, detail, registry, content)
	result, err := handleSoulSelfRecover(recoveryToolContext("della-marlowe", detail.AgentID), json.RawMessage(`{}`))
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("mismatch result = %+v, %v", result, err)
	}
	if registry.upsert != nil || content.published != 0 || content.draft != 0 || content.instructions != 0 {
		t.Fatal("binding mismatch reached persistence")
	}
}

func TestSoulSelfRecoverRejectsNonOAuthAndUnboundCallers(t *testing.T) {
	detail := recoveryToolDetail(hostapi.RecoveryPublishedArtifactVerified)
	registry := &fakeRecoveryRegistry{}
	content := &fakeRecoveryContent{}
	installRecoveryToolFakes(t, detail, registry, content)

	instanceCtx := auth.InjectToolContext(context.Background(), &auth.Principal{Type: auth.PrincipalTypeInstanceKey}, "")
	instanceCtx = runtimepolicy.WithContext(instanceCtx, runtimepolicy.Resolved{Profile: runtimepolicy.ProfileSouled, Determined: true, BoundSoul: true, SoulAgentID: detail.AgentID})
	if result, _ := handleSoulSelfRecover(instanceCtx, json.RawMessage(`{}`)); result == nil || !result.IsError {
		t.Fatalf("instance-key result = %+v", result)
	}

	unbound := recoveryToolContext("della-marlowe", detail.AgentID)
	unbound = runtimepolicy.WithContext(unbound, runtimepolicy.Resolved{Profile: runtimepolicy.ProfileDrone, Determined: true})
	if result, _ := handleSoulSelfRecover(unbound, json.RawMessage(`{}`)); result == nil || !result.IsError {
		t.Fatalf("unbound result = %+v", result)
	}
}

func installRecoveryToolFakes(t *testing.T, detail *hostapi.RecoveryAgent, registry recoveryRegistry, content recoveryContent) {
	t.Helper()
	t.Setenv("LESSER_HOST_INSTANCE_KEY", "managed-test-key")
	t.Setenv(envInstanceAccountID, "theory")
	t.Setenv("MCP_ENDPOINT", "https://api.theory.greater.website/mcp/{actor}")
	oldHost, oldRegistry, oldContent := newRecoveryHost, newRecoveryRegistry, newRecoveryContent
	newRecoveryHost = func() (hostapi.RecoveryClient, error) { return &fakeRecoveryHost{detail: detail}, nil }
	newRecoveryRegistry = func() (recoveryRegistry, error) { return registry, nil }
	newRecoveryContent = func() (recoveryContent, error) { return content, nil }
	t.Cleanup(func() { newRecoveryHost, newRecoveryRegistry, newRecoveryContent = oldHost, oldRegistry, oldContent })
}

func recoveryToolContext(actor, agentID string) context.Context {
	principal := &auth.Principal{Type: auth.PrincipalTypeOAuthToken, Claims: &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "subject-1"}, Username: actor, Scopes: []string{"write"},
	}}
	ctx := auth.InjectToolContext(context.Background(), principal, "oauth-token")
	return runtimepolicy.WithContext(ctx, runtimepolicy.Resolved{Profile: runtimepolicy.ProfileSouled, Determined: true, BoundSoul: true, SoulAgentID: agentID})
}

func recoveryToolDetail(classification string) *hostapi.RecoveryAgent {
	detail := &hostapi.RecoveryAgent{
		Version: "1", AgentID: "0x57d10000000000000000000000000000000000000000000000000000000065c3",
		Domain: "theory.greater.website", LocalID: "della-marlowe", Status: "active", Classification: classification,
		SelfDescriptionVersion: 2, MigrationReadSHA256: "sha256:" + strings.Repeat("a", 64),
		Source:           hostapi.RecoverySource{RegistrationID: "reg", ConversationID: "conv", ProducedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		DeclarationsJSON: json.RawMessage(`{"schemaVersion":"2","selfDescription":{"purpose":"recover"},"capabilities":[],"boundaries":[],"transparency":{}}`),
	}
	if classification == hostapi.RecoveryPublishedArtifactVerified {
		detail.Versions = []hostapi.RecoveryVersion{{VersionNumber: 1}}
	}
	return detail
}
