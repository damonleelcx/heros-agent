package hostdiscovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
)

// store.go holds the GraphStore implementations: Postgres (migration 0022) and in-memory for tests.

// PGGraphStore is the durable graph store.
type PGGraphStore struct{ db *sql.DB }

// NewPGGraphStore returns a store over an open Postgres handle.
func NewPGGraphStore(db *sql.DB) *PGGraphStore { return &PGGraphStore{db: db} }

// Put upserts on (tenant, workflow, revision).
//
// Replace, not append — migration 0021's rule, which this table inherits: re-running discovery at the
// same commit has not produced a second workflow, and two rows for one revision would make "which tree
// is this graph drawn from" depend on insertion order.
func (p *PGGraphStore) Put(ctx context.Context, g Graph) error {
	view, err := json.Marshal(g.View)
	if err != nil {
		return fmt.Errorf("hostdiscovery: encode graph view: %w", err)
	}
	// 🔴 DERIVED HERE, from the document being written, and never taken from the caller. An index that
	// can disagree with what it indexes is worse than no index, because it is believed. See
	// provenance.go, and TestTheStoredProvenanceIndexCannotDriftFromTheDocument.
	prov := ProvenanceOf(g.View)
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO platform_workflow_graph
		   (tenant_id, workflow_id, source_revision, ir_version, taxonomy_version, discovered_at,
		    llm_calls, view_json, provenance)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (tenant_id, workflow_id, source_revision) DO UPDATE
		   SET ir_version       = EXCLUDED.ir_version,
		       taxonomy_version = EXCLUDED.taxonomy_version,
		       discovered_at    = EXCLUDED.discovered_at,
		       llm_calls        = EXCLUDED.llm_calls,
		       view_json        = EXCLUDED.view_json,
		       provenance       = EXCLUDED.provenance`,
		g.TenantID, g.WorkflowID, g.SourceRevision, g.IRVersion, g.TaxonomyVersion,
		g.DiscoveredAt, g.LLMCalls, view, nullProvenance(prov))
	if err != nil {
		return fmt.Errorf("hostdiscovery: put graph %s/%s: %w", g.WorkflowID, g.SourceRevision, err)
	}
	return nil
}

// Latest returns the newest graph for a workflow, or ok=false when none has been discovered.
func (p *PGGraphStore) Latest(ctx context.Context, tenantID, workflowID string) (Graph, bool, error) {
	var g Graph
	var view []byte
	// NULL provenance is a PRE-P30 ROW, not an error and not an empty set of a different kind. It is
	// scanned into a sql.NullString and resolved below, so the two spellings a reader could receive —
	// SQL NULL and the empty string — collapse to one before anything branches on them.
	var prov sql.NullString
	err := p.db.QueryRowContext(ctx,
		`SELECT tenant_id, workflow_id, source_revision, ir_version, taxonomy_version,
		        discovered_at, llm_calls, view_json, provenance
		   FROM platform_workflow_graph
		  WHERE tenant_id = $1 AND workflow_id = $2
		  ORDER BY discovered_at DESC LIMIT 1`, tenantID, workflowID).
		Scan(&g.TenantID, &g.WorkflowID, &g.SourceRevision, &g.IRVersion, &g.TaxonomyVersion,
			&g.DiscoveredAt, &g.LLMCalls, &view, &prov)
	switch {
	case err == sql.ErrNoRows:
		return Graph{}, false, nil
	case err != nil:
		return Graph{}, false, fmt.Errorf("hostdiscovery: get graph %s: %w", workflowID, err)
	}
	if err := json.Unmarshal(view, &g.View); err != nil {
		return Graph{}, false, fmt.Errorf("hostdiscovery: decode graph view for %s: %w", workflowID, err)
	}
	g.Provenance = prov.String
	return g, true, nil
}

// nullProvenance writes the empty index as SQL NULL.
//
// 🔴 NULL and '' are the SAME STATE here — "this row's facts name no author" — and storing both would
// make `WHERE provenance IS NULL` answer a different question from `WHERE provenance = ''` for rows
// that mean the same thing. NULL is the spelling migration 0045 documents as `legacy`, so it is the
// one used, and the reader maps it back through discovery.AuthorOf.
func nullProvenance(p string) any {
	if p == "" {
		return nil
	}
	return p
}

// MemGraphStore is the in-memory store, for tests and demos.
//
// Concurrency-safe, unlike some of its neighbours: the runner is called from HTTP handlers, and a store
// that races only under load is a store that passes every test and fails in production.
type MemGraphStore struct {
	mu sync.RWMutex
	m  map[string]Graph
}

// NewMemGraphStore returns an empty in-memory store.
func NewMemGraphStore() *MemGraphStore { return &MemGraphStore{m: map[string]Graph{}} }

func (s *MemGraphStore) key(tenantID, workflowID string) string {
	return tenantID + "\x00" + workflowID
}

func (s *MemGraphStore) Put(_ context.Context, g Graph) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keyed by (tenant, workflow) rather than including the revision: this store answers Latest, and
	// keeping every revision in memory would grow without bound in the one implementation that has no
	// eviction. The durable store keeps them all.
	s.m[s.key(g.TenantID, g.WorkflowID)] = g
	return nil
}

func (s *MemGraphStore) Latest(_ context.Context, tenantID, workflowID string) (Graph, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.m[s.key(tenantID, workflowID)]
	return g, ok, nil
}
