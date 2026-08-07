package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
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
// *sql.DB serves both. MountConfigRuntime is opt-in because P2 is internal-only and ships behind the run queue
// with no public surface (PRD §12); a deployment without Postgres simply does not mount it.

// p2HTML is the bare run/review/inspect view.
//
// Embedded rather than read from disk: the binary is then self-contained, so deploying the UI is
// deploying agentd — no second artifact, no path to configure, and no way for the page and the API
// that serves it to be different versions of each other.
//
// ConfigRuntimeStores are the read models the UI needs, plus the write path it submits through.
type ConfigRuntimeStores struct {
	Transforms *worktree.Store
	Runs       *executor.Store
	Specs      *variantspec.Store
	// Submit is the write path (task 7.2). Optional, like the stores: a deployment with no target
	// repository to transform mounts the read views and no submit. When nil, POST .../submit answers
	// 503 rather than 404 — "this deployment cannot transform" and "that route does not exist" send an
	// operator to completely different places.
	Submit *submit.Service
}

// MountConfigRuntime registers the P2 UI's routes. Call after New.
func (s *Server) MountConfigRuntime(st ConfigRuntimeStores) {
	s.configRuntime = st
	s.Mux.HandleFunc("GET /api/v1/runs/{run_id}", s.handleGetRun)
	// P27 FR15: the collection. It existed nowhere before, which is why "what did I run last week?" was
	// not a question this API could be asked. It takes NO organization parameter in any position — the
	// scope is the verified principal's, and there is nothing to get wrong.
	s.Mux.HandleFunc("GET /api/v1/runs", s.handleListRuns)
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
	if s.configRuntime.Runs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P2 store is not mounted"})
		return
	}
	rec, err := s.configRuntime.Runs.Get(r.Context(), r.PathValue("run_id"))
	if errors.Is(err, executor.ErrRunNotFound) {
		// 404 and nothing else. A run that does not exist and a run that is still starting are
		// different, and the UI's empty state depends on being able to tell them apart.
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such run"})
		return
	}
	if err == nil && !visibleToPrincipal(r, rec.TenantID) {
		// 🔴 P27 FR18. A run belonging to ANOTHER organization answers exactly what a run that does not
		// exist answers — same status, same body — so the endpoint is not an existence oracle. The
		// caller may name any subject it likes; the platform decides whether this organization may see
		// it, and declining to confirm that it exists is the correct answer to give a stranger.
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

// reportedTransformView is a transform the platform was TOLD about, as the console reads it.
//
// # 🔴 Why this is its own type rather than more optional fields on transformView
//
// It is the discipline §4.2 settled on for a linked RUN, for the same reason. A transform the platform
// GENERATED has a diff, a build gate and a verification strength; one it was told about has per-node
// outcomes and three integers. Merging them would produce a type whose every field is optional, which
// tells a consumer nothing about which half it is holding — and that is precisely the failure this type
// exists to close: `/app/transforms/{hash}/{rev}` answered **500** on a reported transform
// (`Cannot read properties of undefined (reading 'trim')`), because the page read `transform.diff` on a
// payload that has never carried one. §6.5 built the reported ANSWER and left the page reading the
// executor's shape; a generated type covering only that shape type-checked perfectly.
//
// `origin` is the discriminator, present on the wire and never inferred from which fields are empty.
//
// 🚫 There is no `diff` field and there must never be one: the receipt carries counts where a diff would
// go (§2.8), so such a field could only be filled by inventing content the platform was not sent.
// `DiffAvailable` is a plain false and `DiffAbsentBecause` says it in words, so a surface renders a
// stated boundary rather than a blank panel that reads as broken.
type reportedTransformView struct {
	// Origin is always "reported". Stated rather than implied, so a consumer branches on a value it can
	// see instead of on the absence of a field it expected.
	Origin            string                    `json:"origin"`
	ConfigHash        string                    `json:"config_hash"`
	ConfigHash12      string                    `json:"config_hash_display"`
	SourceRevision    string                    `json:"source_revision"`
	SourceRevision12  string                    `json:"source_revision_display"`
	WorkflowID        string                    `json:"workflow_id"`
	Status            string                    `json:"status"`
	ToolVersion       string                    `json:"tool_version"`
	CoverageVersion   string                    `json:"coverage_version,omitempty"`
	ReportedAt        string                    `json:"reported_at"`
	NodeOutcomes      []runlink.WireNodeOutcome `json:"node_outcomes"`
	NodesApplied      int                       `json:"nodes_applied"`
	NodesRefused      int                       `json:"nodes_refused"`
	FilesChanged      int                       `json:"files_changed"`
	LinesAdded        int                       `json:"lines_added"`
	LinesRemoved      int                       `json:"lines_removed"`
	DiffAvailable     bool                      `json:"diff_available"`
	DiffAbsentBecause string                    `json:"diff_absent_because"`
}

func (s *Server) handleGetTransform(w http.ResponseWriter, r *http.Request) {
	configHash, sourceRevision := r.PathValue("config_hash"), r.PathValue("source_revision")
	if s.configRuntime.Transforms == nil {
		// 🔴 P29 §6.5 — a deployment with no EXECUTOR still has transforms to show, because the customer
		// generated them on their own machine and told us. Answering "the P2 store is not mounted" to
		// somebody who ran `heros apply --link-receipt` is telling them their receipt went nowhere.
		if s.reportedTransform(w, r, configHash, sourceRevision) {
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P2 store is not mounted"})
		return
	}
	rec, diff, err := s.configRuntime.Transforms.Get(r.Context(), configHash, sourceRevision)
	if errors.Is(err, worktree.ErrTransformNotFound) {
		// The platform did not generate this transform. It may still have been TOLD about one — which is
		// the whole point of a receipt, and the reason /app/transforms could not resolve for anything a
		// customer applied locally.
		if s.reportedTransform(w, r, configHash, sourceRevision) {
			return
		}
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
	if s.configRuntime.Submit == nil {
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

	// 🔴 The owning organization comes from the VERIFIED principal, and there is no field on
	// `submitRequest` a caller could put one in. That is FR14 and FR16 in the same line: the owner is
	// recorded at write time, and it is not something the request gets to say about itself.
	owner := ""
	if p, ok := auth.PrincipalFrom(r.Context()); ok {
		owner = p.TenantID
	}
	out, err := s.configRuntime.Submit.Submit(r.Context(), submit.Request{
		Spec: &req.Spec, VariantID: req.VariantID, Label: req.Label, Seed: req.Seed, TenantID: owner,
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

// visibleToPrincipal reports whether the caller's organization may see a subject owned by `owner`.
//
// # The three cases, and why the third is not a security hole
//
//   - The owner matches the verified principal's organization. Visible.
//   - The owner is a DIFFERENT organization. Not visible, and the caller is told the subject does not
//     exist rather than that it is forbidden (P27 FR18).
//   - The owner is EMPTY — a pre-ownership row, created before P27, whose owner was never written and is
//     not recoverable. Visible to any authenticated principal, and that is the honest answer: the
//     information needed to scope it does not exist, and inventing one would produce a *confident wrong*
//     owner on a row that is billed usage. These rows are excluded from every LISTING (see
//     `Store.ListForTenant`), so nobody discovers somebody else's history by browsing; they are reachable
//     only by an id somebody already had, which is the state the product was in before this phase.
//
// A request with no principal at all sees nothing: the middleware refuses before this is reached, and
// this returns false rather than trusting that.
func visibleToPrincipal(r *http.Request, owner string) bool {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.TenantID == "" {
		return false
	}
	if strings.TrimSpace(owner) == "" {
		return true
	}
	return owner == p.TenantID
}

// handleListRuns answers "what did I run?" — the question this API could not be asked before P27.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	// 🔴 EITHER source is enough (P29 §4.2). The old guard returned 503 whenever the EXECUTOR store was
	// absent, which is the normal shape of a deployment that only receives links: a customer with a
	// hundred linked runs was told "the P2 store is not mounted" and shown nothing. Hosted execution is
	// P25's standing refusal — the platform learns of a run, it does not perform one — so "no executor"
	// is the expected configuration, not a broken one.
	if s.configRuntime.Runs == nil && s.linkedRuns == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"state": StateNotMounted,
			"error": "this deployment carries neither executed runs nor run linking",
		})
		return
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}

	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	// The cursor is a timestamp, not an offset. An offset page shifts under a concurrent write and
	// silently skips or repeats a row; a timestamp cursor is stable against inserts.
	var before time.Time
	if v := strings.TrimSpace(r.URL.Query().Get("before")); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "`before` must be an RFC3339 timestamp"})
			return
		}
		before = t
	}

	var executed []executor.RunSummary
	executedCarried := s.configRuntime.Runs != nil
	if executedCarried {
		var err error
		executed, err = s.configRuntime.Runs.ListForTenant(r.Context(), p.TenantID, limit, before)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}

	// 🔴 ONE LIST, TWO ORIGINS (P29 §4.2).
	//
	// This endpoint read the EXECUTOR's `run` table and nothing else, while a linked run lands in
	// `run_link` — two tables, one identifier, nothing joining them. So the run a developer linked ninety
	// seconds ago was not in the list of their runs, and the console looked like the platform had lost
	// it.
	//
	// The fix is not a second endpoint. A developer does not have two kinds of run in their head and
	// should not be handed two lists to reconcile; what they need is one list where each row says where
	// it came from. `origin` is that, carried as DATA so the detail view can route on it rather than
	// guess from which fields happen to be empty.
	runs := make([]runListRow, 0, len(executed))
	for i := range executed {
		runs = append(runs, runListRow{RunSummary: &executed[i], Origin: "executed", At: executed[i].StartedAt})
	}
	linkedFailed := false
	if s.linkedRuns != nil {
		linked, lerr := s.linkedRuns.ListForTenant(p.TenantID, limit, before)
		if lerr != nil {
			// 🔴 NOT fatal, and NOT silent. Half a list rendered as a whole one is the failure this
			// endpoint already had; reporting the half we have plus the fact that the other half could
			// not be read is the only honest answer. A 500 here would hide the executed runs too.
			linkedFailed = true
		} else {
			for _, lr := range linked {
				runs = append(runs, runListRow{Origin: "linked", At: lr.LinkedAt,
					SummaryFromLink: summaryFromLink(lr)})
			}
		}
	}
	// ONE ordering over the merged list. Sorting each source separately and concatenating would put
	// every linked run after every executed one regardless of when either happened, which is a list
	// ordered by our storage layout rather than by the customer's day.
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].At.Equal(runs[j].At) {
			return runs[i].At.After(runs[j].At)
		}
		return runs[i].RunID < runs[j].RunID
	})
	if len(runs) > limit {
		runs = runs[:limit]
	}
	// 🔴 THREE states, not one. "You have no runs yet", "runs exist that predate ownership recording",
	// and "the platform did not answer" are three different facts with three different next actions, and
	// collapsing any pair of them into an empty list is what makes a release read as data loss to
	// somebody who used the product last week.
	body := map[string]any{"runs": runs}
	if linkedFailed {
		// A fourth state on this one endpoint, because it reads two sources and either can fail alone.
		// Naming it is what stops a reader concluding their linked runs were never stored.
		body["linked_runs_state"] = string(StateReadFailed)
		body["linked_runs_note"] = "the executed runs below are complete; linked runs could not be read, " +
			"so this list is PARTIAL — they are not missing, they are unread"
	}
	// 🔴 Reported ALWAYS, not only when the page is empty.
	//
	// The first version returned this count only for a tenant with no rows, which made the "runs exist
	// that predate ownership" banner unreachable for the tenant who most needs it: somebody with both
	// new runs and old ones sees a list that is silently partial. The count is a property of the
	// platform rather than of a tenant — a pre-ownership row belongs to nobody, which is the whole
	// meaning of the NULL — so it is the same answer either way and there is no reason to hide it
	// behind an empty list.
	if !executedCarried {
		// Stated rather than implied by absence: a list with no executed runs on a platform that does not
		// execute is complete, and a reader must be able to tell that from a list that lost half itself.
		body["executed_runs_state"] = string(StateNotMounted)
		body["executed_runs_note"] = "this deployment does not execute runs — every row here was LINKED " +
			"from a developer's machine, which is the platform's standing boundary and not a gap"
	}
	if executedCarried {
		if n, cerr := s.configRuntime.Runs.PreOwnedCount(r.Context()); cerr == nil && n > 0 {
			body["pre_ownership_runs"] = n
			body["pre_ownership_note"] = "runs created before this platform recorded which organization " +
				"owns them are not listed; they remain reachable by id"
		}
	}
	if len(runs) == limit && limit > 0 {
		// The cursor for the next page is the LAST row's timestamp, handed back rather than derived by
		// the client — the client does not know the ordering column and must not have to guess it. It is
		// the MERGED ordering's timestamp, so one cursor pages both origins.
		body["next_before"] = runs[len(runs)-1].At.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, body)
}

