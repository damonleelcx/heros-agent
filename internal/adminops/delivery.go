package adminops

import (
	"context"
	"errors"
	"sort"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/changedelivery"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// delivery.go is the operator's view of the most consequential thing the platform does to a customer:
// change their repository (P26 wave 26b).
//
// # The asymmetry that shapes the whole file: a merge is OBSERVED, never inferred
//
// A pull request that closed may have been merged, squashed, rebased, or abandoned, and only one of
// those is a delivery. P12 already records this correctly — `StateMerged` is written from an
// observation of a merge into the target branch — and the job here is to not lose it on the way to a
// screen. So [MergeState] has three values, and the third, `unknown`, is a real answer rather than a
// gap to be papered over with the most likely outcome.
//
// # Why this surface reads and does nothing
//
// Delivery is downstream of verification and never a path around it, and in the default mode the
// platform holds NO forge credential (ADR-005) — so an operator control that retried a delivery would
// have to create one. This service has no writer, and `TestTheDeliverySurfaceIsReadOnly` enumerates
// its methods to keep it that way.
//
// # Why the cross-tenant read is audited on the read's own code path
//
// The same rule the cross-tenant aggregates follow: an authorized fleet-wide read is still a privacy
// event, and a poller would produce a second, lagging record that is silently incomplete after a
// crash. The audit entry is written BEFORE the read is served, and a read that cannot be logged does
// not happen.

// MergeState is what the platform knows about a delivery's merge. Three values, because two would
// force `unknown` to be rendered as one of the others.
type MergeState string

const (
	// MergeObserved: the platform OBSERVED a merge into the target branch. The only state that means a
	// change shipped, and P7 gainshare's observable input.
	MergeObserved MergeState = "merged"
	// MergeClosedUnmerged: the pull request closed without merging — including a supersession, where a
	// newer verified proposal replaced it.
	MergeClosedUnmerged MergeState = "closed_unmerged"
	// MergeUnknown: no merge has been observed and the pull request is still open, or the record does
	// not say. 🔴 It is rendered as *state unknown*, never as the most likely outcome.
	MergeUnknown MergeState = "unknown"
)

// MergeStates lists the three in the order a surface renders a legend.
func MergeStates() []MergeState {
	return []MergeState{MergeObserved, MergeClosedUnmerged, MergeUnknown}
}

// mergeStateOf maps a P12 lifecycle state onto the three.
//
// 🔴 The mapping is a function with a test rather than a lookup at each call site, because the one
// mistake that matters here — reading `closed` as `merged` — is a single wrong case away, and it would
// tell an operator (and, downstream of the same figure, a customer's invoice) that a change shipped
// when it did not.
func mergeStateOf(s forgedelivery.State) MergeState {
	switch s {
	case forgedelivery.StateMerged, forgedelivery.StateReverted:
		// Reverted is a FURTHER state, not a contradiction: the merge was observed and the merged row
		// stays. The row's own lifecycle state carries the revert, so a disputed billed period is
		// answerable as a sequence rather than as a single flag.
		return MergeObserved
	case forgedelivery.StateClosed, forgedelivery.StateSuperseded:
		return MergeClosedUnmerged
	case forgedelivery.StateOpened, forgedelivery.StateUpdated:
		return MergeUnknown
	}
	return MergeUnknown
}

// DeliveryRow is one P12 delivery as an operator reads it.
type DeliveryRow struct {
	DeliveryID     string `json:"delivery_id"`
	TenantID       string `json:"tenant_id"`
	ConfigHash     string `json:"config_hash"`
	SourceRevision string `json:"source_revision"`
	Target         string `json:"target"`
	ForgeRef       string `json:"forge_ref,omitempty"`
	// Mode is the credential path the delivery took — `ci` (the platform holds no forge credential) or
	// `app` (the opt-in hosted Git App). Carried so "which credential opened this" is answerable.
	Mode string `json:"mode"`
	// State is the P12 lifecycle state, VERBATIM. The console renders what the record says.
	State string `json:"state"`
	// Merge is the three-valued outcome derived from State by exactly one function.
	Merge       MergeState `json:"merge"`
	MergeCommit string     `json:"merge_commit,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	// AuditTarget is the audit-chain target this delivery's autonomous merges would be recorded under,
	// when the chain covers the path. Empty for a CI-mediated delivery, whose merge the chain
	// structurally does not hold — see MergeCoverage().
	AuditTarget string `json:"audit_target,omitempty"`
}

// DeliveryCount is one aggregate figure with the filter that drills into it.
//
// The filter travels WITH the count for one reason: an aggregate whose samples are unreachable hides a
// single tenant's pathological repository inside a fleet-wide number, and "click through to the
// records" only works if the console does not have to invent the query.
type DeliveryCount struct {
	Label string `json:"label"`
	Value int    `json:"value"`
	// DrillDown is the query string that lists the records behind this count.
	DrillDown string `json:"drill_down"`
}

// RolloutCauseCount is one typed undeliverable cause and how many change cells carry it.
type RolloutCauseCount struct {
	// Cause is the STABLE identifier, never prose. The same cause renders identically wherever it
	// appears, on this console and on the customer's.
	Cause string `json:"cause"`
	// Owner is who can close it — "nobody", "you", "the platform" — computed by the changedelivery
	// package rather than inferred from the identifier here, so the two cannot disagree.
	Owner string `json:"owner"`
	// Permanent marks a boundary rather than unbuilt work. 🔴 A permanent cause names no missing
	// artifact: attaching one would turn a boundary into a promise.
	Permanent       bool   `json:"permanent"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Label           string `json:"label"`
	Count           int    `json:"count"`
}

// RolloutStageRow is one (axis, change kind) row of the ADR-010 change-delivery picture.
type RolloutStageRow struct {
	Axis   string `json:"axis"`
	Change string `json:"change"`
	Route  string `json:"route"`
	Status string `json:"status"`
	Cause  string `json:"cause,omitempty"`
	Owner  string `json:"owner,omitempty"`
	// Permanent and MissingArtifact carry the boundary/backlog asymmetry to the surface unchanged.
	Permanent       bool   `json:"permanent,omitempty"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
}

// DeliveryView is the operator's delivery read model.
type DeliveryView struct {
	// TenantID is empty for the cross-tenant aggregate and set for a per-tenant view. The two are
	// different reads with different audit entries, not one read with a filter.
	TenantID string        `json:"tenant_id,omitempty"`
	Rows     []DeliveryRow `json:"rows"`
	// Counts are the aggregate figures, each carrying its own drill-down.
	Counts []DeliveryCount `json:"counts"`
	// RolloutStages is the ADR-010 change-delivery picture: what each route does for each change kind.
	RolloutStages []RolloutStageRow `json:"rollout_stages"`
	// Undeliverable is the count of change cells no route can carry, broken out by typed cause.
	//
	// 🔴 Never a single total. The three causes are answered by three different people, and one number
	// tells all three of them the same useless thing.
	Undeliverable      []RolloutCauseCount `json:"undeliverable"`
	UndeliverableTotal int                 `json:"undeliverable_total"`
	// MergeCoverage restates which merge paths the audit chain records, from the ONE place that says
	// so, so the delivery surface and the audit surface cannot describe the boundary two ways.
	MergeCoverage MergePathCoverage `json:"merge_coverage"`
	// Degraded and Detail report that the record could not be read — distinct from "no deliveries".
	Degraded bool   `json:"degraded"`
	Detail   string `json:"detail,omitempty"`
	// ReadOnly is stated on the wire, not assumed by the client. It is what the console renders to say
	// that this surface shows a problem it cannot act on, which is this phase's deliberate boundary.
	ReadOnly bool   `json:"read_only"`
	Source   string `json:"source"`
}

// DeliveryService serves the delivery read models. READ-ONLY: it has no method that writes.
type DeliveryService struct {
	exec     *Executor
	records  forgedelivery.Recorder
	accounts account.Store
}

// NewDeliveryService wires the read model.
//
// The recorder may be nil — a deployment that carries no delivery subsystem. The view then reports
// `not mounted` through the API's own mounted() guard rather than an empty table, because an empty
// table implying "no deliveries" is the failure this whole phase is about.
func NewDeliveryService(exec *Executor, records forgedelivery.Recorder, accounts account.Store) (*DeliveryService, error) {
	if exec == nil {
		return nil, errors.New("adminops: the delivery read model needs the command path")
	}
	if accounts == nil {
		return nil, errors.New("adminops: the delivery read model needs the account store to enumerate tenants")
	}
	return &DeliveryService{exec: exec, records: records, accounts: accounts}, nil
}

// Tenant returns one tenant's delivery records.
func (s *DeliveryService) Tenant(ctx context.Context, tenantID string) (DeliveryView, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapDeliveryRead, TenantTarget(tenantID))
	if err != nil {
		return DeliveryView{}, err
	}
	// Audited on the SAME code path as the read, before it is served. A crash between the read and the
	// log would otherwise leave a look at a tenant's repository history with no record of it.
	if err := s.logRead(sess.AdminID, TenantTarget(tenantID), "tenant"); err != nil {
		return DeliveryView{}, err
	}
	view := s.base()
	view.TenantID = tenantID
	if s.records == nil {
		view.Degraded, view.Detail = true, "this deployment carries no delivery record"
		return view, nil
	}
	heads, err := s.records.ListForTenant(ctx, tenantID)
	if err != nil {
		view.Degraded, view.Detail = true, err.Error()
		return view, nil
	}
	view.Rows = rowsOf(heads)
	view.Counts = countsOf(view.Rows, tenantID)
	return view, nil
}

// Fleet returns the cross-tenant delivery aggregate.
func (s *DeliveryService) Fleet(ctx context.Context) (DeliveryView, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapDeliveryRead, TargetGlobal)
	if err != nil {
		return DeliveryView{}, err
	}
	if err := s.logRead(sess.AdminID, TargetGlobal, "fleet"); err != nil {
		return DeliveryView{}, err
	}
	view := s.base()
	if s.records == nil {
		view.Degraded, view.Detail = true, "this deployment carries no delivery record"
		return view, nil
	}
	for _, acct := range s.accounts.List() {
		heads, err := s.records.ListForTenant(ctx, acct.CustomerID)
		if err != nil {
			// One tenant's record being unreadable makes the AGGREGATE incomplete, and an incomplete
			// aggregate rendered as a total is a wrong number. Say so rather than quietly summing the rest.
			view.Degraded, view.Detail = true, err.Error()
			return view, nil
		}
		view.Rows = append(view.Rows, rowsOf(heads)...)
	}
	sort.SliceStable(view.Rows, func(i, j int) bool {
		if view.Rows[i].TenantID == view.Rows[j].TenantID {
			return view.Rows[i].DeliveryID < view.Rows[j].DeliveryID
		}
		return view.Rows[i].TenantID < view.Rows[j].TenantID
	})
	view.Counts = countsOf(view.Rows, "")
	return view, nil
}

// History returns one delivery's full lifecycle, in append order — the drill-down behind a row.
func (s *DeliveryService) History(ctx context.Context, tenantID, deliveryID string) ([]forgedelivery.Entry, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapDeliveryRead, TenantTarget(tenantID))
	if err != nil {
		return nil, err
	}
	if err := s.logRead(sess.AdminID, TenantTarget(tenantID), "delivery-history"); err != nil {
		return nil, err
	}
	if s.records == nil {
		return nil, errors.New("adminops: this deployment carries no delivery record")
	}
	return s.records.History(ctx, deliveryID)
}

// base builds the parts of the view that do not depend on any record: the ADR-010 rollout picture and
// the merge-path coverage statement.
//
// They come from the packages that own them — `changedelivery.Table()` and [MergeCoverage] — and are
// rendered as received. The console computes nothing, and neither does this file.
func (s *DeliveryService) base() DeliveryView {
	view := DeliveryView{
		// Empty rather than nil: a nil slice marshals to `null`, and a client reading a list's length
		// off `null` crashes.
		Rows:          []DeliveryRow{},
		Counts:        []DeliveryCount{},
		RolloutStages: []RolloutStageRow{},
		Undeliverable: []RolloutCauseCount{},
		ReadOnly:      true,
		MergeCoverage: MergeCoverage(),
		Source:        "p12 delivery record + the ADR-010 change-delivery table",
	}
	byCause := map[string]*RolloutCauseCount{}
	for _, cell := range changedelivery.Table() {
		row := RolloutStageRow{
			Axis: cell.Axis, Change: string(cell.Change), Route: string(cell.Route),
			Status: string(cell.Status), Cause: string(cell.Cause),
			MissingArtifact: cell.MissingArtifact, Note: cell.Note,
		}
		if cell.Cause != "" {
			row.Owner = cell.Cause.Owner()
			row.Permanent = cell.Cause.Permanent()
		}
		view.RolloutStages = append(view.RolloutStages, row)
		if !cell.Refused() {
			continue
		}
		view.UndeliverableTotal++
		c, ok := byCause[string(cell.Cause)]
		if !ok {
			c = &RolloutCauseCount{
				Cause: string(cell.Cause), Owner: cell.Cause.Owner(),
				Permanent: cell.Cause.Permanent(), Label: cell.Cause.Label(),
			}
			byCause[string(cell.Cause)] = c
		}
		// 🔴 A permanent cause carries no missing artifact, ever. The changedelivery package enforces
		// that on its own cells; carrying it forward unchanged is what keeps a boundary from acquiring a
		// roadmap item on the way to a screen.
		if !c.Permanent && cell.MissingArtifact != "" {
			c.MissingArtifact = cell.MissingArtifact
		}
		c.Count++
	}
	// Evaluation order — nobody / you / the platform — not alphabetical and not by count. A surface that
	// sorted by volume would put the platform's backlog first and the permanent boundary last, which is
	// the reading order that sends an engineer to do work that will not help.
	for _, cause := range changedelivery.Causes() {
		if c, ok := byCause[string(cause)]; ok {
			view.Undeliverable = append(view.Undeliverable, *c)
		}
	}
	return view
}

// logRead records an authorized delivery read. A read that cannot be logged does not happen.
func (s *DeliveryService) logRead(adminID, target, scope string) error {
	if _, err := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: adminID,
		Target:       target,
		Action:       adminaudit.ActionCrossTenantView,
		Reason:       "delivery oversight read",
		Result:       "viewed",
		Evidence:     map[string]string{"read_model": "delivery", "scope": scope},
		CreatedAt:    s.exec.Now(),
	}); err != nil {
		return errors.New("adminops: delivery read refused — it could not be logged: " + err.Error())
	}
	return nil
}

func rowsOf(heads []forgedelivery.DeliveryHead) []DeliveryRow {
	out := make([]DeliveryRow, 0, len(heads))
	for _, h := range heads {
		row := DeliveryRow{
			DeliveryID: h.DeliveryID, TenantID: h.TenantID, ConfigHash: h.ConfigHash,
			SourceRevision: h.SourceRevision, Target: h.Target, ForgeRef: h.ForgeRef,
			Mode: string(h.Mode), State: string(h.State), Merge: mergeStateOf(h.State),
			MergeCommit: h.MergeCommit, Reason: h.Reason,
		}
		// The chain covers merges the P6 loop performs itself. A CI-mediated delivery merges in the
		// customer's CI under a credential the platform does not hold, so it names no chain entry —
		// stating that here rather than linking anyway is what keeps the link from implying coverage.
		if h.Mode == forgedelivery.ModeApp && row.Merge == MergeObserved {
			row.AuditTarget = TenantTarget(h.TenantID)
		}
		out = append(out, row)
	}
	return out
}

// countsOf builds the aggregate figures, each with the drill-down that reaches its records.
func countsOf(rows []DeliveryRow, tenantID string) []DeliveryCount {
	byMerge := map[MergeState]int{}
	for _, r := range rows {
		byMerge[r.Merge]++
	}
	scope := ""
	if tenantID != "" {
		scope = "&tenant=" + tenantID
	}
	out := []DeliveryCount{{Label: "deliveries", Value: len(rows), DrillDown: "?merge=all" + scope}}
	for _, m := range MergeStates() {
		out = append(out, DeliveryCount{
			Label: string(m), Value: byMerge[m], DrillDown: "?merge=" + string(m) + scope,
		})
	}
	return out
}
