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
//
// # 🔴 IT IS EMPTY, and that is the point of writing debts down
//
// All four rows that stood here have been paid, and the mechanism is what collected them — each one
// named a package, a symptom and an owner, so the work was findable instead of being a gap nobody
// could see:
//
//	internal/reportstore     RED — its seed inserted a `workflow` row with no `repo_url`. The diagnosis
//	                         in this list was right, and the seed turned out to be wrong in FOUR
//	                         independent ways (three more columns, a `variant.config_hash` that has
//	                         never existed, a missing `variant.label`, and `config` inserted before the
//	                         `variant` its FK points at). Four mistakes do not survive one successful
//	                         run, so there had never been one. Fixed; its six assertions now execute
//	                         for the first time.
//
//	internal/deliveryrecord  "RED IN THE BATCH ONLY — passes alone, fails beside the other proofs, so
//	                         the defect is shared-database interference." Correct, and the interference
//	                         had a name: its trigger check queried `pg_trigger`/`pg_class` by NAME with
//	                         no schema filter. The catalogs are database-wide and every test package
//	                         gets its own schema, so it counted one `transform_immutable` per SCHEMA —
//	                         `count=18` where it wanted 1. Fixed by binding `'transform'::regclass`,
//	                         which is the rule the migration fence in internal/pgmigrate already
//	                         enforced for migrations and now enforces for Go sources too.
//
//	internal/adminops        GREEN and not yet added. Added.
//	internal/deliveryroute   GREEN and not yet added. Added.
//
// 🚫 The map is kept rather than deleted along with its rows. Deleting it would delete the MECHANISM —
// the next red proof would have nowhere to be quarantined with a reason, and the choice would collapse
// back to "break the gate for everybody" or "quietly leave it out", which is the pair this file exists
// to avoid.
var pgproofExceptions = map[string]string{}

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

// packagesInTheGate reads `PGPROOF_PKGS` out of the Makefile.
//
// 🔴 The VARIABLE, not the recipe. The recipe used to hold the package list inline; it now expands
// `$(PGPROOF_PKGS)`, which CI also reads through `make -s pgproof-packages` — so the variable is the
// single copy of this fact and is therefore the thing to judge. Parsing the recipe would find one
// token and report that the gate runs nothing.
func packagesInTheGate(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read %s: %v", makefilePath, err)
	}
	out := map[string]bool{}
	inVar := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PGPROOF_PKGS") {
			inVar = true
		} else if !inVar {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "./internal/") {
				out[strings.TrimSuffix(strings.TrimPrefix(tok, "./"), "/")] = true
			}
		}
		// A trailing backslash continues the assignment; anything else ends it.
		if !strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") {
			break
		}
	}
	if len(out) == 0 {
		t.Fatalf("no packages parsed out of PGPROOF_PKGS in %s — this fence cannot judge a target "+
			"it cannot read", makefilePath)
	}
	return out
}

// 🔴 THE RECIPE AND CI BOTH READ THE VARIABLE. Without this, the fence above would keep judging a
// variable that nothing runs — which is the same class of failure it was written for, one level up.
func TestTheGateAndCIBothReadTheOneList(t *testing.T) {
	mk, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mk), "-tags pgproof -count=1 $(PGPROOF_PKGS)") {
		t.Error("the `pg-proof` recipe no longer expands $(PGPROOF_PKGS), so the list this fence judges " +
			"is not the list that runs")
	}
	ci, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ci), "make -s pgproof-packages") {
		t.Error("CI no longer reads `pgproof-packages`. It kept its own hand-written list once and that " +
			"list was missing fourteen of the twenty-six packages, including two whose proofs had never " +
			"run at all — the comment standing beside it even said this would happen.")
	}
	// 🚫 And CI must not carry a literal package list beside the call, which is how the two would drift
	// apart again while both looking present.
	if strings.Contains(string(ci), "-tags pgproof -count=1 ./internal/") {
		t.Error("CI passes literal packages to `go test -tags pgproof`. That is a second list.")
	}
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
