package deliveryrecord

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// PGStore is the durable delivery record over the Postgres `delivery` table (0015_p12_delivery). It is
// the production backing of forgedelivery.Recorder; MemStore is the dev/demo path. Both enforce the
// same two load-bearing rules — append-only, and one 'opened' per delivery — but PGStore enforces them
// BY CONSTRUCTION (the trigger and the partial unique index), so it cannot drift from the schema the
// pgproof test proves.
type PGStore struct {
	db *sql.DB
}

// NewPGStore builds a store over an open pool. The pool must already have the migration chain applied.
func NewPGStore(db *sql.DB) *PGStore { return &PGStore{db: db} }

// isUniqueViolation reports whether err is a Postgres unique-constraint violation on the one-open-PR
// index. We match the SQLSTATE 23505 (unique_violation) plus the index name, so an unrelated unique
// violation is not misread as the open-race signal.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "delivery_one_open_pr") ||
		(strings.Contains(msg, "23505") && strings.Contains(msg, "delivery"))
}

// Append implements forgedelivery.Recorder. An 'opened' collision returns ErrOpenConflict; the trigger
// makes UPDATE/DELETE impossible, so there is no mutate path to expose here at all.
func (s *PGStore) Append(ctx context.Context, e forgedelivery.Entry) error {
	var reason, mergeCommit any
	if e.Reason != "" {
		reason = e.Reason
	}
	if e.MergeCommit != "" {
		mergeCommit = e.MergeCommit
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO delivery
		   (delivery_id, tenant_id, config_hash, source_revision, target, forge_ref, mode, state, actor, reason, merge_commit)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		e.DeliveryID, e.TenantID, e.ConfigHash, e.SourceRevision, e.Target, e.ForgeRef,
		string(e.Mode), string(e.State), e.Actor, reason, mergeCommit)
	if err != nil {
		if e.State == forgedelivery.StateOpened && isUniqueViolation(err) {
			return forgedelivery.ErrOpenConflict
		}
		return fmt.Errorf("deliveryrecord: append %s for %s: %w", e.State, e.DeliveryID, err)
	}
	return nil
}

const headCols = `delivery_id, tenant_id, config_hash, source_revision, target, forge_ref, mode, state,
	COALESCE(reason,''), COALESCE(merge_commit,'')`

func scanHead(rows *sql.Rows) (forgedelivery.DeliveryHead, error) {
	var h forgedelivery.DeliveryHead
	var mode, state string
	if err := rows.Scan(&h.DeliveryID, &h.TenantID, &h.ConfigHash, &h.SourceRevision, &h.Target,
		&h.ForgeRef, &mode, &state, &h.Reason, &h.MergeCommit); err != nil {
		return h, err
	}
	h.Mode = forgedelivery.Mode(mode)
	h.State = forgedelivery.State(state)
	return h, nil
}

// Head returns the latest entry (highest seq) for a delivery, projected to a head.
func (s *PGStore) Head(ctx context.Context, deliveryID string) (forgedelivery.DeliveryHead, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+headCols+` FROM delivery WHERE delivery_id = $1 ORDER BY seq DESC LIMIT 1`, deliveryID)
	if err != nil {
		return forgedelivery.DeliveryHead{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return forgedelivery.DeliveryHead{}, false, rows.Err()
	}
	h, err := scanHead(rows)
	return h, err == nil, err
}

// OpenForTarget returns the heads of deliveries whose LATEST state is still open, for a (tenant,
// target). The DISTINCT ON reads each delivery's latest row; the outer filter keeps the open ones.
func (s *PGStore) OpenForTarget(ctx context.Context, tenantID, target string) ([]forgedelivery.DeliveryHead, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+headCols+` FROM (
		  SELECT DISTINCT ON (delivery_id) * FROM delivery
		  WHERE tenant_id = $1 AND target = $2
		  ORDER BY delivery_id, seq DESC
		) latest
		WHERE state IN ('opened','updated')
		ORDER BY delivery_id`, tenantID, target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectHeads(rows)
}

// ListForTenant returns the head of every delivery for a tenant, newest first.
func (s *PGStore) ListForTenant(ctx context.Context, tenantID string) ([]forgedelivery.DeliveryHead, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+headCols+` FROM (
		  SELECT DISTINCT ON (delivery_id) * FROM delivery
		  WHERE tenant_id = $1
		  ORDER BY delivery_id, seq DESC
		) latest
		ORDER BY seq DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectHeads(rows)
}

func collectHeads(rows *sql.Rows) ([]forgedelivery.DeliveryHead, error) {
	var out []forgedelivery.DeliveryHead
	for rows.Next() {
		h, err := scanHead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// History returns every entry for a delivery in append order.
func (s *PGStore) History(ctx context.Context, deliveryID string) ([]forgedelivery.Entry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, delivery_id, tenant_id, config_hash, source_revision, target, forge_ref, mode, state,
		       actor, COALESCE(reason,''), COALESCE(merge_commit,''), at
		FROM delivery WHERE delivery_id = $1 ORDER BY seq ASC`, deliveryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []forgedelivery.Entry
	for rows.Next() {
		var e forgedelivery.Entry
		var mode, state string
		if err := rows.Scan(&e.Seq, &e.DeliveryID, &e.TenantID, &e.ConfigHash, &e.SourceRevision,
			&e.Target, &e.ForgeRef, &mode, &state, &e.Actor, &e.Reason, &e.MergeCommit, &e.At); err != nil {
			return nil, err
		}
		e.Mode = forgedelivery.Mode(mode)
		e.State = forgedelivery.State(state)
		out = append(out, e)
	}
	return out, rows.Err()
}

// compile-time assertions that both backings satisfy the one Recorder contract.
var (
	_ forgedelivery.Recorder = (*PGStore)(nil)
	_ forgedelivery.Recorder = (*MemStore)(nil)
)
