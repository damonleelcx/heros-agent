// Package submit is the ONE path from an authored Variant Spec to a queued run (task 7.2).
//
// # What this package is for
//
// Everything P2 needs to turn a spec into a run already existed — variantspec.Resolve, the transform
// engine, worktree.Applier, the stores, runqueue — but the sequence that drives them lived only
// inside internal/e2e's test helper. So the product had every part of "submit a Variant Spec" and no
// submit: the UI made the user hand-paste a config_hash and a run_id that nothing would ever produce
// for them, because nothing persisted a spec or enqueued a run outside a test.
//
// This is that sequence, promoted to production code. It is deliberately an ORCHESTRATOR and nothing
// else: it owns no resolution rules, no hashing, no rewriting, no SQL. Every decision below is
// delegated to the package that already owns it, because a second implementation of "what a dangling
// ref means" or "what config_hash is" is exactly the split source-of-truth that makes two answers
// possible for one question.
//
// # The order is the safety property
//
//  1. discover the IR at source_revision      (read-only checkout; the user's tree is never opened)
//  2. resolve the spec against the registries  → config_hash, or FAIL CLOSED
//  3. persist the spec + its lineage
//  4. generate the codemod                     → a reviewable diff, or a safe refusal
//  5. apply + build on an isolated branch      → built, or build-rejected
//  6. record the transform
//  7. record the run, then enqueue it          (built only)
//
// Steps 1–2 have no persisted side effects, which is what makes FR11's "a dangling ref never triggers
// a partial run" structural rather than a discipline: nothing is written until the spec is known to
// resolve completely. Steps 4 and 5 have their own terminal states, and both are recorded rather than
// thrown away — a spec that does not build is an ANSWER, not an error.
//
// # Why the registries are held HERE and not in the API
//
// Before this package, internal/api held no registries, so POST /api/v1/specs/resolve could only run
// the structural half of validation and had to say so. The fix is not to hand the registries to the
// HTTP layer — an HTTP handler that resolves refs, generates codemods, and drives git is a handler
// that owns the domain. It is to give the domain a front door and let the API call it. The API layer
// keeps doing what it is good at: decode, delegate, map the outcome to a status code.
package submit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/runqueue"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// Service turns a submitted Variant Spec into a queued run.
type Service struct {
	pool  *worktree.Pool
	regs  variantspec.Registries
	specs *variantspec.Store
	// verifierFor maps the discovered workflow's language to its gate, and cache is the other half of
	// what an Applier is built from. Together they replace the single *worktree.Applier this Service
	// used to hold — see applierFor.
	verifierFor func(language string) (worktree.Verifier, error)
	cache       *worktree.Cache
	transforms  *worktree.Store
	runs        *executor.Store
	queue       *runqueue.Queue
	log         *slog.Logger
}

// applierFor builds the Applier for one submission's language (ADR-003 decision 1 + 2).
//
// # Why the Applier is built per submission, and not held on the Service
//
// An Applier holds ONE Verifier, by construction (worktree.NewApplier), because "which language is this
// workflow" is Discovery's answer and re-deriving it from marker files inside the worktree would be a
// second source of truth. That is right — and it means an Applier is bound to a language. A Service
// holding one Applier was therefore a Service that could only ever apply one language, which is exactly
// the Go-only shape ADR-003 exists to remove.
//
// So the language flows: Discovery states it (workflow.language) -> worktree.VerifierFor maps it to the
// gate -> NewApplier binds that gate. This function is the join, and it is the only place in the
// product where those three meet.
//
// A language with no gate is a hard failure BEFORE anything is applied. It must be: the alternative is
// applying a codemod and then discovering nothing can verify it, which would leave a branch and a
// worktree behind carrying an unverified change — and the one thing this whole pipeline promises is
// that an unverified transform is never proposed.
func (s *Service) applierFor(language string) (*worktree.Applier, error) {
	v, err := s.verifierFor(language)
	if err != nil {
		return nil, fmt.Errorf("submit: the workflow discovered at this revision is %q, and this "+
			"platform cannot verify it: %w", language, err)
	}
	return worktree.NewApplier(s.pool, v, s.cache), nil
}

