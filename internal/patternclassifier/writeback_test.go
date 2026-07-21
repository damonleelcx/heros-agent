package patternclassifier

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

func classifyComposite(t *testing.T) (*discovery.IR, Result) {
	t.Helper()
	f := fxComposite()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	return f.ir, res
}

// Task 6.1: labels land in the P0-reserved field — region labels on their subgraph, node labels on
// their node — and the write is additive.
func TestWriteBackPopulatesTheReservedField(t *testing.T) {
	ir, res := classifyComposite(t)
	labelled, err := WriteBack(ir, res)
	if err != nil {
		t.Fatal(err)
	}
	if labelled == ir {
		t.Fatal("WriteBack must not hand back the same pointer")
	}
	if len(ir.Subgraphs) != 0 {
		t.Error("WriteBack must not mutate the caller's IR")
	}
	for _, n := range ir.Nodes {
		if len(n.PatternLabels) != 0 {
			t.Error("WriteBack must not mutate the caller's nodes")
		}
	}

	// The routing region's label is on the subgraph.
	routing := SubgraphIDFor([]string{"n_router", "n_billing", "n_tech"})
	var found *discovery.IRSubgraph
	for i := range labelled.Subgraphs {
		if labelled.Subgraphs[i].SubgraphID == routing {
			found = &labelled.Subgraphs[i]
		}
	}
	if found == nil || len(found.PatternLabels) != 1 || found.PatternLabels[0].Pattern != string(Routing) {
		t.Fatalf("the Routing label is not on its subgraph: %+v", labelled.Subgraphs)
	}
	if found.PatternLabels[0].Source != "rule" || found.PatternLabels[0].TaxonomyVersion != TaxonomyVersion {
		t.Errorf("provenance was not written: %+v", found.PatternLabels[0])
	}

	// The Tool Use capability is on the NODE, inside the routing branch.
	for _, n := range labelled.Nodes {
		if n.NodeID != "n_tech" {
			continue
		}
		if len(n.PatternLabels) != 1 || n.PatternLabels[0].Pattern != string(ToolUse) {
			t.Errorf("n_tech should carry exactly the Tool Use label, got %+v", n.PatternLabels)
		}
	}
}

// Task 6.1: labelled and unlabelled IRs BOTH validate at the same ir_version MAJOR, and an
// unlabelled IR serialises exactly as it did before P3.5 existed.
func TestLabelledAndUnlabelledIRShareTheSameMajor(t *testing.T) {
	ir, res := classifyComposite(t)

	unlabelledJSON, _ := json.Marshal(ir)
	if strings.Contains(string(unlabelledJSON), "pattern_labels") || strings.Contains(string(unlabelledJSON), "subgraphs") {
		t.Fatal("an unlabelled IR must not serialise the new fields at all")
	}
	if ir.IRVersion != discovery.IRVersion {
		t.Errorf("unlabelled ir_version = %q, want %q", ir.IRVersion, discovery.IRVersion)
	}

	labelled, err := WriteBack(ir, res)
	if err != nil {
		t.Fatal(err)
	}
	if labelled.IRVersion != discovery.IRVersionPatternLabels {
		t.Errorf("labelled ir_version = %q, want %q", labelled.IRVersion, discovery.IRVersionPatternLabels)
	}
	if majorOf(labelled.IRVersion) != majorOf(ir.IRVersion) {
		t.Fatalf("writing labels bumped MAJOR: %q -> %q; the write must be additive",
			ir.IRVersion, labelled.IRVersion)
	}

	// Writing NOTHING must not bump anything: an IR that gained no labels keeps saying what it said.
	untouched, err := WriteBack(ir, Result{})
	if err != nil {
		t.Fatal(err)
	}
	if untouched.IRVersion != discovery.IRVersion {
		t.Errorf("an IR that gained no labels must not change version, got %q", untouched.IRVersion)
	}
}

func majorOf(v string) string { return strings.SplitN(v, ".", 2)[0] }

// Task 6.2: a PRE-P3.5 consumer — a struct that knows nothing of pattern_labels or subgraphs —
// still parses a labelled IR and still reads every field it cares about. This is the forward-compat
// PARSE contract from P0, and it is asserted against a type that genuinely lacks the new fields
// rather than against the current struct, which would prove nothing.
func TestPreP35ConsumerStillParsesALabelledIR(t *testing.T) {
	ir, res := classifyComposite(t)
	labelled, err := WriteBack(ir, res)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(labelled)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), "pattern_labels") {
		t.Fatal("the fixture must actually carry labels for this test to mean anything")
	}

	// A consumer frozen at the P0 shape.
	type preP35Node struct {
		NodeID      string   `json:"node_id"`
		Kind        string   `json:"kind"`
		ToolsSkills []string `json:"tools_skills"`
	}
	type preP35IR struct {
		IRVersion string       `json:"ir_version"`
		Nodes     []preP35Node `json:"nodes"`
		Edges     []struct {
			From string `json:"from_node_id"`
			To   string `json:"to_node_id"`
			Kind string `json:"kind"`
		} `json:"edges"`
	}
	var old preP35IR
	if err := json.Unmarshal(blob, &old); err != nil {
		t.Fatalf("a pre-P3.5 consumer failed to parse a labelled IR: %v", err)
	}
	if len(old.Nodes) != len(labelled.Nodes) || len(old.Edges) != len(labelled.Edges) {
		t.Fatalf("the old consumer lost data: %d nodes / %d edges", len(old.Nodes), len(old.Edges))
	}
	if majorOf(old.IRVersion) != "1" {
		t.Errorf("a consumer pinned to MAJOR 1 would reject %q", old.IRVersion)
	}
	for _, n := range old.Nodes {
		if n.Kind != "static_definition" {
			t.Errorf("node %q: the old consumer read a wrong kind %q", n.NodeID, n.Kind)
		}
	}
}

