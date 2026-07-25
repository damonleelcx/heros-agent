package cli

import (
	"fmt"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// version.go declares the platform-contract version this CLI implements and the refuse-on-mismatch
// behavior (PRD FR6, task 2.7). A CLI too old for the platform names the required version and refuses to
// produce results under mismatched semantics — it never silently computes something different.

// ToolVersion is the CLI's own version. Stamped into linked runs (allowlist run_metadata.tool_version)
// and reported by `status`. Overridden at build time via -ldflags for a real release (task 6.x).
var ToolVersion = "0.11.0-dev"

// ContractVersion is the platform contract this CLI speaks. It is the run-linking payload contract
// (runlink.ContractVersion) — one version string, one source of truth, so the CLI and the payload
// cannot disagree about what "the contract" is.
const ContractVersion = runlink.ContractVersion

// CheckContract compares this CLI's contract version against what the platform requires. A platform
// that requires a different contract version gets a loud, named refusal rather than a silent
// reinterpretation. Equality is the M14 rule (a single supported version); the support WINDOW policy is
// documented in docs/decisions/p11-contracts.md and widens this to a range without changing the shape.
func CheckContract(platformRequired string) error {
	if platformRequired == "" {
		// The platform did not state a required version. That is an operational ambiguity, not a
		// mismatch — surface it rather than assuming compatibility.
		return operational("platform did not declare a contract version", nil)
	}
	if platformRequired != ContractVersion {
		return &ExitError{
			Code: ExitOperational,
			Msg: fmt.Sprintf(
				"contract mismatch: this CLI speaks %q but the platform requires %q — upgrade the CLI to the required version; refusing to produce results under mismatched semantics",
				ContractVersion, platformRequired),
		}
	}
	return nil
}
