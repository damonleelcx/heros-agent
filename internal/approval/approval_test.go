package approval

import (
	"database/sql"
	"os"
	"testing"

	"github.com/heros-foreal/agentd/internal/db"
)

func TestSubmitGet_nullRollbackRef(t *testing.T) {
	f, err := os.CreateTemp("", "heros-approval-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(path) }()

	sq, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sq.Close() }()

	p, err := Submit(sq, "", LayerPrompt, "title", "why", "diff body")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if p.RollbackRef != "" {
		t.Fatalf("expected empty rollback_ref, got %q", p.RollbackRef)
	}

	p2, err := Get(sq, p.ID)
	if err != nil {
		t.Fatalf("get after submit: %v", err)
	}
	if p2.RollbackRef != "" {
		t.Fatalf("get rollback: want empty, got %q", p2.RollbackRef)
	}
}

// ── P31 · approval attribution ───────────────────────────────────────────────────────────────────

// testDB opens a throwaway ledger with the real migrations applied. 🔴 The REAL ones: a test that
// created its own simplified `proposals` table would be testing a schema no deployment has, which is
// how a missing column ships green.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "heros-approval-p31-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(path) })
	sq, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sq.Close() })
	return sq
}

func TestApproveRefusesWithoutAPerson(t *testing.T) {
	db := testDB(t)
	p, err := Submit(db, "tenant-a", LayerPrompt, "t", "r", "diff")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := Approve(db, p.ID, ""); err == nil {
		t.Fatal("an approval with no person was accepted; an audit row that names nobody is believed " +
			"and cannot be questioned")
	}
	got, err := Get(db, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("status = %q after a refused approval; want it untouched", got.Status)
	}
}

func TestApproveRecordsWhoAndIsNotReplayable(t *testing.T) {
	db := testDB(t)
	p, err := Submit(db, "tenant-a", LayerPrompt, "t", "r", "diff")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := Approve(db, p.ID, "person-1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	who, ok, err := ApprovedBy(db, p.ID)
	if err != nil || !ok || who != "person-1" {
		t.Fatalf("ApprovedBy = %q, %v, %v; want person-1", who, ok, err)
	}
	// 🔴 A replay must not overwrite the first approver. A double-clicked button and a retried request
	// are the same event, and the second must not rewrite who authorized what.
	if err := Approve(db, p.ID, "person-2"); err == nil {
		t.Fatal("a second approval succeeded; the first approver's name would have been overwritten")
	}
	who, _, _ = ApprovedBy(db, p.ID)
	if who != "person-1" {
		t.Errorf("ApprovedBy = %q after a replayed approval; want the original approver", who)
	}
}
