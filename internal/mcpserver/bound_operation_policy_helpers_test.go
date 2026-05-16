package mcpserver

import (
	"fmt"
	"strings"
)

func boundBodyPolicyJSONForTest(operations ...string) string {
	entries := make([]string, 0, len(operations))
	for _, operation := range operations {
		operation = strings.TrimSpace(operation)
		if operation == "" {
			continue
		}
		entitlement := ""
		if strings.Contains(operation, ".sms.") || strings.Contains(operation, ".voice.") {
			entitlement = `,"entitlement":{"state":"provisioned"}`
		}
		entries = append(entries, fmt.Sprintf(`%q:{"enabled":true,"callerClasses":["bound_body"]%s}`, operation, entitlement))
	}
	return `"capabilityPolicy":{"version":"2026-05-16","operations":{` + strings.Join(entries, ",") + `}},"callerAccessPolicy":{"classes":{"bound_body":{"enabled":true}}}`
}
