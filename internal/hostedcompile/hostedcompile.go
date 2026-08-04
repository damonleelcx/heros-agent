// Package hostedcompile turns a stored proposal into a REVIEWABLE SOURCE DIFF on the platform.
//
// # What was missing
//
// internal/proposalgen emits candidate Variant Specs and records them `unbuilt` with no diff, because
// it never compiles them. That left the product's actual output missing: ADR-001 makes a proposal "an
// AST-level codemod delivered as a reviewable diff", and a recommendation with no diff asks a reviewer
// to trust a config hash. Everything needed was already here and unconnected — the retained source
// snapshot, the re-derivable IR, the registries, and the transform engine.
//
// # Two things this fixes beyond producing the diff
//
// The CONFIG HASH becomes real. proposalgen hashes the candidate's spec to get an id, and says in its
// own comment that this is not a variantspec config_hash: it identifies the candidate and asserts
// nothing about whether the candidate RESOLVES. Compiling resolves the spec against the IR and the
// registries, which mints the genuine hash — the one the verdict ingest is keyed by, and therefore the
// one a customer's CI reports against. A proposal compiled here can be verified; one that was never
// compiled carries an id the gate cannot match.
//
// The REFUSALS become visible. transform.Generate declines changes it cannot make safely, and
// proposal.Compile turns those into a BuildRefused status with the transform's own sentence. Without a
// compile step those refusals never happen, so a change the engine would have declined sits on the
// surface indistinguishable from one it stands behind.
//
// # What it still does not do
//
// It does not BUILD. See gate.go: the deployed image carries no toolchain, so the strongest gate
// available is an in-process parse, and a parse is reported as `unbuilt` with the reason rather than as
// `built`. That keeps delivery gated exactly where ADR-001 puts it while making the diff reviewable.
package hostedcompile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/sourceingest"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// SourceRunner materializes a tenant's snapshot and derives its IR, holding both for the callback.
type SourceRunner interface {
	WithSource(ctx context.Context, ref sourceingest.Ref, fn func(dir string, ir *discovery.IR) error) error
}

// BlobStore holds the diff bytes and the candidate specs. The proposal row stores only content hashes
// — 0012's and 0031's CHECKs require 64-hex or NULL there, so an accidental inline payload is refused
// by the database rather than by review.
type BlobStore interface {
	Put(ctx context.Context, data []byte) (string, error)
	Get(ctx context.Context, contentHash string) ([]byte, error)
}

// Store is the proposal read/write this package needs.
type Store interface {
	ForWorkflow(ctx context.Context, tenantID, workflowID string) ([]proposalstore.Scored, error)
	Put(ctx context.Context, r proposalstore.Record) error
}

// Compiler compiles a tenant's uncompiled proposals for one workflow.
type Compiler struct {
	Runner     SourceRunner
	Store      Store
	Blobs      BlobStore
	Registries variantspec.Registries
	// Sandbox is the isolate the customer's build runs inside. Nil means this deployment has no isolate,
	// and the build gate then reports unavailable — it does NOT build on the host.
	Sandbox *sandbox.Sandbox
	// GoBin is the pinned toolchain. Empty selects `go` from PATH, which the gate reports as unavailable
	// when it is not there.
	GoBin string
	// Bounds caps what a customer's build may consume inside the isolate. Zero takes sandbox's defaults.
	Bounds sandbox.ResourceBounds
	// Now is injectable so a pass is deterministic under test.
	Now func() time.Time
}

// gateFor selects the strongest gate this deployment can actually run.
//
// 🔴 STRONGEST AVAILABLE, and the ladder is the point rather than a convenience. A compile is the claim
// ADR-001 hangs delivery on and it needs a toolchain and an isolate; a parse needs neither and proves
// strictly less. Choosing between them by what is present — instead of assuming one — is what stops a
// deployment silently reporting `built` from a gate that only parsed, which is the single failure this
// whole area is written against.
//
// Both gates report UNAVAILABLE rather than a verdict when they cannot run, so falling from one to the
// other never converts "we could not judge this" into "it does not compile".
func (c *Compiler) gateFor(language, root string) proposal.BuildChecker {
	if c.Sandbox != nil {
		return SandboxGate{
			Language: language, Root: root, Sandbox: c.Sandbox,
			GoBin: c.GoBin, Bounds: c.Bounds,
		}
	}
	return ParseGate{Language: language}
}

