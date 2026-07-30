// Package changedelivery is the cross-axis contract for HOW AN ACCEPTED CHANGE REACHES A RUNNING
// AGENT (P13 §23, FR57–FR68, ADR-010).
//
// # Why this package exists
//
// Delivery used to be one chain with one shape: a rewriter produces a diff, P12 opens a pull request,
// a human merges. That chain is correct and stays the default. What it never said is what happens when
// its FIRST LINK REFUSES — which, read honestly against `transform.AxisCoverage()`, is the common case
// rather than the exception. Memory refuses in every language; harness refuses in every language; skill
// binding materializes in Go for two providers. When the rewriter refuses there is no diff, so there is
// no pull request, so nothing ships — and the only thing the product said about that was nothing at all.
// It could prove a change was better and then deliver a silence indistinguishable from a proposal
// nobody had gotten to.
//
// 🔴 So delivery is a TOTAL FUNCTION over (axis × change × route). Every cell has a value and "absent"
// is not one of them; a change no route can deliver is a REPORTED STATE carrying the route that was
// expected and the cause that refused it. This is deliberately the same move `transform/coverage.go`
// made for language coverage, for the same reason: a spread discoverable only by reading several tables
// becomes one table with no blank cells.
//
// # The second route, and the constraint that shaped it
//
// On that footing a second route is added: a GRADUAL ROLLOUT. The obvious implementation — the platform
// serves a share of the customer's production traffic — is already refused twice. ADR-002 rejected our
// gateway in their request path, and rejected specifically the compromise ("decide per-node at runtime"
// was refused because it "adds a config axis that changes what a measurement means — two runs of the
// same config_hash would no longer be comparable"). ADR-005 extended the refusal to their repository.
//
// So a rollout rides the seam ADR-004 already shipped into the customer's tree: a two-armed BINDING
// DOCUMENT resolved by the generated accessor INSIDE THEIR OWN PROCESS. No credential, no network
// dependency, no new blast radius. ADR-002's comparability objection is answered rather than dodged —
// every invocation is attributed to ITS ARM'S OWN config_hash (see rollout.go), so the arm is the unit
// of record and two runs of one hash stay exactly as comparable as they were.
//
// 🚫 And a rollout is EVIDENCE, NOT DELIVERY. It expires, it reverts itself locally, it never merges,
// and it is never the way a change becomes permanent — that still costs a codemod, a pull request and a
// human. See report.go for the state machine that refuses to say `delivered`.
package changedelivery

import (
	"fmt"
	"sort"
)

// Route is how a change can reach a running agent. Exactly two, and they are NOT interchangeable: the
// source route is the default and the only road to permanence; the runtime route is temporary and
// produces evidence (FR59).
type Route string

const (
	// RouteSource — a call-site materialization carried by a P12 pull request, merged by a human.
	RouteSource Route = "source"
	// RouteRuntime — a gradual rollout under a two-armed binding document, resolved in the customer's
	// own process. Bounded, self-reverting, and never terminal.
	RouteRuntime Route = "runtime"
)

// Routes lists both, source first, because the order is the product's opinion about which is the
// default and every surface should render it the same way.
func Routes() []Route { return []Route{RouteSource, RouteRuntime} }

// Cause is why the RUNTIME route cannot deliver a cell (FR66).
//
// Three classes, because three different people are being told three different things — and the order
// they are evaluated in is load-bearing, not cosmetic. Telling an engineer to migrate a node to `bound`
// mode, when the change they want is a control-loop rewrite that no document can ever carry, sends them
// to do work that will not help. So the PERMANENT BOUNDARY IS ANNOUNCED FIRST.
type Cause string

