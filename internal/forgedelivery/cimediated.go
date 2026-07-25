package forgedelivery

import (
	"context"
	"errors"
	"fmt"
)

// cimediated.go is the CI-mediated delivery STEP — the code that runs inside P11's CI integration hook,
// in the customer's own CI runner (tasks 5.1, 5.3, 5.4). Its shape is the whole security property:
//
//	fetch(authenticated to the platform)  →  open PR (with the CI's OWN token)  →  report(to the platform)
//
// The platform serves a Prepared (a diff + rendered content, NO credential) and records the outcome.
// The pull request is opened by the CI environment using the ephemeral, repository-scoped token it
// already holds — the credential the platform never receives, stores, or requests.

// Fetcher retrieves the verified, deliverable proposals for a target. The implementation authenticates
// to the platform and the platform scopes the answer server-side to the caller's tenant and repository
// (task 5.3) — the fetcher passes no tenant it could forge. It returns Prepared values with no
// credential in them.
type Fetcher interface {
	// Pending returns the proposals the platform has cleared for delivery to this target. An empty slice
	// means "nothing to deliver", which the caller must distinguish from an error (a silent stop is
	// forbidden — task 5.4).
	Pending(ctx context.Context, target Target) ([]Prepared, error)
}

// Report is what the CI runner tells the platform after opening a pull request, so the platform can
// append to the append-only delivery record. It carries no credential.
type Report struct {
	DeliveryID  string `json:"delivery_id"`
	ForgeRef    string `json:"forge_ref"`
	ForgeURL    string `json:"forge_url,omitempty"`
	Created     bool   `json:"created"`
	Merged      bool   `json:"merged,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
}

// Reporter records a CI-opened delivery on the platform.
type Reporter interface {
	Report(ctx context.Context, r Report) error
}

// ErrCICredentialExpired is what a forge writer returns when the CI's own token is expired or rotated
// away. CIStep surfaces it as a DEGRADED outcome and never as a silent stop (task 5.4) — a silent stop
// reads to the customer as "no suggestions this week", which is the failure this exists to prevent.
var ErrCICredentialExpired = errors.New("forgedelivery: the CI forge credential is expired or rotated away")

// CIOutcome is one proposal's result within a CI delivery step.
type CIOutcome struct {
	DeliveryID string
	PR         PullRequest
	Created    bool
	// Degraded is set when this proposal could not be delivered because the CI credential is
	// expired/rotated. It is REPORTED, never swallowed.
	Degraded bool
	Err      error
}

// CIReport is the whole step's result, including whether the credential is degraded.
type CIReport struct {
	Target    Target
	Outcomes  []CIOutcome
	Delivered int
	// CredentialDegraded is true if any proposal hit an expired/rotated CI credential. The caller (the
	// CI action) exits with a distinct, non-silent status so the condition surfaces.
	CredentialDegraded bool
}

// CIStep runs the CI-mediated delivery step through P11's hook: it fetches the platform-cleared
// proposals, opens each pull request with the CI runner's own forge writer, and reports each back. It
// never holds a platform credential; the forge writer's credential is the CI environment's.
//
// A degraded CI credential is reported (task 5.4): the step marks the outcome degraded, reports what it
// can, and returns CredentialDegraded set — the caller must not read "nothing delivered" as "nothing
// to deliver".
func CIStep(ctx context.Context, fetch Fetcher, forge ForgeWriter, report Reporter, target Target, bound int) (CIReport, error) {
	if bound <= 0 {
		bound = DefaultOpenPRBound
	}
	prepared, err := fetch.Pending(ctx, target)
	if err != nil {
		// A fetch failure is a fault, not "nothing to deliver" — returned so the CI action fails loudly.
		return CIReport{Target: target}, fmt.Errorf("forgedelivery: fetching pending deliveries: %w", err)
	}
	out := CIReport{Target: target, Outcomes: make([]CIOutcome, 0, len(prepared))}
	for _, prep := range prepared {
		pr, created, err := OpenFromPrepared(ctx, forge, prep, bound)
		if err != nil {
			if errors.Is(err, ErrCICredentialExpired) {
				out.CredentialDegraded = true
				out.Outcomes = append(out.Outcomes, CIOutcome{DeliveryID: prep.DeliveryID, Degraded: true, Err: err})
				continue
			}
			out.Outcomes = append(out.Outcomes, CIOutcome{DeliveryID: prep.DeliveryID, Err: err})
			continue
		}
		rep := Report{DeliveryID: prep.DeliveryID, ForgeRef: pr.Ref, ForgeURL: pr.URL, Created: created}
		if prep.AllowMerge {
			// Autonomous merge in CI mode is performed by the CI runner with its own token.
			if mc, mErr := forge.MergePR(ctx, pr.Ref); mErr == nil {
				rep.Merged, rep.MergeCommit = true, mc
			} else {
				out.Outcomes = append(out.Outcomes, CIOutcome{DeliveryID: prep.DeliveryID, PR: pr, Created: created, Err: mErr})
			}
		}
		if err := report.Report(ctx, rep); err != nil {
			out.Outcomes = append(out.Outcomes, CIOutcome{DeliveryID: prep.DeliveryID, PR: pr, Created: created, Err: err})
			continue
		}
		out.Delivered++
		out.Outcomes = append(out.Outcomes, CIOutcome{DeliveryID: prep.DeliveryID, PR: pr, Created: created})
	}
	return out, nil
}

// RecordFromReport is the platform-side handler for a CI report: it records the opened/updated delivery
// (and any merge the CI performed) in the append-only record, and returns the superseded deliveries the
// CI runner should close with its own token. The platform records; the CI closes — because closing a
// pull request is a forge write, and in this mode the platform holds no credential to make it.
func (d *Deliverer) RecordFromReport(ctx context.Context, prep Prepared, r Report) (Result, error) {
	pr := PullRequest{Ref: r.ForgeRef, URL: r.ForgeURL, State: "open"}
	res, err := d.RecordOpened(ctx, prep, pr, r.Created)
	if err != nil {
		return res, err
	}
	if r.Merged {
		if r.MergeCommit == "" {
			return res, fmt.Errorf("forgedelivery: a reported merge must carry the merge commit (an observation, not an inference)")
		}
		if err := d.rec.Append(ctx, Entry{
			DeliveryID: prep.DeliveryID, TenantID: prep.TenantID, ConfigHash: prep.ConfigHash,
			SourceRevision: prep.SourceRevision, Target: prep.Target.Key(), ForgeRef: r.ForgeRef,
			Mode: prep.Mode, State: StateMerged, Actor: "ci-reported", MergeCommit: r.MergeCommit, At: d.now().UTC(),
		}); err != nil {
			return res, err
		}
		res.Merged, res.MergeCommit = true, r.MergeCommit
	}
	return res, nil
}
