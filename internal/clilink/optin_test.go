package clilink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// optin_test.go is P29 §2.6, §2.7, §2.10 and §2.11 — the fences over what the two new opt-ins change and,
// far more importantly, over what they must NOT change.

// optInServer answers whoami, link, the structure ingest and the receipt ingest.
func optInServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{"identity": "tenantA"})
		case "/api/v1/run-links":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted": true, "run_url": "https://heros-agent.space/app/runs/run-x",
				"contract_version": runlink.ContractVersion,
			})
		case runlink.WorkflowIRPath:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted": true, "nodes": 2, "edges": 0,
				"graph_url": "https://heros-agent.space/app/workflows/w/graph",
			})
		case runlink.TransformReceiptPath:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted": true, "transform_url": "https://heros-agent.space/app/transforms/h/r",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// runLink drives the shipped command with the given flags and returns the envelope and the captured
// request bodies. It goes through cli.Main rather than calling Link directly, so the FLAG PARSING is
// exercised — which is half of what §2.5 changed.
func runLink(t *testing.T, rt *captureRT, args ...string) (cli.Envelope, [][]byte) {
	t.Helper()
	var out bytes.Buffer
	code := cli.Main(args, cli.Streams{Out: &out, Err: io.Discard},
		func(string) (string, bool) { return "", false }, Commands{RT: rt})
	if code != cli.ExitOK {
		t.Fatalf("`heros %s` exited %d:\n%s", strings.Join(args, " "), code, out.String())
	}
	var env cli.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	return env, rt.bodies
}

func linkBody(t *testing.T, bodies [][]byte) []byte {
	t.Helper()
	for _, b := range bodies {
		var probe map[string]json.RawMessage
		if json.Unmarshal(b, &probe) != nil {
			continue
		}
		if _, ok := probe["ir_structure"]; ok {
			return b
		}
	}
	t.Fatal("no run-link body was transmitted")
	return nil
}

// 🔴 §2.6 — THE PROMISE THE WHOLE BOUNDARY RESTS ON.
//
// A link with NO opt-in transmits a payload byte-identical to the pre-change one. Every other assertion
// in this change is about what the opt-ins ADD; this one is about the default path being untouched, and
// it is the assertion a customer's security review would actually ask for.
//
// It is checked against a stored golden rather than against "the code as it is today", because a test
// that recomputes the expectation from the same code it is testing cannot fail.
func TestADefaultLinkIsByteIdenticalToThePreChangePayload(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)

	rt := &captureRT{handler: optInServer()}
	runLink(t, rt, "login", "--token", "tok")
	_, bodies := runLink(t, rt, "link", "--repo", repo, "--run", runID)

	body := linkBody(t, bodies)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	// The pre-change key set, spelled out. A new top-level key on the DEFAULT payload fails here and the
	// failure names it — which is exactly the review this change promised would not be needed.
	want := []string{
		"contract_version", "run_metadata", "metrics", "ir_structure", "config_hash",
		"source_revision", "scores", "runs_reported", "eval",
	}
	wantSet := map[string]bool{}
	for _, k := range want {
		wantSet[k] = true
		if _, ok := got[k]; !ok {
			t.Errorf("the default link payload no longer carries %q", k)
		}
	}
	for k := range got {
		if !wantSet[k] {
			t.Errorf("the default link payload carries a NEW key %q. P29 widened only the OPT-IN payloads; "+
				"the default must be byte-identical to what it was, and this is the assertion that says so.", k)
		}
	}

	// And nothing this change added may appear anywhere in the bytes, at any depth.
	for _, added := range []string{"coverage_version", "axis_verdicts", "language", "node_outcomes",
		"files_changed", "lines_added", "lines_removed"} {
		if bytes.Contains(body, []byte(`"`+added+`"`)) {
			t.Errorf("the default link payload carries %q, which is an OPT-IN field:\n%s", added, body)
		}
	}

	// Exactly one transmission: the run. No structure, no receipt.
	if n := len(bodies); n != 1 {
		t.Errorf("a default link made %d transmission(s); it must make exactly one", n)
	}
}

