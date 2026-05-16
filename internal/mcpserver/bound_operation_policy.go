package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/soulapi"
)

type boundOperation string

const (
	boundOperationEmailSend        boundOperation = "communication.email.send"
	boundOperationEmailRead        boundOperation = "communication.email.read"
	boundOperationEmailManage      boundOperation = "communication.email.manage"
	boundOperationSMSSend          boundOperation = "communication.sms.send"
	boundOperationSMSRead          boundOperation = "communication.sms.read"
	boundOperationVoiceRead        boundOperation = "communication.voice.read"
	boundOperationChannelsRead     boundOperation = "communication.channels.read"
	boundOperationIdentitySelfRead boundOperation = "identity.self.read"
)

type boundCallerClass string

const (
	boundCallerClassPrincipalOperator boundCallerClass = "principal_operator"
	boundCallerClassBoundBody         boundCallerClass = "bound_body"
	boundCallerClassInstanceKey       boundCallerClass = "instance_key"
	boundCallerClassAllowlistedPeer   boundCallerClass = "allowlisted_peer"
	boundCallerClassPublicPaid        boundCallerClass = "public_paid"
	boundCallerClassUnknown           boundCallerClass = "unknown"
)

type boundPolicyDecision struct {
	Operation     boundOperation
	CallerClass   boundCallerClass
	Allowed       bool
	Reason        string
	PolicyVersion string
}

func authorizedAgentChannelsPayload(ctx context.Context, operation boundOperation) (map[string]any, error) {
	client, err := soulapi.Default()
	if err != nil {
		return nil, &toolUserError{Code: "not_configured", Message: err.Error(), Status: 500}
	}

	agentID, err := authenticatedAgentID(ctx)
	if err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, &toolUserError{Code: "not_found", Message: "no bound soul found for this agent", Status: 404}
	}

	payload, registration, err := agentChannelsPayloadWithRegistration(ctx, client, agentID)
	if err != nil {
		return nil, err
	}
	registration = withAgentContactabilityPolicy(ctx, client, agentID, registration)
	if decision := decideBoundOperation(ctx, registration, operation); !decision.Allowed {
		return nil, boundOperationDeniedError(decision)
	}
	return payload, nil
}

func withAgentContactabilityPolicy(ctx context.Context, client *soulapi.Client, agentID string, registration map[string]any) map[string]any {
	registration = mapFromAny(registration)
	if client == nil || normalizeSoulAgentID(agentID) == "" || len(hostedBoundSoulPolicy(registration)) > 0 {
		return registration
	}
	contactabilityAny, err := client.DoJSON(ctx, "GET", "/api/v1/soul/comm/contactability/"+url.PathEscape(normalizeSoulAgentID(agentID)), nil, "", nil)
	if err != nil {
		return registration
	}
	contactability := mapFromAny(contactabilityAny)
	if len(contactability) == 0 {
		return registration
	}
	if policy := firstPolicyMap(contactability, "policy", "effectivePolicy", "effective_policy"); len(policy) > 0 {
		registration["policy"] = policy
	}
	if channels := firstArrayFromAny(contactability, "channels"); len(channels) > 0 {
		registration["contactabilityChannels"] = channels
	}
	return registration
}

func decideBoundOperation(ctx context.Context, registration map[string]any, operation boundOperation) boundPolicyDecision {
	operation = normalizeBoundOperation(operation)
	callerClass := classifyBoundOperationCaller(ctx)
	decision := boundPolicyDecision{
		Operation:     operation,
		CallerClass:   callerClass,
		Allowed:       false,
		Reason:        "bound_body_policy_denied",
		PolicyVersion: boundPolicyVersion(registration),
	}

	if operation == "" {
		decision.Reason = "operation_unset"
		return decision
	}
	if callerClass == boundCallerClassUnknown {
		decision.Reason = "caller_class_unknown"
		return decision
	}
	if callerClass == boundCallerClassPublicPaid {
		decision.Reason = "public_paid_callers_denied_in_m1"
		return decision
	}

	capabilityAllowed := boundCapabilityPolicyAllows(registration, operation)
	callerAllowed := boundCallerAccessPolicyAllows(registration, operation, callerClass)
	if !capabilityAllowed {
		decision.Reason = "capability_policy_denied"
		return decision
	}
	if !callerAllowed {
		decision.Reason = "caller_access_policy_denied"
		return decision
	}
	if boundOperationRequiresEntitlement(operation) && !boundPaymentPolicyAllows(registration, operation) {
		decision.Reason = "paid_or_provisioned_entitlement_required"
		return decision
	}

	decision.Allowed = true
	decision.Reason = "allowed"
	return decision
}