const (
	// CauseNotRuntimeResolvable — the change is program structure, not data. No binding document can
	// carry it, in any language, ever. 🔴 Nobody's backlog: this is a boundary, and attaching a date to
	// it would be claiming an ability that cannot exist.
	CauseNotRuntimeResolvable Cause = "not-runtime-resolvable"
	// CauseNodeNotBound — the change IS data, but this node is in `inline` apply mode, so there is no
	// accessor to resolve it. The customer's engineer can act on it: a one-time `bound` migration.
	CauseNodeNotBound Cause = "node-not-bound"
	// CauseNoRolloutBinding — the change is data and the node is bound, but this axis has no field in
	// the binding document schema yet. 🔴 The ONLY class that names work we owe, which is why nothing
	// else may borrow it — the moment a permanent boundary wears this label, a reader is told to wait
	// for something that will never arrive.
	CauseNoRolloutBinding Cause = "no-rollout-binding"
)

// Causes lists the three IN EVALUATION ORDER (nobody / them / us). A consumer that iterates this slice
// and reports the first applicable cause is correct by construction; one that reorders is not.
func Causes() []Cause {
	return []Cause{CauseNotRuntimeResolvable, CauseNodeNotBound, CauseNoRolloutBinding}
}

// Valid reports whether c is one of the three. The set is closed so a consumer's switch is exhaustive;
// a refusal carrying anything else is a bug rather than a fourth class.
func (c Cause) Valid() bool {
	for _, k := range Causes() {
		if c == k {
			return true
		}
	}
	return false
}

// Owner is who can close a cause — the one word that decides what a reader does next. It is computed
// here, once, rather than inferred from the identifier at each surface, so the console and the command
// line cannot disagree about whose move it is.
func (c Cause) Owner() string {
	switch c {
	case CauseNotRuntimeResolvable:
		return "nobody"
	case CauseNodeNotBound:
		return "you"
	case CauseNoRolloutBinding:
		return "the platform"
	}
	return ""
}

// Permanent reports whether a cause is a BOUNDARY rather than unbuilt work.
//
// 🔴 This is the single most abusable bit in the package. A permanent cell must never acquire a missing
// artifact, a milestone, or a "not yet" rendering — see TestBoundaryCellsCarryNoArtifact. The failure
// mode is slow: a boundary degrades into a roadmap item, then into an exception, and by then the
// product is promising something that cannot be built.
func (c Cause) Permanent() bool { return c == CauseNotRuntimeResolvable }

// Label is the cause's human half. Cause is the machine half, and it — never this sentence — is what
// selects a surface's treatment.
func (c Cause) Label() string {
	switch c {
	case CauseNotRuntimeResolvable:
		return "Not resolvable at run time — the change is program structure, not data. No binding document can carry it, in any language."
	case CauseNodeNotBound:
		return "This node applies inline, so there is no accessor to resolve an arm. Migrating the node to bound mode would make it eligible."
	case CauseNoRolloutBinding:
		return "The change is data and the node is bound, but the binding document has no field for this axis yet."
	}
	return ""
}

// Status is what a route does in one cell.
type Status string

const (
	// StatusDelivers — the route can carry this change.
	StatusDelivers Status = "delivers"
	// StatusRefuses — the route cannot, and Cause says which kind of thing is missing.
	StatusRefuses Status = "refuses"
	// StatusVaries — the route EXISTS for this change, but whether it produces anything is decided per
	// language and per call-site form.
	//
	// 🔴 This third value exists because the honest answer for the source route is neither yes nor no.
	// P12 can carry any diff; whether a diff exists at all is `transform.AxisCoverage()`'s subject, cell
	// by cell. Reporting `delivers` here would be the optimistic copy the coverage contract exists to
	// prevent — a reader with a Rust repository would see "carries it" and conclude a pull request was
	// coming. Reporting `refuses` would be equally wrong for the Go call site next to it.
	StatusVaries Status = "varies-by-language"
)

// Axis identifiers. These mirror `variantspec.Dimension` values plus wiring, which is an axis a user can
// request even though it is not a Dimension carried on a NodeOverride.
const (
	AxisModel   = "model"
	AxisPrompt  = "prompt"
	AxisSkills  = "skills"
	AxisTools   = "tools"
	AxisContext = "context"
	AxisMemory  = "memory"
	AxisHarness = "harness"
	AxisWiring  = "wiring"
)

