package nodeaxis

import (
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// language.go answers "what language is THIS node?" — per node, from a frontend, or not at all.
//
// # Why not `ir.Workflow.Language`
//
// That field is the workflow's label, and `workflowLanguageLabel` derives it from which frontends
// contributed: with one frontend it is that language, and with several it is `mixed`. A polyglot
// repository is the normal case — hermes-agent has Python call sites and Go tooling — so attributing a
// Python node's verdict to `go`, or to `mixed`, would send every downstream consumer to the wrong
// coverage row. The projection's whole value is that a refusal names the reader's own call site.
//
// # Why not the file extension
//
// Because that is a guess, and a guessed language makes a guessed verdict look computed. `.py` under a
// vendored directory the Python frontend does not walk is not a Python node this engine can rewrite; it
// is a node this build has nothing to say about, and `not-reported` says exactly that.
//
// # What is done instead
//
// Each registered frontend RE-DETECTS the tree and reports the node ids it found. A node id appearing in
// a frontend's index is that frontend saying "this call site is mine" — which is the same statement that
// produced the node in the first place. A node no frontend re-detects gets no language and therefore no
// verdicts, which is the honest answer when the tree on disk no longer matches the IR.

// languagesFor maps node id → language, from the frontends themselves.
func languagesFor(ir *discovery.IR, root string) map[string]string {
	out := map[string]string{}
	for _, lang := range transform.RegisteredLanguages() {
		for id := range indexFor(lang, root) {
			// First frontend to claim a node id wins, and a second claim is IGNORED rather than
			// overwriting: two frontends claiming one id is a discovery defect, and letting iteration
			// order decide which language a node gets would make the whole report non-deterministic.
			if _, taken := out[id]; !taken {
				out[id] = lang
			}
		}
	}
	// Only nodes the IR actually carries. An index entry for a call site the IR does not list is a
	// difference between the tree and the IR, and it is not this function's job to reconcile them.
	inIR := map[string]bool{}
	for _, n := range ir.Nodes {
		inIR[n.NodeID] = true
	}
	for id := range out {
		if !inIR[id] {
			delete(out, id)
		}
	}
	return out
}

// indexFor re-runs one language's detection over the tree, returning node ids.
//
// Errors are swallowed into an empty map ON PURPOSE, and this is the one place in this package where
// that is right: a frontend that cannot walk this tree has reported nothing, which is exactly the state
// "no language" encodes. Propagating the error would turn one unparseable file into a report with no
// verdicts at all, and a developer would be told nothing applies anywhere.
func indexFor(language, root string) map[string]bool {
	out := map[string]bool{}
	if language == "go" {
		sites, err := discovery.IndexGoCallSites(root, nil)
		if err != nil {
			return out
		}
		for id := range sites {
			out[id] = true
		}
		return out
	}
	sites, err := discovery.IndexSpanCallSites(root, language, nil)
	if err != nil {
		return out
	}
	for id := range sites {
		out[id] = true
	}
	return out
}

// dimensionNames is a compile-time reminder that ProbedAxes draws from the same vocabulary the engine
// dispatches on. If a dimension is added to variantspec and not probed here, the projection quietly
// stops reporting it — so the fence in nodeaxis_test.go compares the two sets.
var dimensionNames = []string{
	string(variantspec.DimModel), string(variantspec.DimPrompt), string(variantspec.DimSkills),
	string(variantspec.DimTools), string(variantspec.DimContext), string(variantspec.DimMemory),
	string(variantspec.DimHarness),
}
