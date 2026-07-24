// Package plancfg is the plan-configuration resolver: the P7 answer to "what does this plan entitle,
// what are its limits, and which price does it reference" (design Decision 3).
//
// THE LOAD-BEARING RULE: a plan definition is CONFIGURATION, never code and never git. Limits, the SUM
// band, seat/retention allowances and price REFERENCES are read at runtime from a config store, so a
// plan/price change — or a brand-new plan — takes effect with **no code deploy and no migration**. The
// database holds only the `plan_id` + `plan_config_version` an account points at.
//
// Why this shape rather than constants or a git-committed config file:
//   - Pricing is a business lever pulled on a business cadence. Baking limits into code couples every
//     packaging change to a release.
//   - A git-tracked config file puts business-sensitive numbers into version history permanently. The
//     money-in-git rule forbids exactly that, and TestNoPricedValueInGitTrackedFile is the fence.
//
// What a PlanConfig may carry is therefore deliberately asymmetric: LIMITS are quantities the platform
// enforces itself (it must know them), while PRICES are opaque REFERENCES into the billing provider —
// the platform never holds a dollar amount, so no amount can leak from here into a log, a span, or a
// git object.
package plancfg

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Feature is one gated product surface. Named constants, not literals, for the same reason metric names
// are centralized: a surface spelled two ways is a gate that silently never fires.
type Feature string

const (
	// FeatureCLI is the command-line client — available on every plan including Free.
	FeatureCLI Feature = "cli"
	// FeatureDiscovery is repository discovery — available on every plan including Free.
	FeatureDiscovery Feature = "discovery"
	// FeatureAssistedPR is opening a verified optimization pull request for a human to merge.
	FeatureAssistedPR Feature = "assisted_pr"
	// FeatureDashboard is the web console (seats / retention / SUM band per plan).
	FeatureDashboard Feature = "dashboard"
	// FeatureAutoMerge is P6 Autonomous auto-merge — the highest-authority actor in the system.
	FeatureAutoMerge Feature = "auto_merge"
)

// Features is every gated surface, in packaging order. A new surface is one entry here plus config —
// never an if/else chain at a call site.
var Features = []Feature{FeatureCLI, FeatureDiscovery, FeatureAssistedPR, FeatureDashboard, FeatureAutoMerge}

// Limit is one metered allowance name. These mirror the metering capability's metrics so an over-limit
// denial can name the exact meter it read.
type Limit string

const (
	// LimitSUMBand is the ceiling of the plan's spend-under-management band, in the SUM quantity unit.
	LimitSUMBand Limit = "sum_band"
	// LimitSeats is the dashboard seat allowance.
	LimitSeats Limit = "seats"
	// LimitRetentionDays is the trace/metric retention allowance, in days.
	LimitRetentionDays Limit = "retention_days"
	// LimitEvalCompute is the cloud eval-compute allowance for the period.
	LimitEvalCompute Limit = "eval_compute"
)

// Limits is every metered allowance a plan may set.
var Limits = []Limit{LimitSUMBand, LimitSeats, LimitRetentionDays, LimitEvalCompute}

// PlanConfig is one resolved plan definition. Every field comes from the config store.
//
// Rank orders the named plans so a denial can name the CHEAPEST plan that lifts what was hit
// ("upgrade to Team"), rather than always pointing at the top tier. Ordering is config too: introducing
// a plan between two existing ones is a rank number, not a code change.
type PlanConfig struct {
	PlanID string `json:"plan_id"`
	// DisplayName is the customer-facing plan NAME (Free / Team / Business / Enterprise). Plans are
	// named everywhere in the product; amounts never appear outside the provider.
	DisplayName string `json:"display_name"`
	Rank        int    `json:"rank"`
	// Features is the set of surfaces this plan entitles.
	Features map[Feature]bool `json:"features"`
	// Limits is the plan's metered allowances. An absent limit means UNLIMITED for that meter — stated
	// here because "absent == 0 == everything is denied" would be a silent, catastrophic default.
	Limits map[Limit]float64 `json:"limits"`
	// PriceRefs maps a charge kind ("subscription", "metered", "gainshare") to an OPAQUE provider price
	// handle. It is a reference, never an amount: the platform cannot compute a price and therefore
	// cannot leak one.
	PriceRefs map[string]string `json:"price_refs"`
	// Version is the config-store version this definition was published under. An account pins it so a
	// past period can be re-explained against the plan definition that was actually in force.
	Version string `json:"version"`
}

