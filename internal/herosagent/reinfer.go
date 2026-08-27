package herosagent

import (
	"context"
	"fmt"
	"sort"
)

// reinfer.go is task 4.8: re-inference is EXPLICIT, presented as a DIFF, and replaces only on
// confirmation.
//
// # Why not just re-run and overwrite
//
// D2 pins a result to `(workflow_id, source_revision, agent_config_hash)` on the claim that those three
// determine it. They do not determine the MODEL's behaviour — a provider-side model revision can change
// the answer under an unchanged key — and that is exactly why the design refuses "temperature 0 and a
// fixed seed, described as reproducible": its failure is silent, and "a customer sees a different graph
// on Tuesday and there is nothing in the record that says why" is the outcome being prevented.
//
// So a re-inference does not silently replace. It produces a DIFF against what is stored, somebody
// looks at it, and only then does it replace. The stored answer stays the answer until a person says
// otherwise.

// EdgeChange is one edge that appeared, disappeared, or changed confidence.
type EdgeChange struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind of change: added | removed | changed.
	Change string `json:"change"`
	// Before and After are the confidences. Zero on the side where the edge is absent — read `Change`
	// first, which is why it is not a nullable float: the change kind already says which side is real.
	Before float64 `json:"before,omitempty"`
	After  float64 `json:"after,omitempty"`
	// KindBefore and KindAfter differ on a `changed` edge whose kind moved between data and control.
	KindBefore string `json:"kind_before,omitempty"`
	KindAfter  string `json:"kind_after,omitempty"`
}

// Diff is what a re-inference would change.
type Diff struct {
	InferenceID string       `json:"inference_id"`
	Edges       []EdgeChange `json:"edges"`
	// LabelsAdded and LabelsRemoved name patterns by their region, so a reviewer sees which REGION
	// changed its meaning rather than a count.
	LabelsAdded   []string `json:"labels_added"`
	LabelsRemoved []string `json:"labels_removed"`
	// NarrativeChanged is a BOOLEAN, not a text diff. 🔴 The narrative is assessed prose: it is
	// expected to differ between runs, it dispatches nothing, and rendering a word-level diff of it
	// would draw a reviewer's attention to the one part of the answer that carries no weight.
	NarrativeChanged bool `json:"narrative_changed"`
	// Empty reports that a re-inference would change nothing — which is a REAL and reassuring answer,
	// and the one a reviewer most wants to be able to see quickly.
	Empty bool `json:"empty"`
}

// ReInfer runs the agent again against the same key and returns what WOULD change.
//
// 🔴 It does not write. `ConfirmReplace` does, and only with a diff a person has seen.
// 🔴 P36 — it takes the BINDING, like Infer, and for the same reason: a re-inference of a graph must
// run the graph, not its first node. Re-running one node of five and presenting the difference as "what
// changed" would be a diff between two different computations.
func (r *Runner) ReInfer(ctx context.Context, in Input, binding AssessmentBinding, placement Placement) (Diff, Result, error) {
	agentConfigHash := binding.ConfigHash
	// 🔴 The placement gate again, and NOT because it is tidy to repeat it. `ReInfer` calls the model
	// directly — it deliberately bypasses `Infer`'s cache read, which is the whole point of a
	// re-inference — so it also bypasses the gate that lives there. A second provider-reaching entry
	// point is exactly how a "customer runs nothing platform-side" rule ends up true of one function.
	if err := r.host.MayRun(placement); err != nil {
		code := CodeDisabled
		if placement != PlacementDisabled {
			code = CodeWrongPlacement
		}
		return Diff{}, Result{Code: code, ProviderCalls: 0, Cause: err.Error()}, err
	}
	stored, ok, err := r.store.Get(ctx, in.WorkflowID, in.SourceRevision, agentConfigHash)
	if err != nil {
		return Diff{}, Result{Code: CodeProviderFailed}, err
	}
	if !ok {
		return Diff{}, Result{}, fmt.Errorf("%w: nothing is stored for %s at %s under %s, so there is "+
			"nothing to re-infer — run an analysis instead", ErrInvalidDefinition,
			in.WorkflowID, in.SourceRevision, confighashDisplay(agentConfigHash))
	}

	// The fresh run BYPASSES the cache deliberately: reading through it is what the cache is for, and
	// what a re-inference exists to look past.
	if err := in.Budget.Validate(); err != nil {
		return Diff{}, Result{Code: CodeBudgetExceeded, Cause: err.Error()}, err
	}
	// 🔴 A GRAPH is re-run as a graph, through the SAME executor `Infer` uses. Re-running only
	// `r.model` would re-run the first node and present the difference as "what changed", which is a
	// diff between two different computations.
	//
	// 🚫 The fresh run is NOT stored. Replacing a pinned result is a separate, confirmed operation
	// (`Replace`), and a re-inference that wrote its own answer would make the diff a report of
	// something that had already happened.
	if binding.Graph() {
		fresh, gerr := r.freshGraph(ctx, in, binding)
		if gerr != nil {
			return Diff{}, fresh, gerr
		}
		return DiffOf(stored, fresh), fresh, nil
	}

	in.AgentConfigHash = agentConfigHash
	raw, usage, err := r.model.Infer(ctx, in)
	if err != nil {
		return Diff{}, Result{Code: CodeProviderFailed, ProviderCalls: 1, Cause: err.Error()}, err
	}
	fresh := Result{Code: CodeOK, ProviderCalls: 1, Usage: usage}
	fresh.Edges, fresh.Labels, fresh.Abstentions, fresh.Narrative = r.validate(in, raw)
	if n := binding.Definition.Primary(); n.NodeID != "" {
		for i := range fresh.Edges {
			fresh.Edges[i].ProducedByNode = n.NodeID
		}
	}

	return DiffOf(stored, fresh), fresh, nil
}

