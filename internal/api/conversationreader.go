package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/approval"
	"github.com/heros-foreal/agentd/internal/axisprojection"
	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/verification"
)

// conversationreader.go is the adapter layer: the conversational runner's three seams, wired to the
// read models this deployment actually mounted.
//
// # 🔴 Why every answer here is a STATE and never an absence
//
// This is the file where the temptation is strongest. Nine of the fourteen intents can only be answered
// from data a customer opted in to send, and the easy implementation returns "nothing to say" when it is
// missing. On a conversational surface that reads as *"I looked and nothing is wrong"* — which is a
// claim about the customer's repository, made from an empty map, with no evidence behind it.
//
// So every path below produces one of the four finding states, and `not_measured` NAMES THE MISSING
// INPUT in the form of a command the reader can run. That is P29's `not-reported` discipline, which
// `axisprojection` already enforces one layer down, carried up into the conversation rather than
// flattened at the boundary.
//
// # Why the axis intents are answered from `axisprojection` rather than from a new read
//
// Because `axisprojection.Build` is where the four states are DECIDED, including the one that must never
// be produced by accident (`not-applicable` is a claim about the customer's code). A second computation
// here would be a second place that decision is made, and the two would disagree the first time the
// coverage table moved. The conversation asks the same function the console's coverage page asks, and
// projects its answer onto one axis.

// platformSurfaceReader answers a question from the read models mounted on this server.
type platformSurfaceReader struct {
	srv *Server
}

// intentAxes maps a route-backed intent to the coverage axes that answer it.
//
// 🔴 A TABLE, not a switch with a default. A default arm is how an intent added later silently answers
// from the wrong axis: `transform.CoverageAxes()` and this map are compared by a fence, so an intent
// with no axes is a decision somebody records rather than a fallthrough they inherit.
var intentAxes = map[conversation.Intent][]string{
	conversation.IntentPromptModel: {"prompt", "model"},
	conversation.IntentContext:     {"context"},
	conversation.IntentMemory:      {"memory"},
	conversation.IntentHarness:     {"harness"},
	// `author` is "change something on an axis and show me the diff" — every axis the engine can edit.
	conversation.IntentAuthor: {"prompt", "model", "skills", "tools", "context", "memory", "harness"},
	// `coverage` is the whole projection: "what did you measure, and what did you not".
	conversation.IntentCoverage: {"prompt", "model", "skills", "tools", "context", "memory", "harness"},
}

// Mounted reports whether this deployment can answer an intent at all.
//
// 🔴 It answers about the CAPABILITY, not about the data. A tenant that has reported no structure is
// mounted-and-not-measured, which is a completely different message from not-mounted: one names a
// command the reader can run, the other names something an operator has to install.
func (p platformSurfaceReader) Mounted(spec conversation.IntentSpec) bool {
	switch spec.Intent {
	case conversation.IntentAssess, conversation.IntentImprove:
		// P33's assessment and P35's improvement run. Neither is mounted anywhere yet, and saying so is
		// the honest answer — the alternative is a conversation that accepts "fix it, and open a pull
		// request" and produces a plausible nothing.
		return false
	case conversation.IntentRunHistory, conversation.IntentCompare:
		return p.srv.linkedRuns != nil
	case conversation.IntentPreviewChange:
		return p.srv.transformReceipts != nil
	case conversation.IntentDeliver:
		return p.srv.forgeDelivery != nil
	default:
		return p.srv.workflowIR != nil
	}
}

// Read answers one question about one surface.
func (p platformSurfaceReader) Read(ctx context.Context, tenantID, workflowID string, spec conversation.IntentSpec) (conversation.SurfaceReading, error) {
	switch spec.Intent {
	case conversation.IntentRunHistory, conversation.IntentCompare:
		return p.readRuns(tenantID, spec)
	case conversation.IntentPreviewChange:
		return p.readReceipts(tenantID)
	case conversation.IntentDeliver:
		return p.readDeliveries(ctx, tenantID)
	case conversation.IntentGraph, conversation.IntentGraphOrder:
		return p.readStructure(tenantID, workflowID, spec)
	default:
		return p.readAxes(tenantID, workflowID, spec)
	}
}

