// Package nodeaxis answers, for one call site and one optimization axis, what the REAL transform engine
// does when it is run against the REAL source — `applies`, or `refused` with the engine's own cause.
//
// # Why this exists rather than a coverage lookup
//
// `transform.AxisCoverage()` answers (axis, language, form). It is correct, it is total, and it is a fact
// about THE BUILD. It does not answer a call site's own shape — arguments unpacked from a mapping, a tool
// list assembled at run time, an SDK that binds its model before the call, a registry row with no locator
// — and `call-site-cannot-carry-it` exists precisely because that class of refusal is invisible to the
// table.
//
// So a projection built from the table would be right most of the time and would claim `applies` for
// exactly the call sites that refuse for their own shape. That is worse than no projection: it is the
// input to a customer's decision about what to author, and it would be wrong in the one direction that
// wastes their afternoon.
//
// 🔴 The verdicts are computed HERE, on the customer's machine, because this is the only place the source
// is. The platform is forbidden from deriving one (see the design's D2 and the fence in
// internal/axisprojection): it has the node's language and could compute the (axis, language) cell, and
// it must not.
//
// # The fail-safe direction, which is the whole safety argument
//
// Only an `ErrUnsafeRewrite` — the engine's own refusal, which always carries one of three cause classes
// — is transmitted as `refused`. Everything else is transmitted as NOTHING:
//
//	a node the index did not find          → no verdict for any axis
//	a language with no engine              → no verdict
//	a probe this package could not build   → no verdict for that axis
//	an internal failure                    → no verdict
//
// An absent verdict renders `not-reported` on every surface, which is true and says so. The alternative —
// reporting an uncomputable answer as `refused`, or worse as `applies` — would put this package's own
// limitations into a sentence about the customer's code. `not-reported` is a fourth state for exactly
// this reason, and it is cheap; a wrong verdict is not.
//
// # What a probe is, and why it must be a real change
//
// To ask "can the engine rewrite this node's model?" the engine has to be given a model to rewrite it TO.
// The probe is that value. It must be a change the engine would genuinely accept somewhere, or the
// refusal it produces is a fact about the probe rather than about the call site — which would be the same
// fabrication in a different disguise. So each probe below is built from the real registry vocabulary
// (`BuiltinPolicies`, `MemoryStrategyNamed`, `HarnessStrategyNamed`, `ParseTemplate`), and where a node
// cannot supply what a probe needs — a tool prune at a node that declares no tools — the answer is
// absence, not a guess.
package nodeaxis

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Verdict is one axis's answer for one node.
type Verdict struct {
	Axis string
	// Status is transform-neutral on purpose: `applies` or `refused`, the two values runlink permits.
	Status string
	// Cause is the engine's stable class identifier, set only when Status is refused.
	Cause string
}

// NodeReport is everything this package can say about one node.
type NodeReport struct {
	NodeID string
	// Language is what the frontend that re-detected this node reports. Empty when none did — never
	// inferred from the file's extension, because a guessed language makes a guessed verdict look
	// computed.
	Language string
	// Verdicts is sorted by axis and carries only the axes that were actually decided.
	Verdicts []Verdict
}

// Report is the whole computation for one repository at one revision.
type Report struct {
	// CoverageVersion is the table the engine in THIS binary carries. Transmitted with the verdicts so a
	// console can label a projection stale rather than mixing two builds' answers.
	CoverageVersion string
	Nodes           []NodeReport
	// Undecided counts (node, axis) pairs this package declined to answer, by reason. It is diagnostic
	// output for the CLI's narration — a developer who sees "31 of 40 nodes reported nothing" deserves to
	// know it was a missing engine rather than a broken run.
	Undecided map[string]int
}