// Outcome is what compiling one proposal produced.
type Outcome struct {
	ProposalID string `json:"proposal_id"`
	// Status is the proposal's build status after this pass: built | build_failed | refused | unbuilt.
	Status string `json:"status"`
	// ConfigHash is the RESOLVED hash — the real one, replacing the candidate identity proposalgen
	// recorded. Empty when the candidate was refused before it resolved.
	ConfigHash string `json:"config_hash,omitempty"`
	DiffHash   string `json:"diff_hash,omitempty"`
	// Detail is what the gate or the transform said. Always populated: a proposal that came out of this
	// pass without a diff must say why.
	Detail string `json:"detail"`
}

// Result is one compile pass over a workflow.
type Result struct {
	TenantID   string    `json:"tenant_id"`
	WorkflowID string    `json:"workflow_id"`
	State      State     `json:"state"`
	Detail     string    `json:"detail"`
	Outcomes   []Outcome `json:"outcomes,omitempty"`
}

// State is why a pass produced what it did. A closed set, for the reason proposalgen's is: "you have
// pushed no source", "everything is already compiled" and "the codemod refused all three" are three
// different sentences with three different next actions.
type State string

const (
	// StateCompiled: at least one proposal was compiled (whatever each one's gate outcome).
	StateCompiled State = "compiled"
	// StateNothingToCompile: every proposal already carries a diff. The healthy steady state.
	StateNothingToCompile State = "nothing_to_compile"
	// StateNoProposals: none exist for this workflow yet.
	StateNoProposals State = "no_proposals"
	// StateNoSource: the tenant has pushed no snapshot, or deleted the one this was discovered from.
	// The codemod rewrites source; without it there is nothing to rewrite.
	StateNoSource State = "no_source"
)

func (c *Compiler) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// Compile compiles every proposal for a workflow that does not already carry a diff.
//
// 🔴 It SKIPS proposals that already have one, and that is not merely an optimization. Re-compiling
// would re-resolve and could mint a different config hash — against a newer snapshot, or newer registry
// entries — for a proposal a customer's CI may already be measuring. The verdict would then arrive
// keyed to a hash the proposal no longer carries, and the measurement would be silently orphaned.
// A proposal's diff is minted once; a change of inputs is a NEW proposal.
func (c *Compiler) Compile(ctx context.Context, tenantID, workflowID string) (Result, error) {
	res := Result{TenantID: tenantID, WorkflowID: workflowID}

	scored, err := c.Store.ForWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return res, fmt.Errorf("hostedcompile: read proposals: %w", err)
	}
	if len(scored) == 0 {
		return res.with(StateNoProposals,
			"No proposals exist for this workflow yet. Generate some first."), nil
	}
	todo := uncompiled(scored)
	if len(todo) == 0 {
		return res.with(StateNothingToCompile,
			"Every proposal for this workflow already carries a diff. A proposal's diff is minted "+
				"once — a change of inputs produces a new proposal rather than a new diff for an old one."), nil
	}

	// The revision is the one the proposals were generated against. Taken from the PROPOSAL rather than
	// from the newest pushed snapshot: a diff must be generated against the tree the change was
	// reasoned about, or it rewrites call sites that have since moved.
	revision := todo[0].SourceRevision
	ref := sourceingest.Ref{TenantID: tenantID, WorkflowID: workflowID, SourceRevision: revision}

	err = c.Runner.WithSource(ctx, ref, func(dir string, ir *discovery.IR) error {
		compiler := proposal.Compiler{
			Resolver: &irResolver{ctx: ctx, ir: ir, regs: c.Registries},
			Root:     dir,
			Build:    c.gateFor(languageOf(ir), dir),
			IR:       ir,
		}
		for _, sc := range todo {
			out, err := c.compileOne(ctx, compiler, ir, sc)
			if err != nil {
				return err
			}
			res.Outcomes = append(res.Outcomes, out)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sourceingest.ErrNoSource) {
			return res.with(StateNoSource,
				"No source snapshot is retained for the revision these proposals were generated "+
					"against ("+revision+"). The codemod rewrites source; run `heros push-source` for "+
					"that revision, or generate proposals against one you still hold."), nil
		}
		return res, fmt.Errorf("hostedcompile: %w", err)
	}

	return res.with(StateCompiled, fmt.Sprintf(
		"%d proposal(s) compiled against %s. Each carries a reviewable diff; see each outcome for what "+
			"its gate proved.", len(res.Outcomes), revision)), nil
}