// The two absences, and 🔴 THEY ARE NOT THE SAME ABSENCE.
//
// This distinction was found by opening the surface in a browser, and it is worth recording because
// every gate was green while it was wrong. A tenant that had reported 28 nodes was told *"no structure
// has been reported for this workflow — run `heros link --with-ir`"* — on a card whose own claim, one
// line above, said "on any of this workflow's 28 nodes". Two sentences on one card contradicting each
// other, and the named next action was a command the reader had already run.
//
// The cause is that a projection full of `not-reported` cells looks identical whether the STRUCTURE is
// missing or only the VERDICTS are, and one string covered both. They have different remedies:
//
//	no structure   the platform has never been told this workflow exists → send it
//	no verdicts    the structure arrived, but from a CLI that ran no transform engine, so no per-node
//	               (node × axis) answer came with it → run the engine and link again
//
// A "no data" message that names the wrong next action is worse than one that names none: it costs the
// reader the time to run the command, plus the time to work out why nothing changed.
const missingStructure = "no structure has been reported for this workflow — run `heros link --with-ir` " +
	"from the repository to send it"

const missingVerdicts = "this workflow's structure is on record, but no per-node verdict for this axis " +
	"came with it — run `heros apply` (or `heros link --with-ir` from a CLI that carries the transform " +
	"engine) so each node's answer for this axis is reported"

func (p platformSurfaceReader) latestIR(tenantID, workflowID string) (linkingest.WorkflowIR, bool, error) {
	if p.srv.workflowIR == nil {
		return linkingest.WorkflowIR{}, false, errors.New("this deployment does not accept workflow structure")
	}
	return p.srv.workflowIR.Latest(tenantID, workflowID)
}

// readAxes answers the seven axis intents from the projection the coverage page already reads.
func (p platformSurfaceReader) readAxes(tenantID, workflowID string, spec conversation.IntentSpec) (conversation.SurfaceReading, error) {
	ir, found, err := p.latestIR(tenantID, workflowID)
	if err != nil {
		// A READ FAILURE, surfaced as a transport failure so the turn reconciles the step as `refused`
		// carrying this text. 🚫 Never reported as "you have reported nothing": that would tell a
		// customer their data was never received, on a day the database was merely unreachable.
		return conversation.SurfaceReading{}, fmt.Errorf("this workflow's reported structure could not be read: %w", err)
	}
	if !found {
		return conversation.SurfaceReading{
			Claim:        "nothing has been measured on this workflow yet",
			EvidenceRef:  "workflow:" + workflowID,
			State:        conversation.FindingNotMeasured,
			MissingInput: missingStructure,
		}, nil
	}

	projection := axisprojection.Build(ir, transform.CoverageTableVersion())
	axes := intentAxes[spec.Intent]
	byAxis := map[string]axisprojection.AxisTotals{}
	for _, t := range projection.Totals {
		byAxis[t.Axis] = t
	}

	var applies, refused, notApplicable, notReported, nodes int
	for _, axis := range axes {
		t := byAxis[axis]
		applies += t.Applies
		refused += t.Refused
		notApplicable += t.NotApplicable
		notReported += t.NotReported
		nodes += t.Nodes
	}
	evidence := fmt.Sprintf("axis-projection:%s@%s", workflowID, projection.SourceRevision)

	// 🔴 The ORDER of these branches is the order of honesty. "Nothing was reported" is checked FIRST,
	// because a projection full of `not-reported` cells has counts that look exactly like a projection
	// of a healthy workflow if you only read `Refused`.
	if nodes == 0 || notReported == nodes {
		// 🔴 Which absence is this? The structure is on record — we are reading a projection built from
		// it — so the missing input is the VERDICTS, unless the structure carried no nodes at all.
		missing := missingVerdicts
		if projection.NodeCount == 0 {
			missing = missingStructure
		}
		return conversation.SurfaceReading{
			Claim: fmt.Sprintf("no verdict for %s has been reported on any of this workflow's %d nodes",
				strings.Join(axes, ", "), projection.NodeCount),
			EvidenceRef:  evidence,
			State:        conversation.FindingNotMeasured,
			MissingInput: missing,
			Axis:         axes[0],
		}, nil
	}
	if refused > 0 {
		cause, node := firstRefusal(projection, axes)
		return conversation.SurfaceReading{
			// The CLAIM is a count with its denominator. A proportion rendered without the count behind
			// it is a number a reader cannot check: "68% covered" over three nodes and over four hundred
			// are the same string and different facts.
			Claim: fmt.Sprintf("%d of %d (node × axis) pairs on %s were refused by the engine",
				refused, nodes, strings.Join(axes, ", ")),
			EvidenceRef: evidence,
			State:       conversation.FindingRefused,
			// 🚫 The engine's own cause identifier, carried VERBATIM. A re-worded refusal is a second,
			// softer statement of a safety boundary.
			Cause: cause,
			Axis:  axes[0],
			Node:  node,
		}, nil
	}
	return conversation.SurfaceReading{
		Claim: fmt.Sprintf("%d of %d (node × axis) pairs on %s can be edited by the engine; "+
			"%d are not applicable to their language and %d were never reported",
			applies, nodes, strings.Join(axes, ", "), notApplicable, notReported),
		EvidenceRef: evidence,
		State:       conversation.FindingMeasured,
		Axis:        axes[0],
	}, nil
}

