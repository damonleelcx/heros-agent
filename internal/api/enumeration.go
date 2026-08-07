package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/linkingest"
)

// enumeration.go answers "what does this organization have?" — for workflows, variants and transforms.
//
// # The defect this closes
//
// `web/console/src/lib/subjects.ts` says it in its own words: *"the platform exposes no enumeration
// endpoint for any of them"*. So every picker in the console offered only the subjects THIS BROWSER
// SESSION had already opened — a developer who linked a run, closed the tab and came back found a
// console that had forgotten their workflow existed. The data was durable the whole time; nothing could
// ask for it.
//
// # 🔴 Three states, never one
//
// Every list below answers one of exactly three ways, because collapsing any pair of them is how a
// release reads as data loss:
//
//	200 {"state":"empty"}        you have none. A fact about YOUR data.
//	502 {"state":"read-failed"}  we could not read. A fact about US, and the customer's next action is
//	                             to wait, not to go looking for data they never lost.
//	503 {"state":"not-mounted"}  this deployment does not carry the capability. A POLICY answer.
//
// An empty list is reserved for the first of those and is never used for the other two. The fence
// `TestAReadFailureNeverRendersAsAnEmptyList` holds it.
//
// # Scope
//
// Every query below takes the tenant from the AUTHENTICATED PRINCIPAL. There is no organization
// parameter in any position, so there is nothing to get wrong — and a subject belonging to another
// organization is absent from the list AND answers by id exactly as a nonexistent one does, so the
// endpoint is not an existence oracle.

// EnumerationState is what an enumeration says about itself when it has nothing to show.
type EnumerationState string

const (
	// StateOK — the list is the answer.
	StateOK EnumerationState = "ok"
	// StateEmpty — this organization has none of these. A fact about the customer's data.
	StateEmpty EnumerationState = "empty"
	// StateReadFailed — the platform could not read. A fact about the platform.
	StateReadFailed EnumerationState = "read-failed"
	// StateNotMounted — this deployment does not carry the capability.
	StateNotMounted EnumerationState = "not-mounted"
)

// WorkflowIRIndex is the read side of the reported-structure store: which workflows has this
// organization told us about?
//
// A separate interface from `WorkflowIRSource` (which is the ingest's write side) so a deployment that
// accepts structure and one that lists it are two mounts — and so this file cannot accidentally acquire
// a `Put`.
type WorkflowIRIndex interface {
	ListWorkflows(tenantID string) ([]linkingest.WorkflowSummary, error)
}

// MountEnumeration registers the subject-index routes. Call after New.
//
// 🔴 `GET /api/v1/workflows` is REGISTERED HERE and no longer by `MountStudioMatrix`. The matrix's
// version read `studio.WorkflowCatalog`, a PROCESS-LOCAL map filled only by `cmd/demo` and `cmd/proof`
// — so on every real deployment it answered an empty list, forever, and the studio's workflow picker
// had nothing in it for a reason no screen stated. The catalog is still what the DEMO binaries use; it
// is no longer on the console-facing path.
func (s *Server) MountEnumeration(ir WorkflowIRIndex, runs linkingest.Store, receipts TransformReceiptSource) {
	s.workflowIndex = ir
	s.linkedRuns = runs
	if s.transformReceipts == nil {
		s.transformReceipts = receipts
	}
	s.Mux.HandleFunc("GET /api/v1/workflows", s.handleListWorkflows)
	s.Mux.HandleFunc("GET /api/v1/variants", s.handleListVariants)
	s.Mux.HandleFunc("GET /api/v1/transforms", s.handleListTransforms)
}

// enumPrincipal applies the guards every enumeration shares.
func enumPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "listing requires an authenticated tenant"})
		return auth.Principal{}, false
	}
	return p, true
}

// notMounted answers the policy state.
func notMounted(w http.ResponseWriter, what string) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"state": StateNotMounted,
		"error": "this deployment does not carry " + what,
	})
}

// readFailed answers the platform state. 🔴 It NEVER returns an items array: a caller that reads
// `items` without checking `state` must not be handed an empty one, because the whole point of this
// state is that we do not know whether the list is empty.
func readFailed(w http.ResponseWriter, what string, err error) {
	writeJSON(w, http.StatusBadGateway, map[string]any{
		"state": StateReadFailed,
		"error": "could not read " + what + ": " + err.Error(),
	})
}

// listBody wraps a result with the state that qualifies it.
func listBody(key string, items any, n int) map[string]any {
	state := StateOK
	if n == 0 {
		state = StateEmpty
	}
	return map[string]any{"state": state, key: items, "count": n}
}

// WorkflowListItem is one workflow this organization has reported.
type WorkflowListItem struct {
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	// Revision12 is the display form, so a revision reads identically on every surface.
	Revision12 string `json:"source_revision_display"`
	ReportedAt string `json:"reported_at"`
	Nodes      int    `json:"nodes"`
	Edges      int    `json:"edges"`
	// CoverageVersion is ABSENT when the client reported none. The console renders that distinctly; it
	// is never filled in with the platform's own table version.
	CoverageVersion string `json:"coverage_version,omitempty"`
}

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.workflowIndex == nil {
		notMounted(w, "a hosted workflow catalog")
		return
	}
	p, ok := enumPrincipal(w, r)
	if !ok {
		return
	}
	rows, err := s.workflowIndex.ListWorkflows(p.TenantID)
	if err != nil {
		readFailed(w, "this organization's workflows", err)
		return
	}
	items := make([]WorkflowListItem, 0, len(rows))
	for _, x := range rows {
		items = append(items, WorkflowListItem{
			WorkflowID: x.WorkflowID, SourceRevision: x.SourceRevision,
			Revision12: shortHash12(x.SourceRevision),
			ReportedAt: x.ReceivedAt.UTC().Format(time.RFC3339),
			Nodes:      x.Nodes, Edges: x.Edges, CoverageVersion: x.CoverageVersion,
		})
	}
	writeJSON(w, http.StatusOK, listBody("workflows", items, len(items)))
}

