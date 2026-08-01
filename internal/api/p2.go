package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/submit"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// The P2 read/submit surface the bare run/review/inspect UI is built on (tasks 7.2, 7.4).
//
// # Every unhappy path returns a node and a dimension
//
// PRD §9 (Product Designer) asks for the unhappy path to be designed first, and §6 FR18 wants the
// UI to name *which* node and *which* dimension broke. That is a constraint on THIS layer, not on the
// UI: a frontend can only show what the API tells it. So each of the three failures carries the same
// shape — {node_id, dimension, message} — and the UI renders one component for all three.
//
//	unresolved ref        -> 400, node + dimension, from variantspec.SpecError
//	build-rejected        -> 200 with status build-rejected, node + dimension, from the transform row
//	contract halt         -> 200 with status halted, node + reason, from the run row
//
// The last two are 200s on purpose. They are not API errors: the request to look at a run succeeded,
// and the run's terminal state is the answer. Returning 5xx for "your transform did not build" would
// make a legible product state indistinguishable from the server falling over.
//
// # Postgres, mounted separately
//
// The Server's own DB is the SQLite dev ledger (auth keys, memory). P2's stores are Postgres — a
// different database with different semantics — so P2 gets its own handle rather than pretending one
// *sql.DB serves both. MountP2 is opt-in because P2 is internal-only and ships behind the run queue
// with no public surface (PRD §12); a deployment without Postgres simply does not mount it.

// p2HTML is the bare run/review/inspect view.
//
// Embedded rather than read from disk: the binary is then self-contained, so deploying the UI is
// deploying agentd — no second artifact, no path to configure, and no way for the page and the API
// that serves it to be different versions of each other.
//
// P2Stores are the read models the UI needs, plus the write path it submits through.
type P2Stores struct {
	Transforms *worktree.Store
	Runs       *executor.Store
	Specs      *variantspec.Store
	// Submit is the write path (task 7.2). Optional, like the stores: a deployment with no target
	// repository to transform mounts the read views and no submit. When nil, POST .../submit answers
	// 503 rather than 404 — "this deployment cannot transform" and "that route does not exist" send an
	// operator to completely different places.
	Submit *submit.Service
}

// MountP2 registers the P2 UI's routes. Call after New.
func (s *Server) MountP2(st P2Stores) {
	s.p2 = st
	s.Mux.HandleFunc("GET /api/v1/runs/{run_id}", s.handleGetRun)
	s.Mux.HandleFunc("GET /api/v1/transforms/{config_hash}/{source_revision}", s.handleGetTransform)
	s.Mux.HandleFunc("POST /api/v1/specs/resolve", s.handleResolveSpec)
	s.Mux.HandleFunc("POST /api/v1/specs/submit", s.handleSubmitSpec)
}

// runView is a run plus its per-node I/O — everything the inspect view renders.
type runView struct {
	RunID          string `json:"run_id"`
	ConfigHash     string `json:"config_hash"`
	ConfigHash12   string `json:"config_hash_display"`
	SourceRevision string `json:"source_revision"`
	Seed           int64  `json:"seed"`
	// Status is the record's own value, verbatim. Task 7.3: "read terminal status from the
	// run/transform records (no derived state that drifts)". The UI must never compute a status from
	// the node list — a run whose nodes all succeeded but which was halted is exactly the case a
	// derived status gets wrong, and it is the case that matters.
	Status       string     `json:"status"`
	HaltedNodeID string     `json:"halted_node_id,omitempty"`
	HaltedReason string     `json:"halted_reason,omitempty"`
	Nodes        []nodeView `json:"nodes"`
}

