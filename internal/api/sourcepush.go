package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// sourcepush.go is the authenticated ingest for a customer-pushed source snapshot — the input that lets
// the platform run discovery itself instead of drawing whatever shape it was handed.
//
// # This endpoint accepts something the others refuse
//
// Every other ingest in this package narrows: the run link and the opt-in structure both decode into a
// named wire struct with DisallowUnknownFields, so a key nobody ratified cannot arrive. This one takes
// an opaque archive of the customer's SOURCE. There is no allowlist to apply, because the payload is not
// a set of fields — it is their repository.
//
// 🔴 So the control is different in kind, and it has to be, or it is not a control at all:
//
//   - The transfer is EXPLICIT. It happens because someone ran a command naming a revision. There is no
//     background sync and no standing credential that reads their repo without them.
//   - What is KEPT is the conclusion, not the evidence. internal/hostdiscovery extracts the tree, runs
//     discovery and the classifier, stores the resulting graph, and releases the tree — the bundle the
//     customer pushed is the only source that persists, and DELETE below removes it.
//   - The archive is HOSTILE INPUT until proven otherwise. internal/sourceingest refuses traversal
//     entries, links, device nodes and decompression bombs, with a test per rule.
//
// A deployment that does not want to hold customer source simply does not mount this. Unmounted, the
// route answers 503 — "this platform does not accept source" is a policy answer a customer can read,
// where a 404 would read as a broken URL.

// SourcePushStore is the write side of source ingest: accept a bundle, or forget one.
//
// Narrower than sourceingest.BundleStore on purpose — the API has no business reading bundles back, and
// an interface that cannot express a read is one that cannot accidentally grow a handler that serves a
// customer's source to anyone who asks.
type SourcePushStore interface {
	Put(ctx context.Context, ref sourceingest.Ref, data []byte) error
	Delete(ctx context.Context, ref sourceingest.Ref) error
}

// MaxSourceBundleUpload caps the compressed upload at 512 MiB.
//
// This is the COMPRESSED ceiling and it is not the same control as sourceingest's uncompressed caps: a
// 512 MiB gzip can expand to far more, which is exactly what a decompression bomb is, and that is
// bounded during extraction where the expansion is actually observable. Both limits exist because
// neither one subsumes the other.
const MaxSourceBundleUpload = 512 << 20

// SourceDiscovery runs platform-side discovery over a pushed snapshot and stores the classified graph.
//
// Separate from SourcePushStore so a deployment can accept snapshots without running discovery over
// them, or — more usefully — so the store can be mounted when discovery cannot be. They fail for
// different reasons: a store needs a database, a runner needs a skill registry.
type SourceDiscovery interface {
	// Discover returns a summary of what was found. It must return an error wrapping
	// sourceingest.ErrNoSource when nothing was pushed for the ref.
	Discover(ctx context.Context, ref sourceingest.Ref) (DiscoverySummary, error)
}

// DiscoverySummary is what a caller is told about a completed discovery.
//
// A SUMMARY, not the graph: the graph is read from the pattern-graph route, which already exists and
// already applies tenant scoping. Returning it here would be a second, parallel way to read a workflow's
// shape, and two readers of one fact drift.
type DiscoverySummary struct {
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	Nodes          int    `json:"nodes"`
	Edges          int    `json:"edges"`
	// Labelled and Unclassified are reported SEPARATELY rather than as a single coverage percentage.
	// A percentage would let "we classified nothing" and "there was nothing to classify" print the same
	// number, and those are the two outcomes a user most needs to tell apart.
	Labelled     int `json:"labelled_regions"`
	Unclassified int `json:"unclassified_regions"`
	// LLMCalls is 0 when the run was fully rule-covered. Surfaced so "deterministic" is a fact the
	// caller can check rather than a property they are asked to assume.
	LLMCalls int `json:"llm_calls"`
}

// MountSourcePush registers source-snapshot ingest. Call after New.
//
// discovery may be nil: the snapshot routes still work, and the discover route answers 503.
//
// P29 · `PUT|DELETE /api/v1/workflow-source` are the flat routes the CLI addresses. The parameterised
// ones stay registered and ExposureInternal for one release.
//
// 🔴 The identifiers travel in HEADERS here, and only here. Every other flat route in this change moved
// its identifier into the JSON payload, because there was a JSON payload to move it into. This body is an
// opaque gzip archive — the whole point of the endpoint — so there is no field to occupy, and the two
// remaining places an identifier could live are the query string and a header. The query string is
// REFUSED by `runlink.IsLinkTarget` (a destination must be fully readable from a compiled-in constant, and
// a query string is a way to carry bytes past that), which leaves the header. Named
// `X-Heros-Workflow-Id` and `X-Heros-Source-Revision`, validated by the same `sourceingest.Ref.Validate`
// the path form uses, so the two shapes cannot diverge in what they accept.
func (s *Server) MountSourcePush(store SourcePushStore, discovery SourceDiscovery) {
	s.sourcePush = store
	s.sourceDiscovery = discovery
	s.Mux.HandleFunc("PUT /api/v1/workflow-source", s.handleSourcePush)
	s.Mux.HandleFunc("DELETE /api/v1/workflow-source", s.handleSourceDelete)
	// The fifth machine-addressed path, and the one nobody had counted. `RunDiscovery` used to build its
	// URL from the value `sourceURL` returned, so it appeared in no scan and no manifest: it is the exact
	// failure mode this whole wave exists to close, found while closing it.
	s.Mux.HandleFunc("POST /api/v1/workflow-source-discovery", s.handleSourceDiscover)
	s.Mux.HandleFunc("PUT /api/v1/workflows/{workflow_id}/source/{source_revision}", s.handleSourcePush)
	s.Mux.HandleFunc("DELETE /api/v1/workflows/{workflow_id}/source/{source_revision}", s.handleSourceDelete)
	s.Mux.HandleFunc("POST /api/v1/workflows/{workflow_id}/source/{source_revision}/discover", s.handleSourceDiscover)
}

