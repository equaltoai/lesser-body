package agentcontent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"
)

func TestUpsertCreateUpdateGetFlow(t *testing.T) {
	store, _ := newTestStore(t)

	created, err := store.Upsert(context.Background(), UpsertInput{
		Account:            " Account-A ",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "first soul draft",
		UpdatedBySubjectID: " subject-create ",
	})
	if err != nil {
		t.Fatalf("Upsert(create) error = %v", err)
	}
	if created.Account != "account-a" || created.AgentID != "agent-123" || created.Type != ContentTypeAgentSoul {
		t.Fatalf("created scope/type = %+v, want normalized account, agent, type", created)
	}
	if created.Content != "first soul draft" || created.Version != 1 || created.LifecycleState != LifecycleStateDraft {
		t.Fatalf("created content/version/state = %+v, want draft version 1", created)
	}
	if created.SoulVersion != 1 || created.Document == nil || created.Document.Body != created.Content {
		t.Fatalf("created soul document = %+v, want v2 body-only draft", created)
	}
	if created.UpdatedBySubjectID != "subject-create" {
		t.Fatalf("created UpdatedBySubjectID = %q", created.UpdatedBySubjectID)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created timestamps are zero: %+v", created)
	}

	updated, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               "AgentSoul",
		Content:            "second soul draft",
		UpdatedBySubjectID: "subject-update",
	})
	if err != nil {
		t.Fatalf("Upsert(update) error = %v", err)
	}
	if updated.Content != "second soul draft" || updated.Version != 2 || updated.SoulVersion != 2 || updated.LifecycleState != LifecycleStateDraft {
		t.Fatalf("updated content/version/state = %+v, want draft version 2", updated)
	}
	if updated.UpdatedBySubjectID != "subject-update" {
		t.Fatalf("updated UpdatedBySubjectID = %q", updated.UpdatedBySubjectID)
	}
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("updated UpdatedAt = %s before created UpdatedAt %s", updated.UpdatedAt, created.UpdatedAt)
	}

	got, err := store.Get(context.Background(), "account-a", "agent-123", ContentTypeAgentSoul)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Content != updated.Content || got.Version != updated.Version || got.UpdatedBySubjectID != updated.UpdatedBySubjectID {
		t.Fatalf("Get() = %+v, want updated %+v", got, updated)
	}
}

func TestIndependentVersionCounters(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	soul, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul v1",
		UpdatedBySubjectID: "subject-1",
	})
	if err != nil {
		t.Fatalf("Upsert(soul) error = %v", err)
	}
	instructions, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		Content:            "instructions v1",
		UpdatedBySubjectID: "subject-2",
	})
	if err != nil {
		t.Fatalf("Upsert(instructions) error = %v", err)
	}
	if soul.Version != 1 || instructions.Version != 1 {
		t.Fatalf("initial versions soul=%d instructions=%d, want both 1", soul.Version, instructions.Version)
	}

	soul, err = store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul v2",
		UpdatedBySubjectID: "subject-3",
	})
	if err != nil {
		t.Fatalf("Upsert(soul v2) error = %v", err)
	}
	instructions, err = store.Get(ctx, "account-a", "agent-123", ContentTypeAgentInstructions)
	if err != nil {
		t.Fatalf("Get(instructions) error = %v", err)
	}
	if soul.Version != 2 {
		t.Fatalf("soul version = %d, want 2", soul.Version)
	}
	if instructions.Version != 1 || instructions.Content != "instructions v1" {
		t.Fatalf("instructions after soul update = %+v, want unchanged version 1", instructions)
	}
}

func TestCrossAccountGetReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul",
		UpdatedBySubjectID: "subject-1",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	got, err := store.Get(context.Background(), "account-b", "agent-123", ContentTypeAgentSoul)
	if got != nil {
		t.Fatalf("cross-account Get() returned %+v, want nil", got)
	}
	if !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("cross-account Get() error = %v, want ErrContentNotFound", err)
	}
}

