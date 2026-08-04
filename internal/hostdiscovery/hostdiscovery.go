// Package hostdiscovery runs discovery and classification ON THE PLATFORM, over source a customer
// pushed, and stores the labelled graph that comes out.
//
// # The one input that was missing
//
// `discovery.Run` needs a checked-out tree. Every caller before this one was a developer's laptop or a
// demo binary pointed at a local clone, so the platform never called it — and that single gap is the
// reason internal/launch mounted six console surfaces nil. The eval board, the scorecard, proposals, the
// optimizer, the run monitor and the graph editor each render evidence produced by running something
// over source. The platform ran nothing, because it had nothing to run over.
//
// The visible symptom was narrower and more misleading than an empty page. `heros link --with-ir` let a
// customer send their workflow's SHAPE, so the pattern graph could be drawn — and every region on it read
// `unclassified`, permanently, because the classifier's inputs are prompt text and tool names and the
// wire allowlist refuses both by construction. internal/launch/workflowgraph.go says so at length and is
// right; what it describes is a ceiling, not a bug.
//
// Running discovery here removes the ceiling without touching the wire. Prompts and tool names become
// inputs to a computation on the platform's own side of the boundary: read from the extracted tree, held
// in memory for the duration of the job, and dropped with the tree when the job releases it. The run-link
// contract in internal/runlink is unchanged, byte for byte.
//
// # What is kept afterwards
//
// The CONCLUSION, not the evidence: the classifier's GraphView — node ids, symbols, model refs, policy
// names, tool counts, edges, labels, regions. The IR is not stored. The source is not stored beyond the
// pushed bundle the customer can delete. This is a deliberate line and it is the one that makes "we run
// discovery for you" different from "we keep your code".
//
// 🔴 Nothing in the schema enforces that line — `view_json` would accept a prompt without complaint.
// What enforces it is that this package projects from named classifier fields, so a prompt cannot arrive
// by being forgotten. That is the same discipline internal/runlink applies to the wire, applied to a
// store instead.
package hostdiscovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// Graph is one discovered, classified workflow as the platform stores it.
type Graph struct {
	TenantID        string
	WorkflowID      string
	SourceRevision  string
	IRVersion       string
	TaxonomyVersion string
	DiscoveredAt    time.Time
	LLMCalls        int
	View            patternclassifier.GraphView
}

// GraphStore records and reads discovered graphs.
//
// Every method returns an error, reads included — linkingest.WorkflowIRStore's lesson, for the same
// reason: a read failure and "this tenant has pushed no source" are different facts with different next
// actions, and a store that cannot tell them apart makes an outage look like a customer who never opted
// in.
type GraphStore interface {
	// Put records a graph, replacing any graph previously stored for the same revision.
	Put(ctx context.Context, g Graph) error
	// Latest returns the most recently discovered graph for a workflow. ok=false means NONE HAS BEEN
	// DISCOVERED — never a read failure, which is the error.
	Latest(ctx context.Context, tenantID, workflowID string) (Graph, bool, error)
}

// SkillResolution answers, for one IR, which of its tool bindings resolve against the skill registry.
//
// It is a function type rather than a *registry.Store so that a caller with no Postgres can still
// exercise this package — but note what it does NOT do: it does not make the resolver optional. Classify
// refuses a nil resolver outright, because "we could not reach the registry" reported as "this workflow
// binds no tools" is a silent false negative that surfaces as a confidently tool-free graph. A
// SkillResolution that fails must return an error, and Run propagates it.
type SkillResolution func(ctx context.Context, ir *discovery.IR) (patternclassifier.SkillResolver, error)

// RegistrySkills resolves against the REAL skill registry — the production wiring.
func RegistrySkills(store *registry.Store) SkillResolution {
	return func(ctx context.Context, ir *discovery.IR) (patternclassifier.SkillResolver, error) {
		return patternclassifier.LoadSkillResolver(ctx, store, ir)
	}
}

// Runner performs platform-side discovery.
type Runner struct {
	source sourceingest.Source
	skills SkillResolution
	graphs GraphStore
	now    func() time.Time
}

// NewRunner returns a platform-side discovery runner.
func NewRunner(src sourceingest.Source, skills SkillResolution, graphs GraphStore) (*Runner, error) {
	switch {
	case src == nil:
		return nil, fmt.Errorf("hostdiscovery: nil source")
	case skills == nil:
		return nil, fmt.Errorf("hostdiscovery: nil skill resolution")
	case graphs == nil:
		return nil, fmt.Errorf("hostdiscovery: nil graph store")
	}
	return &Runner{source: src, skills: skills, graphs: graphs, now: time.Now}, nil
}