// firstRefusal returns the first refusal's cause and node, in a stable order.
//
// Stable because this text is rendered: a cause that changed between two identical questions would make
// the surface look non-deterministic when it is not.
func firstRefusal(p axisprojection.Projection, axes []string) (cause, node string) {
	want := map[string]bool{}
	for _, a := range axes {
		want[a] = true
	}
	rows := append([]axisprojection.NodeRow(nil), p.Nodes...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].NodeID < rows[j].NodeID })
	for _, row := range rows {
		cells := append([]axisprojection.Cell(nil), row.Cells...)
		sort.SliceStable(cells, func(i, j int) bool { return cells[i].Axis < cells[j].Axis })
		for _, c := range cells {
			if c.State == axisprojection.StateRefused && want[c.Axis] {
				return c.Cause, c.NodeID
			}
		}
	}
	return "", ""
}

// readStructure answers `graph` and `graph_order` from the reported IR.
func (p platformSurfaceReader) readStructure(tenantID, workflowID string, spec conversation.IntentSpec) (conversation.SurfaceReading, error) {
	ir, found, err := p.latestIR(tenantID, workflowID)
	if err != nil {
		return conversation.SurfaceReading{}, fmt.Errorf("this workflow's reported structure could not be read: %w", err)
	}
	if !found {
		return conversation.SurfaceReading{
			Claim:        "this workflow's shape has not been reported",
			EvidenceRef:  "workflow:" + workflowID,
			State:        conversation.FindingNotMeasured,
			MissingInput: missingStructure,
		}, nil
	}
	languages := map[string]int{}
	for _, n := range ir.Nodes {
		if n.Language != "" {
			languages[n.Language]++
		}
	}
	claim := fmt.Sprintf("this workflow reported %d nodes at revision %s", len(ir.Nodes), short(ir.SourceRevision))
	if len(languages) > 0 {
		claim += " (" + describeLanguages(languages) + ")"
	}
	if spec.Intent == conversation.IntentGraphOrder {
		// 🔴 The ordering question is answered as `not_measured` rather than guessed. `WorkflowIR` carries
		// nodes, not edges — a linear list is not a claim about concurrency, and inferring "these run in
		// sequence" from the order of a JSON array would be a confident wrong answer about the
		// customer's code. P34 is the phase that makes ordering expressible.
		return conversation.SurfaceReading{
			Claim:        claim + ", but their ordering was not reported",
			EvidenceRef:  "workflow-ir:" + workflowID + "@" + ir.SourceRevision,
			State:        conversation.FindingNotMeasured,
			MissingInput: "the reported structure carries nodes but no edges, so nothing here can say what runs before what",
		}, nil
	}
	return conversation.SurfaceReading{
		Claim:       claim,
		EvidenceRef: "workflow-ir:" + workflowID + "@" + ir.SourceRevision,
		State:       conversation.FindingMeasured,
	}, nil
}