// ProbedAxes are the axes this package can ask about per node.
//
// 🔴 It is `transform.CoverageAxes()` MINUS wiring, and the omission is deliberate rather than pending.
// Wiring's scope is a set of EDGES, not a node: `checkWiring` decides against the spec's graph before any
// node is visited, so "does wiring apply at node N" has no answer the engine could give. Reporting one
// would invent a grain, and an invented grain is a fabricated claim wearing a real cause's clothes.
//
// The consequence is stated rather than hidden: every wiring cell in the projection reads `not-reported`,
// and the console says why.
func ProbedAxes() []string {
	var out []string
	for _, a := range transform.CoverageAxes() {
		if a == "wiring" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// Compute runs the engine over every node in ir against the tree at root.
//
// It never writes: `transform.Generate` is a pure function of (bytes at root, resolved config), so this
// cannot touch a working copy. It never executes the target either — discovery and the engine both parse.
func Compute(ir *discovery.IR, root string) Report {
	rep := Report{CoverageVersion: transform.CoverageTableVersion(), Undecided: map[string]int{}}
	if ir == nil || root == "" {
		return rep
	}
	langByNode := languagesFor(ir, root)

	// 🔴 Grouped BY LANGUAGE, and every node's probes for one language answered in a single engine call.
	//
	// The first version asked the engine once per (node, axis) through `transform.Generate`, which indexes
	// the whole tree on every call. Against nousresearch/hermes-agent — 27 nodes, 7 axes, 8200 files —
	// `heros link --with-ir` had not finished after ten minutes and was killed. A command that hangs
	// delivers the same empty console this phase exists to fix, by a longer road, so this is a level-2/3
	// failure rather than an implementation-cost one. `transform.ProbeNodeDimensions` indexes once.
	byLang := map[string][]transform.ProbeRequest{}
	for _, n := range ir.Nodes {
		lang := langByNode[n.NodeID]
		if lang == "" {
			// No frontend claimed this node at this tree. Every axis is undecided, and saying so once is
			// more useful than seven identical absences.
			rep.Undecided["no-frontend-reported-this-node"] += len(ProbedAxes())
			continue
		}
		for _, axis := range ProbedAxes() {
			ov, ok := probeFor(axis, n)
			if !ok {
				rep.Undecided["no-probe-for-this-node-and-axis"]++
				continue
			}
			byLang[lang] = append(byLang[lang], transform.ProbeRequest{
				NodeID: n.NodeID, Dim: axis, Override: ov,
			})
		}
	}

	verdicts := map[string][]Verdict{}
	for _, lang := range sortedKeysOf(byLang) {
		outcomes, err := transform.ProbeNodeDimensions(lang, root, byLang[lang])
		if err != nil {
			// The engine could not read this tree at all. Every request for this language is undecided —
			// never a refusal, which would blame the customer's code for our inability to parse it.
			rep.Undecided["engine-could-not-index-this-tree"] += len(byLang[lang])
			continue
		}
		for _, o := range outcomes {
			switch {
			case o.Undecided:
				rep.Undecided[o.Why]++
			case o.Cause != "":
				verdicts[o.NodeID] = append(verdicts[o.NodeID],
					Verdict{Axis: o.Dim, Status: "refused", Cause: string(o.Cause)})
			case o.Applies:
				verdicts[o.NodeID] = append(verdicts[o.NodeID], Verdict{Axis: o.Dim, Status: "applies"})
			}
		}
	}

	for _, n := range ir.Nodes {
		nr := NodeReport{NodeID: n.NodeID, Language: langByNode[n.NodeID], Verdicts: verdicts[n.NodeID]}
		sort.Slice(nr.Verdicts, func(i, j int) bool { return nr.Verdicts[i].Axis < nr.Verdicts[j].Axis })
		rep.Nodes = append(rep.Nodes, nr)
	}
	sort.Slice(rep.Nodes, func(i, j int) bool { return rep.Nodes[i].NodeID < rep.Nodes[j].NodeID })
	return rep
}

func sortedKeysOf(m map[string][]transform.ProbeRequest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// probeFor builds the minimal real change for one axis at one node, or reports that it cannot.
//
// Every value is drawn from the shipped registry vocabulary. Where the node cannot supply what an axis
// needs, this returns false and the caller records an absence — see the package header on why absence
// beats a guess.
func probeFor(axis string, n discovery.IRNode) (variantspec.ResolvedOverride, bool) {
	switch axis {
	case string(variantspec.DimModel):
		provider := n.Model.Provider
		if provider == "" || provider == discovery.UnresolvedSentinel {
			// With no provider the engine has no registry row to locate the binding against, so the
			// refusal it would produce would be about the probe. Absent.
			return variantspec.ResolvedOverride{}, false
		}
		return variantspec.ResolvedOverride{Model: &registry.ModelEntry{
			VersionID: strings.Repeat("a", 64), Name: "probe",
			// The node's OWN provider, a different model id: a model swap, not a provider swap. A provider
			// swap is refused for reasons that have nothing to do with whether the model is rewritable.
			Spec: registry.ModelSpec{Provider: provider, ModelID: probeModelID(n.Model.ModelID)},
		}}, true

	case string(variantspec.DimPrompt):
		tmpl, err := registry.ParseTemplate("probe")
		if err != nil {
			return variantspec.ResolvedOverride{}, false
		}
		return variantspec.ResolvedOverride{Prompt: &registry.PromptEntry{
			VersionID: strings.Repeat("p", 64), Name: "probe", Template: tmpl,
			Spec: registry.PromptSpec{BodyBlobHash: strings.Repeat("b", 64), Slots: tmpl.Slots()},
		}}, true

	case string(variantspec.DimSkills):
		// 🔴 The input schema declares a PROPERTY, and that is not decoration.
		//
		// `{"type":"object"}` with no `properties` was the first probe here, and it produced
		// `refused: call-site-cannot-carry-it` at a call site that would have accepted a real skill —
		// because the binder refuses a sealed schema with "no argument shape to construct a tool value
		// from". A refusal about the PROBE, transmitted as a fact about the customer's code. That is the
		// exact fabrication this package exists to prevent, found by reading the engine's own `Detail`
		// while building it, and it is the reason every probe below is drawn from the real vocabulary
		// rather than from the smallest thing that compiles.
		e, err := registry.NewSkillEntry(strings.Repeat("s", 64), "probe", registry.SkillSpec{
			ImplHandle: "builtin:probe",
			InputSchema: json.RawMessage(
				`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			OutputSchema: json.RawMessage(
				`{"type":"object","properties":{"result":{"type":"string"}}}`),
		})
		if err != nil {
			return variantspec.ResolvedOverride{}, false
		}
		return variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{e}}, true

	case string(variantspec.DimTools):
		// A prune needs something to prune. A node declaring no tools, or exactly one, cannot express a
		// kept-subset that differs from what is there — `ToolSelection` would be empty, which the resolver
		// reads as "no tools dimension", so the engine would be asked nothing and would answer nothing.
		//
		// 🚫 The tempting shortcut is to report `not-applicable` here. That is a claim about the
		// customer's code and it is the one thing this data must never say by accident: a node with no
		// discovered tools may well have tools the frontend could not see.
		names := toolNames(n)
		if len(names) < 2 {
			return variantspec.ResolvedOverride{}, false
		}
		kept := append([]string(nil), names[:len(names)-1]...)
		sort.Strings(kept)
		return variantspec.ResolvedOverride{ToolSelection: kept}, true

	case string(variantspec.DimContext):
		policy := probePolicy(n.ContextAssembly.Policy)
		if policy == nil {
			return variantspec.ResolvedOverride{}, false
		}
		return variantspec.ResolvedOverride{Context: &registry.ContextEntry{
			VersionID: strings.Repeat("c", 64), Name: "probe", Policy: policy,
			Spec: registry.ContextSpec{Policy: policy.Name(), Params: json.RawMessage(`{}`)},
		}}, true

	case string(variantspec.DimMemory):
		st := registry.MemoryStrategyNamed("scratchpad")
		if st == nil {
			return variantspec.ResolvedOverride{}, false
		}
		return variantspec.ResolvedOverride{Memory: &registry.MemoryEntry{
			VersionID: strings.Repeat("e", 64), Name: "probe",
			Spec: registry.MemorySpec{Strategy: "scratchpad"}, Strategy: st,
		}}, true

	case string(variantspec.DimHarness):
		st := registry.HarnessStrategyNamed("react-loop")
		if st == nil {
			return variantspec.ResolvedOverride{}, false
		}
		return variantspec.ResolvedOverride{Harness: &registry.HarnessEntry{
			VersionID: strings.Repeat("h", 64), Name: "probe",
			Spec: registry.HarnessSpec{Strategy: "react-loop",
				Params: json.RawMessage(`{"max_turns":6,"stop_condition":"no-tool-call"}`)},
			Strategy: st,
		}}, true
	}
	return variantspec.ResolvedOverride{}, false
}

// probeModelID returns a model id that differs from the node's, so the engine is asked for a real
// rewrite rather than a no-op it can satisfy by doing nothing.
func probeModelID(current string) string {
	if current != "probe-model-a" {
		return "probe-model-a"
	}
	return "probe-model-b"
}

// probePolicy returns a builtin context policy that is not the node's current one and that the engine
// can actually materialize somewhere.
//
// 🔴 The second condition is what keeps the answer about the call site. Half the builtin policies are
// run-time strategies — a summarization, a retrieval — which `ContextMaterializes` reports false for in
// EVERY language, because the value does not exist until the program runs. Probing with one of those
// would produce a refusal that is true of the policy rather than of the node, and the projection would
// show `refused` on a call site that would happily accept a selection policy.
//
// A materializing policy is preferred; if this build has none, there is nothing to ask and the caller
// records an absence rather than probing with a question whose answer is predetermined.
func probePolicy(current string) registry.Policy {
	var fallback registry.Policy
	for _, p := range registry.BuiltinPolicies() {
		if p.Name() == current {
			continue
		}
		if transform.ContextMaterializes(p.Name()) {
			return p
		}
		if fallback == nil {
			fallback = p
		}
	}
	_ = fallback // deliberately unused: see the header — a non-materializing probe is not asked at all.
	return nil
}

// toolNames reads the node's discovered tools, preferring the P14 split and falling back to the frozen
// conflated slice — the same order every other consumer reads them in.
func toolNames(n discovery.IRNode) []string {
	if len(n.Tools) > 0 {
		out := make([]string, 0, len(n.Tools))
		for _, t := range n.Tools {
			out = append(out, t.Name)
		}
		return out
	}
	return append([]string(nil), n.ToolsSkills...)
}
