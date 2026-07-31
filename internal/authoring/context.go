package authoring

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// CONTEXT AUTHORING (P16 16c, tasks 8.3–8.10).
//
// # This axis is governed by LOSS, not by permission
//
// Context is the one axis where a change silently destroys information. Every policy other than "keep
// everything" drops something — that is what a policy IS — and how much it drops is MEASURED, not
// asserted. So the interesting question is not "may this user pick this policy?" (they may) but:
//
//	what do we do when we have never measured how much this policy would drop on this node?
//
// # The third verdict, and why both alternatives are lies
//
// Reporting `admissible` asserts that a safety check succeeded when it never ran — on the axis whose
// failure mode is precisely "no error anywhere, the answer just quietly gets worse".
//
// Reporting `refused` blames the USER'S CHANGE for the PLATFORM'S missing measurement, and would make
// the axis unusable on every workflow nobody has evaluated yet, which is every new one.
//
// Fail-closed is right for MEMBERSHIP questions — a tool outside the discovered set, a skill outside the
// registry — because there the missing fact is about the user's input. Here the missing fact is about
// US. Same reflex, wrong direction. So the gate never refuses on ignorance, and never passes on it: it
// says which measurement is absent, which is a next step rather than a dead end.
//
// # A user may set the policy; a user may not set the label
//
// Retrieval parameters are meaningful only on a node the classifier labels as retrieval. A user who
// could set that label could unlock parameters on a node where they do nothing and then attribute a
// result to them. A misclassification is a classifier defect to fix upstream, not an override to hand
// out — the same rule as *a user may not author the evidence*.

// DropMeasurement is what the platform knows about a (node, policy) pair's loss.
//
// `Measured` is a separate field from a zero ratio because 0.0 is a real, meaningful measurement — a
// lossless policy — and inferring "unmeasured" from it would silently discard the very result that
// proves a policy safe.
type DropMeasurement struct {
	Ratio    float64
	Measured bool
}

// DropSource answers "how much has this policy been observed to drop at this node?".
//
// It is an interface so the gate can be exercised without telemetry, and so the production wiring reads
// the same `context_drop_ratio` attribution already consumes rather than a second store.
type DropSource interface {
	Drop(nodeID, policy string) DropMeasurement
}

// PolicyCatalog is the set of registered context policies. A policy outside it is not a lesser choice;
// it is not a choice, because nothing resolves it.
type PolicyCatalog interface {
	Registered(policyRef string) bool
	// Policies lists every registered policy, for a picker that offers only what exists.
	Policies() []string
}

// PatternLabels answers what the classifier called a node. It is READ-ONLY here by design: there is no
// setter on this interface, and that absence is the enforcement.
type PatternLabels interface {
	// IsRetrieval reports whether the classifier labelled this node as retrieval.
	IsRetrieval(nodeID string) bool
	// Label returns the classifier's label, for a refusal that says what the node actually is.
	Label(nodeID string) string
}

// ContextNode is one node's context-authoring surface.
type ContextNode struct {
	NodeID   string
	Language string
	// CurrentPolicy is the policy in force, used to report a tolerance the node already exceeds.
	CurrentPolicy string
	// DeclaredTolerance is the node's declared drop tolerance, or nil when it declares none. A node that
	// declares none is not "zero tolerance" — zero would reject every lossy policy, which is the opposite
	// of declaring nothing.
	DeclaredTolerance *float64
}

// DropGate is the drop-tolerance admissibility gate, run at PREFLIGHT — before any eval spend.
//
// 🚫 It has no Force, no Assume, no DefaultRatio and no StrictMode. A field that let a caller supply a
// ratio would be a way to answer the gate's question without measuring, which is the one thing it exists
// to prevent.
type DropGate struct {
	// Drops supplies measurements. Required: a gate with no source cannot distinguish "lossless" from
	// "never looked", and would have to guess — which is the failure mode this whole file is about.
	Drops DropSource
	// Coverage answers whether the node's language can carry a context change at all.
	Coverage LanguageCoverage
}

// Check implements Admissibility. It is the drop-tolerance gate as preflight consumes it.
func (g DropGate) Check(_ context.Context, r *variantspec.Resolved) (Verdict, Refusal, MissingInput) {
	if r == nil {
		return VerdictAdmissible, Refusal{}, MissingInput{}
	}
	// Deterministic order, so two runs over the same config report the same node first. The resolved
	// nodes are a SLICE, so they are copied and sorted rather than relied on to arrive ordered.
	nodes := append([]variantspec.ResolvedNode(nil), r.Config.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })

	for _, node := range nodes {
		if node.ContextPolicy == "" {
			continue // no context change on this node; nothing for this gate to judge
		}
		v, ref, missing := g.judge(node.NodeID, node.ContextPolicy, node.ContextDropTolerance)
		if v != VerdictAdmissible {
			return v, ref, missing
		}
	}
	return VerdictAdmissible, Refusal{}, MissingInput{}
}

