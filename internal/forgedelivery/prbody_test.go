package forgedelivery_test

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/verification"
)

func improvementVerdict() verification.Verdict {
	return verification.Verdict{
		Metric:     "quality",
		Delta:      evalstats.Interval{Mean: 0.12, Low: 0.04, High: 0.20, Confidence: 0.95},
		HeldOut:    true,
		GateResult: verification.GatePass,
		CasesFixed: []string{"c1", "c2", "c3"},
		CostDelta:  -0.0012, LatencyDelta: -40,
	}
}

func tieVerdict() verification.Verdict {
	v := improvementVerdict()
	// The interval now spans the baseline: mean positive, low bound below zero — a tie.
	v.Delta = evalstats.Interval{Mean: 0.03, Low: -0.02, High: 0.08, Confidence: 0.95}
	return v
}

func evidence(v verification.Verdict, ref string) fd.Evidence {
	return fd.Evidence{
		Title: "Optimize the extraction node", Level: "assisted", Verdict: v,
		ConfigHash: "ch1", SourceRevision: "rev1", ConsoleRef: ref, DiffStat: "2 files, +18 −6",
	}
}

// 3.1: the body carries every required piece.
func TestRenderPRBody_CarriesEvidence(t *testing.T) {
	ref, err := fd.ConsoleEvidenceRef("https://console.example.com", "ch1", "rev1")
	if err != nil {
		t.Fatal(err)
	}
	body := fd.RenderPRBody(evidence(improvementVerdict(), ref))
	for _, want := range []string{
		fd.PRBodyContractVersion,       // the version marker (task 1.1)
		"## Verified delta",            // verified delta section
		"95% confidence interval",      // the CI is present
		"## Held-out status", "held-out",
		"## Eval evidence", "Cases fixed", "Cost impact", "Latency impact",
		"## Lineage", "config_hash", "source_revision", "ch1", "rev1",
		"## Full evidence", ref, // the canonical console reference
		"2 files, +18 −6", // the diff stat
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body missing %q\n---\n%s", want, body)
		}
	}
}

// 3.2 / 3.4: an improvement reads as an improvement, WITH its interval.
func TestRenderPRBody_ImprovementReadsAsImprovement(t *testing.T) {
	body := fd.RenderPRBody(evidence(improvementVerdict(), "https://c.example/e"))
	if !strings.Contains(body, "Verified quality improvement of +0.120") {
		t.Errorf("improvement not rendered as an improvement:\n%s", body)
	}
	if !strings.Contains(body, "[+0.040, +0.200]") {
		t.Errorf("improvement missing its interval:\n%s", body)
	}
	if strings.Contains(body, "a tie") {
		t.Errorf("an improvement was described as a tie:\n%s", body)
	}
}

// 3.2 / 3.4 🔴: a tie-valued delta produces a PR that describes a TIE, never an improvement.
func TestRenderPRBody_TieReadsAsTie(t *testing.T) {
	if !fd.DeltaReadsAsTie(tieVerdict()) {
		t.Fatal("the tie fixture should read as a tie (interval overlaps baseline)")
	}
	body := fd.RenderPRBody(evidence(tieVerdict(), "https://c.example/e"))
	if !strings.Contains(body, "a tie") || !strings.Contains(body, "No measurable improvement") {
		t.Errorf("a tie was not described as a tie:\n%s", body)
	}
	if strings.Contains(body, "Verified quality improvement") {
		t.Errorf("a tie was oversold as an improvement:\n%s", body)
	}
	// The interval is still shown — a tie is stated honestly, not hidden.
	if !strings.Contains(body, "[-0.020, +0.080]") {
		t.Errorf("tie body should still carry the interval:\n%s", body)
	}
}

// 3.2 boundary: an interval whose low bound is exactly the baseline (0) is a tie, not an improvement.
func TestDeltaReadsAsTie_Boundary(t *testing.T) {
	v := improvementVerdict()
	v.Delta = evalstats.Interval{Mean: 0.05, Low: 0.0, High: 0.10}
	if !fd.DeltaReadsAsTie(v) {
		t.Errorf("Low == 0 overlaps the baseline and must read as a tie")
	}
	v.Delta.Low = 0.0001
	if fd.DeltaReadsAsTie(v) {
		t.Errorf("Low > 0 does not overlap the baseline and is an improvement")
	}
}

// 3.3: the console reference is absolute and canonical, so it resolves wherever it is pasted.
func TestConsoleEvidenceRef_Canonical(t *testing.T) {
	ref, err := fd.ConsoleEvidenceRef("https://console.example.com/", "ch/1", "rev 1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "https://console.example.com/app/transforms/") {
		t.Errorf("reference not canonical/absolute: %q", ref)
	}
	// Components are percent-encoded exactly once.
	if !strings.Contains(ref, "ch%2F1") || !strings.Contains(ref, "rev%201") {
		t.Errorf("reference components not encoded: %q", ref)
	}
	// A non-absolute base is refused rather than producing a link that resolves against the forge.
	if _, err := fd.ConsoleEvidenceRef("/app", "ch1", "rev1"); err == nil {
		t.Errorf("a relative console base should be refused")
	}
}

// 3.1 / 9.1 support: rendering is deterministic — identical evidence yields byte-identical bodies. This
// is what lets the parity gate byte-compare the two modes.
func TestRenderPRBody_Deterministic(t *testing.T) {
	e := evidence(improvementVerdict(), "https://c.example/e")
	if fd.RenderPRBody(e) != fd.RenderPRBody(e) {
		t.Errorf("PR body rendering is not deterministic")
	}
}

// Security: no credential-looking token can appear in a body — the body is rendered from evidence that
// contains none, and this fences a regression that added one.
func TestRenderPRBody_NoCredential(t *testing.T) {
	body := fd.RenderPRBody(evidence(improvementVerdict(), "https://c.example/e"))
	for _, forbidden := range []string{"ghp_", "github_pat_", "X-API-Key", "Authorization:", "token "} {
		if strings.Contains(body, forbidden) {
			t.Errorf("PR body contains a credential-shaped string %q", forbidden)
		}
	}
}