// compileOne compiles a single proposal and records what came back.
func (c *Compiler) compileOne(ctx context.Context, compiler proposal.Compiler, ir *discovery.IR, sc proposalstore.Scored) (Outcome, error) {
	cand, err := c.candidateFrom(ctx, sc)
	if err != nil {
		// A stored proposal we cannot reconstruct a candidate from is a DATA problem, not a verdict
		// about the change. Recorded as unbuilt with the reason rather than as build_failed, which would
		// claim we wrote code and it did not compile.
		rec := sc.Record
		rec.BuildStatus = proposalstore.BuildUnbuilt
		if perr := c.Store.Put(ctx, rec); perr != nil {
			return Outcome{}, fmt.Errorf("record %s: %w", sc.ProposalID, perr)
		}
		return Outcome{ProposalID: sc.ProposalID, Status: proposalstore.BuildUnbuilt,
			Detail: "This proposal could not be reconstructed into a candidate: " + err.Error()}, nil
	}

	// 🔴 THE NODE ORDER IS THE SOURCE'S, FILLED HERE, and only when the proposal makes no claim about it.
	//
	// variantspec.Resolve requires a spec to list every node in order, and the transform reads a spec
	// whose order differs from the source's as a WIRING CHANGE — a prune, a merge, a reorder — which it
	// refuses outright, because "no call-site rewriter materializes a node rearrangement as source". So a
	// model swap whose order is merely in a different sequence is rejected as control-flow surgery
	// nobody proposed, with a message about a change the user never asked for.
	//
	// A generator cannot supply it correctly: it holds the classified GRAPH, whose Layer/Order is a
	// deterministic RENDERING layout, not the order the statements run in. Both mistakes were found by
	// compiling a proposal end to end — first a partial order (read as a prune), then a sorted one (read
	// as a reorder).
	//
	// So an EMPTY order means "this proposal says nothing about ordering", and the authority fills it:
	// the IR at the revision being compiled. A proposal that DOES propose a reordering carries its own
	// order and keeps it — overwriting that would silently discard the change being proposed.
	if len(cand.Spec.Order) == 0 {
		cand.Spec.Order = irOrder(ir)
	}

	compiled, err := compiler.Compile(ctx, cand)
	if err != nil {
		return Outcome{}, fmt.Errorf("compile %s: %w", sc.ProposalID, err)
	}

	out := Outcome{ProposalID: sc.ProposalID, Status: string(compiled.BuildStatus), ConfigHash: compiled.ConfigHash}
	rec := sc.Record
	rec.BuildStatus = string(compiled.BuildStatus)

	// 🔴 The RESOLVED config hash replaces the candidate identity. proposalgen's hash identifies the
	// candidate and asserts nothing about whether it resolves; this one is the hash the verdict ingest
	// is keyed by, so until it is recorded a customer's CI has nothing it can report against.
	if compiled.ConfigHash != "" {
		rec.CandidateConfigHash = compiled.ConfigHash
	}

	switch compiled.BuildStatus {
	case proposal.BuildRefused:
		// The transform declined. build_status stays `unbuilt` because 0012's CHECK admits only
		// unbuilt|built|build_failed, and the REFUSAL is recorded in its own columns (migration 0032) —
		// which is what keeps it distinguishable from a proposal nobody has compiled yet, and what
		// carries the sentence the surface renders.
		rec.BuildStatus = proposalstore.BuildUnbuilt
		rec.RefusalReason = compiled.Refusal.Reason
		rec.RefusalDimension = compiled.Refusal.Dimension
		out.Status = string(proposal.BuildRefused)
		out.Detail = "The transform refused this change: " + compiled.Refusal.Reason
	case proposal.BuildFailed:
		rec.BuildStatus = proposalstore.BuildFailed
		rec.Status = proposalstore.StatusBuildFailed
		out.Detail = compiled.BuildLog
	default:
		out.Detail = compiled.BuildLog
	}

	if compiled.Patch != nil {
		hash, err := c.Blobs.Put(ctx, compiled.Patch.Diff)
		if err != nil {
			return Outcome{}, fmt.Errorf("store diff for %s: %w", sc.ProposalID, err)
		}
		rec.SourceDiffBlobHash = hash
		out.DiffHash = hash
	}

	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = c.now()
	}
	if err := c.Store.Put(ctx, rec); err != nil {
		return Outcome{}, fmt.Errorf("record %s: %w", sc.ProposalID, err)
	}
	return out, nil
}

