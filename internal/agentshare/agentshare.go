// Package agentshare canonicalizes lesser principal usernames shared between
// body's actor-admission path and lesser's identity model.
package agentshare

import "strings"

// NormalizePrincipalUsername canonicalizes a lesser principal or DelegatedBy
// value to its bare local-username form: a leading "@" is stripped, the value
// is lowercased and trimmed. Body records the share-grant caller in this form
// so downstream attribution and error details use a stable canonical username.
func NormalizePrincipalUsername(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "@")
	return strings.ToLower(strings.TrimSpace(value))
}
