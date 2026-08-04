package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A missing repository must be distinguishable from a repository with nothing in it.
//
// This is the regression fence for a defect found by running the CLI against a real checkout:
// `discover --repo /path/that/is/not/there` printed `"ok": true`, `0 nodes`, exited 0, and wrote an
// EMPTY ir.json over the 27-node ir.json already in the working directory. Every command that discovers
// shared the behaviour, because each read cfg.Get("repo") for itself.

func TestMissingRepoIsInvalidConfigNotSuccess(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")

	// Every command that takes --repo and discovers through it. `status` is deliberately absent: it
	// reports configuration and must keep working wherever it is run.
	cases := []struct {
		name string
		args []string
	}{
		{"discover", []string{"discover", "--repo", missing}},
		{"eval", []string{"eval", "--repo", missing, "--seeds", "2", "--cases", "2"}},
		{"apply", []string{"apply", "--repo", missing, "--spec", "spec.json"}},
		{"author", []string{"author", "--repo", missing, "--spec", "spec.json", "--node", "n_1", "--drop-tolerance", "0.2"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := run(t, tc.args...)
			if code == ExitOK {
				t.Fatalf("%s exited 0 for a repo that does not exist\nstdout: %s", tc.name, stdout)
			}
			if code != ExitInvalidCfg {
				t.Errorf("%s exit = %d, want %d (invalid-config: fix the invocation)", tc.name, code, ExitInvalidCfg)
			}
			if !containsFoldLocal(stderr, "does not exist") {
				t.Errorf("%s stderr must say the path is not there, got %q", tc.name, stderr)
			}
		})
	}
}

// The empty-IR overwrite is the part that cost real work, so it gets its own assertion: a refused
// invocation must not have written anything.
func TestMissingRepoWritesNothing(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "ir.json")
	const sentinel = `{"ir_version":"1.0.0","nodes":[{"node_id":"n_real"}]}`
	if err := os.WriteFile(existing, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, _ := run(t, "discover", "--repo", filepath.Join(dir, "not-there"), "--out", existing)
	if code == ExitOK {
		t.Fatal("discover exited 0 for a missing repo")
	}

	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("the refused command deleted the existing IR: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("the refused command overwrote an existing IR:\n got: %s\nwant: %s", got, sentinel)
	}
}

// A --repo pointing at a FILE is the same class of mistake and must not be walked as if it were a tree.
func TestRepoThatIsAFileIsRefused(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ir.json")
	if err := os.WriteFile(f, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "discover", "--repo", f)
	if code != ExitInvalidCfg {
		t.Errorf("discover --repo <file> exit = %d, want %d", code, ExitInvalidCfg)
	}
	if !containsFoldLocal(stderr, "not a directory") {
		t.Errorf("stderr should name the cause, got %q", stderr)
	}
}

// A repository that EXISTS and genuinely has no call sites must still succeed with 0 nodes — the whole
// point of the fix is that these two answers stop being the same answer.
func TestEmptyRepoStillSucceedsWithZeroNodes(t *testing.T) {
	empty := t.TempDir()
	out := filepath.Join(t.TempDir(), "ir.json")
	code, stdout, _ := run(t, "discover", "--repo", empty, "--out", out, "--report", filepath.Join(t.TempDir(), "r.json"))
	if code != ExitOK {
		t.Fatalf("an empty but real repo must succeed, got exit %d", code)
	}
	env := decodeEnvelope(t, stdout)
	b, _ := json.Marshal(env.Data)
	var d DiscoverData
	_ = json.Unmarshal(b, &d)
	if d.Nodes != 0 {
		t.Errorf("nodes = %d, want 0", d.Nodes)
	}
}

func containsFoldLocal(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
