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
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: emailSendDef(), Handler: handleEmailSend},
		{Def: emailReadDef(), Handler: handleNotImplemented},
		{Def: emailSearchDef(), Handler: handleNotImplemented},
		{Def: emailReplyDef(), Handler: handleEmailReply},
		{Def: emailDeleteDef(), Handler: handleNotImplemented},
		{Def: smsSendDef(), Handler: handleNotImplemented},
		{Def: smsReadDef(), Handler: handleNotImplemented},
		{Def: phoneCallDef(), Handler: handleNotImplemented},
		{Def: voicemailReadDef(), Handler: handleNotImplemented},
		{Def: identityWhoamiDef(), Handler: handleIdentityWhoami},
		{Def: identityLookupDef(), Handler: handleIdentityLookup},
		{Def: identityVerifyDef(), Handler: handleNotImplemented},
	} {
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
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"to":{"type":"string"},
				"subject":{"type":"string"},
				"body":{"type":"string"},
				"cc":{"type":"array","items":{"type":"string"}},
				"bcc":{"type":"array","items":{"type":"string"}},
				"replyTo":{"type":"string"}
			},
			"required":["to","subject","body"]
		}`),
	}
}

func emailReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_read",
		Description: "Read recent emails delivered to the agent's inbox.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"folder":{"type":"string"},
				"unreadOnly":{"type":"boolean"},
				"limit":{"type":"integer","minimum":1,"maximum":200},
				"since":{"type":"string"}
			}
		}`),
	}
}

func emailSearchDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_search",
		Description: "Search the agent's email (inbox abstraction).",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"},
				"folder":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":200}
			},
			"required":["query"]
		}`),
	}
}

func emailReplyDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_reply",
		Description: "Reply to a specific email message.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string"},
				"body":{"type":"string"},
				"replyAll":{"type":"boolean"}
			},
			"required":["messageId","body"]
		}`),
	}
}

func emailDeleteDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "email_delete",
		Description: "Delete or archive an email message.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messageId":{"type":"string"},
				"action":{"type":"string","enum":["delete","archive"]}
			},
			"required":["messageId","action"]
		}`),
	}
}

func smsSendDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "sms_send",
		Description: "Send an SMS from the agent's number via lesser-host.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"to":{"type":"string"},
				"body":{"type":"string"}
			},
			"required":["to","body"]
		}`),
	}
}

func smsReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "sms_read",
		Description: "Read received SMS messages delivered to the instance.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"unreadOnly":{"type":"boolean"},
				"limit":{"type":"integer","minimum":1,"maximum":200},
				"since":{"type":"string"}
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
				"maxDurationMinutes":{"type":"integer","minimum":1,"maximum":180}
			},
			"required":["to","purpose"]
		}`),
	}
}

func voicemailReadDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:        "voicemail_read",
		Description: "Read voicemail transcriptions delivered to the instance.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"unreadOnly":{"type":"boolean"},
				"limit":{"type":"integer","minimum":1,"maximum":200}
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
		Description: "Look up an agent by ENS name, agentId, or email address.",
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
		Description: "Verify that a communication came from a specific soul identity.",
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
