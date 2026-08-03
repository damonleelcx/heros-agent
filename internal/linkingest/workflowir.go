package linkingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	Nodes          []runlink.WireIRNode
	Edges          []runlink.WireIREdge
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
		`INSERT INTO workflow_ir (tenant_id, workflow_id, source_revision, ir_version, received_at, nodes_json, edges_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (tenant_id, workflow_id, source_revision) DO UPDATE
		   SET ir_version = EXCLUDED.ir_version,
		       received_at = EXCLUDED.received_at,
		       nodes_json = EXCLUDED.nodes_json,
		       edges_json = EXCLUDED.edges_json`,
		ir.TenantID, ir.WorkflowID, ir.SourceRevision, ir.IRVersion, ir.ReceivedAt, nodes, edges)
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
	err := p.db.QueryRowContext(ctx,
		`SELECT tenant_id, workflow_id, source_revision, ir_version, received_at, nodes_json, edges_json
		   FROM workflow_ir WHERE tenant_id = $1 AND workflow_id = $2
		  ORDER BY received_at DESC LIMIT 1`, tenantID, workflowID).
		Scan(&ir.TenantID, &ir.WorkflowID, &ir.SourceRevision, &ir.IRVersion, &ir.ReceivedAt, &nodes, &edges)
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
	return ir, true, nil
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