func TestSizeBounds(t *testing.T) {
	store, _ := newTestStore(t)
	cases := []struct {
		name  string
		typ   ContentType
		limit int
	}{
		{name: "agent soul", typ: ContentTypeAgentSoul, limit: MaxAgentSoulBytes},
		{name: "agent instructions", typ: ContentTypeAgentInstructions, limit: MaxAgentInstructionsBytes},
	}

	for _, tc := range cases {
		t.Run(tc.name+" accepts limit", func(t *testing.T) {
			_, err := store.Upsert(context.Background(), UpsertInput{
				Account:            "account-a",
				AgentID:            "agent-" + strings.ReplaceAll(tc.name, " ", "-"),
				Type:               tc.typ,
				Content:            strings.Repeat("a", tc.limit),
				UpdatedBySubjectID: "subject-limit",
			})
			if err != nil {
				t.Fatalf("Upsert(at limit) error = %v", err)
			}
		})

		t.Run(tc.name+" rejects over limit", func(t *testing.T) {
			_, err := store.Upsert(context.Background(), UpsertInput{
				Account:            "account-a",
				AgentID:            "agent-over-" + strings.ReplaceAll(tc.name, " ", "-"),
				Type:               tc.typ,
				Content:            strings.Repeat("b", tc.limit+1),
				UpdatedBySubjectID: "subject-over",
			})
			if !errors.Is(err, ErrContentTooLarge) {
				t.Fatalf("Upsert(over limit) error = %v, want ErrContentTooLarge", err)
			}
			var sizeErr *SizeError
			if !errors.As(err, &sizeErr) || sizeErr.Type != tc.typ || sizeErr.Limit != tc.limit || sizeErr.Actual != tc.limit+1 {
				t.Fatalf("size error = %#v, want typed details for %s", err, tc.typ)
			}
		})
	}
}

func TestArchiveIsIdempotentAndCapturesAudit(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	created, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		Content:            "instructions draft",
		UpdatedBySubjectID: "subject-create",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	archived, err := store.Archive(ctx, ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		UpdatedBySubjectID: "subject-archive",
	})
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if archived.LifecycleState != LifecycleStateArchived || archived.Version != created.Version || archived.Content != created.Content {
		t.Fatalf("archived = %+v, want archived with same content/version", archived)
	}
	if archived.UpdatedBySubjectID != "subject-archive" {
		t.Fatalf("archived UpdatedBySubjectID = %q", archived.UpdatedBySubjectID)
	}

	again, err := store.Archive(ctx, ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		UpdatedBySubjectID: "subject-archive-again",
	})
	if err != nil {
		t.Fatalf("Archive(again) error = %v", err)
	}
	if again.LifecycleState != LifecycleStateArchived || again.Version != archived.Version || again.Content != archived.Content {
		t.Fatalf("Archive(again) = %+v, want idempotent archived content/version", again)
	}
	if again.UpdatedBySubjectID != "subject-archive-again" {
		t.Fatalf("Archive(again) UpdatedBySubjectID = %q", again.UpdatedBySubjectID)
	}

	draft, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentInstructions,
		Content:            "instructions draft after archive",
		UpdatedBySubjectID: "subject-reopen",
	})
	if err != nil {
		t.Fatalf("Upsert(after archive) error = %v", err)
	}
	if draft.LifecycleState != LifecycleStateDraft || draft.Version != archived.Version+1 {
		t.Fatalf("Upsert(after archive) = %+v, want draft version increment", draft)
	}
}

func TestArchiveMissingRecordReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.Archive(context.Background(), ArchiveInput{
		Account:            "account-a",
		AgentID:            "missing-agent",
		Type:               ContentTypeAgentSoul,
		UpdatedBySubjectID: "subject-archive",
	})
	if !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("Archive(missing) error = %v, want ErrContentNotFound", err)
	}
}

