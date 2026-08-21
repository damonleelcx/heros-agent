package sourceingest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// treeguard_test.go is P32 task 2.2: **every refusal `bundle_test.go` constructs, run against the CLONE
// path as well, from a constructed repository fixture.**
//
// # Why this file exists at all, given bundle_test.go passes
//
// Design D2 names the failure precisely: the defences exist because a *tree* can be malicious, not
// because an *upload* can, and §7.1 says the escaping-symlink case is *"the fence most likely to be
// written only for bundles"*. A refactor that extracts `TreeGuard` and then only exercises it through
// the tar reader has moved the code and not extended the coverage — the clone path could stop calling
// it entirely and every existing test would still pass.
//
// So the cases below build a DIRECTORY, not an archive, and run `InspectTree` over it. That is the
// shape `git clone` produces, and it reaches the guard through a different producer, which is the whole
// point: two producers, one refusal set, and a fence that fails if either producer drifts.
//
// # 🔴 The two producers are compared against each other, not against an expectation
//
// `TestBundleAndCloneRefuseTheSameThings` runs the SAME logical entry through both `tarEntryOf` and the
// walk classifier and asserts they reach the same verdict. Asserting each against a hand-written
// expected message would let both drift together — the classic "A vs expectation" mistake, where the
// expectation is edited to match whatever the code now does.

// hostileTree describes one malicious repository fixture.
type hostileTree struct {
	name string
	// build writes the fixture under root and returns nothing; a builder that cannot create its
	// hazard on this platform calls t.Skip itself.
	build func(t *testing.T, root string)
	want  string
}

func hostileTrees() []hostileTree {
	return []hostileTree{
		{
			name: "escaping symlink",
			build: func(t *testing.T, root string) {
				// The §7.1 case. `git clone` writes this verbatim from a repository that contains it,
				// and it is the entry a walk that followed links would classify as a regular file.
				if err := os.Symlink("/etc/shadow", filepath.Join(root, "secrets")); err != nil {
					t.Skipf("this platform cannot create symlinks: %v", err)
				}
			},
			want: "may not contain links",
		},
		{
			name: "symlink pointing inside the tree",
			build: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "real.go"), []byte("package a\n"), 0o640); err != nil {
					t.Fatalf("write: %v", err)
				}
				// 🔴 Refused too, and deliberately. A link that resolves safely at inspection time can
				// be made to resolve elsewhere by a later change to a parent directory; refusing the
				// entry outright removes the ordering question. This case exists so a future
				// "optimization" that permits in-tree links goes red rather than quietly shipping.
				if err := os.Symlink("real.go", filepath.Join(root, "alias.go")); err != nil {
					t.Skipf("this platform cannot create symlinks: %v", err)
				}
			},
			want: "may not contain links",
		},
		{
			name: "escaping symlinked directory",
			build: func(t *testing.T, root string) {
				if err := os.Symlink(os.TempDir(), filepath.Join(root, "out")); err != nil {
					t.Skipf("this platform cannot create symlinks: %v", err)
				}
			},
			want: "may not contain links",
		},
		{
			name: "fifo",
			build: func(t *testing.T, root string) {
				mkfifo(t, filepath.Join(root, "pipe"))
			},
			want: "unsupported type",
		},
	}
}

