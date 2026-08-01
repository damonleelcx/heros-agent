package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/verification"
)

func demoPres() proposal.Presentation {
	return proposal.Presentation{Operator: proposal.OpModelUpgrade, NodeID: "n", Pattern: "routing",
		DiagID: "d", EvidenceCaseIDs: []string{"c1"}, Rationale: "r", SourceDiff: "--- a\n+++ b\n"}
}

// fakeP55 is a minimal P55Source for the handler tests. It serves one surface and records OpenPR calls
// so the gate-at-the-boundary can be asserted.
type fakeP55 struct {
	surface  Surface
	prCalled bool
}

func (f *fakeP55) Surface(string) (Surface, bool) { return f.surface, true }
func (f *fakeP55) OpenPR(_, pid string) (PRResult, error) {
	f.prCalled = true
	return PRResult{ProposalID: pid, Branch: "optimizer/" + pid, URL: "local", Rollback: "git revert x"}, nil
}

func p55Server(src P55Source) *Server {
	s := New(nil, config.Config{})
	s.MountP55(src)
	return s
}

func surfaceWith(recs, withheld []Card) Surface {
	return Surface{WorkflowID: "wf1", AutomationLevel: "assisted", State: "ready",
		Recommendations: recs, Withheld: withheld}
}

// The page is served self-contained (no external fetch) and expresses every state it owes the reader.
func TestP55UIIsSelfContained(t *testing.T) {
	s := p55Server(&fakeP55{})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/recommendations", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, bad := range []string{"http://", "https://cdn", "<script src="} {
		if strings.Contains(body, bad) {
			t.Errorf("the page reaches outside itself (%q)", bad)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the view must not be cached")
	}
	// Every first-class state and label the surface owes the reader must be expressible in the page.
	for _, want := range []string{"verified", "gate-failed", "build-failed", "held-out", "not held-out",
		"No verified improvement found", "Did not pass verification", "Open PR", "Advisory", "Assisted", "Trend"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page cannot render %q", want)
		}
	}
}

// Unmounted → 503 (distinct from 404), so "not wired" and "no data" never look alike.
func TestP55Unmounted503(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountP55(nil) // routes registered, source nil → 503 (distinct from an unregistered 404)
	for _, path := range []string{"/api/v1/workflows/wf1/proposals"} {
		rec := httptest.NewRecorder()
		s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: want 503 unmounted, got %d", path, rec.Code)
		}
	}
}

// The surface JSON carries only gate-passing recommendations; withheld are separate.
func TestP55SurfaceJSON(t *testing.T) {
	recs := []Card{{ProposalID: "ok", State: "verified", GateResult: "pass", CanOpenPR: true}}
	withheld := []Card{{ProposalID: "bad", State: "gate_failed", GateResult: "fail_regression"}}
	s := p55Server(&fakeP55{surface: surfaceWith(recs, withheld)})

	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf1/proposals", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got Surface
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Recommendations) != 1 || got.Recommendations[0].ProposalID != "ok" {
		t.Errorf("recommendations must contain only the gate-passing proposal, got %+v", got.Recommendations)
	}
	if len(got.Withheld) != 1 || got.Withheld[0].ProposalID != "bad" {
		t.Errorf("withheld must contain the gate-failed proposal, got %+v", got.Withheld)
	}
}

// §6.4 / §7.5: the OpenPR endpoint re-checks the gate. A proposal that is not a gate-passing,
// one-click-openable recommendation is REFUSED (409), even by a direct POST.
func TestP55OpenPR_GatedAtBoundary(t *testing.T) {
	recs := []Card{{ProposalID: "ok", State: "verified", GateResult: "pass", CanOpenPR: true}}
	withheld := []Card{{ProposalID: "bad", State: "gate_failed", GateResult: "fail_regression", CanOpenPR: false}}
	fake := &fakeP55{surface: surfaceWith(recs, withheld)}
	s := p55Server(fake)

	// A verified, openable proposal → 200 and OpenPR invoked.
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf1/proposals/ok/open-pr", nil))
	if rec.Code != http.StatusOK || !fake.prCalled {
		t.Fatalf("a gate-passing proposal must open a PR, got status %d prCalled=%v", rec.Code, fake.prCalled)
	}

	// A gate-failed proposal → 409, OpenPR never invoked.
	fake.prCalled = false
	rec = httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf1/proposals/bad/open-pr", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("a gate-failed proposal must be refused (409), got %d", rec.Code)
	}
	if fake.prCalled {
		t.Error("OpenPR must not be invoked for a non-passing proposal — the gate is at the boundary")
	}
}