// runListRow is one row of the MERGED runs list (P29 §4.2).
//
// # Why a linked run is not flattened into a RunSummary
//
// It cannot honestly be. `RunSummary.Status` is the EXECUTOR's terminal state and a linked run has none
// — the platform learned of a run, it did not perform one. Filling that field would mean inventing a
// value, and the two candidates are both wrong: `succeeded` claims we observed something we did not, and
// an empty string renders as a broken row. So the two shapes are carried side by side, exactly one of
// them is present, and `origin` says which — the same discipline `LinkedRunView` follows for the detail
// view and for the same reason `hostedscorecard` reports `FailureAttribution: unavailable`.
type runListRow struct {
	// Origin is `executed` or `linked`. Carried as DATA so the detail view routes on it rather than
	// guessing from which fields happen to be empty.
	Origin string `json:"origin"`
	// At is the row's position in the merged ordering — a run's start for an executed one, the link's
	// timestamp for a linked one. It is the cursor column, so one cursor pages both origins.
	At time.Time `json:"at"`
	// RunID is lifted to the top level because it is the ONE field both shapes have and every consumer
	// needs, and reading it from two places is how a consumer forgets one of them.
	RunID string `json:"run_id"`

	// Exactly one of the two below is present.
	*executor.RunSummary `json:",omitempty"`
	SummaryFromLink      *linkedRunRow `json:"linked,omitempty"`
}

