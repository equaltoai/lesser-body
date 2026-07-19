package downloadgrant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/theory-cloud/tabletheory/v2"
	tablecore "github.com/theory-cloud/tabletheory/v2/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
)

const (
	// EnvInstanceGrantTable is the body-owned instance-plane grant table. It is
	// provisioned by this repo's CDK stack as INSTANCE_GRANT_TABLE and must not
	// be confused with Lesser's LESSER_TABLE_NAME actor data table.
	EnvInstanceGrantTable = "INSTANCE_GRANT_TABLE"

	envAWSRegion = "AWS_REGION"

	DefaultTTL = 10 * time.Minute

	grantPKPrefix = "DOWNLOAD_GRANT#"
	grantSK       = "GRANT"

	grantIDPrefix        = "dg_"
	grantIDRandomBytes   = 16
	rawTokenRandomBytes  = 32
	tokenHashDomainBytes = "lesser-body/downloadgrant/token/v1\x00"
)

type GrantStatus string

const (
	GrantStatusActive   GrantStatus = "active"
	GrantStatusConsumed GrantStatus = "consumed"
)

type ConsumeOutcome string

const (
	ConsumeOutcomeConsumed        ConsumeOutcome = "consumed"
	ConsumeOutcomeReplay          ConsumeOutcome = "replay"
	ConsumeOutcomeNotFound        ConsumeOutcome = "not_found"
	ConsumeOutcomeExpired         ConsumeOutcome = "expired"
	ConsumeOutcomeTokenMismatch   ConsumeOutcome = "token_mismatch"
	ConsumeOutcomeBindingMismatch ConsumeOutcome = "binding_mismatch"
)

var (
	ErrGrantNotFound      = errors.New("download grant not found")
	ErrGrantIDRequired    = errors.New("download grant id is required")
	ErrRawTokenRequired   = errors.New("download grant token is required")
	ErrInvalidBinding     = errors.New("download grant binding is invalid")
	ErrInvalidGrantStatus = errors.New("download grant status is invalid")
)

// Binding describes the context a one-time Ba download/install grant is bound
// to. Later redemption and local-install planning must supply the same binding
// before a grant can be consumed.
type Binding struct {
	Account    string
	Actor      string
	Namespace  string
	Route      string
	Client     string
	Profile    string
	PackID     string
	PackDigest string
}

// IssueInput describes a new one-time download grant. TTL defaults to
// DefaultTTL when left unset.
type IssueInput struct {
	Binding Binding
	TTL     time.Duration
}

// IssuedGrant is returned exactly once to the service layer that creates a
// grant. Token is intentionally absent from persisted records.
type IssuedGrant struct {
	Binding        Binding
	GrantID        string
	Token          string
	ExpiresAt      time.Time
	ExpiresAtEpoch int64
}

// ConsumeInput describes an attempted one-time grant consume.
type ConsumeInput struct {
	Binding Binding
	GrantID string
	Token   string
}

// ConsumeResult classifies a consume attempt without exposing token hashes or
// raw token material. GrantID is safe for diagnostics; Token is never included.
type ConsumeResult struct {
	Grant   *Grant
	GrantID string
	Outcome ConsumeOutcome
}

// Grant is the sanitized domain view returned by the store. It intentionally
// omits the persisted tokenHash.
type Grant struct {
	CreatedAt      time.Time
	ConsumedAt     time.Time
	ExpiresAt      time.Time
	Binding        Binding
	GrantID        string
	Status         GrantStatus
	ExpiresAtEpoch int64
}

// Store persists short-lived Ba download grants in a body-owned instance table.
type Store struct {
	db        tablecore.DB
	tableName string
	now       func() time.Time
}

