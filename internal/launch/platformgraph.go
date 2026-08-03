package launch

import (
	"context"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// platformgraph.go serves the pattern graph from the BEST source this tenant has given us, and says
// which one that was.
//
// # Two sources, deliberately not merged
//
// A tenant may have sent an allowlisted structure (`heros link --with-ir`, migration 0021), or pushed
// source the platform discovered itself (migration 0022), or both, or neither. The two are not
// interchangeable and are not folded together:
//
//   - The opt-in structure is what the CUSTOMER CHOSE TO SEND. Reviewable field by field. It can be
//     drawn, and it can never be LABELLED, because the classifier reads prompt text and tool names and
//     the wire allowlist refuses both by construction. Every region on it is `unclassified` — correctly,
//     permanently. workflowGraphSource is that view and its comment explains the ceiling at length.
//   - The platform-discovered graph is the CLASSIFIER'S OUTPUT over source the customer pushed. It
//     carries real labels because the classifier's inputs existed on this side of the boundary.
//
// This source prefers the second when it exists. That is a preference for MORE INFORMATION, not for a
// different opinion: both describe the same workflow, and only one of them can answer "what pattern is
// this". When only the opt-in structure exists, the unlabelled view is served exactly as before — a
// customer who has not pushed source loses nothing they had.
//
// 🔴 A fallback that is silently WORSE is a lie the console cannot see through. The two views differ in
// a way that matters to a reader — one says "unclassified" because nothing could be determined, the
// other because nothing was ATTEMPTED — and `GraphView` has no field for provenance. So the difference
// is carried where the console already reads it: a platform-discovered view reports its real LLMCalls
// and its labelled regions, while the opt-in view continues to report zero calls and all-unclassified
// regions. An operator comparing two workflows sees the difference; a future widening of PatternSource
// to carry provenance explicitly is the real fix and is a contract change, not a line in this file.
type platformGraphSource struct {
	// discovered is the labelled graph from platform-side discovery. Preferred.
	discovered hostdiscovery.GraphStore
	// optIn is the allowlisted structure the customer transmitted. Fallback.
	optIn api.PatternSource
}

// GraphView returns the richest view this tenant has for a workflow.
//
// ok=false means NEITHER source has anything — the tenant has not pushed source and has not sent a
// structure. As in workflowGraphSource, a read failure also returns false, because PatternSource has
// nowhere to put an error; that limitation is inherited, not introduced, and it is stated rather than
// hidden. Widening the interface is the fix and does not belong in this commit.
func (p platformGraphSource) GraphView(tenantID, workflowID string) (patternclassifier.GraphView, bool) {
	if p.discovered != nil {
		// A short timeout: this is a page load, and a console that hangs on a slow query is worse than
		// one that falls back to the structure it already has.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		g, ok, err := p.discovered.Latest(ctx, tenantID, workflowID)
		if err == nil && ok {
			return g.View, true
		}
		// Deliberately fall through on BOTH a miss and a read error. A miss is the common case (the
		// tenant never pushed source) and the opt-in structure is the right answer for it. A read error
		// is rarer and the choice is between serving the older, unlabelled view and serving nothing;
		// the unlabelled view is a true statement about the workflow, so it wins.
	}
	if p.optIn == nil {
		return patternclassifier.GraphView{}, false
	}
	return p.optIn.GraphView(tenantID, workflowID)
}

// newPlatformGraphSource returns the pattern-graph adapter over both sources. Either may be nil.
func newPlatformGraphSource(discovered hostdiscovery.GraphStore, optIn api.PatternSource) api.PatternSource {
	return platformGraphSource{discovered: discovered, optIn: optIn}
}

// discoveryAdapter presents a hostdiscovery.Runner as the API's SourceDiscovery.
//
// The adapter lives here rather than as a method on Runner so that internal/hostdiscovery does not
// import internal/api. The domain package does not need to know an HTTP surface exists, and the day it
// does is the day the two cannot be tested apart.
type discoveryAdapter struct{ runner *hostdiscovery.Runner }

// Discover runs discovery and summarises the result.
//
// The error is returned unwrapped when it is ErrNoSource so the handler's errors.Is check reaches it —
// wrapping would still satisfy errors.Is, but Run already preserves the sentinel deliberately and there
// is nothing to add here that the handler does not already say better.
func (d discoveryAdapter) Discover(ctx context.Context, ref sourceingest.Ref) (api.DiscoverySummary, error) {
	g, err := d.runner.Run(ctx, ref)
	if err != nil {
		return api.DiscoverySummary{}, err
	}
	// Labelled regions are counted from the view's Regions, which by construction hold only regions a
	// detector named. Counting node-level labels too would double-count a node-scoped label that is
	// already represented as a region of one.
	return api.DiscoverySummary{
		WorkflowID:     g.WorkflowID,
		SourceRevision: g.SourceRevision,
		Nodes:          len(g.View.Nodes),
		Edges:          len(g.View.Edges),
		Labelled:       len(g.View.Regions),
		Unclassified:   len(g.View.Unclassified),
		LLMCalls:       g.LLMCalls,
	}, nil
}

// editorIRSource serves the graph editor's read model by re-deriving the IR from the pushed snapshot.
//
// # Why the editor gets a recomputed IR and not a stored one
//
// The editor needs the FULL Workflow IR — io_contracts, node kinds, edges with provenance — because its
// whole job is to validate a candidate reordering against the typed contracts. That is strictly more
// than the classified GraphView the platform stores, and it is more ON PURPOSE: the IR carries prompt
// text, and internal/hostdiscovery keeps the conclusion rather than the evidence. So the IR is derived
// from the retained bundle per request. See hostdiscovery.Runner.IR for the cost and the cache that
// would fix it if it ever mattered.
//
// # Why the revision comes from the stored graph
//
// GraphEditorSource asks for a workflow, not a revision, so something has to choose one. It is the
// revision of the LATEST DISCOVERED GRAPH — the same snapshot the pattern graph is drawn from — so the
// editor and the graph are looking at one tree. Choosing "the newest pushed bundle" instead would let
// the two disagree whenever a push has landed but discovery has not run, and a user would be editing a
// workflow whose picture on the next tab is of different code.
type editorIRSource struct {
	graphs hostdiscovery.GraphStore
	runner *hostdiscovery.Runner
}

// IR returns the workflow's IR for one tenant, or ok=false when this tenant has no discovered graph.
func (e editorIRSource) IR(tenantID, workflowID string) (*discovery.IR, bool) {
	// Longer than the graph read's 5s: this one re-parses a repository rather than reading a row.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	g, ok, err := e.graphs.Latest(ctx, tenantID, workflowID)
	if err != nil || !ok {
		return nil, false
	}
	ir, err := e.runner.IR(ctx, sourceingest.Ref{
		TenantID: tenantID, WorkflowID: workflowID, SourceRevision: g.SourceRevision,
	})
	if err != nil {
		// Includes the case where the customer DELETED their snapshot after it was discovered: the
		// graph survives (it is a conclusion, and they did not retract that), and the editor cannot
		// work because it needs the source. false renders as "no such workflow", which is not quite
		// right — the honest answer is "the source this was discovered from is no longer here" — and
		// GraphEditorSource has nowhere to put it. Inherited limitation, same as PatternSource's.
		return nil, false
	}
	return ir, true
}

// newEditorIRSource returns the editor's read model, or nil when either half is missing.
func newEditorIRSource(graphs hostdiscovery.GraphStore, runner *hostdiscovery.Runner) api.GraphEditorSource {
	if graphs == nil || runner == nil {
		return nil // untyped nil — see newDiscoveryAdapter
	}
	return editorIRSource{graphs: graphs, runner: runner}
}

// newDiscoveryAdapter returns the API-facing discovery runner, or nil when there is no runner.
func newDiscoveryAdapter(runner *hostdiscovery.Runner) api.SourceDiscovery {
	if runner == nil {
		// A typed nil in an interface is not nil, and the handler's `s.sourceDiscovery == nil` check
		// would pass while every call panicked. Returning an untyped nil is the difference between a
		// 503 and a 500 on every deployment that mounts snapshots without a runner.
		return nil
	}
	return discoveryAdapter{runner: runner}
}
