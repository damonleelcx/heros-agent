package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/heros-foreal/agentd/internal/arrangements"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The P5 interactive graph editor's read+validate surface. The riskiest interaction in the product, so
// the UNHAPPY PATH is first-class: a candidate reorder is validated through the SAME typed-contract
// check the runtime enforces, BEFORE any commit and before any codemod is generated (ADR-001). An
// invalid reorder is legible — the mismatch is anchored to the offending edge, an adaptable mismatch
// previews the adapter AND the source diff it would generate, and an unadaptable one is explained in
// plain language and never committed.
//
// Three routes, one read model:
//   GET  /p5/editor?workflow_id=…                     the editor page (self-contained HTML, embedded)
//   GET  /api/p5/workflows/{workflow_id}/ir           the IR read model (nodes + edges + io_contracts)
//   POST /api/p5/workflows/{workflow_id}/validate     validate a candidate ordering → verdict + preview
//
// The validate route is PURE: it never persists. It is the "validate before commit" gate the editor
// calls on every edit.

//go:embed static/p5editor.html
var p5EditorHTML []byte

// P5Source is the read model the editor serves: the Workflow IR whose ordering the user re-arranges.
// An interface so the API depends on no concrete store and a test can stub it.
type P5Source interface {
	IR(workflowID string) (*discovery.IR, bool)
}

// MountP5 registers the editor routes. Call after New.
func (s *Server) MountP5(src P5Source) {
	s.p5 = src
	s.Mux.HandleFunc("GET /p5/editor", s.handleP5UI)
	s.Mux.HandleFunc("GET /api/p5/workflows/{workflow_id}/ir", s.handleP5IR)
	s.Mux.HandleFunc("POST /api/p5/workflows/{workflow_id}/validate", s.handleP5Validate)
	s.Mux.HandleFunc("POST /api/p5/workflows/{workflow_id}/commit", s.handleP5Commit)
	s.Mux.HandleFunc("POST /api/p5/workflows/{workflow_id}/orderings", s.handleP5Orderings)
	s.Mux.HandleFunc("POST /api/p5/workflows/{workflow_id}/orderings/stream", s.handleP5OrderingsStream)
}

func (s *Server) handleP5UI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p5EditorHTML)
}

