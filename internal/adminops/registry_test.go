package adminops_test

import (
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/metering"
)

// registry_test.go covers task 5.3 — model-registry administration is audited and NON-RETROACTIVE
// on closed periods (FR10).

const (
	fixtureModel    = "sonnet-5"
	fixturePriceRef = "price_ref_sonnet5_v1"
	repointedRef    = "price_ref_sonnet5_v2"
	closedPeriodID  = "2026-02"
)

// seedRegistry adds a model and closes a period with it in force.
func (h *harness) seedRegistry(t *testing.T) {
	t.Helper()
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	if _, err := h.registry.AddModel(ctx, fixtureModel, "anthropic", fixturePriceRef,
		"onboarding the model for the new eval set", adminops.Confirm()); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	if err := h.models.ClosePeriod(closedPeriodID); err != nil {
		t.Fatalf("ClosePeriod: %v", err)
	}
}

// TestRepointingAPriceRefIsAuditedAndNonRetroactive is FR10's load-bearing scenario.
func TestRepointingAPriceRefIsAuditedAndNonRetroactive(t *testing.T) {
	h := newHarness(t)
	h.seedRegistry(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	// The closed period and the open period both resolve to the original reference right now.
	openPeriod := h.period.ID
	if ref, ok := h.models.PriceRefAt(fixtureModel, closedPeriodID); !ok || ref != fixturePriceRef {
		t.Fatalf("closed-period price ref before the repoint = %q, %v", ref, ok)
	}
	if ref, ok := h.models.PriceRefAt(fixtureModel, openPeriod); !ok || ref != fixturePriceRef {
		t.Fatalf("open-period price ref before the repoint = %q, %v", ref, ok)
	}

	// ── Repoint ──
	if _, err := h.registry.RepointPriceRef(ctx, fixtureModel, repointedRef,
		"vendor published a new price catalogue entry for this model", adminops.Confirm()); err != nil {
		t.Fatalf("RepointPriceRef: %v", err)
	}

	// The CLOSED period keeps the reference in effect when it closed.
	if ref, _ := h.models.PriceRefAt(fixtureModel, closedPeriodID); ref != fixturePriceRef {
		t.Errorf("a closed period's price reference was rewritten to %q — that silently re-derives an "+
			"invoice the customer already reconciled", ref)
	}
	// Only the open/future period uses the new one.
	if ref, _ := h.models.PriceRefAt(fixtureModel, openPeriod); ref != repointedRef {
		t.Errorf("the open period's price reference = %q, want the repointed one", ref)
	}
	if ref, _ := h.models.PriceRefAt(fixtureModel, "2026-12"); ref != repointedRef {
		t.Errorf("a future period's price reference = %q, want the repointed one", ref)
	}

	// ── Audited with actor, model, reason and timestamp ──
	entries := h.entriesFor(adminaudit.ActionRegistryRepointPrice)
	if len(entries) != 2 {
		t.Fatalf("the repoint wrote %d audit entries, want 2", len(entries))
	}
	outcome := entries[1]
	if outcome.ActorAdminID != h.adminIDs[adminrbac.RolePlatformSRE] {
		t.Errorf("audit actor = %q", outcome.ActorAdminID)
	}
	if outcome.Target != adminops.ModelTarget(fixtureModel) {
		t.Errorf("audit target = %q, want the model", outcome.Target)
	}
	if outcome.Evidence["from_price_ref"] != fixturePriceRef || outcome.Evidence["to_price_ref"] != repointedRef {
		t.Errorf("the audit entry does not record the reference change: %v", outcome.Evidence)
	}
	if outcome.Reason == "" || outcome.CreatedAt.IsZero() {
		t.Error("the repoint entry is missing its reason or timestamp")
	}
	h.assertChainIntact()
}

// TestClosedPeriodSUMIsUnchangedByARepoint asserts the consequence that actually matters: SUM
// re-derived for a closed period after a repoint is the SAME number.
//
// It runs the REAL P2.5 derivation over the REAL cost events rather than comparing configuration, so
// this would catch a repoint that reached the metering path by some route the registry does not know
// about.
func TestClosedPeriodSUMIsUnchangedByARepoint(t *testing.T) {
	h := newHarness(t)
	h.seedRegistry(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	h.seedMeteredCharge(tenantAcme)
	closed := h.period
	beforeSUM, err := h.meter.DeriveSUM(tenantAcme, closed)
	if err != nil {
		t.Fatalf("DeriveSUM: %v", err)
	}
	if beforeSUM.Quantity == 0 {
		t.Fatal("precondition: the fixture period has no spend to derive")
	}
	if err := h.models.ClosePeriod(closed.ID); err != nil {
		t.Fatalf("ClosePeriod: %v", err)
	}

	if _, err := h.registry.RepointPriceRef(ctx, fixtureModel, repointedRef,
		"vendor price catalogue update", adminops.Confirm()); err != nil {
		t.Fatalf("RepointPriceRef: %v", err)
	}
	if _, err := h.registry.DeprecateModel(ctx, fixtureModel,
		"model superseded; no new runs should select it", adminops.Confirm()); err != nil {
		t.Fatalf("DeprecateModel: %v", err)
	}

	afterSUM, err := h.meter.DeriveSUM(tenantAcme, closed)
	if err != nil {
		t.Fatalf("DeriveSUM: %v", err)
	}
	if afterSUM.Quantity != beforeSUM.Quantity {
		t.Errorf("closed-period SUM changed from %v to %v after a registry change", beforeSUM.Quantity, afterSUM.Quantity)
	}
	if afterSUM.SourceDigest != beforeSUM.SourceDigest {
		t.Errorf("closed-period SUM source digest changed — the derivation is no longer deterministic")
	}
	// And the closed period still resolves the model it used, even though the model is deprecated.
	if ref, ok := h.models.PriceRefAt(fixtureModel, closed.ID); !ok || ref != fixturePriceRef {
		t.Errorf("a deprecated model's closed-period price reference no longer resolves: %q, %v", ref, ok)
	}
}

// TestDeprecatingAModelIsRecordedAndKeepsThePastResolvable is FR10's second scenario.
func TestDeprecatingAModelIsRecordedAndKeepsThePastResolvable(t *testing.T) {
	h := newHarness(t)
	h.seedRegistry(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.registry.DeprecateModel(ctx, fixtureModel, "superseded by the next generation", adminops.Confirm()); err != nil {
		t.Fatalf("DeprecateModel: %v", err)
	}
	rec, ok := h.models.Get(fixtureModel)
	if !ok {
		t.Fatal("a deprecated model was removed from the registry — closed periods can no longer be explained")
	}
	if !rec.Deprecated || rec.DeprecatedAt.IsZero() {
		t.Errorf("deprecation was not recorded: %+v", rec)
	}
	entries := h.entriesFor(adminaudit.ActionRegistryDeprecate)
	if len(entries) != 2 {
		t.Fatalf("deprecation wrote %d audit entries, want 2", len(entries))
	}
	if entries[1].Evidence["model_id"] != fixtureModel {
		t.Errorf("the deprecation entry does not name the model: %v", entries[1].Evidence)
	}
}

// TestClosingAPeriodTwiceDoesNotResnapshot: a second close must not quietly re-bind the period to
// today's references.
func TestClosingAPeriodTwiceDoesNotResnapshot(t *testing.T) {
	h := newHarness(t)
	h.seedRegistry(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.registry.RepointPriceRef(ctx, fixtureModel, repointedRef, "vendor update", adminops.Confirm()); err != nil {
		t.Fatalf("RepointPriceRef: %v", err)
	}
	if err := h.models.ClosePeriod(closedPeriodID); err != nil {
		t.Fatalf("ClosePeriod (second): %v", err)
	}
	if ref, _ := h.models.PriceRefAt(fixtureModel, closedPeriodID); ref != fixturePriceRef {
		t.Errorf("re-closing a period re-snapshotted it to %q", ref)
	}
}

// TestRegistryAdminIsPermissionGated: Support and Billing-Ops hold no registry capability.
func TestRegistryAdminIsPermissionGated(t *testing.T) {
	h := newHarness(t)
	h.seedRegistry(t)
	for _, role := range []adminrbac.Role{adminrbac.RoleSupport, adminrbac.RoleBillingOps} {
		ctx := h.ctx(role)
		if _, err := h.registry.RepointPriceRef(ctx, fixtureModel, repointedRef, "why not", adminops.Confirm()); !errors.Is(err, adminops.ErrDenied) {
			t.Errorf("%s repointing a price ref: err = %v, want ErrDenied", role, err)
		}
		if _, err := h.registry.List(ctx); !errors.Is(err, adminops.ErrDenied) {
			t.Errorf("%s listing the registry: err = %v, want ErrDenied", role, err)
		}
	}
	if ref, _ := h.models.PriceRefAt(fixtureModel, h.period.ID); ref != fixturePriceRef {
		t.Error("a denied repoint took effect")
	}
}

// TestRegistryChangesRequireAReasonAndConfirmation: FR6's discipline reaches the registry too.
func TestRegistryChangesRequireAReasonAndConfirmation(t *testing.T) {
	h := newHarness(t)
	h.seedRegistry(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.registry.RepointPriceRef(ctx, fixtureModel, repointedRef, "", adminops.Confirm()); !errors.Is(err, adminops.ErrNoReason) {
		t.Errorf("repoint with no reason: err = %v, want ErrNoReason", err)
	}
	if _, err := h.registry.RepointPriceRef(ctx, fixtureModel, repointedRef, "vendor update", adminops.Confirmation{}); !errors.Is(err, adminops.ErrNotConfirmed) {
		t.Errorf("unconfirmed repoint: err = %v, want ErrNotConfirmed", err)
	}
	if ref, _ := h.models.PriceRefAt(fixtureModel, h.period.ID); ref != fixturePriceRef {
		t.Error("a refused repoint took effect")
	}
}

// TestAddingAModelTwiceIsRefused: an "add" must never repoint an existing model's price as a side
// effect.
func TestAddingAModelTwiceIsRefused(t *testing.T) {
	h := newHarness(t)
	h.seedRegistry(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	_, err := h.registry.AddModel(ctx, fixtureModel, "anthropic", repointedRef, "re-adding", adminops.Confirm())
	if !errors.Is(err, adminops.ErrModelExists) {
		t.Fatalf("re-adding a model: err = %v, want ErrModelExists", err)
	}
	if ref, _ := h.models.PriceRefAt(fixtureModel, h.period.ID); ref != fixturePriceRef {
		t.Error("a refused add repointed the model's price reference")
	}
}

// TestUnpricedModelIsRefused: an unpriced model produces SUM gaps, and a gap is worse than a refusal.
func TestUnpricedModelIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	if _, err := h.registry.AddModel(ctx, "unpriced-1", "anthropic", "", "no price yet", adminops.Confirm()); err == nil {
		t.Fatal("a model with no price reference was admitted to the registry")
	}
}

// TestPeriodHelpersAgreeWithTheMeter: the period ids the registry snapshots are the ids the meter uses,
// so a snapshot cannot be keyed to a period nothing else names.
func TestPeriodHelpersAgreeWithTheMeter(t *testing.T) {
	h := newHarness(t)
	if got := metering.MonthPeriod(h.clk.now()).ID; got != h.period.ID {
		t.Fatalf("harness period %q disagrees with the meter's %q", h.period.ID, got)
	}
}
