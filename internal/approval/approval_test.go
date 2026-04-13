package approval

import (
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