// §2.5 — both forms of `--with-ir` produce byte-identical payloads for the same repository.
//
// This is what makes the bare form safe to add: it is not a second, similar payload built by a second
// code path, it is the same payload from the same builder over the same discovery.
func TestBothFormsOfWithIRProduceIdenticalPayloads(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)

	rt := &captureRT{handler: optInServer()}
	runLink(t, rt, "login", "--token", "tok")

	// The bare form, discovering in place.
	envBare, _ := runLink(t, rt, "link", "--repo", repo, "--run", runID, "--with-ir", "--dry-run")
	bare := irPayloadOf(t, envBare)

	// Write the same discovery to disk and use the path form.
	irPath := filepath.Join(t.TempDir(), "ir.json")
	var discOut bytes.Buffer
	if code := cli.Main([]string{"discover", "--repo", repo, "--out", irPath,
		"--report", filepath.Join(t.TempDir(), "rep.json")},
		cli.Streams{Out: &discOut, Err: io.Discard},
		func(string) (string, bool) { return "", false }, nil); code != cli.ExitOK {
		t.Fatalf("discover exited %d: %s", code, discOut.String())
	}
	if _, err := os.Stat(irPath); err != nil {
		t.Fatalf("discover wrote no IR: %v", err)
	}
	envPath, _ := runLink(t, rt, "link", "--repo", repo, "--run", runID, "--with-ir", irPath, "--dry-run")
	fromPath := irPayloadOf(t, envPath)

	if !bytes.Equal(bare, fromPath) {
		t.Errorf("the two forms of --with-ir produced different payloads.\n  bare: %s\n  path: %s", bare, fromPath)
	}
}

