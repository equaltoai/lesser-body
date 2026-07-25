package instancex402

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	"github.com/equaltoai/lesser-body/internal/soulbinding"
	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

const (
	CapabilityVersionInstanceV1 = "instance-capability/v1"

	CapabilityInstallPlan = "instance:install_plan"

	ResourceInstallPlan = "instance://tools/agent_local_install_plan"

	ToolAgentLocalInstallPlan = "agent_local_install_plan"

	x402GrantHeader             = "lesser-x402-grant"
	x402GrantLegacyHeader       = "x-lesser-x402-grant"
	x402GrantIDHeader           = "lesser-x402-grant-id"
	x402GrantIDLegacyHeader     = "x-lesser-x402-grant-id"
	x402GrantCapabilityHeader   = "lesser-x402-capability"
	x402GrantCapabilityLegacy   = "x-lesser-x402-capability"
	x402PaymentSignatureHeader  = "payment-signature"
	x402LegacyPaymentHeader     = "x-payment"
	x402GrantConsumePathPrefix  = "/api/v1/soul/x402/grants/"
	x402GrantConsumePathSuffix  = "/consume"
	x402MaxEvidenceHeaderLength = 64 << 10
)

var soulAgentIDPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

// Requirement describes the host-authored instance capability a body instance
// tool must consume before side effects. The gate is intentionally local to
// instance-plane capability-gated tools; it does not grant OAuth principal
// authority.
type Requirement struct {
	Tool       string
	Capability string
	Resource   string
	Account    string
}

type consumeRequest struct {
	GrantID             string `json:"-"`
	GrantToken          string `json:"grantToken"`
	AgentID             string `json:"agentId"`
	CapabilityVersion   string `json:"capabilityVersion"`
	Capability          string `json:"capability"`
	Tool                string `json:"tool"`
	Resource            string `json:"resource"`
	RequestHash         string `json:"requestHash"`
	IdempotencyKey      string `json:"idempotencyKey"`
	PaymentEvidenceHash string `json:"paymentEvidenceHash"`
}

type consumeResponse struct {
	Accepted bool       `json:"accepted"`
	Replayed bool       `json:"replayed"`
	Grant    grantView  `json:"grant"`
	Usage    grantUsage `json:"usage"`
	Reason   string     `json:"reason,omitempty"`
}

type grantView struct {
	GrantID           string         `json:"grantId,omitempty"`
	AgentID           string         `json:"agentId,omitempty"`
	CapabilityVersion string         `json:"capabilityVersion,omitempty"`
	Capability        string         `json:"capability,omitempty"`
	Tool              string         `json:"tool,omitempty"`
	Scope             string         `json:"scope,omitempty"`
	Resource          string         `json:"resource,omitempty"`
	RequestHash       string         `json:"requestHash,omitempty"`
	CallerSubjectHash string         `json:"callerSubjectHash,omitempty"`
	Payment           paymentBinding `json:"payment,omitempty"`
	PolicyVersion     string         `json:"policyVersion,omitempty"`
	Authority         string         `json:"authority,omitempty"`
	Status            string         `json:"status,omitempty"`
	MaxUsage          int            `json:"maxUsage,omitempty"`
	UsedCount         int            `json:"usedCount,omitempty"`
	ExpiresAt         string         `json:"expiresAt,omitempty"`
}

type paymentBinding struct {
	EvidenceHash string `json:"evidenceHash,omitempty"`
}

type grantUsage struct {
	UsedCount int `json:"usedCount,omitempty"`
	MaxUsage  int `json:"maxUsage,omitempty"`
}

type failure struct {
	Reason      string
	Status      int
	Tool        string
	Capability  string
	Resource    string
	GrantIDHash string
}

type consumerFunc func(context.Context, consumeRequest) (consumeResponse, error)
type agentIDResolverFunc func(context.Context, string) (string, error)

