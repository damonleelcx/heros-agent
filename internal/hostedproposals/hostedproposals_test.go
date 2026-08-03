package hostedproposals

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/verification"
)

type fakeStore struct {
	scored []proposalstore.Scored
	err    error
}

func (f fakeStore) ForWorkflow(context.Context, string, string) ([]proposalstore.Scored, error) {
	return f.scored, f.err
}

func rec(id string, verdict *verification.Verdict, build string) proposalstore.Scored {
	return proposalstore.Scored{
		Record: proposalstore.Record{
			ProposalID: id, TenantID: "t1", WorkflowID: "wf",
			Operator: "model_downgrade", NodeID: "n_router", Pattern: "Routing",
			Rationale:           "cost bottleneck → downgrade to a cheaper model",
			CandidateConfigHash: strings.Repeat("a", 64), SourceRevision: "rev1",
			BuildStatus: build, Status: proposalstore.StatusCandidate,
			CreatedAt: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
		},
		Verdict: verdict,
	}
}

func passing(delta float64) *verification.Verdict {
	v := &verification.Verdict{
		Metric: "quality", Delta: evalstats.Interval{Mean: delta, Low: delta - 0.02, High: delta + 0.02},
		Significant: true, HeldOut: true, RegressionPass: true, GateResult: verification.GatePass,
		CasesFixedCount: 4,
	}
	return v
}

// 🔴 The card must never claim a diff this platform did not compile, and Open-PR must be OFF with a
// reason. An "Open PR" button that produced a pull request with no changes in it is the "looks
// complete" failure the surface exists to avoid.
func TestNoCardOffersAPullRequestThereIsNoDiffFor(t *testing.T) {
	s := NewSource(fakeStore{scored: []proposalstore.Scored{
		rec("prop-1", passing(0.06), proposalstore.BuildUnbuilt),
		rec("prop-2", nil, proposalstore.BuildUnbuilt),
	}})
	surface, ok := s.Surface("t1", "wf")
	if !ok {
		t.Fatal("the surface did not resolve")
	}
	all := append(append([]any{}, toAny(surface.Recommendations)...), toAny(surface.Withheld)...)
	if len(all) != 2 {
		t.Fatalf("2 proposals in, %d cards out", len(all))
	}
	for _, c := range surface.Recommendations {
		if c.CanOpenPR {
			t.Errorf("card %s offers a pull request with no diff behind it", c.ProposalID)
		}
	}
	for _, c := range append(surface.Recommendations, surface.Withheld...) {
		if c.SourceDiff != "" {
			t.Errorf("card %s rendered a diff nobody compiled: %q", c.ProposalID, c.SourceDiff)
		}
		if c.PRDisabledReason == "" {
			t.Errorf("card %s disables the PR action without saying why", c.ProposalID)
		}
	}
}

// The card is addressed by the id the PLATFORM issued — the one `heros report-verdict --proposal` and
// the open-PR route take. api.BuildCard takes it from the verdict and the withheld cards fall back to
// the config hash; neither resolves.
func TestTheCardCarriesTheStoresProposalID(t *testing.T) {
	s := NewSource(fakeStore{scored: []proposalstore.Scored{
		rec("prop-1", passing(0.06), proposalstore.BuildUnbuilt),
		rec("prop-2", nil, proposalstore.BuildUnbuilt),
	}})
	surface, _ := s.Surface("t1", "wf")
	seen := map[string]bool{}
	for _, c := range append(surface.Recommendations, surface.Withheld...) {
		seen[c.ProposalID] = true
	}
	for _, want := range []string{"prop-1", "prop-2"} {
		if !seen[want] {
			t.Errorf("no card is addressed by %q — a card whose id does not resolve is a button that 404s "+
				"and a proposal a customer cannot report a verdict for; got %v", want, seen)
		}
	}
}

// An unmeasured proposal is "awaiting verification", never "gate failed". They are opposite claims.
func TestAnUnmeasuredProposalIsNotReportedAsRejected(t *testing.T) {
	s := NewSource(fakeStore{scored: []proposalstore.Scored{rec("prop-1", nil, proposalstore.BuildUnbuilt)}})
	surface, _ := s.Surface("t1", "wf")
	if len(surface.Withheld) != 1 {
		t.Fatalf("withheld = %+v", surface.Withheld)
	}
	c := surface.Withheld[0]
	if c.State == "gate_failed" {
		t.Error("a proposal nobody has measured is reported as having failed the gate")
	}
	if c.GateResult != string(verification.GateUnrun) {
		t.Errorf("gate_result = %q, want %q", c.GateResult, verification.GateUnrun)
	}
	if !strings.Contains(strings.ToLower(c.Narration), "await") {
		t.Errorf("the narration must say it is waiting on a measurement, got %q", c.Narration)
	}
	if surface.State != "verifying" {
		t.Errorf("surface state = %q; proposals exist and are awaiting a verdict, which is not `empty`",
			surface.State)
	}
}