// MarshalJSON flattens the executed summary and fills RunID from whichever shape is present.
func (r runListRow) MarshalJSON() ([]byte, error) {
	type alias runListRow
	a := alias(r)
	switch {
	case a.RunSummary != nil:
		a.RunID = a.RunSummary.RunID
	case a.SummaryFromLink != nil:
		a.RunID = a.SummaryFromLink.RunID
	}
	return json.Marshal(a)
}

// linkedRunRow is what a LINKED run has: the numbers the developer's own harness computed. No status,
// no attempt groups, no per-node blobs — there is nothing to put in them.
type linkedRunRow struct {
	RunID          string  `json:"run_id"`
	WorkflowID     string  `json:"workflow_id"`
	ConfigHash     string  `json:"config_hash"`
	ConfigHash12   string  `json:"config_hash_display"`
	SourceRevision string  `json:"source_revision"`
	ToolVersion    string  `json:"tool_version"`
	LinkedAt       string  `json:"linked_at"`
	CostUSD        float64 `json:"cost_usd"`
	LatencyMS      float64 `json:"latency_ms"`
	// GateOutcome is the customer's OWN gate verdict, empty when the run predates the evidence crossing
	// the boundary. Empty is deliberately not one of the verdicts.
	GateOutcome string            `json:"gate_outcome,omitempty"`
	Scores      []LinkedScoreView `json:"scores"`
}