// DiffOf compares a stored inference with a fresh result.
func DiffOf(stored Stored, fresh Result) Diff {
	d := Diff{
		InferenceID: stored.InferenceID,
		Edges:       []EdgeChange{}, LabelsAdded: []string{}, LabelsRemoved: []string{},
	}
	type edgeKey struct{ from, to string }
	before := map[edgeKey]ProvenancedEdge{}
	for _, e := range stored.Edges {
		before[edgeKey{e.From, e.To}] = e
	}
	after := map[edgeKey]ProvenancedEdge{}
	for _, e := range fresh.Edges {
		after[edgeKey{e.From, e.To}] = e
	}
	for k, b := range before {
		a, still := after[k]
		switch {
		case !still:
			d.Edges = append(d.Edges, EdgeChange{From: k.from, To: k.to, Change: "removed",
				Before: b.Confidence, KindBefore: b.Kind})
		case a.Confidence != b.Confidence || a.Kind != b.Kind:
			d.Edges = append(d.Edges, EdgeChange{From: k.from, To: k.to, Change: "changed",
				Before: b.Confidence, After: a.Confidence, KindBefore: b.Kind, KindAfter: a.Kind})
		}
	}
	for k, a := range after {
		if _, existed := before[k]; !existed {
			d.Edges = append(d.Edges, EdgeChange{From: k.from, To: k.to, Change: "added",
				After: a.Confidence, KindAfter: a.Kind})
		}
	}
	sort.SliceStable(d.Edges, func(i, j int) bool {
		if d.Edges[i].From != d.Edges[j].From {
			return d.Edges[i].From < d.Edges[j].From
		}
		return d.Edges[i].To < d.Edges[j].To
	})

	labelKey := func(pattern string, ids []string) string {
		c := append([]string(nil), ids...)
		sort.Strings(c)
		return pattern + " over " + fmt.Sprint(c)
	}
	beforeL := map[string]bool{}
	for _, l := range stored.Labels {
		beforeL[labelKey(string(l.Pattern), l.NodeIDs)] = true
	}
	afterL := map[string]bool{}
	for _, l := range fresh.Labels {
		afterL[labelKey(string(l.Pattern), l.NodeIDs)] = true
	}
	for k := range beforeL {
		if !afterL[k] {
			d.LabelsRemoved = append(d.LabelsRemoved, k)
		}
	}
	for k := range afterL {
		if !beforeL[k] {
			d.LabelsAdded = append(d.LabelsAdded, k)
		}
	}
	sort.Strings(d.LabelsRemoved)
	sort.Strings(d.LabelsAdded)

	d.NarrativeChanged = stored.Narrative != fresh.Narrative
	d.Empty = len(d.Edges) == 0 && len(d.LabelsAdded) == 0 && len(d.LabelsRemoved) == 0
	return d
}

// ConfirmReplace writes a re-inferred result over the stored one.
//
// 🔴 It takes the DIFF as an argument, and refuses one that does not name the stored inference. That is
// what makes "replacing only on confirmation" structural rather than a UI convention: a caller cannot
// replace without having computed what would change, and cannot pass a diff of a different inference.
func (r *Runner) ConfirmReplace(ctx context.Context, d Diff, in Input, fresh Result,
	agentConfigHash string, placement Placement) error {

	if err := r.host.MayRun(placement); err != nil {
		return err
	}
	stored, ok, err := r.store.Get(ctx, in.WorkflowID, in.SourceRevision, agentConfigHash)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: nothing is stored under that key", ErrInvalidDefinition)
	}
	if d.InferenceID != stored.InferenceID {
		return fmt.Errorf("%w: this diff describes inference %s and the stored one is %s — a "+
			"confirmation must name what it replaces", ErrInvalidDefinition, d.InferenceID, stored.InferenceID)
	}
	return r.store.Replace(ctx, Stored{
		InferenceID:     stored.InferenceID,
		TenantID:        in.TenantID,
		WorkflowID:      in.WorkflowID,
		SourceRevision:  in.SourceRevision,
		AgentConfigHash: agentConfigHash,
		// The host's own placement, for the same reason Infer stamps it that way.
		Placement: r.host.PlacementOf(),
		Edges:     fresh.Edges, Labels: fresh.Labels, Abstentions: fresh.Abstentions,
		Narrative: fresh.Narrative,
		TokensIn:  fresh.Usage.InputTokens, TokensOut: fresh.Usage.OutputTokens,
		CreatedAtMS: r.nowMS(),
	})
}
