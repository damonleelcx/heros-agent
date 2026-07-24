package adminaudit_test

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
)

// audit_test.go covers task 9.3 — the LOAD-BEARING tamper-evidence test (FR15):
//
//	a mutate/delete attempt (including by a Superadmin) is PREVENTED,
//	and Audit.Verify() DETECTS the resulting chain break.
//
// "Prevented" is asserted structurally: the Store interface has no mutation method, so there is no
// in-band path for any role. "Detected" is asserted by tampering out of band — the way somebody with
// direct store access would — and watching Verify report the exact break.

func fixedClock() func() time.Time {
	t := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	return func() time.Time { t = t.Add(time.Second); return t }
}

func seedChain(t *testing.T) *adminaudit.MemoryStore {
	t.Helper()
	s := adminaudit.NewMemoryStore(fixedClock())
	for i, spec := range []struct {
		actor  string
		action adminaudit.Action
		target string
	}{
		{"adm-superadmin", adminaudit.ActionRoleGrant, "admin:adm-support"},
		{"adm-platform_sre", adminaudit.ActionTenantSuspend, "tenant:acme"},
		{"adm-billing_ops", adminaudit.ActionBillingCredit, "tenant:acme"},
		{adminaudit.ActorSystem, adminaudit.ActionAutonomousMerge, "tenant:boreal"},
	} {
		if _, err := s.Append(adminaudit.Entry{
			ActorAdminID: spec.actor, Action: spec.action, Target: spec.target,
			Reason: "seed entry", Result: "applied",
			Evidence: map[string]string{"i": string(rune('a' + i))},
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return s
}

// TestStoreExposesNoMutationPath is the "prevented" half. It reflects over the Store interface, so a
// Delete or Update method added later is a failing test rather than a code review somebody waves
// through.
func TestStoreExposesNoMutationPath(t *testing.T) {
	iface := adminaudit.StoreInterfaceType()
	got := make([]string, 0, iface.NumMethod())
	for i := 0; i < iface.NumMethod(); i++ {
		got = append(got, iface.Method(i).Name)
	}
	want := adminaudit.StoreMethodNames()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the audit Store interface exposes %v, want exactly %v — no role, including Superadmin, "+
			"may have a mutate or delete path", got, want)
	}
	for _, name := range got {
		lower := strings.ToLower(name)
		for _, forbidden := range []string{"delete", "update", "mutate", "remove", "edit", "purge", "truncate"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("the audit store exposes %q, which is a mutation path", name)
			}
		}
	}
}

// TestVerifyDetectsAnAlteredEntry is the "detected" half.
func TestVerifyDetectsAnAlteredEntry(t *testing.T) {
	s := seedChain(t)
	if v := s.Verify(); !v.Intact || v.Checked != 4 {
		t.Fatalf("a freshly built chain does not verify: %+v", v)
	}

	// Somebody with direct store access rewrites the reason on entry 2 — the classic "cover my tracks"
	// edit, made where no application code could make it.
	if err := s.SimulateOutOfBandTamper(2, func(e *adminaudit.Entry) {
		e.Reason = "routine maintenance"
	}); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	v := s.Verify()
	if v.Intact {
		t.Fatal("Verify reported an ALTERED chain as intact")
	}
	if v.BreakAt != 2 {
		t.Errorf("Verify broke at seq %d, want 2", v.BreakAt)
	}
	if v.Detail == "" {
		t.Error("Verify does not say what is wrong")
	}
}

