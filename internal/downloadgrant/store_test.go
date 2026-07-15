package downloadgrant

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"
)

func TestIssuePersistsTokenHashOnlyAndTTL(t *testing.T) {
	store, fake := newTestStore(t)
	fixedNow := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }

	issued, err := store.Issue(context.Background(), IssueInput{Binding: testBinding()})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.GrantID == "" || !strings.HasPrefix(issued.GrantID, grantIDPrefix) {
		t.Fatalf("GrantID = %q, want generated %s*", issued.GrantID, grantIDPrefix)
	}
	if issued.Token == "" {
		t.Fatal("Issue() returned empty raw token")
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(issued.Token)
	if err != nil {
		t.Fatalf("raw token is not base64url: %v", err)
	}
	if len(tokenBytes) != rawTokenRandomBytes {
		t.Fatalf("raw token entropy bytes = %d, want %d", len(tokenBytes), rawTokenRandomBytes)
	}
	wantExpiry := fixedNow.Add(DefaultTTL).Unix()
	if issued.ExpiresAtEpoch != wantExpiry || issued.ExpiresAt.Unix() != wantExpiry {
		t.Fatalf("expiry = %d/%s, want epoch %d", issued.ExpiresAtEpoch, issued.ExpiresAt, wantExpiry)
	}
	if issued.Binding != mustNormalizeBinding(t, testBinding()) {
		t.Fatalf("issued binding = %+v, want normalized %+v", issued.Binding, mustNormalizeBinding(t, testBinding()))
	}

	items := fake.Items(os.Getenv(EnvInstanceGrantTable))
	if len(items) != 1 {
		t.Fatalf("grant table items = %d, want 1", len(items))
	}
	item := items[0]
	if _, ok := item["tokenHash"]; !ok {
		t.Fatalf("persisted item missing tokenHash: keys=%v", sortedKeys(item))
	}
	for _, forbidden := range []string{"token", "rawToken", "plainToken", "plaintextToken"} {
		if _, ok := item[forbidden]; ok {
			t.Fatalf("persisted item contains forbidden raw token field %q: keys=%v", forbidden, sortedKeys(item))
		}
	}
	if got := attrString(t, item, "tokenHash"); got == issued.Token || !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("tokenHash = %q, want sha256 hash distinct from raw token", got)
	}
	if attrNumber(t, item, "expiresAt") != wantExpiry {
		t.Fatalf("persisted expiresAt = %d, want %d", attrNumber(t, item, "expiresAt"), wantExpiry)
	}
	if rawValuePresent(item, issued.Token) {
		t.Fatalf("persisted item leaked raw token value")
	}
}

func TestHashTokenIsDeterministicAndDomainSeparated(t *testing.T) {
	first, err := HashToken("opaque-token")
	if err != nil {
		t.Fatalf("HashToken() error = %v", err)
	}
	second, err := HashToken("opaque-token")
	if err != nil {
		t.Fatalf("HashToken() second error = %v", err)
	}
	other, err := HashToken("opaque-token-2")
	if err != nil {
		t.Fatalf("HashToken() other error = %v", err)
	}
	if first != second {
		t.Fatalf("HashToken deterministic mismatch: %q != %q", first, second)
	}
	if first == other || first == "opaque-token" || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("HashToken = %q other=%q, want sha256 hash distinct from token and other tokens", first, other)
	}
	if _, err := HashToken(" "); !errors.Is(err, ErrRawTokenRequired) {
		t.Fatalf("HashToken(blank) error = %v, want ErrRawTokenRequired", err)
	}
}

func TestConsumeTransitionsOnceAndReplayClassifies(t *testing.T) {
	store, _ := newTestStore(t)
	issuedAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return issuedAt }
	issued, err := store.Issue(context.Background(), IssueInput{Binding: testBinding()})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	consumeAt := issuedAt.Add(2 * time.Minute)
	store.now = func() time.Time { return consumeAt }
	first, err := store.Consume(context.Background(), ConsumeInput{GrantID: issued.GrantID, Token: issued.Token, Binding: testBinding()})
	if err != nil {
		t.Fatalf("Consume(first) error = %v", err)
	}
	if first.Outcome != ConsumeOutcomeConsumed || first.Grant == nil || first.Grant.Status != GrantStatusConsumed {
		t.Fatalf("Consume(first) = %+v, want consumed grant", first)
	}
	if !first.Grant.ConsumedAt.Equal(consumeAt) {
		t.Fatalf("ConsumedAt = %s, want %s", first.Grant.ConsumedAt, consumeAt)
	}

	replay, err := store.Consume(context.Background(), ConsumeInput{GrantID: issued.GrantID, Token: issued.Token, Binding: testBinding()})
	if err != nil {
		t.Fatalf("Consume(replay) error = %v", err)
	}
	if replay.Outcome != ConsumeOutcomeReplay || replay.Grant == nil || replay.Grant.Status != GrantStatusConsumed {
		t.Fatalf("Consume(replay) = %+v, want replay consumed classification", replay)
	}
	if strings.Contains(fmt.Sprintf("%+v", replay), issued.Token) || strings.Contains(fmt.Sprintf("%+v", replay), "sha256:") {
		t.Fatalf("replay result leaked raw token or token hash: %+v", replay)
	}
}