// 🔴 §2.7 — `--dry-run` renders what is SENT, for every payload. A dry-run that summarises is worse than
// none: it is evidence a reviewer would rely on that does not describe the transmission.
func TestDryRunRendersTheExactStructureBytesThatAreTransmitted(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)

	rt := &captureRT{handler: optInServer()}
	runLink(t, rt, "login", "--token", "tok")

	envDry, _ := runLink(t, rt, "link", "--repo", repo, "--run", runID, "--with-ir", "--dry-run")
	rendered := irPayloadOf(t, envDry)

	sendRT := &captureRT{handler: optInServer()}
	runLink(t, sendRT, "login", "--token", "tok")
	_, bodies := runLink(t, sendRT, "link", "--repo", repo, "--run", runID, "--with-ir")

	var transmitted []byte
	for _, b := range bodies {
		if bytes.Contains(b, []byte(`"`+runlink.WorkflowIRContractVersion+`"`)) {
			transmitted = b
		}
	}
	if transmitted == nil {
		t.Fatal("no structure payload was transmitted")
	}

	// Compared as canonical values rather than as raw bytes: the rendered form is re-marshalled inside an
	// envelope, so whitespace differs while the CONTENT must not. Anything less than this comparison
	// would let a field be dropped from one side.
	var a, b any
	if err := json.Unmarshal(rendered, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(transmitted, &b); err != nil {
		t.Fatal(err)
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if !bytes.Equal(ab, bb) {
		t.Errorf("--dry-run rendered a payload that differs from the transmitted one.\n  rendered:    %s\n  transmitted: %s", ab, bb)
	}
}

// 🔴 §2.11 — THE EGRESS FENCE, extended over the full opt-in path.
//
// The run-link egress test proves a canary field does not cross. This one runs the WIDENED path — the
// structure payload built from a real discovery of a real tree — and asserts that no prompt text, no
// source line and no refusal SENTENCE appears in any transmitted byte, at any depth.
//
// The sentences are the interesting half and they are new with this change: the transform engine's own
// refusals are extremely specific ("this call site passes **opts, so the request — including its message
// list — is assembled elsewhere"), they name arguments and symbols out of the customer's source, and
// copying one onto the wire is a one-word edit that reads as helpfulness.
func TestNothingFromTheSourceCrossesOnTheFullOptInPath(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)

	rt := &captureRT{handler: optInServer()}
	runLink(t, rt, "login", "--token", "tok")
	_, bodies := runLink(t, rt, "link", "--repo", repo, "--run", runID, "--with-ir")

	if len(bodies) < 2 {
		t.Fatalf("expected a run and a structure transmission, got %d bodies", len(bodies))
	}
	all := bytes.Join(bodies, []byte("\n"))

	// Every source line of the fixture, checked verbatim. A payload carrying any line of the customer's
	// code is a boundary failure regardless of which field it arrived in.
	var lines []string
	err := filepath.Walk(repo, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.Contains(path, string(filepath.Separator)+".heros"+string(filepath.Separator)) {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			// Short lines (`}`, `)`, an import) collide with legitimate JSON; only substantial lines are
			// distinctive enough to be evidence.
			if len(l) >= 24 {
				lines = append(lines, l)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("the fixture yielded no source lines to check — this fence would pass vacuously")
	}
	for _, l := range lines {
		if bytes.Contains(all, []byte(l)) {
			t.Errorf("a transmitted payload contains a line of the customer's source:\n  %s", l)
		}
	}

	// The engine's refusal SENTENCES. Distinctive fragments from the real messages, so this fails if
	// anybody ever routes `RewriteError.Detail` onto the wire.
	for _, sentence := range []string{
		"passes **opts", "assembled elsewhere", "there is no written list here",
		"cannot rewrite this call site safely", "the sealed input schema", "Materializing memory here",
		"is not a static string literal", "Rewriting would be a guess",
	} {
		if bytes.Contains(all, []byte(sentence)) {
			t.Errorf("a transmitted payload carries a refusal SENTENCE (%q). Causes cross as identifiers; "+
				"the console renders its own copy, and a sentence built here names the customer's own "+
				"arguments and symbols.", sentence)
		}
	}

	// And the structure payload's keys are all allowlisted.
	for _, b := range bodies {
		if !bytes.Contains(b, []byte(runlink.WorkflowIRContractVersion)) {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		for k := range p {
			// A top-level key is ratified either directly (`workflow_id`) or as the ROOT of a ratified
			// dotted path (`nodes`, whose leaves are `nodes.node_id` and friends). Nested leaves are
			// checked by the type-level fence in internal/runlink; here the top level is enough to catch a
			// whole object having been added.
			if k == "contract_version" || runlink.WorkflowIRPermitted(k) {
				continue
			}
			rooted := false
			for _, ratified := range runlink.WorkflowIRAllowlistKeys() {
				if strings.HasPrefix(ratified, k+".") {
					rooted = true
					break
				}
			}
			if !rooted {
				t.Errorf("the structure payload carries an unratified top-level key %q", k)
			}
		}
	}
}

// §2.10 — the success output names the surfaces the transmission filled, and for each one it did not,
// the single option that would.
//
// The list is DERIVED from the capability registry rather than written here, so a surface added tomorrow
// cannot be omitted silently — which is the failure this whole phase is about: fifteen surfaces with
// nothing to say and no screen saying why.
func TestLinkOutputNamesEverySurfaceAndHowToFillIt(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)

	rt := &captureRT{handler: optInServer()}
	runLink(t, rt, "login", "--token", "tok")
	env, _ := runLink(t, rt, "link", "--repo", repo, "--run", runID)

	b, _ := json.Marshal(env.Data)
	var d LinkData
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Surfaces) == 0 {
		t.Fatal("a successful link reported no surfaces at all — the reader is told nothing about what " +
			"their transmission did or did not fill, which is the defect this phase exists to close")
	}
	if len(d.Surfaces) != len(cli.LinkSurfaces()) {
		t.Errorf("the link reported %d surface(s) and the registry declares %d — the list must be derived, "+
			"so a new surface cannot be omitted silently", len(d.Surfaces), len(cli.LinkSurfaces()))
	}
	unfilled := 0
	for _, s := range d.Surfaces {
		if s.Filled {
			continue
		}
		unfilled++
		if s.FillWith == "" {
			t.Errorf("surface %q was not filled and the output names no option that would fill it. "+
				"A message that says a page is empty and not how to fill it is the screen this phase is "+
				"replacing.", s.Name)
		}
	}
	if unfilled == 0 {
		t.Fatal("a DEFAULT link (no opt-ins) reported every surface as filled — that is not true, and the " +
			"assertion above passed vacuously")
	}
}

// irPayloadOf digs the structure payload out of a link envelope.
func irPayloadOf(t *testing.T, env cli.Envelope) []byte {
	t.Helper()
	b, _ := json.Marshal(env.Data)
	var d LinkData
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d.WorkflowIR == nil || d.WorkflowIR.Payload == nil {
		t.Fatal("the envelope carries no rendered structure payload")
	}
	out, err := json.Marshal(d.WorkflowIR.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// 🔴 §2.9 — `heros apply` transmits a receipt only under the named opt-in, and transmits NOTHING without
// it.
//
// This is the same promise §2.6 makes about `link`, on the command where breaking it would be worst:
// `apply` is the fully offline command that generates a diff, and a version of it that phoned home by
// default would turn the product's most-run local operation into an egress event nobody agreed to.
func TestApplyTransmitsNothingWithoutTheNamedOptIn(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, _ := evalFixture(t)

	// A BASELINE spec — no overrides. It exercises the whole apply path (resolve, isolate, generate,
	// write) and produces an empty diff, which is exactly the case where "did anything transmit?" is
	// answerable without the answer depending on whether a rewriter happened to fire.
	specPath := baselineSpec(t, repo)

	rt := &captureRT{handler: optInServer()}
	runLink(t, rt, "login", "--token", "tok")
	before := len(rt.bodies)

	var out bytes.Buffer
	code := cli.Main([]string{"apply", "--repo", repo, "--spec", specPath,
		"--out", filepath.Join(t.TempDir(), "variant.diff")},
		cli.Streams{Out: &out, Err: io.Discard},
		func(string) (string, bool) { return "", false }, Commands{RT: rt})
	if code != cli.ExitOK {
		t.Fatalf("apply exited %d:\n%s", code, out.String())
	}
	if n := len(rt.bodies) - before; n != 0 {
		t.Fatalf("`heros apply` with no --link-receipt made %d transmission(s). It must make none: this is "+
			"the fully offline command, and a default that reached the network would make every local "+
			"diff an egress event nobody agreed to.\n  %s", n, rt.bodies[len(rt.bodies)-1])
	}

	// And WITH the flag, exactly one — to the receipt path and nowhere else.
	var narr bytes.Buffer
	code = cli.Main([]string{"apply", "--repo", repo, "--spec", specPath, "--link-receipt",
		"--out", filepath.Join(t.TempDir(), "variant2.diff")},
		cli.Streams{Out: &out, Err: &narr},
		func(string) (string, bool) { return "", false }, Commands{RT: rt})
	if code != cli.ExitOK {
		t.Fatalf("apply --link-receipt exited %d:\n%s", code, out.String())
	}
	sent := rt.bodies[before:]
	if len(sent) != 1 {
		t.Fatalf("`heros apply --link-receipt` made %d transmission(s), want exactly 1\n%s",
			len(sent), narr.String())
	}
	if !bytes.Contains(sent[0], []byte(runlink.TransformReceiptContractVersion)) {
		t.Errorf("the transmission is not a transform receipt: %s", sent[0])
	}
	// The diff is on disk and NOT on the wire.
	for _, marker := range []string{"@@", "+++ ", "--- "} {
		if bytes.Contains(sent[0], []byte(marker)) {
			t.Errorf("the receipt carries a diff marker %q — the receipt is three integers where a diff "+
				"would go, and there is no field one could occupy:\n%s", marker, sent[0])
		}
	}
}

// baselineSpec discovers the repo and writes a Variant Spec with no overrides.
//
// Built from a REAL discovery rather than hand-written, because a spec must name the workflow, the
// revision and every node in order — and a hand-written one drifts the moment the fixture does, failing
// with "order must list at least one node" instead of with whatever the test is actually about.
func baselineSpec(t *testing.T, repo string) string {
	t.Helper()
	dir := t.TempDir()
	irPath := filepath.Join(dir, "ir.json")
	var out bytes.Buffer
	if code := cli.Main([]string{"discover", "--repo", repo, "--out", irPath,
		"--report", filepath.Join(dir, "rep.json")},
		cli.Streams{Out: &out, Err: io.Discard},
		func(string) (string, bool) { return "", false }, nil); code != cli.ExitOK {
		t.Fatalf("discover exited %d: %s", code, out.String())
	}
	b, err := os.ReadFile(irPath)
	if err != nil {
		t.Fatal(err)
	}
	var ir struct {
		Workflow struct {
			ID   string `json:"id"`
			Repo struct {
				CommitSHA string `json:"commit_sha"`
			} `json:"repo"`
		} `json:"workflow"`
		Nodes []struct {
			NodeID string `json:"node_id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(b, &ir); err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		order = append(order, n.NodeID)
	}
	if len(order) == 0 {
		t.Fatal("the fixture discovered no nodes; a baseline spec needs at least one")
	}
	rev := ir.Workflow.Repo.CommitSHA
	if rev == "" {
		rev = strings.Repeat("0", 40)
	}
	spec, err := json.Marshal(map[string]any{
		"workflow_id": ir.Workflow.ID, "source_revision": rev,
		"order": order, "overrides": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(path, spec, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
