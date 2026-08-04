// Package proposalgen generates P5.5 proposals on the PLATFORM, from what a hosted deployment
// legitimately holds: the per-node cost and latency of linked runs, and the classified graph
// discovered from a source snapshot the customer pushed.
//
// # What it can propose, and what it structurally cannot
//
// P5.5's design assumes one environment: the engine diagnoses failing cases, compiles a codemod, builds
// a worktree and runs the harness. A hosted platform has none of that. `diagnosis.Diagnose` takes
// `[]attribution.FailingCase`, which carries `evalharness.Case` and `evalharness.Trace` — the eval cases
// and the run traces, both of which stay on the customer's machine by design (internal/runlink). So:
//
//	CAN be proposed here    a change driven by a structural SIGNAL, computed from quantities.
//	                        Cost and latency per node cross the boundary (`metrics.per_node`) precisely
//	                        so the scorecard can say WHICH node is expensive, and that is exactly the
//	                        input SignalCostBottleneck needs.
//	CANNOT be proposed here anything driven by a DIAGNOSIS: a prompt rewrite, a context-policy change
//	                        answering a specific failure, a skill swap. Those need the failing cases,
//	                        and no operator can be handed evidence the platform was never given.
//
// This package does not pretend otherwise. Every result names its state, and "this deployment cannot
// diagnose" is one of the things it can say.
//
// # It proposes; it never verifies
//
// Every proposal is written `candidate` / `unbuilt`. The gate that could promote one runs the eval
// harness, which is on the customer's side, so a verdict arrives through the ingest
// (internal/api/verdictingest.go) or it does not arrive. Nothing in this package writes a verdict.
package proposalgen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// State is what a generation pass found. It is a CLOSED, first-class value, never an empty list: the
// console's whole job here is to say what to do next, and "you have linked no runs", "your models have
// no published tiers" and "nothing on this workflow is a cost bottleneck" are three different sentences
// with three different next actions that an empty result cannot tell apart.
type State string

const (
	// StateGenerated: candidates were emitted and recorded.
	StateGenerated State = "generated"
	// StateNoRuns: this tenant has linked no runs for the workflow, so there is nothing to attribute.
	StateNoRuns State = "no_linked_runs"
	// StateNoPerNode: runs exist but none carries per-node metrics — the customer's CLI recorded no
	// breakdown, so cost cannot be attributed to a node. Distinct from StateNoRuns on purpose: one is
	// "start linking", the other is "your linked runs predate per-node metrics".
	StateNoPerNode State = "no_per_node_metrics"
	// StateNoGraph: no source snapshot has been pushed, so no node carries a pattern label and every
	// operator's admissibility check is unanswerable.
	StateNoGraph State = "no_discovered_graph"
	// StateNoMenu: no model catalog is published (internal/modelcatalog). Nothing is wrong with the
	// workflow; the deployment cannot say which model is cheaper.
	StateNoMenu State = "no_model_menu"
	// StateNoBottleneck: everything was available and no node dominates cost or latency. A real,
	// healthy answer — and the one that must never be confused with the four above.
	StateNoBottleneck State = "no_bottleneck"
	// StateNoCandidates: a bottleneck was found and every operator declined it (e.g. the node already
	// runs the cheapest published model). Also a real answer, with the refusals attached.
	StateNoCandidates State = "no_admissible_candidate"
	// StateRevisionMismatch: the newest linked run and the discovered graph are at DIFFERENT revisions,
	// so the per-node metrics and the workflow's shape describe different code.
	StateRevisionMismatch State = "revision_mismatch"
)

