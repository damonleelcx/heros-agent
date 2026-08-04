package adminrbac_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminrbac"
)

type grantWriter struct {
	rows []adminrbac.RoleGrant
	fail bool
}

func (w *grantWriter) AppendGrant(g adminrbac.RoleGrant) error {
	if w.fail {
		return errors.New("the database said no")
	}
	w.rows = append(w.rows, g)
	return nil
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// TestLoadGrantsRestoresTheSequence is the one that prevents a corrupted audit trail after a restart.
//
// Grant ids are `grant-NNNN` from an in-process counter. A store that restarted at zero would mint ids
// that already exist in `admin_role_grant` — so the first grant after any restart either fails on the
// primary key or, on a store that tolerated duplicates, makes `revokes` point at two different rows and
// the append-only log stops being answerable.
func TestLoadGrantsRestoresTheSequence(t *testing.T) {
	now := fixedClock()
	store := adminrbac.NewGrantStore(now)
	w := &grantWriter{}
	if err := store.SetWriter(w); err != nil {
		t.Fatalf("SetWriter: %v", err)
	}
	// Three rows already in the durable log, as if written by a previous process.
	if err := adminrbac.LoadGrants(store, []adminrbac.RoleGrant{
		{GrantID: "grant-0001", AdminID: "adm-a", Role: adminrbac.RoleSupport, Action: adminrbac.GrantActionGrant, GrantedAt: now()},
		{GrantID: "grant-0002", AdminID: "adm-b", Role: adminrbac.RoleSupport, Action: adminrbac.GrantActionGrant, GrantedAt: now()},
		{GrantID: "grant-0003", AdminID: "adm-b", Role: adminrbac.RoleSupport, Action: adminrbac.GrantActionRevoke, RevokedAt: now(), Revokes: "grant-0002"},
	}); err != nil {
		t.Fatalf("LoadGrants: %v", err)
	}
	got, err := store.Seed("adm-c", adminrbac.RoleSuperadmin, "after a restart")
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got.GrantID != "grant-0004" {
		t.Errorf("the first grant after a restart got id %q, want grant-0004 — the counter did not resume "+
			"past the rows already in the durable log", got.GrantID)
	}
	// And the fold is correct across the replay: adm-b's role was revoked, adm-a's was not.
	if roles := store.Live("adm-a"); len(roles) != 1 || roles[0] != adminrbac.RoleSupport {
		t.Errorf("adm-a holds %v, want [support]", roles)
	}
	if roles := store.Live("adm-b"); len(roles) != 0 {
		t.Errorf("adm-b holds %v after a revoke replayed, want none", roles)
	}
}

// TestFailedGrantPersistLeavesMemoryUntouched pins the write-through order on the log too. The
// direction that matters is a REVOKE: one that persisted nowhere is an operator whose authority returns
// at the next restart, and nobody re-performs a revoke they have already done.
func TestFailedGrantPersistLeavesMemoryUntouched(t *testing.T) {
	store := adminrbac.NewGrantStore(fixedClock())
	if err := store.SetWriter(&grantWriter{fail: true}); err != nil {
		t.Fatalf("SetWriter: %v", err)
	}
	if _, err := store.Seed("adm-a", adminrbac.RoleSuperadmin, "should not apply"); err == nil {
		t.Fatal("Seed succeeded against a writer that refused it")
	}
	if roles := store.Live("adm-a"); len(roles) != 0 {
		t.Errorf("adm-a holds %v after the durable write failed — a restart would withdraw a role the "+
			"process believes was granted", roles)
	}
	if rows := store.Rows(); len(rows) != 0 {
		t.Errorf("the in-memory log holds %d row(s) after the durable write failed", len(rows))
	}
}

// TestBurntSequenceIsNotReused pins that a failed append does not hand its id to the next row.
//
// Reusing it would let a later row take an id an earlier attempt may already have written. A gap in the
// sequence costs nothing; an id that means two things costs the audit trail.
func TestBurntSequenceIsNotReused(t *testing.T) {
	store := adminrbac.NewGrantStore(fixedClock())
	w := &grantWriter{fail: true}
	if err := store.SetWriter(w); err != nil {
		t.Fatalf("SetWriter: %v", err)
	}
	if _, err := store.Seed("adm-a", adminrbac.RoleSupport, "fails"); err == nil {
		t.Fatal("Seed unexpectedly succeeded")
	}
	w.fail = false
	got, err := store.Seed("adm-a", adminrbac.RoleSupport, "succeeds")
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got.GrantID == "grant-0001" {
		t.Error("the id burnt by the failed append was reused")
	}
}

// TestLoadGrantsRefusesAnUnknownRole keeps a corrupt or hand-edited row from silently becoming a role
// nothing grants. Deny-by-default means an unrecognised role would fold to no capability at all, which
// looks like the operator was never granted anything.
func TestLoadGrantsRefusesAnUnknownRole(t *testing.T) {
	store := adminrbac.NewGrantStore(fixedClock())
	err := adminrbac.LoadGrants(store, []adminrbac.RoleGrant{
		{GrantID: "grant-0001", AdminID: "adm-a", Role: adminrbac.Role("wizard"), Action: adminrbac.GrantActionGrant},
	})
	if err == nil {
		t.Fatal("a durable row with an unknown role was accepted")
	}
	if !errors.Is(err, adminrbac.ErrUnknownRole) {
		t.Errorf("error %v does not wrap ErrUnknownRole", err)
	}
	if !strings.Contains(err.Error(), "grant-0001") {
		t.Errorf("error %q does not name the offending row", err)
	}
}
