package cliagent

import "github.com/heros-foreal/agentd/internal/toolcontract"

const (
	ToolStatusOK    = "ok"
	ToolStatusError = "error"
)

type ErrorCode = toolcontract.ErrorCode

const (
	ErrorCodeInvalidAction    = toolcontract.ErrorCodeInvalidAction
	ErrorCodeValidationError  = toolcontract.ErrorCodeValidationError
	ErrorCodePermissionDenied = toolcontract.ErrorCodePermissionDenied
	ErrorCodePolicyBlocked    = toolcontract.ErrorCodePolicyBlocked
	ErrorCodeIOError          = toolcontract.ErrorCodeIOError
	ErrorCodeNotFound         = toolcontract.ErrorCodeNotFound
	ErrorCodeCommandFailed    = toolcontract.ErrorCodeCommandFailed
	ErrorCodeNetworkError     = toolcontract.ErrorCodeNetworkError
	ErrorCodeUnknownError     = toolcontract.ErrorCodeUnknownError
)

var ToolErrorCodeWhitelist = toolcontract.ErrorCodeWhitelist

var ToolAuditRequiredKeys = []string{"timestamp", "session_id", "workdir", "action"}

func IsAllowedToolErrorCode(code ErrorCode) bool {
	return toolcontract.IsAllowedErrorCode(code)
}

func ParseErrorCode(code string) ErrorCode {
	return ErrorCode(code)
}
