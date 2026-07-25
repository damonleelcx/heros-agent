package forgedelivery_test

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/entitlement"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

// platformFetcher is the in-process stand-in for the authenticated fetch the CI runner performs. In
// production it is an HTTP client hitting the platform, which scopes the answer server-side; here it
// drives the SAME Prepare the platform would, proving the CI runner only ever sees platform-cleared,
// credential-free Prepared values.
type platformFetcher struct {
	d     *fd.Deliverer
	props []fd.Proposal
	route *fd.Route
}

func (f *platformFetcher) Pending(ctx context.Context, _ fd.Target) ([]fd.Prepared, error) {
	var out []fd.Prepared
	for _, p := range f.props {
		prep, err := f.d.Prepare(ctx, p, f.route) // server-side enforcement of gate/entitlement/halt/route
		if err != nil {
			continue // an undeliverable proposal is simply not served to CI — never leaked
		}
		out = append(out, prep)
	}
	return out, nil
}

// platformReporter is the in-process stand-in for the /report endpoint: it records on the platform.
type platformReporter struct {
	d     *fd.Deliverer
	preps map[string]fd.Prepared
}

func (r *platformReporter) Report(ctx context.Context, rep fd.Report) error {
	_, err := r.d.RecordFromReport(ctx, r.preps[rep.DeliveryID], rep)
	return err
}

// 5.5: end to end — a verified proposal flows through a CI job to a pull request on the customer's
// repository, with NO platform-held credential anywhere in the path.
func TestCIMediated_EndToEnd(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := route(fd.ModeCI)

	fetch := &platformFetcher{d: h.d, props: []fd.Proposal{proposal(entitlement.LevelAssisted)}, route: r}
	// The CI runner's own forge writer — holds the CI's credential, NOT a platform-held one.
	ciForge := fd.NewInMemForge(fd.ForgeGitHub, false)
	preps, _ := fetch.Pending(ctx, r.Target)
	if len(preps) != 1 {
		t.Fatalf("expected 1 prepared proposal, got %d", len(preps))
	}
	reporter := &platformReporter{d: h.d, preps: map[string]fd.Prepared{preps[0].DeliveryID: preps[0]}}

	report, err := fd.CIStep(ctx, fetch, ciForge, reporter, r.Target, 0)
	if err != nil {
		t.Fatalf("CIStep: %v", err)
	}
	if report.Delivered != 1 || report.CredentialDegraded {
		t.Fatalf("expected 1 delivered, not degraded; got %+v", report)
	}
	// The pull request exists on the customer's repository, opened by the CI runner's writer.
	if n, _ := ciForge.OpenPRCount(ctx, r.Target); n != 1 {
		t.Errorf("open PRs on the customer repo = %d, want 1", n)
	}
	// The platform recorded the delivery — without ever holding a forge credential.
	id := fd.DeliveryID("ch1", "rev1", r.Target.Key())
	head, ok, _ := h.rec.Head(ctx, id)
	if !ok || head.State != fd.StateOpened || head.Mode != fd.ModeCI {
		t.Errorf("platform did not record the CI delivery: %+v ok=%v", head, ok)
	}
}

// 5.5 (gate): the fetch does not serve an unverified proposal to CI — the server-side enforcement.
func TestCIMediated_UnverifiedNotServed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := route(fd.ModeCI)
	unverified := proposal(entitlement.LevelAssisted)
	unverified.ConfigHash = "no-verdict" // no gate-passed verdict recorded
	fetch := &platformFetcher{d: h.d, props: []fd.Proposal{unverified}, route: r}
	preps, _ := fetch.Pending(ctx, r.Target)
	if len(preps) != 0 {
		t.Errorf("an unverified proposal was served to CI (%d) — the gate leaked", len(preps))
	}
}

// degradedForge fails opens with ErrCICredentialExpired, modelling a rotated CI token.
type degradedForge struct{ *fd.InMemForge }

func (degradedForge) OpenOrUpdatePR(context.Context, fd.OpenRequest) (fd.PullRequest, bool, error) {
	return fd.PullRequest{}, false, fd.ErrCICredentialExpired
}

// 5.4: an expired/rotated CI credential is REPORTED as degraded — never a silent stop.
func TestCIMediated_DegradedCredentialReported(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := route(fd.ModeCI)
	fetch := &platformFetcher{d: h.d, props: []fd.Proposal{proposal(entitlement.LevelAssisted)}, route: r}
	preps, _ := fetch.Pending(ctx, r.Target)
	reporter := &platformReporter{d: h.d, preps: map[string]fd.Prepared{preps[0].DeliveryID: preps[0]}}

	broken := degradedForge{fd.NewInMemForge(fd.ForgeGitHub, false)}
	report, err := fd.CIStep(ctx, fetch, broken, reporter, r.Target, 0)
	if err != nil {
		t.Fatalf("CIStep should not hard-fail on a degraded credential: %v", err)
	}
	if !report.CredentialDegraded {
		t.Errorf("a rotated CI credential must be reported as degraded, not silently dropped")
	}
	if report.Delivered != 0 {
		t.Errorf("nothing should be delivered with a broken credential, got %d", report.Delivered)
	}
	// And it is distinguishable from "nothing to deliver": there WAS a proposal to deliver.
	if len(preps) == 0 {
		t.Errorf("test setup: expected a pending proposal so degraded != empty")
	}
}