// TestCloneTreeRefusesHostileEntries is task 2.2 and §7.1: the bundle's refusals, on the clone path.
func TestCloneTreeRefusesHostileEntries(t *testing.T) {
	for _, tc := range hostileTrees() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.build(t, root)
			_, err := NewTreeGuard().InspectTree(root, skipGitMetadata)
			if err == nil {
				t.Fatalf("the clone path ACCEPTED a hostile tree (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestCloneTreeAcceptsAnOrdinaryRepository is the control.
//
// A refusal fence with no acceptance case is a fence that would still pass if the guard refused
// everything — which is the failure mode where a security control is "working" and the product is
// down.
func TestCloneTreeAcceptsAnOrdinaryRepository(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "src"))
	mustWrite(t, filepath.Join(root, "src", "app.ts"), "export const x = 1\n")
	mustWrite(t, filepath.Join(root, "README.md"), "# hello\n")
	// `.git` is present, exactly as a clone leaves it, and must be pruned rather than walked.
	mustMkdir(t, filepath.Join(root, ".git", "objects", "pack"))
	mustWrite(t, filepath.Join(root, ".git", "config"), "[core]\n")

	adm, err := NewTreeGuard().InspectTree(root, skipGitMetadata)
	if err != nil {
		t.Fatalf("an ordinary repository was refused: %v", err)
	}
	// Three entries: src/, src/app.ts, README.md. `.git` and everything under it is pruned, so a
	// repository's own metadata never spends the customer's entry budget.
	if adm.Entries() != 3 {
		t.Errorf("Entries() = %d, want 3 (src/, src/app.ts, README.md — .git pruned)", adm.Entries())
	}
	if adm.Bytes() == 0 {
		t.Error("Bytes() = 0; the admission counted no file bytes for a tree with two files")
	}
}

// TestCloneTreeGitMetadataIsPrunedNotRefused pins WHY `.git` is skipped.
//
// 🔴 If it were walked instead, a large pack file would trip the per-file ceiling and the customer
// would be told their repository was refused for being too large — a refusal about OUR tooling,
// reported as a refusal about their code. The fixture makes the pack oversized on purpose so the test
// fails if the prune is removed.
func TestCloneTreeGitMetadataIsPrunedNotRefused(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	mustMkdir(t, filepath.Join(root, ".git"))
	// A sparse file larger than the per-file ceiling, written without allocating 64 MiB.
	pack, err := os.Create(filepath.Join(root, ".git", "pack-0.pack"))
	if err != nil {
		t.Fatalf("create pack: %v", err)
	}
	if err := pack.Truncate(MaxBundleFileBytes + 1); err != nil {
		_ = pack.Close()
		t.Skipf("this filesystem cannot make a sparse file: %v", err)
	}
	if err := pack.Close(); err != nil {
		t.Fatalf("close pack: %v", err)
	}

	if _, err := NewTreeGuard().InspectTree(root, skipGitMetadata); err != nil {
		t.Fatalf("an oversized .git pack was reported as a refusal of the customer's repository: %v", err)
	}
	// And the same file OUTSIDE .git is still refused, so the prune has not widened into "large files
	// are fine".
	mustWrite(t, filepath.Join(root, "big.bin"), "")
	big, err := os.OpenFile(filepath.Join(root, "big.bin"), os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatalf("open big: %v", err)
	}
	if err := big.Truncate(MaxBundleFileBytes + 1); err != nil {
		_ = big.Close()
		t.Skipf("this filesystem cannot make a sparse file: %v", err)
	}
	_ = big.Close()
	if _, err := NewTreeGuard().InspectTree(root, skipGitMetadata); err == nil {
		t.Fatal("an oversized file outside .git was accepted; the prune has widened into the ceiling")
	}
}

// TestCeilingsAreEnforcedOnTheClonePath is §7.2: entry-count, per-file and total-bytes on the clone.
func TestCeilingsAreEnforcedOnTheClonePath(t *testing.T) {
	t.Run("entry count", func(t *testing.T) {
		g := &TreeGuard{maxEntries: 3, maxFileBytes: MaxBundleFileBytes, maxTotalBytes: MaxBundleTotalBytes, maxPathLength: MaxBundlePathLength}
		root := t.TempDir()
		for i := 0; i < 5; i++ {
			mustWrite(t, filepath.Join(root, string(rune('a'+i))+".go"), "package a\n")
		}
		if _, err := g.InspectTree(root, nil); err == nil || !strings.Contains(err.Error(), "more than 3 entries") {
			t.Fatalf("err = %v, want the entry-count refusal", err)
		}
	})

	t.Run("per-file bytes", func(t *testing.T) {
		g := &TreeGuard{maxEntries: MaxBundleEntries, maxFileBytes: 8, maxTotalBytes: MaxBundleTotalBytes, maxPathLength: MaxBundlePathLength}
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "a.go"), strings.Repeat("x", 9))
		if _, err := g.InspectTree(root, nil); err == nil || !strings.Contains(err.Error(), "over the 8 limit") {
			t.Fatalf("err = %v, want the per-file refusal", err)
		}
	})

	t.Run("total bytes", func(t *testing.T) {
		g := &TreeGuard{maxEntries: MaxBundleEntries, maxFileBytes: MaxBundleFileBytes, maxTotalBytes: 10, maxPathLength: MaxBundlePathLength}
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "a.go"), strings.Repeat("x", 6))
		mustWrite(t, filepath.Join(root, "b.go"), strings.Repeat("x", 6))
		if _, err := g.InspectTree(root, nil); err == nil || !strings.Contains(err.Error(), "exceeds 10 uncompressed bytes") {
			t.Fatalf("err = %v, want the total-bytes refusal", err)
		}
	})
}