// VariantListItem is one variant — a configuration this organization has reported a run for.
//
// 🚫 DERIVED, not a table. A variant IS a config_hash that has runs; there is nothing to store that is
// not already in `run_link`, and `careful-table-creation` forbids a table whose entire content is
// derivable. The grouping RULE lives here in Go rather than in SQL for the reason
// `linkingest.Store.ForWorkflow` gives: a rule in SQL is a rule nobody can test without a database.
type VariantListItem struct {
	ConfigHash   string `json:"config_hash"`
	ConfigHash12 string `json:"config_hash_display"`
	WorkflowID   string `json:"workflow_id"`
	Runs         int    `json:"runs"`
	LatestRunID  string `json:"latest_run_id"`
	LatestLinked string `json:"latest_linked_at"`
	// SourceRevisions is how many revisions this configuration has been run at. A COUNT, because the
	// list of them is a different question and this row does not pretend to answer it.
	SourceRevisions int `json:"source_revisions"`
}

func (s *Server) handleListVariants(w http.ResponseWriter, r *http.Request) {
	if s.linkedRuns == nil {
		notMounted(w, "run linking")
		return
	}
	p, ok := enumPrincipal(w, r)
	if !ok {
		return
	}
	// A generous bound rather than the caller's: this is a GROUPING, and paging the input would produce
	// a variant whose run count depends on the page size — a number that changes when nothing changed.
	runs, err := s.linkedRuns.ListForTenant(p.TenantID, 1000, time.Time{})
	if err != nil {
		readFailed(w, "this organization's variants", err)
		return
	}
	byHash := map[string]*VariantListItem{}
	revs := map[string]map[string]bool{}
	for _, lr := range runs {
		v, ok := byHash[lr.ConfigHash]
		if !ok {
			v = &VariantListItem{
				ConfigHash: lr.ConfigHash, ConfigHash12: shortHash12(lr.ConfigHash),
				WorkflowID: lr.WorkflowID,
			}
			byHash[lr.ConfigHash] = v
			revs[lr.ConfigHash] = map[string]bool{}
		}
		v.Runs++
		revs[lr.ConfigHash][lr.SourceRevision] = true
		// The list is newest-first, so the first row for a hash is its latest.
		if v.LatestRunID == "" {
			v.LatestRunID = lr.RunID
			v.LatestLinked = lr.LinkedAt.UTC().Format(time.RFC3339)
		}
	}
	items := make([]VariantListItem, 0, len(byHash))
	for h, v := range byHash {
		v.SourceRevisions = len(revs[h])
		items = append(items, *v)
	}
	// Sorted by recency then by hash: map order would reshuffle the picker between reloads for no
	// reason a user could explain.
	sort.Slice(items, func(i, j int) bool {
		if items[i].LatestLinked != items[j].LatestLinked {
			return items[i].LatestLinked > items[j].LatestLinked
		}
		return items[i].ConfigHash < items[j].ConfigHash
	})
	writeJSON(w, http.StatusOK, listBody("variants", items, len(items)))
}

// TransformListItem is one transform receipt this organization has reported.
type TransformListItem struct {
	ConfigHash      string `json:"config_hash"`
	ConfigHash12    string `json:"config_hash_display"`
	SourceRevision  string `json:"source_revision"`
	Revision12      string `json:"source_revision_display"`
	WorkflowID      string `json:"workflow_id"`
	Status          string `json:"status"`
	ReportedAt      string `json:"reported_at"`
	FilesChanged    int    `json:"files_changed"`
	LinesAdded      int    `json:"lines_added"`
	LinesRemoved    int    `json:"lines_removed"`
	Applied         int    `json:"nodes_applied"`
	Refused         int    `json:"nodes_refused"`
	CoverageVersion string `json:"coverage_version,omitempty"`
}

func (s *Server) handleListTransforms(w http.ResponseWriter, r *http.Request) {
	if s.transformReceipts == nil {
		notMounted(w, "transform receipts")
		return
	}
	p, ok := enumPrincipal(w, r)
	if !ok {
		return
	}
	limit := 100
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := s.transformReceipts.ListForTenant(p.TenantID, limit)
	if err != nil {
		readFailed(w, "this organization's transforms", err)
		return
	}
	items := make([]TransformListItem, 0, len(rows))
	for _, x := range rows {
		it := TransformListItem{
			ConfigHash: x.ConfigHash, ConfigHash12: shortHash12(x.ConfigHash),
			SourceRevision: x.SourceRevision, Revision12: shortHash12(x.SourceRevision),
			WorkflowID: x.WorkflowID, Status: x.Status,
			ReportedAt:   x.ReceivedAt.UTC().Format(time.RFC3339),
			FilesChanged: x.FilesChanged, LinesAdded: x.LinesAdded, LinesRemoved: x.LinesRemoved,
			CoverageVersion: x.CoverageVersion,
		}
		for _, o := range x.NodeOutcomes {
			switch o.Outcome {
			case "applied":
				it.Applied++
			case "refused":
				it.Refused++
			}
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, listBody("transforms", items, len(items)))
}