func TestSoulLifecycleTransitionsAndPublishedSnapshotImmutability(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	draftOne, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "first immutable candidate",
		UpdatedBySubjectID: "subject-draft-one",
	})
	if err != nil {
		t.Fatalf("Upsert(draft one) error = %v", err)
	}
	if _, err := store.Archive(ctx, ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		UpdatedBySubjectID: "subject-invalid-archive",
	}); !errors.Is(err, ErrInvalidLifecycleTransition) {
		t.Fatalf("Archive(draft) error = %v, want ErrInvalidLifecycleTransition", err)
	}

	publishedOne, err := store.Publish(ctx, PublishInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		UpdatedBySubjectID: "subject-publish-one",
	})
	if err != nil {
		t.Fatalf("Publish(draft one) error = %v", err)
	}
	if publishedOne.LifecycleState != LifecycleStatePublished ||
		publishedOne.SoulVersion != draftOne.SoulVersion ||
		publishedOne.Version != draftOne.Version+1 {
		t.Fatalf("published one = %+v, want published soul version %d", publishedOne, draftOne.SoulVersion)
	}
	if publishedOne.UpdatedBySubjectID != draftOne.UpdatedBySubjectID ||
		!publishedOne.UpdatedAt.Equal(draftOne.UpdatedAt) {
		t.Fatalf("publication rewrote draft audit metadata: draft=%+v published=%+v", draftOne, publishedOne)
	}
	replayed, err := store.Publish(ctx, PublishInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		UpdatedBySubjectID: "subject-publish-replay",
	})
	if err != nil {
		t.Fatalf("Publish(replay) error = %v", err)
	}
	if replayed.Version != publishedOne.Version || replayed.UpdatedBySubjectID != publishedOne.UpdatedBySubjectID {
		t.Fatalf("Publish(replay) mutated published snapshot: before=%+v after=%+v", publishedOne, replayed)
	}

	draftTwo, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "second candidate",
		UpdatedBySubjectID: "subject-draft-two",
	})
	if err != nil {
		t.Fatalf("Upsert(after published) error = %v", err)
	}
	if draftTwo.LifecycleState != LifecycleStateDraft ||
		draftTwo.SoulVersion != publishedOne.SoulVersion+1 ||
		draftTwo.Content == publishedOne.Content {
		t.Fatalf("draft two = %+v, want a distinct new draft version", draftTwo)
	}
	if _, err := store.Publish(ctx, PublishInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		UpdatedBySubjectID: "subject-publish-two",
	}); err != nil {
		t.Fatalf("Publish(draft two) error = %v", err)
	}
	archivedTwo, err := store.Archive(ctx, ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		UpdatedBySubjectID: "subject-archive-two",
	})
	if err != nil {
		t.Fatalf("Archive(published two) error = %v", err)
	}
	if archivedTwo.LifecycleState != LifecycleStateArchived {
		t.Fatalf("archived two = %+v, want archived", archivedTwo)
	}
	if _, err := store.Publish(ctx, PublishInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		UpdatedBySubjectID: "subject-invalid-republish",
	}); !errors.Is(err, ErrInvalidLifecycleTransition) {
		t.Fatalf("Publish(archived) error = %v, want ErrInvalidLifecycleTransition", err)
	}

	history := loadSoulHistory(t, store, "account-a", "agent-123")
	var publishedSnapshot *historyRecord
	for i := range history {
		if history[i].SoulVersion == 1 && history[i].LifecycleState == string(LifecycleStatePublished) {
			publishedSnapshot = &history[i]
			break
		}
	}
	if publishedSnapshot == nil {
		t.Fatalf("history = %+v, missing published soul version 1 snapshot", history)
	}
	if publishedSnapshot.Content != "first immutable candidate" {
		t.Fatalf("published history content = %q, want immutable first candidate", publishedSnapshot.Content)
	}
	if publishedSnapshot.ActionBySubjectID != "subject-publish-one" ||
		publishedSnapshot.ActionAt.IsZero() {
		t.Fatalf("published history action audit = %+v, want publishing subject/time", publishedSnapshot)
	}
	document, err := decodeSoulDocument(publishedSnapshot.DocumentJSON)
	if err != nil {
		t.Fatalf("decode published history document: %v", err)
	}
	if document.Body != "first immutable candidate" || document.LifecycleState != LifecycleStatePublished {
		t.Fatalf("published history document = %+v, want unchanged v1 publication", document)
	}
}

func TestBodyOnlyV1CompatibleSoulUpsertProducesValidV2Draft(t *testing.T) {
	store, _ := newTestStore(t)
	record, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "body-only",
		Type:               ContentTypeAgentSoul,
		Content:            "A canonical Markdown body with no structured overlay.",
		UpdatedBySubjectID: "subject-owner",
	})
	if err != nil {
		t.Fatalf("Upsert(body-only) error = %v", err)
	}
	if record.Document == nil || record.Document.Structure != nil || record.Document.Provenance != nil {
		t.Fatalf("body-only document = %+v", record.Document)
	}
	if err := ValidateSoulDocumentRecord(record.Document, "body-only"); err != nil {
		t.Fatalf("body-only v2 record validation error = %v", err)
	}
}