// NewStore constructs a Store over an injected TableTheory DB. tableName should
// be the configured INSTANCE_GRANT_TABLE value.
func NewStore(db tablecore.DB, tableName string) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("download grant db is required")
	}
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		return nil, fmt.Errorf("%s is required", EnvInstanceGrantTable)
	}
	return &Store{db: db, tableName: tableName, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Default creates the production TableTheory-backed grant store from process
// configuration.
func Default() (*Store, error) {
	tableName := strings.TrimSpace(os.Getenv(EnvInstanceGrantTable))
	if tableName == "" {
		return nil, fmt.Errorf("%s is required", EnvInstanceGrantTable)
	}

	db, err := tabletheory.NewBasic(session.Config{Region: os.Getenv(envAWSRegion)})
	if err != nil {
		return nil, fmt.Errorf("create tabletheory client: %w", err)
	}
	return NewStore(db, tableName)
}

// Issue creates a short-lived active grant and returns the raw opaque token once.
// The raw token is never stored by this package.
func (s *Store) Issue(ctx context.Context, in IssueInput) (*IssuedGrant, error) {
	if s == nil {
		return nil, fmt.Errorf("download grant store is nil")
	}
	binding, err := normalizeBinding(in.Binding)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ttl := in.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := s.clock()
	expiresAt := now.Add(ttl).UTC()
	rawToken, err := GenerateToken()
	if err != nil {
		return nil, fmt.Errorf("generate download grant token: %w", err)
	}
	tokenHash, err := HashToken(rawToken)
	if err != nil {
		return nil, err
	}
	grantID, err := GenerateGrantID()
	if err != nil {
		return nil, fmt.Errorf("generate download grant id: %w", err)
	}

	record := s.recordFor(grantID)
	record.GrantID = grantID
	record.Status = string(GrantStatusActive)
	record.TokenHash = tokenHash
	record.Account = binding.Account
	record.Actor = binding.Actor
	record.Namespace = binding.Namespace
	record.Route = binding.Route
	record.Client = binding.Client
	record.Profile = binding.Profile
	record.PackID = binding.PackID
	record.PackDigest = binding.PackDigest
	record.GrantCreatedAt = now
	record.ExpiresAt = expiresAt.Unix()

	if err := s.db.Model(record).WithContext(ctx).IfNotExists().Create(); err != nil {
		return nil, fmt.Errorf("create download grant %s: %w", grantID, err)
	}

	return &IssuedGrant{
		Binding:        binding,
		GrantID:        grantID,
		Token:          rawToken,
		ExpiresAt:      expiresAt,
		ExpiresAtEpoch: expiresAt.Unix(),
	}, nil
}

// Consume atomically transitions a matching active grant to consumed. The
// TableTheory conditional update requires status=active, matching token hash,
// matching binding, and a future expiresAt epoch.
func (s *Store) Consume(ctx context.Context, in ConsumeInput) (*ConsumeResult, error) {
	if s == nil {
		return nil, fmt.Errorf("download grant store is nil")
	}
	grantID := strings.TrimSpace(in.GrantID)
	if grantID == "" {
		return nil, ErrGrantIDRequired
	}
	binding, err := normalizeBinding(in.Binding)
	if err != nil {
		return nil, err
	}
	tokenHash, err := HashToken(in.Token)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := s.clock()
	updated := s.emptyRecord()
	err = s.db.Model(s.emptyRecord()).
		WithContext(ctx).
		Where("PK", "=", grantPartitionKey(grantID)).
		Where("SK", "=", grantSK).
		UpdateBuilder().
		Set("Status", string(GrantStatusConsumed)).
		Set("GrantConsumedAt", now).
		Condition("Status", "=", string(GrantStatusActive)).
		Condition("TokenHash", "=", tokenHash).
		Condition("Account", "=", binding.Account).
		Condition("Actor", "=", binding.Actor).
		Condition("Namespace", "=", binding.Namespace).
		Condition("Route", "=", binding.Route).
		Condition("Client", "=", binding.Client).
		Condition("Profile", "=", binding.Profile).
		Condition("PackID", "=", binding.PackID).
		Condition("PackDigest", "=", binding.PackDigest).
		Condition("ExpiresAt", ">", now.Unix()).
		ReturnValues("ALL_NEW").
		ExecuteWithResult(updated)
	switch {
	case err == nil:
		grant, err := updated.toGrant()
		if err != nil {
			return nil, err
		}
		return &ConsumeResult{GrantID: grantID, Outcome: ConsumeOutcomeConsumed, Grant: grant}, nil
	case !tableerrors.IsConditionFailed(err):
		return nil, fmt.Errorf("consume download grant %s: %w", grantID, err)
	}

	return s.classifyFailedConsume(ctx, grantID, tokenHash, binding, now)
}

func (s *Store) classifyFailedConsume(ctx context.Context, grantID string, tokenHash string, binding Binding, now time.Time) (*ConsumeResult, error) {
	record, err := s.loadRecord(ctx, grantID)
	switch {
	case err == nil:
		// Continue below.
	case errors.Is(err, ErrGrantNotFound):
		return &ConsumeResult{GrantID: grantID, Outcome: ConsumeOutcomeNotFound}, nil
	default:
		return nil, err
	}

	if !hashEqual(record.TokenHash, tokenHash) {
		return &ConsumeResult{GrantID: grantID, Outcome: ConsumeOutcomeTokenMismatch}, nil
	}
	if !record.bindingEqual(binding) {
		return &ConsumeResult{GrantID: grantID, Outcome: ConsumeOutcomeBindingMismatch}, nil
	}
	status := GrantStatus(record.Status)
	if status == GrantStatusConsumed {
		grant, err := record.toGrant()
		if err != nil {
			return nil, err
		}
		return &ConsumeResult{GrantID: grantID, Outcome: ConsumeOutcomeReplay, Grant: grant}, nil
	}
	if now.Unix() >= record.ExpiresAt {
		grant, err := record.toGrant()
		if err != nil {
			return nil, err
		}
		return &ConsumeResult{GrantID: grantID, Outcome: ConsumeOutcomeExpired, Grant: grant}, nil
	}
	return &ConsumeResult{GrantID: grantID, Outcome: ConsumeOutcomeBindingMismatch}, nil
}

func (s *Store) loadRecord(ctx context.Context, grantID string) (*grantRecord, error) {
	record := s.emptyRecord()
	err := s.db.Model(s.emptyRecord()).
		WithContext(ctx).
		Where("PK", "=", grantPartitionKey(grantID)).
		Where("SK", "=", grantSK).
		First(record)
	switch {
	case err == nil:
		return record, nil
	case tableerrors.IsNotFound(err):
		return nil, ErrGrantNotFound
	default:
		return nil, fmt.Errorf("get download grant %s: %w", grantID, err)
	}
}

func (s *Store) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) emptyRecord() *grantRecord {
	return &grantRecord{tableName: s.tableName}
}

