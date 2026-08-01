package erroreport

import (
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// deployment.go turns the environment into a `Config` — and, on every substrate but one, into `absent`.
//
// # 🔴 The default is ABSENT, and absence is silent
//
// Three substrates run the same binaries: the platform's own hosted deployment, a customer's
// Compose/Kubernetes install, and an air-gapped network. Error reporting is configured for the FIRST
// ONE ONLY. On the other two the correct state is `absent`, and `absent` emits no warning, no log line
// and no readiness noise — a deployment that was never meant to report is not degraded, and a warning
// that fires on every correct install teaches an operator to ignore warnings.
//
// The rejected alternative is worth naming because it is the obvious one: ship the ids everywhere and
// rely on the DSN being unset. That leaves the code path present and the identity one Helm value away
// from being set — silent, with a customer's network as the blast radius, and they find out before we
// do. Here the DSN is the only switch and there is no discovered default: `FromEnv` reads one variable
// and returns the absent reporter when it is empty.

// EnvDSN is the ONE variable that turns reporting on. There is no fallback, no file, no discovered
// default, and no per-substrate default that could be inherited by accident.
const EnvDSN = "HEROS_ERROR_REPORTING_DSN"

// EnvEdition names the deployment shape. Closed set — an unrecognised value is reported as `unknown`
// with a WARN rather than transmitted, because an edition label is a dimension an operator groups by
// and a free-text one makes the grouping useless.
const EnvEdition = "HEROS_EDITION"

// EnvRelease is the build identifier. `HEROS_VERSION` already carries it elsewhere in the platform, so
// this reads the same variable rather than introducing a second spelling of the same fact.
const EnvRelease = "HEROS_VERSION"

// Editions is the closed set of deployment shapes.
//
// A label, never a customer name and never a hostname: "which shape of install was this" is a property
// of the deployment topology, and the moment it could carry a customer's identity it would be doing a
// different job than the one it is allowlisted for.
var Editions = []string{"hosted", "compose", "kubernetes", "airgapped", "dev"}

func validEdition(v string) bool {
	for _, e := range Editions {
		if e == v {
			return true
		}
	}
	return false
}

// FromEnv builds a reporter from the process environment.
//
// Returns the ABSENT reporter, with no error and no log line, when `HEROS_ERROR_REPORTING_DSN` is
// empty. Returns an error when the variable is set but unusable — that is a deployment somebody meant
// to configure and got wrong, and failing at boot is the loudest and cheapest place to say so.
func FromEnv(logf func(format string, args ...any)) (Reporter, error) {
	if logf == nil {
		logf = log.Printf
	}
	dsn := strings.TrimSpace(os.Getenv(EnvDSN))
	if dsn == "" {
		return Absent(), nil
	}

	edition := strings.TrimSpace(os.Getenv(EnvEdition))
	if edition == "" {
		edition = "unknown"
	} else if !validEdition(edition) {
		// A silent fall-back to a default is the failure class this repository has a rule about: the
		// path that falls back must say so, or a mislabelled fleet looks like a correctly labelled one.
		logf("WARN error_reporting.edition.unrecognised value=%q known=%v — reporting as \"unknown\"",
			edition, Editions)
		edition = "unknown"
	}

	release := strings.TrimSpace(os.Getenv(EnvRelease))
	if release == "" {
		release = "unknown"
	}

	return New(Config{
		DSN:      dsn,
		Release:  release,
		Edition:  edition,
		Runtime:  "go " + runtime.Version(),
		Scrubber: telemetry.NewScrubber(),
		Logf:     logf,
	})
}