// Entitles reports whether the plan's config lists f.
func (p PlanConfig) Entitles(f Feature) bool { return p.Features[f] }

// Limit returns the plan's allowance for l and whether one is set. An unset limit is UNLIMITED, not
// zero — see the Limits field comment.
func (p PlanConfig) Limit(l Limit) (float64, bool) {
	v, ok := p.Limits[l]
	return v, ok
}

// Snapshot is one published, immutable version of the whole plan catalog. A publish replaces the
// snapshot atomically, so no reader can ever observe a half-updated catalog.
type Snapshot struct {
	Version   string                `json:"version"`
	Plans     map[string]PlanConfig `json:"plans"`
	Published time.Time             `json:"published_at"`
}

// ErrUnknownPlan is returned for a plan_id absent from the live snapshot. It is an ERROR, never a
// silent fallback to a default plan: falling back would silently grant or deny entitlements nobody
// configured, which is the revenue-leak and mystery-breakage failure mode at once.
var ErrUnknownPlan = errors.New("plancfg: no such plan in the published configuration")

// ErrNoConfig is returned before any snapshot has been published. Fail closed and loud.
var ErrNoConfig = errors.New("plancfg: no plan configuration has been published")

// Source is the config store. It is deliberately a one-method interface so the store can be a mounted
// file, a KV service, or a control-plane API without any of this package changing.
type Source interface {
	// Load returns the currently published catalog. It MUST NOT be a git-tracked file.
	Load() (Snapshot, error)
	// Describe names the store for the readiness surface — the identity, never its contents.
	Describe() string
}

// PlanChangeEvent is the audit record emitted on every publish (task 1.3). It records WHICH plans moved
// between versions, never the values they moved to: the audit trail must be safe to ship to a log sink,
// and a price value in an audit row is a price value in a log.
type PlanChangeEvent struct {
	FromVersion string    `json:"from_version"`
	ToVersion   string    `json:"to_version"`
	Actor       string    `json:"actor"`
	Source      string    `json:"source"`
	Added       []string  `json:"added,omitempty"`
	Removed     []string  `json:"removed,omitempty"`
	Changed     []string  `json:"changed,omitempty"`
	TS          time.Time `json:"ts"`
}

// AuditSink records plan-change events. A publish whose audit append fails is REJECTED (see Reload):
// an unauditable packaging change is exactly the change nobody can explain at month-end.
type AuditSink interface {
	AppendPlanChange(PlanChangeEvent) error
}

// MemAudit is an in-memory append-only AuditSink for the default path and the tests.
type MemAudit struct {
	mu     sync.Mutex
	events []PlanChangeEvent
	down   bool
}

// NewMemAudit builds an empty audit log.
func NewMemAudit() *MemAudit { return &MemAudit{} }

// SetDown flips the sink between available and unavailable — the fail-closed test seam.
func (a *MemAudit) SetDown(down bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.down = down
}

// AppendPlanChange records ev, or fails if the sink is down.
func (a *MemAudit) AppendPlanChange(ev PlanChangeEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.down {
		return errors.New("plancfg: audit sink unavailable — publish refused")
	}
	a.events = append(a.events, ev)
	return nil
}

// Events returns a copy of the audit log in append order.
func (a *MemAudit) Events() []PlanChangeEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]PlanChangeEvent(nil), a.events...)
}

// Resolver holds the live snapshot and answers ResolvePlan. It is HOT-RELOADABLE: Reload swaps in a
// newly published catalog inside one running process — no restart, no deploy, no migration.
type Resolver struct {
	src   Source
	audit AuditSink
	now   func() time.Time

	mu   sync.RWMutex
	snap Snapshot
	// loaded distinguishes "published an empty catalog" from "never published", which fail differently.
	loaded bool
}

// NewResolver builds a resolver over src. It does NOT load: a caller decides when the first publish
// happens, and a constructor that silently swallows a load error would start the process in a state
// where every entitlement check fails for an unexplained reason.
func NewResolver(src Source, audit AuditSink) *Resolver {
	return &Resolver{src: src, audit: audit, now: time.Now}
}

