package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/heros-foreal/heros/internal/autonomy"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// TestTheDaemonsWorkerIsFullyWired.
//
// 🔴 Regression fence for a bug that was invisible in every other test. The daemon assembled its worker
// inline and never set `Reviser`, so improvement runs assessed and stopped — and reported SUCCESS,
// because a scoped goal's completion criterion is satisfied by the assessment alone. The end-to-end test
// passed the whole time, because it wires its own worker: the tested path and the shipped path were
// different objects assembled by different code.
//
// This asserts on the object the daemon actually runs.
func TestTheDaemonsWorkerIsFullyWired(t *testing.T) {
	plans, err := planner.Default()
	if err != nil {
		t.Fatalf("planners: %v", err)
	}
	w := buildWorker(store.NewMemory(), toolcontract.NewRegistry(), memory.NewMem(), plans,
		autonomy.Policy{})

	if w.Store == nil {
		t.Error("no store: the worker cannot claim anything")
	}
	if w.Tools == nil {
		t.Error("no tool registry: every task would fail with `no tool registered`")
	}
	if w.Policy == nil {
		t.Error("no approval policy: effect-bearing tasks would run ungated")
	}
	// 🔴 And it is the AUTONOMY policy, not the built-in default. The default gates every effect, which
	// is safe — so a daemon that silently kept it would pass every safety test while the organization's
	// setting did nothing at all, and the only symptom would be customers reporting that a feature they
	// turned on has no effect.
	if _, ok := w.Policy.(autonomy.Policy); !ok {
		t.Errorf("the daemon's approval policy is %T, not autonomy.Policy: the per-organization "+
			"autonomy setting would be read by nobody", w.Policy)
	}
	if w.Clock == nil {
		t.Error("no clock: lease expiry cannot be evaluated")
	}
	if w.Lease <= 0 {
		t.Error("no lease duration: a claim would expire immediately and every task would be reclaimed")
	}
	if w.Episodes == nil {
		t.Error("no episode store: run history would be permanently empty")
	}
	// 🔴 The one that was missing.
	if w.Reviser == nil {
		t.Fatal("no reviser: an improvement run assesses, stops, and reports SUCCESS for doing so — " +
			"the plan never grows into proposals and nothing goes red")
	}
}

// TestTheDefaultWebRootServesEveryPageTheProductHas.
//
// # 🔴 The bug this exists for
//
// `-web` defaulted to `web`, which contains no index.html — so `go run ./cmd/herosd` with no flags
// served a **directory listing**. Nothing was red: the process started, the port answered, `curl /`
// returned 200. It just was not the product. Every set of instructions had to carry `-web web/static`,
// and the one that forgot looked like a broken build rather than a wrong flag.
//
// ⚠️ IT USED TO CHECK ONLY THE ROOT index.html, and that stopped being enough the moment the console
// moved. `/` is the public home page now and the console lives at `/app/`; a check on the root alone
// stays green while `app/index.html` is missing and `/app/` serves a directory listing — the same bug,
// one directory down, and invisible in exactly the same way.
//
// Every page the file server is expected to answer is named here. A page added to the product and not
// added to this list is a page whose absence nothing reports.
//
// The check is on `defaultWebRoot`, the constant the flag declaration uses, rather than on the string
// "web/static" — asserting a copy of the value would pass happily while the daemon used another one.
func TestTheDefaultWebRootServesEveryPageTheProductHas(t *testing.T) {
	root := repoRoot(t)
	for _, page := range []struct{ path, what string }{
		{"index.html", "the public home page, at /"},
		{"app/index.html", "the console, at /app/"},
		{"signin/index.html", "the sign-in page, at /signin/"},
		{"signup/index.html", "the sign-up page, at /signup/"},
		{"heros.css", "the shared token stylesheet every page links"},
		{"heros-auth.js", "the script both auth pages load"},
		{"avatar.jpg", "her portrait, shown on every page"},
		{"favicon.png", "the tab icon every page links"},
	} {
		full := filepath.Join(root, defaultWebRoot, page.path)
		info, err := os.Stat(full)
		if err != nil {
			t.Errorf("%s is missing from the default -web directory (%s), so %s does not exist: %v",
				page.path, defaultWebRoot, page.what, err)
			continue
		}
		if info.IsDir() || info.Size() == 0 {
			t.Errorf("%s is not a file with content in it — %s", page.path, page.what)
		}
	}
}

// repoRoot walks up from the test's working directory to the module root.
//
// 🚫 Not an environment variable and not a path relative to a checkout somewhere else: a fence anchored
// on an absolute repository path judges whichever tree that path names, which in a worktree is not the
// tree being tested.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory; cannot locate the module root")
		}
		dir = parent
	}
}
