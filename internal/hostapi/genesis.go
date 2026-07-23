package hostapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-body/internal/soulapi"
)

const (
	// AuthorityModelInstanceTrust is the Host-owned authority model used by the
	// instance-plane genesis flow. The Host creates the registration and
	// derives the agent identity; Body does not create a local substitute.
	AuthorityModelInstanceTrust = "instance_trust"

	instanceRegistrationBeginPath = "/api/v1/soul/instance/agents/register/begin"
	instanceRegistrationPath      = "/api/v1/soul/instance/agents/register/"
	instanceAgentPath             = "/api/v1/soul/instance/agents/"
)

// JSONDoer is the small subset of the Soul/Host HTTP client needed by this
// package. Keeping the seam here lets tests prove the exact Host contract
// without constructing a second HTTP stack or using cloud state.
type JSONDoer interface {
	DoJSON(ctx context.Context, method string, path string, query url.Values, bearerToken string, body any) (any, error)
}

// GenesisClient is the Host-backed registration and mint-conversation
// contract consumed by Ptah. The returned maps are Host projections; callers
// must apply an output allowlist before returning them to an MCP client.
type GenesisClient interface {
	BeginRegistration(ctx context.Context, bearerToken string, req RegistrationBeginRequest) (map[string]any, error)
	ListConversations(ctx context.Context, bearerToken string, agentID string, limit int) (map[string]any, error)
	AdvanceConversation(ctx context.Context, bearerToken string, registrationID string, req MintConversationRequest) (map[string]any, error)
	ReadConversation(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error)
	RecoverConversation(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error)
	FinalizePreflight(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error)
	FinalizeConversation(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error)
}

