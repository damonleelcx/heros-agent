package forgedelivery

import "fmt"

// surfacedefault.go is program ruling **R3** and P35 design D3: the delivery default is **per surface**.
//
// # What ADR-005 decided, and what R3 changes
//
// [ADR-005](../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md) made the customer's own CI
// the default so that no write-scoped forge credential is held by the platform, because pushing to a
// customer's repository is the highest-blast-radius action in the system. That argument is not wrong.
// It is an argument about **what to do when the platform does not know how the customer works.**
//
// There are now two surfaces and the platform knows different things about each:
//
//	CLI, CI    it genuinely does not know. The customer may have any CI, any token policy, any review
//	           process. CI-mediated stays the default and the platform receives no forge credential.
//	console    it does know. They arrived with no CI integration and no CLI, and the CI-mediated path
//	           requires both. Defaulting to a mode the customer cannot use is a default that means
//	           "this feature is off".
//
// # 🔴 The default changes the MODE, not the SCOPE
//
// This is the sentence design D3 says the phase is most exposed to losing. The installation stays
// per-repository, least-privilege and customer-revocable; nothing here requests a broader grant because
// a mode became a default. `Installation.Validate` already refuses an installation that selected no
// repositories, and there is deliberately still no way to express "all repositories".
//
// The failure this file is written against is the default quietly widening into *"the platform has
// write access to your account"*, which is a different product.

// Surface is where a delivery request originated. A closed set, because the delivery MODE is decided
// from it and a value nobody enumerated would have to fall back to something.
//
// 🔴 It is resolved from the TRANSPORT, never from a request body. A surface a caller could set is a
// surface a caller sets to `console` to obtain the write-credential path — see
// `internal/api.originFor`, which is the one place it is decided.
type Surface string

const (
	// SurfaceConsole — a person acting in the console. Hosted Git App by default (R3).
	SurfaceConsole Surface = "console"
	// SurfaceCLI — `heros` on the customer's machine. CI-mediated; the platform receives no credential.
	SurfaceCLI Surface = "cli"
	// SurfaceCI — a CI job. CI-mediated.
	SurfaceCI Surface = "ci"
)

var surfaces = []Surface{SurfaceConsole, SurfaceCLI, SurfaceCI}

// Surfaces returns the closed set. A copy.
func Surfaces() []Surface { return append([]Surface(nil), surfaces...) }

// Valid reports membership.
func (s Surface) Valid() bool {
	for _, v := range surfaces {
		if v == s {
			return true
		}
	}
	return false
}

// String makes Surface printable.
func (s Surface) String() string { return string(s) }

// DefaultModeFor is R3, as a function rather than as a rule somebody remembers.
//
// 🔴 An unknown surface returns `ModeCI` and an error. The MODE is the safe one — CI-mediated needs no
// platform credential — and the error is returned as well because silently treating an unrecognised
// surface as a CLI call would hide a bug in whichever transport failed to classify itself. The safe
// fallback and the loud complaint are not alternatives.
func DefaultModeFor(s Surface) (Mode, error) {
	switch s {
	case SurfaceConsole:
		return ModeApp, nil
	case SurfaceCLI, SurfaceCI:
		return ModeCI, nil
	default:
		return ModeCI, fmt.Errorf("forgedelivery: %q is not a delivery surface (%s); defaulting to the "+
			"credential-free mode, which is safe, but a transport that cannot name its own surface is a "+
			"defect", s, joinSurfaces())
	}
}

// HoldsPlatformCredential reports whether this surface's default mode makes the platform hold a
// write-scoped forge credential.
//
// It exists so the claim "the CLI path gives the platform no forge credential" is a value a fence can
// read rather than a sentence in a document. `TestOnlyTheConsoleSurfaceReachesForACredential` asserts
// it across the whole closed set, so a fourth surface added later must decide this explicitly.
func (s Surface) HoldsPlatformCredential() bool { return s == SurfaceConsole }

func joinSurfaces() string {
	out := ""
	for i, v := range surfaces {
		if i > 0 {
			out += ", "
		}
		out += string(v)
	}
	return out
}