// SourceWorkflowHeader and SourceRevisionHeader name the workflow revision a flat source request applies
// to. Exported so the transport and the tests read the same constants the handler does.
const (
	SourceWorkflowHeader = "X-Heros-Workflow-Id"
	SourceRevisionHeader = "X-Heros-Source-Revision"
)

// handleSourceDiscover runs discovery over an already-pushed snapshot.
//
// SYNCHRONOUS, and that is a considered choice rather than a shortcut. Discovery is static analysis —
// it parses, it never executes the target repo (discovery invariant I1) — so it completes in seconds on
// a real repository, and a caller that waits gets a real answer instead of a job id it must poll. The
// honest limit: a very large repository will hold a request open, and when that becomes the common case
// this becomes a queued job with the summary moved behind a status route. It is not that today, and
// building the queue first would be building for a load nobody has.
func (s *Server) handleSourceDiscover(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.sourceRefFrom(w, r)
	if !ok {
		return
	}
	if s.sourceDiscovery == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "this deployment does not run discovery"})
		return
	}
	summary, err := s.sourceDiscovery.Discover(r.Context(), ref)
	switch {
	case errors.Is(err, sourceingest.ErrNoSource):
		// 409, not 404: the workflow may well exist and the route certainly does. What is missing is the
		// snapshot, and the caller's next action is to push one — which "not found" would not tell them.
		writeJSON(w, http.StatusConflict, specError{
			Error: "no source has been pushed for this revision — push a snapshot before running discovery",
		})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, specError{Error: "discovery failed: " + err.Error()})
	default:
		writeJSON(w, http.StatusOK, summary)
	}
}

// handleSourcePush accepts a gzipped tar of the workflow's source at one revision.
func (s *Server) handleSourcePush(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.sourceRefFrom(w, r)
	if !ok {
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxSourceBundleUpload))
	if err != nil {
		// MaxBytesReader's error is indistinguishable from a truncated upload here, and the remedy
		// differs, so the message names both rather than asserting one.
		writeJSON(w, http.StatusRequestEntityTooLarge, specError{
			Error: "the source bundle could not be read in full — it may exceed the upload limit or the transfer was cut short",
		})
		return
	}
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the source bundle is empty"})
		return
	}

	if err := s.sourcePush.Put(r.Context(), ref, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: "the source bundle could not be stored"})
		return
	}
	// 202, not 200: the bundle is stored, and discovery over it has NOT run yet. Answering 200 would
	// tell a CLI that the graph is ready, and the next request would 404 on a workflow the user was
	// just told we accepted.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"workflow_id":     ref.WorkflowID,
		"source_revision": ref.SourceRevision,
		"bytes":           len(body),
	})
}

// handleSourceDelete removes a pushed snapshot.
//
// This is the retraction that makes the push reversible, and it is a first-class route rather than a
// support process for the obvious reason: "stop holding my source" must be something a customer can DO.
func (s *Server) handleSourceDelete(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.sourceRefFrom(w, r)
	if !ok {
		return
	}
	if err := s.sourcePush.Delete(r.Context(), ref); err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: "the source bundle could not be deleted"})
		return
	}
	// 204 whether or not a row was there. Reporting 404 for an absent bundle would let a caller probe
	// which revisions a tenant has pushed, and "it is not there" is the outcome the caller asked for
	// either way.
	w.WriteHeader(http.StatusNoContent)
}

// sourceRefFrom applies the guards both handlers share: mounted, authenticated, and a complete ref.
func (s *Server) sourceRefFrom(w http.ResponseWriter, r *http.Request) (sourceingest.Ref, bool) {
	if s.sourcePush == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "this deployment does not accept source snapshots"})
		return sourceingest.Ref{}, false
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "pushing source requires an authenticated tenant"})
		return sourceingest.Ref{}, false
	}
	// The tenant comes from the PRINCIPAL, never from the path, header or body. A tenant id a caller can
	// name is a tenant id a caller can change.
	//
	// On the flat route the path values are empty and the headers carry the ref. Where BOTH are present
	// and DISAGREE the request is refused — never resolved by precedence, for the reason the workflow-IR
	// and verdict handlers give: a precedence rule turns a disagreement into a silent mis-attribution.
	workflowID, revision := r.PathValue("workflow_id"), r.PathValue("source_revision")
	hWorkflow := strings.TrimSpace(r.Header.Get(SourceWorkflowHeader))
	hRevision := strings.TrimSpace(r.Header.Get(SourceRevisionHeader))
	if (workflowID != "" && hWorkflow != "" && workflowID != hWorkflow) ||
		(revision != "" && hRevision != "" && revision != hRevision) {
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "the workflow revision named in the path and in the headers disagree — refusing rather than choosing one",
		})
		return sourceingest.Ref{}, false
	}
	if workflowID == "" {
		workflowID = hWorkflow
	}
	if revision == "" {
		revision = hRevision
	}
	ref := sourceingest.Ref{
		TenantID:       principal.TenantID,
		WorkflowID:     workflowID,
		SourceRevision: revision,
	}
	if err := ref.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return sourceingest.Ref{}, false
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		return sourceingest.Ref{}, false
	}
	return ref, true
}
