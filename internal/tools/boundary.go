package tools

// boundary.go states the constraint every tool in this package is built around, because it was implicit
// until it forced a design decision and nearly got violated by accident.
//
// # 🔴 heros never executes the customer's code
//
// It reads source, it reasons about source, and it proposes changes to source. It does not run it — not
// to check a change, not to measure a workflow, not to evaluate anything.
//
// The reason is not squeamishness. Running a customer's agent means running arbitrary code, with their
// credentials, against their providers, on our infrastructure, while they are not present. Every one of
// those clauses is a separate thing to get wrong, and getting any of them wrong is the kind of incident
// a platform does not recover from.
//
// # What that costs, stated plainly
//
// It means this system cannot measure whether a change actually improved anything by running it. That is
// a real limitation of the product, not a temporary gap:
//
//   - `evalset` GENERATES an evaluation set and hands it over. The customer runs it in their own
//     harness, with their own credentials, on their own machine.
//   - `compare` cannot run an eval set twice and diff the scores. It compares ASSESSMENTS — the nine-axis
//     findings at one revision against those at another — which is a weaker claim, honestly weaker, and
//     obtainable without executing anything.
//
// 🚫 A future version that accepts eval RESULTS the customer reports back would strengthen `compare`
// without weakening this boundary. A future version that runs their agent for them would not.
//
// The boundary is stated here rather than in nine comments because it is one decision, and a decision
// scattered across nine places is one that erodes at whichever place a deadline lands on.
const NeverExecutesCustomerCode = true