func TestLegacyOpaqueSoulTransitionsRequireTypedRewrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  LifecycleState
		action ContentAction
		run    func(*Store, string) error
	}{
		{
			name:   "publish",
			state:  LifecycleStateDraft,
			action: ContentActionPublish,
			run: func(store *Store, agentID string) error {
				_, err := store.Publish(context.Background(), PublishInput{
					Account:            "account-a",
					AgentID:            agentID,
					UpdatedBySubjectID: "subject-publisher",
				})
				return err
			},
		},
		{
			name:   "archive",
			state:  LifecycleStatePublished,
			action: ContentActionArchive,
			run: func(store *Store, agentID string) error {
				_, err := store.Archive(context.Background(), ArchiveInput{
					Account:            "account-a",
					AgentID:            agentID,
					Type:               ContentTypeAgentSoul,
					UpdatedBySubjectID: "subject-archiver",
				})
				return err
			},
		},
		{
			name:   "genesis seed",
			state:  LifecycleStateDraft,
			action: ContentActionPublish,
			run: func(store *Store, agentID string) error {
				_, _, err := store.SeedPublished(context.Background(), SeedPublishedInput{
					Account: "account-a",
					AgentID: agentID,
					SoulDocument: &SoulDocument{
						SchemaVersion: SoulDocumentSchemaVersion,
						AgentID:       agentID,
						Body:          "legacy opaque body",
					},
					UpdatedBySubjectID: "subject-genesis",
				})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			agentID := "legacy-" + strings.ReplaceAll(tc.name, " ", "-")
			now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
			legacy := store.recordFor("account-a", agentID, ContentTypeAgentSoul)
			legacy.Content = "legacy opaque body"
			legacy.Version = 1
			legacy.LifecycleState = string(tc.state)
			legacy.ContentCreatedAt = now
			legacy.ContentUpdatedAt = now
			legacy.UpdatedBySubjectID = "subject-legacy"
			if err := store.db.Model(legacy).Create(); err != nil {
				t.Fatalf("seed legacy row: %v", err)
			}

			err := tc.run(store, agentID)
			if !errors.Is(err, ErrSoulRewriteRequired) {
				t.Fatalf("%s legacy row error = %v, want ErrSoulRewriteRequired", tc.action, err)
			}
			var rewriteErr *SoulRewriteRequiredError
			if !errors.As(err, &rewriteErr) || rewriteErr.Action != tc.action {
				t.Fatalf("%s legacy row typed error = %#v / %v", tc.action, rewriteErr, err)
			}
			for _, fix := range []string{"agent_soul_upsert", "agent_soul_publish"} {
				if !strings.Contains(err.Error(), fix) {
					t.Fatalf("typed rewrite error does not name %s: %v", fix, err)
				}
			}

			current, getErr := store.loadRecord(context.Background(), "account-a", agentID, ContentTypeAgentSoul)
			if getErr != nil {
				t.Fatalf("load legacy row after rejected transition: %v", getErr)
			}
			if current.Version != 1 || current.DocumentJSON != "" || current.LifecycleState != string(tc.state) {
				t.Fatalf("legacy row mutated after rejected transition: %+v", current)
			}
			if history := loadSoulHistory(t, store, "account-a", agentID); len(history) != 0 {
				t.Fatalf("legacy transition wrote history: %+v", history)
			}
		})
	}
}

func TestSeedPublishedIsIdempotentAndNeverOverwritesDifferentContent(t *testing.T) {
	store, _ := newTestStore(t)
	document := &SoulDocument{
		SchemaVersion: SoulDocumentSchemaVersion,
		AgentID:       "seed-agent",
		Body:          "deterministically rendered seed",
		Provenance: &Provenance{
			DeclarationSchemaVersion: "soul-five-body-schema.v2",
			DeclarationCandidateHash: "sha256:" + strings.Repeat("a", 64),
			RegistrationID:           "registration-1",
			ConversationID:           "conversation-1",
			Model:                    "provider:model",
			Source:                   "ptah_seed",
		},
	}
	first, created, err := store.SeedPublished(context.Background(), SeedPublishedInput{
		Account:            "account-a",
		AgentID:            "seed-agent",
		SoulDocument:       document,
		UpdatedBySubjectID: "subject-owner",
	})
	if err != nil {
		t.Fatalf("SeedPublished(first) error = %v", err)
	}
	if !created || first.LifecycleState != LifecycleStatePublished || first.SoulVersion != 1 {
		t.Fatalf("SeedPublished(first) = created %t record %+v", created, first)
	}
	replayed, created, err := store.SeedPublished(context.Background(), SeedPublishedInput{
		Account:            "account-a",
		AgentID:            "seed-agent",
		SoulDocument:       cloneSoulDocument(document),
		UpdatedBySubjectID: "subject-owner-replay",
	})
	if err != nil {
		t.Fatalf("SeedPublished(replay) error = %v", err)
	}
	if created || replayed.Version != first.Version || replayed.SoulVersion != first.SoulVersion ||
		replayed.UpdatedBySubjectID != first.UpdatedBySubjectID {
		t.Fatalf("SeedPublished(replay) mutated publication: first=%+v replay=%+v created=%t", first, replayed, created)
	}

	different := cloneSoulDocument(document)
	different.Body = "different owner content"
	if _, _, err := store.SeedPublished(context.Background(), SeedPublishedInput{
		Account:            "account-a",
		AgentID:            "seed-agent",
		SoulDocument:       different,
		UpdatedBySubjectID: "subject-owner",
	}); !errors.Is(err, ErrContentConflict) {
		t.Fatalf("SeedPublished(different) error = %v, want ErrContentConflict", err)
	}
	current, err := store.Get(context.Background(), "account-a", "seed-agent", ContentTypeAgentSoul)
	if err != nil || current.Content != first.Content || current.Version != first.Version {
		t.Fatalf("current after conflicting seed = %+v/%v, want original publication", current, err)
	}
}

