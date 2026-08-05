package deploy

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// pgproof_gate_test.go asserts that every live-Postgres proof in this repository is actually RUN by the
// target that claims to run them.
//
// # How this was found
//
// P27 amended D3 so an absent provider handle is the empty string rather than NULL. Four packages'
// pgproof tests were updated with it; `internal/signup`'s was not, and its assertion — a `sql.NullString`
// checking `.Valid` — silently inverted, reporting an empty handle as a handle. Nothing went red, because
// `make pg-proof` does not run `internal/signup`.
//
// Seven packages were in that state. Two of them are RED right now and have been for some time:
//
//   - `internal/reportstore` fails on its own fixture — `null value in column "repo_url" of relation
//     "workflow"` — because a later migration made that column NOT NULL and the seed was never updated.
//   - `internal/deliveryrecord` passes alone and fails inside the batch, which is a cross-package
//     interference in the shared database rather than a defect in what it asserts.
//
// Neither is P27's, and neither is fixed here — adding a red package to the gate would break the gate for
// everybody, which is how a gate gets bypassed. They are listed below with their reason, so the gap is a
// row somebody can act on rather than an absence nobody can see.
//
// # Why this is a test rather than a lint
//
// The failure mode is not a broken proof. It is a proof that nobody notices has stopped being run, which
// looks exactly like a proof that keeps passing. The only observable difference is this list.

const makefilePath = "../../Makefile"

// pgproofExceptions are packages with live-Postgres proofs that `make pg-proof` deliberately does not
// run, each with the reason. An entry here is a debt with a name on it, not an exemption.
var pgproofExceptions = map[string]string{
	"internal/reportstore": "RED — its fixture inserts a `workflow` row with no `repo_url`, which a later " +
		"migration made NOT NULL. The proof has been failing on its own seed; adding it to the gate would " +
		"break the gate rather than fix the seed. Owner: whoever owns reportstore.",
	"internal/deliveryrecord": "RED IN THE BATCH ONLY — passes when run alone, fails when run beside the " +
		"other proofs, so the defect is shared-database interference and not the assertion. Diagnosing it " +
		"means finding what another package's schema recreation disturbs.",
	"internal/adminops": "GREEN and not yet added. It predates this fence and belongs to the operator " +
		"domain; adding it is a one-word change somebody should make deliberately rather than as a " +
		"side effect of an accounts phase.",
	"internal/deliveryroute": "GREEN and not yet added, for the same reason as internal/adminops.",
}

// packagesWithPgproofTests finds every package carrying a `pgproof`-tagged test file.
func packagesWithPgproofTests(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	root := filepath.Join("..", "..", "internal")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		// 🔴 The BUILD TAG, not the filename. The `*_pgproof_test.go` naming is a convention and this
		// file is the counter-example that proves it: it is named for the thing it is ABOUT, carries no
		// tag, and a name-matching walker reported `internal/deploy` as an ungated proof package — this
		// fence's first run flagged itself. What makes a file a live-Postgres proof is that the tag keeps
		// it out of an ordinary build.
		b, rerr := os.ReadFile(path)
		if rerr != nil || !hasPgproofTag(string(b)) {
			return nil
		}
		rel, rerr := filepath.Rel(filepath.Join("..", ".."), filepath.Dir(path))
		if rerr != nil {
			return nil
		}
		seen[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// hasPgproofTag reports whether a file is behind the `pgproof` build constraint. Only the leading
// comment block is inspected — a build constraint further down the file is not one, and a `//go:build`
// line quoted inside a string or a doc comment must not count.
func hasPgproofTag(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false // past the constraint block
		}
		if strings.HasPrefix(trimmed, "//go:build ") && strings.Contains(trimmed, "pgproof") {
			return true
		}
	}
	return false
}

// packagesInTheGate reads the `pg-proof` recipe out of the Makefile.
func packagesInTheGate(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read %s: %v", makefilePath, err)
	}
	inTarget := false
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "pg-proof:") {
			inTarget = true
			continue
		}
		if inTarget {
			if !strings.HasPrefix(line, "\t") {
				break // the recipe ended
			}
			for _, tok := range strings.Fields(line) {
				if strings.HasPrefix(tok, "./internal/") {
					out[strings.TrimSuffix(strings.TrimPrefix(tok, "./"), "/")] = true
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no packages parsed out of the pg-proof recipe in %s — this fence cannot judge a target "+
			"it cannot read", makefilePath)
	}
	return out
}

// TestEveryLivePostgresProofIsRunByTheGate is the assertion.
func TestEveryLivePostgresProofIsRunByTheGate(t *testing.T) {
	gated := packagesInTheGate(t)
	var unrun []string
	for _, pkg := range packagesWithPgproofTests(t) {
		if gated[pkg] {
			continue
		}
		if _, excepted := pgproofExceptions[pkg]; excepted {
			continue
		}
		unrun = append(unrun, pkg)
	}
	if len(unrun) > 0 {
		t.Fatalf("these packages carry live-Postgres proofs that `make pg-proof` never runs:\n  %s\n\n"+
			"A proof nobody runs is indistinguishable from a proof that passes. P27 found this the "+
			"expensive way: a schema change inverted an assertion in `internal/signup` and nothing went "+
			"red, because that package was not in the target.\n\n"+
			"Add it to the `pg-proof` recipe, or add it to pgproofExceptions in this file WITH THE REASON.",
			strings.Join(unrun, "\n  "))
	}
}

// TestTheExceptionListIsNotStale keeps the debts honest. An exception for a package that no longer has
// live proofs — or that somebody has since added to the gate — is a row that reads as a known problem
// and is not one, which makes the whole list less believable.
func TestTheExceptionListIsNotStale(t *testing.T) {
	have := map[string]bool{}
	for _, p := range packagesWithPgproofTests(t) {
		have[p] = true
	}
	gated := packagesInTheGate(t)
	for pkg, why := range pgproofExceptions {
		if !have[pkg] {
			t.Errorf("%s is excepted but carries no live-Postgres proof any more — delete the row", pkg)
		}
		if gated[pkg] {
			t.Errorf("%s is both excepted and IN the gate. Delete the exception: it is now claiming a "+
				"debt that has been paid, which makes the others read as noise.", pkg)
		}
		if len(strings.TrimSpace(why)) < 40 {
			t.Errorf("%s's exception gives no usable reason (%q). An exception without one is an "+
				"exemption, and it is how a list like this stops being read.", pkg, why)
		}
	}
}
