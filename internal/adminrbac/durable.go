package adminrbac

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// durable.go gives the append-only role-grant log an optional durable backing.
//
// It is the same write-through port `adminidentity/durable.go` documents, for the same reason and in
// the same order: persist first, and a failed write aborts the mutation with memory untouched. What is
// specific to this store is what a lost log MEANS. `GrantStore` is folded — `Live()` replays every row
// to compute the roles an admin holds now — so losing it does not merely forget who is a Superadmin, it
// makes every operator hold NO role. On a deny-by-default gate that is a console where a signed-in
// operator is refused every action, which reads as a broken product rather than as lost state.
//
// The log is append-only in the schema (a revoke is a new row, never an edit), so the durable side needs
// exactly one write: append. There is deliberately no update and no delete here, because there is none
// in `admin_role_grant` either.

// GrantWriter persists appended role-grant rows.
type GrantWriter interface {
	// AppendGrant writes one row of the append-only log.
	AppendGrant(g RoleGrant) error
}

// SetWriter attaches a durable backing to the log.
func (s *GrantStore) SetWriter(w GrantWriter) error {
	if w == nil {
		return errors.New("adminrbac: SetWriter(nil) — leave the writer unset for an in-memory grant log rather than clearing one")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer != nil {
		return errors.New("adminrbac: this grant log already has a durable backing — a second one would leave the rows written to the first invisible")
	}
	s.writer = w
	return nil
}

// Durable reports whether the log survives a restart.
func (s *GrantStore) Durable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writer != nil
}

// LoadGrants replays durably-held rows WITHOUT persisting them again.
//
// It also restores the sequence counter past the highest id it sees. That is not bookkeeping: grant ids
// are `grant-NNNN` from an in-process counter, and a store that restarted at zero would mint ids that
// collide with rows already in the table — so the first grant after a restart would either fail on the
// primary key or, on a store that tolerated it, make `revokes` point at two different rows.
func LoadGrants(s *GrantStore, rows []RoleGrant) error {
	if s == nil {
		return errors.New("adminrbac: LoadGrants needs a store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range rows {
		if strings.TrimSpace(g.GrantID) == "" || strings.TrimSpace(g.AdminID) == "" {
			return fmt.Errorf("adminrbac: durable grant row %q is missing a grant_id or an admin_id", g.GrantID)
		}
		if !g.Role.Valid() {
			return fmt.Errorf("%w: durable grant row %s carries %q", ErrUnknownRole, g.GrantID, g.Role)
		}
		if g.Action != GrantActionGrant && g.Action != GrantActionRevoke {
			return fmt.Errorf("adminrbac: durable grant row %s has unknown action %q", g.GrantID, g.Action)
		}
		s.rows = append(s.rows, g)
		if n, ok := grantSeq(g.GrantID); ok && n > s.seq {
			s.seq = n
		}
	}
	return nil
}

// grantSeq reads the counter back out of a `grant-NNNN` id. An id in any other shape returns false
// rather than zero: a row whose id this store did not mint must not silently lower the counter.
func grantSeq(grantID string) (int, bool) {
	rest, ok := strings.CutPrefix(grantID, "grant-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