// Result is one generation pass.
type Result struct {
	TenantID   string `json:"tenant_id"`
	WorkflowID string `json:"workflow_id"`
	State      State  `json:"state"`
	// Detail is the sentence a console renders under the state. Always populated.
	Detail string `json:"detail"`
	// ProposalIDs are the proposals recorded by this pass, in deterministic order.
	ProposalIDs []string `json:"proposal_ids,omitempty"`
	// Bottlenecks are the flagged nodes the pass proposed against.
	Bottlenecks []attribution.BottleneckFlag `json:"bottlenecks,omitempty"`
	// Refusals are candidates an operator or a gate declined, kept for diagnostics. A refusal is not an
	// error and is never silently dropped — "the engine considered this and said no" is information.
	Refusals []proposal.Refusal `json:"refusals,omitempty"`
}

// RunSource reads a tenant's linked runs.
type RunSource interface {
	ForWorkflow(tenantID, workflowID string) ([]linkingest.LinkedRun, error)
}

// GraphSource reads the classified graph discovered from a pushed source snapshot.
type GraphSource interface {
	Latest(ctx context.Context, tenantID, workflowID string) (hostdiscovery.Graph, bool, error)
}

// MenuSource builds the operator menu for this deployment. It returns the menu and an account of what
// the join found, so a pass with no usable models can say which half was missing.
type MenuSource interface {
	Menu(ctx context.Context) (proposal.Menu, string, error)
}

// Sink records generated proposals.
type Sink interface {
	Put(ctx context.Context, r proposalstore.Record) error
}

// BlobStore holds the candidate Variant Spec (migration 0031). The row stores a content hash; the
// bytes live in the object store, the same discipline the diff and the grounding bundle follow.
type BlobStore interface {
	Put(ctx context.Context, data []byte) (string, error)
}

// Generator turns a tenant's linked runs into candidate proposals.
type Generator struct {
	Runs   RunSource
	Graphs GraphSource
	Menus  MenuSource
	Sink   Sink
	// Blobs persists each candidate's Variant Spec.
	//
	// 🔴 REQUIRED, not optional. "A proposal IS a candidate Variant Spec" (P5.5 design Decision 1), and a
	// proposal recorded without one describes a change nobody can reconstruct: the codemod has nothing
	// to apply, and re-deriving the spec later would compile a DIFFERENT change under an id a customer
	// may already be verifying. Generating without it produces rows that can never become diffs.
	Blobs BlobStore
	// Coverage is the Pareto coverage a bottleneck must reach. Zero selects attribution's default (the
	// majority), which is the same threshold the scorecard flags with — two thresholds for one word
	// would let the console call a node a bottleneck that the generator does not.
	Coverage float64
	// Now is injectable so a pass is deterministic under test.
	Now func() time.Time
}

func (g *Generator) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

