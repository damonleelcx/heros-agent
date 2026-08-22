package assessment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// store.go is the durable Store over migration 0050.
//
// # 🔴 Why the write is one transaction and not ten statements
//
// A report missing three findings validates against nothing and renders as a report. `INSERT OR IGNORE`
// is the specific hazard the schema-code coherence rule names: a column added to the finding record
// without the migration means rows are swallowed while the endpoint keeps returning 200, and the only
// place the loss is visible is a `SELECT` nobody runs. So the insert names every column explicitly,
// there is no `ON CONFLICT DO NOTHING` on the finding rows, and the nine land together or none do.
//
// # Why `Put` re-validates
//
// `Runner.Run` validates before calling. This validates again, because `Store` is an interface a future
// caller can reach directly and because a store that trusts its caller is a store that writes whatever
// the worst caller sends. It is three lines.

// PGStore is the assessment store over Postgres.
type PGStore struct{ db *sql.DB }

// NewPGStore returns a store over an open Postgres handle.
func NewPGStore(db *sql.DB) (*PGStore, error) {
	if db == nil {
		return nil, errors.New("assessment: nil database")
	}
	return &PGStore{db: db}, nil
}

func (s *PGStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 15*time.Second)
}

// Put writes the assessment and its nine findings atomically.
func (s *PGStore) Put(parent context.Context, a Assessment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assessment: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// ON CONFLICT DO NOTHING on the RUN row only, and it is not an error to the caller: two runners
	// that raced produced a report for one id, and the row that won is the report. Failing the loser
	// would turn a benign race into an assessment failure a customer sees. The findings below are
	// then written against the winner's id and are byte-identical to the loser's by FR15, which is
	// what makes this safe rather than merely convenient.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO assessment
		   (assessment_id, tenant_id, workflow_id, source_revision, agent_config_hash,
		    started_at_ms, completed_at_ms, spend_usd, spend_cap_usd)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (assessment_id) DO NOTHING`,
		a.AssessmentID, a.TenantID, a.WorkflowID, a.SourceRevision, a.AgentConfigHash,
		a.StartedAtMS, a.CompletedAtMS, a.SpendUSD, a.SpendCapUSD); err != nil {
		return fmt.Errorf("assessment: writing %s: %w", a.AssessmentID, err)
	}

	for _, f := range a.Findings {
		var evalJSON any
		if f.Eval() != nil {
			b, err := json.Marshal(f.Eval())
			if err != nil {
				return fmt.Errorf("assessment: encoding the eval-set report for %s: %w", f.Axis(), err)
			}
			evalJSON = string(b)
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO assessment_finding
			   (assessment_id, axis, state, origin, claim,
			    evidence_surface, evidence_locator, evidence_fragment,
			    missing_input, refusal_cause, provider_model_version, inference_address, eval_set_json)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			 ON CONFLICT (assessment_id, axis) DO NOTHING`,
			a.AssessmentID, string(f.Axis()), string(f.State()), string(f.Origin()), f.Claim(),
			string(f.Evidence().Surface), f.Evidence().Locator, nullable(f.Evidence().Fragment),
			nullable(string(f.MissingInput())), nullable(string(f.RefusalCause())),
			nullable(f.ProviderModelVersion()), nullable(f.InferenceAddress()), evalJSON)
		if err != nil {
			return fmt.Errorf("assessment: writing the %s finding of %s: %w", f.Axis(), a.AssessmentID, err)
		}
		// 🔴 The rows-affected check is the whole point of the schema-code coherence rule. A conflict
		// here is NOT benign the way the run row's is: it means a finding for this axis already
		// exists, so either the report is being written twice with different content or a column
		// mismatch swallowed the row — and both look identical to a caller reading the error, which
		// is to say identical to success.
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("assessment: counting the write of the %s finding: %w", f.Axis(), err)
		}
		if n != 1 {
			return fmt.Errorf("assessment: the %s finding of %s was not written (%d rows). A finding "+
				"silently not written is a report that renders eight axes and claims nine",
				f.Axis(), a.AssessmentID, n)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("assessment: commit %s: %w", a.AssessmentID, err)
	}
	return nil
}

// Get reads one assessment back. ok=false is NOT FOUND — never an error.
//
// 🔴 It decodes through the same constructors a writer used, so a row that violates a conditional
// requirement fails the READ rather than rendering. That is deliberate and it is the uncomfortable
// choice: the alternative is to render whatever is in the table, and "render whatever is there" is how
// a `not_measured` finding with a NULL missing input reaches a customer as a blank row.
func (s *PGStore) Get(parent context.Context, tenantID, assessmentID string) (Assessment, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()

	var a Assessment
	err := s.db.QueryRowContext(ctx,
		`SELECT assessment_id, tenant_id, workflow_id, source_revision, agent_config_hash,
		        started_at_ms, completed_at_ms, spend_usd, spend_cap_usd
		   FROM assessment WHERE tenant_id = $1 AND assessment_id = $2`, tenantID, assessmentID).
		Scan(&a.AssessmentID, &a.TenantID, &a.WorkflowID, &a.SourceRevision, &a.AgentConfigHash,
			&a.StartedAtMS, &a.CompletedAtMS, &a.SpendUSD, &a.SpendCapUSD)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Assessment{}, false, nil
	case err != nil:
		return Assessment{}, false, fmt.Errorf("assessment: reading %s: %w", assessmentID, err)
	}

	findings, err := s.findings(ctx, assessmentID)
	if err != nil {
		return Assessment{}, false, err
	}
	a.Findings = findings
	if err := a.Validate(); err != nil {
		return Assessment{}, false, err
	}
	return a, true, nil
}

