package adminlaunch

import (
	"context"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// agentspend.go is task 9.3's operator half: the console's placement and cap controls, wired to the
// durable stores that §7 and §9.2 created.
//
// # Why an adapter and not a method on the stores
//
// `adminops.AgentSpendSource` is the operator surface's vocabulary — rows for a table, a fleet cap, a
// placement setter. `herosagent`'s stores are the runtime's — a ceiling a checker reads, a placement a
// gate reads. They are the same data at two grains, and the adapter is where that translation lives so
// neither side has to carry the other's shape.
//
// It lives HERE because this is the only package that imports both: `adminops` imports `herosagent`
// (its refusals name the agent's vocabulary), so `herosagent` cannot import back.
//
// # 🔴 What this closes
//
// Until now `AgentSpendSource` was nil on every deployed path. The console rendered its placement
// column and its cap editors, and pressing either wrote to nothing — §6 shipped the control and §7
// shipped the table under it, with no wire between. A surface that accepts a decision and drops it is
// worse than one that refuses: the operator believes the fleet is configured.

// agentSpend implements adminops.AgentSpendSource over the durable stores.
type agentSpend struct {
	placements *herosagent.PGPlacementStore
	caps       *herosagent.PGCapStore
	spend      *herosagent.PGSpendStore
	inferences *herosagent.PGInferenceStore
	now        func() time.Time
	// actor names who the audit trail will attribute a write to. The console's own authorization has
	// already run by the time these are called — `adminops.SetPlacement` authorizes and audits before
	// it delegates — so this is the record on the row, not a second check.
	actor string
}

// Spend returns the per-tenant meter rows the console renders (task 6.5).
func (a *agentSpend) Spend(ctx context.Context) ([]adminops.AgentSpendRow, error) {
	placements, err := a.placements.List(ctx)
	if err != nil {
		return nil, err
	}
	byTenant := map[string]adminops.AgentSpendRow{}
	for _, p := range placements {
		byTenant[p.TenantID] = adminops.AgentSpendRow{
			TenantID:  p.TenantID,
			Placement: string(p.Placement),
			// 🔴 EXPLICIT, because there IS a row. The console distinguishes "somebody switched this off"
			// from "nobody has looked", and the placement store answers it by the row's existence — see
			// migration 0047's header for why the absence is the value.
			PlacementSource: adminops.PlacementExplicit,
		}
	}

	since := a.now().UnixMilli() - herosagent.CapWindow.Milliseconds()
	rows := make([]adminops.AgentSpendRow, 0, len(byTenant))
	for tenantID, row := range byTenant {
		tokens, err := a.spend.SpentSince(ctx, tenantID, since)
		if err != nil {
			return nil, err
		}
		// 🚫 The split between in and out is not reconstructed from a total. `SpentSince` sums both
		// because that is what a ceiling measures, and reporting a guessed split would put two numbers
		// on a page that no query produced. The total goes in `TokensIn` and the console shows a total.
		row.TokensIn = tokens
		if a.inferences != nil {
			n, err := a.inferences.CountFor(ctx, tenantID)
			if err != nil {
				return nil, err
			}
			row.Inferences = n
		}
		// Priced stays FALSE. This deployment holds no price list, so every row reads `unpriced` — the
		// word, never `0` — which is the honest answer and exactly what task 6.5 requires.
		rows = append(rows, row)
	}
	return rows, nil
}

// FleetCap returns the fleet ceiling, or 0 when none is set.
//
// 🔴 Zero here means UNSET, and `adminops.AgentSpendView.FleetCap` documents that and renders it as
// "none is set" rather than as an empty cell. The store's own API returns `ok=false` instead, because
// at the runtime grain a zero would be a ceiling of zero; the translation happens here, once.
func (a *agentSpend) FleetCap(ctx context.Context) (int64, error) {
	c, ok, err := a.caps.Get(ctx, herosagent.FleetTenantID)
	if err != nil || !ok {
		return 0, err
	}
	return c.MaxTokens, nil
}

// SetFleetCap sets or REMOVES the fleet ceiling. Zero removes it, because that is the vocabulary the
// console's editor already has — and removal is a delete in the store, where zero is refused.
func (a *agentSpend) SetFleetCap(ctx context.Context, tokens int64) error {
	return a.setCap(ctx, herosagent.FleetTenantID, tokens, "set from the operator console")
}

// SetTenantCap sets or removes one tenant's ceiling.
func (a *agentSpend) SetTenantCap(ctx context.Context, tenantID string, tokens int64) error {
	return a.setCap(ctx, tenantID, tokens, "set from the operator console")
}

func (a *agentSpend) setCap(ctx context.Context, tenantID string, tokens int64, reason string) error {
	if tokens <= 0 {
		return a.caps.Delete(ctx, tenantID)
	}
	return a.caps.Set(ctx, herosagent.Cap{
		TenantID: tenantID, MaxTokens: tokens, Reason: reason,
		SetBy: a.actor, UpdatedAtMS: a.now().UnixMilli(),
	})
}

// SetPlacement records an operator's decision, and marks or clears the stale flag with it (task 9.5).
//
// 🔴 The two happen TOGETHER and that is the whole of 9.5. Disabling a tenant without marking its
// stored inferences leaves a graph that keeps rendering agent-authored facts as current, indefinitely,
// with nothing maintaining them — which is the failure "returns every surface to rule-derived facts"
// is about. 🚫 The inferences are NOT deleted: see stale.go for why retention is the answer.
func (a *agentSpend) SetPlacement(ctx context.Context, tenantID, placement string) error {
	p, err := herosagent.ParsePlacement(placement)
	if err != nil {
		return err
	}
	if err := a.placements.Set(ctx, herosagent.TenantPlacement{
		TenantID: tenantID, Placement: p,
		Reason: "set from the operator console", SetBy: a.actor,
		UpdatedAtMS: a.now().UnixMilli(),
	}); err != nil {
		return err
	}
	if a.spend == nil {
		return nil
	}

	// 🔴 The mark is written AFTER the placement, and its failure does not undo the placement. An
	// operator switching a tenant off during an incident must succeed at switching it off even if the
	// bookkeeping write fails — the important half is that nothing analyses, and a rollback here would
	// leave the tenant enabled to preserve a flag.
	if p == herosagent.PlacementDisabled {
		if _, err := a.spend.MarkStale(ctx, tenantID, herosagent.StaleDisabled, a.now().UnixMilli()); err != nil {
			return fmt.Errorf("the placement was set to %s and its stored inferences could not be "+
				"marked stale, so their graphs will render as current: %w", p, err)
		}
		return nil
	}
	// Re-enabling clears the mark. 🚫 It re-runs nothing — the stored facts are still the ones written
	// before the gap, and the surface says so.
	if _, err := a.spend.ClearStale(ctx, tenantID); err != nil {
		return fmt.Errorf("the placement was set to %s and the stale marks on its stored inferences "+
			"could not be cleared: %w", p, err)
	}
	return nil
}

// newAgentSpend wires the adapter, or returns nil when this deployment has no platform database — in
// which case the console renders every one of these as the honest absence it already knows how to show.
func newAgentSpend(placements *herosagent.PGPlacementStore, caps *herosagent.PGCapStore,
	spend *herosagent.PGSpendStore, inferences *herosagent.PGInferenceStore,
	now func() time.Time, actor string) adminops.AgentSpendSource {

	if placements == nil || caps == nil {
		return nil
	}
	return &agentSpend{
		placements: placements, caps: caps, spend: spend, inferences: inferences,
		now: now, actor: actor,
	}
}

var _ adminops.AgentSpendSource = (*agentSpend)(nil)