// Run materializes the pushed source for ref, discovers the workflow, classifies it, and stores the
// resulting graph.
//
// It returns sourceingest.ErrNoSource unwrapped when the customer has pushed nothing, so a caller can
// branch on it with errors.Is and render "you have not pushed source for this revision" rather than an
// error page.
func (r *Runner) Run(ctx context.Context, ref sourceingest.Ref) (Graph, error) {
	if err := ref.Validate(); err != nil {
		return Graph{}, err
	}

	mat, err := r.source.Materialize(ctx, ref)
	if err != nil {
		if errors.Is(err, sourceingest.ErrNoSource) {
			return Graph{}, err // preserved for errors.Is at the call site
		}
		return Graph{}, fmt.Errorf("hostdiscovery: materialize %s: %w", ref, err)
	}
	// The customer's source leaves this machine when the job ends. Not a finalizer, not a sweep: the
	// defer is the retention policy, and hostdiscovery_test.go asserts the directory is gone afterwards.
	defer mat.Release()

	ir, err := r.discover(ctx, mat.Dir, ref)
	if err != nil {
		return Graph{}, err
	}

	// The classifier's only I/O, done before the pure detectors run so that an unreachable registry
	// fails the job instead of quietly producing a workflow with no tool use in it.
	resolver, err := r.skills(ctx, ir)
	if err != nil {
		return Graph{}, fmt.Errorf("hostdiscovery: resolving skills for %s: %w", ref, err)
	}

	// Fallback is deliberately nil: with no model configured, the ambiguous residue stays honestly
	// unclassified, which patternclassifier documents as a first-class state. A default LLM here would
	// make every deployment quietly consult a model, and a plausible label is indistinguishable from a
	// correct one on a page nobody can check.
	res, err := patternclassifier.Classify(ctx, ir, patternclassifier.Options{Skills: resolver})
	if err != nil {
		return Graph{}, fmt.Errorf("hostdiscovery: classify %s: %w", ref, err)
	}

	view := patternclassifier.BuildGraphView(ir, res)
	// The IR's workflow id is a discovery-time suggestion; the ref is what the customer and every other
	// table call this workflow. Disagreeing here would store a graph under an id no console asks for.
	view.WorkflowID = ref.WorkflowID

	g := Graph{
		TenantID:        ref.TenantID,
		WorkflowID:      ref.WorkflowID,
		SourceRevision:  ref.SourceRevision,
		IRVersion:       view.IRVersion,
		TaxonomyVersion: view.TaxonomyVersion,
		DiscoveredAt:    r.now().UTC(),
		LLMCalls:        view.LLMCalls,
		View:            view,
	}
	if err := r.graphs.Put(ctx, g); err != nil {
		return Graph{}, fmt.Errorf("hostdiscovery: store graph for %s: %w", ref, err)
	}
	return g, nil
}

// IR re-derives a workflow's full Workflow IR from the pushed snapshot.
//
// # Why this recomputes instead of reading a stored IR
//
// The IR carries prompt text, I/O-contract schemas and in-scope symbol sets. Storing it would undo the
// line this package draws — keep the CONCLUSION, not the evidence — and it would do so quietly, since a
// JSONB column accepts a prompt without complaint. The pushed bundle is the one artifact that is
// retained, deliberately and visibly, and the customer can delete it. So the IR is DERIVED from it on
// demand and lives only for the duration of the request.
//
// The honest cost: this re-parses the repository on every call, which for the graph editor means once
// per page load. That is seconds on a real workflow and it is the right trade while the alternative is
// a durable store of customer prompt text. If it ever needs to be faster, the fix is a bounded in-memory
// cache keyed by (tenant, workflow, revision) with an explicit TTL — not a table.
func (r *Runner) IR(ctx context.Context, ref sourceingest.Ref) (*discovery.IR, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	mat, err := r.source.Materialize(ctx, ref)
	if err != nil {
		if errors.Is(err, sourceingest.ErrNoSource) {
			return nil, err
		}
		return nil, fmt.Errorf("hostdiscovery: materialize %s: %w", ref, err)
	}
	defer mat.Release()
	return r.discover(ctx, mat.Dir, ref)
}

// WithSource materializes the snapshot, derives its IR, and hands BOTH to fn for the lifetime of the
// extracted tree.
//
// 🔴 IR above cannot serve a caller that needs the FILES as well as the shape: it releases the
// materialized directory before returning, so the path it parsed is gone by the time the caller has the
// IR. The codemod needs exactly that pair — an IR to resolve the change against, and the tree to
// rewrite — and the alternative shapes are both worse. Returning the directory would hand its lifetime
// to a caller who has no idea it is a temp extraction; materializing twice would parse the repository
// twice and, worse, could get two DIFFERENT trees if a push landed in between, so the diff would be
// generated against a tree the IR does not describe.
//
// The tree is read-only as far as this contract is concerned: fn must not write into dir. transform
// .Generate reads it and returns the rewritten bytes rather than mutating, which is what makes that
// safe.
func (r *Runner) WithSource(ctx context.Context, ref sourceingest.Ref, fn func(dir string, ir *discovery.IR) error) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	mat, err := r.source.Materialize(ctx, ref)
	if err != nil {
		if errors.Is(err, sourceingest.ErrNoSource) {
			return err
		}
		return fmt.Errorf("hostdiscovery: materialize %s: %w", ref, err)
	}
	defer mat.Release()

	ir, err := r.discover(ctx, mat.Dir, ref)
	if err != nil {
		return err
	}
	return fn(mat.Dir, ir)
}

// discover runs the discovery pipeline over an extracted tree.
func (r *Runner) discover(ctx context.Context, dir string, ref sourceingest.Ref) (*discovery.IR, error) {
	// Discovery is CPU-bound and does not take a context; check for cancellation before starting rather
	// than pretending we can interrupt it. A caller who has already given up should not pay for a full
	// parse of a repository.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts := discovery.Options{
		Repo:       dir,
		WorkflowID: ref.WorkflowID,
		CommitSHA:  ref.SourceRevision,
		ConfigPath: llmEvalConfigPath(dir),
	}
	out, err := discovery.Run(opts)
	if err != nil {
		return nil, fmt.Errorf("hostdiscovery: discovery over %s: %w", ref, err)
	}
	ir := out.IR
	return &ir, nil
}

// llmEvalConfigPath returns the workflow's declared config if the tree carries one, else "".
//
// Absence is valid — discovery treats a missing llm-eval.yaml as "nothing declared" and reports it in
// detections_by_source. Passing a path that does not exist would be equivalent, but checking here means
// the ONE case that is a real fault (a config that exists and is malformed) still fails loud, instead of
// being indistinguishable from a repository that never had one.
func llmEvalConfigPath(dir string) string {
	for _, name := range []string{"llm-eval.yaml", "llm-eval.yml"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
