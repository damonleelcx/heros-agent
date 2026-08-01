package adminops

import (
	"fmt"
	"sync"
)

// rollout.go is P8's rollout gate (task 13.4): 8a behind an admin feature flag, dark until the M11-8a
// checklist is green, and 8b enabled only after the P6 kill switch and P2.5 aggregates are live.
//
// # Why the rollout state is a HEALTH SIGNAL, not a boot log line
//
// "Is the operator console live, and is it in test mode" is a question an operator asks about the box
// that is misbehaving, NOW — and a log line that scrolled past three restarts ago cannot answer it.
// So the rollout describes itself on the readiness surface, like P7's billing rollout does, and the
// admin API refuses to serve a capability whose wave is not enabled.
//
// # Why 8b is gated on its dependencies rather than on a date
//
// 8b's kill switch is only real if the P6 kill switch it wires to is live, and its cross-tenant views
// are only real if the P2.5 aggregates they read exist. Gating 8b on those being present makes "ship
// 8b before its dependencies" a refused configuration rather than a runtime surprise.

// Wave names one rollout wave.
type Wave string

const (
	// Wave8a is admin RBAC + tenant/billing/entitlement admin + audit log.
	Wave8a Wave = "8a"
	// Wave8b is fleet ops + global autonomous controls + cross-tenant observability + compliance.
	Wave8b Wave = "8b"
)

// Rollout is the P8 rollout state.
type Rollout struct {
	mu sync.RWMutex
	// enabled is the admin feature flag. Dark (false) until the M11-8a checklist is green; nothing is
	// served while dark.
	enabled bool
	// testModeIdP records that the admin IdP is in test mode (rollout 8a). It is descriptive — the IdP
	// itself is the source of truth — and lets /readyz warn if a production box is still in test mode.
	testModeIdP bool
	// wave8b is enabled only after its dependencies are live.
	wave8b bool
	// killSwitchLive and aggregatesLive are 8b's preconditions.
	killSwitchLive bool
	aggregatesLive bool
	// checklistGreen records that the M11-8a exit checklist passed.
	checklistGreen bool
}

// NewRollout builds a DARK rollout: nothing is served until it is explicitly enabled, which is the
// safe default for the platform's highest-blast-radius surface.
func NewRollout() *Rollout { return &Rollout{} }

// EnableWave8a turns on 8a. It refuses unless the M11-8a checklist is green, so "ship 8a before the
// checklist" is a refused call rather than a dark launch that quietly went live.
func (r *Rollout) EnableWave8a(testModeIdP bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.checklistGreen {
		return fmt.Errorf("adminops: 8a cannot be enabled until the M11-8a checklist is green")
	}
	r.enabled, r.testModeIdP = true, testModeIdP
	return nil
}

// MarkChecklistGreen records that the M11-8a exit checklist passed.
func (r *Rollout) MarkChecklistGreen() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checklistGreen = true
}

// MarkKillSwitchLive / MarkAggregatesLive record 8b's preconditions.
func (r *Rollout) MarkKillSwitchLive() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.killSwitchLive = true
}
func (r *Rollout) MarkAggregatesLive() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aggregatesLive = true
}

// EnableWave8b turns on 8b. It refuses unless 8a is live AND the P6 kill switch and P2.5 aggregates
// are both live (task 13.4).
func (r *Rollout) EnableWave8b() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return fmt.Errorf("adminops: 8b cannot be enabled before 8a")
	}
	if !r.killSwitchLive {
		return fmt.Errorf("adminops: 8b cannot be enabled until the P6 kill switch is live — its global kill switch wires to it")
	}
	if !r.aggregatesLive {
		return fmt.Errorf("adminops: 8b cannot be enabled until the P2.5 aggregates are live — its cross-tenant views read them")
	}
	r.wave8b = true
	return nil
}

// WaveEnabled reports whether a wave is serving.
func (r *Rollout) WaveEnabled(w Wave) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	switch w {
	case Wave8a:
		return r.enabled
	case Wave8b:
		return r.enabled && r.wave8b
	}
	return false
}

// Describe reports the rollout state for the readiness surface — words, never a secret.
func (r *Rollout) Describe() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return map[string]string{
		"wave_8a":             boolWord(r.enabled),
		"wave_8b":             boolWord(r.enabled && r.wave8b),
		"admin_idp_test_mode": boolWord(r.testModeIdP),
		"m11_8a_checklist":    boolWord(r.checklistGreen),
		"p6_kill_switch_live": boolWord(r.killSwitchLive),
		"p25_aggregates_live": boolWord(r.aggregatesLive),
	}
}

func boolWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// CapabilityWave maps each capability to the wave it ships in, so the rollout gate can refuse a
// capability whose wave is dark. Table-driven, so a capability added later gets a considered wave
// rather than a silent default.
var CapabilityWave = map[string]Wave{
	// 8a — RBAC + tenant/billing/entitlement admin + audit
	"tenant.read": Wave8a, "job.read": Wave8a, "impersonate.read": Wave8a,
	"billing.read": Wave8a, "billing.correct": Wave8a, "entitlement.override": Wave8a,
	"tenant.suspend": Wave8a, "tenant.quota": Wave8a, "role.grant": Wave8a,
	"audit.read": Wave8a, "impersonate.elevate": Wave8a,
	// 8b — fleet ops + autonomous controls + cross-tenant + compliance
	"job.retry": Wave8b, "job.cancel": Wave8b, "registry.admin": Wave8b,
	"killswitch.operate": Wave8b, "crosstenant.read": Wave8b, "gdpr.execute": Wave8b,
	// P26's three oversight surfaces. 8b rather than 8a, and the choice is conservative on purpose:
	// all three are FLEET-WIDE reads, which is what 8b gates, and 8b is the wave that requires the
	// cross-tenant aggregates to be live before anything reads across tenants. Putting them in 8a
	// would light three cross-tenant surfaces in the wave whose checklist says nothing about them.
	"delivery.read": Wave8b, "release.read": Wave8b, "axis.read": Wave8b,
}