func classifyBoundOperationCaller(ctx context.Context) boundCallerClass {
	principal := auth.PrincipalFromToolContext(ctx)
	if principal == nil {
		return boundCallerClassUnknown
	}
	if principal.Type == auth.PrincipalTypeInstanceKey {
		return boundCallerClassInstanceKey
	}
	if principal.Type != auth.PrincipalTypeOAuthToken {
		return boundCallerClassUnknown
	}

	claims := principal.Claims
	if claims == nil {
		return boundCallerClassUnknown
	}

	switch normalizeCallerClass(claims.ClientClass) {
	case boundCallerClassPublicPaid:
		return boundCallerClassPublicPaid
	case boundCallerClassAllowlistedPeer:
		return boundCallerClassAllowlistedPeer
	case boundCallerClassPrincipalOperator:
		return boundCallerClassPrincipalOperator
	case boundCallerClassBoundBody:
		return boundCallerClassBoundBody
	}

	if strings.TrimSpace(claims.DelegatedBy) != "" {
		return boundCallerClassPrincipalOperator
	}
	switch strings.ToLower(strings.TrimSpace(claims.AgentType)) {
	case "operator", "principal", "principal_operator":
		return boundCallerClassPrincipalOperator
	}
	return boundCallerClassBoundBody
}

func normalizeCallerClass(value string) boundCallerClass {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", " ", "_", ".", "_", ":", "_").Replace(value)
	switch value {
	case "principal_operator", "principal", "operator", "owner", "account_operator":
		return boundCallerClassPrincipalOperator
	case "bound_body", "body", "agent", "self", "actor":
		return boundCallerClassBoundBody
	case "instance_key", "managed_instance_key", "host_instance_key":
		return boundCallerClassInstanceKey
	case "allowlisted_peer", "allowlisted", "trusted_peer", "peer":
		return boundCallerClassAllowlistedPeer
	case "public_paid", "paid_public", "x402", "x402_public", "public_x402":
		return boundCallerClassPublicPaid
	default:
		return ""
	}
}

func boundOperationDeniedError(decision boundPolicyDecision) *toolUserError {
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = "bound_body_policy_denied"
	}
	details := map[string]any{
		"source":      "bound_body_operation_policy",
		"reason":      reason,
		"operation":   string(decision.Operation),
		"callerClass": string(decision.CallerClass),
	}
	if decision.PolicyVersion != "" {
		details["policyVersion"] = decision.PolicyVersion
	}
	return &toolUserError{
		Code:    "operation_not_allowed",
		Message: "operation is not allowed by current bound-body policy",
		Status:  403,
		Details: details,
	}
}

func boundCapabilityPolicyAllows(registration map[string]any, operation boundOperation) bool {
	if registration == nil {
		return false
	}
	for _, entry := range boundOperationPolicyEntries(registration, operation) {
		if boundPolicyEntryEnabled(entry) {
			return true
		}
	}
	return false
}

func boundCallerAccessPolicyAllows(registration map[string]any, operation boundOperation, callerClass boundCallerClass) bool {
	if registration == nil || callerClass == "" {
		return false
	}
	for _, entry := range boundOperationPolicyEntries(registration, operation) {
		if !boundPolicyEntryEnabled(entry) {
			continue
		}
		if boundPolicyEntryAllowsCaller(entry, callerClass) {
			return true
		}
	}
	for _, root := range boundPolicyRoots(registration, "callerAccessPolicy", "caller_access_policy", "accessPolicy", "access_policy", "callerPolicy", "caller_policy") {
		if boundPolicyEntryAllowsCaller(root, callerClass) {
			return true
		}
		if classes := firstPolicyMap(root, "classes", "callerClasses", "caller_classes", "callers"); len(classes) > 0 {
			for key, value := range classes {
				if normalizeCallerClass(key) != callerClass {
					continue
				}
				if boundPolicyValueEnabled(value) {
					return true
				}
			}
		}
	}
	return false
}