// Generate runs one pass for a tenant's workflow.
//
// It is explicit rather than scheduled: generating a proposal reads a customer's discovered graph and
// writes rows that a delivery pipeline can later act on, and a background loop that does that on its
// own schedule is harder to reason about than a call somebody made.
func (g *Generator) Generate(ctx context.Context, tenantID, workflowID string) (Result, error) {
	res := Result{TenantID: tenantID, WorkflowID: workflowID}

	runs, err := g.Runs.ForWorkflow(tenantID, workflowID)
	if err != nil {
		return res, fmt.Errorf("proposalgen: read linked runs: %w", err)
	}
	if len(runs) == 0 {
		return res.with(StateNoRuns,
			"No runs have been linked for this workflow, so there is nothing to attribute cost to. "+
				"Run `heros link` after an eval."), nil
	}

	// The most recent run wins. Not an average across runs: a proposal names a config_hash and a source
	// revision, and averaging across configurations would attribute a cost to a configuration that never
	// ran. The newest linked run is the one whose per-node numbers describe the code as it stands.
	latest := newestRun(runs)
	costByNode, latencyByNode := totals(latest)
	if len(costByNode) == 0 && len(latencyByNode) == 0 {
		return res.with(StateNoPerNode,
			"The linked runs for this workflow carry no per-node metrics, so cost cannot be attributed "+
				"to a node. Per-node metrics are recorded by the CLI's eval; a run linked by an older "+
				"CLI has none."), nil
	}

	graph, ok, err := g.Graphs.Latest(ctx, tenantID, workflowID)
	if err != nil {
		return res, fmt.Errorf("proposalgen: read the discovered graph: %w", err)
	}
	if !ok {
		return res.with(StateNoGraph,
			"No source snapshot has been pushed for this workflow, so no node carries a pattern label "+
				"and no operator can judge whether a change is admissible. Run `heros push-source`."), nil
	}

	// 🔴 THE RUN AND THE GRAPH MUST BE THE SAME TREE, and nothing checked it.
	//
	// The per-node cost comes from the linked run; the node ids, the pattern labels and the spec's node
	// ORDER come from the discovered graph. Node ids are derived from call sites, so at two different
	// revisions they are two different id spaces — the bottleneck lookup silently matches nothing, and
	// the spec's order lists nodes the compile-time IR does not have. The transform then refuses the
	// proposal as "a wiring change" (it sees nodes dropped from the order), which is a confusing verdict
	// about a change nobody proposed.
	//
	// Found by compiling a proposal end to end. Before that, generation never resolved a spec against an
	// IR, so the two id spaces were never compared and the mismatch had nowhere to surface.
	if graph.SourceRevision != latest.SourceRevision {
		return res.with(StateRevisionMismatch, fmt.Sprintf(
			"The newest linked run is at %s and the discovered graph is at %s. Cost is attributed to node "+
				"ids from the run and the workflow's shape comes from the graph, so at two revisions they "+
				"describe different code. Push source for %s, or link a run measured at %s.",
			latest.SourceRevision, graph.SourceRevision, latest.SourceRevision, graph.SourceRevision)), nil
	}

	menu, menuDetail, err := g.Menus.Menu(ctx)
	if err != nil {
		// A missing catalog is a CONDITION reported as a state, not an error: the deployment is working
		// and is not configured to say which model is cheaper.
		return res.with(StateNoMenu, err.Error()), nil
	}
	if len(menu.Models) == 0 {
		return res.with(StateNoMenu,
			"No registered model has a published tier, so `cheaper` is not expressible on this "+
				"deployment and no downgrade can be proposed. "+menuDetail), nil
	}

	flags := attribution.BottleneckFromTotals(
		attribution.Variant{VariantID: latest.ConfigHash, EvalSetHash: latest.ConfigHash},
		costByNode, latencyByNode,
		attribution.BottleneckConfig{Coverage: g.Coverage},
	)
	if len(flags) == 0 {
		return res.with(StateNoBottleneck,
			"No node dominates this workflow's cost or latency. There is nothing here to downgrade, "+
				"which is a healthy result rather than a missing one."), nil
	}
	res.Bottlenecks = flags

	patterns := patternsByNode(graph.View)
	targets := make([]proposal.Target, 0, len(flags))
	for _, f := range flags {
		if f.Dimension != attribution.DimCost {
			// Latency dominance is flagged and carried in the result, but no operator fires on it: the
			// catalog's only signal-driven cost operator is the model downgrade, and proposing one against
			// a latency flag would answer "this node is slow" with "make it cheaper" — a different claim.
			continue
		}
		flag := f
		targets = append(targets, proposal.Target{
			// 🔴 A synthetic Diagnosis carrying ONLY the node id. OperatorInput.NodeID() reads
			// Diagnosis.NodeID, so a signal-driven target has nowhere else to put it. Every other field
			// stays zero deliberately — in particular TaxonomyCode, because `fires` short-circuits on the
			// signal and a fabricated cause would make a cost observation look like a diagnosed failure.
			Diagnosis:  diagnosis.Diagnosis{NodeID: f.NodeID},
			Signal:     proposal.SignalCostBottleneck,
			Pattern:    patterns[f.NodeID],
			Bottleneck: &flag,
		})
	}
	if len(targets) == 0 {
		return res.with(StateNoBottleneck,
			"Latency dominance was found but no cost bottleneck. This deployment has no signal-driven "+
				"operator for latency alone, so nothing is proposed."), nil
	}

	// 🔴 THE BASELINE IS BUILT FROM THE DISCOVERED GRAPH, and leaving it nil was a real defect.
	//
	// With Base == nil, `currentTier` reads "no discoverable current model" and returns maxTier+1 — so
	// EVERY published model counts as cheaper and the engine proposes a downgrade to each, including to
	// the model the node is already running. modelDowngradeOp's own neighbour in the catalog records
	// that exact failure ("every node is implicitly single-shot and the first candidate emitted was the
	// baseline"), found the same way: by running it against a real repository.
	//
	// The platform is not actually ignorant here. `ViewNode.Model` is `provider/model`, discovered from
	// the customer's own source, and the menu carries Provider and ModelID beside each Ref — so the
	// node's current model resolves to a registry ref by joining the two. A node whose model does not
	// resolve keeps no override, which returns it to the old behaviour for that node ALONE rather than
	// for the whole workflow.
	base := baseSpec(workflowID, latest.SourceRevision, graph.View, menu)
	em := proposal.Engine{Menu: menu, Base: base, BaseVariantID: latest.ConfigHash}.Propose(targets)
	res.Refusals = em.Refusals
	if len(em.Candidates) == 0 {
		return res.with(StateNoCandidates,
			"A cost bottleneck was found and every operator declined it — most often because the node "+
				"already runs the cheapest model with a published tier."), nil
	}

	if g.Blobs == nil {
		return res, fmt.Errorf("proposalgen: a blob store is required — a proposal recorded without its " +
			"Variant Spec can never be compiled into a diff")
	}
	proposal.SortCandidates(em.Candidates)
	for _, c := range em.Candidates {
		rec := g.record(tenantID, workflowID, latest, c)
		specHash, err := g.putSpec(ctx, c)
		if err != nil {
			return res, err
		}
		rec.SpecBlobHash = specHash
		if err := g.Sink.Put(ctx, rec); err != nil {
			return res, fmt.Errorf("proposalgen: record %s: %w", rec.ProposalID, err)
		}
		res.ProposalIDs = append(res.ProposalIDs, rec.ProposalID)
	}
	return res.with(StateGenerated, fmt.Sprintf(
		"%d candidate(s) recorded against %d cost bottleneck(s). Each is UNVERIFIED: the gate runs your "+
			"eval harness, so a verdict arrives via `heros report-verdict` or not at all.",
		len(res.ProposalIDs), len(targets))), nil
}