type nodeView struct {
	NodeID         string `json:"node_id"`
	AttemptGroup   int    `json:"attempt_group"`
	Status         string `json:"status"`
	InputBlobHash  string `json:"input_blob_hash,omitempty"`
	OutputBlobHash string `json:"output_blob_hash,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
	Error          string `json:"error,omitempty"`
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if s.p2.Runs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P2 store is not mounted"})
		return
	}
	rec, err := s.p2.Runs.Get(r.Context(), r.PathValue("run_id"))
	if errors.Is(err, executor.ErrRunNotFound) {
		// 404 and nothing else. A run that does not exist and a run that is still starting are
		// different, and the UI's empty state depends on being able to tell them apart.
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such run"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	v := runView{
		RunID: rec.RunID, ConfigHash: rec.ConfigHash, ConfigHash12: variantspec.Display(rec.ConfigHash),
		SourceRevision: rec.SourceRevision, Seed: rec.Seed, Status: string(rec.Status),
		HaltedNodeID: rec.HaltedNodeID, HaltedReason: rec.HaltedReason,
		Nodes: []nodeView{}, // never null: the UI distinguishes "no nodes yet" from a decode failure
	}
	for _, n := range rec.Nodes {
		v.Nodes = append(v.Nodes, nodeView{
			NodeID: n.NodeID, AttemptGroup: n.AttemptGroup, Status: string(n.Status),
			InputBlobHash: n.InputBlobHash, OutputBlobHash: n.OutputBlobHash,
			IdempotencyKey: n.IdempotencyKey, Error: n.Error,
		})
	}
	writeJSON(w, http.StatusOK, v)
}

// transformView is a transform plus its diff — the review artifact ADR-001 makes the product's output.
type transformView struct {
	ConfigHash     string `json:"config_hash"`
	ConfigHash12   string `json:"config_hash_display"`
	SourceRevision string `json:"source_revision"`
	// Status is `built` or `build-rejected`, from the record. build-rejected is a first-class state
	// the UI renders distinctly (task 7.3), not an error.
	Status string `json:"status"`
	// VerificationStrength is what the gate PROVED — `type-checked` or `syntax-checked` (ADR-003
	// decision 3). It rides on the same response as the diff, deliberately: a reviewer looking at a
	// diff must be able to see, WITHOUT ASKING, whether a compiler stood behind it. `status: built`
	// answers "did the gate pass?" and says nothing about how much the gate could see, and before this
	// field the two were indistinguishable to every consumer.
	//
	// 🚫 A `syntax-checked` diff must never be presentable as though it were `type-checked`. Verbatim
	// from the record, like Status — never computed here (task 7.3: "no derived state that drifts").
	VerificationStrength string `json:"verification_strength"`
	// RequiresHumanReview is ADR-003 decision 5 as the UI consumes it: `syntax-checked` is human-
	// reviewed at every automation level, and only `type-checked` may ever be auto-applied.
	//
	// Derived, and derived HERE rather than in the client, because it is the one place the rule can be
	// stated once for every consumer. It calls worktree.Strength.AllowsAutonomousApply — the single
	// definition — instead of re-testing the string, so a UI and a future P6 loop cannot disagree about
	// what a value means. Not omitempty: `false` is the whole answer for a type-checked transform, and
	// a missing key would read as "unknown" to a client that then has to guess.
	RequiresHumanReview bool   `json:"requires_human_review"`
	Branch              string `json:"variant_branch,omitempty"`
	Commit              string `json:"variant_commit,omitempty"`
	// Diff is the reviewable patch. Empty for a baseline with no overrides — which the UI shows as its
	// own state ("no changes"), not as a failure to load.
	Diff     string `json:"diff"`
	DiffHash string `json:"diff_hash,omitempty"`
	// BuildLog and the attribution below are what a build-rejected transform gives the user to act on.
	BuildLog       string `json:"build_log,omitempty"`
	RejectedNodeID string `json:"rejected_node_id,omitempty"`
	RejectedDim    string `json:"rejected_dimension,omitempty"`
}

func (s *Server) handleGetTransform(w http.ResponseWriter, r *http.Request) {
	if s.p2.Transforms == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P2 store is not mounted"})
		return
	}
	rec, diff, err := s.p2.Transforms.Get(r.Context(), r.PathValue("config_hash"), r.PathValue("source_revision"))
	if errors.Is(err, worktree.ErrTransformNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such transform"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, transformView{
		ConfigHash: rec.ConfigHash, ConfigHash12: variantspec.Display(rec.ConfigHash),
		SourceRevision: rec.SourceRevision, Status: string(rec.Status),
		VerificationStrength: string(rec.Strength),
		RequiresHumanReview:  rec.Strength.RequiresHumanReview(),
		Branch:               rec.Branch, Commit: rec.Commit,
		Diff: string(diff), DiffHash: rec.DiffHash, BuildLog: rec.BuildLog,
		RejectedNodeID: rec.RejectedNodeID, RejectedDim: rec.RejectedDim,
	})
}

// specError is the one shape every fail-closed rejection takes (task 7.4).
type specError struct {
	Error     string `json:"error"`
	NodeID    string `json:"node_id,omitempty"`
	Dimension string `json:"dimension,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

// handleResolveSpec validates a Variant Spec's STRUCTURE, cheaply and with no side effects.
//
// It deliberately runs only variantspec.Validate. That is no longer because the registries are out of
// reach — POST .../submit resolves against them properly — but because this endpoint answers a
// different question: "have I typed a well-formed spec?", asked while the user is still editing, and
// answerable in microseconds against nothing. It catches the mistakes an author actually makes: a node
// not in the ordering, an edge to nowhere, a duplicate skill binding, an inlined definition.
//
// It is NOT a green light. A spec that passes here can still fail to resolve (a dangling ref) — only
// submit knows that, because only submit reads the registries and the IR at source_revision. Keeping
// the cheap check honest about its own scope is the point: a validator that sometimes resolved refs
// and sometimes did not would give a green whose meaning depended on what happened to be mounted.
//
// The rejection carries node + dimension in the same shape as every other unhappy path, so the UI
// renders one component for all of them.
func (s *Server) handleResolveSpec(w http.ResponseWriter, r *http.Request) {
	var spec variantspec.VariantSpec
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&spec); err != nil {
		// A malformed body is the author's error, and saying WHICH is the difference between a
		// fixable message and "400". json's own message names the offending field.
		writeJSON(w, http.StatusBadRequest, specError{Error: "the spec is not valid JSON: " + err.Error()})
		return
	}
	if err := spec.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, specErrorFrom(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid": true,
		"refs":  spec.Refs(),
		"nodes": len(spec.Order),
	})
}

