package mcpapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	x402PaymentSignatureHeader  = "payment-signature"
	x402LegacyPaymentHeader     = "x-payment"
	x402GrantValidationPath     = "/api/v1/soul/x402/grants/validate"
	x402InvocationGrantVersion  = "scoped-x402-invocation-grant/v1"
	x402InvocationGrantScope    = "tools/call"
	x402MaxEvidenceHeaderLength = 64 << 10
)

type x402GrantValidationRequest struct {
	Version             string `json:"version"`
	Grant               string `json:"grant"`
	Actor               string `json:"actor"`
	AgentID             string `json:"agentId"`
	Resource            string `json:"resource"`
	Method              string `json:"method"`
	Tool                string `json:"tool"`
	RequestHash         string `json:"requestHash"`
	PaymentEvidenceHash string `json:"paymentEvidenceHash"`
	PaymentHeader       string `json:"paymentHeader"`
	Consume             bool   `json:"consume"`
}

type x402GrantValidationResponse struct {
	Valid               bool   `json:"valid"`
	Reason              string `json:"reason,omitempty"`
	GrantID             string `json:"grantId,omitempty"`
	GrantIDHash         string `json:"grantIdHash,omitempty"`
	AgentID             string `json:"agentId,omitempty"`
	Actor               string `json:"actor,omitempty"`
	Tool                string `json:"tool,omitempty"`
	Capability          string `json:"capability,omitempty"`
	Scope               string `json:"scope,omitempty"`
	GrantVersion        string `json:"grantVersion,omitempty"`
	Version             string `json:"version,omitempty"`
	PolicyVersion       string `json:"policyVersion,omitempty"`
	Resource            string `json:"resource,omitempty"`
	RequestHash         string `json:"requestHash,omitempty"`
	PaymentEvidenceHash string `json:"paymentEvidenceHash,omitempty"`
	CallerEvidenceHash  string `json:"callerEvidenceHash,omitempty"`
	ExpiresAt           string `json:"expiresAt,omitempty"`
	MaxUses             int    `json:"maxUses,omitempty"`
	RemainingUses       int    `json:"remainingUses,omitempty"`
	UsageAccepted       bool   `json:"usageAccepted,omitempty"`
	Replay              bool   `json:"replay,omitempty"`
}

type x402GrantFailure struct {
	Reason        string
	Status        int
	Tool          string
	GrantIDHash   string
	PolicyVersion string
}

var validateX402GrantWithHost = defaultValidateX402GrantWithHost

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

	paymentEvidence, paymentHeader := x402PaymentEvidence(headersForContext(ctx))
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

	validationReq := x402GrantValidationRequest{
		Version:             x402InvocationGrantVersion,
		Grant:               rawGrant,
		Actor:               actor,
		AgentID:             expectedAgentID,
		Resource:            resource,
		Method:              "tools/call",
		Tool:                toolName,
		RequestHash:         hashString("sha256", strings.TrimSpace(string(ctx.Request.Body))),
		PaymentEvidenceHash: hashString("sha256", paymentEvidence),
		PaymentHeader:       paymentHeader,
		Consume:             true,
	}

	validationResp, err := validateX402GrantWithHost(contextForRuntimePolicy(ctx), validationReq)
	if err != nil {
		return nil, &x402GrantFailure{Reason: "x402_grant_validation_unavailable", Status: 502, Tool: toolName}, nil
	}

	return normalizeX402GrantValidationResponse(validationReq, validationResp)
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

func defaultValidateX402GrantWithHost(ctx context.Context, req x402GrantValidationRequest) (x402GrantValidationResponse, error) {
	commBearer, err := auth.LesserHostInstanceKey(ctx)
	if err != nil {
		return x402GrantValidationResponse{}, fmt.Errorf("resolve lesser-host instance key for x402 grant validation: %w", err)
	}
	commBearer = strings.TrimSpace(commBearer)
	if commBearer == "" {
		return x402GrantValidationResponse{}, errors.New("LESSER_HOST_INSTANCE_KEY is required for x402 grant validation")
	}

	client, err := soulapi.Default()
	if err != nil {
		return x402GrantValidationResponse{}, err
	}

	raw, err := client.DoJSON(ctx, "POST", x402GrantValidationPath, nil, commBearer, req)
	if err != nil {
		return x402GrantValidationResponse{}, err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return x402GrantValidationResponse{}, fmt.Errorf("marshal x402 validation response: %w", err)
	}
	var out x402GrantValidationResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return x402GrantValidationResponse{}, fmt.Errorf("unmarshal x402 validation response: %w", err)
	}
	return out, nil
}

