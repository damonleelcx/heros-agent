package herosagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// inferencestore.go is the durable InferenceStore over migration 0046.

// PGInferenceStore is the pinned-inference store.
type PGInferenceStore struct{ db *sql.DB }

// NewPGInferenceStore returns a store over an open Postgres handle.
func NewPGInferenceStore(db *sql.DB) (*PGInferenceStore, error) {
	if db == nil {
		return nil, errors.New("herosagent: nil database")
	}
	return &PGInferenceStore{db: db}, nil
}

func (s *PGInferenceStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 15*time.Second)
}

// Get reads a stored inference by its three-part key. ok=false is NOT INFERRED — never an error.
func (s *PGInferenceStore) Get(parent context.Context, workflowID, sourceRevision, hash string) (Stored, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()

	var st Stored
	var edges, labels string
	var narrative sql.NullString
	// 🔴 NULL-able, and read into a NullString rather than a string. A row written before per-node
	// attribution existed has no record, and NULL means NOT RECORDED — see the migration's header for
	// why it is not backfilled. `sql.NullString` is what keeps that distinguishable from `[]`.
	var nodes sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT inference_id, tenant_id, workflow_id, source_revision, agent_config_hash, placement,
		        edges_json, labels_json, narrative, tokens_in, tokens_out, created_at_ms, nodes_json
		   FROM heros_inference
		  WHERE workflow_id = $1 AND source_revision = $2 AND agent_config_hash = $3`,
		workflowID, sourceRevision, hash).
		Scan(&st.InferenceID, &st.TenantID, &st.WorkflowID, &st.SourceRevision, &st.AgentConfigHash,
			&st.Placement, &edges, &labels, &narrative, &st.TokensIn, &st.TokensOut, &st.CreatedAtMS,
			&nodes)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Stored{}, false, nil
	case err != nil:
		return Stored{}, false, fmt.Errorf("herosagent: reading the inference for %s@%s: %w",
			workflowID, sourceRevision, err)
	}
	if err := json.Unmarshal([]byte(edges), &st.Edges); err != nil {
		return Stored{}, false, fmt.Errorf("herosagent: decoding edges for %s: %w", st.InferenceID, err)
	}
	if err := json.Unmarshal([]byte(labels), &st.Labels); err != nil {
		return Stored{}, false, fmt.Errorf("herosagent: decoding labels for %s: %w", st.InferenceID, err)
	}
	st.Narrative = narrative.String
	if nodes.Valid && nodes.String != "" {
		if err := json.Unmarshal([]byte(nodes.String), &st.Nodes); err != nil {
			// 🔴 LOUD. A per-node record that will not decode is a provenance answer this deployment
			// cannot give, and returning the inference without it would render "which node produced
			// this" as "one node, unattributed" — a claim rather than an absence.
			return Stored{}, false, fmt.Errorf("herosagent: decoding the per-node record for %s: %w",
				st.InferenceID, err)
		}
	}

	abst, err := s.abstentions(ctx, st.InferenceID)
	if err != nil {
		return Stored{}, false, err
	}
	st.Abstentions = abst
	return st, true, nil
}

func (s *PGInferenceStore) abstentions(ctx context.Context, inferenceID string) ([]Abstention, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT subject, reason, confidence FROM heros_abstention WHERE inference_id = $1
		  ORDER BY subject, reason`, inferenceID)
	if err != nil {
		return nil, fmt.Errorf("herosagent: reading abstentions for %s: %w", inferenceID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []Abstention{}
	for rows.Next() {
		var a Abstention
		var reason string
		var conf sql.NullFloat64
		if err := rows.Scan(&a.Subject, &reason, &conf); err != nil {
			return nil, err
		}
		a.Reason = AbstentionReason(reason)
		if conf.Valid {
			// A pointer, so "declined at 0.0" stays distinguishable from "declined with no candidate".
			v := conf.Float64
			a.Confidence = &v
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Put records an inference IDEMPOTENTLY on the three-part key.
//
// 🔴 `ON CONFLICT DO NOTHING` on the unique index, and the conflict is NOT an error to the caller
// (task 2.5's writer path). Two runners that raced produced answers for one key; the row that won is
// the answer, and failing the loser would turn a benign race into an analysis failure a customer sees.
//
// 🚫 It never OVERWRITES. Replacing a pinned result is a separate, confirmed operation — see Replace.
func (s *PGInferenceStore) Put(parent context.Context, st Stored) error {
	ctx, cancel := s.ctx(parent)
	defer cancel()

	edges, labels, err := encodeFacts(st)
	if err != nil {
		return err
	}
	nodesJSON, err := encodeNodeRuns(st)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("herosagent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO heros_inference
		   (inference_id, workflow_id, source_revision, agent_config_hash, tenant_id, placement,
		    edges_json, labels_json, narrative, tokens_in, tokens_out, created_at_ms, nodes_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 ON CONFLICT ON CONSTRAINT uq_heros_inference_key DO NOTHING`,
		st.InferenceID, st.WorkflowID, st.SourceRevision, st.AgentConfigHash, st.TenantID,
		st.Placement, edges, labels, nullString(st.Narrative),
		st.TokensIn, st.TokensOut, st.CreatedAtMS, nodesJSON)
	if err != nil {
		return fmt.Errorf("herosagent: recording inference %s: %w", st.InferenceID, err)
	}
	// Zero rows means the other runner won. Its abstentions belong to ITS inference id, so writing ours
	// against a row that does not exist would violate the foreign key — and would attribute our
	// refusals to somebody else's run.
	if n, _ := res.RowsAffected(); n == 0 {
		return tx.Commit()
	}
	if err := writeAbstentions(ctx, tx, st); err != nil {
		return err
	}
	return tx.Commit()
}

// Replace overwrites a pinned inference after a CONFIRMED re-inference (task 4.8).
func (s *PGInferenceStore) Replace(parent context.Context, st Stored) error {
	ctx, cancel := s.ctx(parent)
	defer cancel()

	edges, labels, err := encodeFacts(st)
	if err != nil {
		return err
	}
	nodesJSON, err := encodeNodeRuns(st)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("herosagent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE heros_inference
		    SET edges_json = $1, labels_json = $2, narrative = $3,
		        tokens_in = $4, tokens_out = $5, created_at_ms = $6, nodes_json = $8
		  WHERE inference_id = $7`,
		edges, labels, nullString(st.Narrative), st.TokensIn, st.TokensOut,
		st.CreatedAtMS, st.InferenceID, nodesJSON); err != nil {
		return fmt.Errorf("herosagent: replacing inference %s: %w", st.InferenceID, err)
	}
	// Abstentions are REPLACED, not merged: a re-inference that declined different things must not
	// accumulate the previous run's refusals, which would report a decline nobody made this time.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM heros_abstention WHERE inference_id = $1`, st.InferenceID); err != nil {
		return fmt.Errorf("herosagent: clearing abstentions for %s: %w", st.InferenceID, err)
	}
	if err := writeAbstentions(ctx, tx, st); err != nil {
		return err
	}
	return tx.Commit()
}

func encodeFacts(st Stored) (string, string, error) {
	edges, err := json.Marshal(nonNilEdges(st.Edges))
	if err != nil {
		return "", "", fmt.Errorf("herosagent: encoding edges: %w", err)
	}
	labels, err := json.Marshal(st.Labels)
	if err != nil {
		return "", "", fmt.Errorf("herosagent: encoding labels: %w", err)
	}
	return string(edges), string(labels), nil
}

// encodeNodeRuns renders the per-node record, or SQL NULL when there is none.
//
// 🔴 `nil` rather than `"[]"` for an inference with no per-node record. NULL means NOT RECORDED; `[]`
// would mean "recorded, and no node ran", which is a different and false claim. The distinction is what
// keeps a row written before this column existed distinguishable from a definition that produced
// nothing — see the migration's header.
func encodeNodeRuns(st Stored) (any, error) {
	if len(st.Nodes) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(st.Nodes)
	if err != nil {
		return nil, fmt.Errorf("herosagent: encoding the per-node record: %w", err)
	}
	return string(b), nil
}

func nonNilEdges(e []ProvenancedEdge) []ProvenancedEdge {
	if e == nil {
		return []ProvenancedEdge{}
	}
	return e
}

func writeAbstentions(ctx context.Context, tx *sql.Tx, st Stored) error {
	for _, a := range st.Abstentions {
		var conf any
		if a.Confidence != nil {
			conf = *a.Confidence
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO heros_abstention (inference_id, subject, reason, confidence)
			 VALUES ($1,$2,$3,$4)
			 ON CONFLICT (inference_id, subject, reason) DO NOTHING`,
			st.InferenceID, a.Subject, string(a.Reason), conf); err != nil {
			return fmt.Errorf("herosagent: recording abstention %s/%s: %w", a.Subject, a.Reason, err)
		}
	}
	return nil
}

// ByAuthor reports how many stored inferences a tenant has, and their token totals — the read the
// spend meter and the `/agent` overview both need.
func (s *PGInferenceStore) CountFor(parent context.Context, tenantID string) (int, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	var n int
	q := `SELECT count(*) FROM heros_inference`
	args := []any{}
	if strings.TrimSpace(tenantID) != "" {
		q += ` WHERE tenant_id = $1`
		args = append(args, tenantID)
	}
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("herosagent: counting inferences: %w", err)
	}
	return n, nil
}

// LatestFor returns the most recently created inference for one workflow (task 8.2's narrative read).
//
// Scoped by TENANT as well as workflow, and that is not belt-and-braces: workflow ids are chosen by
// customers and they collide. A query on `workflow_id` alone would serve one organization's narrative
// on another's graph page, and nothing in the response would look wrong.
func (s *PGInferenceStore) LatestFor(parent context.Context, tenantID, workflowID string) (Stored, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()

	var st Stored
	var edges, labels string
	var narrative sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT inference_id, tenant_id, workflow_id, source_revision, agent_config_hash, placement,
		        edges_json, labels_json, narrative, tokens_in, tokens_out, created_at_ms
		   FROM heros_inference
		  WHERE tenant_id = $1 AND workflow_id = $2
		  ORDER BY created_at_ms DESC, inference_id DESC
		  LIMIT 1`,
		tenantID, workflowID).
		Scan(&st.InferenceID, &st.TenantID, &st.WorkflowID, &st.SourceRevision, &st.AgentConfigHash,
			&st.Placement, &edges, &labels, &narrative, &st.TokensIn, &st.TokensOut, &st.CreatedAtMS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Stored{}, false, nil
	case err != nil:
		return Stored{}, false, fmt.Errorf("herosagent: reading the latest inference for %s/%s: %w",
			tenantID, workflowID, err)
	}
	st.Narrative = narrative.String
	// 🚫 Edges and labels are deliberately NOT decoded here. This read exists for the narrative, and a
	// caller that wanted the facts would be addressing them by D2's three-part key rather than by "the
	// latest" — decoding them would make this look like a second way to obtain an inference.
	return st, true, nil
}
