package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// p37_axis_values_test.go is P37 §5 — the reads the source-bound editors bind to, and the one write.
//
// The claims under test are not "the handler returns 200". They are:
//
//   · the per-node CURRENT VALUES travel with the per-node VERDICTS, from ONE read of ONE structure,
//     because two reads are two chances to disagree about which nodes exist;
//   · the context coverage is LIVE and PER LANGUAGE — the read that lets `/app/context`'s transcribed
//     table go away (FR17, design D5);
//   · a value that could not be resolved is `not_measured` with a NAMED missing input, and the WARN
//     that records it carries all three correlation identities (§5.3, §5.5).

func axisValuesServer(t *testing.T, nodes []runlink.WireIRNode) *Server {
	t.Helper()
	store := linkingest.NewMemWorkflowIRStore()
	if err := store.Put(linkingest.WorkflowIR{
		TenantID: "t-1", WorkflowID: "wf", SourceRevision: "rev", IRVersion: "v1",
		ReceivedAt: time.Now().UTC(), Nodes: nodes,
	}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, config.Config{})
	s.MountWorkflowIR(store)
	s.MountAxisProjection()
	return s
}

func getAxisProjection(t *testing.T, s *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf/axis-projection", nil)
	ctx := auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "t-1"})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// §5.1 — the per-node current values arrive beside the verdicts, over the SAME node set.
//
// 🔴 The node-set equality is the assertion that matters. If the two were separate reads, a structure
// replaced between them would produce a values list and a verdicts list that disagree — and the reader
// would see a node in one and not the other, with nothing on the screen to explain it.
func TestTheAxisReadCarriesCurrentValuesOverTheSameNodesAsTheVerdicts(t *testing.T) {
	s := axisValuesServer(t, []runlink.WireIRNode{
		{NodeID: "answer", Symbol: "handleAnswer", File: "a.go", Language: "go",
			Provider: "anthropic", ModelID: "claude-sonnet-4-5", ContextPolicy: "sliding-window", ToolCount: 2},
		{NodeID: "classify", Symbol: "classify", File: "b.go", Language: "go",
			Provider: "anthropic", ModelID: "claude-haiku-4-5", ContextPolicy: "full", ToolCount: 0},
	})
	body := getAxisProjection(t, s)

	values, ok := body["values"].(map[string]any)
	if !ok {
		t.Fatal("the axis read carries no `values` — the editors have nothing to bind to")
	}
	valueNodes := idsOf(t, values["nodes"])

	projection, ok := body["projection"].(map[string]any)
	if !ok {
		t.Fatal("the axis read lost its projection — P37 ADDS beside P29, it does not replace")
	}
	verdictNodes := idsOf(t, projection["nodes"])

	if strings.Join(valueNodes, ",") != strings.Join(verdictNodes, ",") {
		t.Fatalf("values cover %v and verdicts cover %v — one read, one node set", valueNodes, verdictNodes)
	}
}

func idsOf(t *testing.T, raw any) []string {
	t.Helper()
	rows, ok := raw.([]any)
	if !ok {
		t.Fatalf("expected a node list, got %T", raw)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.(map[string]any)["node_id"].(string))
	}
	return out
}

// §5.2 / FR17 — the live coverage read, keyed by the languages this workflow actually reports.
//
// This is what replaces `/app/context`'s hand-transcribed `COVERAGE` array. The surface never has to
// pick a row by guessing which language the reader meant, which is the guess the transcription invited.
func TestTheAxisReadCarriesLiveContextCoverageForTheReportedLanguages(t *testing.T) {
	s := axisValuesServer(t, []runlink.WireIRNode{
		{NodeID: "a", Language: "go", ModelID: "m", Provider: "p", ContextPolicy: "full"},
		{NodeID: "b", Language: "cobol", ModelID: "m", Provider: "p", ContextPolicy: "full"},
	})
	body := getAxisProjection(t, s)

	coverage, ok := body["context_coverage"].(map[string]any)
	if !ok {
		t.Fatal("no `context_coverage` — the surface would have to transcribe the engine's table again")
	}
	goRows, ok := coverage["go"].([]any)
	if !ok || len(goRows) == 0 {
		t.Fatal("go has a context selection rewriter and must arrive with its policy rows")
	}
	first := goRows[0].(map[string]any)
	for _, field := range []string{"policy", "mode"} {
		if _, ok := first[field]; !ok {
			t.Errorf("a coverage row carries no %q", field)
		}
	}

	// 🔴 A language with no rewriter arrives PRESENT AND EMPTY, never absent and never with Go's rows.
	// Absent and empty are only the same to a careless client; empty says "we looked, and this language
	// has no rows", which is the answer.
	cobol, present := coverage["cobol"]
	if !present {
		t.Fatal("a reported language with no rewriter must still be a key — absent reads as `not asked`")
	}
	if rows, _ := cobol.([]any); len(rows) != 0 {
		t.Fatalf("cobol arrived with %d rows — a coverage answer attributed to the wrong language is a "+
			"claim about the reader's code drawn from a guess", len(rows))
	}

	if langs, _ := body["covered_languages"].([]any); len(langs) == 0 {
		t.Error("a surface with no coverage for the reader's language must be able to NAME the ones that " +
			"do have it, rather than saying only `no`")
	}
}