func (p platformSurfaceReader) readRuns(tenantID string, spec conversation.IntentSpec) (conversation.SurfaceReading, error) {
	ids, err := p.srv.linkedRuns.LinkedRunIDs(tenantID)
	if err != nil {
		return conversation.SurfaceReading{}, fmt.Errorf("this organization's linked runs could not be read: %w", err)
	}
	if len(ids) == 0 {
		return conversation.SurfaceReading{
			Claim:        "no run has been linked to this organization",
			EvidenceRef:  "linked-runs:" + tenantID,
			State:        conversation.FindingNotMeasured,
			MissingInput: "run `heros link` after an evaluation to send a run's numbers",
		}, nil
	}
	claim := fmt.Sprintf("%d runs have been linked to this organization", len(ids))
	if spec.Intent == conversation.IntentCompare {
		// 🔴 A comparison needs at least two runs, and saying so is better than comparing one run with
		// itself and reporting no difference — which is a true sentence and a useless one.
		if len(ids) < 2 {
			return conversation.SurfaceReading{
				Claim:        "only one run has been linked, so there is nothing to compare it against",
				EvidenceRef:  "linked-runs:" + tenantID,
				State:        conversation.FindingNotMeasured,
				MissingInput: "link a second run of the same workflow",
			}, nil
		}
		claim = fmt.Sprintf("%d runs are available to compare on /app/variants", len(ids))
	}
	return conversation.SurfaceReading{
		Claim: claim, EvidenceRef: "linked-runs:" + tenantID, State: conversation.FindingMeasured,
	}, nil
}

func (p platformSurfaceReader) readReceipts(tenantID string) (conversation.SurfaceReading, error) {
	receipts, err := p.srv.transformReceipts.ListForTenant(tenantID, 25)
	if err != nil {
		return conversation.SurfaceReading{}, fmt.Errorf("this organization's transform receipts could not be read: %w", err)
	}
	if len(receipts) == 0 {
		return conversation.SurfaceReading{
			Claim:        "no transform has been reported, so there is no preview of what would be written",
			EvidenceRef:  "transform-receipts:" + tenantID,
			State:        conversation.FindingNotMeasured,
			MissingInput: "run `heros apply --link-receipt` to report what a change did to your tree",
		}, nil
	}
	return conversation.SurfaceReading{
		Claim: fmt.Sprintf("%d transform receipts are on record; each shows the per-node outcome and a "+
			"diffstat for one (config_hash, source_revision)", len(receipts)),
		EvidenceRef: "transform-receipts:" + tenantID,
		State:       conversation.FindingMeasured,
	}, nil
}

func (p platformSurfaceReader) readDeliveries(ctx context.Context, tenantID string) (conversation.SurfaceReading, error) {
	condition, err := p.srv.forgeDelivery.RouteConditionFor(ctx, tenantID)
	if err != nil {
		return conversation.SurfaceReading{}, fmt.Errorf("this organization's delivery route could not be read: %w", err)
	}
	heads, err := p.srv.forgeDelivery.ListDeliveries(ctx, tenantID)
	if err != nil {
		return conversation.SurfaceReading{}, fmt.Errorf("this organization's deliveries could not be read: %w", err)
	}
	// The route CONDITION is carried verbatim: `no_route`, `degraded` and `revoked` send an operator to
	// three different places, and collapsing them into "delivery is not set up" loses the one word that
	// decides what happens next.
	return conversation.SurfaceReading{
		Claim: fmt.Sprintf("the delivery route for this organization is %q, and %d deliveries are on record",
			condition, len(heads)),
		EvidenceRef: "deliveries:" + tenantID,
		State:       conversation.FindingMeasured,
	}, nil
}

func short(revision string) string {
	if len(revision) > 8 {
		return revision[:8]
	}
	if revision == "" {
		return "(unreported)"
	}
	return revision
}

