package proposalstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// pass.go records what the last proposal-generation pass FOUND, per tenant per workflow.
//
// # The fact that was being thrown away
//
// `proposalgen` returns a closed State and a sentence for every pass, including — especially — the ones
// that produce no proposals at all. Every one of those was discarded when the HTTP response was written.
// The recommendation surface is read from a different process, minutes or days later, and had exactly
// one input: how many proposal rows exist. So a workflow nobody has ever analysed and a workflow that
// was analysed and is genuinely healthy both rendered as `empty`, under the sentence
// "Nothing is pending."
//
// 🔴 Those are opposites. One means press the button; the other means you are done. This table is what
// lets the surface say which.

// Pass is one generation pass as the platform remembers it.
type Pass struct {
	TenantID   string
	WorkflowID string
	// State is proposalgen.State, verbatim. Held as a string here rather than importing proposalgen: a
	// store that imported the generator would make the generator's package graph depend on persistence,
	// and the value is closed at the WRITER, which is where the closure means something.
	State string
	// Detail is the sentence the generator wrote. Stored rather than re-derived — it names what the pass
	// actually saw, and a console re-deriving it would have to re-run the pass to know.
	Detail string
	// Proposals is how many rows THIS pass recorded, which is not how many rows exist.
	Proposals int
	// RanAtMS is epoch milliseconds. int64 ms, both dialects, per the standing rule.
	RanAtMS int64
}

// ErrPassUnrecorded is returned by a writer handed a pass that does not say what it found. A row whose
// state is empty is the row this table exists to prevent, so it is refused here as well as by the CHECK.
var ErrPassUnrecorded = errors.New("proposalstore: a generation pass must record its state")

// PassStore records and reads generation passes.
//
// LastPass returns ok=false for NEVER RUN, and that is the whole point of the type: it is a different
// answer from "ran and found nothing", and every method returns an error so a read failure is a third
// answer again rather than being flattened into the first.
type PassStore interface {
	PutPass(ctx context.Context, p Pass) error
	LastPass(ctx context.Context, tenantID, workflowID string) (Pass, bool, error)
}

// PutPass records a pass, REPLACING any earlier one for the same workflow.
//
// Replacement rather than append: the surface asks "what does the platform currently believe about this
// workflow", and a log would answer a different question while making the current answer an ORDER BY
// somebody eventually gets wrong.
func (p *PGStore) PutPass(parent context.Context, pass Pass) error {
	switch {
	case pass.TenantID == "" || pass.WorkflowID == "":
		return fmt.Errorf("%w: a pass must name its tenant and workflow", ErrUnscoped)
	case pass.State == "":
		return fmt.Errorf("%w: tenant %s workflow %s", ErrPassUnrecorded, pass.TenantID, pass.WorkflowID)
	}
	ctx, cancel := p.ctx(parent)
	defer cancel()

	if _, err := p.db.ExecContext(ctx,
		`INSERT INTO proposal_generation_pass (tenant_id, workflow_id, state, detail, proposals, ran_at_ms)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (tenant_id, workflow_id) DO UPDATE
		   SET state     = EXCLUDED.state,
		       detail    = EXCLUDED.detail,
		       proposals = EXCLUDED.proposals,
		       ran_at_ms = EXCLUDED.ran_at_ms`,
		pass.TenantID, pass.WorkflowID, pass.State, pass.Detail, pass.Proposals, pass.RanAtMS); err != nil {
		return fmt.Errorf("proposalstore: record pass for %s/%s: %w", pass.TenantID, pass.WorkflowID, err)
	}
	return nil
}

// LastPass reads the newest pass. ok=false means NO PASS HAS EVER RUN — never a read failure, which is
// the error.
func (p *PGStore) LastPass(parent context.Context, tenantID, workflowID string) (Pass, bool, error) {
	ctx, cancel := p.ctx(parent)
	defer cancel()

	out := Pass{TenantID: tenantID, WorkflowID: workflowID}
	err := p.db.QueryRowContext(ctx,
		`SELECT state, detail, proposals, ran_at_ms
		   FROM proposal_generation_pass WHERE tenant_id = $1 AND workflow_id = $2`,
		tenantID, workflowID).Scan(&out.State, &out.Detail, &out.Proposals, &out.RanAtMS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Pass{}, false, nil
	case err != nil:
		return Pass{}, false, fmt.Errorf("proposalstore: read pass for %s/%s: %w", tenantID, workflowID, err)
	}
	return out, true, nil
}