// specErrorFrom unpacks a rejection that names a node and a dimension into the wire shape. The node
// and dimension are the whole point — "invalid spec" tells a user nothing they can act on.
//
// Two error types carry that attribution, from the two layers that can reject a spec:
//
//	*variantspec.SpecError    the spec does not resolve (a dangling ref, an unknown node)
//	*transform.RewriteError   it resolves, but a call site cannot be rewritten SAFELY (FR5)
//
// Both are the user's spec being refused with no side effects, and the user's question is identical
// either way, so both land in one shape and the UI renders one component for them.
func specErrorFrom(err error) specError {
	out := specError{Error: err.Error()}
	var se *variantspec.SpecError
	if errors.As(err, &se) {
		out.NodeID, out.Dimension, out.Ref = se.NodeID, string(se.Dim), se.Ref
		// The sentinel's text is already in Error(); keep the message but strip the redundant prefix
		// so the UI can show it next to the node/dimension chips without repeating them.
		out.Error = strings.TrimSpace(se.Detail)
		if out.Error == "" {
			out.Error = err.Error()
		}
		return out
	}
	var re *transform.RewriteError
	if errors.As(err, &re) {
		out.NodeID, out.Dimension = re.NodeID, string(re.Dim)
		out.Error = strings.TrimSpace(re.Detail)
		if out.Error == "" {
			out.Error = err.Error()
		}
	}
	return out
}

// isSpecRejection reports whether err is the AUTHOR's fault rather than ours — a 400, not a 500.
//
// Explicit about which errors those are, rather than defaulting to 400 for anything with a node
// attached: a Postgres outage during a submission must not be reported to the user as "your spec is
// bad", which would send them to rewrite a spec that was fine.
func isSpecRejection(err error) bool {
	var se *variantspec.SpecError
	var re *transform.RewriteError
	return errors.As(err, &se) || errors.As(err, &re)
}

// submitRequest is a submission: the spec, plus the two things the spec deliberately does NOT carry.
//
// variant_id and seed are not spec fields and must not become them. A Variant Spec is the hashed
// CONFIGURATION — config_hash is a pure function of it — while variant_id is a mutable human label
// that spans many configurations (PRD §8.4) and seed is explicitly excluded from the hash so that
// seeds 1..5 of one configuration roll up together (config-hash-spec §5). Putting either inside the
// spec would fold it into config_hash and break both properties. So they ride alongside it.
type submitRequest struct {
	Spec variantspec.VariantSpec `json:"spec"`
	// VariantID is the stable label this configuration is one version of.
	VariantID string `json:"variant_id"`
	Label     string `json:"label,omitempty"`
	// Seed is the run's seed and part of the run's identity (executor.RunIDFor). Absent means 0 — a
	// real, deterministic value. The reproducibility unit is {config_hash, source_revision, seed} and
	// a random default would make the platform non-reproducible by default.
	Seed int64 `json:"seed"`
}

// submitResult is what a submission became — and, deliberately, everything the UI needs to carry the
// user into the diff-review and run-watch views WITHOUT retyping an identifier.
//
// That is the point of returning the coordinates rather than just "202 accepted": before this
// endpoint the user had to hand-paste a config_hash and a run_id that nothing would ever hand them,
// which is why the three panels were three tools rather than one flow.
type submitResult struct {
	ConfigHash     string `json:"config_hash"`
	ConfigHash12   string `json:"config_hash_display"`
	SourceRevision string `json:"source_revision"`
	// TransformStatus is `built` or `build-rejected`, from the transform record.
	TransformStatus string `json:"transform_status"`
	Branch          string `json:"variant_branch,omitempty"`
	Commit          string `json:"variant_commit,omitempty"`
	DiffHash        string `json:"diff_hash,omitempty"`
	// RunID is set only when the transform BUILT. Empty on a rejection: FR5b says a transform that
	// does not build is never proposed and never run, so there is no run to watch.
	RunID string `json:"run_id,omitempty"`
	// The attribution for a build rejection (task 7.4).
	RejectedNodeID string `json:"rejected_node_id,omitempty"`
	RejectedDim    string `json:"rejected_dimension,omitempty"`
}

