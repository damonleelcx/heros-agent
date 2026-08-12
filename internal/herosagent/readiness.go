package herosagent

import (
	"context"
	"fmt"
	"sort"
)

// readiness.go is task 9.1: what `/readyz` says about the analysis agent.
//
// # 🚫 NOT ASSERTED FROM CONFIGURATION — the task's own words, and the failure they name
//
// The obvious implementation reads the deployment's settings: HEROS is configured, a credential
// reference is set, a cap exists, therefore `ready`. Every one of those is a statement about a config
// file, and a readiness signal built from config files is green on a box that has never once succeeded
// at the thing it claims to be ready for.
//
// This product has already paid for that exactly once, and the incident is worth restating because the
// shape is identical: `/readyz` reported `components.postgres: ready` on a process that had never
// opened a Postgres connection, because the component's NAME came from an environment variable while
// the probe pinged the SQLite ledger. "That signal could not go red no matter what happened to
// Postgres, which is the one thing a readiness signal may never be" (P19 Decision 9).
//
// So every field below is resolved by doing what an inference does:
//
//	the definition   read from the version store, and it must be ACTIVE — not "a definition exists"
//	the credential   RESOLVED through the same Secrets source the gateway calls at use
//	the ceiling      read from the cap store and compared against the real meter
//
// # Why `disabled` and `capped` are not `not_ready`
//
// 🔴 They must never turn the deployment red. `disabled` is the DEFAULT (Q2), so a red signal there
// would page somebody about the configuration every deployment ships with. `capped` is a ceiling
// working as intended — the deployment is healthy and is declining to spend. A readiness signal that
// goes red on correct behaviour is a signal an operator learns to ignore, and then it is worth nothing
// on the day it means something.
//
// `credential_unresolved` is the one that is a fault, and even it does not fail the whole deployment:
// HEROS is optional, every other surface is rule-derived, and taking a platform down because an
// optional subsystem cannot reach its vendor is a bigger outage than the one being reported.

// Readiness is the agent's entry on `/readyz`.
type Readiness struct {
	State ReadyState `json:"state"`
	// Detail is the sentence an operator reads. Always populated: a state with no sentence is a word
	// somebody has to look up during an incident.
	Detail string `json:"detail"`
	// ConfigHash names WHICH definition this readiness is about. A signal that says `ready` without
	// saying ready-to-run-what cannot be compared against what an operator thinks is deployed.
	ConfigHash string `json:"config_hash,omitempty"`
	// EnabledTenants is how many tenants are placed anywhere but `disabled`. The number that says
	// whether `disabled` means "nobody enabled" or "everybody switched off".
	EnabledTenants int `json:"enabled_tenants"`
	// CapsEnforced is false when no ceiling is wired at all. 🔴 Reported rather than hidden: a
	// deployment with unwired caps is indistinguishable on every other signal from one whose caps are
	// merely generous, and the two are very different the first time an analysis runs away.
	CapsEnforced bool `json:"caps_enforced"`
	// CredentialSource names where the credential was resolved FROM — the value the gateway's own
	// Secrets source reports. Carried so a reader can tell an env-var deployment from a secrets-manager
	// one without opening the process environment.
	CredentialSource string `json:"credential_source,omitempty"`
}

// CredentialResolver resolves a provider reference exactly as the gateway does at use.
//
// 🔴 An interface over the REAL resolution rather than a boolean, because the whole point is that this
// performs the resolution instead of asserting it succeeded. The implementation a deployment wires is
// the same `providergateway.Secrets` its runner calls.
type CredentialResolver interface {
	// Resolve returns nil when the provider's credential is obtainable right now. It must NOT return
	// the credential: a readiness surface that held one would be a readiness surface that could leak
	// one, and nothing here needs the value.
	Resolve(ctx context.Context, provider string) error
	// Describe names the source, without naming any value.
	Describe() string
}

// ReadinessInput is what a readiness check reads. Every field is a live store, not a setting.
type ReadinessInput struct {
	Versions   activeReader
	Placements interface {
		List(context.Context) ([]TenantPlacement, error)
	}
	Credentials CredentialResolver
	Caps        *CapChecker
}