func summaryFromLink(lr linkingest.LinkedRun) *linkedRunRow {
	row := &linkedRunRow{
		RunID: lr.RunID, WorkflowID: lr.WorkflowID,
		ConfigHash: lr.ConfigHash, ConfigHash12: shortHash12(lr.ConfigHash),
		SourceRevision: lr.SourceRevision, ToolVersion: lr.ToolVersion,
		LinkedAt:    lr.LinkedAt.UTC().Format(time.RFC3339),
		GateOutcome: string(lr.Eval.GateOutcome),
		Scores:      make([]LinkedScoreView, 0, len(lr.Scores)),
	}
	for _, sc := range lr.Scores {
		row.Scores = append(row.Scores, LinkedScoreView{
			Metric: sc.Metric, Value: sc.Value, CILow: sc.CILow, CIHigh: sc.CIHigh,
		})
	}
	return row
}

// reportedTransform answers from a TRANSMITTED RECEIPT, and reports whether it did.
//
// # 🔴 What it renders, and the one thing it cannot
//
// Per-node outcomes and a DIFFSTAT. Never a diff — the receipt carries three integers where one would
// go and there is no field a hunk could occupy, so this handler could not render one if it tried. That
// is stated in the response itself (`diff_available: false` with a reason), because a transform page
// with no diff on it looks broken unless the screen says why it is absent.
//
// `origin: reported` sits beside the data for the same reason the runs list carries one: a transform the
// platform GENERATED and one it was TOLD about support different things, and a reader must be able to
// tell which they are looking at rather than inferring it from which fields are empty.
func (s *Server) reportedTransform(w http.ResponseWriter, r *http.Request, configHash, sourceRevision string) bool {
	if s.transformReceipts == nil {
		return false
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.TenantID == "" {
		return false
	}
	rec, found, err := s.transformReceipts.Get(p.TenantID, configHash, sourceRevision)
	if err != nil {
		// A read failure is neither "no such transform" nor a missing store. Saying so costs one branch
		// and saves a customer from concluding their receipt was never accepted.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"state": StateReadFailed,
			"error": "could not read the reported transform: " + err.Error(),
		})
		return true
	}
	if !found {
		return false
	}
	applied, refused := 0, 0
	for _, o := range rec.NodeOutcomes {
		switch o.Outcome {
		case runlink.OutcomeApplied:
			applied++
		case runlink.OutcomeRefused:
			refused++
		}
	}
	// 🔴 A TYPE, not a map literal. The map was invisible to `ConsoleViewTypes` — nothing generated a
	// TypeScript shape for it — so the console had no declaration of this response and read the
	// executor's instead. A map cannot participate in the drift gate; a struct must.
	outcomes := rec.NodeOutcomes
	if outcomes == nil {
		// Never null on the wire. A consumer that maps over this must get an empty list, not a crash —
		// the same rule §4.5 states for an enumeration, applied one level down.
		outcomes = []runlink.WireNodeOutcome{}
	}
	writeJSON(w, http.StatusOK, reportedTransformView{
		Origin:           "reported",
		ConfigHash:       rec.ConfigHash,
		ConfigHash12:     shortHash12(rec.ConfigHash),
		SourceRevision:   rec.SourceRevision,
		SourceRevision12: shortHash12(rec.SourceRevision),
		WorkflowID:       rec.WorkflowID,
		Status:           rec.Status,
		ToolVersion:      rec.ToolVersion,
		CoverageVersion:  rec.CoverageVersion,
		ReportedAt:       rec.ReceivedAt.UTC().Format(time.RFC3339),
		NodeOutcomes:     outcomes,
		NodesApplied:     applied,
		NodesRefused:     refused,
		FilesChanged:     rec.FilesChanged,
		LinesAdded:       rec.LinesAdded,
		LinesRemoved:     rec.LinesRemoved,
		// 🚫 Said out loud rather than left as an empty field. A transform page with a blank diff reads
		// as a broken page; a transform page that says WHY there is no diff reads as a boundary.
		DiffAvailable: false,
		DiffAbsentBecause: "This transform was generated on your machine and reported as a receipt. " +
			"The receipt carries counts, not content: the diff never crosses this boundary and there is " +
			"no field on the payload it could occupy. It is on the machine that produced it.",
	})
	return true
}