// Each "nothing to show" reason is its own state. A read failure is emphatically not an empty surface.
func TestEachSurfaceStateIsDistinct(t *testing.T) {
	for name, tc := range map[string]struct {
		store fakeStore
		want  string
	}{
		"no proposals at all":      {fakeStore{}, "empty"},
		"proposals awaiting a run": {fakeStore{scored: []proposalstore.Scored{rec("p", nil, proposalstore.BuildUnbuilt)}}, "verifying"},
		"the store is unreadable":  {fakeStore{err: errors.New("connection refused")}, "error"},
	} {
		t.Run(name, func(t *testing.T) {
			surface, ok := NewSource(tc.store).Surface("t1", "wf")
			if !ok {
				t.Fatal("the surface must resolve: `no such workflow` and `nothing proposed` send a " +
					"reader to two different places")
			}
			if surface.State != tc.want {
				t.Errorf("state = %q, want %q", surface.State, tc.want)
			}
			if surface.Recommendations == nil || surface.Withheld == nil {
				t.Error("both lists must be non-nil so the console renders two empty sections")
			}
		})
	}
}

// A read failure must not leak the driver's words to a tenant, and must not read as a verdict.
func TestAReadFailureDoesNotLeakOrLookLikeAFinding(t *testing.T) {
	surface, _ := NewSource(fakeStore{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")}).Surface("t1", "wf")
	if strings.Contains(surface.Error, "10.0.0.5") || strings.Contains(surface.Error, "dial tcp") {
		t.Errorf("the read error leaked into the surface: %q", surface.Error)
	}
	if surface.Error == "" {
		t.Error("an error state with no sentence is indistinguishable from an empty one")
	}
}

// OpenPR refuses for every proposal, including a gate-passing one, and names the limit.
func TestOpenPRRefusesAndSaysWhy(t *testing.T) {
	s := NewSource(fakeStore{scored: []proposalstore.Scored{rec("prop-1", passing(0.06), proposalstore.BuildUnbuilt)}})
	_, err := s.OpenPR("t1", "wf", "prop-1")
	if err == nil {
		t.Fatal("OpenPR returned success with no diff behind it")
	}
	if !strings.Contains(err.Error(), "prop-1") {
		t.Errorf("the refusal does not name the proposal: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "compile") {
		t.Errorf("the refusal does not name the limit that causes it: %v", err)
	}
}

// The trend uses MEASURED proposals only. Padding it with unverified ones at delta zero would draw a
// flat line through changes nobody measured and read as "your workflow is not improving".
func TestTheTrendIsBuiltFromMeasurementsOnly(t *testing.T) {
	s := NewSource(fakeStore{scored: []proposalstore.Scored{
		rec("p1", nil, proposalstore.BuildUnbuilt),
		rec("p2", nil, proposalstore.BuildUnbuilt),
		rec("p3", nil, proposalstore.BuildUnbuilt),
	}})
	surface, _ := s.Surface("t1", "wf")
	if len(surface.Trend.Points) != 0 {
		t.Fatalf("unverified proposals became trend points: %+v", surface.Trend.Points)
	}
	if !strings.Contains(surface.Trend.Narrative, "Not enough") {
		t.Errorf("with no measurements the trend must say so, got %q", surface.Trend.Narrative)
	}
}

// Recommendations rank by verified delta, best first.
func TestRecommendationsRankByVerifiedDelta(t *testing.T) {
	small := rec("p-small", passing(0.02), proposalstore.BuildBuilt)
	big := rec("p-big", passing(0.09), proposalstore.BuildBuilt)
	s := NewSource(fakeStore{scored: []proposalstore.Scored{small, big}})
	surface, _ := s.Surface("t1", "wf")
	if len(surface.Recommendations) != 2 {
		t.Fatalf("recommendations = %+v", surface.Recommendations)
	}
	if surface.Recommendations[0].ProposalID != "p-big" {
		t.Errorf("ranked %q first; the larger verified delta must lead", surface.Recommendations[0].ProposalID)
	}
}

// A gate-passing proposal that never BUILT is withheld, not recommended. Both halves are required —
// api.Recommendable says so, and this deployment only ever produces the unbuilt half.
func TestAPassingButUnbuiltProposalIsWithheld(t *testing.T) {
	s := NewSource(fakeStore{scored: []proposalstore.Scored{rec("p1", passing(0.09), proposalstore.BuildUnbuilt)}})
	surface, _ := s.Surface("t1", "wf")
	if len(surface.Recommendations) != 0 {
		t.Errorf("an unbuilt proposal was recommended: %+v", surface.Recommendations)
	}
	if len(surface.Withheld) != 1 {
		t.Errorf("withheld = %+v", surface.Withheld)
	}
}

// Advisory, not assisted: assisted is the level at which the platform opens the pull request, and it
// cannot. Declaring it would light the Open-PR affordance on every card.
func TestTheSurfaceDeclaresAdvisory(t *testing.T) {
	surface, _ := NewSource(fakeStore{}).Surface("t1", "wf")
	if surface.AutomationLevel != string(verification.Advisory) {
		t.Errorf("automation level = %q, want %q", surface.AutomationLevel, verification.Advisory)
	}
}

func toAny[T any](in []T) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// ── the P12 adapters ────────────────────────────────────────────────────────────────────────────────

type fakePending struct {
	scored []proposalstore.Scored
	err    error
}

func (f fakePending) PendingVerified(context.Context, string) ([]proposalstore.Scored, error) {
	return f.scored, f.err
}

// 🔴 The delivery candidate carries NO DIFF, because the platform compiled none — and that must remain
// visible rather than being papered over with a placeholder patch to make the pipeline "work".
// Deliverer.Prepare refuses it (ErrNoDiff) and Service.Pending reports `no_diff`.
func TestADeliveryCandidateCarriesNoInventedDiff(t *testing.T) {
	p := NewPending(fakePending{scored: []proposalstore.Scored{
		rec("prop-1", passing(0.06), proposalstore.BuildUnbuilt),
	}}, "https://console.example")

	got, err := p.PendingVerified(context.Background(), "t1")
	if err != nil {
		t.Fatalf("PendingVerified: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates", len(got))
	}
	c := got[0]
	if c.DiffPatch != "" {
		t.Errorf("a diff was synthesised for a proposal nobody compiled: %q", c.DiffPatch)
	}
	if c.DiffStat != "" {
		t.Errorf("a diff STAT was rendered for a diff that does not exist: %q — it would appear in a "+
			"pull-request body as a claim about content nobody generated", c.DiffStat)
	}
	if c.TenantID != "t1" || c.ProposalID != "prop-1" {
		t.Errorf("identity did not survive: %+v", c)
	}
	if !strings.Contains(c.Title, "n_router") {
		t.Errorf("the title does not name the node being changed: %q", c.Title)
	}
	if !strings.HasPrefix(c.ConsoleRef, "https://console.example/") {
		t.Errorf("console ref = %q, want the configured origin", c.ConsoleRef)
	}
}

// With no console origin the evidence ref is RELATIVE, not pointed at a host that may not resolve.
func TestAHeadlessDeploymentGetsARelativeConsoleRef(t *testing.T) {
	p := NewPending(fakePending{scored: []proposalstore.Scored{
		rec("prop-1", passing(0.06), proposalstore.BuildUnbuilt),
	}}, "")
	got, _ := p.PendingVerified(context.Background(), "t1")
	if len(got) != 1 || !strings.HasPrefix(got[0].ConsoleRef, "/app/") {
		t.Errorf("console ref = %+v, want a relative path", got)
	}
}

// A proposal predating migration 0030 has no node id. The title says so rather than rendering
// "Optimize  (…)", which reads as a bug.
func TestAProposalWithNoNodeIDIsTitledHonestly(t *testing.T) {
	r := rec("prop-1", passing(0.06), proposalstore.BuildUnbuilt)
	r.NodeID = ""
	p := NewPending(fakePending{scored: []proposalstore.Scored{r}}, "")
	got, _ := p.PendingVerified(context.Background(), "t1")
	if len(got) != 1 {
		t.Fatal("no candidate")
	}
	if strings.Contains(got[0].Title, "Optimize  ") {
		t.Errorf("the title has a hole where the node should be: %q", got[0].Title)
	}
	if !strings.Contains(got[0].Title, "unrecorded") {
		t.Errorf("an absent node id must be named, got %q", got[0].Title)
	}
}

type fakeVerdicts struct {
	v     verification.Verdict
	ok    bool
	err   error
	calls int
}

func (f *fakeVerdicts) VerdictFor(context.Context, string, string, string) (verification.Verdict, bool, error) {
	f.calls++
	return f.v, f.ok, f.err
}

// The gate is a passthrough to the AUTHORITY, and it must report "not recorded" as ok=false rather than
// as a zero verdict — a zero GateResult is not `pass`, but "not measured" and "measured and rejected"
// are different sentences downstream.
func TestTheGateReportsUnmeasuredDistinctly(t *testing.T) {
	store := &fakeVerdicts{ok: false}
	v, ok, err := NewGate(store).Verdict(context.Background(), "t1", "ch", "rev")
	if err != nil {
		t.Fatalf("Verdict: %v", err)
	}
	if ok {
		t.Error("an unrecorded verdict was reported as recorded")
	}
	if v.Passed() {
		t.Error("a zero verdict passed the gate")
	}
	if store.calls != 1 {
		t.Errorf("the gate consulted the store %d times", store.calls)
	}
}
