package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/verification"
)

// fakeSink records what reached the store, and whose proposal it was reported against. It answers
// ErrNotFound for a proposal the tenant does not own, which is what the real store's WHERE clause does.
type fakeSink struct {
	owner  map[string]string // proposal id -> owning tenant
	got    []verification.Verdict
	tenant string
}

func (f *fakeSink) PutVerdict(_ context.Context, tenantID string, v verification.Verdict) error {
	if f.owner[v.ProposalID] != tenantID {
		return proposalstore.ErrNotFound
	}
	f.tenant = tenantID
	f.got = append(f.got, v)
	return nil
}

func verdictServer(t *testing.T) (*Server, *fakeSink) {
	t.Helper()
	sink := &fakeSink{owner: map[string]string{"prop-1": "tenantA"}}
	s := New(nil, config.Config{})
	s.MountVerdictIngest(sink)
	return s, sink
}

func verdictBody(t *testing.T, mutate func(*runlink.VerdictPayload)) []byte {
	t.Helper()
	p := runlink.BuildVerdict(runlink.VerdictRecord{
		ProposalID: "prop-1", ConfigHash: strings.Repeat("a", 64), DiffHash: strings.Repeat("b", 64),
		SourceRevision: "rev1", Metric: "quality",
		Delta: 0.06, CILow: 0.02, CIHigh: 0.10, Significant: true, HeldOut: true,
		CostDelta: -0.004, LatencyDelta: -120, RegressionPass: true, GateResult: "pass",
		CasesFixed:  []string{"c1", "c2", "c3", "c4"},
		CasesBroken: nil,
	})
	if mutate != nil {
		mutate(&p)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func postVerdict(t *testing.T, s *Server, tenant, proposalID string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/proposals/"+proposalID+"/verdict", bytes.NewReader(body))
	if tenant != "" {
		req = req.WithContext(auth.WithPrincipal(req.Context(),
			auth.Principal{TenantID: tenant, Role: "member", APIKeyID: "k"}))
	}
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

func TestVerdictIngestRecordsWhatWasReported(t *testing.T) {
	s, sink := verdictServer(t)
	rec := postVerdict(t, s, "tenantA", "prop-1", verdictBody(t, nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if len(sink.got) != 1 {
		t.Fatalf("recorded %d verdicts", len(sink.got))
	}
	v := sink.got[0]
	if v.GateResult != verification.GatePass || !v.Passed() {
		t.Errorf("gate result did not survive: %+v", v)
	}
	// The COUNT arrives; the ids do not, and neither does the reason. All three are the contract.
	if v.CasesFixedCount != 4 {
		t.Errorf("cases_fixed_count = %d, want 4 — the count is the whole record of how many", v.CasesFixedCount)
	}
	if len(v.CasesFixed) != 0 || len(v.CasesBroken) != 0 {
		t.Errorf("case ids reached the store: %+v / %+v", v.CasesFixed, v.CasesBroken)
	}
	if v.Reason != "" {
		t.Errorf("a reason reached the store: %q", v.Reason)
	}
	if v.Delta.Mean != 0.06 || v.Delta.Low != 0.02 || v.Delta.High != 0.10 {
		t.Errorf("the interval did not survive: %+v", v.Delta)
	}
}

// The narration must not end mid-sentence when the reason was not reported.
func TestAReportedVerdictNarratesWithoutItsReason(t *testing.T) {
	s, sink := verdictServer(t)
	body := verdictBody(t, func(p *runlink.VerdictPayload) {
		p.GateResult = string(verification.GateFailSig)
		p.Significant = false
	})
	if rec := postVerdict(t, s, "tenantA", "prop-1", body); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	n := verification.Narrate(sink.got[0])
	if strings.HasSuffix(strings.TrimSpace(n), ".") && strings.Contains(n, "(a tie). \"") {
		t.Fatalf("narration ends mid-thought: %q", n)
	}
	if !strings.Contains(n, "not reported") {
		t.Errorf("a verdict with no reason must SAY the reason was not reported, got: %q", n)
	}
}

// 🔴 The one that matters most: a verdict for someone else's proposal.
func TestVerdictIngestRefusesAnotherTenantsProposal(t *testing.T) {
	s, sink := verdictServer(t)
	rec := postVerdict(t, s, "tenantB", "prop-1", verdictBody(t, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — a passing verdict on another tenant's proposal is what opens "+
			"a pull request into their repository; body %s", rec.Code, rec.Body)
	}
	if len(sink.got) != 0 {
		t.Errorf("the verdict was recorded anyway: %+v", sink.got)
	}
	// The answer must not reveal that the id exists under another tenant.
	if strings.Contains(rec.Body.String(), "tenantA") || strings.Contains(strings.ToLower(rec.Body.String()), "not yours") {
		t.Errorf("the refusal leaks that the proposal exists elsewhere: %s", rec.Body)
	}
}

func TestVerdictIngestRequiresAuthentication(t *testing.T) {
	s, _ := verdictServer(t)
	if rec := postVerdict(t, s, "", "prop-1", verdictBody(t, nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestVerdictIngestUnmountedIs503(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountVerdictIngest(nil)
	rec := postVerdict(t, s, "tenantA", "prop-1", verdictBody(t, nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 — 'not accepted here' and 'no such proposal' are different "+
			"answers with different next actions", rec.Code)
	}
}

// A field outside the wire contract is REJECTED, not ignored. On this endpoint that is the privacy
// control: the field a future CLI would add is a case id or a reason.
func TestVerdictIngestRejectsAnUnknownField(t *testing.T) {
	s, sink := verdictServer(t)
	var m map[string]any
	if err := json.Unmarshal(verdictBody(t, nil), &m); err != nil {
		t.Fatal(err)
	}
	m["cases_fixed"] = []string{"case_ceo_comp_2025_q3"}
	b, _ := json.Marshal(m)

	rec := postVerdict(t, s, "tenantA", "prop-1", b)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — an unknown key must be refused, not silently dropped", rec.Code)
	}
	if len(sink.got) != 0 {
		t.Errorf("the payload was accepted anyway: %+v", sink.got)
	}
}

func TestVerdictIngestRefusesAMismatchedOrUnknownField(t *testing.T) {
	for name, tc := range map[string]struct {
		proposalID string
		mutate     func(*runlink.VerdictPayload)
		want       int
	}{
		"path and body disagree": {
			proposalID: "prop-other", want: http.StatusBadRequest,
		},
		"contract version mismatch": {
			proposalID: "prop-1", want: http.StatusUpgradeRequired,
			mutate: func(p *runlink.VerdictPayload) { p.ContractVersion = "p55.verdict.v0" },
		},
		"unknown gate result": {
			proposalID: "prop-1", want: http.StatusBadRequest,
			mutate: func(p *runlink.VerdictPayload) { p.GateResult = "probably_fine" },
		},
		"negative count": {
			proposalID: "prop-1", want: http.StatusBadRequest,
			mutate: func(p *runlink.VerdictPayload) { p.CasesFixedCount = -1 },
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, sink := verdictServer(t)
			rec := postVerdict(t, s, "tenantA", tc.proposalID, verdictBody(t, tc.mutate))
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body)
			}
			if len(sink.got) != 0 {
				t.Errorf("a refused payload was recorded: %+v", sink.got)
			}
		})
	}
}