// Latest reads the newest assessment of one workflow.
func (s *PGStore) Latest(parent context.Context, tenantID, workflowID string) (Assessment, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT assessment_id FROM assessment
		  WHERE tenant_id = $1 AND workflow_id = $2
		  ORDER BY started_at_ms DESC LIMIT 1`, tenantID, workflowID).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Assessment{}, false, nil
	case err != nil:
		return Assessment{}, false, fmt.Errorf("assessment: reading the latest assessment of %s: %w", workflowID, err)
	}
	return s.Get(parent, tenantID, id)
}

// FindPin returns the assessment already held for a `(workflow, revision, config)`, if any.
//
// This is what makes FR15 cheap rather than merely true: a repeat assessment that cannot find its
// predecessor is a repeat assessment that pays for the inference again.
func (s *PGStore) FindPin(parent context.Context, tenantID, workflowID, revision, configHash string) (Assessment, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT assessment_id FROM assessment
		  WHERE tenant_id = $1 AND workflow_id = $2 AND source_revision = $3 AND agent_config_hash = $4
		  ORDER BY started_at_ms DESC LIMIT 1`, tenantID, workflowID, revision, configHash).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Assessment{}, false, nil
	case err != nil:
		return Assessment{}, false, fmt.Errorf("assessment: looking up the pin for %s@%s: %w", workflowID, revision, err)
	}
	return s.Get(parent, tenantID, id)
}

func (s *PGStore) findings(ctx context.Context, assessmentID string) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT axis, state, origin, claim, evidence_surface, evidence_locator, evidence_fragment,
		        missing_input, refusal_cause, provider_model_version, inference_address, eval_set_json
		   FROM assessment_finding WHERE assessment_id = $1 ORDER BY axis`, assessmentID)
	if err != nil {
		return nil, fmt.Errorf("assessment: reading the findings of %s: %w", assessmentID, err)
	}
	defer func() { _ = rows.Close() }()

	out := []Finding{}
	for rows.Next() {
		var (
			axis, state, origin, claim, surface, locator string
			fragment, missing, refusal, model, address   sql.NullString
			evalRaw                                      sql.NullString
		)
		if err := rows.Scan(&axis, &state, &origin, &claim, &surface, &locator, &fragment,
			&missing, &refusal, &model, &address, &evalRaw); err != nil {
			return nil, err
		}
		f := Finding{
			axis: Axis(axis), state: State(state), origin: Origin(origin), claim: claim,
			evidence:     EvidenceRef{Surface: Surface(surface), Locator: locator, Fragment: fragment.String},
			missingInput: MissingInput(missing.String), refusalCause: RefusalCause(refusal.String),
			providerModelVersion: model.String, inferenceAddress: address.String,
		}
		if evalRaw.Valid {
			var report EvalSetReport
			if err := json.Unmarshal([]byte(evalRaw.String), &report); err != nil {
				return nil, fmt.Errorf("assessment: decoding the eval-set report of %s/%s: %w", assessmentID, axis, err)
			}
			f.eval = &report
		}
		if err := f.Validate(); err != nil {
			return nil, fmt.Errorf("assessment: the stored %s finding of %s is invalid: %w", axis, assessmentID, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AxisStateCount is one cell of DevOps task 6.1's breakdown.
type AxisStateCount struct {
	Axis  Axis  `json:"axis"`
	State State `json:"state"`
	Count int64 `json:"count"`
}

// AxisStateBreakdown counts findings per axis and per state over a window.
//
// 🔴 Per axis AND per state, never a total. §9.6: *"How many assessments produced nine `not_measured`
// findings" is the single best early signal that a frontend or the sandbox broke, and it is invisible
// in an aggregate success rate.* A total would be the aggregate that hides the broken part — the
// failure mode this whole phase is named after.
func (s *PGStore) AxisStateBreakdown(parent context.Context, sinceMS int64) ([]AxisStateCount, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.axis, f.state, COUNT(*)
		   FROM assessment_finding f JOIN assessment a ON a.assessment_id = f.assessment_id
		  WHERE a.started_at_ms >= $1
		  GROUP BY f.axis, f.state
		  ORDER BY f.axis, f.state`, sinceMS)
	if err != nil {
		return nil, fmt.Errorf("assessment: counting findings per axis and state: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []AxisStateCount{}
	for rows.Next() {
		var c AxisStateCount
		if err := rows.Scan(&c.Axis, &c.State, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AllNotMeasuredRate counts assessments in a window and how many of them returned NINE
// `not_measured` findings — DevOps task 6.2.
//
// Two numbers rather than a ratio, because a ratio of 1.0 over one assessment and a ratio of 1.0 over
// four hundred are different emergencies, and a single float cannot say which one is happening.
func (s *PGStore) AllNotMeasuredRate(parent context.Context, sinceMS int64) (total, allNotMeasured int64, err error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COUNT(*) FILTER (WHERE nm = $2)
		   FROM (
		     SELECT a.assessment_id,
		            COUNT(*) FILTER (WHERE f.state = 'not_measured') AS nm
		       FROM assessment a JOIN assessment_finding f ON f.assessment_id = a.assessment_id
		      WHERE a.started_at_ms >= $1
		      GROUP BY a.assessment_id
		   ) per_assessment`, sinceMS, len(Axes())).Scan(&total, &allNotMeasured)
	if err != nil {
		return 0, 0, fmt.Errorf("assessment: counting all-not-measured assessments: %w", err)
	}
	return total, allNotMeasured, nil
}

// nullable turns "" into a SQL NULL. The schema's conditional CHECKs are stated over NULL, and an
// empty string is not NULL — a `refused` finding writing `”` into `missing_input` would fail the
// equivalence constraint with an error naming a column the caller never set.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