func (s *Store) recordFor(grantID string) *grantRecord {
	grantID = strings.TrimSpace(grantID)
	return &grantRecord{
		tableName: s.tableName,
		PK:        grantPartitionKey(grantID),
		SK:        grantSK,
		GrantID:   grantID,
	}
}

type grantRecord struct {
	tableName string

	PK string `theorydb:"pk,attr:pk" json:"pk"`
	SK string `theorydb:"sk,attr:sk" json:"sk"`

	GrantCreatedAt  time.Time `theorydb:"attr:createdAt" json:"created_at"`
	GrantConsumedAt time.Time `theorydb:"attr:consumedAt" json:"consumed_at"`
	GrantID         string    `theorydb:"attr:grantId" json:"grant_id"`
	Status          string    `theorydb:"attr:status" json:"status"`
	TokenHash       string    `theorydb:"attr:tokenHash" json:"token_hash"`
	Account         string    `theorydb:"attr:account" json:"account"`
	Actor           string    `theorydb:"attr:actor" json:"actor"`
	Namespace       string    `theorydb:"attr:namespace" json:"namespace"`
	Route           string    `theorydb:"attr:route" json:"route"`
	Client          string    `theorydb:"attr:client" json:"client"`
	Profile         string    `theorydb:"attr:profile" json:"profile"`
	PackID          string    `theorydb:"attr:packId" json:"pack_id"`
	PackDigest      string    `theorydb:"attr:packDigest" json:"pack_digest"`
	ExpiresAt       int64     `theorydb:"attr:expiresAt" json:"expires_at"`
}

