package forgedelivery_test

import (
	"context"
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/entitlement"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/verification"
)

// TestUnverifiedNeverAutoMerges (P13 13c task 13.3, FR26).
//
// An authored change may be APPLIED without a verification run — that is the user's repository and
// their call. What it may never be is MERGED automatically, at any automation level, because
// auto-merge is the platform acting on its own judgment, and an unverified change is exactly one the
// platform has no judgment about.
//
// The test drives the real enforcement funnel rather than restating the rule. `Prepare` is the single
// place every delivery path converges, and it reads the verdict from the gate AUTHORITATIVELY — not
// from the proposal — so a caller cannot assert its own verification.
func TestUnverifiedNeverAutoMerges(t *testing.T) {
	ctx := context.Background()

	t.Run("an unverified change is refused at the highest automation level", func(t *testing.T) {
		h := newHarness(t)
		// Every entitlement it could need, at the highest level. If the refusal holds HERE it holds
		// everywhere; a test at a lower level would prove much less.
		h.ents.deliver, h.ents.merge = true, true

		// No verdict for this change: the gate has never run it. That is precisely the state an authored
		// change is in the moment it is applied.
		p := proposal(entitlement.LevelAutonomous)
		p.ConfigHash, p.SourceRevision = "authored-hash", "authored-rev"

		_, err := h.d.Prepare(ctx, p, route(fd.ModeCI))
		if !errors.Is(err, fd.ErrNotVerified) {
			t.Fatalf("Prepare = %v, want ErrNotVerified — an unverified change reached the delivery path", err)
		}
	})

	t.Run("a gate-failed change is refused too", func(t *testing.T) {
		h := newHarness(t)
		h.ents.deliver, h.ents.merge = true, true
		// The other unverified-adjacent state: the harness ran and did NOT pass.
		h.gate.verdicts["failed-hash|failed-rev"] = verification.Verdict{GateResult: verification.GateFailSig}

		p := proposal(entitlement.LevelAutonomous)
		p.ConfigHash, p.SourceRevision = "failed-hash", "failed-rev"

		_, err := h.d.Prepare(ctx, p, route(fd.ModeCI))
		if !errors.Is(err, fd.ErrNotVerified) {
			t.Fatalf("Prepare = %v, want ErrNotVerified for a gate-failed change", err)
		}
	})

	t.Run("the gate can go green — so the refusals above are about verification, not about everything", func(t *testing.T) {
		// 🔴 Without this, every assertion above would also pass against a Prepare that refused
		// unconditionally, and the test would prove nothing about verification at all.
		h := newHarness(t)
		h.ents.deliver, h.ents.merge = true, true

		prepared, err := h.d.Prepare(ctx, proposal(entitlement.LevelAutonomous), route(fd.ModeCI))
		if err != nil {
			t.Fatalf("a verified change was refused: %v", err)
		}
		if !prepared.AllowMerge {
			t.Error("a verified change at Autonomous with the entitlement did not allow merge")
		}
	})

	t.Run("nothing was written for the refused deliveries", func(t *testing.T) {
		// Prepare decides; it must not record. A refusal that left a delivery row behind would make the
		// append-only history describe attempts rather than deliveries.
		h := newHarness(t)
		h.ents.deliver, h.ents.merge = true, true
		p := proposal(entitlement.LevelAutonomous)
		p.ConfigHash, p.SourceRevision = "authored-hash", "authored-rev"
		_, _ = h.d.Prepare(ctx, p, route(fd.ModeCI))

		entries, err := h.rec.ListForTenant(ctx, "t1")
		if err != nil {
			t.Fatalf("read record: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("a refused Prepare wrote %d delivery row(s), want 0", len(entries))
		}
	})
}
