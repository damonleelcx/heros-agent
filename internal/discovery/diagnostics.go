package discovery

// Diagnostic is one entry in the discovery run report (doc 09). Every skipped file, stale declaration,
// or unresolved value produces one, so that "why is this node missing?" is always answerable (I4).
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // "error" | "warn" | "info"
	File     string `json:"file,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

// Diagnostic codes are a closed, documented enum (doc 09 §3.3) so P5 can triage programmatically.
const (
	CodeParseError          = "PARSE_ERROR"
	CodeSymlinkCycleSkipped = "SYMLINK_CYCLE_SKIPPED"
	CodeDeclSymbolNotFound  = "DECL_SYMBOL_NOT_FOUND"
	CodeExprDepthExceeded   = "EXPR_DEPTH_EXCEEDED"
	CodeWalkError           = "WALK_ERROR"
	CodePackagePanic        = "PACKAGE_PANIC"
	CodeFrameworkReaderErr  = "FRAMEWORK_READER_ERROR"
	CodeLanguageUnsupported = "LANGUAGE_UNSUPPORTED"

	// §4 extraction ambiguity codes (unresolved fields → P5 dynamic-trace candidates, FR8 / doc 08).
	CodeModelUnresolved        = "MODEL_UNRESOLVED"
	CodeModelConstructionBound = "MODEL_CONSTRUCTION_BOUND"
	CodePromptUnresolved       = "PROMPT_UNRESOLVED"
	CodePromptOpaqueBody       = "PROMPT_OPAQUE_BODY"
	CodePromptConstructed      = "PROMPT_CONSTRUCTED"
	CodeFrameworkVersionDrift  = "FRAMEWORK_VERSION_UNRECOGNIZED"
)

// UnresolvedSentinel is the single documented constant emitted into IR fields that static analysis
// cannot resolve (doc 08 §3, invariant I5). The authoritative record of WHY is the ambiguity flag.
const UnresolvedSentinel = "unresolved"

const (
	SeverityError = "error"
	SeverityWarn  = "warn"
	SeverityInfo  = "info"
)
