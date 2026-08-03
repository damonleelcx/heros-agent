package launch

import (
	"context"
	"fmt"
	"testing"

	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// platformgraph_test.go pins the preference between the two graph sources.
//
// The failure mode worth testing for is not a crash — it is a SILENT DOWNGRADE: the platform holds a
// classified graph and serves the unlabelled opt-in structure instead. Nothing errors, the page renders,
// every region reads `unclassified`, and the customer concludes the classifier found nothing in their
// workflow. These tests make each branch of that choice explicit.

// stubOptIn is a PatternSource standing in for the allowlisted structure: nodes, no labels, everything
// unclassified — which is what workflowGraphSource can produce and all it can produce.
type stubOptIn struct {
	view patternclassifier.GraphView
	ok   bool
}

func (s stubOptIn) GraphView(_, _ string) (patternclassifier.GraphView, bool) { return s.view, s.ok }

// failingGraphStore returns a read error, standing in for a database outage.
type failingGraphStore struct{}

func (failingGraphStore) Put(context.Context, hostdiscovery.Graph) error { return nil }
func (failingGraphStore) Latest(context.Context, string, string) (hostdiscovery.Graph, bool, error) {
	return hostdiscovery.Graph{}, false, fmt.Errorf("database unreachable")
}

func labelledView() patternclassifier.GraphView {
	return patternclassifier.GraphView{
		WorkflowID: "wf", IRVersion: "1", TaxonomyVersion: patternclassifier.TaxonomyVersion,
		Nodes: []patternclassifier.ViewNode{{NodeID: "n1", Symbol: "Run"}},
		Regions: []patternclassifier.ViewRegion{{
			SubgraphID: "sg1", NodeIDs: []string{"n1"},
			Labels: []patternclassifier.ViewLabel{{Pattern: patternclassifier.PromptChaining}},
		}},
	}
}

func unlabelledView() patternclassifier.GraphView {
	return patternclassifier.GraphView{
		WorkflowID: "wf", IRVersion: "1", TaxonomyVersion: patternclassifier.TaxonomyVersion,
		Nodes:        []patternclassifier.ViewNode{{NodeID: "n1", Symbol: "Run"}},
		Unclassified: []patternclassifier.ViewRegion{{SubgraphID: "sg_n1", NodeIDs: []string{"n1"}}},
	}
}

// TestDiscoveredGraphWinsOverTheOptInStructure is the anti-downgrade assertion. Both sources describe
// the same workflow and only one of them can say what pattern it implements.
func TestDiscoveredGraphWinsOverTheOptInStructure(t *testing.T) {
	store := hostdiscovery.NewMemGraphStore()
	if err := store.Put(context.Background(), hostdiscovery.Graph{
		TenantID: "t1", WorkflowID: "wf", View: labelledView(),
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	src := newPlatformGraphSource(store, stubOptIn{view: unlabelledView(), ok: true})

	view, ok := src.GraphView("t1", "wf")
	if !ok {
		t.Fatal("GraphView returned no view although a discovered graph exists")
	}
	if len(view.Regions) == 0 {
		t.Fatal("served the UNLABELLED opt-in view while a classified graph was available — a silent " +
			"downgrade that renders as 'the classifier found nothing in your workflow'")
	}
}

// TestFallsBackToOptInWhenNoSourceWasPushed: the common case. A customer who never pushed source must
// keep exactly the view they had before this feature existed.
func TestFallsBackToOptInWhenNoSourceWasPushed(t *testing.T) {
	src := newPlatformGraphSource(hostdiscovery.NewMemGraphStore(), stubOptIn{view: unlabelledView(), ok: true})

	view, ok := src.GraphView("t1", "wf")
	if !ok {
		t.Fatal("a tenant with an opt-in structure and no pushed source lost their graph")
	}
	if len(view.Nodes) != 1 {
		t.Fatalf("view has %d nodes, want the opt-in structure's 1", len(view.Nodes))
	}
}

// TestFallsBackToOptInOnAReadError: serving the older, unlabelled view is a true statement about the
// workflow; serving nothing is not. The choice is stated in the adapter and asserted here.
func TestFallsBackToOptInOnAReadError(t *testing.T) {
	src := newPlatformGraphSource(failingGraphStore{}, stubOptIn{view: unlabelledView(), ok: true})

	if _, ok := src.GraphView("t1", "wf"); !ok {
		t.Fatal("a discovered-graph read error took down a workflow whose opt-in structure was available")
	}
}

// TestNothingFromEitherSourceIsNotFound: ok=false only when the tenant has neither.
func TestNothingFromEitherSourceIsNotFound(t *testing.T) {
	src := newPlatformGraphSource(hostdiscovery.NewMemGraphStore(), stubOptIn{ok: false})

	if _, ok := src.GraphView("t1", "wf"); ok {
		t.Fatal("GraphView reported a view for a tenant that has sent nothing at all")
	}
}

// TestNilOptInStillServesTheDiscoveredGraph: a deployment may mount discovery without the opt-in store.
func TestNilOptInStillServesTheDiscoveredGraph(t *testing.T) {
	store := hostdiscovery.NewMemGraphStore()
	if err := store.Put(context.Background(), hostdiscovery.Graph{
		TenantID: "t1", WorkflowID: "wf", View: labelledView(),
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	src := newPlatformGraphSource(store, nil)

	if _, ok := src.GraphView("t1", "wf"); !ok {
		t.Fatal("a discovered graph was not served because no opt-in source was mounted")
	}
	if _, ok := src.GraphView("t1", "other"); ok {
		t.Fatal("a workflow with no discovered graph and no opt-in source reported a view")
	}
}

// TestNilRunnerYieldsAnUntypedNilAdapter guards a specific Go trap with a specific consequence: a nil
// *Runner wrapped in an interface is NOT nil, so `s.sourceDiscovery == nil` in the handler would be
// false and every discover request would panic instead of answering 503.
func TestNilRunnerYieldsAnUntypedNilAdapter(t *testing.T) {
	if got := newDiscoveryAdapter(nil); got != nil {
		t.Fatalf("newDiscoveryAdapter(nil) = %#v, want an untyped nil — a typed nil in the interface "+
			"turns the handler's not-mounted 503 into a panic on every request", got)
	}
}

// TestCrossTenantGraphsDoNotLeak: workflow ids are chosen by customers and collide.
func TestCrossTenantGraphsDoNotLeak(t *testing.T) {
	store := hostdiscovery.NewMemGraphStore()
	if err := store.Put(context.Background(), hostdiscovery.Graph{
		TenantID: "tenant-a", WorkflowID: "agent", View: labelledView(),
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	src := newPlatformGraphSource(store, nil)

	if _, ok := src.GraphView("tenant-b", "agent"); ok {
		t.Fatal("tenant-b was served tenant-a's graph for a colliding workflow id")
	}
}
