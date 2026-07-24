package adminops

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// tenant.go is the tenant lifecycle surface: search/view, suspend/reactivate, quota (FR7).
//
// # Why suspension halts autonomous merges through the admission gate rather than a side effect here
//
// FR7 says a suspension halts the tenant's autonomous merges. It would be easy to implement that by
// having Suspend reach into the running loop. It is implemented instead by the loop CONSULTING
// tenant status through Admission before every merge, for two reasons: a loop that is not running
// right now would miss a push-style signal and resume merging when it restarts, and a second
// enforcement point is a second thing that can drift from the first. One state, read at the moment
// it matters.

// TenantView is the operator's read model of one tenant. It carries plan NAMES and limit names, never
// prices — the whole console is priceless by construction (FR28).
type TenantView struct {
	TenantID string `json:"tenant_id"`
	// PlanID and PlanName come from the account and the config store respectively. PlanName is what
	// the console renders; PlanID is what an operator quotes in a ticket.
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// PlanConfigVersion is the published plan-config version the tenant resolves against, so an
	// operator can tell a stale entitlement from a wrong one.
	PlanConfigVersion string `json:"plan_config_version"`
	Status            string `json:"status"`
	SuspensionReason  string `json:"suspension_reason,omitempty"`
	SuspendedAt       string `json:"suspended_at,omitempty"`
	// QuotaOverrides are the operator-set allowances, keyed by limit name.
	QuotaOverrides map[string]float64 `json:"quota_overrides,omitempty"`
	// AutonomousMergesHalted is the answer to the question an operator actually asks — "is this
	// tenant's loop stopped right now" — resolved through the same admission gate the loop reads, so
	// the screen cannot say "running" while the gate says "halted".
	AutonomousMergesHalted bool   `json:"autonomous_merges_halted"`
	HaltReason             string `json:"halt_reason,omitempty"`
	GainshareConsent       bool   `json:"gainshare_consent"`
}

// TenantService implements the tenant lifecycle commands.
type TenantService struct {
	exec      *Executor
	accounts  account.Store
	plans     *plancfg.Resolver
	admission *Admission
}

// NewTenantService wires the service.
func NewTenantService(exec *Executor, accounts account.Store, plans *plancfg.Resolver, admission *Admission) (*TenantService, error) {
	if exec == nil || accounts == nil {
		return nil, errors.New("adminops: the tenant service needs the command path and the account store")
	}
	return &TenantService{exec: exec, accounts: accounts, plans: plans, admission: admission}, nil
}

// List returns every tenant the operator may see. Permission-gated (CapTenantRead) but reason-free:
// it is a read.
func (s *TenantService) List(ctx context.Context) ([]TenantView, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapTenantRead, TargetGlobal); err != nil {
		return nil, err
	}
	accts := s.accounts.List()
	out := make([]TenantView, 0, len(accts))
	for _, a := range accts {
		out = append(out, s.view(a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}

// Get returns one tenant.
func (s *TenantService) Get(ctx context.Context, tenantID string) (TenantView, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapTenantRead, TenantTarget(tenantID)); err != nil {
		return TenantView{}, err
	}
	a, err := s.accounts.Get(tenantID)
	if err != nil {
		return TenantView{}, err
	}
	return s.view(a), nil
}

// Search filters tenants by a substring of the tenant id or plan name — the "search/view tenants"
// half of FR7. Case-insensitive, because an operator pasting an id from a ticket should not have to
// match its case.
func (s *TenantService) Search(ctx context.Context, query string) ([]TenantView, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all, nil
	}
	var out []TenantView
	for _, v := range all {
		if strings.Contains(strings.ToLower(v.TenantID), q) || strings.Contains(strings.ToLower(v.PlanName), q) {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *TenantService) view(a account.Account) TenantView {
	v := TenantView{
		TenantID: a.CustomerID, PlanID: a.ActivePlanID, PlanConfigVersion: a.PlanConfigVersion,
		Status: string(account.StatusActive), QuotaOverrides: a.QuotaOverrides,
		GainshareConsent: a.GainshareConsent,
	}
	if a.Status != "" {
		v.Status = string(a.Status)
	}
	v.SuspensionReason = a.SuspensionReason
	if a.SuspendedAt != nil {
		v.SuspendedAt = a.SuspendedAt.UTC().Format(time.RFC3339)
	}
	if s.plans != nil {
		if p, err := s.plans.ResolvePlan(a.ActivePlanID); err == nil {
			v.PlanName = p.DisplayName
		}
	}
	if v.PlanName == "" {
		v.PlanName = a.ActivePlanID
	}
	if s.admission != nil {
		allowed, why, err := s.admission.AllowMerge(a.CustomerID)
		v.AutonomousMergesHalted = !allowed || err != nil
		switch {
		case err != nil:
			// An indeterminate answer is reported as halted AND said out loud. Rendering it as
			// "running" would be the console lying in the direction that gets a fleet merged.
			v.HaltReason = "kill-switch state indeterminate — merges fail closed to halt"
		case !allowed:
			v.HaltReason = why
		}
	}
	return v
}

// Suspend halts a tenant. Permission-gated, reason-required, confirmed, audited — and the suspension
// itself is what stops the tenant's autonomous merges, via the admission gate the loop reads.
func (s *TenantService) Suspend(ctx context.Context, tenantID, reason string, confirm Confirmation) (Receipt, error) {
	target := TenantTarget(tenantID)
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapTenantSuspend,
		Action:     adminaudit.ActionTenantSuspend,
		Target:     target,
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{tenantID, string(account.StatusSuspended)},
		Evidence:   map[string]string{"tenant_id": tenantID, "to_status": string(account.StatusSuspended)},
	}, func(context.Context) (map[string]string, error) {
		a, err := s.accounts.SetStatus(tenantID, account.StatusSuspended, reason, s.exec.Now())
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"status": string(a.Status), "autonomous_merges": "halted",
		}, nil
	})
}

