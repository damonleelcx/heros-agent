package evolve

import (
	"context"
	"fmt"

	"github.com/heros-foreal/agentd/internal/agentlayout"
	"github.com/heros-foreal/agentd/internal/approval"
	"github.com/heros-foreal/agentd/internal/harness"
	"github.com/heros-foreal/agentd/internal/memorylayer"
	"github.com/heros-foreal/agentd/internal/platform"
	"github.com/heros-foreal/agentd/internal/promptlayer"
	"github.com/heros-foreal/agentd/internal/toolindex"
	"github.com/heros-foreal/agentd/internal/tooling"
)

// ApplyApprovedProposal commits a vetted mutation and records rollback_ref (spine step 4).
func ApplyApprovedProposal(ctx context.Context, rt *platform.Runtime, proposalID string) error {
	if rt == nil || rt.DB == nil {
		return fmt.Errorf("nil runtime")
	}
	db := rt.DB
	p, err := approval.Get(db, proposalID)
	if err != nil {
		return err
	}
	if p.Status != approval.StatusPending {
		return fmt.Errorf("proposal not pending")
	}
	var rollback string
	switch approval.Layer(p.Layer) {
	case approval.LayerPrompt:
		rollback, err = promptlayer.ApplyMutation(db, rt.Cfg.DataDir, agentlayout.SanitizeTenantScope(p.TenantID), proposalID, p.DiffText)
	case approval.LayerContext:
		rollback, err = memorylayer.ApplyContextMutation(ctx, db, rt.Neo, rt.VectorInfra(), p.TenantID, p.DiffText)
	case approval.LayerHarness:
		rollback, err = harness.ApplyTopologyMutation(db, p.DiffText)
	case approval.LayerTooling:
		pol := toolindex.SyncPolicyFromConfig(rt.Cfg)
		err = tooling.ApplyToolMutation(db, rt.Cfg.DataDir, pol, p.TenantID, proposalID, p.DiffText)
		rollback = "tool_registry"
	default:
		return fmt.Errorf("unknown layer %s", p.Layer)
	}
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE proposals SET status = ?, reviewed_at = datetime('now'), rollback_ref = ? WHERE id = ?`,
		string(approval.StatusApproved), rollback, proposalID)
	return err
}