// uncompiled returns the proposals with no diff yet.
func uncompiled(scored []proposalstore.Scored) []proposalstore.Scored {
	var out []proposalstore.Scored
	for _, sc := range scored {
		if sc.SourceDiffBlobHash == "" && sc.BuildStatus != proposalstore.BuildFailed {
			out = append(out, sc)
		}
	}
	return out
}

func (r Result) with(s State, detail string) Result {
	r.State, r.Detail = s, detail
	return r
}

// irOrder is the workflow's node order as the SOURCE runs it: by file, then by line.
//
// 🔴 NOT `ir.Nodes`' own order, and that distinction cost two debugging rounds. The transform validates
// a spec's order against `sourceOrderedPairs` — the consecutive statements it can actually see in the
// file — and `ir.Nodes` is not emitted in that order. Handing it through produced "this spec asks for
// the opposite order (1 source-ordered pair(s) inverted)": a refusal about a rewiring, for a model swap.
//
// ⚠️ variantspec.WiringOf has the same shape (it copies ir.Nodes' order into Wiring.Order) and is used
// as `DiscoveredWiring`. That is fine for the SET comparison it feeds, and it is not a source order —
// so it must not be borrowed as one here.
func irOrder(ir *discovery.IR) []string {
	if ir == nil {
		return nil
	}
	nodes := append([]discovery.IRNode(nil), ir.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i].CallSite, nodes[j].CallSite
		if a.File != b.File {
			return a.File < b.File
		}
		if a.LineStart != b.LineStart {
			return a.LineStart < b.LineStart
		}
		// Two calls on one line: the node id breaks the tie so the order is deterministic rather than
		// dependent on however discovery happened to emit them.
		return nodes[i].NodeID < nodes[j].NodeID
	})
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.NodeID)
	}
	return out
}

// languageOf reads the workflow's discovered language, which selects the gate's parser.
func languageOf(ir *discovery.IR) string {
	if ir == nil {
		return ""
	}
	return ir.Workflow.Language
}

// irResolver adapts variantspec.Resolve onto proposal.Resolver, which takes no context.
//
// The context is captured at construction rather than dropped: Resolve reads the registries, which is
// a database call, and a resolve that cannot be cancelled outlives the request that asked for it.
type irResolver struct {
	ctx  context.Context
	ir   *discovery.IR
	regs variantspec.Registries
}

func (r *irResolver) Resolve(spec *variantspec.VariantSpec) (*variantspec.Resolved, error) {
	return variantspec.Resolve(r.ctx, spec, r.ir, r.regs)
}

// candidateFrom rebuilds the candidate the generator emitted, from what the row records.
//
// 🔴 The SPEC is the load-bearing part, and it comes from the blob store (migration 0031) rather than
// being re-derived. Re-running the generator to recover it would produce a change against whatever the
// inputs are NOW — a different model, a different bottleneck — under an id a customer may already be
// verifying. A proposal is a candidate Variant Spec; if the spec is gone, the proposal is not
// recoverable, and saying so is the only honest answer.
func (c *Compiler) candidateFrom(ctx context.Context, sc proposalstore.Scored) (proposal.Candidate, error) {
	if sc.SpecBlobHash == "" {
		return proposal.Candidate{}, errors.New("it records no Variant Spec (it predates migration 0031), " +
			"and re-deriving one would compile a different change under this proposal's id")
	}
	raw, err := c.Blobs.Get(ctx, sc.SpecBlobHash)
	if err != nil {
		return proposal.Candidate{}, fmt.Errorf("its Variant Spec could not be read from the object store: %w", err)
	}
	var spec variantspec.VariantSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return proposal.Candidate{}, fmt.Errorf("its stored Variant Spec is not readable: %w", err)
	}
	return proposal.Candidate{
		Operator:  proposal.OperatorKind(sc.Operator),
		DiagID:    sc.DiagnosisID,
		NodeID:    sc.NodeID,
		Pattern:   patternclassifier.Pattern(sc.Pattern),
		Spec:      &spec,
		Rationale: sc.Rationale,
	}, nil
}
