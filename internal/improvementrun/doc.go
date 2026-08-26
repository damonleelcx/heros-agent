// Package improvementrun is P35: the phase where the conversation does something.
//
// A person asks a question; this package turns it into a BOUNDED PLAN, drives the existing P6
// optimizer under that plan's bounds, surfaces only P5.5-verified candidates, takes a per-proposal
// approval through `internal/approval`, re-measures after applying, withdraws a change that fails to
// reproduce, and hands the survivor to `internal/forgedelivery`.
//
// # What this package deliberately does NOT contain
//
// A verification gate, an approval primitive, a proposal operator, a delivery mode, or a copy of the
// optimizer's loop. Every one of those exists, and P35's entire risk is that a NEW CALLER goes around
// one of them. So the rule this package is written to is:
//
//	🚫 Nothing here decides anything a shipped gate already decides.
//	🚫 Nothing here re-implements a loop; it BOUNDS the one that exists.
//
// The gates it must not bypass are enumerated, as a walkable checklist rather than as prose, in
// `openspec/changes/p35-autonomous-improvement-run/gate-inventory.md`, and `Gates()` below is the Go
// half of that checklist — `TestGateInventoryIsComplete` fails when the two disagree.
//
// # The two genuinely new pieces
//
//	plan.go / translate.go   A question becomes a plan with a scope, a candidate cap, a spend budget
//	                         and a stopping condition — or is REFUSED. Refused, not run with defaults:
//	                         defaults are how a conversational surface spends someone's money on a
//	                         search they did not ask for, and the failure is discovered on an invoice.
//	remeasure.go             The second observation is allowed to DISAGREE, and disagreement withdraws
//	                         the change before delivery, reporting both numbers. A ritual that cannot
//	                         fail teaches everyone downstream to ignore it.
//
// Everything else in this package is wiring, and the tests are what prove the wiring did not become a
// bypass.
package improvementrun
