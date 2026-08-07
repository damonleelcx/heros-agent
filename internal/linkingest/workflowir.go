package linkingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// workflowir.go stores and reads back the OPT-IN workflow structure.
//
// It lives beside the run-link store because it arrives through the same boundary, is scoped by the same
// authenticated tenant, and is worthless without the runs it describes. Keeping it here means one place
// answers "what has this tenant sent us", which is the question a privacy review actually asks.

// WorkflowIR is one stored structure, as the platform holds it.
type WorkflowIR struct {
	TenantID       string
	WorkflowID     string
	SourceRevision string
	IRVersion      string
	ReceivedAt     time.Time
	// CoverageVersion is the coverage table the per-node verdicts inside Nodes were computed against.
	//
	// 🔴 EMPTY means NOT REPORTED, and it must never be filled in with the platform's own table version.
	// A row written by a CLI that predates P29 has no version because it had no verdicts; dating those
	// nodes to this build's table would suppress the STALE label that exists to catch exactly that
	// mismatch, and would do it silently.
	CoverageVersion string
	Nodes           []runlink.WireIRNode
	Edges           []runlink.WireIREdge
}

// WorkflowSummary is one workflow this organization has reported, as a picker renders it.
//
// A SUMMARY rather than the whole structure: an enumeration that returned every node of every workflow
// would make opening a picker as expensive as opening a graph, and the picker draws a name and a date.
type WorkflowSummary struct {
	WorkflowID     string
	SourceRevision string
	ReceivedAt     time.Time
	Nodes          int
	Edges          int
	// CoverageVersion is EMPTY when the client reported none — never the platform's own.
	CoverageVersion string
}

// WorkflowIRStore records and reads workflow structures.
//
// Every method returns an error, reads included — the same lesson `Store` learned. A read failure and
// "this tenant never sent us a structure" are different facts with different next actions, and a store
// that cannot distinguish them makes an outage look like a customer who has not opted in.
type WorkflowIRStore interface {
	// Put records a structure, replacing any structure previously stored for the same revision.
	Put(ir WorkflowIR) error
	// Latest returns the most recently received structure for a workflow. ok=false means NONE HAS BEEN
	// SENT — never a read failure, which is the error.
	Latest(tenantID, workflowID string) (WorkflowIR, bool, error)
	// ListWorkflows returns the workflows this tenant has reported, newest first (P29 §4.1).
	//
	// 🔴 This is what replaces `studio.WorkflowCatalog` on the console-facing path. That catalog is a
	// process-local map filled only by `cmd/demo` and `cmd/proof`, so `GET /api/v1/workflows` answered an
	// empty list on every real deployment — permanently, with the studio's picker empty for a reason no
	// screen stated.
	ListWorkflows(tenantID string) ([]WorkflowSummary, error)
}

// PGWorkflowIRStore is the durable store, backed by migration 0021.
type PGWorkflowIRStore struct{ db *sql.DB }

// NewPGWorkflowIRStore returns a store over an open Postgres handle.
func NewPGWorkflowIRStore(db *sql.DB) *PGWorkflowIRStore { return &PGWorkflowIRStore{db: db} }