func (r grantRecord) TableName() string {
	if tableName := strings.TrimSpace(r.tableName); tableName != "" {
		return tableName
	}
	return strings.TrimSpace(os.Getenv(EnvInstanceGrantTable))
}

func (r *grantRecord) toGrant() (*Grant, error) {
	if r == nil {
		return nil, nil
	}
	binding, err := normalizeBinding(Binding{
		Account:    r.Account,
		Actor:      r.Actor,
		Namespace:  r.Namespace,
		Route:      r.Route,
		Client:     r.Client,
		Profile:    r.Profile,
		PackID:     r.PackID,
		PackDigest: r.PackDigest,
	})
	if err != nil {
		return nil, err
	}
	status, err := normalizeStatus(GrantStatus(r.Status))
	if err != nil {
		return nil, err
	}
	return &Grant{
		Binding:        binding,
		GrantID:        strings.TrimSpace(r.GrantID),
		Status:         status,
		CreatedAt:      r.GrantCreatedAt.UTC(),
		ConsumedAt:     r.GrantConsumedAt.UTC(),
		ExpiresAt:      time.Unix(r.ExpiresAt, 0).UTC(),
		ExpiresAtEpoch: r.ExpiresAt,
	}, nil
}

func (r *grantRecord) bindingEqual(binding Binding) bool {
	if r == nil {
		return false
	}
	return r.Account == binding.Account &&
		r.Actor == binding.Actor &&
		r.Namespace == binding.Namespace &&
		r.Route == binding.Route &&
		r.Client == binding.Client &&
		r.Profile == binding.Profile &&
		r.PackID == binding.PackID &&
		r.PackDigest == binding.PackDigest
}

func normalizeBinding(binding Binding) (Binding, error) {
	out := Binding{
		Account:    strings.ToLower(strings.TrimSpace(binding.Account)),
		Actor:      strings.ToLower(strings.TrimSpace(binding.Actor)),
		Namespace:  strings.ToLower(strings.TrimSpace(binding.Namespace)),
		Route:      strings.TrimSpace(binding.Route),
		Client:     strings.ToLower(strings.TrimSpace(binding.Client)),
		Profile:    strings.ToLower(strings.TrimSpace(binding.Profile)),
		PackID:     strings.TrimSpace(binding.PackID),
		PackDigest: strings.ToLower(strings.TrimSpace(binding.PackDigest)),
	}
	if out.Account == "" || out.Actor == "" || out.Namespace == "" || out.Route == "" || out.Client == "" || out.Profile == "" || out.PackID == "" || out.PackDigest == "" {
		return Binding{}, ErrInvalidBinding
	}
	return out, nil
}

func normalizeStatus(status GrantStatus) (GrantStatus, error) {
	switch GrantStatus(strings.ToLower(strings.TrimSpace(string(status)))) {
	case GrantStatusActive:
		return GrantStatusActive, nil
	case GrantStatusConsumed:
		return GrantStatusConsumed, nil
	default:
		return "", ErrInvalidGrantStatus
	}
}

func grantPartitionKey(grantID string) string {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return ""
	}
	return grantPKPrefix + grantID
}

// HashToken returns a deterministic, domain-separated SHA-256 hash for an
// opaque raw grant token. The raw token itself must never be stored or logged.
func HashToken(rawToken string) (string, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return "", ErrRawTokenRequired
	}
	h := sha256.New()
	_, _ = h.Write([]byte(tokenHashDomainBytes))
	_, _ = h.Write([]byte(rawToken))
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// GenerateToken returns a high-entropy opaque token suitable for one-time
// return to a caller.
func GenerateToken() (string, error) {
	return randomBase64(rawTokenRandomBytes)
}

// GenerateGrantID returns an opaque grant identifier. Grant IDs are safe to
// include in sanitized diagnostics.
func GenerateGrantID() (string, error) {
	buf := make([]byte, grantIDRandomBytes)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return grantIDPrefix + hex.EncodeToString(buf), nil
}

func randomBase64(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashEqual(a string, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