func normalizeX402GrantValidationResponse(req x402GrantValidationRequest, resp x402GrantValidationResponse) (*auth.X402InvocationGrant, *x402GrantFailure, error) {
	failure := func(reason string) (*auth.X402InvocationGrant, *x402GrantFailure, error) {
		return nil, &x402GrantFailure{
			Reason:        reason,
			Status:        403,
			Tool:          req.Tool,
			GrantIDHash:   normalizedGrantIDHash(resp),
			PolicyVersion: strings.TrimSpace(resp.PolicyVersion),
		}, nil
	}

	if !resp.Valid {
		reason := safeX402HostDenialReason(resp.Reason)
		if reason == "" {
			reason = "x402_grant_denied"
		}
		return failure(reason)
	}
	if strings.TrimSpace(resp.AgentID) == "" || !strings.EqualFold(strings.TrimSpace(resp.AgentID), strings.TrimSpace(req.AgentID)) {
		return failure("x402_grant_agent_mismatch")
	}
	if actor := strings.TrimSpace(resp.Actor); actor != "" && !strings.EqualFold(actor, strings.TrimSpace(req.Actor)) {
		return failure("x402_grant_actor_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Tool), strings.TrimSpace(req.Tool)) {
		return failure("x402_grant_tool_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Scope), x402InvocationGrantScope) {
		return failure("x402_grant_scope_mismatch")
	}
	grantVersion := strings.TrimSpace(resp.GrantVersion)
	if grantVersion == "" {
		grantVersion = strings.TrimSpace(resp.Version)
	}
	if !strings.EqualFold(grantVersion, x402InvocationGrantVersion) {
		return failure("x402_grant_version_mismatch")
	}
	if !x402PolicyVersionAllowed(resp.PolicyVersion) {
		return failure("x402_policy_version_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Resource), strings.TrimSpace(req.Resource)) {
		return failure("x402_resource_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(resp.RequestHash), strings.TrimSpace(req.RequestHash)) {
		return failure("x402_request_hash_mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(resp.PaymentEvidenceHash), strings.TrimSpace(req.PaymentEvidenceHash)) {
		return failure("x402_payment_evidence_mismatch")
	}
	if strings.TrimSpace(resp.CallerEvidenceHash) == "" {
		return failure("x402_caller_evidence_missing")
	}
	if !resp.UsageAccepted {
		if resp.Replay {
			return failure("x402_grant_replay")
		}
		return failure("x402_usage_not_accepted")
	}
	if resp.MaxUses <= 0 {
		return failure("x402_usage_limit_missing")
	}

	expiresAt, err := parseX402Expiry(resp.ExpiresAt)
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
		GrantIDHash:         normalizedGrantIDHash(resp),
		AgentID:             strings.TrimSpace(resp.AgentID),
		Actor:               strings.TrimSpace(resp.Actor),
		Tool:                strings.TrimSpace(resp.Tool),
		Capability:          strings.TrimSpace(resp.Capability),
		Scope:               strings.TrimSpace(resp.Scope),
		GrantVersion:        grantVersion,
		PolicyVersion:       strings.TrimSpace(resp.PolicyVersion),
		Resource:            strings.TrimSpace(resp.Resource),
		RequestHash:         strings.TrimSpace(resp.RequestHash),
		PaymentEvidenceHash: strings.TrimSpace(resp.PaymentEvidenceHash),
		CallerEvidenceHash:  strings.TrimSpace(resp.CallerEvidenceHash),
		ExpiresAt:           expiresAt,
		MaxUses:             resp.MaxUses,
		RemainingUses:       resp.RemainingUses,
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
		"x402_grant_tool_mismatch", "x402_grant_scope_mismatch", "x402_grant_version_mismatch":
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

func normalizedGrantIDHash(resp x402GrantValidationResponse) string {
	if value := strings.TrimSpace(resp.GrantIDHash); value != "" {
		if strings.HasPrefix(strings.ToLower(value), "sha256:") {
			return value
		}
		return hashString("sha256", value)
	}
	if value := strings.TrimSpace(resp.GrantID); value != "" {
		return hashString("sha256", value)
	}
	return ""
}

func x402GrantAllowsMCPRequest(req *mcpruntime.Request, grant *auth.X402InvocationGrant) bool {
	if req == nil || grant == nil {
		return false
	}
	if req.Method != "tools/call" {
		return false
	}
	return strings.EqualFold(requestedToolName(req), strings.TrimSpace(grant.Tool))
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
		return "x402 grant validation unavailable"
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