func describeLanguages(counts map[string]int) string {
	langs := make([]string, 0, len(counts))
	for l := range counts {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	parts := make([]string, 0, len(langs))
	for _, l := range langs {
		parts = append(parts, fmt.Sprintf("%d %s", counts[l], l))
	}
	return strings.Join(parts, ", ")
}

// ── the pin ──────────────────────────────────────────────────────────────────────────────────────

// noPins is the PinResolver a deployment with no inference store gets.
//
// 🔴 It reports "nothing pinned" rather than being nil-checked away at the call site, so the replay path
// exists on every deployment and is exercised by every turn. A capability that only runs where a store
// happens to be mounted is a capability nobody notices breaking.
type noPins struct{}

func (noPins) Resolve(context.Context, string, string, conversation.IntentSpec) (conversation.Pin, error) {
	return conversation.Pin{}, nil
}

// ConversationPinSource is the pinned-inference store, as this package needs it (task 2.8).
//
// 🔴 The key is `(workflow, source_revision, agent config_hash)` — all THREE. A lookup by workflow alone
// would replay an answer computed by a different agent configuration and present it as this one's, which
// is a determinism guarantee inverted into a determinism lie.
type ConversationPinSource interface {
	// Get reads a stored inference by its three-part key. ok=false is NOT INFERRED, never an error.
	Get(ctx context.Context, workflowID, sourceRevision, agentConfigHash string) (herosagent.Stored, bool, error)
	// LatestFor is what makes staleness detectable: it returns the most recent inference for a workflow
	// WHATEVER revision it was taken at, so the replay can compare that revision against the current one.
	LatestFor(ctx context.Context, tenantID, workflowID string) (herosagent.Stored, bool, error)
}

// ConversationCurrentRevision reports a workflow's revision NOW, so a pin can be called stale.
type ConversationCurrentRevision interface {
	Latest(tenantID, workflowID string) (linkingest.WorkflowIR, bool, error)
}

// storedPins replays a pinned inference (FR11, PRD §14 Q2).
//
// # 🔴 Why the replay is a store READ and nothing else
//
// FR11's guarantee is that a repeated question costs NOTHING and returns the SAME answer. Both halves
// come from this function containing no call to a model, no call to a runner, and no path that could
// acquire one: `herosagent.Runner` is the only thing in the product that reaches a provider, and it is
// not reachable from here. The fence for this (task 6.7) asserts that no provider call was made rather
// than that two answers matched — two answers matching is what a deterministic model does too.
//
// # Q2: answer from the pin, label it stale, name the revision
//
// A pin taken at an earlier revision is NOT refused and NOT silently served. Refusing applies P30's
// operator rule to a customer and is too rigid — the answer is usually still true, and "I will not tell
// you what I know" is a bad trade. Serving it silently is worse: the reader has no way to learn that
// the claim describes code they have since changed. So it is served, marked `stale`, and carries the
// revision it describes, which is the only form of "stale" a reader can act on.
type storedPins struct {
	inferences ConversationPinSource
	structure  ConversationCurrentRevision
	// configHash is the agent configuration a replay must match. Empty means this deployment publishes
	// no active agent definition, in which case NOTHING is pinned — a replay keyed on an empty hash
	// would match rows from every configuration at once.
	configHash func(ctx context.Context) string
}

func (p storedPins) Resolve(ctx context.Context, tenantID, workflowID string, spec conversation.IntentSpec) (conversation.Pin, error) {
	if p.inferences == nil {
		return conversation.Pin{}, nil
	}
	hash := ""
	if p.configHash != nil {
		hash = p.configHash(ctx)
	}
	if hash == "" {
		return conversation.Pin{}, nil
	}

	current := ""
	if p.structure != nil {
		if ir, found, err := p.structure.Latest(tenantID, workflowID); err == nil && found {
			current = ir.SourceRevision
		}
	}

	// The exact key first: a pin for the CURRENT revision under the CURRENT configuration is the case
	// FR11 is about, and it is fresh by construction.
	if current != "" {
		if stored, ok, err := p.inferences.Get(ctx, workflowID, current, hash); err == nil && ok {
			if stored.TenantID != tenantID {
				// 🚫 A stored row for another tenant is NOT a pin. The three-part key contains no
				// tenant, so this check is the only thing between a shared workflow id and a
				// cross-tenant replay.
				return conversation.Pin{}, nil
			}
			return pinFrom(stored, current, spec), nil
		}
	}

	// Otherwise the most recent one, whatever revision it describes. `Pin.Stale()` compares the two and
	// the runner labels the finding — nothing here decides staleness, so there is one definition of it.
	stored, ok, err := p.inferences.LatestFor(ctx, tenantID, workflowID)
	if err != nil || !ok {
		return conversation.Pin{}, nil
	}
	if stored.AgentConfigHash != hash {
		// A pin from a DIFFERENT agent configuration is not this question's answer. Serving it would
		// attribute one configuration's reasoning to another, which no label could honestly describe.
		return conversation.Pin{}, nil
	}
	return pinFrom(stored, current, spec), nil
}

// pinFrom turns a stored inference into the reading a replay emits.
//
// The claim is the agent's own narrative, and the evidence reference is the INFERENCE ID — so a reader
// following it lands on the stored row that produced the sentence, rather than on a page that recomputes
// something similar.
func pinFrom(stored herosagent.Stored, currentRevision string, spec conversation.IntentSpec) conversation.Pin {
	claim := strings.TrimSpace(stored.Narrative)
	state := conversation.FindingMeasured
	missing := ""
	if claim == "" {
		// 🔴 An inference with no narrative is a real and common state — the agent may produce edges and
		// no prose, and D2 stores an abstention-only inference too. It replays as `not_measured` naming
		// what is absent, NOT as an empty claim: an empty claim renders as a card that says nothing,
		// which reads as "nothing is wrong here".
		claim = "the stored analysis of this workflow produced no prose about " + string(spec.Intent)
		state = conversation.FindingNotMeasured
		missing = "the pinned inference recorded edges or abstentions but no narrative for this surface"
	}
	return conversation.Pin{
		Found:           true,
		SourceRevision:  stored.SourceRevision,
		CurrentRevision: currentRevision,
		Reading: conversation.SurfaceReading{
			Claim:        claim,
			EvidenceRef:  "inference:" + stored.InferenceID,
			State:        state,
			MissingInput: missing,
		},
	}
}

// ── the budget envelope (PRD §14 Q6) ─────────────────────────────────────────────────────────────

// entitlementBudget derives a turn's envelope from the tenant's plan.
//
// 🚫 Nothing the person types can influence it. An authorable budget is better UX and is also how one
// question spends a month's allowance — and the person typing the question is the one least able to
// price it. It is DISPLAYED (the `plan` message carries all four numbers) and not editable, which is the
// combination that makes a limit a stated fact rather than a surprise.
type entitlementBudget struct {
	// gate is the tenant's entitlement gate, when this deployment has one. A deployment with none gets
	// the conservative envelope, which is the right default: too small is a VISIBLE stop reason, too
	// large is an INVISIBLE bill.
	gate *entitlement.Gate
}

// conservativeEnvelope is what a deployment with no entitlement source spends per turn.
//
// The numbers are deliberately modest and deliberately WRITTEN DOWN rather than derived: a reader asking
// "what will one question cost me?" gets an answer from this file, and the four limits are the four a
// stop reason can name.
var conservativeEnvelope = conversation.BudgetEnvelope{
	TurnCeiling:      4,
	TokenBudget:      40_000,
	ToolCallCeiling:  12,
	WallClockSeconds: 240,
}

// automationEnvelope is the envelope for a tenant whose plan includes autonomous work. Larger on every
// axis, and still bounded on every axis — "unbounded for paying customers" is how one question becomes
// an incident.
var automationEnvelope = conversation.BudgetEnvelope{
	TurnCeiling:      8,
	TokenBudget:      160_000,
	ToolCallCeiling:  40,
	WallClockSeconds: 900,
}

func (e entitlementBudget) Envelope(_ context.Context, tenantID string) (conversation.BudgetEnvelope, error) {
	if e.gate == nil {
		return conservativeEnvelope, nil
	}
	// 🔴 A gate ERROR yields the conservative envelope rather than an error, and the run continues. The
	// alternative — refuse the turn because billing was unreachable — makes an outage in a system that
	// decides how MUCH you may spend into an outage of whether you may ask anything at all. Spending
	// less than the customer is entitled to is a visible stop reason they can act on.
	decision, err := e.gate.AutoMerge(tenantID)
	if err != nil || !decision.Allowed {
		return conservativeEnvelope, nil
	}
	return automationEnvelope, nil
}

// ── the approval gate ────────────────────────────────────────────────────────────────────────────

// ledgerApprovalGate forwards to `internal/approval` and does nothing else.
//
// 🔴 Read the body: it is four lines, and three of them are the ownership check that already exists.
// Every line it does NOT contain is the point — no entitlement evaluation, no automation-level check, no
// attribution decided here. A "yes" in a chat window is not a new authorization primitive, and the way
// this stays true is that this adapter has nowhere to put a second opinion.
type ledgerApprovalGate struct{ db *sql.DB }

func (g ledgerApprovalGate) Approve(_ context.Context, approvalID, tenantID, userID string) error {
	p, err := approval.Get(g.db, approvalID)
	if err != nil {
		// 🔴 The SAME message as "not yours" below. A caller that could tell a missing proposal from
		// somebody else's could walk the id space and learn which proposals another organization has.
		return errors.New("no such approval")
	}
	if !approval.CanAccess(p, tenantID, false) {
		return errors.New("no such approval")
	}
	return approval.Approve(g.db, approvalID, userID)
}

// ── artifact resolvers (D7) ──────────────────────────────────────────────────────────────────────

// ledgerProposals resolves a `proposal_id` in the approval ledger.
type ledgerProposals struct{ db *sql.DB }

func (r ledgerProposals) Resolve(tenantID, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	p, err := approval.Get(r.db, ref)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 🔴 NOT FOUND is `false, nil` — a refusal. A missing row and an unreachable store are
			// different failures with different next actions, and the emitter refuses on both; only the
			// error path says the store was the problem.
			return false, nil
		}
		return false, err
	}
	return approval.CanAccess(p, tenantID, false), nil
}

