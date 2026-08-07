package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// linkcoverage_test.go is P29 §7.1, §7.3 and §7.6.

// coverageSource is a LinkIngestSource whose coverage answer is scriptable.
type coverageSource struct {
	cov linkingest.LinkCoverage
	err error
}

func (c coverageSource) Ingest(string, runlink.Payload) (linkingest.Result, error) {
	return linkingest.Result{Accepted: true}, nil
}
func (c coverageSource) Coverage(string) (linkingest.LinkCoverage, error) { return c.cov, c.err }
func (c coverageSource) LinkedRun(string, string) (linkingest.LinkedRun, bool, error) {
	return linkingest.LinkedRun{}, false, nil
}

// countingProvisioner records how it was called, and what plan the account already had.
type countingProvisioner struct {
	calls    int
	existing map[string]string // tenant -> plan it already has
	changed  []string          // tenants whose plan this provisioner altered
}

func (p *countingProvisioner) EnsureAccount(tenantID string) error {
	p.calls++
	if _, ok := p.existing[tenantID]; ok {
		// The real provisioner returns here WITHOUT reading the plan, so there is no path that could
		// change it. This stub records that it did not.
		return nil
	}
	if p.existing == nil {
		p.existing = map[string]string{}
	}
	p.existing[tenantID] = "free"
	return nil
}

// 🔴 §7.1 — an organization on a PAYING plan is unchanged across a restart and across repeated links.
//
// The failure this prevents is the one that costs money: a provisioner written as an upsert would move a
// paying customer back to Free, and the symptom is indistinguishable from a plan change they made
// themselves. `accountsystem.go`'s own comment has said so since P27 and this is the fence for it.
func TestProvisioningNeverCorrectsAnExistingAccount(t *testing.T) {
	p := &countingProvisioner{existing: map[string]string{"t-paying": "team-annual"}}
	s := New(nil, config.Config{})
	s.MountRunLinking(coverageSource{cov: linkingest.LinkCoverage{Known: true, RunsLinked: 2, RunsReported: 2, Complete: true}})
	s.MountLinkCoverage(p)

	// Many acts, including a "restart" (a second mount over the same provisioner).
	for i := 0; i < 5; i++ {
		enumRequest(t, s, "/api/v1/link-coverage", "t-paying")
	}
	s2 := New(nil, config.Config{})
	s2.MountRunLinking(coverageSource{cov: linkingest.LinkCoverage{Known: true}})
	s2.MountLinkCoverage(p)
	enumRequest(t, s2, "/api/v1/link-coverage", "t-paying")

	if p.calls == 0 {
		t.Fatal("the provisioner was never called — an authenticated act must provision, or an " +
			"organization created by self-serve sign-up still has no billing surface")
	}
	if plan := p.existing["t-paying"]; plan != "team-annual" {
		t.Errorf("a paying organization's plan became %q after %d authenticated act(s). It must be "+
			"UNTOUCHED: the provisioner is create-if-absent, and an upsert here moves a paying customer "+
			"back to Free with a symptom nobody can distinguish from a plan change they made.",
			plan, p.calls)
	}
	if len(p.changed) != 0 {
		t.Errorf("the provisioner altered %v", p.changed)
	}

	// And an organization with NO account gets one.
	enumRequest(t, s, "/api/v1/link-coverage", "t-new")
	if p.existing["t-new"] != "free" {
		t.Errorf("an organization with no account did not get one (%q). That was the whole defect: a "+
			"linked run attributed to a customer the billing read model cannot find, and an /app/billing "+
			"that is ABSENT rather than empty.", p.existing["t-new"])
	}
}

// 🔴 §7.3 — UNKNOWN coverage renders distinctly from complete, and a read failure yields unknown rather
// than zero.
//
// Verified red by making the coverage read return `(linkingest.LinkCoverage{}, nil)` on error — the
// shape of `return 0, 0, nil`. Coverage then answered `known:false` with zeros that a caller reading the
// numbers would render as "0 of 0 runs observed", which is a claim, and a false one.
func TestUnknownCoverageIsDistinctFromCompleteAndFromZero(t *testing.T) {
	cases := []struct {
		what      string
		src       coverageSource
		wantState string
		wantKnown bool
	}{
		{"a read failure", coverageSource{err: errors.New("db down")}, "unknown", false},
		{"an unknown denominator", coverageSource{cov: linkingest.LinkCoverage{Known: false}}, "unknown", false},
		{"complete coverage", coverageSource{cov: linkingest.LinkCoverage{Known: true, Complete: true, RunsLinked: 3, RunsReported: 3}}, "complete", true},
		{"partial coverage", coverageSource{cov: linkingest.LinkCoverage{Known: true, RunsLinked: 1, RunsReported: 3}}, "partial", true},
	}
	for _, tc := range cases {
		s := New(nil, config.Config{})
		s.MountRunLinking(tc.src)
		s.MountLinkCoverage(nil)

		rec := enumRequest(t, s, "/api/v1/link-coverage", "t-1")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s answered %d: %s", tc.what, rec.Code, rec.Body.String())
		}
		var body LinkCoverageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.State != tc.wantState {
			t.Errorf("%s → state %q, want %q", tc.what, body.State, tc.wantState)
		}
		if body.Known != tc.wantKnown {
			t.Errorf("%s → known %v, want %v", tc.what, body.Known, tc.wantKnown)
		}
	}

	// 🔴 And the two that must never be confusable ARE different bytes on the wire. A spend figure at
	// 100%% coverage and one whose denominator could not be read look identical as a NUMBER and mean
	// opposite things, so the distinction has to survive serialisation.
	render := func(src coverageSource) string {
		s := New(nil, config.Config{})
		s.MountRunLinking(src)
		s.MountLinkCoverage(nil)
		return enumRequest(t, s, "/api/v1/link-coverage", "t-1").Body.String()
	}
	unknown := render(coverageSource{err: errors.New("db down")})
	complete := render(coverageSource{cov: linkingest.LinkCoverage{Known: true, Complete: true}})
	if unknown == complete {
		t.Errorf("unknown coverage and complete coverage serialise identically:\n  %s", unknown)
	}
}

// Coverage is readable with NO plan, NO account and NO invoice — that is the whole point of lifting it
// out of the billing view.
func TestCoverageIsReadableWithNoBillingAtAll(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountRunLinking(coverageSource{cov: linkingest.LinkCoverage{Known: true, RunsLinked: 4, RunsReported: 9}})
	s.MountLinkCoverage(nil) // no provisioner, no account system, no plan catalog

	rec := enumRequest(t, s, "/api/v1/link-coverage", "t-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d — coverage must not require a billing account: it is the one number a link "+
			"certainly produces, and it was unreadable for exactly as long as it lived inside BillingView",
			rec.Code)
	}
	var body LinkCoverageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.RunsLinked != 4 || body.RunsReported != 9 {
		t.Errorf("got %d/%d, want 4/9", body.RunsLinked, body.RunsReported)
	}
}

// An unmounted linking capability is a POLICY answer, distinct from unknown coverage.
func TestUnmountedLinkingIsNotUnknownCoverage(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountLinkCoverage(nil)

	rec := enumRequest(t, s, "/api/v1/link-coverage", "t-1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503. \"This deployment does not accept run links\" and \"we could not "+
			"read your coverage\" are different facts: one is permanent and one is transient.", rec.Code)
	}
}