// record maps a candidate onto the durable row.
//
// 🔴 BuildStatus is `unbuilt` and there is no source diff, because this platform has not compiled one.
// Recording it as `built` — or leaving a diff hash that points at nothing — would let P12 offer to
// deliver a change whose bytes do not exist. `unbuilt` is what the surface reads to withhold it.
func (g *Generator) record(tenantID, workflowID string, run linkingest.LinkedRun, c proposal.Candidate) proposalstore.Record {
	return proposalstore.Record{
		ProposalID: proposalID(tenantID, workflowID, c),
		TenantID:   tenantID,
		WorkflowID: workflowID,
		// No DiagID: this candidate came from a signal, and Candidate.DiagID's own doc says a
		// signal-driven operator leaves it empty. An invented one would tie a cost observation to a
		// diagnosis nobody made.
		Operator: string(c.Operator),
		// The three fields the card renders (migration 0030). Without them the surface shows an operator
		// name and a hash and asks a reviewer to open a pull request on faith.
		NodeID:              c.NodeID,
		Pattern:             string(c.Pattern),
		Rationale:           c.Rationale,
		BaseVariantID:       run.ConfigHash,
		CandidateConfigHash: candidateHash(c),
		// The revision the run and the graph agree on — Generate refuses to proceed when they differ, so
		// by here there is only one. It is what hostedcompile materializes to compile the diff against.
		SourceRevision: run.SourceRevision,
		BuildStatus:    proposalstore.BuildUnbuilt,
		Status:         proposalstore.StatusCandidate,
		CreatedAt:      g.now(),
		// No Evidence: evidence is failing CASE ids, and this deployment holds none. An empty list here
		// is the truth, and the card renders it as "no case evidence" rather than as zero cases.
	}
}

