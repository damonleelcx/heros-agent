// Package errorcode is the platform's CENTRAL error-code enum.
//
// # Why a closed enum and not a string
//
// An error code is the only message-shaped value P24 permits to cross the error-reporting boundary
// (design D5). Everything else about a failure — the message body, the wrapped cause, the formatted
// arguments — is dropped, because `fmt.Errorf("failed to resolve prompt %q", p)` is an ordinary Go
// error and an exfiltration path, and no amount of care at the call site changes that. What is left
// has to be enough to triage with, which means it has to be a CLASSIFICATION rather than a sentence.
//
// A closed enum is what makes the guarantee checkable. You can read this file and know the complete
// set of message-shaped strings that can reach a third party, which you can never do for a free-text
// code — and the reporting boundary refuses a value that is not one of these rather than passing it
// through, so a literal typed at a call site is absent from the wire instead of quietly present on it.
//
// # Naming
//
// `UPPER_SNAKE_CASE`, one code per failure CLASS rather than per call site. Two call sites that fail
// for the same reason share a code; two that fail for different reasons must not, because the code is
// the whole of what an operator gets.
//
// # What a code may never encode
//
// A tenant, a run, a variant, a node, a model, a provider, a path or any other identifier. A code that
// interpolated one would reintroduce exactly the leak the message drop exists to close, one layer down
// and harder to see. `Valid` is what enforces that: a code is a member of this list or it is not a code.
package errorcode

// Code is a member of the enum. A bare string is not assignable to it by accident from another package,
// and the reporting boundary checks membership before transmitting.
type Code string

const (
	// Unknown is the code for a failure nothing classified. It is a real answer, not a placeholder:
	// "we do not know what class this was" is more useful to an operator than a plausible wrong class,
	// and a rising count of it is itself the signal that a class is missing.
	Unknown Code = "UNKNOWN"

	// ── Process and runtime ──────────────────────────────────────────────────
	PlatformPanic  Code = "PLATFORM_PANIC"  // an unrecovered panic reached the top of a goroutine or a handler
	ContextExpired Code = "CONTEXT_EXPIRED" // the caller's deadline passed before the work finished
	Timeout        Code = "TIMEOUT"         // an internal operation exceeded its own bound

	// ── Configuration and contracts ──────────────────────────────────────────
	ConfigInvalid    Code = "CONFIG_INVALID"    // configuration failed validation at load
	ContractMismatch Code = "CONTRACT_MISMATCH" // a wire contract version or shape did not match
	SchemaMismatch   Code = "SCHEMA_MISMATCH"   // the database schema is not what the code expects

	// ── Storage ──────────────────────────────────────────────────────────────
	StoreNotMounted  Code = "STORE_NOT_MOUNTED" // a read model this deployment does not ship was asked for
	StoreUnavailable Code = "STORE_UNAVAILABLE" // the datastore could not be reached
	StoreWriteFailed Code = "STORE_WRITE_FAILED"
	StoreReadFailed  Code = "STORE_READ_FAILED"
	MigrationFailed  Code = "MIGRATION_FAILED"

	// ── Request handling ─────────────────────────────────────────────────────
	NotFound          Code = "NOT_FOUND"
	RequestInvalid    Code = "REQUEST_INVALID"
	AuthFailed        Code = "AUTH_FAILED"
	EntitlementDenied Code = "ENTITLEMENT_DENIED"
	RateLimited       Code = "RATE_LIMITED"

	// ── Outbound ─────────────────────────────────────────────────────────────
	UpstreamError    Code = "UPSTREAM_ERROR"    // a dependency answered with a failure
	TransportFailure Code = "TRANSPORT_FAILURE" // a dependency could not be reached at all
	ProviderError    Code = "PROVIDER_ERROR"    // a model provider refused or failed the call

	// ── Reading a customer's reported structure (P37) ────────────────────────
	//
	// 🔴 Its own class rather than `CONTRACT_MISMATCH`, and the distinction is the point. A contract
	// mismatch means the two sides disagree about the SHAPE. This means the shape is right and a field
	// that should carry a value carries discovery's `unresolved` sentinel — a fact about the customer's
	// code that the platform must report as absence rather than resolve to a default. An operator
	// watching its rate climb is watching discovery degrade on real repositories, which is a different
	// investigation from a version skew.
	AxisValueUnresolved Code = "AXIS_VALUE_UNRESOLVED"

	// ── Browser (P24 wave 24c) ───────────────────────────────────────────────
	//
	// Four classes rather than one, because they have four different causes and three of them are
	// invisible to a green `next build`: a CSP that refuses a script, a chunk that failed to load and a
	// hydration mismatch all render a page that looks correct and does nothing.
	BrowserUnhandledError     Code = "BROWSER_UNHANDLED_ERROR"
	BrowserUnhandledRejection Code = "BROWSER_UNHANDLED_REJECTION"
	BrowserChunkLoadFailed    Code = "BROWSER_CHUNK_LOAD_FAILED"
	BrowserHydrationFailed    Code = "BROWSER_HYDRATION_FAILED"
)

// All is the complete enum, in the order above. It is the single source the reporting boundary checks
// membership against and the browser's mirror is asserted equal to.
var All = []Code{
	Unknown,
	PlatformPanic, ContextExpired, Timeout,
	ConfigInvalid, ContractMismatch, SchemaMismatch,
	StoreNotMounted, StoreUnavailable, StoreWriteFailed, StoreReadFailed, MigrationFailed,
	NotFound, RequestInvalid, AuthFailed, EntitlementDenied, RateLimited,
	UpstreamError, TransportFailure, ProviderError,
	AxisValueUnresolved,
	BrowserUnhandledError, BrowserUnhandledRejection, BrowserChunkLoadFailed, BrowserHydrationFailed,
}

var valid = func() map[Code]bool {
	m := make(map[Code]bool, len(All))
	for _, c := range All {
		m[c] = true
	}
	return m
}()

// Valid reports whether s is a member of the enum.
//
// This is the check that makes "the message body is dropped unless it is an error code" enforceable
// rather than aspirational: the reporting boundary calls it, and a value that fails it does not reach
// the wire in any field.
func Valid(s string) bool { return valid[Code(s)] }

// String satisfies fmt.Stringer so a code formats as itself rather than as its underlying type.
func (c Code) String() string { return string(c) }