func TestSeedPublishedRepairsMatchingPartialDraft(t *testing.T) {
	store, _ := newTestStore(t)
	document := &SoulDocument{
		SchemaVersion: SoulDocumentSchemaVersion,
		AgentID:       "partial-seed",
		Body:          "deterministically rendered seed",
		Provenance: &Provenance{
			DeclarationSchemaVersion: "soul-five-body-schema.v2",
			DeclarationCandidateHash: "sha256:" + strings.Repeat("b", 64),
			RegistrationID:           "registration-2",
			ConversationID:           "conversation-2",
			Model:                    "provider:model",
			Source:                   "ptah_seed",
		},
	}
	draft, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "partial-seed",
		Type:               ContentTypeAgentSoul,
		SoulDocument:       document,
		UpdatedBySubjectID: "subject-owner",
	})
	if err != nil {
		t.Fatalf("Upsert(partial seed) error = %v", err)
	}
	repaired, created, err := store.SeedPublished(context.Background(), SeedPublishedInput{
		Account:            "account-a",
		AgentID:            "partial-seed",
		SoulDocument:       cloneSoulDocument(document),
		UpdatedBySubjectID: "subject-owner",
	})
	if err != nil {
		t.Fatalf("SeedPublished(partial draft) error = %v", err)
	}
	if created || repaired.LifecycleState != LifecycleStatePublished ||
		repaired.SoulVersion != draft.SoulVersion ||
		repaired.Version != draft.Version+1 {
		t.Fatalf("SeedPublished(partial draft) created=%t record=%+v draft=%+v", created, repaired, draft)
	}
}

func TestSeedPublicationVersionGuardNeverPublishesConcurrentOwnerDraft(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	seedDocument := &SoulDocument{
		SchemaVersion: SoulDocumentSchemaVersion,
		AgentID:       "seed-agent",
		Body:          "finalized genesis declaration",
	}
	seedDraft, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "seed-agent",
		Type:               ContentTypeAgentSoul,
		SoulDocument:       seedDocument,
		UpdatedBySubjectID: "subject-genesis",
	})
	if err != nil {
		t.Fatalf("Upsert(seed draft) error = %v", err)
	}
	validated, err := validateUpsertInput(UpsertInput{
		Account:            "account-a",
		AgentID:            "seed-agent",
		Type:               ContentTypeAgentSoul,
		SoulDocument:       seedDocument,
		UpdatedBySubjectID: "subject-genesis",
	})
	if err != nil {
		t.Fatalf("validate seed input error = %v", err)
	}

	ownerDraft, err := store.Upsert(ctx, UpsertInput{
		Account:            "account-a",
		AgentID:            "seed-agent",
		Type:               ContentTypeAgentSoul,
		Content:            "concurrent owner-authored draft",
		UpdatedBySubjectID: "subject-owner",
	})
	if err != nil {
		t.Fatalf("Upsert(owner draft) error = %v", err)
	}
	if _, err := store.transitionSoul(
		ctx,
		validated,
		ContentActionPublish,
		LifecycleStateDraft,
		LifecycleStatePublished,
		seedDraft.Version,
	); !errors.Is(err, ErrContentConflict) {
		t.Fatalf("seed-bound transition error = %v, want ErrContentConflict", err)
	}

	current, err := store.Get(ctx, "account-a", "seed-agent", ContentTypeAgentSoul)
	if err != nil {
		t.Fatalf("Get(current) error = %v", err)
	}
	if current.LifecycleState != LifecycleStateDraft ||
		current.Version != ownerDraft.Version ||
		current.Content != ownerDraft.Content {
		t.Fatalf("concurrent owner draft was published or overwritten: %+v", current)
	}
	for _, history := range loadSoulHistory(t, store, "account-a", "seed-agent") {
		if history.LifecycleState == string(LifecycleStatePublished) {
			t.Fatalf("seed race created an unrelated published history snapshot: %+v", history)
		}
	}
}