// ChangeKind is the unit of delivery eligibility WITHIN an axis, and it is not decoration.
//
// 🔴 "The model dimension is rollout-eligible" is false of a provider-crossing swap, and a user who
// reads that row and concludes they can canary one vendor against another has been misled by a table
// that was too coarse. Likewise "tools are not rollout-eligible" answers two different questions with
// the same wrong answer — binding is permanent, tool-set selection is a schema gap. So the cell key is
// (axis, change kind), never the axis alone.
type ChangeKind string

const (
	// ChangeModelWithinProvider — swapping the model id inside one provider. A value change.
	ChangeModelWithinProvider ChangeKind = "model-within-provider"
	// ChangeModelAcrossProvider — moving the node to a different provider. 🔴 A rewrite of the SDK CALL
	// ITSELF (ADR-002, P2 FR12 as narrowed), not an argument swap. The codemod refuses it and no
	// document can carry it.
	ChangeModelAcrossProvider ChangeKind = "model-across-provider"
	// ChangeInferenceParams — temperature, top_p, max_tokens. Data, already in the binding document.
	ChangeInferenceParams ChangeKind = "inference-params"
	// ChangePromptVersion — swapping the prompt template version. Data, already in the document.
	ChangePromptVersion ChangeKind = "prompt-version"
	// ChangeSkillBinding — constructing a provider SDK's tool value at the call site. CODE.
	ChangeSkillBinding ChangeKind = "skill-binding"
	// ChangeToolSet — which of the tools the program ALREADY CONSTRUCTS are offered on a call. A set,
	// which is data — the document simply has no field for it yet.
	ChangeToolSet ChangeKind = "tool-set"
	// ChangeRetrievalParams — a top_k, a token budget, a similarity floor. Numbers.
	ChangeRetrievalParams ChangeKind = "retrieval-params"
	// ChangeSelectionPolicy — which turns a node retains. Applied by DELETING the turns it does not
	// retain from a constructed message list: a source rewrite, not a value.
	ChangeSelectionPolicy ChangeKind = "selection-policy"
	// ChangeMemoryStrategy — a memory strategy needs a STORE that persists between invocations. A
	// document carries values, not a running store.
	ChangeMemoryStrategy ChangeKind = "memory-strategy"
	// ChangeHarnessStrategy — a control loop. Program structure.
	ChangeHarnessStrategy ChangeKind = "harness-strategy"
	// ChangeHarnessParams — max_turns, retry budget, stop condition: parameters of a loop ALREADY
	// WRITTEN, which is data the schema does not carry yet.
	ChangeHarnessParams ChangeKind = "harness-params"
	// ChangeWiring — node order, parallelization, merge, prune, edge changes. Compiled structure.
	ChangeWiring ChangeKind = "wiring"
)

// Cell is one (axis, change kind, route) answer — the unit the delivery table is total over.
type Cell struct {
	Axis   string     `json:"axis"`
	Change ChangeKind `json:"change"`
	Route  Route      `json:"route"`
	Status Status     `json:"status"`
	// Cause is empty when Status is delivers; one of the three classes otherwise.
	Cause Cause `json:"cause,omitempty"`
	// MissingArtifact names what would close a CauseNoRolloutBinding gap — a document field, a schema
	// addition. 🔴 EMPTY for the other two classes, and that asymmetry IS the contract: a permanent
	// boundary has no artifact to build, and a node in inline mode is not waiting on us.
	MissingArtifact string `json:"missing_artifact,omitempty"`
	// Note is the cell's own sentence — why this particular change is or is not data.
	Note string `json:"note,omitempty"`
	// Contingent marks a refusal whose reason could change, distinct from a boundary that cannot.
	Contingent bool `json:"contingent,omitempty"`
	// MissingComponent names what a contingent refusal is waiting on. 🚫 Never a date.
	MissingComponent string `json:"missing_component,omitempty"`
}

