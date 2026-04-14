package toolcontract

type ErrorCode string

const (
	ErrorCodeInvalidAction    ErrorCode = "INVALID_ACTION"
	ErrorCodeValidationError  ErrorCode = "VALIDATION_ERROR"
	ErrorCodePermissionDenied ErrorCode = "PERMISSION_DENIED"
	ErrorCodePolicyBlocked    ErrorCode = "POLICY_BLOCKED"
	ErrorCodeIOError          ErrorCode = "IO_ERROR"
	ErrorCodeNotFound         ErrorCode = "NOT_FOUND"
	ErrorCodeCommandFailed    ErrorCode = "COMMAND_FAILED"
	ErrorCodeNetworkError     ErrorCode = "NETWORK_ERROR"
	ErrorCodeUnknownError     ErrorCode = "UNKNOWN_ERROR"
)

var ErrorCodeWhitelist = map[ErrorCode]struct{}{
	ErrorCodeInvalidAction:    {},
	ErrorCodeValidationError:  {},
	ErrorCodePermissionDenied: {},
	ErrorCodePolicyBlocked:    {},
	ErrorCodeIOError:          {},
	ErrorCodeNotFound:         {},
	ErrorCodeCommandFailed:    {},
	ErrorCodeNetworkError:     {},
	ErrorCodeUnknownError:     {},
}

func IsAllowedErrorCode(code ErrorCode) bool {
	_, ok := ErrorCodeWhitelist[code]
	return ok
}

func ParseErrorCode(code string) ErrorCode {
	return ErrorCode(code)
}
