package mcpserver

import (
	"sync"

	mcpruntime "github.com/theory-cloud/apptheory/v2/runtime/mcp"
)

// Scope names carried in a caller's JWT claims.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
)

// StrictestToolScope is the scope demanded of any registered tool that carries no
// explicit classification in toolScopes. It is deliberately the scope that the
// fewest callers hold: a tool added without a classification entry is unreachable
// rather than silently readable.
const StrictestToolScope = ScopeAdmin

// toolScopes is the exhaustive, explicit scope classification for every tool
// registered by registerTools. A tool is classified write when invoking it has a
// side effect on the actor's world -- a post, a follow, a memory append, an
// outbound email or SMS delegated to lesser-host. Everything else is read.
//
// TestEveryRegisteredToolIsClassified fails when a registered tool is missing
// here, and TestNoStaleToolScopeEntries fails when an entry no longer maps to a
// registered tool. Adding a tool without adding it here is a test failure, not a
// silent read-scope grant.
var toolScopes = map[string][]string{
	// Diagnostics.
	"echo": {ScopeRead},

	// Social reads.
	"profile_read":          {ScopeRead},
	"timeline_read":         {ScopeRead},
	"post_search":           {ScopeRead},
	"post_get":              {ScopeRead},
	"followers_list":        {ScopeRead},
	"following_list":        {ScopeRead},
	"conversations_read":    {ScopeRead},
	"conversation_get":      {ScopeRead},
	"direct_messages_read":  {ScopeRead},
	"message_requests_list": {ScopeRead},
	"notifications_read":    {ScopeRead},
	"notification_get":      {ScopeRead},

	// Social writes.
	"post_create":             {ScopeWrite},
	"post_boost":              {ScopeWrite},
	"post_favorite":           {ScopeWrite},
	"follow":                  {ScopeWrite},
	"unfollow":                {ScopeWrite},
	"profile_update":          {ScopeWrite},
	"notification_dismiss":    {ScopeWrite},
	"message_request_accept":  {ScopeWrite},
	"message_request_decline": {ScopeWrite},

	// Article reads.
	"article_draft_get":     {ScopeRead},
	"article_draft_list":    {ScopeRead},
	"article_draft_preview": {ScopeRead},
	"article_get":           {ScopeRead},
	"article_list":          {ScopeRead},

	// Article writes.
	"article_draft_create":  {ScopeWrite},
	"article_draft_update":  {ScopeWrite},
	"article_draft_publish": {ScopeWrite},
	"article_update":        {ScopeWrite},

	// Memory.
	"memory_query":  {ScopeRead},
	"memory_append": {ScopeWrite},

	// Communication reads.
	"email_read":        {ScopeRead},
	"email_get":         {ScopeRead},
	"email_get_content": {ScopeRead},
	"email_search":      {ScopeRead},
	"sms_read":          {ScopeRead},
	"voicemail_read":    {ScopeRead},

	// Communication writes: every one of these delegates an outbound side effect
	// or a mailbox state change to lesser-host.
	"email_send":        {ScopeWrite},
	"email_reply":       {ScopeWrite},
	"email_delete":      {ScopeWrite},
	"email_mark_read":   {ScopeWrite},
	"email_mark_unread": {ScopeWrite},
	"sms_send":          {ScopeWrite},

	// Identity reads.
	"identity_whoami": {ScopeRead},
	"identity_lookup": {ScopeRead},
	"identity_verify": {ScopeRead},
	"soul_read":       {ScopeRead},

	// Skills reads.
	"skills_catalog":   {ScopeRead},
	"skill_bundle_get": {ScopeRead},
}

// registeredToolSnapshot is the set of tool names registerTools actually
// registers. It is derived from the same registerTools the app runs, so it cannot
// drift from the live surface.
type registeredToolSnapshot struct {
	names map[string]struct{}
	err   error
}

var registeredTools = sync.OnceValue(func() registeredToolSnapshot {
	r := mcpruntime.NewToolRegistry()
	if err := registerTools(r); err != nil {
		return registeredToolSnapshot{err: err}
	}
	names := make(map[string]struct{}, r.Len())
	for _, def := range r.List() {
		names[def.Name] = struct{}{}
	}
	return registeredToolSnapshot{names: names}
})

// RequiredScopesForTool returns the scopes a caller must hold to invoke toolName.
//
// A classified tool returns its declared scopes. A tool that is registered but
// carries no classification returns StrictestToolScope, so a newly added
// side-effecting tool cannot inherit read scope by omission. A name that is not
// registered at all returns nil: it has no handler and can cause no side effect,
// so it is left to the MCP runtime to answer with its usual tool-not-found error
// rather than an authorization failure.
func RequiredScopesForTool(toolName string) []string {
	return requiredScopesForTool(toolName, toolScopes, registeredTools())
}

func requiredScopesForTool(toolName string, scopes map[string][]string, registered registeredToolSnapshot) []string {
	if declared, ok := scopes[toolName]; ok {
		return append([]string(nil), declared...)
	}
	// If the registered surface could not be derived, we cannot prove toolName is
	// unregistered, so demand the strictest scope.
	if registered.err != nil {
		return []string{StrictestToolScope}
	}
	if _, ok := registered.names[toolName]; ok {
		return []string{StrictestToolScope}
	}
	return nil
}
