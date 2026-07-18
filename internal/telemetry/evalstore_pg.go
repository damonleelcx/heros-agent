package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// PGEvalStore is the Postgres EvalStore — the real backing for quality-metric events (Decision 5). It
// writes eval_result rows whose seven tag columns are NOT NULL and whose FKs to config/variant/node/
// case hold, so the tagging + lineage invariants are enforced a second time at the row (the emission
// gate is the first; task 2.2's "layered with the DB").
type PGEvalStore struct{ db *sql.DB }

// NewPGEvalStore builds the Postgres eval store.
func NewPGEvalStore(db *sql.DB) *PGEvalStore { return &PGEvalStore{db: db} }

// PutEval writes one quality-metric event. workflow_id (eval_result's structural FK helper) is derived
// from the config row rather than carried on the event, so QualityMetricEvent stays the pure seven-tag
// shape and lineage supplies the rest. Idempotent: a redelivery collides on the natural key and is a
// no-op (ON CONFLICT DO NOTHING), so a re-scored run does not double-write.
func (s *PGEvalStore) PutEval(ctx context.Context, ev QualityMetricEvent) error {
	if err := ev.Validate(); err != nil {
		// Defense in depth: even reaching the store, an under-tagged event is refused before the INSERT.
		return fmt.Errorf("telemetry: PGEvalStore refused an under-tagged event: %w", err)
	}
	if ev.EvaluatorName == "" {
		return fmt.Errorf("telemetry: PGEvalStore requires evaluator_name (an unnamed evaluator's output is unattributable)")
	}
	ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
	if err != nil {
		if ts, err = time.Parse(time.RFC3339, ev.Timestamp); err != nil {
			return fmt.Errorf("telemetry: PGEvalStore bad timestamp %q: %w", ev.Timestamp, err)
		}
	}
	var inBlob, outBlob any
	if ev.InputBlobHash != "" {
		inBlob = ev.InputBlobHash
	}
	if ev.OutputBlobHash != "" {
		outBlob = ev.OutputBlobHash
	}

	// workflow_id from config lineage; the (workflow_id, node_id) FK then resolves. If the config_hash
	// is not in `config`, the subquery yields NULL and the NOT NULL/FK rejects the row loudly — a run
	// for a configuration nobody recorded is not writable, which is the point.
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO eval_result
		   (config_hash, variant_id, run_id, node_id, case_id, seed, ts, workflow_id,
		    metric_name, value, unit, evaluator_name, input_blob_hash, output_blob_hash)
		 SELECT $1, $2, $3, $4, $5, $6, $7, c.workflow_id,
		        $8, $9, $10, $11, $12, $13
		 FROM config c WHERE c.config_hash = $1
		 ON CONFLICT ON CONSTRAINT eval_result_natural_key DO NOTHING`,
		ev.ConfigHash, ev.VariantID, ev.RunID, ev.NodeID, ev.CaseID, seedOf(ev.Event), ts,
		ev.MetricName, valueOf(ev.Event), ev.Unit, ev.EvaluatorName, inBlob, outBlob)
	if err != nil {
		return fmt.Errorf("telemetry: PGEvalStore insert %s/%s: %w", ev.RunID, ev.MetricName, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("telemetry: PGEvalStore rows affected: %w", err)
	}
	if n == 0 {
		// Either an idempotent redelivery (natural-key conflict) or the config_hash was not in `config`
		// so the SELECT produced no row. These are very different; distinguish them rather than silently
		// call a non-write a success (the fail-loud stance the whole store family takes).
		var exists bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM config WHERE config_hash=$1)`, ev.ConfigHash).Scan(&exists); err != nil {
			return fmt.Errorf("telemetry: PGEvalStore config check: %w", err)
		}
		if !exists {
			return fmt.Errorf("telemetry: PGEvalStore: no config row for config_hash %s — the event is unattributable", ev.ConfigHash)
		}
		// config exists -> the 0 rows is an idempotent redelivery of an already-written result.
	}
	return nil
}

// ByConfigHash returns every quality-metric row for a configuration (comparison query, task 5.4),
// filtered by config_hash.
func (s *PGEvalStore) ByConfigHash(ctx context.Context, configHash string) ([]QualityMetricEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT config_hash, variant_id, run_id, node_id, case_id, seed, ts,
		        metric_name, value, unit, evaluator_name,
		        coalesce(input_blob_hash,''), coalesce(output_blob_hash,'')
		 FROM eval_result WHERE config_hash = $1
		 ORDER BY evaluator_name, node_id, case_id, metric_name`, configHash)
	if err != nil {
		return nil, fmt.Errorf("telemetry: PGEvalStore query by config_hash: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []QualityMetricEvent
	for rows.Next() {
		var (
			qm    QualityMetricEvent
			seed  int64
			ts    time.Time
			value float64
		)
		if err := rows.Scan(&qm.ConfigHash, &qm.VariantID, &qm.RunID, &qm.NodeID, &qm.CaseID, &seed, &ts,
			&qm.MetricName, &value, &qm.Unit, &qm.EvaluatorName, &qm.InputBlobHash, &qm.OutputBlobHash); err != nil {
			return nil, fmt.Errorf("telemetry: PGEvalStore scan: %w", err)
		}
		qm.SchemaVersion = metricevent.SchemaVersion
		qm.Seed = &seed
		qm.Timestamp = ts.UTC().Format(time.RFC3339Nano)
		qm.Value = &value
		out = append(out, qm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("telemetry: PGEvalStore rows: %w", err)
	}
	return out, nil
}

func seedOf(ev metricevent.Event) int64 {
	if ev.Seed == nil {
		return -1 // will fail the seed >= 0 CHECK loudly rather than write a wrong 0
	}
	return *ev.Seed
}
