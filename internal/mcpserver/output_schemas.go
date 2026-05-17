package mcpserver

import (
	"encoding/json"
)

func genericDataObjectOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{"type":"object","additionalProperties":true}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func memoryEventOutputSchemaProperties() string {
	return `"event_id":{"type":"string"},
		"occurred_at":{"type":"string"},
		"content":{"type":"string"},
		"tags":{"type":"array","items":{"type":"string"}},
		"expires_at":{"type":"string"}`
}

func memoryAppendOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"event":{
				"type":"object",
				"properties":{` + memoryEventOutputSchemaProperties() + `},
				"required":["event_id","occurred_at","content"]
			},
			"created":{"type":"boolean"}
		},
		"required":["event","created"],
		"additionalProperties":false
	}`)
}

func memoryQueryOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"events":{
				"type":"array",
				"items":{
					"type":"object",
					"properties":{` + memoryEventOutputSchemaProperties() + `},
					"required":["event_id","occurred_at","content"]
				}
			},
			"next_cursor":{"type":"string"}
		},
		"required":["events"],
		"additionalProperties":false
	}`)
}

func skillsCatalogOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"authority":{"type":"object","additionalProperties":true},
					"items":{"type":"array","items":{"type":"object","additionalProperties":true}},
					"bundles":{"type":"array","items":{"type":"object","additionalProperties":true}},
					"nextCursor":{"type":"string"}
				},
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func skillBundleGetOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"authority":{"type":"object","additionalProperties":true},
					"bundle":{"type":"object","additionalProperties":true},
					"content":{"type":"object","additionalProperties":true},
					"verification":{"type":"object","additionalProperties":true}
				},
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func postGetOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"id":{"type":"string"},
					"view":{"type":"string"},
					"source":{"type":"string"},
					"status":{"type":"object","additionalProperties":true},
					"statusRef":{"type":"object","additionalProperties":true}
				},
				"required":["id","view","source","status","statusRef"],
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func notificationGetOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"id":{"type":"string"},
					"view":{"type":"string"},
					"source":{"type":"string"},
					"notification":{"type":"object","additionalProperties":true},
					"notificationRef":{"type":"object","additionalProperties":true}
				},
				"required":["id","view","source","notification","notificationRef"],
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func soulReadOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"query":{"type":"string"},
					"count":{"type":"integer"},
					"access":{"type":"object","additionalProperties":true},
					"souls":{"type":"array","items":{"type":"object","additionalProperties":true}}
				},
				"required":["query","count","access","souls"],
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func identityWhoamiOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"agentId":{"type":"string"},
					"domain":{"type":"string"},
					"localId":{"type":"string"},
					"status":{"type":"string"},
					"channels":{"type":"object","additionalProperties":true},
					"contactPreferences":{"type":"object","additionalProperties":true}
				},
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func identityLookupOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"query":{"type":"string"},
					"matches":{"type":"array","items":{"type":"object","additionalProperties":true}},
					"count":{"type":"integer"}
				},
				"required":["query","matches","count"],
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func identityVerifyOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"channel":{"type":"string"},
					"identifier":{"type":"string"},
					"verificationScope":{"type":"string"},
					"identityResolved":{"type":"boolean"},
					"verified":{"type":"boolean"},
					"agent":{"type":"object","additionalProperties":true},
					"reason":{"type":"string"},
					"messageId":{"type":"string"}
				},
				"required":["channel","identifier","verificationScope","identityResolved","verified"],
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func commSendOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"data":{
				"type":"object",
				"properties":{
					"messageId":{"type":"string"},
					"messageRef":{"type":"string"},
					"deliveryId":{"type":"string"},
					"hostMessageId":{"type":"string"},
					"idempotencyKey":{"type":"string"},
					"status":{"type":"string"},
					"threadId":{"type":"string"},
					"channel":{"type":"string"},
					"agentId":{"type":"string"},
					"to":{"type":"string"},
					"provider":{"type":"string"},
					"providerMessageId":{"type":"string"},
					"createdAt":{"type":"string"},
					"inReplyTo":{"type":"string"},
					"replyResolvedByHost":{"type":"boolean"},
					"advisory":{"type":"object","additionalProperties":true},
					"delivery":{"type":"object","additionalProperties":true},
					"result":{"additionalProperties":true}
				},
				"required":["messageId","status","idempotencyKey"],
				"additionalProperties":true
			}
		},
		"required":["data"],
		"additionalProperties":false
	}`)
}

func mailboxMessageSummaryOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"messageId":{"type":"string"},
			"messageRef":{"type":"string"},
			"deliveryId":{"type":"string"},
			"hostMessageId":{"type":"string"},
			"threadId":{"type":"string"},
			"channel":{"type":"string"},
			"channelType":{"type":"string"},
			"direction":{"type":"string"},
			"status":{"type":"string"},
			"from":{"type":"object","additionalProperties":true},
			"to":{"type":"object","additionalProperties":true},
			"subject":{"type":"string"},
			"preview":{"type":"string"},
			"body":{"type":"string"},
			"bodyIsPreview":{"type":"boolean"},
			"content":{"type":"object","additionalProperties":true},
			"state":{"type":"object","additionalProperties":true},
			"createdAt":{"type":"string"},
			"receivedAt":{"type":"string"},
			"updatedAt":{"type":"string"}
		},
		"additionalProperties":true
	}`)
}

func mailboxListOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"source":{"type":"string"},
			"messages":{"type":"array","items":` + string(mailboxMessageSummaryOutputSchema()) + `},
			"count":{"type":"integer"},
			"hasMore":{"type":"boolean"},
			"nextCursor":{"type":"string"},
			"nextSince":{"type":"string"},
			"notes":{
				"type":"object",
				"properties":{
					"authority":{"type":"string"},
					"bodyField":{"type":"string"},
					"messageIdRef":{"type":"string"},
					"legacySinceName":{"type":"string"}
				},
				"required":["authority","bodyField","messageIdRef","legacySinceName"],
				"additionalProperties":true
			},
			"folder":{"type":"string"},
			"query":{"type":"string"},
			"strategy":{"type":"string"},
			"unreadOnly":{"type":"boolean"},
			"read":{"type":"boolean"},
			"includeArchived":{"type":"boolean"},
			"archived":{"type":"boolean"},
			"includeDeleted":{"type":"boolean"},
			"deleted":{"type":"boolean"},
			"cursor":{"type":"string"},
			"since":{"type":"string"}
		},
		"required":["source","messages","count","hasMore","nextCursor","nextSince","notes"],
		"additionalProperties":true
	}`)
}

func mailboxGetOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"message":` + string(mailboxMessageSummaryOutputSchema()) + `,
			"source":{"type":"string"}
		},
		"required":["message"],
		"additionalProperties":true
	}`)
}

func mailboxContentOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"source":{"type":"string"},
			"messageId":{"type":"string"},
			"messageRef":{"type":"string"},
			"deliveryId":{"type":"string"},
			"hostMessageId":{"type":"string"},
			"contentType":{"type":"string"},
			"sha256":{"type":"string"},
			"bytes":{"type":"integer"},
			"body":{"type":"string"},
			"instanceSlug":{"type":"string"},
			"agentId":{"type":"string"}
		},
		"required":["source","messageId","body"],
		"additionalProperties":true
	}`)
}

func mailboxMutationOutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"messageId":{"type":"string"},
			"messageRef":{"type":"string"},
			"action":{"type":"string"},
			"message":` + string(mailboxMessageSummaryOutputSchema()) + `,
			"state":{"type":"object","additionalProperties":true},
			"source":{"type":"string"}
		},
		"required":["messageId","messageRef","action","message","state"],
		"additionalProperties":true
	}`)
}
