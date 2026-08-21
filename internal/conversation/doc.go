// Package conversation is P31's vocabulary and its boundary: the closed set of things the
// conversational console may say, and the rules that decide whether a given message is sayable.
//
// # What this package is for, in one sentence
//
// A chat surface is the natural way to break the two rules this product is built on — *the browser
// derives nothing* and *diagnosis proposes, verification decides* — so the vocabulary is closed, every
// kind that makes a claim carries the reference that supports it, and the check is on the SERVER,
// before the transport.
//
// # Why a vocabulary rather than prose
//
// Prose is not falsifiable. A paragraph that says "your extraction node is inconsistent" either has a
// measurement behind it or does not, and no fence can tell which. A typed `finding` either carries an
// evidence reference or it does not, and `Emitter.Emit` can refuse it. Everything in this file exists
// so that a claim has a shape a test can go red over.
//
// # What lives here and what deliberately does not
//
//	HERE  the enums (Kind, Provenance, Phase, StepState, FindingState), the effect-artifact table,
//	      the message shapes, the budget accounting, the intent set, and the emitter that refuses.
//	NOT   the transport (internal/api), the approval gate (internal/approval), the verification ledger
//	      (internal/verification), and the stop-reason vocabulary (internal/harnessruntime).
//
// 🔴 The last one is the one worth stating twice. A conversation that ran out of tokens and a node loop
// that ran out of turns are the SAME CONCEPT, and this package extends `harnessruntime.StopReason`
// rather than declaring a second vocabulary beside it — see design.md D8 and the expand-only note on
// that type.
//
// # Scope: a conversation is per-person (PRD §14 Q4)
//
// A conversation is visible to, resumable by, and approvable by the person who started it, inside the
// tenant that owns the run. The tenant still owns the RUN and its evidence, so a colleague reaches the
// same work through `/app/runs` — nothing is hidden that was previously shared. The direction is chosen
// because it is the reversible one: widening to per-tenant later is additive, and narrowing later
// removes a capability people have built habits around.
package conversation