// deliveryRecords resolves a delivery reference against the tenant's own delivery heads.
type deliveryRecords struct{ src ForgeDeliverySource }

func (r deliveryRecords) Resolve(tenantID, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	heads, err := r.src.ListDeliveries(context.Background(), tenantID)
	if err != nil {
		return false, err
	}
	for _, h := range heads {
		if h.DeliveryID == ref {
			return true, nil
		}
	}
	return false, nil
}

// ConversationVerdicts resolves a verification record. Satisfied by `hostedproposals.Gate`.
//
// 🔴 The SAME reader the delivery gate uses, so "verified" means one thing on this surface and one thing
// on every other. A second notion of verified living beside the ledger the proposal gate reads is
// precisely what design.md D8 refused when it made `verify` a phase rather than a message kind.
type ConversationVerdicts interface {
	Verdict(ctx context.Context, tenantID, configHash, sourceRevision string) (verification.Verdict, bool, error)
}

// verdictRecords resolves a `config_hash@source_revision` reference in the verification ledger.
type verdictRecords struct{ src ConversationVerdicts }

// VerdictRefSeparator joins the two halves of a verdict's identity in a message payload.
//
// A verdict is keyed by (config_hash, source_revision) — a PAIR — and a message field is one string, so
// the two are joined. Written as a constant because a separator spelled at two call sites is a separator
// that is `:` at one of them, and the failure mode is a reference that never resolves and a `result`
// that is refused for a reason nobody can see.
const VerdictRefSeparator = "@"