func TestInvalidTypeStateAndAuditErrors(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Get(context.Background(), "account-a", "agent-123", "biography"); !errors.Is(err, ErrInvalidContentType) {
		t.Fatalf("Get(invalid type) error = %v, want ErrInvalidContentType", err)
	}
	if _, err := store.Upsert(context.Background(), UpsertInput{
		Account: "account-a",
		AgentID: "agent-123",
		Type:    ContentTypeAgentSoul,
		Content: "soul",
	}); !errors.Is(err, ErrMissingUpdatedBySubjectID) {
		t.Fatalf("Upsert(missing subject) error = %v, want ErrMissingUpdatedBySubjectID", err)
	}

	record := store.recordFor("account-a", "agent-bad-state", ContentTypeAgentSoul)
	record.Content = "soul"
	record.Version = 1
	record.LifecycleState = "corrupt"
	record.ContentCreatedAt = time.Now().UTC()
	record.ContentUpdatedAt = record.ContentCreatedAt
	record.UpdatedBySubjectID = "subject-seed"
	if err := store.db.Model(record).Create(); err != nil {
		t.Fatalf("seed invalid-state record error = %v", err)
	}

	got, err := store.Get(context.Background(), "account-a", "agent-bad-state", ContentTypeAgentSoul)
	if got != nil {
		t.Fatalf("Get(invalid state) returned %+v, want nil", got)
	}
	if !errors.Is(err, ErrInvalidLifecycleState) {
		t.Fatalf("Get(invalid state) error = %v, want ErrInvalidLifecycleState", err)
	}
}

func TestWritesUseInstanceContentTableNotLesserTable(t *testing.T) {
	store, fake := newTestStore(t)
	if _, err := store.Upsert(context.Background(), UpsertInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		Content:            "soul",
		UpdatedBySubjectID: "subject-create",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if _, err := store.Archive(context.Background(), ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		UpdatedBySubjectID: "subject-archive",
	}); !errors.Is(err, ErrInvalidLifecycleTransition) {
		t.Fatalf("Archive(draft soul) error = %v, want ErrInvalidLifecycleTransition", err)
	}
	if _, err := store.Publish(context.Background(), PublishInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		UpdatedBySubjectID: "subject-publish",
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if _, err := store.Archive(context.Background(), ArchiveInput{
		Account:            "account-a",
		AgentID:            "agent-123",
		Type:               ContentTypeAgentSoul,
		UpdatedBySubjectID: "subject-archive",
	}); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}

	contentItems := fake.Items(os.Getenv(EnvInstanceContentTable))
	if len(contentItems) != 4 {
		t.Fatalf("content table items = %d, want current plus draft/published/archived history", len(contentItems))
	}
	lesserItems := fake.Items(os.Getenv("LESSER_TABLE_NAME"))
	if len(lesserItems) != 0 {
		t.Fatalf("LESSER_TABLE_NAME items = %d, want 0", len(lesserItems))
	}
}

func newTestStore(t *testing.T) (*Store, *fakedb.Fake) {
	t.Helper()
	t.Setenv(EnvInstanceContentTable, "body-instance-content-test")
	t.Setenv("LESSER_TABLE_NAME", "lesser-stage-table-test")

	fake := fakedb.New()
	db, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	store, err := NewStore(db, os.Getenv(EnvInstanceContentTable))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := db.CreateTable(store.emptyRecord()); err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	return store, fake
}

func loadSoulHistory(t *testing.T, store *Store, account, agentID string) []historyRecord {
	t.Helper()
	var history []historyRecord
	err := store.db.Model(&historyRecord{tableName: store.tableName}).
		Where("PK", "=", contentPartitionKey(account, agentID)).
		Where("SK", "begins_with", contentSKPrefix+string(ContentTypeAgentSoul)+"#HISTORY#").
		All(&history)
	if err != nil {
		t.Fatalf("load soul history: %v", err)
	}
	return history
}