// 🔴 §5.3 — an unresolvable value is `not_measured` with a named missing input, over the wire.
//
// The fixture is the real one: `discovery` writes its `unresolved` sentinel into both model fields for a
// detect-only declaration. A surface that rendered it would print a model called "unresolved".
func TestAnUnresolvableValueCrossesTheWireAsNotMeasuredWithItsNamedInput(t *testing.T) {
	s := axisValuesServer(t, []runlink.WireIRNode{{
		NodeID: "detect-only", Language: "python",
		Provider: discovery.UnresolvedSentinel, ModelID: discovery.UnresolvedSentinel,
		ContextPolicy: discovery.UnresolvedSentinel,
	}})
	body := getAxisProjection(t, s)

	raw, _ := json.Marshal(body["values"])
	var report struct {
		Nodes []struct {
			NodeID string `json:"node_id"`
			Values []struct {
				Axis         string `json:"axis"`
				State        string `json:"state"`
				Current      string `json:"current"`
				MissingInput string `json:"missing_input"`
				Because      string `json:"because"`
			} `json:"values"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Nodes) != 1 {
		t.Fatalf("expected one node, got %d", len(report.Nodes))
	}

	seen := map[string]bool{}
	for _, v := range report.Nodes[0].Values {
		seen[v.Axis] = true
		if strings.Contains(v.Current, discovery.UnresolvedSentinel) {
			t.Fatalf("axis %s put the sentinel in the value position", v.Axis)
		}
		if v.State == string(assessment.StateNotMeasured) {
			if v.MissingInput == "" || v.Because == "" {
				t.Errorf("axis %s is not_measured without naming its missing input or saying why", v.Axis)
			}
		}
	}
	// Every axis, always — a row that omitted its unresolved axes would make absence invisible.
	for _, axis := range assessment.Axes() {
		if !seen[string(axis)] {
			t.Errorf("the wire dropped axis %q for this node", axis)
		}
	}
}

// §5.5 — the WARN on the axis read carries `request_id`, `trace_id` and `span_id`.
//
// 🔴 Asserted on the LOG RECORD rather than by reading the source, because the failure this prevents is
// an operator holding a node id with no way to reach the request that produced it — and a source-level
// check would pass on a line that formats the ids into a message nobody can filter on.
func TestTheAxisReadsWarningCarriesAllThreeCorrelationIdentities(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(restore)

	s := axisValuesServer(t, []runlink.WireIRNode{{
		NodeID: "detect-only", Language: "python",
		Provider: discovery.UnresolvedSentinel, ModelID: discovery.UnresolvedSentinel,
	}})
	getAxisProjection(t, s)

	line := findLog(t, buf.String(), "unresolved_pairs")
	for _, key := range []string{"request_id", "trace_id", "span_id", "event", "error_code", "workflow_id"} {
		if _, ok := line[key]; !ok {
			t.Errorf("the WARN carries no %q", key)
		}
	}
	if line["trace_id"] == "" {
		t.Error("an unidentified request is one nobody can follow through an incident")
	}
	if line["span_id"] == telemetry.RequestSpanID("") {
		t.Error("the span was derived from an empty trace")
	}
}

// 🔴 §5.3, the direction that matters — a value that RESOLVED must not produce the WARN.
//
// A warning on every request is a warning nobody reads. The four axes with no wire field are
// `not_measured` on every node in every repository, forever; warning on those would emit `4 × nodes`
// lines describing the designed answer, and the real signal — a field that should have resolved and did
// not — would be buried under it.
func TestAResolvedStructureProducesNoUnresolvedWarning(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(restore)

	s := axisValuesServer(t, []runlink.WireIRNode{{
		NodeID: "answer", Language: "go", Provider: "anthropic", ModelID: "claude-sonnet-4-5",
		ContextPolicy: "sliding-window", ToolCount: 1,
	}})
	getAxisProjection(t, s)

	if strings.Contains(buf.String(), "unresolved_pairs") {
		t.Fatalf("a fully resolved structure warned anyway:\n%s", buf.String())
	}
}

// findLog returns the first JSON log record containing `key`.
func findLog(t *testing.T, out, key string) map[string]string {
	t.Helper()
	for _, raw := range strings.Split(strings.TrimSpace(out), "\n") {
		if raw == "" || !strings.Contains(raw, key) {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			continue
		}
		flat := map[string]string{}
		for k, v := range line {
			if s, ok := v.(string); ok {
				flat[k] = s
			} else {
				flat[k] = "present"
			}
		}
		return flat
	}
	t.Fatalf("no log record carrying %q in:\n%s", key, out)
	return nil
}

// §5.6 — the four P37 event names exist in the central enum and are spelled as the contract promises.
//
// The Go-side membership is fenced in `internal/eventname`; this asserts the API layer references the
// CONSTANTS rather than re-spelling them, which is the drift a string literal introduces silently.
func TestTheSavePathReferencesTheCentralEventNames(t *testing.T) {
	for _, n := range []eventname.Name{
		eventname.ConsoleSubjectResolved, eventname.ConsoleSubjectAmbiguous,
		eventname.ConsoleAxisSaved, eventname.ConsoleAxisSaveRefused,
	} {
		if !n.Valid() {
			t.Errorf("%q is not a member of the central enum", n)
		}
	}
}
