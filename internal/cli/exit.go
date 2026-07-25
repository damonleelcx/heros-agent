// Package cli is the P11 command surface — the customer-installed binary that runs discovery, apply and
// eval offline with no account, and links a run to the platform as an explicit, authenticated act.
//
// This file is the exit-code contract (PRD FR5, task 1.3). The codes are a PUBLIC contract the moment a
// customer's pipeline branches on them, so they are decided here and documented in
// docs/decisions/p11-contracts.md, not discovered in call sites. Three remedies must never share a code.
package cli

// Exit codes. Distinct and documented; a CI job branches on these without parsing prose.
//
//	0  success                — the command did what it was asked; no gate failed, nothing broke.
//	1  configured-gate-failed — a quality gate the CUSTOMER configured failed. Their remedy: fix the
//	                            regression, or change the gate. NOT our failure.
//	2  operational-error      — the tool broke, or a platform-facing command could not reach the
//	                            platform. Their remedy: retry, file a bug, check connectivity.
//	3  invalid-config         — the invocation is malformed: a missing required input, an unreadable
//	                            config file, a flag out of range. Their remedy: fix the invocation.
//
// The gap between 1 and 2 is the load-bearing one: "your gate failed" and "our tool broke" have
// opposite remedies, and a CI step that fails for an unclear reason gets disabled (design Decision 9).
const (
	ExitOK          = 0
	ExitGateFailed  = 1
	ExitOperational = 2
	ExitInvalidCfg  = 3
)

// ExitError carries an exit code up to main(). Every command returns one of these (or nil for success),
// so the process exit is decided in exactly one place and cannot be a bare os.Exit scattered around.
type ExitError struct {
	Code int
	// Msg is the human-facing reason, written to stderr. It never carries machine output.
	Msg string
	// Err is the wrapped cause, for %w unwrapping in tests.
	Err error
}

func (e *ExitError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "cli error"
}

func (e *ExitError) Unwrap() error { return e.Err }

// invalidConfig builds an ExitInvalidCfg error. A missing required input names the input (FR8 / task
// 2.5): a command that exits invalid-config without saying what was missing is a support ticket.
func invalidConfig(msg string) *ExitError { return &ExitError{Code: ExitInvalidCfg, Msg: msg} }

// operational builds an ExitOperational error wrapping a cause.
func operational(msg string, err error) *ExitError {
	return &ExitError{Code: ExitOperational, Msg: msg, Err: err}
}

// gateFailed builds an ExitGateFailed error naming the gate that failed (FR23 / task 8.4). A gate
// failure that does not name the gate is indistinguishable from a crash to the person reading the log.
func gateFailed(msg string) *ExitError { return &ExitError{Code: ExitGateFailed, Msg: msg} }