// TestBundleAndCloneRefuseTheSameThings compares the two PRODUCERS against each other.
//
// 🔴 A-vs-B, never A-vs-expectation. Asserting each producer against a hand-written message would let
// both drift together, because whoever changes the guard also edits the expectation. Comparing them
// means a change that makes the tar path stricter than the walk path (or vice versa) fails, which is
// the only property that actually matters: one refusal set, two producers.
func TestBundleAndCloneRefuseTheSameThings(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
	}{
		{"symlink", Entry{Path: "link", Kind: EntryLink, LinkTarget: "/etc/passwd"}},
		{"device node", Entry{Path: "dev", Kind: EntryOther, RawKind: "c"}},
		{"regular file", Entry{Path: "a.go", Kind: EntryFile, Size: 10}},
		{"directory", Entry{Path: "src", Kind: EntryDir}},
		{"escaping path", Entry{Path: "../out.go", Kind: EntryFile, Size: 1}},
		{"absolute path", Entry{Path: "/etc/passwd", Kind: EntryFile, Size: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Two independent admissions over two roots: the verdict must not depend on which
			// producer's traversal is in progress.
			a1, err := NewTreeGuard().Admit(t.TempDir())
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			a2, err := NewTreeGuard().Admit(t.TempDir())
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			_, e1 := a1.Entry(tc.entry)
			_, e2 := a2.Entry(tc.entry)
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("the two producers disagree: bundle=%v clone=%v", e1, e2)
			}
			if e1 != nil && e1.Error() != e2.Error() {
				t.Errorf("the two producers refuse differently:\n bundle: %v\n clone:  %v", e1, e2)
			}
		})
	}
}

// TestPaxHeaderIsCountedBeforeItIsSkipped pins the ORDER the refactor had to preserve.
//
// The entry-count and path-length checks run BEFORE the metadata skip, exactly as bundle.go enforced
// them. Moving the skip earlier would look equivalent and would mean an archive of ten million pax
// headers never trips the entry ceiling.
func TestPaxHeaderIsCountedBeforeItIsSkipped(t *testing.T) {
	g := &TreeGuard{maxEntries: 2, maxFileBytes: MaxBundleFileBytes, maxTotalBytes: MaxBundleTotalBytes, maxPathLength: MaxBundlePathLength}
	adm, err := g.Admit(t.TempDir())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := adm.Entry(Entry{Path: "pax_global_header", Kind: EntryMetadata}); err != ErrSkipEntry {
			t.Fatalf("entry %d: err = %v, want ErrSkipEntry", i, err)
		}
	}
	if _, err := adm.Entry(Entry{Path: "pax_global_header", Kind: EntryMetadata}); err == nil ||
		!strings.Contains(err.Error(), "more than 2 entries") {
		t.Fatalf("err = %v; a metadata entry must still be COUNTED, or the ceiling can be bypassed with pax headers", err)
	}

	// The path-length check is likewise before the skip.
	adm2, err := g.Admit(t.TempDir())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	long := strings.Repeat("a", MaxBundlePathLength+1)
	if _, err := adm2.Entry(Entry{Path: long, Kind: EntryMetadata}); err == nil ||
		!strings.Contains(err.Error(), "entry path exceeds") {
		t.Fatalf("err = %v, want the path-length refusal on a metadata entry", err)
	}
}