func boundPaymentPolicyAllows(registration map[string]any, operation boundOperation) bool {
	if registration == nil {
		return false
	}
	for _, entry := range boundOperationPolicyEntries(registration, operation) {
		if !boundPolicyEntryEnabled(entry) {
			continue
		}
		if boundEntitlementPresent(entry) {
			return true
		}
	}
	for _, root := range boundPolicyRoots(registration, "paymentPolicy", "payment_policy", "entitlementPolicy", "entitlement_policy", "callerAccessPolicy", "caller_access_policy") {
		if boundEntitlementPresent(root) {
			return true
		}
	}
	channel := boundOperationChannel(operation)
	if channel != "" {
		channels := firstPolicyMap(registration, "channels")
		if channelMap := firstPolicyMap(channels, channel); boundEntitlementPresent(channelMap) {
			return true
		}
		if channel == "sms" || channel == "voice" {
			if phoneMap := firstPolicyMap(channels, "phone"); boundEntitlementPresent(phoneMap) {
				return true
			}
		}
	}
	return false
}

func boundOperationPolicyEntries(registration map[string]any, operation boundOperation) []map[string]any {
	operation = normalizeBoundOperation(operation)
	if registration == nil || operation == "" {
		return nil
	}
	aliases := boundOperationAliases(operation)
	entries := []map[string]any{}
	if entry := hostedBoundSoulPolicyEntry(registration, operation); len(entry) > 0 {
		entries = append(entries, entry)
	}
	for _, root := range boundPolicyRoots(registration, "capabilityPolicy", "capability_policy", "operationPolicy", "operation_policy", "boundBodyPolicy", "bound_body_policy", "bodyPolicy", "body_policy", "policy") {
		entries = append(entries, boundEntriesFromOperationsMap(root, aliases)...)
		for _, key := range []string{"capabilities", "operations", "policies", "rules"} {
			for _, item := range firstArrayFromAny(root, key) {
				entry := mapFromAny(item)
				if len(entry) == 0 || !boundEntryMatchesOperation(entry, aliases) {
					continue
				}
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

func hostedBoundSoulPolicyEntry(registration map[string]any, operation boundOperation) map[string]any {
	policy := hostedBoundSoulPolicy(registration)
	if len(policy) == 0 {
		return nil
	}
	entry := map[string]any{
		"operation":     string(operation),
		"enabled":       false,
		"source":        "hosted-bound-soul/v1",
		"callerClasses": hostedBoundSoulAllowedCallerClasses(policy),
	}
	if version := hostedBoundSoulPolicyVersion(policy); version != "" {
		entry["policyVersion"] = version
	}
	if !hostedBoundSoulOperationalBindingAllowed(policy) {
		return entry
	}

	capabilities := firstPolicyMap(policy, "capabilities")
	phone := firstPolicyMap(capabilities, "phone")
	switch operation {
	case boundOperationEmailSend, boundOperationEmailRead, boundOperationEmailManage:
		email := firstPolicyMap(capabilities, "email")
		entry["enabled"] = boundPolicyValueEnabled(valueForPolicyKey(email, "defaultAllowed", "default_allowed"))
	case boundOperationSMSSend, boundOperationSMSRead:
		entry["enabled"] = boundPolicyValueEnabled(valueForPolicyKey(phone, "smsAllowed", "sms_allowed"))
		if state := firstNonEmptyStringMap(phone, "entitlementStatus", "entitlement_status", "status", "state"); state != "" {
			entry["entitlementState"] = state
		}
	case boundOperationVoiceRead:
		entry["enabled"] = boundPolicyValueEnabled(valueForPolicyKey(phone, "voiceAllowed", "voice_allowed"))
		if state := firstNonEmptyStringMap(phone, "entitlementStatus", "entitlement_status", "status", "state"); state != "" {
			entry["entitlementState"] = state
		}
	case boundOperationChannelsRead, boundOperationIdentitySelfRead:
		entry["enabled"] = true
	default:
		return nil
	}
	return entry
}

func hostedBoundSoulPolicy(registration map[string]any) map[string]any {
	for _, key := range []string{"policy", "effectivePolicy", "effective_policy"} {
		policy := firstPolicyMap(registration, key)
		if hostedBoundSoulPolicyLooksExplicit(policy) {
			return policy
		}
	}
	for _, key := range []string{"contactability", "commContactability", "comm_contactability"} {
		contactability := firstPolicyMap(registration, key)
		if policy := firstPolicyMap(contactability, "policy", "effectivePolicy", "effective_policy"); hostedBoundSoulPolicyLooksExplicit(policy) {
			return policy
		}
	}
	return nil
}

func hostedBoundSoulPolicyLooksExplicit(policy map[string]any) bool {
	if len(policy) == 0 {
		return false
	}
	if hostedBoundSoulPolicyVersion(policy) == "hosted-bound-soul/v1" {
		return true
	}
	return len(firstPolicyMap(policy, "capabilities")) > 0 &&
		len(firstPolicyMap(policy, "callerAccessPayment", "caller_access_payment")) > 0
}

func hostedBoundSoulPolicyVersion(policy map[string]any) string {
	return strings.ToLower(strings.TrimSpace(firstNonEmptyStringMap(policy, "version", "policyVersion", "policy_version")))
}

func hostedBoundSoulOperationalBindingAllowed(policy map[string]any) bool {
	return normalizePolicyToken(firstNonEmptyStringMap(policy, "operationalBinding", "operational_binding")) == "hosted_bound_soul"
}

func hostedBoundSoulAllowedCallerClasses(policy map[string]any) []any {
	if !hostedBoundSoulCallerPolicyPresent(policy) {
		return nil
	}
	classes := []any{
		string(boundCallerClassPrincipalOperator),
		string(boundCallerClassBoundBody),
		string(boundCallerClassInstanceKey),
	}
	callerAccessPayment := firstPolicyMap(policy, "callerAccessPayment", "caller_access_payment")
	if hostedBoundSoulCallerAccessAllows(callerAccessPayment, "allowlistedPeer", "allowlisted_peer") {
		classes = append(classes, string(boundCallerClassAllowlistedPeer))
	}
	return classes
}

func hostedBoundSoulCallerPolicyPresent(policy map[string]any) bool {
	if normalizePolicyToken(firstNonEmptyStringMap(policy, "callerAccessPaymentPolicyVersion", "caller_access_payment_policy_version")) == "caller_access_payment/v1" {
		return true
	}
	return len(firstPolicyMap(policy, "callerAccessPayment", "caller_access_payment")) > 0
}

func hostedBoundSoulCallerAccessAllows(root map[string]any, keys ...string) bool {
	for _, key := range keys {
		entry := firstPolicyMap(root, key)
		if len(entry) == 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(firstNonEmptyStringMap(entry, "access", "state", "status"))) {
		case "allowed", "allow", "enabled", "active", "provisioned", "paid":
			return true
		}
	}
	return false
}

func valueForPolicyKey(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := lookupPolicyKey(m, key); ok {
			return value
		}
	}
	return nil
}

func boundEntriesFromOperationsMap(root map[string]any, aliases []string) []map[string]any {
	if root == nil {
		return nil
	}
	out := []map[string]any{}
	for _, key := range []string{"operations", "operation", "capabilities", "capability"} {
		ops := firstPolicyMap(root, key)
		if len(ops) == 0 {
			continue
		}
		for _, alias := range aliases {
			raw, ok := lookupPolicyKey(ops, alias)
			if !ok {
				continue
			}
			entry := mapFromAny(raw)
			if len(entry) == 0 {
				entry = map[string]any{"operation": alias, "enabled": raw}
			} else if _, ok := entry["operation"]; !ok {
				entry["operation"] = alias
			}
			out = append(out, entry)
		}
	}
	return out
}

func boundPolicyRoots(registration map[string]any, keys ...string) []map[string]any {
	out := []map[string]any{registration}
	for _, key := range keys {
		if m := firstPolicyMap(registration, key); len(m) > 0 {
			out = append(out, m)
			for _, nestedKey := range keys {
				if nested := firstPolicyMap(m, nestedKey); len(nested) > 0 {
					out = append(out, nested)
				}
			}
		}
	}
	return out
}

func firstPolicyMap(raw map[string]any, keys ...string) map[string]any {
	if raw == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := lookupPolicyKey(raw, key); ok {
			m := mapFromAny(value)
			if len(m) > 0 {
				return m
			}
		}
	}
	return nil
}

func lookupPolicyKey(m map[string]any, key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	if value, ok := m[key]; ok {
		return value, true
	}
	normalized := normalizePolicyToken(key)
	for k, value := range m {
		if normalizePolicyToken(k) == normalized {
			return value, true
		}
	}
	return nil, false
}

func boundEntryMatchesOperation(entry map[string]any, aliases []string) bool {
	for _, key := range []string{"operation", "capability", "name", "tool", "id"} {
		value := strings.TrimSpace(fmt.Sprint(entry[key]))
		if value != "" && stringListContainsNormalized(aliases, value) {
			return true
		}
	}
	for _, key := range []string{"operations", "capabilities", "tools"} {
		values := boundStringListFromAny(entry[key])
		for _, value := range values {
			if stringListContainsNormalized(aliases, value) {
				return true
			}
		}
	}
	return false
}

func boundPolicyEntryEnabled(entry map[string]any) bool {
	if entry == nil {
		return false
	}
	if effect := strings.ToLower(strings.TrimSpace(fmt.Sprint(entry["effect"]))); effect == "deny" || effect == "disabled" {
		return false
	}
	for _, key := range []string{"enabled", "allowed", "allow", "active"} {
		if raw, ok := lookupPolicyKey(entry, key); ok {
			return boundPolicyValueEnabled(raw)
		}
	}
	if status := strings.ToLower(strings.TrimSpace(fmt.Sprint(entry["status"]))); status != "" {
		switch status {
		case "enabled", "active", "allowed", "provisioned", "paid":
			return true
		case "disabled", "denied", "blocked", "inactive":
			return false
		}
	}
	return true
}

func boundPolicyEntryAllowsCaller(entry map[string]any, callerClass boundCallerClass) bool {
	if entry == nil || callerClass == "" {
		return false
	}
	for _, key := range []string{"callerClasses", "caller_classes", "callers", "allowedCallers", "allowed_callers", "allowedCallerClasses", "allowed_caller_classes"} {
		values := boundStringListFromAny(entry[key])
		for _, value := range values {
			if normalizeCallerClass(value) == callerClass {
				return true
			}
		}
	}
	if classes := firstPolicyMap(entry, "classes", "callerClasses", "caller_classes", "callers"); len(classes) > 0 {
		for key, value := range classes {
			if normalizeCallerClass(key) != callerClass {
				continue
			}
			if boundPolicyValueEnabled(value) {
				return true
			}
		}
	}
	return false
}

func boundPolicyValueEnabled(raw any) bool {
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on", "enabled", "active", "allowed", "allow", "provisioned", "paid":
			return true
		default:
			return false
		}
	case map[string]any:
		return boundPolicyEntryEnabled(typed)
	default:
		return false
	}
}

