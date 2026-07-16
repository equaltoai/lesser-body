package mcpapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/runtimepolicy"
	"github.com/equaltoai/lesser-body/internal/soulapi"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

const (
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
	x402InvocationGrantVersion  = "scoped-x402-invocation-grant/v1"
	x402MaxEvidenceHeaderLength = 64 << 10
)

// x402GrantConsumeRequest is the request body dispatched to lesser-host's
// x402 grant consume endpoint. The PaymentEvidenceHash field is sent to host
// so host can verify the caller's payment evidence matches the grant's
// recorded evidence BEFORE consuming (decrementing) the grant's usage count.
// This prevents burning a grant use on a mismatched payment evidence hash.
// Depends on lesser-host#361 for verified-host-side pre-consume verification.
type x402GrantConsumeRequest struct {
	GrantID             string `json:"-"`
	GrantToken          string `json:"grantToken"`
	Actor               string `json:"-"`
	AgentID             string `json:"agentId"`
	Capability          string `json:"capability"`
	Tool                string `json:"tool"`
	Resource            string `json:"resource"`
	RequestHash         string `json:"requestHash"`
	IdempotencyKey      string `json:"idempotencyKey"`
	PaymentEvidenceHash string `json:"paymentEvidenceHash"`
}

type x402GrantConsumeResponse struct {
	Accepted bool                     `json:"accepted"`
	Replayed bool                     `json:"replayed"`
	Grant    x402InvocationGrantView  `json:"grant"`
	Usage    x402InvocationGrantUsage `json:"usage"`
	Reason   string                   `json:"reason,omitempty"`
}

type x402InvocationGrantView struct {
	GrantID           string                  `json:"grantId,omitempty"`
	GrantToken        string                  `json:"grantToken,omitempty"`
	AgentID           string                  `json:"agentId,omitempty"`
	Actor             string                  `json:"actor,omitempty"`
	Capability        string                  `json:"capability,omitempty"`
	Tool              string                  `json:"tool,omitempty"`
	Scope             string                  `json:"scope,omitempty"`
	Resource          string                  `json:"resource,omitempty"`
	RequestHash       string                  `json:"requestHash,omitempty"`
	CallerSubjectHash string                  `json:"callerSubjectHash,omitempty"`
	Payment           x402GrantPaymentBinding `json:"payment,omitempty"`
	PolicyVersion     string                  `json:"policyVersion,omitempty"`
	Authority         string                  `json:"authority,omitempty"`
	Status            string                  `json:"status,omitempty"`
	MaxUsage          int                     `json:"maxUsage,omitempty"`
	UsedCount         int                     `json:"usedCount,omitempty"`
	ExpiresAt         string                  `json:"expiresAt,omitempty"`
}

type x402GrantPaymentBinding struct {
	EvidenceHash string `json:"evidenceHash,omitempty"`
}

type x402InvocationGrantUsage struct {
	UsedCount int `json:"usedCount,omitempty"`
	MaxUsage  int `json:"maxUsage,omitempty"`
}

type x402GrantFailure struct {
	Reason        string
	Status        int
	Tool          string
	GrantIDHash   string
	PolicyVersion string
}

var consumeX402GrantWithHost = defaultConsumeX402GrantWithHost

func tryAuthorizeX402InvocationGrant(ctx *apptheory.Context) (string, bool, *apptheory.Response, error) {
	rawGrant := firstHeaderValue(headersForContext(ctx), x402GrantHeader)
	if rawGrant == "" {
		rawGrant = firstHeaderValue(headersForContext(ctx), x402GrantLegacyHeader)
	}
	rawGrant = strings.TrimSpace(rawGrant)
	if rawGrant == "" {
		return "", false, nil, nil
	}
	if bearerTokenFromHeaders(ctx.Request.Headers) != "" {
		return "", true, x402GrantFailureResponse(ctx, &x402GrantFailure{Reason: "x402_mixed_auth_not_allowed", Status: 403}), nil
	}

	if x402InitializeRequest(ctx) {
		identity := "public_x402:pending"
		if rawGrant != "" {
			identity += ":" + hashString("sha256", rawGrant)
		}
		actor := actorFromRequestContext(ctx)
		auth.WithPrincipal(ctx, &auth.Principal{
			Type:     auth.PrincipalTypeX402Grant,
			Identity: identity,
			X402Grant: &auth.X402InvocationGrant{
				GrantIDHash: hashString("sha256", rawGrant),
				Actor:       actor,
				AgentID:     expectedAgentIDForX402Request(ctx, actor),
			},
		})
		ctx.AuthIdentity = identity
		return identity, true, nil, nil
	}

	grant, failure, err := validateX402InvocationGrant(ctx, rawGrant)
	if err != nil {
		return "", true, nil, err
	}
	if failure != nil {
		return "", true, x402GrantFailureResponse(ctx, failure), nil
	}
	if grant == nil {
		return "", true, x402GrantFailureResponse(ctx, &x402GrantFailure{Reason: "x402_grant_invalid", Status: 403}), nil
	}

	identity := "public_x402"
	if grant.GrantIDHash != "" {
		identity += ":" + grant.GrantIDHash
	}
	auth.WithPrincipal(ctx, &auth.Principal{
		Type:      auth.PrincipalTypeX402Grant,
		Identity:  identity,
		X402Grant: grant,
	})
	ctx.AuthIdentity = identity
	return identity, true, nil, nil
}