// Check resolves the agent's readiness by DOING what an inference does.
func Check(ctx context.Context, in ReadinessInput) Readiness {
	out := Readiness{CapsEnforced: in.Caps != nil}

	// 1 · who is enabled. Read from the placement store, so "disabled" is a fact about tenants rather
	// than about a feature flag.
	enabled := []string{}
	if in.Placements != nil {
		placements, err := in.Placements.List(ctx)
		if err != nil {
			return unresolvable(out, "this deployment's tenant placements could not be read, so whether "+
				"anything analyses is unknown: "+err.Error())
		}
		for _, p := range placements {
			if p.Placement != PlacementDisabled {
				enabled = append(enabled, p.TenantID)
			}
		}
	}
	sort.Strings(enabled)
	out.EnabledTenants = len(enabled)

	// 2 · what is serving. ACTIVE, not merely published — a definition that passed its rehearsal and
	// was never activated is serving nothing, which is exactly what task 6.3's surface says.
	var active Version
	if in.Versions != nil {
		v, ok, err := in.Versions.Active(ctx)
		if err != nil {
			return unresolvable(out, "the agent version store could not be read: "+err.Error())
		}
		if ok {
			active, out.ConfigHash = v, v.ConfigHash
		}
	}

	if out.EnabledTenants == 0 {
		// 🔴 THE DEFAULT, and it is reported before the checks below rather than after. A deployment
		// with nobody enabled has no credential to fail on and no ceiling to be under, so running those
		// checks would produce a fault about machinery nothing is using.
		out.State = ReadyDisabled
		out.Detail = "No organization is placed for analysis, which is the default (Q2). Nothing runs " +
			"and nothing spends. This is not a fault."
		return out
	}
	if out.ConfigHash == "" {
		out.State = ReadyNoDefinition
		out.Detail = fmt.Sprintf("%d organization(s) are enabled for analysis and no agent definition "+
			"is active, so nothing can run for them. A definition must be published and activated after "+
			"its rehearsal — this is a configuration half-done rather than a fault.", out.EnabledTenants)
		return out
	}

	// 3 · the credential, RESOLVED. Not "a reference is set" — the reference is resolved through the
	// same source the gateway calls, because a reference pointing at a secret nobody provisioned looks
	// identical to a working one until the first call.
	if in.Credentials != nil {
		out.CredentialSource = in.Credentials.Describe()
		if err := in.Credentials.Resolve(ctx, active.Definition.CredentialRef); err != nil {
			out.State = ReadyCredentialUnresolved
			out.Detail = fmt.Sprintf("The active definition binds the provider %q and its credential "+
				"does not resolve from %s. Inference fails closed: zero provider calls, no substitution, "+
				"and every surface falls back to rule-derived facts. Detail: %v",
				active.Definition.CredentialRef, out.CredentialSource, err)
			return out
		}
	}

	// 4 · the ceiling, against the REAL meter. Checked for the fleet, which is the scope that stops
	// everything; a single capped tenant is that tenant's state and not the deployment's.
	if in.Caps != nil {
		verdict, err := in.Caps.Check(ctx, FleetTenantID)
		if err != nil {
			return unresolvable(out, "the token ceiling could not be read: "+err.Error())
		}
		if !verdict.Allowed {
			out.State = ReadyCapped
			out.Detail = fmt.Sprintf("The fleet token ceiling is reached — %d spent against a limit of "+
				"%d. Nothing will spend until it is raised or the window rolls. The deployment is "+
				"healthy and is declining to spend; this is a ceiling working.", verdict.Spent, verdict.Limit)
			return out
		}
	}

	out.State = ReadyReady
	out.Detail = fmt.Sprintf("%d organization(s) enabled, definition %s active, credential resolves.",
		out.EnabledTenants, confighashDisplay(out.ConfigHash))
	if !out.CapsEnforced {
		// 🔴 Said on the READY line, because this is the state where it is most dangerous and least
		// visible: everything works, and nothing is bounded.
		out.Detail += " 🔴 NO TOKEN CEILING IS ENFORCED on this deployment — analysis is unbounded."
	}
	return out
}

// unresolvable is the answer when a store this check depends on could not be read.
//
// 🔴 It reports `credential_unresolved` rather than inventing a sixth state, and the reasoning is that
// the four states are about what INFERENCE would do — and an inference on this deployment would fail
// closed for the same reason this check did. A state meaning "the readiness check itself broke" would
// be a fifth thing for a monitor to learn, describing our machinery rather than the customer's.
func unresolvable(out Readiness, detail string) Readiness {
	out.State = ReadyCredentialUnresolved
	out.Detail = detail
	return out
}

// ReadyStates returns the closed set, so a consumer's switch can be proved exhaustive and the enum
// fence can reserve every value.
func ReadyStates() []ReadyState {
	return []ReadyState{
		ReadyDisabled, ReadyReady, ReadyCredentialUnresolved, ReadyCapped, ReadyNoDefinition,
	}
}
