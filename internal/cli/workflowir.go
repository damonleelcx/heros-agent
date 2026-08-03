package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// workflowir.go projects a discovered IR onto the opt-in egress payload.
//
// # Why the projection lives here and not in internal/runlink
//
// `runlink` is the boundary package and it does not import `discovery` — deliberately. If it did, the
// wire types and the rich internal representation would sit in one package, and the very first
// convenience helper anybody wrote would be the one that marshals the rich type "just for a test". The
// separation is what makes "a new IR field is absent from the wire until somebody adds it" a property of
// the code rather than a habit.
//
// So: `runlink` owns the wire shape and the allowlist; this file owns the reading of named fields off an
// `discovery.IR` and nothing else. It cannot serialise an IR node even if it wanted to.

// buildWorkflowIR reads the allowlisted fields off a discovered IR.
//
// 🔴 Read the fields it does NOT touch: `node.Prompt` (the inline prompt text and its variable names),
// `node.IOContract` (schemas lifted out of source, literals and all), `node.CallSite.InScope` (a lexical
// dump), `node.ToolsSkills` as NAMES (a count crosses instead), `node.CallSite.ASTPath`. Each was one
// field access away and each is deliberately absent. That is the allowlist discipline working: the
// payload is CONSTRUCTED, so anything not written here cannot be sent.
// BuildWorkflowIR is exported for internal/clilink, which owns the network half of linking.
func BuildWorkflowIR(ir *discovery.IR) runlink.WorkflowIRPayload {
	p := runlink.WorkflowIRPayload{
		ContractVersion: runlink.WorkflowIRContractVersion,
		WorkflowID:      ir.Workflow.ID,
		SourceRevision:  ir.Workflow.Repo.CommitSHA,
		IRVersion:       ir.IRVersion,
		Nodes:           make([]runlink.WireIRNode, 0, len(ir.Nodes)),
		Edges:           make([]runlink.WireIREdge, 0, len(ir.Edges)),
	}
	for _, n := range ir.Nodes {
		p.Nodes = append(p.Nodes, runlink.WireIRNode{
			NodeID:        n.NodeID,
			Symbol:        n.CallSite.Symbol,
			File:          n.CallSite.File,
			LineStart:     n.CallSite.LineStart,
			LineEnd:       n.CallSite.LineEnd,
			Provider:      n.Model.Provider,
			ModelID:       n.Model.ModelID,
			ContextPolicy: n.ContextAssembly.Policy,
			ToolCount:     len(n.ToolsSkills),
		})
	}
	for _, e := range ir.Edges {
		// Provenance/Confidence/Signal are on the IR edge and are NOT read: an inferred edge's confidence
		// is a fact about our analysis, not about the customer's workflow, and the graph draws neither.
		p.Edges = append(p.Edges, runlink.WireIREdge{From: e.FromNodeID, To: e.ToNodeID, Kind: e.Kind})
	}
	return p
}

// loadIRForLink reads an IR from disk and refuses one that does not describe the run being linked.
//
// # Why the match is checked rather than trusted
//
// `--with-ir` takes a path, so the two artifacts are chosen independently and nothing stops a developer
// pointing at last week's `ir.json`. Uploading it would attach one repository's shape to another
// repository's measurements, and the console would render the mismatch as fact — a graph that looks
// right and describes something else. There is no recovering from that downstream, so it is refused
// here, where both values are in hand and the message can name them.
// LoadIRForLink is exported for internal/clilink.
func LoadIRForLink(path, wantWorkflow, wantRevision string) (*discovery.IR, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, invalidConfig("link: no IR at " + path +
				" — `heros discover -out <path>` writes one, and --with-ir transmits it")
		}
		return nil, operational("link: read IR", err)
	}
	var ir discovery.IR
	if err := json.Unmarshal(b, &ir); err != nil {
		return nil, invalidConfig("link: " + path + " is not a valid Workflow IR: " + err.Error())
	}
	if ir.Workflow.ID != wantWorkflow {
		return nil, invalidConfig(fmt.Sprintf(
			"link: %s describes workflow %q, but run %s was measured on %q — transmitting it would attach "+
				"one repository's shape to another's measurements. Re-run `heros discover -workflow-id %s`.",
			path, ir.Workflow.ID, wantWorkflow, wantWorkflow, wantWorkflow))
	}
	if ir.Workflow.Repo.CommitSHA != wantRevision {
		return nil, invalidConfig(fmt.Sprintf(
			"link: %s was discovered at revision %s, but the run was measured at %s. A graph drawn at one "+
				"revision and scored at another is not a picture of either.",
			path, short12(ir.Workflow.Repo.CommitSHA), short12(wantRevision)))
	}
	return &ir, nil
}

func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