func x402InitializeRequest(ctx *apptheory.Context) bool {
	if ctx == nil {
		return false
	}
	body := strings.TrimSpace(string(ctx.Request.Body))
	if body == "" || strings.HasPrefix(body, "[") {
		return false
	}
	req, err := mcpruntime.ParseRequest(ctx.Request.Body)
	return err == nil && req != nil && req.Method == "initialize"
}

func validateX402InvocationGrant(ctx *apptheory.Context, rawGrant string) (*auth.X402InvocationGrant, *x402GrantFailure, error) {
	req, failure := singleToolCallRequestForX402(ctx)
	if failure != nil {
		return nil, failure, nil
	}
	toolName := requestedToolName(req)
	if toolName == "" {
		return nil, &x402GrantFailure{Reason: "x402_grant_tool_required", Status: 403}, nil
	}

	headers := headersForContext(ctx)
	grantID := x402GrantID(headers)
	if grantID == "" {
		return nil, &x402GrantFailure{Reason: "x402_grant_id_required", Status: 403, Tool: toolName}, nil
	}
	if len(grantID) > 128 {
		return nil, &x402GrantFailure{Reason: "x402_grant_id_invalid", Status: 403, Tool: toolName}, nil
	}
	capability := x402GrantCapability(headers)
	if capability == "" {
		return nil, &x402GrantFailure{Reason: "x402_capability_required", Status: 403, Tool: toolName}, nil
	}
	if len(capability) > 128 {
		return nil, &x402GrantFailure{Reason: "x402_capability_invalid", Status: 403, Tool: toolName}, nil
	}

	paymentEvidence, _ := x402PaymentEvidence(headers)
	if paymentEvidence == "" {
		return nil, &x402GrantFailure{Reason: "missing_payment_evidence", Status: http.StatusPaymentRequired, Tool: toolName}, nil
	}
	if len(paymentEvidence) > x402MaxEvidenceHeaderLength {
		return nil, &x402GrantFailure{Reason: "payment_evidence_too_large", Status: 403, Tool: toolName}, nil
	}

	resource, err := validatedMcpEndpointForRequest(ctx)
	if err != nil {
		return nil, nil, err
	}
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return nil, &x402GrantFailure{Reason: "x402_resource_unavailable", Status: 403, Tool: toolName}, nil
	}

	actor := actorFromRequestContext(ctx)
	resolved := runtimepolicy.ResolveForActor(contextForRuntimePolicy(ctx), actor)
	if !runtimepolicy.ToolAllowed(resolved.Profile, toolName) {
		return nil, &x402GrantFailure{Reason: "runtime_boundary", Status: 403, Tool: toolName}, nil
	}
	expectedAgentID := expectedAgentIDFromResolvedX402Actor(actor, resolved)
	if expectedAgentID == "" {
		return nil, &x402GrantFailure{Reason: "x402_agent_unresolved", Status: 403, Tool: toolName}, nil
	}

	requestHash := hashString("", strings.TrimSpace(string(ctx.Request.Body)))
	consumeReq := x402GrantConsumeRequest{
		GrantID:             grantID,
		GrantToken:          rawGrant,
		Actor:               actor,
		AgentID:             expectedAgentID,
		Capability:          capability,
		Tool:                toolName,
		Resource:            resource,
		RequestHash:         requestHash,
		IdempotencyKey:      x402ConsumeIdempotencyKey(grantID, requestHash),
		PaymentEvidenceHash: hashString("", paymentEvidence),
	}

	consumeResp, err := consumeX402GrantWithHost(contextForRuntimePolicy(ctx), consumeReq)
	if err != nil {
		return nil, &x402GrantFailure{Reason: "x402_grant_consume_unavailable", Status: 502, Tool: toolName}, nil
	}

	return normalizeX402GrantConsumeResponse(consumeReq, consumeResp)
}