// judge decides one node.
func (g DropGate) judge(nodeID, policy string, tolerance *float64) (Verdict, Refusal, MissingInput) {
	if tolerance == nil {
		// A node that declares no tolerance is not judged on loss. Declaring nothing is not declaring
		// zero, and treating it as zero would reject every lossy policy on every node nobody configured.
		return VerdictAdmissible, Refusal{}, MissingInput{}
	}
	if g.Drops == nil {
		// No measurement source at all. This is ignorance too, and the same rule applies.
		return VerdictNotYetMeasurable, Refusal{},
			MissingInput{Kind: "context_drop_ratio", NodeID: nodeID, Subject: policy}
	}

	m := g.Drops.Drop(nodeID, policy)
	if !m.Measured {
		// 🔴 The third verdict. Not admissible — that would assert a check that never ran. Not refused —
		// that would blame the user for our missing measurement.
		return VerdictNotYetMeasurable, Refusal{},
			MissingInput{Kind: "context_drop_ratio", NodeID: nodeID, Subject: policy}
	}
	if m.Ratio > *tolerance {
		return VerdictRefused, Refusal{
			Cause: fmt.Sprintf(
				"node %q declares a drop tolerance of %.2f, and %q was measured to discard %.2f of its "+
					"context — refused before any evaluation spend",
				nodeID, *tolerance, policy, m.Ratio),
			NodeID: nodeID, Field: "context", Shape: "context policy",
		}, MissingInput{}
	}
	return VerdictAdmissible, Refusal{}, MissingInput{}
}

// ContextOffer is what a surface may present for a node's context authoring.
type ContextOffer struct {
	// Policies are the registered policies that may be selected. Empty when the language cannot carry a
	// context change.
	Policies []string
	// RetrievalParams reports whether retrieval parameters may be offered here.
	RetrievalParams bool
	// RetrievalReason states why they are not offered, when they are not. A control that is simply
	// absent reads as a missing feature.
	RetrievalReason string
	// Refused is set when the language boundary blocks the node entirely.
	Refused Refusal
}

// OfferContext decides what a surface may present, and states every boundary rather than expressing it
// as an absence.
func OfferContext(n ContextNode, cat PolicyCatalog, cov LanguageCoverage, labels PatternLabels) ContextOffer {
	if cov != nil && !cov.Materializes(n.Language) {
		return ContextOffer{Refused: Refusal{
			Cause: fmt.Sprintf(
				"node %q, dim context: context assembly is how the surrounding code builds the message "+
					"list, so applying a policy means rewriting that code — and no rewriter for %s has "+
					"landed yet (covered today: %s)",
				n.NodeID, displayLanguage(n.Language), joinOr(cov.Languages(), "none")),
			NodeID: n.NodeID, Field: "context", Shape: "context policy"}}
	}

	out := ContextOffer{}
	if cat != nil {
		out.Policies = append([]string(nil), cat.Policies()...)
		sort.Strings(out.Policies)
	}

	// Retrieval parameters are gated by the CLASSIFIER, and the reason is stated when they are absent.
	if labels == nil {
		out.RetrievalReason = "the classifier has not labelled this node, so retrieval tuning is not offered"
		return out
	}
	if !labels.IsRetrieval(n.NodeID) {
		out.RetrievalReason = fmt.Sprintf(
			"retrieval parameters apply only to a retrieval node; the classifier labelled %q as %s",
			n.NodeID, displayLabel(labels.Label(n.NodeID)))
		return out
	}
	out.RetrievalParams = true
	return out
}

// ValidateContextChange accepts or refuses an authored context selection.
func ValidateContextChange(n ContextNode, cat PolicyCatalog, cov LanguageCoverage, labels PatternLabels,
	policyRef string, retrievalParams map[string]any) Refusal {

	if offer := OfferContext(n, cat, cov, labels); offer.Refused.Cause != "" {
		return offer.Refused
	}
	if policyRef != "" && cat != nil && !cat.Registered(policyRef) {
		return Refusal{
			Cause: fmt.Sprintf(
				"node %q, dim context: %q is not a registered context policy — a policy is selected from "+
					"the registry, never named as free text",
				n.NodeID, policyRef),
			NodeID: n.NodeID, Field: "context", Shape: "context policy"}
	}
	if len(retrievalParams) > 0 {
		if labels == nil || !labels.IsRetrieval(n.NodeID) {
			return Refusal{
				Cause: fmt.Sprintf(
					"node %q, dim context: retrieval parameters apply only to a node the classifier labels "+
						"as retrieval, and this one is labelled %s — the label is not settable here, because "+
						"a node relabelled to unlock parameters would let a result be attributed to "+
						"parameters that did nothing",
					n.NodeID, displayLabel(labelOf(labels, n.NodeID))),
				NodeID: n.NodeID, Field: "context", Shape: "retrieval tuning"}
		}
	}
	return Refusal{}
}