func boundEntitlementPresent(entry map[string]any) bool {
	if entry == nil {
		return false
	}
	for _, key := range []string{"entitlement", "entitlements", "paymentPolicy", "payment_policy", "accessPaymentPolicy", "access_payment_policy"} {
		if m := firstPolicyMap(entry, key); len(m) > 0 && boundEntitlementMapAllows(m) {
			return true
		}
	}
	for _, key := range []string{"entitlementState", "entitlement_state", "entitlementStatus", "entitlement_status", "paymentState", "payment_state", "paymentStatus", "payment_status"} {
		if raw, ok := lookupPolicyKey(entry, key); ok {
			if boundEntitlementStateAllows(fmt.Sprint(raw)) {
				return true
			}
		}
	}
	for _, key := range []string{"paid", "provisioned", "entitled", "satisfied"} {
		if raw, ok := lookupPolicyKey(entry, key); ok && boundPolicyValueEnabled(raw) {
			return true
		}
	}
	return false
}

func boundEntitlementMapAllows(m map[string]any) bool {
	if m == nil {
		return false
	}
	for _, key := range []string{"status", "state", "entitlementStatus", "entitlement_state", "paymentStatus", "payment_state"} {
		if raw, ok := lookupPolicyKey(m, key); ok && boundEntitlementStateAllows(fmt.Sprint(raw)) {
			return true
		}
	}
	for _, key := range []string{"paid", "provisioned", "entitled", "satisfied", "active", "enabled", "allowed"} {
		if raw, ok := lookupPolicyKey(m, key); ok && boundPolicyValueEnabled(raw) {
			return true
		}
	}
	return false
}