// Deps are the collaborators a Service needs. A struct rather than eight positional arguments: every
// one of these is a pointer to something with a Put method, and a transposed pair would compile.
type Deps struct {
	Pool       *worktree.Pool
	Registries variantspec.Registries
	Specs      *variantspec.Store
	// Cache is the transform cache the per-language Applier is built with (task 3.6).
	Cache *worktree.Cache
	// Verifiers maps a Discovery language label to that language's gate. Defaults to
	// worktree.VerifierFor, which is the product's table.
	//
	// It is a seam rather than a hard call so a deployment can PIN its toolchains (PRD §7 requires a
	// deterministic build: "whatever is first on PATH" is not pinned, and two runs of one config_hash on
	// two machines must not verify with different compilers). worktree.VerifierFor returns
	// zero-configured verifiers, which resolve their tools from PATH — right for a default, not
	// something to hard-wire past.
	//
	// 🚫 It is NOT a way to weaken a gate. Whatever it returns must still state what it proved; the
	// Applier rejects a verifier that returns no strength, and the DB's CHECK rejects it again.
	Verifiers  func(language string) (worktree.Verifier, error)
	Transforms *worktree.Store
	Runs       *executor.Store
	Queue      *runqueue.Queue
	Log        *slog.Logger
}

// New builds a Service, refusing to start without a collaborator it cannot work without.
//
// Checked at construction, not at the first submit: a Service missing its queue would resolve,
// persist, generate, apply, and BUILD before discovering it cannot enqueue — having spent minutes and
// left rows behind. A deployment that is wired wrong should fail to come up.
func New(d Deps) (*Service, error) {
	missing := []string{}
	for name, ok := range map[string]bool{
		"Pool": d.Pool != nil, "Registries": d.Registries != nil, "Specs": d.Specs != nil,
		"Cache": d.Cache != nil, "Transforms": d.Transforms != nil, "Runs": d.Runs != nil,
		"Queue": d.Queue != nil,
	} {
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sortStrings(missing)
		return nil, fmt.Errorf("submit: cannot serve submissions without %v", missing)
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	// The product's table is the default. Not a fallback to something permissive: VerifierFor errors on
	// a language it has no gate for, which is what a missing Verifiers seam should also do.
	verifiers := d.Verifiers
	if verifiers == nil {
		verifiers = worktree.VerifierFor
	}
	return &Service{
		pool: d.Pool, regs: d.Registries, specs: d.Specs, verifierFor: verifiers, cache: d.Cache,
		transforms: d.Transforms, runs: d.Runs, queue: d.Queue, log: log,
	}, nil
}

// Request is one submission.
type Request struct {
	// Spec is the authored Variant Spec. It carries its own workflow_id and source_revision.
	Spec *variantspec.VariantSpec
	// VariantID is the stable logical label this configuration is one version of (PRD §8.4: a human
	// edits a variant over time, and one variant_id maps to many config_hash values across that
	// history). Required: variantspec.Store.Put refuses without it, and inventing one per submit would
	// make every edit look like a brand-new variant and destroy the edit history the field exists for.
	VariantID string
	// Label is the human-readable name for the variant. Defaults to VariantID.
	Label string
	// Seed is the run's seed — the third axis of the reproducibility unit, and part of the run's
	// identity (see executor.RunIDFor). 0 is a real, deterministic value, not "unset": a random
	// default would make the platform non-reproducible by default, which is the opposite of what it
	// is for.
	Seed int64
}

// Outcome is what a submission became. Every terminal state is a value here, not an error — a
// build-rejected transform is a legible answer the UI renders, and modelling it as a failure is how
// it ends up indistinguishable from the server falling over.
type Outcome struct {
	ConfigHash     string
	SourceRevision string
	// TransformStatus is `built` or `build-rejected`, from the record.
	TransformStatus worktree.BuildStatus
	Branch          string
	Commit          string
	DiffHash        string
	// RunID is set only when the transform BUILT and was enqueued. Empty on a build rejection, which
	// is FR5b: a transform that does not build is never proposed and never run.
	RunID string
	// Rejection carries the node and dimension the compiler blamed, when TransformStatus is
	// build-rejected. It is what task 7.4 requires the UI be able to show.
	Rejection *worktree.BuildRejection
}

// Submit resolves, persists, transforms, builds, records, and enqueues.
//
// Every failure before step 3 leaves NOTHING behind. See the package comment for the order and why.
func (s *Service) Submit(ctx context.Context, req Request) (*Outcome, error) {
	if req.Spec == nil {
		return nil, fmt.Errorf("submit: a submission needs a Variant Spec")
	}
	if req.VariantID == "" {
		return nil, fmt.Errorf("submit: a submission needs a variant_id (the stable label this " +
			"configuration is one version of)")
	}
	if req.Seed < 0 {
		// 0005 CHECKs seed >= 0. Caught here so the user gets a sentence instead of a constraint name
		// from Postgres four steps later, after a build.
		return nil, fmt.Errorf("submit: seed must not be negative, got %d", req.Seed)
	}
	rev := req.Spec.SourceRevision
	if rev == "" {
		return nil, fmt.Errorf("submit: the spec has no source_revision to check out")
	}
	label := req.Label
	if label == "" {
		label = req.VariantID
	}

	// ── 1 · the IR at source_revision ────────────────────────────────────────────────────────────
	//
	// From an isolated checkout, never the user's tree — the same rule internal/worktree exists to
	// enforce, and it applies just as much to reading. Released before returning: this checkout is a
	// scratch read, and leaving one per submission behind would fill the pool with garbage.
	tree, err := s.checkout(ctx, rev)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tree.Release(ctx); err != nil {
			// A leaked worktree is not worth failing a successful submission over, but it IS the kind
			// of thing that silently fills a disk, so it does not get to be invisible.
			s.log.WarnContext(ctx, "submit: could not release the read checkout; it will occupy the pool",
				"dir", tree.Dir, "revision", rev, "error", err)
		}
	}()

	res, err := discovery.Run(discovery.Options{
		Repo: tree.Dir, WorkflowID: req.Spec.WorkflowID, CommitSHA: rev,
	})
	if err != nil {
		return nil, fmt.Errorf("submit: discover the graph at %s: %w", rev, err)
	}
	ir := res.IR

	// The gate is chosen HERE, from the language Discovery just stated, and before anything is
	// persisted or applied. ADR-003 decision 1/2: the apply path is language-neutral, and which gate
	// stood behind a diff is a per-language fact the record has to carry. A language this platform
	// cannot verify stops the submission dead — with nothing written, because we are still above step 3.
	applier, err := s.applierFor(ir.Workflow.Language)
	if err != nil {
		return nil, err
	}

	// ── 2 · resolve · FAIL CLOSED ────────────────────────────────────────────────────────────────
	//
	// The registries are real here, so this is the WHOLE resolution — not the structural half
	// POST /api/v1/specs/resolve can do on its own. A dangling ref stops the submission dead, with
	// the node and the dimension named (FR11, task 7.4), and — because nothing above wrote a row —
	// with no spec, no transform and no run to clean up.
	resolved, err := variantspec.Resolve(ctx, req.Spec, &ir, s.regs)
	if err != nil {
		return nil, err // already a *variantspec.SpecError naming node + dimension
	}

	// ── 3 · persist ──────────────────────────────────────────────────────────────────────────────
	//
	// The workflow row first: `variant` and `config` are FK-bound to it, so without it the Put below
	// fails on a constraint name rather than doing what the user asked. Its content comes from the
	// discovery run above — repo, commit and language are discovery's to state, not ours to guess.
	if err := s.specs.EnsureWorkflow(ctx, ir.Workflow, ir.IRVersion); err != nil {
		return nil, err
	}
	if err := s.specs.Put(ctx, resolved, req.Spec, req.VariantID, label); err != nil {
		return nil, err
	}

	// ── 4 · generate ─────────────────────────────────────────────────────────────────────────────
	//
	// Generated from the checkout, which IS at source_revision — the guarantee transform.Generate
	// cannot make for itself ("it is handed a directory, not a git repo") and requires of its caller.
	patch, err := transform.Generate(resolved, tree.Dir)
	if err != nil {
		// A refusal (ErrUnsafeRewrite) is FR5 working: a call site the transform cannot rewrite safely
		// is rejected BEFORE anything is applied. It already names the node and the dimension.
		return nil, err
	}

	// ── 5 · apply + build ────────────────────────────────────────────────────────────────────────
	applied, applyErr := applier.Apply(ctx, patch)
	var rejection *worktree.BuildRejection
	_ = errors.As(applyErr, &rejection)
	if applied == nil {
		// No terminal build state at all — the worktree or git itself failed. Distinct from a
		// rejection, and not something to record as one: "your transform does not build" would be a
		// lie about the user's spec.
		return nil, fmt.Errorf("submit: apply the transform for %s: %w",
			variantspec.Display(resolved.ConfigHash), applyErr)
	}
	if applyErr != nil && rejection == nil {
		return nil, fmt.Errorf("submit: apply the transform for %s: %w",
			variantspec.Display(resolved.ConfigHash), applyErr)
	}

	// ── 6 · record the transform ─────────────────────────────────────────────────────────────────
	//
	// Recorded whether it built or was rejected. The rejection is the review artifact for a spec that
	// does not compile, and it is what stops P4 re-attempting a known-broken variant.
	if err := s.transforms.Put(ctx, applied, rejection); err != nil {
		return nil, err
	}

	out := &Outcome{
		ConfigHash: applied.ConfigHash, SourceRevision: applied.SourceRevision,
		TransformStatus: applied.Status, Branch: applied.Branch, Commit: applied.Commit,
		DiffHash: applied.DiffHash, Rejection: rejection,
	}

	// A build-rejected transform is TERMINAL and gets no run (FR5b). The user is not left at a dead
	// end: the transform record carries the build log and the blamed node/dimension, and the caller
	// hands them straight to the diff-review view.
	if applied.Status != worktree.StatusBuilt {
		s.log.InfoContext(ctx, "submit: transform rejected by the build gate; no run enqueued",
			"config_hash", applied.ConfigHash, "source_revision", rev,
			"node_id", rejectedNode(rejection), "dimension", rejectedDim(rejection))
		return out, nil
	}

	// ── 7 · record the run, then enqueue it ──────────────────────────────────────────────────────
	//
	// This order, not the reverse. The `run` row is what GET /api/v1/runs/{run_id} reads, so writing
	// it first means the id this call returns is watchable the instant the user has it — a run that is
	// queued but 404s would make a successful submit look like a broken one.
	//
	// It also fails in the better direction. A crash between the two leaves a `running` run that is
	// not queued: visible, diagnosable, and REPAIRED by re-submitting, because every step here is
	// idempotent on a derived run_id. The reverse order would leave a queue item whose run row does
	// not exist, which a worker would pick up and fail on.
	runID := executor.RunIDFor(resolved.ConfigHash, rev, req.Seed)
	if err := s.runs.Start(ctx, runID, resolved.ConfigHash, rev, req.Seed); err != nil {
		return nil, err
	}
	if err := s.queue.Enqueue(ctx, runID, resolved.ConfigHash, rev, req.Seed); err != nil {
		return nil, err
	}
	out.RunID = runID
	return out, nil
}

// checkout takes an isolated, read-only working copy at rev.
//
// The branch name carries a random suffix, and that is load-bearing rather than cosmetic. Pool.Acquire
// derives the worktree directory from the branch name and CLEARS whatever is already there — so two
// submissions naming one read branch would have the second delete the first's checkout out from under
// it, mid-discovery. A unique branch per submission makes concurrent submits structurally disjoint
// instead of relying on them not overlapping.
//
// `heros/read/` rather than `variant/`: this is a scratch read at a pinned revision, and it must never
// be mistaken — by a human reading `git branch`, or by anything else — for the variant branch that
// carries the applied change.
func (s *Service) checkout(ctx context.Context, rev string) (*worktree.Worktree, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("submit: name a read checkout: %w", err)
	}
	tree, err := s.pool.Acquire(ctx, rev, "heros/read/"+hex.EncodeToString(nonce[:]))
	if err != nil {
		return nil, fmt.Errorf("submit: check out %s: %w", rev, err)
	}
	return tree, nil
}

func rejectedNode(r *worktree.BuildRejection) string {
	if r == nil {
		return ""
	}
	return r.NodeID
}

func rejectedDim(r *worktree.BuildRejection) string {
	if r == nil {
		return ""
	}
	return r.Dim
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