// handleSubmitSpec resolves a Variant Spec against the registries, persists it, generates and builds
// the transform, and enqueues a run (task 7.2).
//
// ─────────────────────────────────────────────────────────────────────────────────────────────────
// WHY THIS IS A NEW ENDPOINT (careful-api-creation — the five questions, answered)
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
//  1. Can an existing endpoint carry this with an optional field?
//     The candidate is POST /api/v1/specs/resolve + {"apply": true}. NO — and the reason is safety,
//     not tidiness. resolve is a pure, side-effect-free validator the page calls on every Validate
//     click. This writes four tables, checks out git, runs a compiler, and enqueues billable work.
//     Hanging that on a boolean means one wrong default, one client bug, or one replayed request
//     turns a free read into a build and a bill. An endpoint's most useful property is that you can
//     tell what it does to the world from its name; a flag that flips "reads nothing" to "writes
//     everything" destroys exactly that property.
//
//  2. Can a metadata / extra JSON field on an existing payload carry it?
//     NO, for the same reason: the carrier was never the problem. The side-effect boundary is, and a
//     field cannot express one.
//
//  3. Can it be an existing type plus a discriminator?
//     NO. Same objection — a discriminator on the request would put a read and a write behind one
//     route, so no proxy, log, retry policy, or timeout could tell them apart. They differ in every
//     wire property that matters: latency class (milliseconds vs a compile), retry safety, and
//     idempotency.
//
//  4. Is it serving a half-finished feature with no consumer?
//     NO — the inverse. Its consumer is the console's configure surface, and it is
//     the thing task 7.2 has been claiming existed. The half-finished feature was submit's ABSENCE:
//     a UI that says "submit a Variant Spec" and then asks the user to paste a config_hash that
//     nothing produces.
//
//  5. Is this a semantic preference rather than a functional necessity?
//     NECESSITY. Nothing in this system persisted a spec, generated a transform, or enqueued a run
//     outside a test helper. resolve cannot be extended into it even in principle: it holds no
//     registries — its own doc comment says so — and resolving refs is the first thing submit must do.
//
// Alternatives, and why they lost:
//   - POST /api/v1/specs/resolve + {"apply":true} — question 1. Rejected on safety.
//   - PUT /api/p2/variants/{variant_id}/configs (resource-shaped) — models the durable artifact, but
//     a submission's most important outputs (the build verdict, the run_id) are not that resource's
//     state, so every caller would have to follow up with two more reads. It also invents a variant
//     resource nothing else serves.
//
// Consumers, named as the rule requires: the write side is the console's configure surface. The read side is
// the two GET views already here, which the response's coordinates feed directly.
//
// ─────────────────────────────────────────────────────────────────────────────────────────────────
// STATUS CODES
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
//	400  the spec is the author's mistake — malformed, unresolved ref, or an unsafe rewrite. Carries
//	     node + dimension. NOTHING was persisted.
//	200  the submission ran to a terminal transform state. `built` (with a run_id) and
//	     `build-rejected` (without one) are BOTH 200: a transform that does not build is a legible
//	     answer the UI renders, and a 5xx would make it indistinguishable from the server falling over.
//	503  no submit path is mounted.
//	500  ours: the database, git, or the toolchain failed. Never the user's spec.
func (s *Server) handleSubmitSpec(w http.ResponseWriter, r *http.Request) {
	if s.p2.Submit == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment has no transform target mounted, so specs cannot be submitted"})
		return
	}
	var req submitRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the submission is not valid JSON: " + err.Error()})
		return
	}
	if req.VariantID == "" {
		// Named, not defaulted. A variant_id invented per submission would make every edit of one
		// variant look like a brand-new variant, destroying the edit history PRD §8.4 gives the field.
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "variant_id is required: it is the stable label this configuration is one version of"})
		return
	}

	out, err := s.p2.Submit.Submit(r.Context(), submit.Request{
		Spec: &req.Spec, VariantID: req.VariantID, Label: req.Label, Seed: req.Seed,
	})
	if err != nil {
		if isSpecRejection(err) {
			// Fail-closed (FR11): nothing was written. The rejection names the node and the dimension.
			writeJSON(w, http.StatusBadRequest, specErrorFrom(err))
			return
		}
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}

	res := submitResult{
		ConfigHash: out.ConfigHash, ConfigHash12: variantspec.Display(out.ConfigHash),
		SourceRevision: out.SourceRevision, TransformStatus: string(out.TransformStatus),
		Branch: out.Branch, Commit: out.Commit, DiffHash: out.DiffHash, RunID: out.RunID,
	}
	if out.Rejection != nil {
		res.RejectedNodeID, res.RejectedDim = out.Rejection.NodeID, out.Rejection.Dim
	}
	writeJSON(w, http.StatusOK, res)
}

var _ = sql.ErrNoRows