// Refused reports whether this cell is a refusal.
func (c Cell) Refused() bool { return c.Status == StatusRefuses }

// runtimeRule is the per-(axis, change) runtime-route answer BEFORE the node's apply mode is consulted.
//
// 🚫 There is no second copy of this anywhere. Every surface, the CLI's offline table, and the rollout
// authoring gate all read it, so a console that showed a cell as eligible while authoring refused it is
// not possible by construction.
type runtimeRule struct {
	axis     string
	change   ChangeKind
	eligible bool
	// cause and artifact apply when !eligible. A rule that is not eligible with a permanent cause and a
	// non-empty artifact is rejected by TestBoundaryCellsCarryNoArtifact rather than rendered.
	cause    Cause
	artifact string
	note     string
	// contingent marks a `notRuntimeResolvable` cell whose reason COULD change — unlike wiring, whose
	// boundary is a property of compiled code, memory is refused because a runtime component does not
	// exist, and one could.
	//
	// 🔴 The cause stays `notRuntimeResolvable`, because it is accurate today: the change is not
	// expressible as data. What contingency adds is the answer to "should I ask again?", which is the
	// only question separating these two cells for a reader. Rendering them identically tells someone
	// to stop asking about something that is merely unbuilt.
	contingent bool
	// component names what is missing for a contingent cell. 🚫 It carries NO date: naming a missing
	// component is not a promise to build it.
	component string
}

// runtimeRules is THE table. Read it top to bottom and it is the honest answer to "what can a rollout
// actually change?" — which is: the fields ADR-009 already fixed in the binding document, and nothing
// else yet.
var runtimeRules = []runtimeRule{
	// ── P13: the only axis whose runtime route is live, and not because it is the important one. Model
	// id, inference params and prompt version are precisely the fields ADR-009 already froze in the
	// document, because ADR-004 already decided they are data rather than program structure.
	{axis: AxisModel, change: ChangeModelWithinProvider, eligible: true,
		note: "The model id is a value in the binding document (ADR-009); a rollout adds a second one."},
	{axis: AxisModel, change: ChangeModelAcrossProvider, eligible: false,
		cause: CauseNotRuntimeResolvable,
		note:  "Swapping the provider rewrites the SDK call itself (ADR-002), not an argument. No document can carry it, and a bound migration would not change that."},
	{axis: AxisModel, change: ChangeInferenceParams, eligible: true,
		note: "Inference params are values in the binding document (ADR-009)."},
	{axis: AxisPrompt, change: ChangePromptVersion, eligible: true,
		note: "The prompt template is a value in the binding document (ADR-009)."},

	// ── P14: two refusals one row apart, pointing at OPPOSITE conclusions. Merging them is the defect
	// this axis already corrected once for language coverage.
	{axis: AxisSkills, change: ChangeSkillBinding, eligible: false,
		cause: CauseNotRuntimeResolvable,
		note:  "Binding a skill CONSTRUCTS a provider SDK tool value. A document holds data, not a constructed value; a schema that could hold one would be a code generator running at request time."},
	{axis: AxisTools, change: ChangeToolSet, eligible: false,
		cause:    CauseNoRolloutBinding,
		artifact: "binding document field: nodes[].tool_set (the enabled subset of the tools the call site already constructs)",
		note:     "Selecting among tools the program already constructs is a SET, which a document carries naturally. The schema simply has no field for it yet."},

	// ── P15: permanently outside the runtime route. A document that could reorder statements in a built
	// binary would be an interpreter, and shipping one into a customer's process to rearrange their own
	// code is a larger change to their system than any optimization justifies.
	{axis: AxisWiring, change: ChangeWiring, eligible: false,
		cause: CauseNotRuntimeResolvable,
		note:  "Order and concurrency are compiled program structure. No document reorders statements in a built binary, in any language, in any apply mode."},

	// ── P16: the axis splits, and not where a reader expects.
	{axis: AxisContext, change: ChangeRetrievalParams, eligible: false,
		cause:    CauseNoRolloutBinding,
		artifact: "binding document field: nodes[].retrieval (top_k, token budget, similarity floor)",
		note:     "A retrieval parameter is a NUMBER — exactly the kind of fact the document exists to carry. Only the field is missing."},
	{axis: AxisContext, change: ChangeSelectionPolicy, eligible: false,
		cause: CauseNotRuntimeResolvable,
		note:  "A selection policy is applied by DELETING the turns it does not retain from a constructed message list. No document performs a deletion in built code."},

	// ── P17: contingent, not permanent — and not scheduled either. Unlike wiring, a memory runtime
	// COULD exist; naming what is missing is not a promise to build it.
	{axis: AxisMemory, change: ChangeMemoryStrategy, eligible: false,
		cause:      CauseNotRuntimeResolvable,
		contingent: true,
		component:  "a memory store running in the customer's process, and the document schema to point at it",
		note:       "A memory strategy needs a STORE that persists between invocations. The binding document carries values, not a running store, and the platform ships no store into the customer's tree."},

	// ── P18: a scaffold is structure; its bounds are numbers.
	{axis: AxisHarness, change: ChangeHarnessStrategy, eligible: false,
		cause: CauseNotRuntimeResolvable,
		note:  "A strategy swap changes how many calls the program makes and in what control flow. That is a loop, and no document introduces one."},
	{axis: AxisHarness, change: ChangeHarnessParams, eligible: false,
		cause:    CauseNoRolloutBinding,
		artifact: "binding document field: nodes[].harness_params (turn ceiling, retry budget, stop condition)",
		note:     "max_turns, the retry budget and the stop condition are parameters of a loop ALREADY WRITTEN — data in exactly the sense the document was designed for."},
}