// BuildCard is the single verdict→UI mapping; the PR gate it sets must match verification.PROpenAvailable.
func TestBuildCard_PRGateMatchesVerdict(t *testing.T) {
	pass := verification.Verdict{ProposalID: "p", GateResult: verification.GatePass, HeldOut: true}
	c := BuildCard(demoPres(), "built", pass, verification.Assisted)
	if !c.CanOpenPR || c.State != "verified" {
		t.Errorf("assisted + pass must be openable and verified, got %+v", c)
	}
	adv := BuildCard(demoPres(), "built", pass, verification.Advisory)
	if adv.CanOpenPR || adv.PRDisabledReason == "" {
		t.Error("advisory must not offer one-click PR-open and must give a reason")
	}
}

// ── P14 task 8.2 — a refused candidate reaches the surface BY NAME ────────────────────────────────

// TestCardForRoutesEveryBuildStatus is the gate that made api.CardFor worth having.
//
// The defect it prevents is not hypothetical — it is what the codebase actually had until P14 added a
// third status: each producer wrote its own two-branch `if` over (built / build_failed), so a `refused`
// candidate fell through the `built` branch and rendered a verified zero delta and an empty diff for a
// change the transform had explicitly declined to make. The surface would then be asserting, in the
// product's own voice, that a change nobody wrote made no difference.
//
// So the routing is exhaustive HERE, and the default is refused rather than built: "we could not tell
// you what happened" is honest, and "verified, delta 0.00" is not.
func TestCardForRoutesEveryBuildStatus(t *testing.T) {
	refusal := proposal.ChangeRefusal{NodeID: "agent", Dimension: "skills",
		Reason: "no materializer for this language has landed yet"}
	pres := demoPres()
	pres.Refusal = &refusal
	pres.SourceDiff = "" // a refusal carries no diff

	pass := verification.Verdict{ProposalID: "p1", GateResult: verification.GatePass, RegressionPass: true, Significant: true}

	for _, tc := range []struct {
		status     proposal.BuildStatus
		wantStatus string
		wantNamed  bool
	}{
		{proposal.BuildBuilt, "built", false},
		{proposal.BuildFailed, string(proposal.BuildFailed), false},
		{proposal.BuildRefused, string(proposal.BuildRefused), true},
		// An unrecognised status must fail CLOSED, not render as verified.
		{proposal.BuildStatus("something_a_later_phase_added"), string(proposal.BuildRefused), true},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			card := CardFor(pres, tc.status, "log", pass, verification.Assisted)
			if card.BuildStatus != tc.wantStatus {
				t.Errorf("build_status = %q, want %q", card.BuildStatus, tc.wantStatus)
			}
			if !tc.wantNamed {
				return
			}
			if card.RefusedNodeID != "agent" || card.RefusedDimension != "skills" || card.RefusedReason == "" {
				t.Errorf("a refused card must name node, dimension and reason; got %+v",
					[]string{card.RefusedNodeID, card.RefusedDimension, card.RefusedReason})
			}
			if card.SourceDiff != "" {
				t.Error("a refused card must carry no source diff")
			}
			if card.CanOpenPR {
				t.Error("a refused card must not be able to open a pull request")
			}
			if !strings.Contains(card.Narration, "refused") {
				t.Errorf("the narration must say it was refused, got %q", card.Narration)
			}
		})
	}

	// 🔴 And the list decision: a refused candidate is never recommendable, even holding a PASSING
	// verdict — which is the shape a producer that reused a previous verdict would hand it.
	if Recommendable(proposal.BuildRefused, pass) {
		t.Error("a refused candidate was recommendable")
	}
	if !Recommendable(proposal.BuildBuilt, pass) {
		t.Error("a built, passing candidate must be recommendable, or nothing ever surfaces")
	}
}