var (
	consumerMu       sync.RWMutex
	consumerOverride consumerFunc

	resolverMu       sync.RWMutex
	resolverOverride agentIDResolverFunc
)

// RequireGrant returns nil when the caller is exempt or a matching Host
// instance capability grant was consumed successfully. Otherwise it returns a
// structured MCP tool error result before the caller's minting side effect runs.
func RequireGrant(ctx context.Context, req Requirement) (*mcpruntime.ToolResult, error) {
	req = normalizeRequirement(req)
	if req.Tool == "" || req.Capability == "" || req.Resource == "" {
		return toolFailure(&failure{Reason: "x402_instance_gate_misconfigured", Status: http.StatusInternalServerError, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource})
	}

	principal := auth.PrincipalFromToolContext(ctx)
	if instanceOperatorExempt(principal) {
		return nil, nil
	}

	snapshot := auth.RequestSnapshotFromToolContext(ctx)
	headers := snapshot.Headers
	grantToken := grantTokenFromHeaders(headers)
	if grantToken == "" {
		return toolFailure(&failure{Reason: "x402_grant_required", Status: http.StatusPaymentRequired, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource})
	}

	grantID := grantIDFromHeaders(headers)
	if grantID == "" {
		return toolFailure(&failure{Reason: "x402_grant_id_required", Status: http.StatusForbidden, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource})
	}
	if len(grantID) > 128 {
		return toolFailure(&failure{Reason: "x402_grant_id_invalid", Status: http.StatusForbidden, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource})
	}

	headerCapability := capabilityFromHeaders(headers)
	if headerCapability == "" {
		return toolFailure(&failure{Reason: "x402_capability_required", Status: http.StatusForbidden, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource, GrantIDHash: hashString("sha256", grantID)})
	}
	if !strings.EqualFold(headerCapability, req.Capability) {
		return toolFailure(&failure{Reason: "x402_grant_capability_mismatch", Status: http.StatusForbidden, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource, GrantIDHash: hashString("sha256", grantID)})
	}

	paymentEvidence := paymentEvidenceFromHeaders(headers)
	if paymentEvidence == "" {
		return toolFailure(&failure{Reason: "missing_payment_evidence", Status: http.StatusPaymentRequired, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource, GrantIDHash: hashString("sha256", grantID)})
	}
	if len(paymentEvidence) > x402MaxEvidenceHeaderLength {
		return toolFailure(&failure{Reason: "payment_evidence_too_large", Status: http.StatusForbidden, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource, GrantIDHash: hashString("sha256", grantID)})
	}

	agentID, err := resolveAgentID(ctx, req.Account)
	if err != nil {
		return toolFailure(&failure{Reason: "x402_agent_unresolved", Status: http.StatusForbidden, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource, GrantIDHash: hashString("sha256", grantID)})
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if !soulAgentIDPattern.MatchString(agentID) {
		return toolFailure(&failure{Reason: "x402_agent_unresolved", Status: http.StatusForbidden, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource, GrantIDHash: hashString("sha256", grantID)})
	}

	requestHash := hashString("sha256", strings.TrimSpace(string(snapshot.Body)))
	consumeReq := consumeRequest{
		GrantID:             grantID,
		GrantToken:          grantToken,
		AgentID:             agentID,
		CapabilityVersion:   CapabilityVersionInstanceV1,
		Capability:          req.Capability,
		Tool:                req.Tool,
		Resource:            req.Resource,
		RequestHash:         requestHash,
		IdempotencyKey:      consumeIdempotencyKey(grantID, requestHash, req.Tool, req.Capability),
		PaymentEvidenceHash: hashString("sha256", paymentEvidence),
	}

	consumeResp, err := consumeWithHost(ctx, consumeReq)
	if err != nil {
		return toolFailure(&failure{Reason: "x402_grant_consume_unavailable", Status: http.StatusBadGateway, Tool: req.Tool, Capability: req.Capability, Resource: req.Resource, GrantIDHash: hashString("sha256", grantID)})
	}
	if fail := validateConsumeResponse(consumeReq, consumeResp); fail != nil {
		fail.Tool = req.Tool
		fail.Capability = req.Capability
		fail.Resource = req.Resource
		return toolFailure(fail)
	}
	return nil, nil
}

func normalizeRequirement(req Requirement) Requirement {
	req.Tool = strings.ToLower(strings.TrimSpace(req.Tool))
	req.Capability = strings.ToLower(strings.TrimSpace(req.Capability))
	req.Resource = strings.TrimSpace(req.Resource)
	req.Account = strings.ToLower(strings.TrimSpace(req.Account))
	if req.Resource == "" {
		switch req.Tool {
		case ToolAgentLocalInstallPlan:
			req.Resource = ResourceInstallPlan
		}
	}
	return req
}

func instanceOperatorExempt(principal *auth.Principal) bool {
	return IsInstanceOperator(principal)
}

// IsInstanceOperator reports explicit owner/operator authority for instance
// tools. A write scope, a delegated_by marker, or an agent token is not an
// operator claim. This predicate is shared by the x402 exemption and Ptah's
// Host-backed genesis flow so paid/public OAuth sessions cannot be upgraded
// into instance-owner authority by inference.
func IsInstanceOperator(principal *auth.Principal) bool {
	if principal == nil || principal.Type != auth.PrincipalTypeOAuthToken || principal.Claims == nil {
		return false
	}
	claims := principal.Claims
	if claims.IsAgent {
		return false
	}
	for _, scope := range claims.Scopes {
		if strings.EqualFold(strings.TrimSpace(scope), "admin") {
			return true
		}
	}
	if normalizeOperatorClass(claims.ClientClass) == "operator" {
		return true
	}
	return normalizeOperatorClass(claims.AgentType) == "operator"
}

func normalizeOperatorClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", " ", "_", ".", "_", ":", "_").Replace(value)
	switch value {
	case "operator", "principal", "principal_operator", "owner", "account_operator":
		return "operator"
	default:
		return value
	}
}