func boundEntitlementStateAllows(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "paid", "provisioned", "active", "enabled", "allowed", "satisfied":
		return true
	default:
		return false
	}
}

func boundPolicyVersion(registration map[string]any) string {
	if policy := hostedBoundSoulPolicy(registration); len(policy) > 0 {
		if version := hostedBoundSoulPolicyVersion(policy); version != "" {
			return version
		}
	}
	for _, key := range []string{"capabilityPolicy", "capability_policy", "callerAccessPolicy", "caller_access_policy", "boundBodyPolicy", "bound_body_policy", "policy"} {
		root := firstPolicyMap(registration, key)
		if len(root) == 0 {
			continue
		}
		if version := firstNonEmptyStringMap(root, "policyVersion", "policy_version", "version"); version != "" {
			return version
		}
	}
	if version := firstNonEmptyStringMap(registration, "policyVersion", "policy_version"); version != "" {
		return version
	}
	return ""
}

func normalizeBoundOperation(operation boundOperation) boundOperation {
	value := strings.TrimSpace(string(operation))
	switch value {
	case string(boundOperationEmailSend):
		return boundOperationEmailSend
	case string(boundOperationEmailRead):
		return boundOperationEmailRead
	case string(boundOperationEmailManage):
		return boundOperationEmailManage
	case string(boundOperationSMSSend):
		return boundOperationSMSSend
	case string(boundOperationSMSRead):
		return boundOperationSMSRead
	case string(boundOperationVoiceRead):
		return boundOperationVoiceRead
	case string(boundOperationChannelsRead):
		return boundOperationChannelsRead
	case string(boundOperationIdentitySelfRead):
		return boundOperationIdentitySelfRead
	default:
		return boundOperation(value)
	}
}

