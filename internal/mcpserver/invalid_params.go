package mcpserver

import "strings"

// InvalidParamsError is returned by tool/resource/prompt helpers when a caller
// provided invalid JSON arguments.
//
// Note: AppTheory's upstream MCP server currently maps tool handler errors to a
// JSON-RPC "Server error" (-32000). We keep this error type so our handlers can
// consistently distinguish validation issues internally (and tests can assert
// behavior at the tool layer).
type InvalidParamsError struct {
	Message string
}

func (e *InvalidParamsError) Error() string {
	if e == nil {
		return "invalid params"
	}
	return strings.TrimSpace(e.Message)
}

func invalidParams(msg string) error {
	return &InvalidParamsError{Message: msg}
}
