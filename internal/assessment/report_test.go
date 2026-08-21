package assessment

import (
	"encoding/json"
	"strings"
	"testing"
)

// nine builds a complete report whose findings the caller can override by axis.
func nine(t *testing.T, override map[Axis]Finding) Assessment {
	t.Helper()
	a := Assessment{
		AssessmentID: "as-1", TenantID: "t-1", WorkflowID: "wf-1",
		SourceRevision: "rev-abc", AgentConfigHash: "cfg-1",
	}
	for _, axis := range Axes() {
		if f, ok := override[axis]; ok {
			a.Findings = append(a.Findings, f)
			continue
		}
		f, err := NotMeasured(axis, MissingNoNodes,
			"no LLM call sites were discovered, so this surface has no subject yet",
			EvidenceRef{Surface: SurfaceGraph, Locator: "wf-1"})
		if err != nil {
			t.Fatalf("building the default finding for %s: %v", axis, err)
		}
		a.Findings = append(a.Findings, f)
	}
	return a
}

// TestNineAxesInARepositoryThatFailsAtEveryAxis is task 7.8 and acceptance A1. The interesting case
// is not the healthy repository; it is the one where every answer is "we could not", because that is
// the report a shortening instinct would turn into an empty page.
func TestNineAxesInARepositoryThatFailsAtEveryAxis(t *testing.T) {
	a := nine(t, nil)
	if err := a.Validate(); err != nil {
		t.Fatalf("a report in which every axis failed is still a nine-axis report: %v", err)
	}
	if len(a.Findings) != 9 {
		t.Fatalf("got %d findings, want 9", len(a.Findings))
	}
	if !a.AllNotMeasured() {
		t.Fatal("AllNotMeasured is false for a report in which every axis is not_measured — this is " +
			"DevOps task 6.2's signal and it is invisible in a success rate")
	}
	for _, f := range a.Ordered() {
		if f.MissingInput() == "" {
			t.Fatalf("%s is not_measured and names nothing", f.Axis())
		}
	}
}

// TestAnOmittedAxisFailsTheReport is FR1's teeth. A shorter report is prettier and lies by
// construction.
func TestAnOmittedAxisFailsTheReport(t *testing.T) {
	a := nine(t, nil)
	a.Findings = a.Findings[:8]
	err := a.Validate()
	if err == nil || !strings.Contains(err.Error(), "omits") {
		t.Fatalf("an eight-axis report validated: %v", err)
	}
}

// TestADuplicatedAxisFailsTheReport is why the check is set equality and not a count.
func TestADuplicatedAxisFailsTheReport(t *testing.T) {
	a := nine(t, nil)
	a.Findings[8] = a.Findings[0]
	err := a.Validate()
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("a report with nine findings covering eight axes validated: %v", err)
	}
}

// TestFindingsAreOrderedByEvidenceStrength is FR5. The ladder is measured → observed → inferred →
// not_measured → refused, and the rung between observed and not_measured is an ORIGIN, which is the
// only place in the report where the two vocabularies interleave.
func TestFindingsAreOrderedByEvidenceStrength(t *testing.T) {
	ev := EvidenceRef{Surface: SurfaceGraph, Locator: "wf-1"}

	measured, err := Measured(AxisTools, "the tool suite scores 0.81", EvidenceRef{Surface: SurfaceBoard, Locator: "wf-1"}, report(t))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := Observed(AxisModel, "every node names one model", ev)
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := Inferred(AxisMemory, "a per-session store, never pruned", ev, "claude-opus-5", "sha256:1")
	if err != nil {
		t.Fatal(err)
	}
	refused, err := Refused(AxisGraph, RefusalAnalysis, "topology is not assessed by this build", ev)
	if err != nil {
		t.Fatal(err)
	}

	a := nine(t, map[Axis]Finding{
		AxisTools: measured, AxisModel: observed, AxisMemory: inferred, AxisGraph: refused,
	})
	got := a.Ordered()

	want := []Axis{AxisTools, AxisModel, AxisMemory}
	for i, axis := range want {
		if got[i].Axis() != axis {
			t.Fatalf("position %d is %s, want %s — the ladder is measured, observed, inferred, "+
				"then absence:\n%s", i, got[i].Axis(), axis, describe(got))
		}
	}
	if last := got[len(got)-1]; last.Axis() != AxisGraph {
		t.Fatalf("refused is not last, %s is:\n%s", last.Axis(), describe(got))
	}
}

// TestOrderingIsStableAcrossRuns is FR15 as a property of the RENDERING, not only of the findings.
// "Identical findings" has to mean identical output, and a map iteration inside a rung is exactly the
// instability that makes a byte-comparison fence flap.
func TestOrderingIsStableAcrossRuns(t *testing.T) {
	a := nine(t, nil)
	first := describe(a.Ordered())
	for i := 0; i < 20; i++ {
		if got := describe(a.Ordered()); got != first {
			t.Fatalf("ordering is not stable:\n%s\n vs \n%s", first, got)
		}
	}
}