// putSpec persists the candidate's Variant Spec and returns its content hash.
//
// The SAME canonical JSON candidateHash reads, so the hash the row carries and the bytes the compiler
// later unmarshals are the same serialization — two encoders for one value is two ways to disagree
// about what the proposal was.
func (g *Generator) putSpec(ctx context.Context, c proposal.Candidate) (string, error) {
	if c.Spec == nil {
		// An operator that emitted no spec emitted no change. Refused rather than recorded: a proposal
		// with nothing to apply is a card that can never become a diff.
		return "", fmt.Errorf("proposalgen: %s emitted a candidate with no Variant Spec", c.Operator)
	}
	b, err := json.Marshal(c.Spec)
	if err != nil {
		return "", fmt.Errorf("proposalgen: encode the spec for %s: %w", c.NodeID, err)
	}
	hash, err := g.Blobs.Put(ctx, b)
	if err != nil {
		return "", fmt.Errorf("proposalgen: store the spec for %s: %w", c.NodeID, err)
	}
	return hash, nil
}

// proposalID is deterministic in (tenant, workflow, node, operator, candidate). Re-running a pass over
// unchanged inputs must UPSERT the same rows rather than mint a second copy of every proposal — the
// store's Put is an upsert on this id, so determinism here is what makes the pass safe to repeat.
func proposalID(tenantID, workflowID string, c proposal.Candidate) string {
	sum := sha256.Sum256([]byte("proposalgen\x00" + tenantID + "\x00" + workflowID + "\x00" +
		c.NodeID + "\x00" + string(c.Operator) + "\x00" + candidateHash(c)))
	return "prop_" + hex.EncodeToString(sum[:16])
}

