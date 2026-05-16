package mcpserver

import (
	"context"
	"testing"

	"github.com/equaltoai/lesser-body/internal/auth"
)

func TestBoundOperationPolicyDeniesBindingWithoutExplicitCapabilityPolicy(t *testing.T) {
	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1", Scopes: []string{"write"}},
	}, "token")

	registration := map[string]any{
		"version":            "3",
		"channels":           map[string]any{"email": map[string]any{"address": "agent@example.com", "capabilities": []any{"email-send"}}},
		"contactPreferences": map[string]any{},
	}

	decision := decideBoundOperation(ctx, registration, boundOperationEmailSend)
	if decision.Allowed {
		t.Fatalf("expected binding-only registration to deny email_send")
	}
	if decision.Reason != "capability_policy_denied" {
		t.Fatalf("expected capability_policy_denied, got %+v", decision)
	}
}

func TestBoundOperationPolicyAllowsHostEffectivePolicy(t *testing.T) {
	ctx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1", Scopes: []string{"write"}},
	}, "token")

	registration := map[string]any{
		"version": "3",
		"policy": map[string]any{
			"version":                          "hosted-bound-soul/v1",
			"operationalBinding":               "hosted_bound_soul",
			"capabilityPolicyVersion":          "capability-policy/v1",
			"callerAccessPaymentPolicyVersion": "caller-access-payment/v1",
			"capabilities": map[string]any{
				"email": map[string]any{"defaultAllowed": true},
				"phone": map[string]any{
					"entitlementStatus": "not_entitled",
					"smsAllowed":        false,
					"voiceAllowed":      false,
				},
			},
			"callerAccessPayment": map[string]any{
				"publicPaidCaller": map[string]any{"access": "denied"},
			},
		},
	}

	if decision := decideBoundOperation(ctx, registration, boundOperationEmailSend); !decision.Allowed {
		t.Fatalf("expected hosted-bound-soul/v1 email policy to allow email_send, got %+v", decision)
	}
	operatorCtx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1", DelegatedBy: "operator"},
	}, "token")
	if decision := decideBoundOperation(operatorCtx, registration, boundOperationEmailSend); !decision.Allowed {
		t.Fatalf("expected principal operator caller to allow email_send under hosted policy, got %+v", decision)
	}
	peerCtx := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1", ClientClass: "allowlisted_peer"},
	}, "token")
	if decision := decideBoundOperation(peerCtx, registration, boundOperationEmailSend); decision.Allowed || decision.Reason != "caller_access_policy_denied" {
		t.Fatalf("expected allowlisted peer to require explicit caller access policy, got %+v", decision)
	}
	if decision := decideBoundOperation(ctx, registration, boundOperationSMSSend); decision.Allowed || decision.Reason != "capability_policy_denied" {
		t.Fatalf("expected hosted-bound-soul/v1 phone policy to deny unentitled sms_send, got %+v", decision)
	}
	registration["policy"].(map[string]any)["capabilities"].(map[string]any)["phone"].(map[string]any)["entitlementStatus"] = "provisioned"
	registration["policy"].(map[string]any)["capabilities"].(map[string]any)["phone"].(map[string]any)["smsAllowed"] = true
	if decision := decideBoundOperation(ctx, registration, boundOperationSMSSend); !decision.Allowed {
		t.Fatalf("expected hosted-bound-soul/v1 provisioned sms policy to allow sms_send, got %+v", decision)
	}
}

func TestBoundOperationPolicyDistinguishesCallerClasses(t *testing.T) {
	scenarios := []struct {
		name      string
		principal *auth.Principal
		want      boundCallerClass
	}{
		{
			name: "bound body default",
			principal: &auth.Principal{
				Type:     auth.PrincipalTypeOAuthToken,
				Identity: "agent1",
				Claims:   &auth.Claims{Username: "agent1"},
			},
			want: boundCallerClassBoundBody,
		},
		{
			name: "principal operator from delegated claim",
			principal: &auth.Principal{
				Type:     auth.PrincipalTypeOAuthToken,
				Identity: "agent1",
				Claims:   &auth.Claims{Username: "agent1", DelegatedBy: "operator"},
			},
			want: boundCallerClassPrincipalOperator,
		},
		{
			name: "instance key",
			principal: &auth.Principal{
				Type:     auth.PrincipalTypeInstanceKey,
				Identity: "instance",
			},
			want: boundCallerClassInstanceKey,
		},
		{
			name: "allowlisted peer",
			principal: &auth.Principal{
				Type:     auth.PrincipalTypeOAuthToken,
				Identity: "agent1",
				Claims:   &auth.Claims{Username: "agent1", ClientClass: "allowlisted_peer"},
			},
			want: boundCallerClassAllowlistedPeer,
		},
		{
			name: "public paid",
			principal: &auth.Principal{
				Type:     auth.PrincipalTypeOAuthToken,
				Identity: "agent1",
				Claims:   &auth.Claims{Username: "agent1", ClientClass: "public_paid"},
			},
			want: boundCallerClassPublicPaid,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := auth.InjectToolContext(context.Background(), scenario.principal, "token")
			if got := classifyBoundOperationCaller(ctx); got != scenario.want {
				t.Fatalf("caller class: got %q want %q", got, scenario.want)
			}
		})
	}
}

func TestBoundOperationPolicyDeniesPublicPaidAndRequiresPhoneEntitlement(t *testing.T) {
	registration := map[string]any{
		"version": "3",
		"capabilityPolicy": map[string]any{
			"version": "2026-05-16",
			"operations": map[string]any{
				"communication.email.send": map[string]any{
					"enabled":       true,
					"callerClasses": []any{"bound_body", "public_paid"},
				},
				"communication.sms.send": map[string]any{
					"enabled":       true,
					"callerClasses": []any{"bound_body"},
				},
			},
		},
		"callerAccessPolicy": map[string]any{
			"classes": map[string]any{
				"bound_body":  map[string]any{"enabled": true},
				"public_paid": map[string]any{"enabled": true},
			},
		},
	}

	publicPaid := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1", ClientClass: "public_paid"},
	}, "token")
	if decision := decideBoundOperation(publicPaid, registration, boundOperationEmailSend); decision.Allowed || decision.Reason != "public_paid_callers_denied_in_m1" {
		t.Fatalf("expected public paid caller to be modeled but denied in M1, got %+v", decision)
	}

	boundBody := auth.InjectToolContext(context.Background(), &auth.Principal{
		Type:     auth.PrincipalTypeOAuthToken,
		Identity: "agent1",
		Claims:   &auth.Claims{Username: "agent1"},
	}, "token")
	if decision := decideBoundOperation(boundBody, registration, boundOperationSMSSend); decision.Allowed || decision.Reason != "paid_or_provisioned_entitlement_required" {
		t.Fatalf("expected sms_send to require paid/provisioned entitlement, got %+v", decision)
	}

	registration["capabilityPolicy"].(map[string]any)["operations"].(map[string]any)["communication.sms.send"].(map[string]any)["entitlement"] = map[string]any{"state": "provisioned"}
	if decision := decideBoundOperation(boundBody, registration, boundOperationSMSSend); !decision.Allowed {
		t.Fatalf("expected provisioned sms entitlement to allow operation, got %+v", decision)
	}
}
