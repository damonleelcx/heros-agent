// Package proposalstore is the durable home for P5.5 proposals and their verdicts.
//
// # Why a store package rather than a store inside internal/proposal
//
// The tables span two domains: `proposal`/`proposal_evidence`/`rank_entry` describe a candidate change
// (internal/proposal's subject) and `verdict` describes what measuring it found (internal/verification's).
// They are one aggregate — a verdict is meaningless without its proposal, and the FK says so — and
// putting the store in either package would make that package import the other for persistence reasons
// alone. This package depends on both and neither depends on it.
//
// # What is stored, and what is not
//
// Identity, scope, status and CONTENT HASHES. The diff bytes and the grounding bundle live in the blob
// store; migration 0012's CHECKs require those columns to be 64-hex or NULL, so an accidental inline
// payload is refused by the database rather than by review.
//
// 🔴 The verdict is written by an INGEST, never by us. The platform PROPOSES and CI VERIFIES: we write a
// proposal row as `candidate`, the customer's CI evaluates it on held-out data and reports back, and
// PutVerdict records what came back. A verdict this platform authored would be a measurement nobody
// took — and the recommendation surface reads `gate_result = 'pass'`, so authoring one is the shortest
// path to recommending an unverified change.
package proposalstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/verification"
)

// Errors this store returns.
var (
	ErrNotFound = errors.New("proposalstore: no such proposal")
	// ErrUnscoped guards the one mistake that cannot be recovered from: a row written without a tenant
	// is a proposal nobody owns, and migration 0025's CHECK refuses it — this catches it earlier, with
	// a sentence instead of a constraint violation.
	ErrUnscoped = errors.New("proposalstore: a proposal must name its tenant and workflow")
)

// Status values, mirroring 0012's CHECK. A closed set: the surface separates recommendations from
// withheld ones by reading these, and an unrecognised status would silently land in neither.
const (
	StatusCandidate          = "candidate"
	StatusBuildFailed        = "build_failed"
	StatusVerifying          = "verifying"
	StatusVerified           = "verified"
	StatusGateFailed         = "gate_failed"
	StatusConstraintExcluded = "constraint_excluded"
)

// Build statuses, mirroring 0012's CHECK.
const (
	BuildUnbuilt = "unbuilt"
	BuildBuilt   = "built"
	BuildFailed  = "build_failed"
)

// Record is one proposal as the platform holds it.
type Record struct {
	ProposalID string
	// TenantID and WorkflowID are migration 0025's scope. 0012 had neither, which is why the read model
	// could not be served from this schema at all.
	TenantID   string
	WorkflowID string

	DiagnosisID string
	Operator    string
	// NodeID, Pattern and Rationale are what the CARD renders (migration 0030). 0012 stored none of the
	// three, and a card with no node id asks a reviewer to open a pull request on faith: the whole claim
	// a proposal makes is "change THIS call site".
	NodeID              string
	Pattern             string
	Rationale           string
	BaseVariantID       string
	CandidateConfigHash string
	SourceRevision      string

	// Content hashes into the blob store. Empty means absent, and is written as NULL — 0012's CHECK
	// accepts NULL or 64-hex, so "" would be refused, which is the constraint catching an inline payload.
	SourceDiffBlobHash string
	GroundingBlobHash  string
	// SpecBlobHash addresses the candidate Variant Spec (migration 0031). "A proposal IS a candidate
	// Variant Spec" — without this the row describes a change and does not record what the change is,
	// and the codemod has nothing to apply.
	SpecBlobHash string

	BuildStatus string
	Status      string
	CreatedAt   time.Time

	// RefusalReason and RefusalDimension record the transform DECLINING this change (migration 0032).
	// A non-empty reason is what marks a proposal refused: 0012's build_status CHECK has no `refused`
	// value, and recording one as `unbuilt` makes it indistinguishable from a proposal nobody has
	// compiled yet — which is the disappearance BuildRefused was made a status to prevent.
	RefusalReason    string
	RefusalDimension string

	// Evidence is the failing cases attached to this proposal, by split role.
	Evidence []Evidence
}

// Evidence is one case attached to a proposal, tagged with the split role it plays.
//
// The role is what keeps a held-out claim honest: a delta measured on the cases that GENERATED the
// proposal proves the change fits the evidence it was built from, which is not the same as proving it
// generalises.
type Evidence struct {
	CaseID string
	Role   string // "generating" | "held_out"
}