// TestVerifyDetectsADeletedEntry: removing a row is as detectable as editing one.
func TestVerifyDetectsADeletedEntry(t *testing.T) {
	s := seedChain(t)
	if err := s.SimulateOutOfBandDeletion(2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	v := s.Verify()
	if v.Intact {
		t.Fatal("Verify reported a chain with a DELETED entry as intact")
	}
	if v.BreakAt == 0 {
		t.Error("Verify did not name where the chain breaks")
	}
}

// TestVerifyDetectsARehashedEntry: recomputing an altered entry's own hash is not enough, because the
// NEXT entry's prev_hash still points at the original. This is what makes the chain, rather than the
// per-entry hash, the tamper-evidence.
func TestVerifyDetectsARehashedEntry(t *testing.T) {
	s := seedChain(t)
	entries := s.Entries()
	target := entries[1]
	target.Reason = "routine maintenance"
	target.EntryHash = adminaudit.HashEntry(target.PrevHash, target)
	if err := s.SimulateOutOfBandTamper(2, func(e *adminaudit.Entry) { *e = target }); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	v := s.Verify()
	if v.Intact {
		t.Fatal("Verify reported a re-hashed forgery as intact — the chain link is not being checked")
	}
	if v.BreakAt != 3 {
		t.Errorf("Verify broke at seq %d, want 3 (the entry whose prev_hash no longer matches)", v.BreakAt)
	}
}

// TestAppendIsTheOnlyWayIn: the entry a caller supplies cannot dictate its own position or hash.
func TestAppendIsTheOnlyWayIn(t *testing.T) {
	s := adminaudit.NewMemoryStore(fixedClock())
	first, err := s.Append(adminaudit.Entry{
		ActorAdminID: "adm-1", Action: adminaudit.ActionTenantSuspend, Target: "tenant:acme", Result: "applied",
		// A caller trying to place itself at seq 99 with a hash it made up.
		Seq: 99, PrevHash: "not-the-real-prev", EntryHash: "made-up",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if first.Seq != 1 {
		t.Errorf("a caller-supplied seq survived: %d", first.Seq)
	}
	if first.PrevHash != adminaudit.GenesisHash {
		t.Errorf("the first entry's prev_hash = %q, want %q", first.PrevHash, adminaudit.GenesisHash)
	}
	if first.EntryHash == "made-up" {
		t.Error("a caller-supplied entry hash survived")
	}
	if !s.Verify().Intact {
		t.Error("the chain does not verify after a hostile append")
	}
}

// TestEntriesReturnsACopy: a reader cannot mutate the chain through the slice it was handed.
func TestEntriesReturnsACopy(t *testing.T) {
	s := seedChain(t)
	got := s.Entries()
	got[0].Reason = "mutated through the returned slice"
	if !s.Verify().Intact {
		t.Fatal("mutating the returned slice altered the stored chain")
	}
	if s.Entries()[0].Reason == "mutated through the returned slice" {
		t.Fatal("Entries hands out a reference to the stored chain")
	}
}

// TestUnrecordableEntryIsRejected: an entry that cannot answer "who, what, to whom" is not a record.
func TestUnrecordableEntryIsRejected(t *testing.T) {
	s := adminaudit.NewMemoryStore(fixedClock())
	for name, e := range map[string]adminaudit.Entry{
		"no actor":  {Action: adminaudit.ActionTenantSuspend, Target: "tenant:acme"},
		"no action": {ActorAdminID: "adm-1", Target: "tenant:acme"},
		"no target": {ActorAdminID: "adm-1", Action: adminaudit.ActionTenantSuspend},
	} {
		if _, err := s.Append(e); err == nil {
			t.Errorf("%s: an unreconstructable entry was accepted", name)
		}
	}
}

// TestAppendFailsClosedWhenTheStoreIsDown: the condition FR16's "an admin action that cannot be
// audited does not take effect" rests on.
func TestAppendFailsClosedWhenTheStoreIsDown(t *testing.T) {
	s := seedChain(t)
	before := len(s.Entries())
	s.SetUnavailable(true)
	if _, err := s.Append(adminaudit.Entry{
		ActorAdminID: "adm-1", Action: adminaudit.ActionKillSwitchArm, Target: "global", Result: "applied",
	}); !errors.Is(err, adminaudit.ErrStoreUnavailable) {
		t.Fatalf("Append with the store down: err = %v, want ErrStoreUnavailable", err)
	}
	s.SetUnavailable(false)
	if len(s.Entries()) != before {
		t.Error("an entry landed while the store was reporting itself unavailable")
	}
}

// TestCanonicalIsDeterministicAcrossEvidenceOrder: the hash cannot depend on Go's randomized map
// iteration, or the chain would fail verification at random.
func TestCanonicalIsDeterministicAcrossEvidenceOrder(t *testing.T) {
	base := adminaudit.Entry{
		Seq: 7, ActorAdminID: "adm-1", Target: "tenant:acme", Action: adminaudit.ActionTenantSuspend,
		Reason: "r", Result: "applied", CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		Evidence: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"},
	}
	want := string(adminaudit.Canonical(base))
	for i := 0; i < 50; i++ {
		other := base
		other.Evidence = map[string]string{"e": "5", "d": "4", "c": "3", "b": "2", "a": "1"}
		if got := string(adminaudit.Canonical(other)); got != want {
			t.Fatal("Canonical depends on map iteration order — the chain would fail verification at random")
		}
	}
}

// TestCanonicalIsUnambiguous: two different entries cannot serialize to the same bytes by
// concatenation accident, which would let one be substituted for the other.
func TestCanonicalIsUnambiguous(t *testing.T) {
	a := adminaudit.Entry{ActorAdminID: "ab", Target: "c", Action: "d", Reason: "e", Result: "f"}
	b := adminaudit.Entry{ActorAdminID: "a", Target: "bc", Action: "d", Reason: "e", Result: "f"}
	if string(adminaudit.Canonical(a)) == string(adminaudit.Canonical(b)) {
		t.Fatal("two different entries canonicalize identically")
	}
}

// TestDigestKeepsParametersOutOfTheChain: the chain stores a digest, so no parameter value can land in
// a store that is by design never deleted.
func TestDigestKeepsParametersOutOfTheChain(t *testing.T) {
	const secretish = "customer-provided-value-that-should-never-persist"
	d := adminaudit.Digest("tenant-acme", secretish)
	if strings.Contains(d, secretish) {
		t.Fatal("Digest returned the parameter rather than a hash of it")
	}
	if d == adminaudit.Digest("tenant-acme", "something-else") {
		t.Fatal("Digest collides on different parameters")
	}
}

// TestSelectFiltersWithoutMutating: the audit viewer reads a snapshot.
func TestSelectFiltersWithoutMutating(t *testing.T) {
	s := seedChain(t)
	byActor := adminaudit.Select(s, adminaudit.Filter{Actor: "adm-billing_ops"})
	if len(byActor) != 1 || byActor[0].Action != adminaudit.ActionBillingCredit {
		t.Fatalf("filter by actor returned %+v", byActor)
	}
	byAction := adminaudit.Select(s, adminaudit.Filter{Action: adminaudit.ActionAutonomousMerge})
	if len(byAction) != 1 {
		t.Fatalf("filter by action returned %d entries, want 1", len(byAction))
	}
	if !s.Verify().Intact {
		t.Error("filtering altered the chain")
	}
}