// SetClock injects a deterministic clock (tests).
func (r *Resolver) SetClock(now func() time.Time) { r.now = now }

// Reload re-reads the config store and atomically swaps in the new catalog, emitting a plan_change
// audit event naming the plans that moved.
//
// Ordering is deliberate: AUDIT FIRST, then swap. If the audit sink is down the publish is refused and
// the previous catalog stays live — a packaging change that cannot be explained afterwards must not
// take effect (the same write-ahead-audit discipline P6 applies to a merge).
func (r *Resolver) Reload(actor string) (Snapshot, error) {
	next, err := r.src.Load()
	if err != nil {
		return Snapshot{}, fmt.Errorf("plancfg: load from config store: %w", err)
	}
	if next.Version == "" {
		return Snapshot{}, errors.New("plancfg: config store returned an unversioned catalog — a plan definition nothing can cite is not publishable")
	}

	r.mu.RLock()
	prev, hadPrev := r.snap, r.loaded
	r.mu.RUnlock()

	ev := diffCatalog(prev, next)
	ev.Actor = actor
	ev.Source = r.src.Describe()
	ev.TS = r.now().UTC()
	if !hadPrev {
		ev.FromVersion = ""
	}
	if r.audit != nil {
		if err := r.audit.AppendPlanChange(ev); err != nil {
			return Snapshot{}, fmt.Errorf("plancfg: publish refused, plan_change audit failed: %w", err)
		}
	}

	r.mu.Lock()
	r.snap, r.loaded = next, true
	r.mu.Unlock()
	return next, nil
}

// ResolvePlan returns the live definition of planID.
func (r *Resolver) ResolvePlan(planID string) (PlanConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.loaded {
		return PlanConfig{}, ErrNoConfig
	}
	p, ok := r.snap.Plans[planID]
	if !ok {
		return PlanConfig{}, fmt.Errorf("%w: %q", ErrUnknownPlan, planID)
	}
	p.Version = r.snap.Version
	return p, nil
}

// Version is the live catalog's config version — what an account pins.
func (r *Resolver) Version() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap.Version
}

// Plans returns every live plan in rank order — what the packaging UI renders and what the upgrade-path
// search walks.
func (r *Resolver) Plans() []PlanConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PlanConfig, 0, len(r.snap.Plans))
	for _, p := range r.snap.Plans {
		p.Version = r.snap.Version
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].PlanID < out[j].PlanID
	})
	return out
}

// Describe names the live config store for the readiness surface.
func (r *Resolver) Describe() string {
	if r.src == nil {
		return "none"
	}
	return r.src.Describe()
}

// diffCatalog names which plans were added, removed, or changed between two versions. It compares
// SHAPES, not values, so the audit row can name "team changed" without carrying what it changed to.
func diffCatalog(prev, next Snapshot) PlanChangeEvent {
	ev := PlanChangeEvent{FromVersion: prev.Version, ToVersion: next.Version}
	for id, np := range next.Plans {
		pp, ok := prev.Plans[id]
		switch {
		case !ok:
			ev.Added = append(ev.Added, id)
		case !samePlan(pp, np):
			ev.Changed = append(ev.Changed, id)
		}
	}
	for id := range prev.Plans {
		if _, ok := next.Plans[id]; !ok {
			ev.Removed = append(ev.Removed, id)
		}
	}
	sort.Strings(ev.Added)
	sort.Strings(ev.Removed)
	sort.Strings(ev.Changed)
	return ev
}

func samePlan(a, b PlanConfig) bool {
	if a.DisplayName != b.DisplayName || a.Rank != b.Rank {
		return false
	}
	if len(a.Features) != len(b.Features) || len(a.Limits) != len(b.Limits) || len(a.PriceRefs) != len(b.PriceRefs) {
		return false
	}
	for k, v := range a.Features {
		if b.Features[k] != v {
			return false
		}
	}
	for k, v := range a.Limits {
		if bv, ok := b.Limits[k]; !ok || bv != v {
			return false
		}
	}
	for k, v := range a.PriceRefs {
		if bv, ok := b.PriceRefs[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// NormalizePlanID trims and lowercases a plan id so "Team" and "team" cannot become two plans.
func NormalizePlanID(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