// Store records proposals and the verdicts CI reports for them.
type Store interface {
	// Put records a proposal and its evidence. Re-putting the same proposal id replaces its row.
	Put(ctx context.Context, r Record) error
	// ForWorkflow returns one tenant's proposals for a workflow, newest first, each with its verdict
	// when one has been reported.
	ForWorkflow(ctx context.Context, tenantID, workflowID string) ([]Scored, error)
	// PutVerdict records a verdict CI reported. It refuses a verdict for a proposal this tenant does not
	// own — the ingest is authenticated, but the proposal id in the body is not.
	PutVerdict(ctx context.Context, tenantID string, v verification.Verdict) error
}

// Scored is a proposal together with the verdict CI reported for it, if any.
type Scored struct {
	Record
	// Verdict is nil until CI reports one. Nil is NOT "failed": an unverified proposal has not been
	// measured, and the surface must show it as awaiting verification rather than as rejected.
	Verdict *verification.Verdict
}

// PGStore is the durable store over migrations 0012 and 0025.
type PGStore struct{ db *sql.DB }

// NewPGStore returns a store over an open Postgres handle.
func NewPGStore(db *sql.DB) (*PGStore, error) {
	if db == nil {
		return nil, errors.New("proposalstore: nil database")
	}
	return &PGStore{db: db}, nil
}