// ToleranceReport is what a surface tells a user about a tolerance they just declared.
//
// A declared tolerance the node's CURRENT policy already exceeds is REPORTED rather than silently
// accepted: quietly storing it would leave the node in a state its own declaration forbids, and nobody
// would learn that until the next proposal was refused for a reason that looked unrelated.
type ToleranceReport struct {
	// AlreadyExceeded is true when the node's current policy discards more than the new tolerance allows.
	AlreadyExceeded bool
	// CurrentPolicy and MeasuredRatio are what makes the report actionable.
	CurrentPolicy string
	MeasuredRatio float64
	Message       string
}

// ReportTolerance describes the effect of declaring a tolerance on a node.
func ReportTolerance(n ContextNode, drops DropSource, tolerance float64) ToleranceReport {
	if drops == nil || n.CurrentPolicy == "" {
		return ToleranceReport{}
	}
	m := drops.Drop(n.NodeID, n.CurrentPolicy)
	if !m.Measured || m.Ratio <= tolerance {
		return ToleranceReport{CurrentPolicy: n.CurrentPolicy, MeasuredRatio: m.Ratio}
	}
	return ToleranceReport{
		AlreadyExceeded: true, CurrentPolicy: n.CurrentPolicy, MeasuredRatio: m.Ratio,
		Message: fmt.Sprintf(
			"node %q currently runs %q, which was measured to discard %.2f of its context — more than the "+
				"%.2f you just declared. The declaration is recorded; the node is already outside it.",
			n.NodeID, n.CurrentPolicy, m.Ratio, tolerance),
	}
}

// DropRatioLabel is how a drop ratio is DESCRIBED, everywhere.
//
// 🔴 It is a function rather than a convention because the alternative wording is so tempting: a lossier
// policy shows fewer tokens, and "tokens saved" is the easiest number in the product to draw. On an
// unverified change it is also false — until the harness runs, task success is unmeasured, and a policy
// that dropped the answer looks identical to one that dropped filler.
func DropRatioLabel(ratio float64) string {
	return fmt.Sprintf("%.0f%% of this node's context was discarded by the policy", ratio*100)
}

func joinOr(xs []string, fallback string) string {
	if len(xs) == 0 {
		return fallback
	}
	out := xs[0]
	for _, x := range xs[1:] {
		out += ", " + x
	}
	return out
}

func labelOf(labels PatternLabels, nodeID string) string {
	if labels == nil {
		return ""
	}
	return labels.Label(nodeID)
}

func displayLabel(l string) string {
	if l == "" {
		return "unclassified"
	}
	return l
}

// HeldOutExcluding derives the held-out split for an AUTHORED change, excluding cases the surface showed
// the author as motivation (P16 16c task 8.10).
//
// # Why this is stricter than the operator path's split
//
// An operator's motivating cases are whatever it selected. A person's are whatever they were LOOKING AT
// — and a human who has spent an afternoon on five failing cases will tune to those five whether or not
// they intended to. So the shown set is excluded from the held-out bucket explicitly, rather than relying
// on the deterministic split to separate them by luck.
//
// The derivation is otherwise identical: a pure function of the configuration hash and the case ids, so
// the verdict is reproducible and the author cannot shop for a split by resubmitting.
func HeldOutExcluding(configHash string, allCases, shown []string) CaseSplit {
	shownSet := map[string]bool{}
	for _, c := range shown {
		shownSet[c] = true
	}
	var split CaseSplit
	seen := map[string]bool{}
	uniq := make([]string, 0, len(allCases))
	for _, c := range allCases {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		uniq = append(uniq, c)
	}
	sort.Strings(uniq)

	for _, c := range uniq {
		// A shown case can never judge the change, whichever bucket the hash would have put it in.
		if shownSet[c] {
			split.Motivating = append(split.Motivating, c)
			continue
		}
		if heldOutBucket(configHash, c) {
			split.HeldOut = append(split.HeldOut, c)
		} else {
			split.Motivating = append(split.Motivating, c)
		}
	}
	return split
}

// CaseSplit is a deterministic, disjoint partition of eval cases.
type CaseSplit struct {
	Motivating []string
	HeldOut    []string
}

// heldOutBucket assigns one case, deterministically, from the configuration hash and the case id. It
// mirrors the proposal engine's derivation rather than importing it, because importing the proposal
// package here would invert the dependency the engine is built on.
func heldOutBucket(configHash, caseID string) bool {
	sum := sha256.Sum256([]byte(configHash + "\x00" + caseID))
	return binary.BigEndian.Uint64(sum[:8])%2 == 0
}