func (r verdictRecords) Resolve(tenantID, ref string) (bool, error) {
	if ref == "" || r.src == nil {
		return false, nil
	}
	configHash, sourceRevision, ok := strings.Cut(ref, VerdictRefSeparator)
	if !ok || configHash == "" || sourceRevision == "" {
		// A malformed reference is REFUSED, not treated as absent-and-fine. A `result` asserting
		// verification with an unparseable citation is exactly the shape a compromised model produces.
		return false, nil
	}
	_, found, err := r.src.Verdict(context.Background(), tenantID, configHash, sourceRevision)
	return found, err
}

// ── the seams the boot path assembles from ───────────────────────────────────────────────────────
//
// Six accessors, all of them one line. They exist so `internal/launch` can build the conversational
// mount WITHOUT this package exporting its adapter types — the same reasoning `consoletypes.go` gives
// for living in this package rather than exporting four view structs to satisfy a tool.
//
// 🔴 Each reads the LIVE server rather than a snapshot taken at boot. `Mounted` has to answer "is the
// delivery surface present?" at the moment somebody asks, and a struct filled in during boot would be
// the readiness signal that cannot go red — the failure P19 Decision 9 records for `components.postgres`.

// ConversationSurfaceReader returns the reader over this server's mounted read models.
func (s *Server) ConversationSurfaceReader() conversation.SurfaceReader {
	return platformSurfaceReader{srv: s}
}