func (p *PGStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

// Put records a proposal and replaces its evidence set.
//
// One transaction: a proposal whose evidence half-landed is a card that cites cases it cannot show, and
// the surface renders evidence as the reason to trust a recommendation.
func (p *PGStore) Put(parent context.Context, r Record) error {
	if r.TenantID == "" || r.WorkflowID == "" {
		return fmt.Errorf("%w: %s", ErrUnscoped, r.ProposalID)
	}
	if r.Status == "" {
		r.Status = StatusCandidate
	}
	if r.BuildStatus == "" {
		r.BuildStatus = BuildUnbuilt
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}

	ctx, cancel := p.ctx(parent)
	defer cancel()

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("proposalstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO proposal
		   (proposal_id, tenant_id, workflow_id, diagnosis_id, operator, base_variant_id,
		    candidate_config_hash, source_revision, source_diff_blob_hash, grounding_blob_hash,
		    build_status, status, created_at, node_id, pattern, rationale, spec_blob_hash,
		    refusal_reason, refusal_dimension)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		 ON CONFLICT (proposal_id) DO UPDATE
		   SET build_status = EXCLUDED.build_status,
		       status       = EXCLUDED.status,
		       source_diff_blob_hash = EXCLUDED.source_diff_blob_hash,
		       grounding_blob_hash   = EXCLUDED.grounding_blob_hash,
		       node_id   = EXCLUDED.node_id,
		       pattern   = EXCLUDED.pattern,
		       rationale = EXCLUDED.rationale,
		       spec_blob_hash = EXCLUDED.spec_blob_hash,
		       refusal_reason = EXCLUDED.refusal_reason,
		       refusal_dimension = EXCLUDED.refusal_dimension`,
		r.ProposalID, r.TenantID, r.WorkflowID, r.DiagnosisID, r.Operator, r.BaseVariantID,
		r.CandidateConfigHash, r.SourceRevision, nullStr(r.SourceDiffBlobHash), nullStr(r.GroundingBlobHash),
		r.BuildStatus, r.Status, r.CreatedAt.UTC(), r.NodeID, r.Pattern, r.Rationale,
		nullStr(r.SpecBlobHash), r.RefusalReason, r.RefusalDimension); err != nil {
		return fmt.Errorf("proposalstore: put %s: %w", r.ProposalID, err)
	}

	// Evidence is REPLACED, not merged: re-proposing at a new revision can legitimately cite a
	// different set, and a merge would accumulate cases the proposal no longer rests on.
	if _, err := tx.ExecContext(ctx, `DELETE FROM proposal_evidence WHERE proposal_id = $1`, r.ProposalID); err != nil {
		return fmt.Errorf("proposalstore: clear evidence for %s: %w", r.ProposalID, err)
	}
	for _, e := range r.Evidence {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO proposal_evidence (proposal_id, case_id, role) VALUES ($1,$2,$3)
			 ON CONFLICT (proposal_id, case_id) DO UPDATE SET role = EXCLUDED.role`,
			r.ProposalID, e.CaseID, e.Role); err != nil {
			return fmt.Errorf("proposalstore: evidence %s/%s: %w", r.ProposalID, e.CaseID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("proposalstore: commit %s: %w", r.ProposalID, err)
	}
	return nil
}

const proposalColumns = `p.proposal_id, p.tenant_id, p.workflow_id, p.diagnosis_id, p.operator,
	p.base_variant_id, p.candidate_config_hash, p.source_revision, p.source_diff_blob_hash,
	p.grounding_blob_hash, p.build_status, p.status, p.created_at,
	p.node_id, p.pattern, p.rationale, p.spec_blob_hash, p.refusal_reason, p.refusal_dimension`

// ForWorkflow returns a tenant's proposals for a workflow, newest first, each with its verdict.
func (p *PGStore) ForWorkflow(parent context.Context, tenantID, workflowID string) ([]Scored, error) {
	ctx, cancel := p.ctx(parent)
	defer cancel()

	// LEFT JOIN: an unverified proposal has no verdict row and must still appear. An inner join would
	// silently drop everything awaiting CI — which, on a platform that proposes and lets CI verify, is
	// most of what exists at any moment.
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+proposalColumns+`,
		        v.metric, v.delta, v.ci_low, v.ci_high, v.significant, v.held_out,
		        v.cost_delta, v.latency_delta, v.regression_pass, v.cases_fixed_json,
		        v.cases_broken_json, v.gate_result, v.cases_fixed_count, v.cases_broken_count
		   FROM proposal p
		   LEFT JOIN verdict v ON v.proposal_id = p.proposal_id
		  WHERE p.tenant_id = $1 AND p.workflow_id = $2
		  ORDER BY p.created_at DESC, p.proposal_id ASC`, tenantID, workflowID)
	if err != nil {
		return nil, fmt.Errorf("proposalstore: proposals for %s/%s: %w", tenantID, workflowID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Scored
	for rows.Next() {
		s, err := scanScored(rows)
		if err != nil {
			return nil, fmt.Errorf("proposalstore: proposals for %s/%s: %w", tenantID, workflowID, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proposalstore: proposals for %s/%s: %w", tenantID, workflowID, err)
	}
	return p.attachEvidence(ctx, out)
}

// attachEvidence loads each proposal's cited cases.
func (p *PGStore) attachEvidence(ctx context.Context, in []Scored) ([]Scored, error) {
	for i := range in {
		rows, err := p.db.QueryContext(ctx,
			`SELECT case_id, role FROM proposal_evidence WHERE proposal_id = $1 ORDER BY case_id`,
			in[i].ProposalID)
		if err != nil {
			return nil, fmt.Errorf("proposalstore: evidence for %s: %w", in[i].ProposalID, err)
		}
		for rows.Next() {
			var e Evidence
			if err := rows.Scan(&e.CaseID, &e.Role); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("proposalstore: evidence for %s: %w", in[i].ProposalID, err)
			}
			in[i].Evidence = append(in[i].Evidence, e)
		}
		cerr := rows.Err()
		_ = rows.Close()
		if cerr != nil {
			return nil, fmt.Errorf("proposalstore: evidence for %s: %w", in[i].ProposalID, cerr)
		}
	}
	return in, nil
}

// VerdictFor is the P12 GATE ORACLE's read: the authoritative verdict for a (tenant, config hash,
// source revision), or ok=false when none has been recorded.
//
// 🔴 It is keyed by the CHANGE, not by the proposal id, and scoped to the tenant. Deliverer.Prepare
// calls this rather than trusting the verdict attached to the proposal it was handed — the whole point
// of the enforcement funnel is that the gate is re-read from the authority at delivery time, so a
// proposal object carrying a stale or forged verdict cannot open a pull request.
//
// Ordering: the newest recorded verdict wins. A change can be re-verified (CI re-runs, a flake is
// re-measured), and the last measurement is the current answer.
func (p *PGStore) VerdictFor(parent context.Context, tenantID, configHash, sourceRevision string) (verification.Verdict, bool, error) {
	if tenantID == "" {
		return verification.Verdict{}, false, ErrUnscoped
	}
	ctx, cancel := p.ctx(parent)
	defer cancel()

	row := p.db.QueryRowContext(ctx,
		`SELECT `+proposalColumns+`,
		        v.metric, v.delta, v.ci_low, v.ci_high, v.significant, v.held_out,
		        v.cost_delta, v.latency_delta, v.regression_pass, v.cases_fixed_json,
		        v.cases_broken_json, v.gate_result, v.cases_fixed_count, v.cases_broken_count
		   FROM proposal p
		   JOIN verdict v ON v.proposal_id = p.proposal_id
		  WHERE p.tenant_id = $1 AND p.candidate_config_hash = $2 AND p.source_revision = $3
		  ORDER BY p.created_at DESC, p.proposal_id ASC
		  LIMIT 1`, tenantID, configHash, sourceRevision)

	// An INNER join here, unlike ForWorkflow's LEFT join, and the difference is the contract: this
	// answers "has this change been measured", so a proposal with no verdict row must be ok=false rather
	// than a zero Verdict. A zero GateResult is not `pass`, so the fail-closed direction holds either
	// way — but "not measured" and "measured and rejected" are different sentences downstream.
	sc, err := scanScored(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return verification.Verdict{}, false, nil
	case err != nil:
		return verification.Verdict{}, false, fmt.Errorf("proposalstore: verdict for %s@%s: %w",
			configHash, sourceRevision, err)
	}
	if sc.Verdict == nil {
		return verification.Verdict{}, false, nil
	}
	return *sc.Verdict, true, nil
}

// PendingVerified returns a tenant's proposals whose recorded verdict PASSED — the P12 delivery
// candidates.
//
// It reads `gate_result = 'pass'` from the verdict table rather than `status = 'verified'` on the
// proposal. The two are written in the same transaction and cannot disagree today, but only one of them
// is the measurement: status is a projection of the verdict, and a projection is the thing that goes
// stale. Delivery opens a pull request into a customer's repository, so it reads the authority.
func (p *PGStore) PendingVerified(parent context.Context, tenantID string) ([]Scored, error) {
	if tenantID == "" {
		return nil, ErrUnscoped
	}
	ctx, cancel := p.ctx(parent)
	defer cancel()

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+proposalColumns+`,
		        v.metric, v.delta, v.ci_low, v.ci_high, v.significant, v.held_out,
		        v.cost_delta, v.latency_delta, v.regression_pass, v.cases_fixed_json,
		        v.cases_broken_json, v.gate_result, v.cases_fixed_count, v.cases_broken_count
		   FROM proposal p
		   JOIN verdict v ON v.proposal_id = p.proposal_id
		  WHERE p.tenant_id = $1 AND v.gate_result = 'pass'
		  ORDER BY p.created_at DESC, p.proposal_id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("proposalstore: pending verified for %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Scored
	for rows.Next() {
		sc, err := scanScored(rows)
		if err != nil {
			return nil, fmt.Errorf("proposalstore: pending verified for %s: %w", tenantID, err)
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proposalstore: pending verified for %s: %w", tenantID, err)
	}
	return out, nil
}

// PutVerdict records a verdict CI reported.
//
// 🔴 The tenant is CHECKED against the proposal row, not trusted from the request. The ingest is
// authenticated, but the proposal id inside the body is attacker-chosen: without this, an authenticated
// tenant could report a passing verdict on ANOTHER tenant's proposal and promote an unmeasured change
// into their recommendations. The UPDATE ... WHERE tenant_id makes that a zero-row no-op rather than a
// check somebody has to remember.
func (p *PGStore) PutVerdict(parent context.Context, tenantID string, v verification.Verdict) error {
	if tenantID == "" {
		return ErrUnscoped
	}
	fixed, err := json.Marshal(v.CasesFixed)
	if err != nil {
		return fmt.Errorf("proposalstore: encode cases_fixed for %s: %w", v.ProposalID, err)
	}
	if len(v.CasesFixed) == 0 {
		fixed = []byte("[]")
	}
	broken, err := json.Marshal(v.CasesBroken)
	if err != nil {
		return fmt.Errorf("proposalstore: encode cases_broken for %s: %w", v.ProposalID, err)
	}
	if len(v.CasesBroken) == 0 {
		broken = []byte("[]")
	}

	ctx, cancel := p.ctx(parent)
	defer cancel()

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("proposalstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Ownership first, in the same transaction as the write.
	var owns int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM proposal WHERE proposal_id = $1 AND tenant_id = $2`,
		v.ProposalID, tenantID).Scan(&owns); err != nil {
		return fmt.Errorf("proposalstore: verify ownership of %s: %w", v.ProposalID, err)
	}
	if owns == 0 {
		// Deliberately the same error as a genuinely missing proposal. Distinguishing them would tell an
		// authenticated caller that a proposal id EXISTS under some other tenant, which is the only bit
		// of information this check is protecting.
		return fmt.Errorf("%w: %s", ErrNotFound, v.ProposalID)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO verdict
		   (proposal_id, metric, delta, ci_low, ci_high, significant, held_out,
		    cost_delta, latency_delta, regression_pass, cases_fixed_json, cases_broken_json, gate_result,
		    cases_fixed_count, cases_broken_count)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 ON CONFLICT (proposal_id) DO UPDATE
		   SET metric = EXCLUDED.metric, delta = EXCLUDED.delta,
		       ci_low = EXCLUDED.ci_low, ci_high = EXCLUDED.ci_high,
		       significant = EXCLUDED.significant, held_out = EXCLUDED.held_out,
		       cost_delta = EXCLUDED.cost_delta, latency_delta = EXCLUDED.latency_delta,
		       regression_pass = EXCLUDED.regression_pass,
		       cases_fixed_json = EXCLUDED.cases_fixed_json,
		       cases_broken_json = EXCLUDED.cases_broken_json,
		       gate_result = EXCLUDED.gate_result,
		       cases_fixed_count = EXCLUDED.cases_fixed_count,
		       cases_broken_count = EXCLUDED.cases_broken_count`,
		v.ProposalID, v.Metric, v.Delta.Mean, v.Delta.Low, v.Delta.High, v.Significant, v.HeldOut,
		v.CostDelta, v.LatencyDelta, v.RegressionPass, fixed, broken, string(v.GateResult),
		v.CasesFixedCount, v.CasesBrokenCount); err != nil {
		return fmt.Errorf("proposalstore: put verdict %s: %w", v.ProposalID, err)
	}

	// The proposal's status follows its verdict, in the same transaction. A verdict recorded beside a
	// proposal still reading `candidate` would leave the surface classifying it by a stale field.
	status := StatusGateFailed
	if v.GateResult == verification.GatePass {
		status = StatusVerified
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE proposal SET status = $2 WHERE proposal_id = $1 AND tenant_id = $3`,
		v.ProposalID, status, tenantID); err != nil {
		return fmt.Errorf("proposalstore: status for %s: %w", v.ProposalID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("proposalstore: commit verdict %s: %w", v.ProposalID, err)
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanScored(sc scanner) (Scored, error) {
	var s Scored
	var diffHash, groundHash, specHash sql.NullString
	// Every verdict column is nullable in the result because of the LEFT JOIN.
	var metric, gateResult sql.NullString
	var delta, ciLow, ciHigh, costDelta, latDelta sql.NullFloat64
	var significant, heldOut, regressionPass sql.NullBool
	var fixed, broken []byte
	var fixedCount, brokenCount sql.NullInt64

	if err := sc.Scan(&s.ProposalID, &s.TenantID, &s.WorkflowID, &s.DiagnosisID, &s.Operator,
		&s.BaseVariantID, &s.CandidateConfigHash, &s.SourceRevision, &diffHash, &groundHash,
		&s.BuildStatus, &s.Status, &s.CreatedAt,
		&s.NodeID, &s.Pattern, &s.Rationale, &specHash, &s.RefusalReason, &s.RefusalDimension,
		&metric, &delta, &ciLow, &ciHigh, &significant, &heldOut,
		&costDelta, &latDelta, &regressionPass, &fixed, &broken, &gateResult,
		&fixedCount, &brokenCount); err != nil {
		return Scored{}, err
	}
	s.SourceDiffBlobHash, s.GroundingBlobHash = diffHash.String, groundHash.String
	s.SpecBlobHash = specHash.String

	// No verdict row: leave Verdict nil. NOT a zero Verdict — a zero GateResult would read as a
	// specific failure, and "not yet measured" is a different state from "measured and rejected".
	if !gateResult.Valid {
		return s, nil
	}
	v := verification.Verdict{
		ProposalID: s.ProposalID, Metric: metric.String,
		Significant: significant.Bool, HeldOut: heldOut.Bool,
		CostDelta: costDelta.Float64, LatencyDelta: latDelta.Float64,
		RegressionPass: regressionPass.Bool,
		GateResult:     verification.GateResult(gateResult.String),
	}
	v.Delta.Mean, v.Delta.Low, v.Delta.High = delta.Float64, ciLow.Float64, ciHigh.Float64
	// The counts come from their OWN columns, never from len() of the arrays below. A verdict reported
	// by a customer's CI has a count and no ids, and deriving it would report that it fixed nothing.
	v.CasesFixedCount, v.CasesBrokenCount = int(fixedCount.Int64), int(brokenCount.Int64)
	if len(fixed) > 0 {
		if err := json.Unmarshal(fixed, &v.CasesFixed); err != nil {
			return Scored{}, fmt.Errorf("decode cases_fixed for %s: %w", s.ProposalID, err)
		}
	}
	if len(broken) > 0 {
		if err := json.Unmarshal(broken, &v.CasesBroken); err != nil {
			return Scored{}, fmt.Errorf("decode cases_broken for %s: %w", s.ProposalID, err)
		}
	}
	s.Verdict = &v
	return s, nil
}

// nullStr sends NULL for an empty string. Load-bearing: 0012 constrains the blob-hash columns to
// `NULL OR ~ '^[0-9a-f]{64}$'`, so "" is REFUSED — which is the constraint catching an inline payload
// where a content hash belongs.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
