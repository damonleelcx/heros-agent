package cli

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// app_test.go is the §2 CLI acceptance surface: the offline commands work with no account and no
// network, exit codes discriminate, stdout is machine-only, and config resolution reports provenance.

// fixtureRepo copies the discovery sample repo into a temp dir so `eval` can write its run store there
// without polluting testdata. Returns the repo path.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "discovery", "testdata", "samplerepo")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("sample repo not present: %v", err)
	}
	dst := t.TempDir()
	repo := filepath.Join(dst, "repo")
	if err := copyTree(src, repo); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return repo
}

// run drives Main with captured streams and a fixed empty environment, returning code+stdout+stderr.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(args, Streams{Out: &out, Err: &errb}, func(string) (string, bool) { return "", false }, nil)
	return code, out.String(), errb.String()
}

func decodeEnvelope(t *testing.T, stdout string) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not a single JSON envelope: %v\n%s", err, stdout)
	}
	return env
}

func TestVersionCommand(t *testing.T) {
	code, stdout, stderr := run(t, "version")
	if code != ExitOK {
		t.Fatalf("version exit = %d, want %d", code, ExitOK)
	}
	env := decodeEnvelope(t, stdout)
	if env.ContractVersion != OutputContractVersion || env.Command != "version" || !env.OK {
		t.Errorf("envelope = %+v", env)
	}
	if !strings.Contains(stderr, "heros ") {
		t.Errorf("stderr should carry the human line, got %q", stderr)
	}
}

func TestDiscoverOfflineNoAccount(t *testing.T) {
	repo := fixtureRepo(t)
	out := filepath.Join(t.TempDir(), "ir.json")
	rep := filepath.Join(t.TempDir(), "report.json")
	code, stdout, _ := run(t, "discover", "--repo", repo, "--out", out, "--report", rep)
	if code != ExitOK {
		t.Fatalf("discover exit = %d, want 0", code)
	}
	env := decodeEnvelope(t, stdout)
	data, _ := json.Marshal(env.Data)
	var d DiscoverData
	_ = json.Unmarshal(data, &d)
	if d.Nodes < 1 {
		t.Errorf("expected >=1 node, got %d", d.Nodes)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("IR not written: %v", err)
	}
}

func TestEvalDeterminismAndRecord(t *testing.T) {
	repo := fixtureRepo(t)
	code1, out1, _ := run(t, "eval", "--repo", repo, "--seeds", "5", "--cases", "6")
	if code1 != ExitOK {
		t.Fatalf("eval exit = %d, want 0", code1)
	}
	env1 := decodeEnvelope(t, out1)
	var e1 EvalData
	remarshal(t, env1.Data, &e1)

	// The run record is written to the store, allowlist-shaped.
	runs, err := OpenRunStore(repo).List()
	if err != nil || len(runs) != 1 {
		t.Fatalf("run store: %v runs=%v", err, runs)
	}
	rec, err := OpenRunStore(repo).Get(e1.RunID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if rec.RunsReported != 1 || len(rec.Scores) == 0 {
		t.Errorf("record not populated: %+v", rec)
	}

	// Determinism (NFR5): same repo+revision+config → identical config_hash.
	_, out2, _ := run(t, "eval", "--repo", repo, "--seeds", "5", "--cases", "6")
	var e2 EvalData
	remarshal(t, decodeEnvelope(t, out2).Data, &e2)
	if e1.ConfigHash != e2.ConfigHash {
		t.Errorf("config_hash drift: %s != %s", e1.ConfigHash, e2.ConfigHash)
	}
}

func TestExitCodesDiscriminate(t *testing.T) {
	// invalid-config: missing required input (link needs --run).
	code, _, stderr := run(t, "link", "--repo", t.TempDir())
	// link is a net command, but the missing-run check happens before any transport; with net==nil the
	// dispatcher would route to net. To test the invalid-config path deterministically, use apply which
	// requires --spec and is offline.
	_ = code
	_ = stderr

	codeA, _, stderrA := run(t, "apply", "--repo", t.TempDir())
	if codeA != ExitInvalidCfg {
		t.Errorf("apply without --spec: exit = %d, want %d (%q)", codeA, ExitInvalidCfg, stderrA)
	}
	if !strings.Contains(stderrA, "spec") {
		t.Errorf("invalid-config must name the missing input, got %q", stderrA)
	}

	// unknown command → invalid-config.
	codeU, _, _ := run(t, "frobnicate")
	if codeU != ExitInvalidCfg {
		t.Errorf("unknown command: exit = %d, want %d", codeU, ExitInvalidCfg)
	}

	// success axis.
	codeV, _, _ := run(t, "version")
	if codeV != ExitOK {
		t.Errorf("version: exit = %d, want 0", codeV)
	}
}

func TestStdoutIsMachineOnly(t *testing.T) {
	repo := fixtureRepo(t)
	_, stdout, _ := run(t, "discover", "--repo", repo, "--out", filepath.Join(t.TempDir(), "ir.json"), "--report", filepath.Join(t.TempDir(), "r.json"))
	// stdout must be exactly one JSON document — no narration leaked in.
	dec := json.NewDecoder(strings.NewReader(stdout))
	var first any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Errorf("stdout carries more than one document — narration leaked to stdout")
	}
}

func TestConfigProvenance(t *testing.T) {
	repo := fixtureRepo(t)
	// A project file supplies workflow-id; a flag overrides seeds; env is empty. status must show the
	// source of each.
	if err := os.WriteFile(filepath.Join(repo, ProjectFile), []byte(`{"workflow-id":"from-file"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stdout, _ := run(t, "status", "--repo", repo, "--seeds", "9")
	var sd StatusData
	remarshal(t, decodeEnvelope(t, stdout).Data, &sd)
	got := map[string]Source{}
	for _, r := range sd.Config {
		got[r.Key] = r.Source
	}
	if got["workflow-id"] != SourceFile {
		t.Errorf("workflow-id source = %q, want file", got["workflow-id"])
	}
	if got["seeds"] != SourceFlag {
		t.Errorf("seeds source = %q, want flag", got["seeds"])
	}
	if got["repo"] != SourceFlag {
		t.Errorf("repo source = %q, want flag", got["repo"])
	}
}

// TestOfflineNoNetworkImports proves offline-by-construction for the cli package's OWN files: none may
// import net/http directly. The deep guarantee (dependencies never dial) is covered by the runtime
// network-denied test in the QA suite; this is the cheap structural half, mirroring
// discovery/noexec_test.go.
func TestOfflineNoNetworkImports(t *testing.T) {
	fset := token.NewFileSet()
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "net" || p == "net/http" {
				t.Errorf("%s imports %q — the offline command surface must not link the network", e.Name(), p)
			}
		}
	}
}

func remarshal(t *testing.T, v any, into any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