// ConversationApprovalGate returns the adapter that FORWARDS to `internal/approval`.
func (s *Server) ConversationApprovalGate() ConversationApprovalGate {
	return ledgerApprovalGate{db: s.DB}
}

// ConversationWorkflows returns the ownership check backing the create route's non-disclosing 404.
func (s *Server) ConversationWorkflows() ConversationWorkflows {
	return workflowOwnership{srv: s}
}

// ConversationResolvers returns the artifact resolvers D7 requires.
//
// 🔴 A resolver whose store is absent is left NIL rather than replaced with one that says yes. The
// emitter then refuses the kind outright (`ErrNoResolver`), which is the correct behaviour: a
// deployment that cannot resolve proposals must not be able to emit proposals.
func (s *Server) ConversationResolvers() conversation.Resolvers {
	r := conversation.Resolvers{Proposal: ledgerProposals{db: s.DB}}
	if s.forgeDelivery != nil {
		r.Delivery = deliveryRecords{src: s.forgeDelivery}
	}
	if v, ok := s.proposals.(ConversationVerdicts); ok {
		r.Verdict = verdictRecords{src: v}
	}
	// The entitlement decision behind an `approval_request`. Backed by the same ledger the approval
	// gate reads, so an un-approvable request and a refused approval agree about why.
	r.Entitlement = ledgerProposals{db: s.DB}
	return r
}

// ConversationPins returns the pinned-inference resolver (FR11, task 2.8).
//
// A deployment with no inference store gets `noPins`, which reports "nothing pinned" — so the replay
// path exists and runs on every deployment, and the difference between deployments is whether it ever
// finds anything, not whether the code runs.
func (s *Server) ConversationPins(inferences ConversationPinSource) conversation.PinResolver {
	if inferences == nil || s.herosAgent == nil {
		return noPins{}
	}
	return storedPins{
		inferences: inferences,
		structure:  s.workflowIR,
		configHash: func(ctx context.Context) string {
			// 🔴 Read LIVE, on every turn. A hash captured at boot would replay under a definition
			// somebody has since activated a replacement for — a stale pin presented as a fresh one,
			// which is the failure the whole determinism guarantee exists to make impossible.
			def, ok, err := s.herosAgent.ActiveDefinition(ctx)
			if err != nil || !ok {
				return ""
			}
			return def.ConfigHash
		},
	}
}

// ConversationBudgets returns the envelope source (PRD §14 Q6).
func (s *Server) ConversationBudgets(gate *entitlement.Gate) conversation.BudgetSource {
	return entitlementBudget{gate: gate}
}

// workflowOwnership answers the create route's ownership question from the subject index.
type workflowOwnership struct{ srv *Server }

func (w workflowOwnership) OwnsWorkflow(tenantID, workflowID string) (bool, error) {
	if w.srv.workflowIndex == nil {
		// 🔴 No catalogue means the check CANNOT BE MADE, and "cannot be made" is answered as YES here
		// rather than NO. That reads backwards for a security check, so the reason matters: this
		// deployment does not enumerate workflows at all, the conversation is scoped to the session's
		// own tenant on every subsequent read, and every finding is drawn from stores that scope by
		// tenant themselves. Refusing instead would make the whole surface unusable on a deployment
		// with no subject index while protecting nothing that is not already protected one layer down.
		return true, nil
	}
	summaries, err := w.srv.workflowIndex.ListWorkflows(tenantID)
	if err != nil {
		return false, err
	}
	for _, s := range summaries {
		if s.WorkflowID == workflowID {
			return true, nil
		}
	}
	return false, nil
}