func singleToolCallRequestForX402(ctx *apptheory.Context) (*mcpruntime.Request, *x402GrantFailure) {
	if ctx == nil {
		return nil, &x402GrantFailure{Reason: "x402_request_missing", Status: 403}
	}
	body := strings.TrimSpace(string(ctx.Request.Body))
	if body == "" {
		return nil, &x402GrantFailure{Reason: "x402_request_missing", Status: 403}
	}
	if strings.HasPrefix(body, "[") {
		return nil, &x402GrantFailure{Reason: "x402_batch_not_supported", Status: 403}
	}
	req, err := mcpruntime.ParseRequest(ctx.Request.Body)
	if err != nil {
		return nil, &x402GrantFailure{Reason: "x402_request_invalid", Status: 403}
	}
	if req == nil || req.Method != "tools/call" {
		return nil, &x402GrantFailure{Reason: "x402_tools_call_required", Status: 403}
	}
	return req, nil
}

func x402PaymentEvidence(headers map[string][]string) (string, string) {
	if value := firstHeaderValue(headers, x402PaymentSignatureHeader); value != "" {
		return value, "PAYMENT-SIGNATURE"
	}
	if value := firstHeaderValue(headers, x402LegacyPaymentHeader); value != "" {
		return value, "X-PAYMENT"
	}
	return "", ""
}

