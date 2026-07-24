package billing

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// rollout.go is task 8.3: the billing feature flag that keeps P7 DARK until each wave's checklist is
// green, and keeps the waves ordered.
//
// ## Three gates, and why each is separate
//
// A single "billing on/off" switch would be the wrong shape, because the three things that must be
// true before money moves become true at different times and for different reasons:
//
//	MODE          test | live. 7a ships in provider TEST MODE and only becomes live when the M10-7a
//	              checklist is green. Nothing about the code changes; the provider does.
//	GAINSHARE     7b is a separate switch that stays off until the P5.5 verified-delta ledger is live
//	              AND the estimated-saving-bills-nothing test passes. Shipping it early would be the
//	              exact "beat the dependency with an estimated-savings shortcut" the proposal forbids.
//	AUTO-MERGE    the Enterprise auto-merge ENTITLEMENT stays off until the gate itself is verified.
//	              The gate being correct and the gate being trusted with the highest-authority actor in
//	              the system are two different claims.
//
// ## Fail closed, and loudly
//
// The zero value is fully DARK: test mode, no gainshare, no auto-merge entitlement. A deployment that
// forgets to configure the flag does not start charging real money — it starts in the state where the
// worst outcome is a charge in a provider test account. And every refusal names WHICH gate stopped it,
// because "billing is disabled" is not something a support engineer can act on.

// Mode is the billing provider mode.
type Mode string

const (
	// ModeTest is the provider's test mode. Charges are recorded against a test account and move no
	// real money. This is the DEFAULT.
	ModeTest Mode = "test"
	// ModeLive moves real money. Reachable only by explicit configuration.
	ModeLive Mode = "live"
)

// Rollout errors, one per gate, so a refusal names what to flip.
var (
	ErrBillingDark       = errors.New("billing: the billing feature flag is off — no charge may be raised")
	ErrGainshareDisabled = errors.New("billing: gainshare (wave 7b) is disabled — it stays off until the P5.5 verified-delta ledger is live and the estimated-saving-bills-nothing test passes")
	ErrAutoMergeDisabled = errors.New("billing: the Enterprise auto-merge entitlement is disabled until the entitlement gate is verified")
)

// Rollout is the P7 feature flag. Its zero value is fully dark.
type Rollout struct {
	mu sync.RWMutex
	// enabled turns the billing capability on at all. Off means the surface reads (a customer can see
	// their usage) but nothing charge-bearing runs.
	enabled bool
	mode    Mode
	// gainshare gates wave 7b independently of 7a.
	gainshare bool
	// autoMergeEntitlement gates whether the Enterprise auto-merge entitlement may be granted at all,
	// regardless of what the plan config says.
	autoMergeEntitlement bool
}

// NewRollout builds a fully dark rollout: billing off, provider test mode, gainshare off, auto-merge
// entitlement off.
func NewRollout() *Rollout { return &Rollout{mode: ModeTest} }

// Enable turns billing on in the given mode. ModeLive is deliberately explicit — there is no way to
// reach it by omission.
func (r *Rollout) Enable(mode Mode) error {
	switch mode {
	case ModeTest, ModeLive:
	default:
		return fmt.Errorf("billing: unknown provider mode %q (test|live)", mode)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled, r.mode = true, mode
	return nil
}

// Disable turns billing off. It does NOT reset the sub-gates: turning billing off during an incident
// and back on should not silently re-enable gainshare that somebody deliberately switched off.
func (r *Rollout) Disable() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = false
}

// EnableGainshare turns wave 7b on. It REFUSES while billing itself is dark: gainshare on top of
// disabled billing is a configuration that cannot do anything and would read as enabled.
func (r *Rollout) EnableGainshare() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return fmt.Errorf("billing: cannot enable gainshare while billing is off (%w)", ErrBillingDark)
	}
	r.gainshare = true
	return nil
}

// DisableGainshare turns wave 7b off.
func (r *Rollout) DisableGainshare() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gainshare = false
}

// EnableAutoMergeEntitlement allows the Enterprise auto-merge entitlement to be granted.
func (r *Rollout) EnableAutoMergeEntitlement() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoMergeEntitlement = true
}

// DisableAutoMergeEntitlement withdraws it. Immediate: the P6 loop consults the gate before every
// merge, so flipping this stops auto-merge at the next merge decision rather than at the next deploy.
func (r *Rollout) DisableAutoMergeEntitlement() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoMergeEntitlement = false
}

// AllowCharge reports whether a charge of this kind may be raised, naming the gate that stopped it.
func (r *Rollout) AllowCharge(kind ChargeKind) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.enabled {
		return ErrBillingDark
	}
	if kind == KindGainshare && !r.gainshare {
		return ErrGainshareDisabled
	}
	return nil
}

// AllowAutoMergeEntitlement reports whether the Enterprise auto-merge entitlement may be granted.
func (r *Rollout) AllowAutoMergeEntitlement() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.autoMergeEntitlement
}

// Mode is the live provider mode.
func (r *Rollout) Mode() Mode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mode
}

// Describe reports the rollout state for the readiness surface — the health signal
// (health-signal-surface: "which gates are open" must be checkable NOW, on the box that is
// misbehaving, not inferred from a log line three restarts ago).
func (r *Rollout) Describe() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return map[string]string{
		"billing":                string(boolWord(r.enabled)),
		"provider_mode":          string(r.mode),
		"gainshare":              string(boolWord(r.gainshare)),
		"auto_merge_entitlement": string(boolWord(r.autoMergeEntitlement)),
	}
}

type word string

func boolWord(b bool) word {
	if b {
		return "enabled"
	}
	return "disabled"
}

// String renders the rollout for a startup log line — greppable, and short enough to read.
func (r *Rollout) String() string {
	d := r.Describe()
	parts := make([]string, 0, len(d))
	for _, k := range []string{"billing", "provider_mode", "gainshare", "auto_merge_entitlement"} {
		parts = append(parts, k+"="+d[k])
	}
	return strings.Join(parts, " ")
}