// RegistrationBeginRequest is the no-wallet instance-trust registration
// request accepted by lesser-host. Wallet-bearing registration is deliberately
// not represented in this type: Ptah's instance-owner flow is Host-backed
// instance trust, not a local wallet-signing ceremony.
type RegistrationBeginRequest struct {
	Domain       string   `json:"domain"`
	LocalID      string   `json:"local_id"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// MintConversationRequest is the user turn submitted to Host's durable
// hosted-genesis conversation. ConversationID is empty only for the first
// turn; Host then returns the durable ID which callers must persist and reuse.
type MintConversationRequest struct {
	ConversationID  string                      `json:"conversation_id,omitempty"`
	Model           string                      `json:"model,omitempty"`
	Message         string                      `json:"message"`
	CandidateAction *DeclarationCandidateAction `json:"candidate_action,omitempty"`
	IdempotencyKey  string                      `json:"idempotency_key,omitempty"`
	CorrelationID   string                      `json:"correlation_id,omitempty"`
	LesserRequestID string                      `json:"lesser_request_id,omitempty"`
}

// DeclarationCandidateAction is the Host-owned structural owner decision for
// the exact declaration review currently projected by HostedGenesisSession.
// Body validates and relays these bindings; it never infers an action from
// prose or recomputes candidate truth.
type DeclarationCandidateAction struct {
	Action            string `json:"action"`
	Section           string `json:"section,omitempty"`
	CandidateRevision int64  `json:"candidate_revision"`
	CandidateHash     string `json:"candidate_hash"`
	ReviewHash        string `json:"review_hash"`

	decoded                  bool
	sectionPresent           bool
	actionPresent            bool
	candidateRevisionPresent bool
	candidateHashPresent     bool
	reviewHashPresent        bool
}

var declarationCandidateHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// UnmarshalJSON rejects unknown, missing, null, malformed, and conditionally
// invalid action objects before a Host request can be issued.
func (a *DeclarationCandidateAction) UnmarshalJSON(data []byte) error {
	if a == nil {
		return errors.New("candidate action is nil")
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return fmt.Errorf("candidate action must be an object: %w", err)
	}
	if fields == nil {
		return errors.New("candidate action must be an object")
	}
	allowed := map[string]bool{
		"action": true, "section": true, "candidate_revision": true,
		"candidate_hash": true, "review_hash": true,
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown candidate action field %q", key)
		}
	}
	*a = DeclarationCandidateAction{decoded: true}
	decodeString := func(key string, dst *string, present *bool) error {
		raw, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("candidate action %s is required", key)
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return fmt.Errorf("candidate action %s must be a string", key)
		}
		*present = true
		return nil
	}
	if err := decodeString("action", &a.Action, &a.actionPresent); err != nil {
		return err
	}
	if raw, ok := fields["section"]; ok {
		a.sectionPresent = true
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &a.Section) != nil {
			return errors.New("candidate action section must be a string")
		}
	}
	if raw, ok := fields["candidate_revision"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		revision, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return errors.New("candidate action candidate_revision must be a non-negative integer")
		}
		a.CandidateRevision = revision
		a.candidateRevisionPresent = true
	} else {
		return errors.New("candidate action candidate_revision is required")
	}
	if err := decodeString("candidate_hash", &a.CandidateHash, &a.candidateHashPresent); err != nil {
		return err
	}
	if err := decodeString("review_hash", &a.ReviewHash, &a.reviewHashPresent); err != nil {
		return err
	}
	return a.Validate()
}

// Validate enforces Host PR #978's candidate-action contract without
// normalizing any value.
func (a DeclarationCandidateAction) Validate() error {
	if a.decoded && (!a.actionPresent || !a.candidateRevisionPresent || !a.candidateHashPresent || !a.reviewHashPresent) {
		return errors.New("candidate action is missing required bindings")
	}
	if a.Action != "affirm" && a.Action != "edit" {
		return errors.New("candidate action must be affirm or edit")
	}
	if a.CandidateRevision < 0 {
		return errors.New("candidate action candidate_revision must be non-negative")
	}
	if !declarationCandidateHashPattern.MatchString(a.CandidateHash) || !declarationCandidateHashPattern.MatchString(a.ReviewHash) {
		return errors.New("candidate action hashes must be exact sha256 identifiers")
	}
	if a.Action == "affirm" {
		if a.Section != "" || a.sectionPresent {
			return errors.New("candidate action section is forbidden for affirm")
		}
		return nil
	}
	if !validDeclarationCandidateSection(a.Section) {
		return errors.New("candidate action section is required for edit")
	}
	return nil
}

func validDeclarationCandidateSection(section string) bool {
	switch section {
	case "identity", "philosophy", "discipline", "boundaries", "soul":
		return true
	default:
		return false
	}
}

// APIError is a sanitized Host HTTP failure. Body deliberately does not
// retain upstream response bytes: Host error payloads can contain private
// conversation or credential-adjacent material and are never suitable for an
// MCP error result.
type APIError struct {
	Status int
	Code   string
}

func (e *APIError) Error() string {
	if e == nil {
		return "lesser-host api error"
	}
	if e.Status > 0 {
		return fmt.Sprintf("lesser-host api request failed (status=%d)", e.Status)
	}
	return "lesser-host api request failed"
}

// New creates a Host genesis client over an existing Soul API client.
func New(client JSONDoer) *Client {
	return &Client{client: client}
}

// Client is the thin HTTP adapter for lesser-host's instance genesis routes.
type Client struct {
	client JSONDoer
}

// Default returns a Host client using the repository's existing Soul API
// endpoint resolution. It does not create a separate endpoint or credential
// path for Ptah.
func Default() (*Client, error) {
	client, err := soulapi.Default()
	if err != nil {
		return nil, err
	}
	return New(client), nil
}

func (c *Client) BeginRegistration(ctx context.Context, bearerToken string, req RegistrationBeginRequest) (map[string]any, error) {
	req.Domain = strings.TrimSpace(req.Domain)
	req.LocalID = strings.TrimSpace(req.LocalID)
	if req.Domain == "" || req.LocalID == "" {
		return nil, errors.New("host genesis registration domain and local id are required")
	}
	body := map[string]any{
		"domain":          req.Domain,
		"local_id":        req.LocalID,
		"authority_model": AuthorityModelInstanceTrust,
	}
	if len(req.Capabilities) > 0 {
		caps := make([]string, 0, len(req.Capabilities))
		for _, capability := range req.Capabilities {
			if capability = strings.TrimSpace(capability); capability != "" {
				caps = append(caps, capability)
			}
		}
		if len(caps) > 0 {
			body["capabilities"] = caps
		}
	}
	return c.do(ctx, http.MethodPost, instanceRegistrationBeginPath, bearerToken, body)
}

func (c *Client) ListConversations(ctx context.Context, bearerToken string, agentID string, limit int) (map[string]any, error) {
	agentID, err := normalizePathID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	if limit < 0 || limit > 50 {
		return nil, errors.New("host genesis conversation list limit must be between 1 and 50")
	}
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	return c.doQuery(ctx, http.MethodGet, instanceAgentPath+url.PathEscape(agentID)+"/mint-conversations", query, bearerToken, nil)
}

func (c *Client) AdvanceConversation(ctx context.Context, bearerToken string, registrationID string, req MintConversationRequest) (map[string]any, error) {
	registrationID, err := normalizePathID(registrationID, "registration id")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Message) == "" {
		return nil, errors.New("host genesis conversation message is required")
	}
	if req.CandidateAction != nil {
		if err := req.CandidateAction.Validate(); err != nil {
			return nil, fmt.Errorf("host genesis candidate action is invalid: %w", err)
		}
	}
	return c.do(ctx, http.MethodPost, instanceRegistrationPath+url.PathEscape(registrationID)+"/mint-conversation", bearerToken, req)
}

func (c *Client) ReadConversation(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error) {
	path, err := conversationPath(registrationID, conversationID)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, path, bearerToken, nil)
}

func (c *Client) RecoverConversation(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error) {
	path, err := conversationPath(registrationID, conversationID)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, path+"/recover", bearerToken, map[string]any{})
}

func (c *Client) FinalizePreflight(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error) {
	path, err := conversationPath(registrationID, conversationID)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, path+"/finalize/preflight", bearerToken, map[string]any{})
}

func (c *Client) FinalizeConversation(ctx context.Context, bearerToken string, registrationID string, conversationID string) (map[string]any, error) {
	path, err := conversationPath(registrationID, conversationID)
	if err != nil {
		return nil, err
	}
	// Instance-trust finalization accepts an empty body. Body must not accept
	// or relay wallet signatures, self-attestations, or private declarations.
	return c.do(ctx, http.MethodPost, path+"/finalize", bearerToken, map[string]any{})
}

func (c *Client) do(ctx context.Context, method string, path string, bearerToken string, body any) (map[string]any, error) {
	return c.doQuery(ctx, method, path, nil, bearerToken, body)
}

func (c *Client) doQuery(ctx context.Context, method string, path string, query url.Values, bearerToken string, body any) (map[string]any, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("host genesis client is not configured")
	}
	bearerToken = strings.TrimSpace(bearerToken)
	if bearerToken == "" {
		return nil, errors.New("lesser-host instance key is required for genesis")
	}
	raw, err := c.client.DoJSON(ctx, method, path, query, bearerToken, body)
	if err != nil {
		return nil, sanitizeError(err)
	}
	out, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("lesser-host genesis response was not an object")
	}
	return out, nil
}

func conversationPath(registrationID string, conversationID string) (string, error) {
	registrationID, err := normalizePathID(registrationID, "registration id")
	if err != nil {
		return "", err
	}
	conversationID, err = normalizePathID(conversationID, "conversation id")
	if err != nil {
		return "", err
	}
	return instanceRegistrationPath + url.PathEscape(registrationID) + "/mint-conversation/" + url.PathEscape(conversationID), nil
}

func normalizePathID(value string, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("host genesis %s is required", label)
	}
	if len(value) > 128 {
		return "", fmt.Errorf("host genesis %s is too long", label)
	}
	return value, nil
}

func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *soulapi.APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return err
	}
	return &APIError{Status: apiErr.Status, Code: safeHostErrorCode(apiErr.Body)}
}

func safeHostErrorCode(body []byte) string {
	var envelope map[string]any
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	for _, candidate := range []map[string]any{envelope, objectValue(envelope, "error")} {
		code, _ := candidate["code"].(string)
		code = strings.TrimSpace(code)
		if isSafeHostErrorCode(code) {
			return code
		}
	}
	return ""
}

func objectValue(m map[string]any, key string) map[string]any {
	value, _ := m[key].(map[string]any)
	return value
}

func isSafeHostErrorCode(code string) bool {
	if code == "" {
		return false
	}
	if strings.HasPrefix(code, "soul_instance.") {
		return true
	}
	switch code {
	case "app.bad_request", "app.conflict", "app.not_found", "app.unauthorized", "app.forbidden", "app.internal", "app.microvm_unavailable", "app.assistant_turn_failed":
		return true
	default:
		return false
	}
}
