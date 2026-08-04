package adminstore

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
)

// killswitch.go is the durable kill-switch state store.
//
// # Why the in-memory one could not ship
//
// `kill_switch_state` was created by migration 0014 and no Go code ever read or wrote it;
// `MemKillSwitchStore` says in its own comment that "the durable shape is the `kill_switch_state`
// table". So the operator console's fleet brake was a map. Two things follow, and the second is worse
// than the first:
//
//  1. An operator arms the switch to halt a runaway fleet, the pod restarts for any reason, and the
//     fleet resumes. Nothing is logged, because from the new process's point of view nothing was ever
//     armed.
//  2. The console shows DISARMED after that restart — so the surface actively asserts the brake is off
//     when the operator's last action was to pull it.
//
// # Every error means INDETERMINATE, never "not armed"
//
// That is the store interface's own rule, and it is the whole mechanism: `HaltsMerge` treats an error
// as "halt", so a read failure fails CLOSED. This implementation therefore never converts a query
// error into a zero state — a missing ROW is un-armed (a known answer), a failed READ is an error.

// KillSwitch is the Postgres-backed kill-switch state store.
type KillSwitch struct{ db *sql.DB }

// NewKillSwitch wraps a live platform pool.
func NewKillSwitch(db *sql.DB) (*KillSwitch, error) {
	if db == nil {
		return nil, errors.New("adminstore: the kill switch needs the platform database — a fleet brake held in memory is disarmed by a restart")
	}
	return &KillSwitch{db: db}, nil
}

// Get returns one scope's state.
//
// A scope with NO ROW is returned un-armed and with no error: "never armed" is a known answer, and it
// is the correct one for a fleet that has never been halted. Only a failed read is an error.
func (k *KillSwitch) Get(scope string) (adminops.KillSwitchState, error) {
	ctx, cancel := k.ctx()
	defer cancel()
	var (
		st            adminops.KillSwitchState
		setBy, reason sql.NullString
		setAt         sql.NullTime
	)
	err := k.db.QueryRowContext(ctx,
		`SELECT scope, armed, set_by, reason, set_at FROM kill_switch_state WHERE scope = $1`, scope).
		Scan(&st.Scope, &st.Armed, &setBy, &reason, &setAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return adminops.KillSwitchState{Scope: scope}, nil
	case err != nil:
		return adminops.KillSwitchState{}, fmt.Errorf("%w: reading scope %q: %v", adminops.ErrKillStateIndeterminate, scope, err)
	}
	st.SetBy, st.Reason = setBy.String, reason.String
	if setAt.Valid {
		st.SetAt = setAt.Time.UTC().Format(time.RFC3339)
	}
	return st, nil
}

// Put writes one scope's state.
func (k *KillSwitch) Put(state adminops.KillSwitchState) error {
	ctx, cancel := k.ctx()
	defer cancel()
	// The timestamp is the state's own when it carries one, so the row records WHEN THE OPERATOR ACTED
	// rather than when the write landed. They differ under retry, and the audit chain quotes the former.
	at := time.Now().UTC()
	if state.SetAt != "" {
		if parsed, perr := time.Parse(time.RFC3339, state.SetAt); perr == nil {
			at = parsed
		}
	}
	_, err := k.db.ExecContext(ctx, `
		INSERT INTO kill_switch_state (scope, armed, set_by, reason, set_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (scope) DO UPDATE SET
			armed = EXCLUDED.armed, set_by = EXCLUDED.set_by,
			reason = EXCLUDED.reason, set_at = EXCLUDED.set_at`,
		state.Scope, state.Armed, state.SetBy, state.Reason, at)
	if err != nil {
		return fmt.Errorf("%w: writing scope %q: %v", adminops.ErrKillStateIndeterminate, state.Scope, err)
	}
	return nil
}

// All returns every scope with a state, for the console's fleet view.
func (k *KillSwitch) All() ([]adminops.KillSwitchState, error) {
	ctx, cancel := k.ctx()
	defer cancel()
	rows, err := k.db.QueryContext(ctx,
		`SELECT scope, armed, set_by, reason, set_at FROM kill_switch_state ORDER BY scope`)
	if err != nil {
		return nil, fmt.Errorf("%w: listing scopes: %v", adminops.ErrKillStateIndeterminate, err)
	}
	defer func() { _ = rows.Close() }()
	out := []adminops.KillSwitchState{}
	for rows.Next() {
		var (
			st            adminops.KillSwitchState
			setBy, reason sql.NullString
			setAt         sql.NullTime
		)
		if err := rows.Scan(&st.Scope, &st.Armed, &setBy, &reason, &setAt); err != nil {
			return nil, fmt.Errorf("%w: scanning a scope: %v", adminops.ErrKillStateIndeterminate, err)
		}
		st.SetBy, st.Reason = setBy.String, reason.String
		if setAt.Valid {
			st.SetAt = setAt.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: listing scopes: %v", adminops.ErrKillStateIndeterminate, err)
	}
	return out, nil
}

// Describe names the store on the readiness surface, so an operator can confirm the brake they are
// looking at is the durable one rather than a fixture.
func (k *KillSwitch) Describe() string {
	return "postgres kill_switch_state (survives a restart)"
}
