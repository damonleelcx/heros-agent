package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// qa_acceptance_test.go is the §7 (11a) acceptance gate, consolidating the cases the other suites do not
// already pin: the gate-failed exit code (1), IR determinism, and local↔recorded score parity. The
// offline (7.1), egress (7.2), credential (7.3) and idempotency (7.7) cases live in their own packages'
// suites and are referenced from the tasks file.

// TestExitCode_GateFailedIsDistinct — a configured gate failing exits 1 (ExitGateFailed), distinct from
// operational (2) and invalid-config (3), and the machine envelope names the gate (task 7.4 / FR5).
func TestExitCode_GateFailedIsDistinct(t *testing.T) {
	repo := fixtureRepo(t)
	// The reference runtime answers ~80% correct, so a 0.99 quality floor must fail.
	code, out, _ := run(t, "eval", "--repo", repo, "--seeds", "5", "--cases", "6", "--min-quality", "0.99")
	if code != ExitGateFailed {
		t.Fatalf("failing gate exit = %d, want %d (gate-failed)", code, ExitGateFailed)
	}
	env := decodeEnvelope(t, out)
	if env.Gate == nil || env.Gate.Passed {
		t.Errorf("envelope should name a failed gate, got %+v", env.Gate)
	}
	if env.Gate.Name == "" {
		t.Errorf("a failing gate must name itself")
	}

	// The SAME run with a satisfiable floor passes (exit 0), proving the code discriminates.
	codeOK, outOK, _ := run(t, "eval", "--repo", repo, "--seeds", "5", "--cases", "6", "--min-quality", "0.1")
	if codeOK != ExitOK {
		t.Fatalf("passing gate exit = %d, want 0", codeOK)
	}
	if g := decodeEnvelope(t, outOK).Gate; g == nil || !g.Passed {
		t.Errorf("a satisfied gate should report passed, got %+v", g)
	}
}

// TestDeterministicIR — same repo + revision + config → byte-identical IR (task 7.5 / NFR5).
func TestDeterministicIR(t *testing.T) {
	repo := fixtureRepo(t)
	ir1 := filepath.Join(t.TempDir(), "ir1.json")
	ir2 := filepath.Join(t.TempDir(), "ir2.json")
	rep := filepath.Join(t.TempDir(), "r.json")
	if code, _, _ := run(t, "discover", "--repo", repo, "--commit", "abcdef1", "--out", ir1, "--report", rep); code != ExitOK {
		t.Fatal("discover 1 failed")
	}
	if code, _, _ := run(t, "discover", "--repo", repo, "--commit", "abcdef1", "--out", ir2, "--report", rep); code != ExitOK {
		t.Fatal("discover 2 failed")
	}
	b1, _ := os.ReadFile(ir1)
	b2, _ := os.ReadFile(ir2)
	if !bytes.Equal(b1, b2) {
		t.Errorf("IR is not deterministic: two discoveries produced different bytes")
	}
}

// TestParity_RecordedScoresEqualEvalScores — the scores written to the run record are exactly the
// scores `eval` computed and reported, so a linked (hosted) view matches the local one (task 7.6). The
// CLI uses evalstats for the intervals — one implementation, no second scorer.
func TestParity_RecordedScoresEqualEvalScores(t *testing.T) {
	repo := fixtureRepo(t)
	var out bytes.Buffer
	code := Main([]string{"eval", "--repo", repo, "--seeds", "5", "--cases", "6"},
		Streams{Out: &out, Err: io.Discard}, func(string) (string, bool) { return "", false }, nil)
	if code != ExitOK {
		t.Fatalf("eval exit %d", code)
	}
	var e EvalData
	remarshal(t, decodeEnvelope(t, out.String()).Data, &e)

	rec, err := OpenRunStore(repo).Get(e.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Scores) != len(e.Scores) || len(e.Scores) == 0 {
		t.Fatalf("score count mismatch: record %d, eval %d", len(rec.Scores), len(e.Scores))
	}
	for i := range e.Scores {
		a, b := e.Scores[i], rec.Scores[i]
		if a != b {
			t.Errorf("score %d differs between eval output and stored record: %+v vs %+v", i, a, b)
		}
	}
	// And the eval output round-trips as JSON identically (a consumer reads the same numbers we stored).
	ej, _ := json.Marshal(e.Scores)
	rj, _ := json.Marshal(rec.Scores)
	if !bytes.Equal(ej, rj) {
		t.Errorf("eval scores JSON != recorded scores JSON")
	}
}