// irNodeView is the per-node projection the editor renders: identity plus the io_contract it needs to
// explain a mismatch. The raw schemas are passed through so the client can show which fields a rejected
// edge is about.
type irNodeView struct {
	NodeID       string         `json:"node_id"`
	Kind         string         `json:"kind"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
}

type irView struct {
	WorkflowID string       `json:"workflow_id"`
	Language   string       `json:"language"`
	Order      []string     `json:"order"`
	Nodes      []irNodeView `json:"nodes"`
	Edges      []edgeView   `json:"edges"`
}

type edgeView struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind"`
	// Provenance / Confidence / Signal are the recovered-topology fields (P4.5, reused by P5 for
	// non-graph agents). Empty Provenance means a framework-certain edge; anything else is a RECOVERED
	// hypothesis the editor must render as inferred, never as certain.
	Provenance string  `json:"provenance,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Signal     string  `json:"signal,omitempty"`
}

func (s *Server) handleP5IR(w http.ResponseWriter, r *http.Request) {
	if s.p5 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P5 editor is not mounted"})
		return
	}
	ir, ok := s.p5.IR(r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such workflow"})
		return
	}
	writeJSON(w, http.StatusOK, irToView(r.PathValue("workflow_id"), ir))
}

func irToView(workflowID string, ir *discovery.IR) irView {
	v := irView{WorkflowID: workflowID, Language: ir.Workflow.Language}
	for _, n := range ir.Nodes {
		v.Order = append(v.Order, n.NodeID)
		v.Nodes = append(v.Nodes, irNodeView{
			NodeID: n.NodeID, Kind: n.Kind,
			InputSchema: n.IOContract.InputSchema, OutputSchema: n.IOContract.OutputSchema,
		})
	}
	for _, e := range ir.Edges {
		v.Edges = append(v.Edges, edgeView{
			FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind,
			Provenance: e.Provenance, Confidence: e.Confidence, Signal: e.Signal,
		})
	}
	return v
}

// validateRequest is the candidate ordering the editor submits on every edit.
type validateRequest struct {
	Order []string   `json:"order"`
	Edges []edgeView `json:"edges"`
}

// adapterView is the editor's adapter-inserted preview: the adapter it would insert, named for what it
// does, PLUS the source diff that insertion would generate (task 3.3). preview_diff is empty and
// preview_error set when the target language cannot receive a generated adapter yet.
type adapterView struct {
	FromNodeID   string         `json:"from_node_id"`
	ToNodeID     string         `json:"to_node_id"`
	Kind         string         `json:"kind"`
	Params       map[string]any `json:"params"`
	InSchema     map[string]any `json:"in_schema"`
	OutSchema    map[string]any `json:"out_schema"`
	PreviewPath  string         `json:"preview_path,omitempty"`
	PreviewDiff  string         `json:"preview_diff,omitempty"`
	PreviewError string         `json:"preview_error,omitempty"`
}

type diagnosticView struct {
	FromNodeID string   `json:"from_node_id"`
	ToNodeID   string   `json:"to_node_id"`
	Reason     string   `json:"reason"`
	Fields     []string `json:"fields"`
	Message    string   `json:"message"`
}

// validateResponse mirrors the total verdict. `kind` is coherent | adapted | rejected — the same
// partition the validator returns — so the client's first-class states map one-to-one.
type validateResponse struct {
	Kind        string           `json:"kind"`
	Adapters    []adapterView    `json:"adapters,omitempty"`
	Diagnostics []diagnosticView `json:"diagnostics,omitempty"`
	// Announcement is the plain-language line the client hands to a screen-reader live region (task 3.4).
	Announcement string `json:"announcement"`
}

func (s *Server) handleP5Validate(w http.ResponseWriter, r *http.Request) {
	if s.p5 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P5 editor is not mounted"})
		return
	}
	ir, ok := s.p5.IR(r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such workflow"})
		return
	}
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed candidate ordering"})
		return
	}

	ordering := typedcontract.Ordering{Order: req.Order}
	for _, e := range req.Edges {
		ordering.Edges = append(ordering.Edges, typedcontract.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	verdict := typedcontract.ValidateOrdering(ir, ordering, typedcontract.DefaultCatalog())
	writeJSON(w, http.StatusOK, verdictToResponse(verdict, ir.Workflow.Language))
}

// commitResponse is returned by the commit route. On a coherent/adapted edit it carries the new
// lineage-tracked Variant Spec (parent_variant_id + inserted adapters) AND the reviewable source diff
// the codemod would generate (task 3.6). On a rejected edit it carries the verdict and NO diff — the
// "no source diff/PR is generated for an incoherent ordering" guarantee (task 3.2). `status` is
// committed | rejected | rejected_transform.
type commitResponse struct {
	Status       string           `json:"status"`
	ParentID     string           `json:"parent_variant_id,omitempty"`
	Order        []string         `json:"order,omitempty"`
	Adapters     []adapterView    `json:"adapters,omitempty"`
	Diff         string           `json:"diff,omitempty"`
	DiffHash     string           `json:"diff_hash,omitempty"`
	Diagnostics  []diagnosticView `json:"diagnostics,omitempty"`
	BuildError   string           `json:"build_error,omitempty"`
	Announcement string           `json:"announcement"`
}

// commitRequest carries the candidate ordering plus the parent's identity for lineage.
type commitRequest struct {
	Order    []string   `json:"order"`
	Edges    []edgeView `json:"edges"`
	ParentID string     `json:"parent_variant_id"`
}

func (s *Server) handleP5Commit(w http.ResponseWriter, r *http.Request) {
	if s.p5 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P5 editor is not mounted"})
		return
	}
	ir, ok := s.p5.IR(r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such workflow"})
		return
	}
	var req commitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed candidate ordering"})
		return
	}

	// Build the candidate spec and GATE it through the validator BEFORE generating any transform
	// (task 3.2, ADR-001). A rejected ordering yields no runnable spec and no diff.
	parent := &variantspec.VariantSpec{WorkflowID: r.PathValue("workflow_id"), SourceRevision: ir.Workflow.Repo.CommitSHA}
	edges := make([]variantspec.Edge, 0, len(req.Edges))
	for _, e := range req.Edges {
		edges = append(edges, variantspec.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	cand := variantspec.Reorder(parent, req.ParentID, req.Order, edges)
	spec, verdict := variantspec.GateReorder(ir, cand, typedcontract.DefaultCatalog())
	if spec == nil {
		resp := commitResponse{Status: "rejected",
			Announcement: "Commit blocked: this reorder breaks a data contract, so no source diff was generated."}
		for _, d := range verdict.Diagnostics {
			resp.Diagnostics = append(resp.Diagnostics, diagnosticView{
				FromNodeID: d.FromNodeID, ToNodeID: d.ToNodeID, Reason: string(d.Reason), Fields: d.Fields, Message: d.Message})
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Coherent/adapted → generate the reviewable source diff. A build failure would surface here as a
	// rejected_transform (the build gate itself runs in the worktree layer at apply time — §2).
	// DiscoveredWiring is what the SOURCE wires, carried so the transform engine can tell an edit that
	// only inserts an adapter (materializable — it emits generated source) from one that rearranges the
	// graph (P15 task 4.2: refused, because no rewriter moves a call). Without it the engine would have
	// no evidence to compare against and would emit an adapter-only diff for a reorder, letting the new
	// config_hash be scored against source that was never rewired.
	resolved := &variantspec.Resolved{
		Language:         ir.Workflow.Language,
		SourceRevision:   parent.SourceRevision,
		DiscoveredWiring: variantspec.WiringOf(ir),
	}
	patch, err := transform.GenerateTransform(resolved, spec, "")
	if err != nil {
		writeJSON(w, http.StatusOK, commitResponse{Status: "rejected_transform", BuildError: err.Error(),
			Announcement: "The reorder is contract-coherent but its codemod could not be generated; it is a rejected transform and was not applied."})
		return
	}
	resp := commitResponse{
		Status: "committed", ParentID: spec.ParentVariantID, Order: spec.Order,
		Diff: string(patch.Diff), DiffHash: patch.DiffHash,
		Announcement: "Committed. A new Variant Spec with lineage and a reviewable source diff was produced; it must build before it is proposed.",
	}
	for _, a := range spec.InsertedAdapters {
		resp.Adapters = append(resp.Adapters, adapterView{
			FromNodeID: a.FromNodeID, ToNodeID: a.ToNodeID, Kind: a.CatalogKind, Params: a.Params,
			InSchema: a.InSchema, OutSchema: a.OutSchema})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleP5Orderings enumerates every ordering of the workflow's nodes, validates and scores each, and
// returns them ranked: approved (coherent / adapter-augmented) first, rejected below, each group by
// score. Enumeration is bounded and the truncation is surfaced (no silent cap). It never persists.
func (s *Server) handleP5Orderings(w http.ResponseWriter, r *http.Request) {
	if s.p5 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P5 editor is not mounted"})
		return
	}
	ir, ok := s.p5.IR(r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such workflow"})
		return
	}
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed request"})
		return
	}
	edges := make([]typedcontract.Edge, 0, len(req.Edges))
	for _, e := range req.Edges {
		edges = append(edges, typedcontract.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	rank := arrangements.Enumerate(ir, req.Order, edges, typedcontract.DefaultCatalog(), 0)
	writeJSON(w, http.StatusOK, rank)
}

// handleP5OrderingsStream streams arrangements as they are DISCOVERED (task: live discovery animation).
// It responds with newline-delimited JSON (application/x-ndjson): first a "meta" line, then one
// "arrangement" line per ordering as it is validated (flushed immediately so the client can animate its
// arrival), then a "done" line with the final summary. The client inserts each arrangement into its
// ranked position on arrival, so the ranked list builds up live. It never persists.
func (s *Server) handleP5OrderingsStream(w http.ResponseWriter, r *http.Request) {
	if s.p5 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the P5 editor is not mounted"})
		return
	}
	ir, ok := s.p5.IR(r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such workflow"})
		return
	}
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed request"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without flushing there is no live stream; fall back to the batch endpoint's shape so the
		// feature degrades to "all at once" rather than failing.
		s.handleP5Orderings(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering so events arrive as they are written

	edges := make([]typedcontract.Edge, 0, len(req.Edges))
	for _, e := range req.Edges {
		edges = append(edges, typedcontract.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	enc := json.NewEncoder(w)
	// meta line up front: total space + cap, so the client can render "considered N of M" as it streams.
	total := factorialTotal(len(req.Order))
	_ = enc.Encode(map[string]any{"type": "meta", "total": total, "cap": arrangements.DefaultCap})
	flusher.Flush()

	ctx := r.Context()
	aborted := false
	sum := arrangements.EnumerateStream(ir, req.Order, edges, typedcontract.DefaultCatalog(), 0, func(a arrangements.Arrangement) {
		if aborted {
			return
		}
		select {
		case <-ctx.Done(): // client navigated away — stop enumerating rather than burn CPU
			aborted = true
			return
		default:
		}
		line := map[string]any{
			"type": "arrangement", "order": a.Order, "kind": string(a.Kind), "score": a.Score,
			"approved": a.Approved, "adapter_count": a.AdapterCount, "rejected_edge_count": a.RejectedEdgeCount,
		}
		if enc.Encode(line) != nil {
			aborted = true
			return
		}
		flusher.Flush()
	})
	if !aborted {
		_ = enc.Encode(map[string]any{"type": "done", "considered": sum.Considered, "truncated": sum.Truncated,
			"approved_count": sum.ApprovedCount, "rejected_count": sum.RejectedCount, "cap": sum.Cap})
		flusher.Flush()
	}
}

// factorialTotal reports n! for the meta line, or -1 when too large to represent.
func factorialTotal(n int) int64 {
	if n < 0 || n > 20 {
		if n > 20 {
			return -1
		}
		return 0
	}
	out := int64(1)
	for i := 2; i <= n; i++ {
		out *= int64(i)
	}
	return out
}

func verdictToResponse(verdict typedcontract.Verdict, language string) validateResponse {
	resp := validateResponse{Kind: string(verdict.Kind)}
	switch verdict.Kind {
	case typedcontract.VerdictCoherent:
		resp.Announcement = "Coherent: every data edge satisfies its contract. Safe to commit."
	case typedcontract.VerdictAdapted:
		for _, ins := range verdict.Adapters {
			av := adapterView{
				FromNodeID: ins.FromNodeID, ToNodeID: ins.ToNodeID, Kind: string(ins.Adapter.Kind),
				Params: ins.Adapter.Params, InSchema: ins.Adapter.InSchema, OutSchema: ins.Adapter.OutSchema,
			}
			// Preview the source diff the adapter insertion would generate.
			inserted := variantspec.InsertedAdapter{
				AdapterNodeID: "adapter:" + string(ins.Adapter.Kind) + ":" + ins.FromNodeID + "->" + ins.ToNodeID,
				FromNodeID:    ins.FromNodeID, ToNodeID: ins.ToNodeID, CatalogKind: string(ins.Adapter.Kind),
				Params: ins.Adapter.Params, InSchema: ins.Adapter.InSchema, OutSchema: ins.Adapter.OutSchema,
			}
			if path, diff, err := transform.PreviewAdapterDiff(inserted, language); err != nil {
				av.PreviewError = err.Error()
			} else {
				av.PreviewPath, av.PreviewDiff = path, diff
			}
			resp.Adapters = append(resp.Adapters, av)
		}
		resp.Announcement = "Adapter-inserted: the reorder is coherent once an adapter is added. Review the previewed adapter and source diff, then accept or decline."
	case typedcontract.VerdictRejected:
		for _, d := range verdict.Diagnostics {
			resp.Diagnostics = append(resp.Diagnostics, diagnosticView{
				FromNodeID: d.FromNodeID, ToNodeID: d.ToNodeID, Reason: string(d.Reason),
				Fields: d.Fields, Message: d.Message,
			})
		}
		sort.Slice(resp.Diagnostics, func(i, j int) bool {
			if resp.Diagnostics[i].FromNodeID != resp.Diagnostics[j].FromNodeID {
				return resp.Diagnostics[i].FromNodeID < resp.Diagnostics[j].FromNodeID
			}
			return resp.Diagnostics[i].ToNodeID < resp.Diagnostics[j].ToNodeID
		})
		resp.Announcement = "Rejected: this reorder breaks a data contract. Commit is blocked. See the flagged edge for what would break."
	}
	return resp
}