func TestConsumeFailsClosedForUnknownTokenBindingAndExpiry(t *testing.T) {
	store, fake := newTestStore(t)
	issuedAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return issuedAt }
	issued, err := store.Issue(context.Background(), IssueInput{Binding: testBinding()})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	unknown, err := store.Consume(context.Background(), ConsumeInput{GrantID: "missing-grant", Token: issued.Token, Binding: testBinding()})
	if err != nil {
		t.Fatalf("Consume(unknown) error = %v", err)
	}
	if unknown.Outcome != ConsumeOutcomeNotFound {
		t.Fatalf("Consume(unknown) = %+v, want not_found", unknown)
	}

	wrongToken := issued.Token[:len(issued.Token)-1] + "x"
	tokenMismatch, err := store.Consume(context.Background(), ConsumeInput{GrantID: issued.GrantID, Token: wrongToken, Binding: testBinding()})
	if err != nil {
		t.Fatalf("Consume(wrong token) error = %v", err)
	}
	if tokenMismatch.Outcome != ConsumeOutcomeTokenMismatch || tokenMismatch.Grant != nil {
		t.Fatalf("Consume(wrong token) = %+v, want token_mismatch without grant details", tokenMismatch)
	}
	if strings.Contains(fmt.Sprintf("%+v", tokenMismatch), wrongToken) || strings.Contains(fmt.Sprintf("%+v", tokenMismatch), "sha256:") {
		t.Fatalf("token mismatch result leaked token material: %+v", tokenMismatch)
	}
	assertRecordStatus(t, store, issued.GrantID, GrantStatusActive)

	wrongBinding := testBinding()
	wrongBinding.Client = "claude_code"
	bindingMismatch, err := store.Consume(context.Background(), ConsumeInput{GrantID: issued.GrantID, Token: issued.Token, Binding: wrongBinding})
	if err != nil {
		t.Fatalf("Consume(wrong binding) error = %v", err)
	}
	if bindingMismatch.Outcome != ConsumeOutcomeBindingMismatch || bindingMismatch.Grant != nil {
		t.Fatalf("Consume(wrong binding) = %+v, want binding_mismatch without grant details", bindingMismatch)
	}
	assertRecordStatus(t, store, issued.GrantID, GrantStatusActive)

	store.now = func() time.Time { return issuedAt.Add(DefaultTTL + time.Second) }
	expired, err := store.Consume(context.Background(), ConsumeInput{GrantID: issued.GrantID, Token: issued.Token, Binding: testBinding()})
	if err != nil {
		t.Fatalf("Consume(expired) error = %v", err)
	}
	if expired.Outcome != ConsumeOutcomeExpired || expired.Grant == nil || expired.Grant.Status != GrantStatusActive {
		t.Fatalf("Consume(expired) = %+v, want expired active grant classification", expired)
	}
	assertRecordStatus(t, store, issued.GrantID, GrantStatusActive)

	items := fake.Items(os.Getenv(EnvInstanceGrantTable))
	if rawValuePresent(items[0], issued.Token) || rawValuePresent(items[0], wrongToken) {
		t.Fatalf("grant table leaked raw token after failed consumes")
	}
}

func TestConcurrentConsumeExactlyOneWinner(t *testing.T) {
	store, _ := newTestStore(t)
	issuedAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return issuedAt }
	issued, err := store.Issue(context.Background(), IssueInput{Binding: testBinding()})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	store.now = func() time.Time { return issuedAt.Add(time.Minute) }

	const attempts = 32
	start := make(chan struct{})
	outcomes := make(chan ConsumeOutcome, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			<-start
			result, err := store.Consume(context.Background(), ConsumeInput{GrantID: issued.GrantID, Token: issued.Token, Binding: testBinding()})
			if err != nil {
				errs <- err
				return
			}
			outcomes <- result.Outcome
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Consume() error = %v", err)
		}
	}

	counts := map[ConsumeOutcome]int{}
	for outcome := range outcomes {
		counts[outcome]++
	}
	if counts[ConsumeOutcomeConsumed] != 1 {
		t.Fatalf("consumed winners = %d outcomes=%v, want exactly 1", counts[ConsumeOutcomeConsumed], counts)
	}
	if counts[ConsumeOutcomeReplay] != attempts-1 {
		t.Fatalf("replay losers = %d outcomes=%v, want %d", counts[ConsumeOutcomeReplay], counts, attempts-1)
	}
	assertRecordStatus(t, store, issued.GrantID, GrantStatusConsumed)
}