func grantTokenFromHeaders(headers map[string][]string) string {
	if value := firstHeaderValue(headers, x402GrantHeader); value != "" {
		return value
	}
	return firstHeaderValue(headers, x402GrantLegacyHeader)
}

func grantIDFromHeaders(headers map[string][]string) string {
	if value := firstHeaderValue(headers, x402GrantIDHeader); value != "" {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(firstHeaderValue(headers, x402GrantIDLegacyHeader))
}

func capabilityFromHeaders(headers map[string][]string) string {
	if value := firstHeaderValue(headers, x402GrantCapabilityHeader); value != "" {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return strings.ToLower(strings.TrimSpace(firstHeaderValue(headers, x402GrantCapabilityLegacy)))
}

func paymentEvidenceFromHeaders(headers map[string][]string) string {
	if value := firstHeaderValue(headers, x402PaymentSignatureHeader); value != "" {
		return value
	}
	return firstHeaderValue(headers, x402LegacyPaymentHeader)
}

func firstHeaderValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func resolveAgentID(ctx context.Context, account string) (string, error) {
	resolverMu.RLock()
	resolver := resolverOverride
	resolverMu.RUnlock()
	if resolver != nil {
		return resolver(ctx, account)
	}
	return soulbinding.ResolveAgentID(ctx, account)
}

func consumeWithHost(ctx context.Context, req consumeRequest) (consumeResponse, error) {
	consumerMu.RLock()
	consumer := consumerOverride
	consumerMu.RUnlock()
	if consumer != nil {
		return consumer(ctx, req)
	}
	return defaultConsumeWithHost(ctx, req)
}

func defaultConsumeWithHost(ctx context.Context, req consumeRequest) (consumeResponse, error) {
	commBearer, err := auth.LesserHostInstanceKey(ctx)
	if err != nil {
		return consumeResponse{}, fmt.Errorf("resolve lesser-host instance key for instance x402 grant consume: %w", err)
	}
	commBearer = strings.TrimSpace(commBearer)
	if commBearer == "" {
		return consumeResponse{}, errors.New("LESSER_HOST_INSTANCE_KEY is required for instance x402 grant consume")
	}
	client, err := soulapi.Default()
	if err != nil {
		return consumeResponse{}, err
	}
	grantID := strings.TrimSpace(req.GrantID)
	if grantID == "" {
		return consumeResponse{}, errors.New("grantId is required for instance x402 grant consume")
	}
	path := x402GrantConsumePathPrefix + url.PathEscape(grantID) + x402GrantConsumePathSuffix
	raw, err := client.DoJSON(ctx, "POST", path, nil, commBearer, req)
	if err != nil {
		return consumeResponse{}, err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return consumeResponse{}, fmt.Errorf("marshal instance x402 consume response: %w", err)
	}
	var out consumeResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return consumeResponse{}, fmt.Errorf("unmarshal instance x402 consume response: %w", err)
	}
	return out, nil
}

func validateConsumeResponse(req consumeRequest, resp consumeResponse) *failure {
	fail := func(reason string) *failure {
		return &failure{
			Reason:      reason,
			Status:      http.StatusForbidden,
			GrantIDHash: normalizedGrantIDHash(req, resp),
		}
	}
	if !resp.Accepted {
		reason := safeHostDenialReason(resp.Reason)
		if reason == "" {
			reason = "x402_grant_denied"
		}
		return fail(reason)
	}
	if resp.Replayed {
		return fail("x402_grant_replay")
	}
	grant := resp.Grant
	if strings.TrimSpace(grant.AgentID) == "" || !strings.EqualFold(strings.TrimSpace(grant.AgentID), strings.TrimSpace(req.AgentID)) {
		return fail("x402_grant_agent_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.CapabilityVersion), CapabilityVersionInstanceV1) {
		return fail("x402_grant_version_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.Capability), strings.TrimSpace(req.Capability)) {
		return fail("x402_grant_capability_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.Tool), strings.TrimSpace(req.Tool)) {
		return fail("x402_grant_tool_mismatch")
	}
	if !grantScopeAuthorizesWrite(grant.Scope) {
		return fail("x402_grant_scope_mismatch")
	}
	if strings.TrimSpace(grant.Resource) != strings.TrimSpace(req.Resource) {
		return fail("x402_resource_mismatch")
	}
	if !hashEqual(grant.RequestHash, req.RequestHash) {
		return fail("x402_request_hash_mismatch")
	}
	if !hashEqual(grant.Payment.EvidenceHash, req.PaymentEvidenceHash) {
		return fail("x402_payment_evidence_mismatch")
	}
	if authority := strings.TrimSpace(grant.Authority); authority != "" && !strings.EqualFold(authority, "scoped_invocation") {
		return fail("x402_grant_authority_mismatch")
	}
	if status := strings.TrimSpace(grant.Status); status != "" && !strings.EqualFold(status, "issued") {
		return fail("x402_grant_status_mismatch")
	}
	maxUses := grant.MaxUsage
	if resp.Usage.MaxUsage > maxUses {
		maxUses = resp.Usage.MaxUsage
	}
	if maxUses <= 0 {
		return fail("x402_usage_limit_missing")
	}
	if expiresAt := strings.TrimSpace(grant.ExpiresAt); expiresAt != "" {
		t, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return fail("x402_grant_expiry_invalid")
		}
		if !time.Now().UTC().Before(t.UTC()) {
			return fail("x402_grant_expired")
		}
	}
	return nil
}

func grantScopeAuthorizesWrite(scope string) bool {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "write", "admin":
		return true
	default:
		return false
	}
}

func safeHostDenialReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x402_grant_denied", "x402_grant_expired", "x402_grant_replay", "x402_usage_not_accepted",
		"x402_payment_evidence_mismatch", "x402_caller_evidence_missing", "x402_policy_version_mismatch",
		"x402_resource_mismatch", "x402_request_hash_mismatch", "x402_grant_agent_mismatch",
		"x402_grant_tool_mismatch", "x402_grant_capability_mismatch", "x402_grant_scope_mismatch",
		"x402_grant_version_mismatch", "x402_grant_authority_mismatch", "x402_grant_status_mismatch":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func toolFailure(f *failure) (*mcpruntime.ToolResult, error) {
	if f == nil {
		f = &failure{Reason: "x402_grant_invalid", Status: http.StatusForbidden}
	}
	status := f.Status
	if status == 0 {
		status = http.StatusForbidden
	}
	reason := strings.TrimSpace(f.Reason)
	if reason == "" {
		reason = "x402_grant_invalid"
	}
	details := map[string]any{
		"source":            "lesser_body_x402",
		"reauthorize":       false,
		"authAction":        "x402_grant",
		"refreshRequired":   false,
		"reason":            reason,
		"capabilityVersion": CapabilityVersionInstanceV1,
	}
	if tool := strings.TrimSpace(f.Tool); tool != "" {
		details["tool"] = tool
	}
	if capability := strings.TrimSpace(f.Capability); capability != "" {
		details["capability"] = capability
	}
	if resource := strings.TrimSpace(f.Resource); resource != "" {
		details["resource"] = resource
	}
	if grantIDHash := strings.TrimSpace(f.GrantIDHash); grantIDHash != "" {
		details["grantIdHash"] = grantIDHash
	}
	payload := map[string]any{
		"code":    errorCodeForStatus(status),
		"message": errorMessageForStatus(status),
		"status":  status,
		"details": details,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal instance x402 tool error: %w", err)
	}
	return &mcpruntime.ToolResult{
		Content: []mcpruntime.ContentBlock{{Type: "text", Text: string(b)}},
		IsError: true,
		StructuredContent: map[string]any{
			"error": payload,
		},
	}, nil
}

func errorCodeForStatus(status int) string {
	if status == http.StatusPaymentRequired {
		return "payment_required"
	}
	if status >= 500 {
		return "app.unavailable"
	}
	return "app.forbidden"
}

func errorMessageForStatus(status int) string {
	if status == http.StatusPaymentRequired {
		return "payment required"
	}
	if status >= 500 {
		return "x402 grant consume unavailable"
	}
	return "forbidden"
}

func normalizedGrantIDHash(req consumeRequest, resp consumeResponse) string {
	if value := strings.TrimSpace(resp.Grant.GrantID); value != "" {
		return hashString("sha256", value)
	}
	if value := strings.TrimSpace(req.GrantID); value != "" {
		return hashString("sha256", value)
	}
	return ""
}

func hashEqual(left string, right string) bool {
	l := normalizeHashForCompare(left)
	r := normalizeHashForCompare(right)
	return l != "" && r != "" && strings.EqualFold(l, r)
}

func normalizeHashForCompare(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.TrimPrefix(value, "sha256:")
}

func consumeIdempotencyKey(grantID string, requestHash string, tool string, capability string) string {
	return hashString("sha256", strings.TrimSpace(grantID)+"\n"+strings.TrimSpace(requestHash)+"\n"+strings.TrimSpace(tool)+"\n"+strings.TrimSpace(capability)+"\nlesser-body-instance-x402-consume/v1")
}

func hashString(algorithm string, value string) string {
	sum := sha256.Sum256([]byte(value))
	if strings.TrimSpace(algorithm) == "" {
		return hex.EncodeToString(sum[:])
	}
	return strings.ToLower(strings.TrimSpace(algorithm)) + ":" + hex.EncodeToString(sum[:])
}
