package deliveryrecord_test

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

func entry(id string, state fd.State, at time.Time) fd.Entry {
	return fd.Entry{
		DeliveryID: id, TenantID: "t1", ConfigHash: "ch1", SourceRevision: "rev1",
		Target: "o/r", ForgeRef: "o/r#1", Mode: fd.ModeCI, State: state, Actor: "ci:p1", At: at,
	}
}

// 4.1 / 4.6: state changes append; the previous entry remains; history reconstructs in order.
func TestRecord_AppendOnly_HistoryReconstructs(t *testing.T) {
	s := deliveryrecord.NewMemStore()
	ctx := context.Background()
	base := time.Unix(0, 0).UTC()
	states := []fd.State{fd.StateOpened, fd.StateUpdated}
	for i, st := range states {
		if err := s.Append(ctx, entry("d1", st, base.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("append %s: %v", st, err)
		}
	}
	// merged needs a commit
	me := entry("d1", fd.StateMerged, base.Add(2*time.Second))
	me.MergeCommit = "abc123"
	if err := s.Append(ctx, me); err != nil {
		t.Fatalf("append merged: %v", err)
	}
	hist, err := s.History(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	want := []fd.State{fd.StateOpened, fd.StateUpdated, fd.StateMerged}
	if len(hist) != len(want) {
		t.Fatalf("history len = %d, want %d", len(hist), len(want))
	}
	for i := range want {
		if hist[i].State != want[i] {
			t.Errorf("history[%d] = %s, want %s", i, hist[i].State, want[i])
		}
		if i > 0 && hist[i].Seq <= hist[i-1].Seq {
			t.Errorf("history not in append order at %d", i)
		}
	}
}

// 4.1: the interface itself expresses no mutate or delete — this is a compile-time property, asserted
// by the Recorder interface having only Append + reads. This test documents it at runtime: appending
// the same delivery many times only grows history; nothing is overwritten.
func TestRecord_NothingOverwritten(t *testing.T) {
	s := deliveryrecord.NewMemStore()
	ctx := context.Background()
	_ = s.Append(ctx, entry("d1", fd.StateOpened, time.Unix(1, 0)))
	_ = s.Append(ctx, entry("d1", fd.StateUpdated, time.Unix(2, 0)))
	hist, _ := s.History(ctx, "d1")
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2 (both entries retained)", len(hist))
	}
	if hist[0].State != fd.StateOpened {
		t.Errorf("the original 'opened' entry was not retained")
	}
}

// 2.3 store-level: a second 'opened' for one delivery is rejected with ErrOpenConflict.
func TestRecord_OneOpenPerDelivery(t *testing.T) {
	s := deliveryrecord.NewMemStore()
	ctx := context.Background()
	if err := s.Append(ctx, entry("d1", fd.StateOpened, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	err := s.Append(ctx, entry("d1", fd.StateOpened, time.Unix(2, 0)))
	if err != fd.ErrOpenConflict {
		t.Fatalf("second 'opened' should be ErrOpenConflict, got %v", err)
	}
}

// 4.4 / 4.6: a close-without-merge records 'closed', never 'merged'.
func TestObserve_CloseIsNotMerge(t *testing.T) {
	s := deliveryrecord.NewMemStore()
	ctx := context.Background()
	_ = s.Append(ctx, entry("d1", fd.StateOpened, time.Unix(1, 0)))
	obs := fd.NewMergeObserver(s)
	obs.SetClock(func() time.Time { return time.Unix(2, 0).UTC() })
	if err := obs.ObserveClose(ctx, "d1", "app-webhook", "author closed it"); err != nil {
		t.Fatal(err)
	}
	hist, _ := s.History(ctx, "d1")
	for _, e := range hist {
		if e.State == fd.StateMerged {
			t.Fatalf("a close-without-merge was recorded as merged")
		}
	}
	if hist[len(hist)-1].State != fd.StateClosed {
		t.Errorf("last state = %s, want closed", hist[len(hist)-1].State)
	}
}

// 4.4: a merge is recorded from an observation carrying the commit.
func TestObserve_MergeFromObservation(t *testing.T) {
	s := deliveryrecord.NewMemStore()
	ctx := context.Background()
	_ = s.Append(ctx, entry("d1", fd.StateOpened, time.Unix(1, 0)))
	obs := fd.NewMergeObserver(s)
	obs.SetClock(func() time.Time { return time.Unix(2, 0).UTC() })

	if err := obs.ObserveMerge(ctx, "d1", "", "ci"); err == nil {
		t.Errorf("a merge observation with no commit should be refused (it would be an inference)")
	}
	if err := obs.ObserveMerge(ctx, "d1", "deadbeef", "ci"); err != nil {
		t.Fatal(err)
	}
	head, _, _ := s.Head(ctx, "d1")
	if head.State != fd.StateMerged || head.MergeCommit != "deadbeef" {
		t.Errorf("head = %+v, want merged with commit deadbeef", head)
	}
}

// 4.5: a revert after a merge is a FURTHER state; the merged state stays; both recoverable in order.
func TestObserve_RevertKeepsMerged(t *testing.T) {
	s := deliveryrecord.NewMemStore()
	ctx := context.Background()
	_ = s.Append(ctx, entry("d1", fd.StateOpened, time.Unix(1, 0)))
	obs := fd.NewMergeObserver(s)
	obs.SetClock(func() time.Time { return time.Unix(2, 0).UTC() })
	_ = obs.ObserveMerge(ctx, "d1", "abc123", "ci")
	obs.SetClock(func() time.Time { return time.Unix(3, 0).UTC() })
	if err := obs.ObserveRevert(ctx, "d1", "rev999", "ci"); err != nil {
		t.Fatal(err)
	}
	hist, _ := s.History(ctx, "d1")
	var sawMerged, sawReverted bool
	var mergedIdx, revertIdx int
	for i, e := range hist {
		if e.State == fd.StateMerged {
			sawMerged, mergedIdx = true, i
		}
		if e.State == fd.StateReverted {
			sawReverted, revertIdx = true, i
		}
	}
	if !sawMerged || !sawReverted {
		t.Fatalf("both merged and reverted must be in the record; got %+v", hist)
	}
	if mergedIdx >= revertIdx {
		t.Errorf("merged must precede reverted in the sequence")
	}
}

// 4.3: the mode is recoverable from the record for an audit.
func TestRecord_ModeRecorded(t *testing.T) {
	s := deliveryrecord.NewMemStore()
	ctx := context.Background()
	e := entry("d1", fd.StateOpened, time.Unix(1, 0))
	e.Mode = fd.ModeApp
	_ = s.Append(ctx, e)
	head, _, _ := s.Head(ctx, "d1")
	if head.Mode != fd.ModeApp {
		t.Errorf("mode = %q, want app", head.Mode)
	}
}
