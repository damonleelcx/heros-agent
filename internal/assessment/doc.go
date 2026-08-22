// Package assessment is P33's report: for each of the nine axes, what the repository does and what
// evidence supports the claim.
//
// # What this package is NOT
//
// 🚫 It computes no score, grade, maturity level or ranking. Program ruling R4, PRD FR4, design D3.
// There is no field on any type here that could hold one, and `composite_fence_test.go` reads this
// package's own declarations to keep it that way — an absence is not self-defending, a fence is.
//
// # The one thing the shape is for
//
// An assessment of a repository the platform has just met is mostly ABSENCE. Several axes cannot be
// determined, one or two cannot be measured, and at least one cannot be assessed at all in the target
// language. So the design problem is not "how do we score"; it is "how do we report absence in a way a
// reader acts on rather than discounts". That is why `not_measured` carries a NAMED missing input and
// why a finding without one is not merely discouraged but UNCONSTRUCTABLE (task 1.2):
//
//	assessment.NotMeasured(AxisMemory, "", ref)   // returns an error; there is no other way in
//
// Every field on Finding is unexported and every value arrives through a constructor that refuses the
// invalid combinations. A caller cannot write `Finding{State: StateNotMeasured}` because the field is
// not addressable from another package, which is the difference between a constraint somebody must
// remember and one the compiler holds.
//
// # Layout
//
//	axis.go      the nine axes; the seven shared ones are READ from variantspec, never retyped
//	state.go     the four states, the two origins, and the three refusal causes
//	evidence.go  a reference INTO an existing surface (D5); the assessment is an index, not a source
//	finding.go   Finding — nine fields, four conditional requirements, constructors only
//	report.go    Assessment — exactly nine findings, ordered by evidence strength (FR5)
//
// # Vocabulary, and why this is a second enum
//
// `conversation.FindingState` (P31) is four states — `measured`, `not_measured`, `refused`, `stale`.
// This package's `State` is also four — `measured`, `observed`, `not_measured`, `refused`. They are
// deliberately NOT the same enum, and P31's own rule is the one being applied: *"the single-truth-source
// rule applies to the vocabulary even though it does not apply to the enum boundary"*.
//
//	conversation.FindingState  answers "is this claim still true of the CURRENT revision?"
//	assessment.State           answers "how was this claim ESTABLISHED?"
//
// `stale` is a fact about a pin answering a question about a newer revision — a conversation's problem.
// An assessment is *of* one revision, so nothing in it can be stale with respect to itself. `observed`
// is true-by-construction as against `measured`'s true-by-experiment, which a conversation never had to
// distinguish because it never reported a structural extraction.
//
// 🔴 What IS shared is the SPELLING of the three states both name, and `vocabulary_fence_test.go`
// asserts it byte-for-byte in both directions. A reader who learns `not_measured` once has learned it,
// and one copy string in the console serves both surfaces.
package assessment
