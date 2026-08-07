package verification

import "github.com/heros-foreal/agentd/internal/verdictrecord"

// verdict.go re-exports the verdict vocabulary, which now lives in internal/verdictrecord.
//
// These are ALIASES, not new types: `verification.Verdict` and `verdictrecord.Verdict` are the same type, so
// every existing caller compiles unchanged and a value crosses between the two packages without conversion.
//
// The split exists because this package imports the eval runner and, through it, the sandbox — which uses
// Unix process groups and does not compile on Windows. A program that only needs to NAME a verdict (the CLI's
// `report-verdict`, which unmarshals one from a file) must not have to link a process isolator to do it. See
// internal/verdictrecord for the full account.

// GateResult is the terminal verdict of the verification gate.
type GateResult = verdictrecord.GateResult

const (
	GatePass          = verdictrecord.GatePass
	GateFailSig       = verdictrecord.GateFailSig
	GateFailRegress   = verdictrecord.GateFailRegress
	GateFailConstrain = verdictrecord.GateFailConstrain
	GateUnrun         = verdictrecord.GateUnrun
)

// Verdict is the structured, source-of-truth result of verifying one proposal.
type Verdict = verdictrecord.Verdict