// candidateHash is a content address for the candidate's spec.
//
// It hashes the spec's canonical JSON rather than a hand-listed set of fields, and that choice is the
// load-bearing one. An enumeration — ModelRef, PromptRef, ContextPolicy, … — is right on the day it is
// written and silently wrong afterwards: `NodeOverride` has eight-odd fields and gains one per phase
// (ToolSelection in P14, ContextDropTolerance in P16), and a field the hash does not read is a field two
// DIFFERENT candidates hash identically on. They then collide onto one proposal id, and Put is an
// upsert, so one candidate quietly overwrites the other. encoding/json sorts map keys, so marshalling is
// deterministic, and a new field joins the hash by existing.
//
// ⚠️ This is NOT a variantspec config_hash, and the difference is worth stating. A real config_hash is
// minted by variantspec.Resolve against the IR and the registries, which resolves every ref and thereby
// proves the configuration is constructible. This identifies the candidate; it asserts nothing about
// whether the candidate resolves. That assertion belongs with the build, and the row says `unbuilt`.
func candidateHash(c proposal.Candidate) string {
	h := sha256.New()
	_, _ = h.Write([]byte("candidate\x00" + string(c.Operator) + "\x00" + c.NodeID + "\x00"))
	if c.Spec != nil {
		b, err := json.Marshal(c.Spec)
		if err != nil {
			// A spec that cannot be marshalled cannot be hashed, and a hash that silently degrades to
			// "operator + node" would collide every candidate for that node onto one row. Panicking here
			// is loud during a generation pass; a collision is silent forever.
			panic("proposalgen: candidate spec is not marshalable: " + err.Error())
		}
		_, _ = h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (r Result) with(s State, detail string) Result {
	r.State, r.Detail = s, detail
	return r
}

// baseSpec reconstructs the baseline Variant Spec from the discovered graph: for each node whose
// current `provider/model` resolves to a menu entry, the entry's registry ref.
//
// It is deliberately PARTIAL. A node whose model the menu cannot name gets no override, and the
// operator then treats that one node as unknown-tier — which is the honest answer for that node and
// leaves every other node judged correctly. Filling it with a guess would put a tier on a model nobody
// published, which is what the menu join already refuses to do.
func baseSpec(workflowID, sourceRevision string, view patternclassifier.GraphView, menu proposal.Menu) *variantspec.VariantSpec {
	byKey := make(map[string]string, len(menu.Models))
	for _, m := range menu.Models {
		byKey[m.Provider+"/"+m.ModelID] = m.Ref
	}
	spec := &variantspec.VariantSpec{
		WorkflowID:     workflowID,
		SourceRevision: sourceRevision,
		Nodes:          map[string]variantspec.NodeOverride{},
		// 🔴 ORDER IS DELIBERATELY EMPTY, and that is a claim rather than an omission: this proposal says
		// NOTHING about the workflow's ordering. internal/hostedcompile fills it from the IR at compile
		// time, and only when it is empty, so a proposal that does propose a reordering keeps its own.
		//
		// Filling it here is not possible and two attempts proved it. variantspec.Resolve requires an
		// order, so the first attempt supplied the graph's LAYOUT order (ViewNode's Layer/Order) — and
		// the transform reads a spec whose order differs from the SOURCE's as a wiring change, refusing
		// the whole proposal as control-flow surgery nobody proposed. The layout is a rendering, not the
		// order the statements run in, and the generator holds no IR to learn the difference from.
		//
		// Both mistakes were invisible until a proposal was compiled end to end, because generation
		// never resolved a spec against an IR.
	}
	for _, n := range view.Nodes {
		if ref, ok := byKey[n.Model]; ok {
			spec.Nodes[n.NodeID] = variantspec.NodeOverride{ModelRef: ref}
		}
	}
	return spec
}

// newestRun picks the most recently linked run, breaking ties on run id so a pass is deterministic.
func newestRun(runs []linkingest.LinkedRun) linkingest.LinkedRun {
	best := runs[0]
	for _, r := range runs[1:] {
		if r.LinkedAt.After(best.LinkedAt) || (r.LinkedAt.Equal(best.LinkedAt) && r.RunID > best.RunID) {
			best = r
		}
	}
	return best
}

// totals splits a run's per-node metrics into the two maps the Pareto takes. A node with a zero in one
// dimension is omitted from THAT map only: a free node is not a bottleneck, and including it as a zero
// would pad the denominator with nodes that contribute nothing.
func totals(run linkingest.LinkedRun) (cost, latency map[string]float64) {
	cost, latency = map[string]float64{}, map[string]float64{}
	for node, m := range run.PerNode {
		if m.CostUSD > 0 {
			cost[node] = m.CostUSD
		}
		if m.LatencyMS > 0 {
			latency[node] = m.LatencyMS
		}
	}
	return cost, latency
}

// patternsByNode indexes the discovered graph's node labels.
//
// A node with several labels takes the HIGHEST-CONFIDENCE one, and ties break on the pattern name so
// the choice is deterministic. A node with none keeps the zero Pattern, which every operator's
// admissibility check reads as "unclassified" — and an operator whose AdmissiblePatterns list is
// non-empty will decline it, which is the correct outcome rather than a defaulted label.
func patternsByNode(view patternclassifier.GraphView) map[string]patternclassifier.Pattern {
	out := make(map[string]patternclassifier.Pattern, len(view.Nodes))
	for _, n := range view.Nodes {
		best := -1.0
		for _, l := range n.Labels {
			if l.Confidence > best || (l.Confidence == best && string(l.Pattern) < string(out[n.NodeID])) {
				best, out[n.NodeID] = l.Confidence, l.Pattern
			}
		}
	}
	return out
}