// ChangeKinds returns every change kind the delivery table is total over, in table order.
func ChangeKinds() []ChangeKind {
	out := make([]ChangeKind, 0, len(runtimeRules))
	for _, r := range runtimeRules {
		out = append(out, r.change)
	}
	return out
}

// AxisFor returns the axis a change kind belongs to, and whether the kind is known. An unknown kind is
// an error rather than a default: a change the table has never heard of must not be silently reported
// as eligible OR as refused, because both would be a claim nobody made.
func AxisFor(kind ChangeKind) (string, bool) {
	for _, r := range runtimeRules {
		if r.change == kind {
			return r.axis, true
		}
	}
	return "", false
}

// Eligibility is the runtime route's answer for one change on one node.
type Eligibility struct {
	Change ChangeKind `json:"change"`
	Axis   string     `json:"axis"`
	// Eligible is true only when the change is data AND the node is bound AND the document has a field.
	Eligible bool  `json:"eligible"`
	Cause    Cause `json:"cause,omitempty"`
	// MissingArtifact is non-empty only for CauseNoRolloutBinding (FR66's asymmetry).
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
	// Contingent marks a `notRuntimeResolvable` cell whose reason COULD change.
	//
	// 🔴 Memory and wiring both refuse as "not data", and a reader who cannot tell them apart draws the
	// wrong conclusion from one of them. Wiring is a property of compiled code and will not move. Memory
	// refuses because a runtime component does not exist — and one could. That difference is the entire
	// answer to "should I ask again?", so it is a field rather than a matter of prose.
	Contingent bool `json:"contingent,omitempty"`
	// MissingComponent names what a contingent refusal waits on. 🚫 Never a date, a milestone, or a
	// commitment: naming a missing component is not a promise to build it.
	MissingComponent string `json:"missing_component,omitempty"`
}

// ErrUnknownChangeKind is returned for a kind the table does not carry. It fails closed by design.
type ErrUnknownChangeKind struct{ Kind ChangeKind }

