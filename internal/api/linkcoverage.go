package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
)

// linkcoverage.go lifts LINK COVERAGE out of the billing view (P29 §7.2), and provisions an account at
// the first authenticated act (§7.1).
//
// # Two separate defects wearing one symptom
//
// A developer linked a run and opened `/app/billing`. It was absent. Two independent things were wrong:
//
//  1. **No account existed.** `ensureSeededAccounts` is create-if-absent over the organizations the
//     CONFIG SEED made, gated on a plan catalog. An organization created any other way — self-serve
//     sign-up, or simply created after boot — has none, and a linked run is then attributed to a
//     customer the billing read model cannot find.
//  2. **Coverage was only readable INSIDE the billing view.** `linkCoverageFor` already returns three
//     states correctly and is only ever called from `BillingView`. So when the account was missing, the
//     one number a link certainly produced became unreadable — with no plan, no invoice and no money
//     involved anywhere in its computation.
//
// # 🔴 `nil` means UNKNOWN, permanently, and renders distinctly from complete
//
// This is the property the whole surface rests on. A spend figure at 100% coverage and one whose
// denominator could not be read look identical as a number and mean opposite things: one is "this is
// what you spent", the other is "this is what we happened to see". Collapsing unknown into complete is
// the single most expensive mistake available on a metering surface, and it is one `if err != nil`
// away at all times.

// AccountProvisioner creates a Free account for an organization that has none.
//
// 🔴 CREATE-IF-ABSENT, and it must NEVER correct an existing account. The comment in
// `accountsystem.go` is right and stays right: a provisioner that "reconciled" an existing account
// would move a paying customer back to Free — on the next restart, or on their next link, silently,
// with the correction looking exactly like the intended behaviour.
type AccountProvisioner interface {
	// EnsureAccount creates the organization's account if it has none. It returns nil when one already
	// exists, whatever plan that account is on.
	EnsureAccount(tenantID string) error
}

// MountLinkCoverage registers the coverage read and installs the provisioner. Call after New.
func (s *Server) MountLinkCoverage(p AccountProvisioner) {
	s.accountProvisioner = p
	s.Mux.HandleFunc("GET /api/v1/link-coverage", s.handleLinkCoverage)
}

// provisionAccount is called on an authenticated act. It is deliberately best-effort: a failure to
// create an account must never fail the act that triggered it.
//
// A link that succeeded and then 500'd because billing could not be set up would be the worst available
// outcome — the run IS linked, the customer's CI would report a failure, and re-running would report
// "already linked". So the failure is logged by the provisioner and the act proceeds; the coverage read
// below then answers `no-account`, which is a state the screen already renders.
func (s *Server) provisionAccount(tenantID string) {
	if s.accountProvisioner == nil || tenantID == "" {
		return
	}
	_ = s.accountProvisioner.EnsureAccount(tenantID)
}

// LinkCoverageResponse is the three-state coverage, outside BillingView.
type LinkCoverageResponse struct {
	// Known is false when coverage is UNKNOWN. When it is false, RunsLinked and RunsReported are
	// meaningless and the console must not render them as zero.
	Known bool `json:"known"`
	// Complete is true when every run the CLI observed was linked.
	Complete     bool `json:"complete"`
	RunsLinked   int  `json:"runs_linked"`
	RunsReported int  `json:"runs_reported"`
	// State is the machine half: `complete`, `partial`, `unknown` or `not-mounted`. The console branches
	// on this rather than on the booleans, so a new state cannot be silently absorbed into an old
	// treatment.
	State string `json:"state"`
}

func (s *Server) handleLinkCoverage(w http.ResponseWriter, r *http.Request) {
	if s.runLinking == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"state": StateNotMounted,
			"error": "this deployment does not accept run links, so there is no coverage to report",
		})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "reading link coverage requires an authenticated tenant"})
		return
	}
	// The first authenticated act provisions the account, so a customer who has linked a run has a
	// billing surface to look at rather than an absent one.
	s.provisionAccount(principal.TenantID)

	c, err := s.runLinking.Coverage(principal.TenantID)
	if err != nil {
		// 🔴 UNKNOWN, and 200. A read failure is not zero coverage and it is not an error the caller can
		// act on — the runs are linked either way. What the caller must not do is render a spend figure
		// as complete, and `known: false` is what stops them.
		writeJSON(w, http.StatusOK, LinkCoverageResponse{Known: false, State: "unknown"})
		return
	}
	state := "partial"
	switch {
	case !c.Known:
		state = "unknown"
	case c.Complete:
		state = "complete"
	}
	writeJSON(w, http.StatusOK, LinkCoverageResponse{
		Known: c.Known, Complete: c.Complete,
		RunsLinked: c.RunsLinked, RunsReported: c.RunsReported,
		State: state,
	})
}