// The two representations of a label — the classifier's Label and the IR's wire struct — must
// serialise identically. They live in different packages so the IR contract does not depend on its
// producer; this is the fence that stops that split becoming two different formats.
func TestLabelWireShapeDoesNotDrift(t *testing.T) {
	l := Label{
		Pattern: Routing, Confidence: 0.95, Source: SourceRule, SubgraphRef: "sg_x",
		DetectorID: "routing.v1", TaxonomyVersion: TaxonomyVersion,
	}
	wire := discovery.IRPatternLabel{
		Pattern: string(l.Pattern), Confidence: l.Confidence, Source: string(l.Source),
		SubgraphRef: l.SubgraphRef, DetectorID: l.DetectorID, TaxonomyVersion: l.TaxonomyVersion,
	}
	a, _ := json.Marshal(l)
	b, _ := json.Marshal(wire)
	if string(a) != string(b) {
		t.Fatalf("the classifier and IR label shapes have drifted:\n  classifier: %s\n  ir:         %s", a, b)
	}
}

// Round trip: what WriteBack writes, ReadLabels reads back unchanged. Without this, the UI and P4
// would be reading a shape nothing guarantees.
func TestWriteBackReadLabelsRoundTrip(t *testing.T) {
	ir, res := classifyComposite(t)
	labelled, err := WriteBack(ir, res)
	if err != nil {
		t.Fatal(err)
	}
	// Through JSON, so the round trip is over the WIRE, not over Go structs that never left memory.
	blob, _ := json.Marshal(labelled)
	var reparsed discovery.IR
	if err := json.Unmarshal(blob, &reparsed); err != nil {
		t.Fatal(err)
	}
	got, diags, err := ReadLabels(&reparsed)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Fatalf("a freshly written IR must read back clean: %v", diags)
	}
	if len(got) != len(res.Labels) {
		t.Fatalf("round trip lost labels: wrote %d, read %d", len(res.Labels), len(got))
	}
	want, _ := json.Marshal(res.Labels)
	have, _ := json.Marshal(got)
	if string(want) != string(have) {
		t.Errorf("round trip is not lossless:\n  wrote: %s\n  read:  %s", want, have)
	}
}

// A label that would be invalid must never reach the document, even if a caller hand-assembles a
// Result. The write is the last gate and it must hold.
func TestWriteBackRefusesAnInvalidLabel(t *testing.T) {
	ir, _ := classifyComposite(t)
	bad := Result{Labels: []Label{{
		Pattern: "self_healing_swarm", Confidence: 0.9, Source: SourceRule,
		SubgraphRef: "n_router", DetectorID: "d", TaxonomyVersion: TaxonomyVersion,
	}}}
	if _, err := WriteBack(ir, bad); err == nil {
		t.Fatal("an out-of-taxonomy label must not be writable to the IR")
	}
	dangling := Result{Labels: []Label{{
		Pattern: Routing, Confidence: 0.9, Source: SourceRule,
		SubgraphRef: "sg_does_not_exist", DetectorID: "d", TaxonomyVersion: TaxonomyVersion,
	}}}
	if _, err := WriteBack(ir, dangling); err == nil {
		t.Fatal("a label referencing no known region must not be writable")
	}
}

// Re-classifying REPLACES labels; it never accumulates them. Otherwise a workflow re-run twice would
// carry each label twice and every count downstream would be wrong.
func TestWriteBackIsIdempotent(t *testing.T) {
	ir, res := classifyComposite(t)
	once, err := WriteBack(ir, res)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := WriteBack(once, res)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(once)
	b, _ := json.Marshal(twice)
	if string(a) != string(b) {
		t.Errorf("writing the same result twice changed the document:\n%s\n%s", a, b)
	}
}

// A stored label from an older, looser era ({pattern, confidence} only, free-text name) is REPORTED
// on read, not silently dropped and not silently accepted.
func TestReadLabelsReportsAnInvalidStoredLabel(t *testing.T) {
	ir := buildIR([]discovery.IRNode{node("n_a")}, nil)
	ir.Nodes[0].PatternLabels = []discovery.IRPatternLabel{{Pattern: "router", Confidence: 0.82}}
	got, diags, err := ReadLabels(ir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("an out-of-taxonomy stored label must not be returned as valid: %+v", got)
	}
	if len(diags) != 1 || diags[0].RawPattern != "router" {
		t.Fatalf("the invalid stored label must be reported: %v", diags)
	}
}