// TestTheWireFormIsCanonicalWhateverOrderTheFindingsArrive is FR15 at the boundary where "identical"
// is actually measured.
//
// 🔴 This test exists because the live Postgres proof caught what every struct-level test missed: a
// freshly produced assessment carried its findings in report order, one read back carried them
// alphabetically, every field matched, and the two DOCUMENTS differed. The guarantee held for a reader
// comparing fields and failed for anyone diffing the export.
func TestTheWireFormIsCanonicalWhateverOrderTheFindingsArrive(t *testing.T) {
	a := nine(t, nil)

	shuffled := nine(t, nil)
	// Reverse — the most different order there is, and one a `ORDER BY` could plausibly produce.
	for i, j := 0, len(shuffled.Findings)-1; i < j; i, j = i+1, j-1 {
		shuffled.Findings[i], shuffled.Findings[j] = shuffled.Findings[j], shuffled.Findings[i]
	}

	first, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("two assessments with the same findings in different SLICE order produced different "+
			"documents. FR15's guarantee is consumed as an export, and a diff is how anyone checks it.\n"+
			" a: %s\n b: %s", first, second)
	}

	// And the order is the AXIS report order, not evidence strength — which changes as findings change
	// state and would reorder the document for reasons unrelated to the axis that moved.
	var back Assessment
	if err := json.Unmarshal(first, &back); err != nil {
		t.Fatal(err)
	}
	for i, axis := range Axes() {
		if back.Findings[i].Axis() != axis {
			t.Fatalf("wire position %d is %s, want %s", i, back.Findings[i].Axis(), axis)
		}
	}
}

// TestTallyIsADistributionAndNotAReduction is ruling R4 stated positively: the manager gets nine
// numbers that sum to nine, and no arithmetic over them orders one repository against another.
func TestTallyIsADistributionAndNotAReduction(t *testing.T) {
	ev := EvidenceRef{Surface: SurfaceGraph, Locator: "wf-1"}
	inferred, err := Inferred(AxisMemory, "a per-session store", ev, "claude-opus-5", "sha256:1")
	if err != nil {
		t.Fatal(err)
	}
	a := nine(t, map[Axis]Finding{AxisMemory: inferred})
	got := a.Tally()
	if sum := got.Measured + got.Observed + got.NotMeasured + got.Refused; sum != len(Axes()) {
		t.Fatalf("the tally sums to %d, not %d — a state is uncounted", sum, len(Axes()))
	}
	if got.Inferred != 1 {
		t.Fatalf("the inferred count is %d, want 1; it cuts ACROSS the states and is not part of the sum", got.Inferred)
	}
}

// TestPartialIsCarriedByTheReport is §7.3: a partial report is never presented as complete, and the
// only defence is that the report says so rather than a reader noticing.
func TestPartialIsCarriedByTheReport(t *testing.T) {
	a := nine(t, nil)
	if a.Partial() {
		t.Fatal("a report that never hit its cap reports itself partial")
	}
	exhausted, err := NotMeasured(AxisTools, MissingBudgetExhausted,
		"the assessment reached its spend cap before this axis was inferred",
		EvidenceRef{Surface: SurfaceGraph, Locator: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	a = nine(t, map[Axis]Finding{AxisTools: exhausted})
	if !a.Partial() {
		t.Fatal("a report carrying a budget_exhausted finding does not report itself partial")
	}
}

// TestTheTwoP34AxesAreNamedRatherThanDiscovered is task 9.2. The deferral has to be a fact the code
// can be asked about, so the day P34 lands it is removed in one place.
func TestTheTwoP34AxesAreNamedRatherThanDiscovered(t *testing.T) {
	pending := map[Axis]bool{}
	for _, a := range Axes() {
		if a.P34Pending() {
			pending[a] = true
		}
	}
	if len(pending) != 2 || !pending[AxisLoop] || !pending[AxisGraph] {
		t.Fatalf("the axes awaiting P34 are %v, want exactly loop and graph", pending)
	}
}

func TestAnAssessmentMustNameItsRevisionAndConfig(t *testing.T) {
	a := nine(t, nil)
	a.SourceRevision = ""
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "no source revision") {
		t.Fatalf("an assessment of no particular code validated: %v", err)
	}
	a = nine(t, nil)
	a.AgentConfigHash = ""
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "no agent config hash") {
		t.Fatalf("an unattributable assessment validated: %v", err)
	}
}

func describe(fs []Finding) string {
	var b strings.Builder
	for i, f := range fs {
		b.WriteString(strings.TrimSpace(strings.Join([]string{
			"  ", string(rune('0' + i)), " ", string(f.Axis()), " ", string(f.State()), "/", string(f.Origin()),
		}, "")))
		b.WriteString("\n")
	}
	return b.String()
}