func TestInvalidInputsAreSanitized(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Issue(context.Background(), IssueInput{}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Issue(empty binding) error = %v, want ErrInvalidBinding", err)
	}
	if _, err := store.Consume(context.Background(), ConsumeInput{GrantID: " grant-1 ", Token: " raw-secret-token ", Binding: Binding{Account: "account"}}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Consume(invalid binding) error = %v, want ErrInvalidBinding", err)
	} else if strings.Contains(err.Error(), "raw-secret-token") || strings.Contains(err.Error(), "sha256:") {
		t.Fatalf("Consume(invalid binding) leaked token material in error: %v", err)
	}
	if _, err := store.Consume(context.Background(), ConsumeInput{Binding: testBinding(), Token: "raw-secret-token"}); !errors.Is(err, ErrGrantIDRequired) {
		t.Fatalf("Consume(missing grant id) error = %v, want ErrGrantIDRequired", err)
	} else if strings.Contains(err.Error(), "raw-secret-token") || strings.Contains(err.Error(), "sha256:") {
		t.Fatalf("Consume(missing grant id) leaked token material in error: %v", err)
	}
}

func newTestStore(t *testing.T) (*Store, *fakedb.Fake) {
	t.Helper()
	t.Setenv(EnvInstanceGrantTable, "body-instance-grants-test")
	t.Setenv("LESSER_TABLE_NAME", "lesser-stage-table-test")

	fake := fakedb.New()
	db, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	store, err := NewStore(db, os.Getenv(EnvInstanceGrantTable))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := db.CreateTable(store.emptyRecord()); err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	return store, fake
}

func testBinding() Binding {
	return Binding{
		Account:    " Account-A ",
		Actor:      " Agent-One ",
		Namespace:  " EqualToAI ",
		Route:      " /instance/ba/mcp ",
		Client:     " Codex ",
		Profile:    " Dev ",
		PackID:     "ba-install-pack-codex",
		PackDigest: "SHA256:ABC123",
	}
}

func mustNormalizeBinding(t *testing.T, binding Binding) Binding {
	t.Helper()
	out, err := normalizeBinding(binding)
	if err != nil {
		t.Fatalf("normalizeBinding() error = %v", err)
	}
	return out
}

func assertRecordStatus(t *testing.T, store *Store, grantID string, want GrantStatus) {
	t.Helper()
	record, err := store.loadRecord(context.Background(), grantID)
	if err != nil {
		t.Fatalf("loadRecord(%s) error = %v", grantID, err)
	}
	if record.Status != string(want) {
		t.Fatalf("record status = %q, want %q", record.Status, want)
	}
}

func attrString(t *testing.T, item map[string]types.AttributeValue, name string) string {
	t.Helper()
	member, ok := item[name].(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("attribute %s = %#v, want string", name, item[name])
	}
	return member.Value
}

func attrNumber(t *testing.T, item map[string]types.AttributeValue, name string) int64 {
	t.Helper()
	member, ok := item[name].(*types.AttributeValueMemberN)
	if !ok {
		t.Fatalf("attribute %s = %#v, want number", name, item[name])
	}
	value, err := strconv.ParseInt(member.Value, 10, 64)
	if err != nil {
		t.Fatalf("parse number %s=%q: %v", name, member.Value, err)
	}
	return value
}

func rawValuePresent(item map[string]types.AttributeValue, raw string) bool {
	for _, value := range item {
		if attributeContainsRaw(value, raw) {
			return true
		}
	}
	return false
}

func attributeContainsRaw(value types.AttributeValue, raw string) bool {
	switch v := value.(type) {
	case *types.AttributeValueMemberS:
		return v.Value == raw
	case *types.AttributeValueMemberSS:
		for _, s := range v.Value {
			if s == raw {
				return true
			}
		}
	case *types.AttributeValueMemberM:
		return rawValuePresent(v.Value, raw)
	case *types.AttributeValueMemberL:
		for _, child := range v.Value {
			if attributeContainsRaw(child, raw) {
				return true
			}
		}
	}
	return false
}

func sortedKeys(item map[string]types.AttributeValue) []string {
	keys := make([]string, 0, len(item))
	for key := range item {
		keys = append(keys, key)
	}
	// Tiny insertion sort avoids pulling in another helper just for test failure output.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
