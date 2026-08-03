package hostdiscovery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// hostdiscovery_test.go proves the claim this package was written to make: given source, the platform
// produces a LABELLED graph.
//
// That is the whole increment. The opt-in `--with-ir` path can draw nodes and edges and can never label
// them, because the classifier's inputs are prompt text and tool names and the wire allowlist refuses
// both. TestRunProducesLabelsTheOptInPathCannot is the assertion that this path clears that ceiling — if
// it ever produces an all-`unclassified` view again, the feature has silently reverted to the thing it
// replaced, and nothing else in the suite would notice.

// --- fixture plumbing ----------------------------------------------------------------------------

// memBundleStore is an in-memory sourceingest.BundleStore.
type memBundleStore struct{ data map[string][]byte }

func newMemBundleStore() *memBundleStore { return &memBundleStore{data: map[string][]byte{}} }

func (m *memBundleStore) key(r sourceingest.Ref) string {
	return r.TenantID + "\x00" + r.WorkflowID + "\x00" + r.SourceRevision
}

func (m *memBundleStore) Put(_ context.Context, ref sourceingest.Ref, data []byte) error {
	m.data[m.key(ref)] = append([]byte(nil), data...)
	return nil
}

func (m *memBundleStore) Open(_ context.Context, ref sourceingest.Ref) (io.ReadCloser, error) {
	b, ok := m.data[m.key(ref)]
	if !ok {
		return nil, sourceingest.ErrNoSource
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memBundleStore) Delete(_ context.Context, ref sourceingest.Ref) error {
	delete(m.data, m.key(ref))
	return nil
}

// tarGz packs files (path -> contents) into a gzipped tar.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o640, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// chainingRepo is a draft-then-revise workflow — the shape the Prompt Chaining detector recognises.
//
// The DATA DEPENDENCY is what makes it a chain, and it took a failing test to get right: two calls
// sitting one after the other are not a chain, because internal/discovery/graph.go emits a `data` edge
// only when a value bound by one call appears inside a later call's arguments. Two independent calls
// produce two unconnected nodes, the detector correctly declines, and the graph comes back unlabelled —
// which is exactly what the first version of this fixture proved.
//
// Deliberately a REAL SDK call against a registry-known signature: a fixture discovery cannot detect
// would make every assertion here pass vacuously over an empty graph.
func chainingRepo() map[string]string {
	return map[string]string{
		"go.mod": "module example.com/agent\n\ngo 1.24\n",
		"agent.go": `package agent

import "github.com/anthropics/anthropic-sdk-go"

// Run drafts, then revises the draft: the second call consumes the first call's result.
func Run(c *anthropic.Client) {
	draft := c.Messages.New(nil, anthropic.MessageNewParams{})
	c.Messages.New(nil, anthropic.MessageNewParams{Messages: draft})
}
`,
	}
}

// staticSkills resolves every binding, standing in for the registry without needing Postgres. It never
// returns an error, which is exactly what makes TestSkillResolutionFailureFailsTheJob a separate test:
// the interesting behaviour is the failing resolver, not this one.
func staticSkills(names ...string) SkillResolution {
	return func(_ context.Context, _ *discovery.IR) (patternclassifier.SkillResolver, error) {
		return patternclassifier.NewStaticSkillResolver(names...), nil
	}
}

// newRunner wires a runner over an in-memory bundle store preloaded with files.
func newRunner(t *testing.T, files map[string]string, skills SkillResolution) (*Runner, *MemGraphStore, sourceingest.Ref) {
	t.Helper()
	ref := sourceingest.Ref{TenantID: "t1", WorkflowID: "wf-agent", SourceRevision: "rev-abc"}
	bundles := newMemBundleStore()
	if err := bundles.Put(context.Background(), ref, tarGz(t, files)); err != nil {
		t.Fatalf("put bundle: %v", err)
	}
	src, err := sourceingest.NewBundleSource(bundles, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	graphs := NewMemGraphStore()
	r, err := NewRunner(src, skills, graphs)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	return r, graphs, ref
}

// --- the tests -----------------------------------------------------------------------------------

func TestRunDiscoversAndStoresAGraph(t *testing.T) {
	r, graphs, ref := newRunner(t, chainingRepo(), staticSkills())

	g, err := r.Run(context.Background(), ref)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(g.View.Nodes) == 0 {
		t.Fatal("discovery found no nodes in a repo with two SDK calls — the fixture or the frontend is broken, " +
			"and every other assertion here would pass vacuously on an empty graph")
	}
	if g.WorkflowID != ref.WorkflowID {
		t.Errorf("stored workflow id = %q, want %q — a graph stored under a discovery-time id is one no "+
			"console asks for", g.WorkflowID, ref.WorkflowID)
	}
	if g.SourceRevision != ref.SourceRevision {
		t.Errorf("stored revision = %q, want %q", g.SourceRevision, ref.SourceRevision)
	}
	if g.TaxonomyVersion != patternclassifier.TaxonomyVersion {
		t.Errorf("taxonomy version = %q, want %q — labels mean what the taxonomy in force when they were "+
			"computed said they meant", g.TaxonomyVersion, patternclassifier.TaxonomyVersion)
	}

	stored, ok, err := graphs.Latest(context.Background(), ref.TenantID, ref.WorkflowID)
	if err != nil || !ok {
		t.Fatalf("Latest after Run: ok=%v err=%v — the graph was returned but not stored", ok, err)
	}
	if len(stored.View.Nodes) != len(g.View.Nodes) {
		t.Errorf("stored view has %d nodes, returned view has %d", len(stored.View.Nodes), len(g.View.Nodes))
	}
}

// TestRunProducesLabelsTheOptInPathCannot is the point of the increment.
//
// internal/launch/workflowgraph.go — the adapter over the OPT-IN structure — sets every node's Labels to
// an empty slice and files every node under Unclassified, and its comment explains that this is forced:
// the classifier reads prompts and tool names, and neither crosses the wire. Here the platform holds the
// source, so the classifier gets its inputs. If this assertion fails, the platform is back to drawing
// unlabelled dots and the extra machinery is buying nothing.
func TestRunProducesLabelsTheOptInPathCannot(t *testing.T) {
	r, _, ref := newRunner(t, chainingRepo(), staticSkills())

	g, err := r.Run(context.Background(), ref)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var labels []string
	for _, n := range g.View.Nodes {
		for _, l := range n.Labels {
			labels = append(labels, string(l.Pattern))
		}
	}
	labels = append(labels, regionLabels(g.View.Regions)...)

	if len(labels) == 0 {
		t.Fatalf("the classified graph carries NO labels (%d nodes, %d regions, %d unclassified) — "+
			"this is exactly the state the opt-in path was already stuck in, so platform-side discovery "+
			"bought nothing", len(g.View.Nodes), len(g.View.Regions), len(g.View.Unclassified))
	}
	t.Logf("labels produced from pushed source: %v", labels)
}

func regionLabels(regions []patternclassifier.ViewRegion) []string {
	var out []string
	for _, r := range regions {
		for _, l := range r.Labels {
			out = append(out, string(l.Pattern))
		}
	}
	return out
}

// TestRunReportsLLMCallsHonestly: with no fallback model configured, the classifier must not consult one.
// The count is stored so the console can say "fully rule-covered" only when it is true.
func TestRunConsultsNoModelWithoutAFallback(t *testing.T) {
	r, _, ref := newRunner(t, chainingRepo(), staticSkills())
	g, err := r.Run(context.Background(), ref)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if g.LLMCalls != 0 {
		t.Errorf("llm_calls = %d, want 0 — no fallback model is configured, so no model may be consulted", g.LLMCalls)
	}
}

// TestRunReleasesTheSourceTree is the retention rule made a test. A customer's source lives on the
// platform's disk for the duration of the job and not one moment longer; the defer in Run is the policy,
// and a policy nothing checks is a comment.
func TestRunReleasesTheSourceTree(t *testing.T) {
	ref := sourceingest.Ref{TenantID: "t1", WorkflowID: "wf-agent", SourceRevision: "rev-abc"}
	bundles := newMemBundleStore()
	if err := bundles.Put(context.Background(), ref, tarGz(t, chainingRepo())); err != nil {
		t.Fatalf("put bundle: %v", err)
	}
	scratch := t.TempDir()
	src, err := sourceingest.NewBundleSource(bundles, scratch)
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	r, err := NewRunner(src, staticSkills(), NewMemGraphStore())
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	if _, err := r.Run(context.Background(), ref); err != nil {
		t.Fatalf("run: %v", err)
	}
	left, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatalf("read scratch: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("%d extracted tree(s) left on disk after the job finished: %v — the customer's source "+
			"outlived the job that needed it", len(left), left)
	}
}

// TestRunWithoutPushedSourceIsErrNoSource: "this customer has not pushed source" must stay distinguishable
// from "this platform is broken" all the way to the caller, because they render differently and page
// different people.
func TestRunWithoutPushedSourceIsErrNoSource(t *testing.T) {
	src, err := sourceingest.NewBundleSource(newMemBundleStore(), t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	r, err := NewRunner(src, staticSkills(), NewMemGraphStore())
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	_, err = r.Run(context.Background(), sourceingest.Ref{TenantID: "t", WorkflowID: "w", SourceRevision: "r"})
	if !errors.Is(err, sourceingest.ErrNoSource) {
		t.Fatalf("err = %v, want ErrNoSource to survive to the caller", err)
	}
}

// TestSkillResolutionFailureFailsTheJob guards the false negative patternclassifier warns about: a
// registry we cannot read must NOT be reported as a workflow that binds no tools. The job fails instead.
func TestSkillResolutionFailureFailsTheJob(t *testing.T) {
	failing := func(_ context.Context, _ *discovery.IR) (patternclassifier.SkillResolver, error) {
		return nil, fmt.Errorf("registry unreachable")
	}
	r, graphs, ref := newRunner(t, chainingRepo(), failing)

	if _, err := r.Run(context.Background(), ref); err == nil {
		t.Fatal("an unreachable skill registry produced a graph; it must fail the job, because a " +
			"workflow reported as tool-free on a registry outage is a silent false negative")
	}
	if _, ok, _ := graphs.Latest(context.Background(), ref.TenantID, ref.WorkflowID); ok {
		t.Error("a graph was STORED despite the registry failure — the next reader cannot tell it apart " +
			"from a good one")
	}
}

func TestNewRunnerRefusesMissingCollaborators(t *testing.T) {
	src, err := sourceingest.NewBundleSource(newMemBundleStore(), t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	if _, err := NewRunner(nil, staticSkills(), NewMemGraphStore()); err == nil {
		t.Error("NewRunner accepted a nil source")
	}
	if _, err := NewRunner(src, nil, NewMemGraphStore()); err == nil {
		t.Error("NewRunner accepted a nil skill resolution — Classify would refuse it later, in a worse place")
	}
	if _, err := NewRunner(src, staticSkills(), nil); err == nil {
		t.Error("NewRunner accepted a nil graph store")
	}
}
