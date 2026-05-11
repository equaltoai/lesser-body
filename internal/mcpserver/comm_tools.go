package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	mcpruntime "github.com/theory-cloud/apptheory/runtime/mcp"
)

func registerCommunicationTools(r *mcpruntime.ToolRegistry) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	for _, tool := range []struct {
		Def       mcpruntime.ToolDef
		Handler   mcpruntime.ToolHandler
		Streaming mcpruntime.StreamingToolHandler
	}{
		{Def: emailSendDef(), Handler: handleEmailSend, Streaming: handleEmailSendStreaming},
		{Def: emailReadDef(), Handler: handleEmailRead},
		{Def: emailGetDef(), Handler: handleEmailGet},
		{Def: emailGetContentDef(), Handler: handleEmailGetContent},
		{Def: emailSearchDef(), Handler: handleEmailSearch},
		{Def: emailReplyDef(), Handler: handleEmailReply, Streaming: handleEmailReplyStreaming},
		{Def: emailDeleteDef(), Handler: handleEmailDelete},
		{Def: emailMarkReadDef(), Handler: handleEmailMarkRead},
		{Def: emailMarkUnreadDef(), Handler: handleEmailMarkUnread},
		{Def: smsSendDef(), Handler: handleSmsSend, Streaming: handleSmsSendStreaming},
		{Def: smsReadDef(), Handler: handleSmsRead},
		{Def: voicemailReadDef(), Handler: handleVoicemailRead},
		{Def: identityWhoamiDef(), Handler: handleIdentityWhoami},
		{Def: soulReadDef(), Handler: handleSoulRead},
		{Def: identityLookupDef(), Handler: handleIdentityLookup},
		{Def: identityVerifyDef(), Handler: handleIdentityVerify},
	} {
		if tool.Streaming != nil {
			if err := r.RegisterStreamingTool(tool.Def, tool.Streaming); err != nil {
				return err
			}
			continue
		}
		if err := r.RegisterTool(tool.Def, tool.Handler); err != nil {
			return err
		}
	}

	return nil
}

func handleNotImplemented(_ context.Context, _ json.RawMessage) (*mcpruntime.ToolResult, error) {
	return toolErrorResult("not_implemented", "not implemented", 501, nil)
}

func emailSendDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_send",
		Description: "Send an email from the agent's address via lesser-host (no provider credentials).",
		Annotations: destructiveToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"to":{"type":"string"},
				"subject":{"type":"string"},
				"body":{"type":"string"},
				"cc":{"type":"array","items":{"type":"string"}},
				"bcc":{"type":"array","items":{"type":"string"}},
				"replyTo":{"type":"string"},
				"messageId":{"type":"string"},
				"inReplyTo":{"type":"string"}
			},
			"required":["to","subject","body"]
		}`),
	}
}

func emailReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_read",
		Description: "List recent email metadata/previews from lesser-host's canonical mailbox.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"folder":{"type":"string","enum":["inbox","sent"]},
				"unreadOnly":{"type":"boolean"},
				"include_raw":{"type":"boolean","description":"Include verbose upstream mailbox data under _raw. Defaults to false."},
				"read":{"type":"boolean","description":"Exact read-state filter. Conflicts with unreadOnly=true when read=true."},
				"includeArchived":{"type":"boolean"},
				"archived":{"type":"boolean","description":"Exact archive-state filter."},
				"includeDeleted":{"type":"boolean"},
				"deleted":{"type":"boolean","description":"Exact delete-state filter."},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"cursor":{"type":"string"},
				"since":{"type":"string","description":"Legacy alias for cursor."},
				"threadId":{"type":"string"}
			}
		}`),
	}
}

func emailGetDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_get",
		Description: "Get canonical email metadata/state by opaque host message reference.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string","description":"Opaque host messageRef returned by email_read/email_search/email_send/email_reply."},
				"include_raw":{"type":"boolean","description":"Include verbose upstream mailbox data under _raw. Defaults to false."}
			},
			"required":["messageId"]
		}`),
	}
}

func emailGetContentDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_get_content",
		Description: "Fetch full email content explicitly from lesser-host's canonical mailbox.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string","description":"Opaque host messageRef returned by email_read/email_search/email_get."}
			},
			"required":["messageId"]
		}`),
	}
}

func emailSearchDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_search",
		Description: "Run a bounded lesser-host metadata/preview search over the agent's email mailbox.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"},
				"folder":{"type":"string","enum":["inbox","sent"]},
				"include_raw":{"type":"boolean","description":"Include verbose upstream mailbox data under _raw. Defaults to false."},
				"includeArchived":{"type":"boolean"},
				"archived":{"type":"boolean","description":"Exact archive-state filter."},
				"includeDeleted":{"type":"boolean"},
				"deleted":{"type":"boolean","description":"Exact delete-state filter."},
				"read":{"type":"boolean","description":"Exact read-state filter."},
				"unreadOnly":{"type":"boolean"},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"cursor":{"type":"string"},
				"threadId":{"type":"string"}
			},
			"required":["query"]
		}`),
	}
}

func emailReplyDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_reply",
		Description: "Reply to a specific email message.",
		Annotations: destructiveToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string","description":"Opaque host messageRef for the source email."},
				"body":{"type":"string"},
				"to":{"type":"string","description":"Deprecated for host-backed replies; recipient is resolved by host."},
				"subject":{"type":"string"},
				"cc":{"type":"array","items":{"type":"string"}},
				"bcc":{"type":"array","items":{"type":"string"}},
				"replyTo":{"type":"string"},
				"replyAll":{"type":"boolean"},
				"idempotencyKey":{"type":"string"}
			},
			"required":["messageId","body"]
		}`),
	}
}

func emailDeleteDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_delete",
		Description: "Delete or archive an email message in lesser-host's canonical mailbox.",
		Annotations: destructiveToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string","description":"Opaque host messageRef returned by email_read/email_search/email_get."},
				"action":{"type":"string","enum":["delete","archive"]}
			},
			"required":["messageId","action"]
		}`),
	}
}

func emailMarkReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_mark_read",
		Description: "Mark an email read in lesser-host's canonical mailbox.",
		Annotations: idempotentMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string","description":"Opaque host messageRef returned by email_read/email_search/email_get."}
			},
			"required":["messageId"]
		}`),
	}
}

func emailMarkUnreadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_mark_unread",
		Description: "Mark an email unread in lesser-host's canonical mailbox.",
		Annotations: idempotentMutationToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string","description":"Opaque host messageRef returned by email_read/email_search/email_get."}
			},
			"required":["messageId"]
		}`),
	}
}

func smsSendDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "sms_send",
		Description: "Send an SMS from the agent's number via lesser-host.",
		Annotations: destructiveToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"to":{"type":"string"},
				"body":{"type":"string"},
				"messageId":{"type":"string"},
				"inReplyTo":{"type":"string"}
			},
			"required":["to","body"]
		}`),
	}
}

func smsReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "sms_read",
		Description: "Read received SMS metadata/previews from lesser-host's canonical mailbox.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"unreadOnly":{"type":"boolean"},
				"include_raw":{"type":"boolean","description":"Include verbose upstream mailbox data under _raw. Defaults to false."},
				"read":{"type":"boolean","description":"Exact read-state filter. Conflicts with unreadOnly=true when read=true."},
				"includeArchived":{"type":"boolean"},
				"archived":{"type":"boolean","description":"Exact archive-state filter."},
				"includeDeleted":{"type":"boolean"},
				"deleted":{"type":"boolean","description":"Exact delete-state filter."},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"cursor":{"type":"string"},
				"since":{"type":"string","description":"Legacy alias for cursor."},
				"threadId":{"type":"string"}
			}
		}`),
	}
}

func phoneCallDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "phone_call",
		Description: "Initiate a voice call via lesser-host.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"to":{"type":"string"},
				"purpose":{"type":"string"},
				"maxDurationMinutes":{"type":"integer","minimum":1,"maximum":180},
				"messageId":{"type":"string"},
				"inReplyTo":{"type":"string"}
			},
			"required":["to","purpose"]
		}`),
	}
}

func voicemailReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "voicemail_read",
		Description: "Read voicemail metadata/previews from lesser-host's canonical mailbox.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"unreadOnly":{"type":"boolean"},
				"include_raw":{"type":"boolean","description":"Include verbose upstream mailbox data under _raw. Defaults to false."},
				"read":{"type":"boolean","description":"Exact read-state filter. Conflicts with unreadOnly=true when read=true."},
				"includeArchived":{"type":"boolean"},
				"archived":{"type":"boolean","description":"Exact archive-state filter."},
				"includeDeleted":{"type":"boolean"},
				"deleted":{"type":"boolean","description":"Exact delete-state filter."},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"cursor":{"type":"string"},
				"threadId":{"type":"string"}
			}
		}`),
	}
}

func soulReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "soul_read",
		Description: "Read a public-only soul identity bundle from Host/Soul public endpoints. Private email/phone reachability and contact preferences are omitted or marked unavailable.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string","description":"Full soul agentId, ENS name, current-instance local ID, explicit @user@domain ActivityPub handle, or canonical actor URL."},
				"agentId":{"type":"string","description":"Full soul agent ID. Takes precedence over query when provided."},
				"ensName":{"type":"string","description":"Public ENS name. Takes precedence over query when agentId is absent."},
				"limit":{"type":"integer","minimum":1,"maximum":3,"description":"Maximum matches to compose when query resolves through search. Defaults to 1."},
				"include_raw":{"type":"boolean","description":"Include raw public Host/Soul endpoint payloads under _raw for audit/debug use. Defaults to false."}
			}
		}`),
	}
}

func identityWhoamiDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "identity_whoami",
		Description: "Return this agent's full identity including communication channels and preferences.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func identityLookupDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "identity_lookup",
		Description: "Look up a public soul identity by full agentId, ENS name, a current-instance local ID such as medic, an explicit remote ActivityPub handle such as @steward@remote.example, or a canonical actor URL such as https://remote.example/users/steward. Private email/phone reachability lookup fails closed until lesser-host exposes a body-facing resolver.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"}
			},
			"required":["query"]
		}`),
	}
}

func identityVerifyDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "identity_verify",
		Description: "Verify that a communication came from a specific soul identity. ENS verification uses public resolution plus authoritative message provenance; private email/phone verification fails closed until lesser-host exposes a body-facing resolver.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"channel":{"type":"string","enum":["ens","email","phone"]},
				"identifier":{"type":"string"},
				"messageId":{"type":"string"}
			},
			"required":["channel","identifier"]
		}`),
	}
}