func (e *ErrUnknownChangeKind) Error() string {
	return fmt.Sprintf("change delivery: unknown change kind %q — the delivery table is total over a closed set, so an unlisted kind is a defect rather than an eligible change", e.Kind)
}

// RuntimeEligibility evaluates the three causes IN ORDER for one change on one node (FR66).
//
// 🔴 The order is the whole requirement. `bound` is consulted only AFTER the permanent boundary has
// had its say, so a user whose change can never be data is never sent to migrate a node — that work
// would not help them, and telling them to do it is worse than telling them nothing.
//
// nodeIsBound is the node's apply mode (ADR-004): true for `bound`, false for `inline`.
func RuntimeEligibility(kind ChangeKind, nodeIsBound bool) (Eligibility, error) {
	for _, r := range runtimeRules {
		if r.change != kind {
			continue
		}
		out := Eligibility{Change: kind, Axis: r.axis, Note: r.note}
		// 1. notRuntimeResolvable — the boundary, announced first and independent of apply mode.
		if !r.eligible && r.cause == CauseNotRuntimeResolvable {
			out.Cause = CauseNotRuntimeResolvable
			out.Contingent = r.contingent
			out.MissingComponent = r.component
			return out, nil
		}
		// 2. nodeNotBound — the change is data, but there is no accessor on this node.
		if !nodeIsBound {
			out.Cause = CauseNodeNotBound
			return out, nil
		}
		// 3. noRolloutBinding — data, bound, but the schema has no field. Ours, and named.
		if !r.eligible {
			out.Cause = CauseNoRolloutBinding
			out.MissingArtifact = r.artifact
			return out, nil
		}
		out.Eligible = true
		return out, nil
	}
	return Eligibility{Change: kind}, &ErrUnknownChangeKind{Kind: kind}
}

// Table is THE total delivery read: every change kind × both routes (FR57).
//
// The source-route column is deliberately coarse here — it reports that the route EXISTS for the change,
// because whether it materializes is a per-language question `transform.AxisCoverage()` already answers
// cell by cell. Report() joins the two for a concrete change; this table is the shape of the contract.
//
// Sorted, so a doc, a console table and a CLI listing render identically and a diff between two builds
// is readable.
func Table() []Cell {
	out := make([]Cell, 0, len(runtimeRules)*2)
	for _, r := range runtimeRules {
		src := Cell{
			Axis: r.axis, Change: r.change, Route: RouteSource,
			Status: StatusVaries,
			Note:   "Whether a diff exists is decided per language and per call-site form; transform.AxisCoverage() carries that cell.",
		}
		// 🔴 …except where the source route refuses in EVERY language for a reason no materializer would
		// fix. Saying "per language" there would send a reader to check twelve coverage cells that all
		// say the same thing, and would imply that one of them might one day say something else.
		if key, ok := sourceKeys[r.change]; ok && key.permanentlyRefused {
			src.Status = StatusRefuses
			src.Cause = CauseNotRuntimeResolvable
			src.Note = key.note
		}
		out = append(out, src)
		cell := Cell{Axis: r.axis, Change: r.change, Route: RouteRuntime}
		if r.eligible {
			cell.Status = StatusDelivers
		} else {
			cell.Status = StatusRefuses
			cell.Cause = r.cause
			cell.MissingArtifact = r.artifact
			cell.Contingent = r.contingent
			cell.MissingComponent = r.component
		}
		cell.Note = r.note
		out = append(out, cell)
	}
	sortCells(out)
	return out
}

// sortCells orders by axis, then change, then route, so every rendering agrees.
func sortCells(cells []Cell) {
	sort.SliceStable(cells, func(i, j int) bool {
		a, b := cells[i], cells[j]
		if a.Axis != b.Axis {
			return a.Axis < b.Axis
		}
		if a.Change != b.Change {
			return a.Change < b.Change
		}
		return a.Route < b.Route
	})
}