func boundOperationRequiresEntitlement(operation boundOperation) bool {
	switch operation {
	case boundOperationSMSSend, boundOperationSMSRead, boundOperationVoiceRead:
		return true
	default:
		return false
	}
}

func boundOperationChannel(operation boundOperation) string {
	switch operation {
	case boundOperationEmailSend, boundOperationEmailRead, boundOperationEmailManage:
		return "email"
	case boundOperationSMSSend, boundOperationSMSRead:
		return "sms"
	case boundOperationVoiceRead:
		return "voice"
	default:
		return ""
	}
}

func boundOperationAliases(operation boundOperation) []string {
	switch operation {
	case boundOperationEmailSend:
		return []string{"communication.email.send", "email.send", "email_send", "email-send", "email:send"}
	case boundOperationEmailRead:
		return []string{"communication.email.read", "email.read", "email_read", "email-read", "email:read"}
	case boundOperationEmailManage:
		return []string{"communication.email.manage", "email.manage", "email_manage", "email-manage", "email:manage"}
	case boundOperationSMSSend:
		return []string{"communication.sms.send", "sms.send", "sms_send", "sms-send", "sms:send"}
	case boundOperationSMSRead:
		return []string{"communication.sms.read", "sms.read", "sms_read", "sms-read", "sms:read"}
	case boundOperationVoiceRead:
		return []string{"communication.voice.read", "voice.read", "voice_read", "voicemail_read", "voicemail.read", "voice-receive"}
	case boundOperationChannelsRead:
		return []string{"communication.channels.read", "channels.read", "channels_read", "channels-read"}
	case boundOperationIdentitySelfRead:
		return []string{"identity.self.read", "identity_whoami", "identity.whoami", "identity-self-read"}
	default:
		if operation == "" {
			return nil
		}
		return []string{string(operation)}
	}
}

func boundStringListFromAny(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		parts := strings.FieldsFunc(typed, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\n' || r == '\t'
		})
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	default:
		return nil
	}
}

func stringListContainsNormalized(values []string, want string) bool {
	want = normalizePolicyToken(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if normalizePolicyToken(value) == want {
			return true
		}
	}
	return false
}

func normalizePolicyToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", ".", "_", ":", "_", " ", "_").Replace(value)
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}