// Reactivate restores a suspended tenant.
func (s *TenantService) Reactivate(ctx context.Context, tenantID, reason string, confirm Confirmation) (Receipt, error) {
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapTenantSuspend,
		Action:     adminaudit.ActionTenantReactivate,
		Target:     TenantTarget(tenantID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{tenantID, string(account.StatusActive)},
		Evidence:   map[string]string{"tenant_id": tenantID, "to_status": string(account.StatusActive)},
	}, func(context.Context) (map[string]string, error) {
		a, err := s.accounts.SetStatus(tenantID, account.StatusActive, reason, s.exec.Now())
		if err != nil {
			return nil, err
		}
		return map[string]string{"status": string(a.Status), "autonomous_merges": "restored"}, nil
	})
}

// SetQuota sets one per-tenant allowance override. A NaN value clears the override and returns the
// tenant to the plan's published allowance.
//
// The value is a QUANTITY (seats, retention days, a SUM-band ceiling in the SUM unit), never a price.
// Prices are references resolved from configuration and never pass through this path.
func (s *TenantService) SetQuota(ctx context.Context, tenantID string, limit plancfg.Limit, value float64, reason string, confirm Confirmation) (Receipt, error) {
	if !knownLimit(limit) {
		return Receipt{}, fmt.Errorf("adminops: %q is not a plan limit this platform meters", limit)
	}
	rendered := "cleared"
	if !math.IsNaN(value) {
		rendered = strconv.FormatFloat(value, 'f', -1, 64)
	}
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapTenantQuota,
		Action:     adminaudit.ActionTenantSetQuota,
		Target:     TenantTarget(tenantID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{tenantID, string(limit), rendered},
		Evidence:   map[string]string{"tenant_id": tenantID, "limit": string(limit), "value": rendered},
	}, func(context.Context) (map[string]string, error) {
		if _, err := s.accounts.SetQuota(tenantID, string(limit), value); err != nil {
			return nil, err
		}
		return map[string]string{"limit": string(limit), "value": rendered}, nil
	})
}

// knownLimit rejects a limit the platform does not meter, so a typo becomes an error rather than an
// override that silently never applies.
func knownLimit(l plancfg.Limit) bool {
	for _, k := range plancfg.Limits {
		if k == l {
			return true
		}
	}
	return false
}

// ── Admission: the P6-facing brake ──────────────────────────────────────────────────────────────

// Admission answers the question the P6 loop asks immediately before every merge: may this tenant's
// autonomous optimizer merge right now?
//
// It satisfies optimizer.MergeAdmission. Two inputs, both read at the moment of the question:
// the tenant's lifecycle status (FR7) and the operator kill switch (FR12, wired in killswitch.go).
// Both are read FAIL-CLOSED: an error from either means halt.
type Admission struct {
	accounts account.Store
	kill     KillStateReader
}

// KillStateReader is the kill-switch half of admission. Declared as an interface so Admission can be
// built before the switch exists in a deployment and so the fail-closed behaviour has one definition.
type KillStateReader interface {
	// HaltsMerge reports whether a merge for this tenant is currently halted, and why. An error means
	// the state is INDETERMINATE and the caller must halt.
	HaltsMerge(tenantID string) (halted bool, reason string, err error)
}

// NewAdmission builds the brake. kill may be nil in a deployment with no operator kill switch, in
// which case only tenant status is consulted.
func NewAdmission(accounts account.Store, kill KillStateReader) (*Admission, error) {
	if accounts == nil {
		return nil, errors.New("adminops: admission needs the account store to read tenant status")
	}
	return &Admission{accounts: accounts, kill: kill}, nil
}

// SetKillSwitch attaches the kill-switch reader after construction, which is the order the console
// wires its dependencies in.
func (a *Admission) SetKillSwitch(k KillStateReader) { a.kill = k }

// AllowMerge implements optimizer.MergeAdmission.
func (a *Admission) AllowMerge(tenantID string) (bool, string, error) {
	// The kill switch is checked FIRST: it is the platform-level brake, and a global arm must halt a
	// tenant whose account row cannot even be read.
	if a.kill != nil {
		halted, why, err := a.kill.HaltsMerge(tenantID)
		if err != nil {
			return false, "kill-switch state indeterminate", err
		}
		if halted {
			return false, why, nil
		}
	}
	acct, err := a.accounts.Get(tenantID)
	if err != nil {
		// An unreadable tenant is indeterminate, not permitted. Failing open here would let a store
		// outage resume the whole fleet.
		return false, "tenant status indeterminate", fmt.Errorf("adminops: cannot read tenant %q status: %w", tenantID, err)
	}
	if acct.Status.Suspended() {
		reason := "tenant suspended"
		if acct.SuspensionReason != "" {
			reason += ": " + acct.SuspensionReason
		}
		return false, reason, nil
	}
	return true, "", nil
}