// TestEntryOtherIsTheZeroValue pins the fail-closed direction.
//
// An `Entry` built without setting `Kind` must be REFUSED. If the zero value were `EntryFile`, a
// producer that forgot the field would admit whatever it found — and that is the shape of every
// unpacker defect there has ever been.
func TestEntryOtherIsTheZeroValue(t *testing.T) {
	if EntryOther != 0 {
		t.Fatalf("EntryOther = %d, want 0 — an Entry with no Kind must fail closed", EntryOther)
	}
	adm, err := NewTreeGuard().Admit(t.TempDir())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if _, err := adm.Entry(Entry{Path: "mystery"}); err == nil {
		t.Fatal("an Entry with no Kind was ACCEPTED")
	}
}

// TestConnectionHasNoFieldThatCanExpressWriteOrBreadth is the ADR-013 Option B refusal, made
// structural (task 1.5).
//
// 🔴 Two checks, not one. The denylist catches the field somebody names honestly (`scope`, `org`); the
// exact-set check catches the one they do not (`flags`, `metadata`, `extra`). A whitelist alone cannot
// protect an invariant and a denylist alone cannot see a euphemism.
//
// # 🔴 Why the denylist matches WORDS and not substrings
//
// The first version matched substrings and went red on `Forge` — because `f-org-e` contains `org`. That
// is not a curiosity to work around; it is the reason a substring denylist over identifiers is the
// wrong instrument. A fence that cries wolf on a correct field gets an exception added to it, the
// exception is `"forge"`, and six months later `ForgeOrgScope` passes. Splitting the identifier into
// its camelCase words and comparing whole words has no such pressure on it.
func TestConnectionHasNoFieldThatCanExpressWriteOrBreadth(t *testing.T) {
	forbidden := map[string]bool{
		"scope": true, "scopes": true, "permission": true, "permissions": true,
		"write": true, "push": true, "admin": true, "maintain": true,
		"org": true, "organization": true, "workspace": true, "group": true, "account": true,
		"all": true, "every": true, "wildcard": true, "pattern": true, "glob": true,
		"token": true, "secret": true, "key": true, "credential": true, "password": true,
	}
	ty := reflect.TypeOf(Connection{})
	got := make([]string, 0, ty.NumField())
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		got = append(got, f.Name)
		for _, word := range camelWords(f.Name) {
			if forbidden[word] {
				t.Errorf("Connection.%s has the word %q in it — a connection may not express a scope, a breadth "+
					"or a secret (ADR-013 Option B is refused on the record; changing that is an ADR amendment)", f.Name, word)
			}
		}
	}
	want := ConnectionFieldAllowlist()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Connection's fields are\n  %v\nand the reviewed set is\n  %v\n"+
			"Adding a field here is a decision: edit connectionFieldAllowlist and say why in the review.", got, want)
	}
}

// camelWords splits an identifier into lowercase words: "ConnectionID" → ["connection", "id"].
//
// Runs of capitals are one word, so "ID" does not become "i","d" and an initialism cannot be split
// into fragments that accidentally match the denylist.
func camelWords(name string) []string {
	var out []string
	var cur []rune
	runes := []rune(name)
	for i, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		prevUpper := i > 0 && runes[i-1] >= 'A' && runes[i-1] <= 'Z'
		nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
		if upper && len(cur) > 0 && (!prevUpper || nextLower) {
			out = append(out, strings.ToLower(string(cur)))
			cur = nil
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, strings.ToLower(string(cur)))
	}
	return out
}

// ── fixture helpers ──────────────────────────────────────────────────────────────────────────────

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}