func x402GrantID(headers map[string][]string) string {
	if value := firstHeaderValue(headers, x402GrantIDHeader); value != "" {
		return strings.TrimSpace(value)
	}
	if value := firstHeaderValue(headers, x402GrantIDLegacyHeader); value != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func x402GrantCapability(headers map[string][]string) string {
	if value := firstHeaderValue(headers, x402GrantCapabilityHeader); value != "" {
		return strings.TrimSpace(value)
	}
	if value := firstHeaderValue(headers, x402GrantCapabilityLegacy); value != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func x402ConsumeIdempotencyKey(grantID string, requestHash string) string {
	return hashString("sha256", strings.TrimSpace(grantID)+"\n"+strings.TrimSpace(requestHash)+"\nlesser-body-x402-consume/v1")
}

func expectedAgentIDForX402Request(ctx *apptheory.Context, actor string) string {
	actor = strings.TrimSpace(actor)
	resolved := runtimepolicy.ResolveForActor(contextForRuntimePolicy(ctx), actor)
	return expectedAgentIDFromResolvedX402Actor(actor, resolved)
}

func expectedAgentIDFromResolvedX402Actor(actor string, resolved runtimepolicy.Resolved) string {
	actor = strings.TrimSpace(actor)
	if soulAgentID := strings.TrimSpace(resolved.SoulAgentID); soulAgentID != "" {
		return soulAgentID
	}
	return actor
}

func defaultConsumeX402GrantWithHost(ctx context.Context, req x402GrantConsumeRequest) (x402GrantConsumeResponse, error) {
	commBearer, err := auth.LesserHostInstanceKey(ctx)
	if err != nil {
		return x402GrantConsumeResponse{}, fmt.Errorf("resolve lesser-host instance key for x402 grant consume: %w", err)
	}
	commBearer = strings.TrimSpace(commBearer)
	if commBearer == "" {
		return x402GrantConsumeResponse{}, errors.New("LESSER_HOST_INSTANCE_KEY is required for x402 grant consume")
	}

	client, err := soulapi.Default()
	if err != nil {
		return x402GrantConsumeResponse{}, err
	}

	grantID := strings.TrimSpace(req.GrantID)
	if grantID == "" {
		return x402GrantConsumeResponse{}, errors.New("grantId is required for x402 grant consume")
	}
	path := x402GrantConsumePathPrefix + url.PathEscape(grantID) + x402GrantConsumePathSuffix
	raw, err := client.DoJSON(ctx, "POST", path, nil, commBearer, req)
	if err != nil {
		return x402GrantConsumeResponse{}, err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return x402GrantConsumeResponse{}, fmt.Errorf("marshal x402 consume response: %w", err)
	}
	var out x402GrantConsumeResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return x402GrantConsumeResponse{}, fmt.Errorf("unmarshal x402 consume response: %w", err)
	}
	return out, nil
}

func normalizeX402GrantConsumeResponse(req x402GrantConsumeRequest, resp x402GrantConsumeResponse) (*auth.X402InvocationGrant, *x402GrantFailure, error) {
	failure := func(reason string) (*auth.X402InvocationGrant, *x402GrantFailure, error) {
		return nil, &x402GrantFailure{
			Reason:        reason,
			Status:        403,
			Tool:          req.Tool,
			GrantIDHash:   normalizedGrantIDHash(req, resp),
			PolicyVersion: strings.TrimSpace(resp.Grant.PolicyVersion),
		}, nil
	}

	if !resp.Accepted {
		reason := safeX402HostDenialReason(resp.Reason)
		if reason == "" {
			reason = "x402_grant_denied"
		}
		return failure(reason)
	}
	if resp.Replayed {
		return failure("x402_grant_replay")
	}
	grant := resp.Grant
	if strings.TrimSpace(grant.AgentID) == "" || !strings.EqualFold(strings.TrimSpace(grant.AgentID), strings.TrimSpace(req.AgentID)) {
		return failure("x402_grant_agent_mismatch")
	}
	if actor := strings.TrimSpace(grant.Actor); actor != "" && !strings.EqualFold(actor, strings.TrimSpace(req.Actor)) {
		return failure("x402_grant_actor_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.Tool), strings.TrimSpace(req.Tool)) {
		return failure("x402_grant_tool_mismatch")
	}
	grantScope := x402NormalizeAccessScope(grant.Scope)
	if !x402ScopeAuthorizesRequiredScopes(grantScope, requiredScopesForTool(req.Tool)) {
		return failure("x402_grant_scope_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.Capability), strings.TrimSpace(req.Capability)) {
		return failure("x402_grant_capability_mismatch")
	}
	if !x402PolicyVersionAllowed(grant.PolicyVersion) {
		return failure("x402_policy_version_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.Authority), "scoped_invocation") {
		return failure("x402_grant_authority_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.Status), "issued") {
		return failure("x402_grant_status_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(grant.Resource), strings.TrimSpace(req.Resource)) {
		return failure("x402_resource_mismatch")
	}
	if !x402HashEqual(grant.RequestHash, req.RequestHash) {
		return failure("x402_request_hash_mismatch")
	}
	if !x402HashEqual(grant.Payment.EvidenceHash, req.PaymentEvidenceHash) {
		return failure("x402_payment_evidence_mismatch")
	}
	if strings.TrimSpace(grant.CallerSubjectHash) == "" {
		return failure("x402_caller_evidence_missing")
	}
	maxUses := grant.MaxUsage
	if maxUses == 0 {
		maxUses = resp.Usage.MaxUsage
	}
	if maxUses <= 0 {
		return failure("x402_usage_limit_missing")
	}
	usedCount := grant.UsedCount
	if resp.Usage.UsedCount > usedCount {
		usedCount = resp.Usage.UsedCount
	}
	remainingUses := maxUses - usedCount
	if remainingUses < 0 {
		remainingUses = 0
	}

	expiresAt, err := parseX402Expiry(grant.ExpiresAt)
	if err != nil {
		return failure("x402_grant_expiry_invalid")
	}
	if expiresAt.IsZero() {
		return failure("x402_grant_expiry_missing")
	}
	if !time.Now().UTC().Before(expiresAt) {
		return failure("x402_grant_expired")
	}

	return &auth.X402InvocationGrant{
		GrantIDHash:         normalizedGrantIDHash(req, resp),
		AgentID:             strings.TrimSpace(grant.AgentID),
		Actor:               strings.TrimSpace(grant.Actor),
		Tool:                strings.TrimSpace(grant.Tool),
		Capability:          strings.TrimSpace(grant.Capability),
		Scope:               grantScope,
		GrantVersion:        x402InvocationGrantVersion,
		PolicyVersion:       strings.TrimSpace(grant.PolicyVersion),
		Resource:            strings.TrimSpace(grant.Resource),
		RequestHash:         normalizeX402HashForCompare(grant.RequestHash),
		PaymentEvidenceHash: normalizeX402HashForCompare(grant.Payment.EvidenceHash),
		CallerEvidenceHash:  strings.TrimSpace(grant.CallerSubjectHash),
		ExpiresAt:           expiresAt,
		MaxUses:             maxUses,
		RemainingUses:       remainingUses,
	}, nil, nil
}

func x402PolicyVersionAllowed(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "caller-access-payment/v1", "hosted-bound-soul/v1":
		return true
	default:
		return false
	}
}

func safeX402HostDenialReason(value string) string {
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

func parseX402Expiry(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse x402 grant expiry: %w", err)
	}
	return t.UTC(), nil
}

func normalizedGrantIDHash(req x402GrantConsumeRequest, resp x402GrantConsumeResponse) string {
	if value := strings.TrimSpace(resp.Grant.GrantID); value != "" {
		return hashString("sha256", value)
	}
	if value := strings.TrimSpace(req.GrantID); value != "" {
		return hashString("sha256", value)
	}
	return ""
}

func x402HashEqual(left string, right string) bool {
	l := normalizeX402HashForCompare(left)
	r := normalizeX402HashForCompare(right)
	return l != "" && r != "" && strings.EqualFold(l, r)
}

func normalizeX402HashForCompare(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.TrimPrefix(value, "sha256:")
}

func x402GrantAllowsMCPRequest(req *mcpruntime.Request, grant *auth.X402InvocationGrant) bool {
	if req == nil || grant == nil {
		return false
	}
	if req.Method != "tools/call" {
		return false
	}
	toolName := requestedToolName(req)
	if !strings.EqualFold(toolName, strings.TrimSpace(grant.Tool)) {
		return false
	}
	return x402ScopeAuthorizesRequiredScopes(grant.Scope, requiredScopesForTool(toolName))
}

func x402NormalizeAccessScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "read":
		return "read"
	case "write":
		return "write"
	case "admin":
		return "admin"
	default:
		return ""
	}
}

func x402ScopeAuthorizesRequiredScopes(grantScope string, requiredScopes []string) bool {
	grantRank := x402AccessScopeRank(grantScope)
	if grantRank == 0 || len(requiredScopes) == 0 {
		return false
	}
	for _, requiredScope := range requiredScopes {
		requiredRank := x402AccessScopeRank(requiredScope)
		if requiredRank > 0 && grantRank >= requiredRank {
			return true
		}
	}
	return false
}

func x402AccessScopeRank(scope string) int {
	switch x402NormalizeAccessScope(scope) {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	default:
		return 0
	}
}

func x402GrantFailureResponse(ctx *apptheory.Context, failure *x402GrantFailure) *apptheory.Response {
	if failure == nil {
		failure = &x402GrantFailure{Reason: "x402_grant_invalid", Status: 403}
	}
	status := failure.Status
	if status == 0 {
		status = 403
	}
	reason := strings.TrimSpace(failure.Reason)
	if reason == "" {
		reason = "x402_grant_invalid"
	}
	details := map[string]any{
		"source":          "lesser_body_x402",
		"reauthorize":     false,
		"authAction":      "x402_grant",
		"refreshRequired": false,
		"reason":          reason,
	}
	if tool := strings.TrimSpace(failure.Tool); tool != "" {
		details["tool"] = tool
	}
	if grantIDHash := strings.TrimSpace(failure.GrantIDHash); grantIDHash != "" {
		details["grantIdHash"] = grantIDHash
	}
	if policyVersion := strings.TrimSpace(failure.PolicyVersion); policyVersion != "" && x402PolicyVersionAllowed(policyVersion) {
		details["policyVersion"] = policyVersion
	}

	body := map[string]any{
		"error": map[string]any{
			"code":    x402ErrorCodeForStatus(status),
			"message": x402ErrorMessageForStatus(status),
			"details": details,
		},
	}
	if ctx != nil && strings.TrimSpace(ctx.RequestID) != "" {
		body["error"].(map[string]any)["request_id"] = ctx.RequestID
	}
	return apptheory.MustJSON(status, body)
}

func x402ErrorCodeForStatus(status int) string {
	if status == http.StatusPaymentRequired {
		return "payment_required"
	}
	if status == http.StatusUnauthorized {
		return "app.unauthorized"
	}
	if status >= 500 {
		return "app.unavailable"
	}
	return "app.forbidden"
}

func x402ErrorMessageForStatus(status int) string {
	if status == http.StatusPaymentRequired {
		return "payment required"
	}
	if status == http.StatusUnauthorized {
		return "unauthorized"
	}
	if status >= 500 {
		return "x402 grant consume unavailable"
	}
	return "forbidden"
}

func hashString(algorithm string, value string) string {
	sum := sha256.Sum256([]byte(value))
	if strings.TrimSpace(algorithm) == "" {
		return hex.EncodeToString(sum[:])
	}
	return strings.ToLower(strings.TrimSpace(algorithm)) + ":" + hex.EncodeToString(sum[:])
}