// Put upserts on (tenant, workflow, revision).
//
// 🔴 Replace, not append. A developer re-running `discover` at the same commit has not produced a second
// workflow; two rows for one revision would make "which structure is this graph drawn from" unanswerable
// and the answer would depend on insertion order.
func (p *PGWorkflowIRStore) Put(ir WorkflowIR) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodes, err := json.Marshal(ir.Nodes)
	if err != nil {
		return fmt.Errorf("linkingest: encode nodes: %w", err)
	}
	edges, err := json.Marshal(ir.Edges)
	if err != nil {
		return fmt.Errorf("linkingest: encode edges: %w", err)
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO workflow_ir (tenant_id, workflow_id, source_revision, ir_version, received_at,
		                          nodes_json, edges_json, coverage_version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (tenant_id, workflow_id, source_revision) DO UPDATE
		   SET ir_version = EXCLUDED.ir_version,
		       received_at = EXCLUDED.received_at,
		       nodes_json = EXCLUDED.nodes_json,
		       edges_json = EXCLUDED.edges_json,
		       coverage_version = EXCLUDED.coverage_version`,
		ir.TenantID, ir.WorkflowID, ir.SourceRevision, ir.IRVersion, ir.ReceivedAt, nodes, edges,
		// NULL, not "", when the client reported none. The two read back differently and only one of
		// them is true of a pre-P29 client.
		nullIfEmpty(ir.CoverageVersion))
	if err != nil {
		return fmt.Errorf("linkingest: put workflow ir %s: %w", ir.WorkflowID, err)
	}
	return nil
}

// Latest returns the newest structure for a workflow, or ok=false when none was ever sent.
func (p *PGWorkflowIRStore) Latest(tenantID, workflowID string) (WorkflowIR, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var ir WorkflowIR
	var nodes, edges []byte
	// 🔴 sql.NullString, not string. A NULL `coverage_version` means the client reported none, and
	// scanning it into a plain string would turn it into "" — indistinguishable from a client that
	// reported an empty version, and the projection would have no way to render `not reported`.
	var coverage sql.NullString
	err := p.db.QueryRowContext(ctx,
		`SELECT tenant_id, workflow_id, source_revision, ir_version, received_at, nodes_json, edges_json,
		        coverage_version
		   FROM workflow_ir WHERE tenant_id = $1 AND workflow_id = $2
		  ORDER BY received_at DESC LIMIT 1`, tenantID, workflowID).
		Scan(&ir.TenantID, &ir.WorkflowID, &ir.SourceRevision, &ir.IRVersion, &ir.ReceivedAt, &nodes, &edges,
			&coverage)
	switch {
	case err == sql.ErrNoRows:
		return WorkflowIR{}, false, nil
	case err != nil:
		return WorkflowIR{}, false, fmt.Errorf("linkingest: get workflow ir %s: %w", workflowID, err)
	}
	if err := json.Unmarshal(nodes, &ir.Nodes); err != nil {
		return WorkflowIR{}, false, fmt.Errorf("linkingest: decode nodes for %s: %w", workflowID, err)
	}
	if err := json.Unmarshal(edges, &ir.Edges); err != nil {
		return WorkflowIR{}, false, fmt.Errorf("linkingest: decode edges for %s: %w", workflowID, err)
	}
	if coverage.Valid {
		ir.CoverageVersion = coverage.String
	}
	return ir, true, nil
}

// ListWorkflows returns the tenant's reported workflows, newest first.
//
// One row per WORKFLOW, not per revision: the picker asks "which workflows do I have", and a workflow
// reported at four revisions is one entry whose revision is the newest. Two rows would make a picker
// show the same name four times with no way to tell them apart.
func (p *PGWorkflowIRStore) ListWorkflows(tenantID string) ([]WorkflowSummary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx,
		`SELECT DISTINCT ON (workflow_id)
		        workflow_id, source_revision, received_at, coverage_version,
		        jsonb_array_length(nodes_json), jsonb_array_length(edges_json)
		   FROM workflow_ir
		  WHERE tenant_id = $1
		  ORDER BY workflow_id, received_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("linkingest: list workflows for %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()

	out := []WorkflowSummary{}
	for rows.Next() {
		var ws WorkflowSummary
		var coverage sql.NullString
		if err := rows.Scan(&ws.WorkflowID, &ws.SourceRevision, &ws.ReceivedAt, &coverage,
			&ws.Nodes, &ws.Edges); err != nil {
			return nil, fmt.Errorf("linkingest: list workflows for %s: %w", tenantID, err)
		}
		if coverage.Valid {
			ws.CoverageVersion = coverage.String
		}
		out = append(out, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("linkingest: list workflows for %s: %w", tenantID, err)
	}
	// Newest first. `DISTINCT ON` forces an ORDER BY starting with the distinct key, so the final
	// ordering the caller wants is applied here rather than left to the query.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ReceivedAt.Equal(out[j].ReceivedAt) {
			return out[i].ReceivedAt.After(out[j].ReceivedAt)
		}
		return out[i].WorkflowID < out[j].WorkflowID
	})
	return out, nil
}

// MemWorkflowIRStore is the in-memory store, for tests and demos.
type MemWorkflowIRStore struct{ m map[string]WorkflowIR }

// NewMemWorkflowIRStore returns an empty in-memory store.
func NewMemWorkflowIRStore() *MemWorkflowIRStore {
	return &MemWorkflowIRStore{m: map[string]WorkflowIR{}}
}

func (s *MemWorkflowIRStore) Put(ir WorkflowIR) error {
	s.m[ir.TenantID+"\x00"+ir.WorkflowID] = ir
	return nil
}

func (s *MemWorkflowIRStore) Latest(tenantID, workflowID string) (WorkflowIR, bool, error) {
	ir, ok := s.m[tenantID+"\x00"+workflowID]
	return ir, ok, nil
}

// ListWorkflows returns the tenant's reported workflows, newest first.
func (s *MemWorkflowIRStore) ListWorkflows(tenantID string) ([]WorkflowSummary, error) {
	out := []WorkflowSummary{}
	for _, ir := range s.m {
		// 🔴 Scoped here as well as in the PG query. An in-memory store that returned everything would
		// make a cross-tenant leak invisible in every test that uses it — which is most of them.
		if ir.TenantID != tenantID {
			continue
		}
		out = append(out, WorkflowSummary{
			WorkflowID: ir.WorkflowID, SourceRevision: ir.SourceRevision, ReceivedAt: ir.ReceivedAt,
			Nodes: len(ir.Nodes), Edges: len(ir.Edges), CoverageVersion: ir.CoverageVersion,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ReceivedAt.Equal(out[j].ReceivedAt) {
			return out[i].ReceivedAt.After(out[j].ReceivedAt)
		}
		return out[i].WorkflowID < out[j].WorkflowID
	})
	return out, nil
}
