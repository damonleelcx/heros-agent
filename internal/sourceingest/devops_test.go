package sourceingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// devops_test.go is P32 §5: the egress declaration, and the benchmark.
//
// # Why the egress fence compares a COMMENT to a function
//
// The k8s rule is an `ipBlock` — L3/L4 policy has no DNS, so the manifest physically cannot name
// `github.com`. That makes the real restriction an APPLICATION one (`CloneHosts()`), and it makes the
// manifest's comment the only place an operator can read what the platform reaches.
//
// A comment nothing checks is a comment that goes stale, and this one goes stale in the worst
// direction: a fourth forge added in Go widens what the platform pulls while the manifest still says
// three. So the fence reads both and requires they name the same set. It is an unusual thing to test
// and it is the honest shape given what the policy layer can express — stated here rather than left
// looking like an oddity.

// TestForgeHostsAreOnTheEgressAllowlist is task 5.1.
func TestForgeHostsAreOnTheEgressAllowlist(t *testing.T) {
	manifest := filepath.Join("..", "..", "deploy", "k8s", "base", "networkpolicy.yaml")
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read %s: %v", manifest, err)
	}
	body := string(b)

	// 1) The declaration exists and points at the function, so a reader can get from the manifest to
	//    the restriction in one step.
	if !strings.Contains(body, "sourceingest.CloneHosts()") {
		t.Fatal("networkpolicy.yaml does not reference sourceingest.CloneHosts(). Cloning is a NEW EGRESS " +
			"CLASS and is not implicitly permitted because it is git — an operator reading this file must " +
			"be able to see that the platform reaches code hosts.")
	}

	// 2) Every host the application can clone from is NAMED in the manifest.
	for _, host := range CloneHosts() {
		if !strings.Contains(body, host) {
			t.Errorf("networkpolicy.yaml does not name %q, which sourceingest can clone from. A forge "+
				"added in Go without touching the manifest widens what the platform pulls while the "+
				"manifest still says otherwise.", host)
		}
	}

	// 3) 🔴 And the reverse. A host named in the manifest that the application CANNOT reach is a stale
	//    claim in the opposite direction — it tells a security reviewer the platform talks to something
	//    it does not, which is how a real removal gets missed later.
	for _, candidate := range []string{"github.com", "gitlab.com", "bitbucket.org", "codeberg.org", "gitea.com", "sr.ht"} {
		named := strings.Contains(body, candidate)
		reachable := false
		for _, h := range CloneHosts() {
			if h == candidate {
				reachable = true
			}
		}
		if named && !reachable {
			t.Errorf("networkpolicy.yaml names %q and sourceingest cannot clone from it — the manifest "+
				"claims an egress the application does not have", candidate)
		}
	}

	// 4) The bootstrap-vm substrate carries the same declaration, because a deployment that is not
	//    Kubernetes has the same question and the same answer.
	vm := filepath.Join("..", "..", "deploy", "scripts", "bootstrap-vm.sh")
	if vb, verr := os.ReadFile(vm); verr == nil {
		for _, host := range CloneHosts() {
			if !strings.Contains(string(vb), host) {
				t.Errorf("bootstrap-vm.sh does not name %q. The two substrates must document the same "+
					"egress, or an operator on one of them is reading a different product.", host)
			}
		}
	}
}

// TestTheBenchmarkRepositoryIngestsWithinBudget is task 5.5.
//
// # 🔴 What "within budget" means here, and what this can honestly measure
//
// PRD §7.4 makes 30,500 files *"the demo's own benchmark, so it is the floor, not the ceiling."* Two
// things about measuring it are worth stating before the number:
//
//   - **A wall-clock budget on CI measures the runner.** A shared, noisy, throttled box will produce a
//     different number every run, and a fence whose answer moves without the commit moving is not a
//     fence. So the assertion is on WORK — the guard admits every entry, the archive round-trips, and
//     the counters are right — plus a duration reported as a LOG LINE for a human to read, not a
//     threshold to fail on.
//   - **The network is not measured.** A clone's duration is dominated by the fetch, which is the
//     forge's speed and the operator's link. That is what the per-forge `DurationMaxMS` metric on
//     /readyz exists to report from production; a unit test cannot stand in for it and should not
//     pretend to.
//
// What this DOES prove: the ceilings do not refuse a repository of this size, the guard's per-entry
// cost is linear, and the archive/extract round trip preserves it. Those are the three ways this phase
// could break a large repository, and all three are deterministic.
func TestTheBenchmarkRepositoryIngestsWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("the 30,500-file benchmark writes a large fixture; skipped under -short")
	}
	const files = 30_500

	root := t.TempDir()
	// 200 directories × ~152 files, which is the shape of a real monorepo rather than one flat
	// directory — a flat 30,500-entry directory is faster to walk than the real thing and would
	// flatter the result.
	const perDir = 153
	written := 0
	for d := 0; written < files; d++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%03d", d))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for i := 0; i < perDir && written < files; i++ {
			p := filepath.Join(dir, fmt.Sprintf("f%03d.go", i))
			if err := os.WriteFile(p, []byte("package p\n\nfunc F() {}\n"), 0o640); err != nil {
				t.Fatalf("write: %v", err)
			}
			written++
		}
	}

	start := time.Now()
	adm, err := NewTreeGuard().InspectTree(root, skipGitMetadata)
	guardMS := time.Since(start).Milliseconds()
	if err != nil {
		t.Fatalf("the %d-file benchmark repository was REFUSED: %v", files, err)
	}
	// Entries = files + the directories that hold them.
	if adm.Entries() < files {
		t.Errorf("the guard admitted %d entries for %d files — it is not seeing the whole tree", adm.Entries(), files)
	}
	if adm.Entries() > MaxBundleEntries {
		t.Fatalf("the benchmark's %d entries exceed MaxBundleEntries (%d) — the shipped ceiling refuses "+
			"the repository the PRD calls the FLOOR", adm.Entries(), MaxBundleEntries)
	}

	start = time.Now()
	archive, err := archiveTree(context.Background(), root, skipGitMetadata)
	archiveMS := time.Since(start).Milliseconds()
	if err != nil {
		t.Fatalf("archiving the benchmark repository failed: %v", err)
	}

	// The round trip: what was archived extracts back through the SAME extractor the bundle path uses,
	// which is the property mode parity rests on.
	store := NewMemStore()
	ref := Ref{TenantID: "t1", WorkflowID: "wf-bench", SourceRevision: "rev1"}
	if err := store.Put(context.Background(), ref, archive); err != nil {
		t.Fatalf("store: %v", err)
	}
	src, err := NewBundleSource(store, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	start = time.Now()
	m, err := src.Materialize(context.Background(), ref)
	extractMS := time.Since(start).Milliseconds()
	if err != nil {
		t.Fatalf("extracting the benchmark repository failed: %v", err)
	}
	defer m.Release()

	// Count what came back out. A round trip that silently dropped entries would pass every assertion
	// above — the archive would be smaller, the extract would succeed, and the tree would be wrong.
	back := 0
	if werr := filepath.Walk(m.Dir, func(_ string, fi os.FileInfo, werr error) error {
		if werr == nil && !fi.IsDir() {
			back++
		}
		return werr
	}); werr != nil {
		t.Fatalf("walk extracted: %v", werr)
	}
	if back != files {
		t.Errorf("%d files went in and %d came out — the archive/extract round trip is lossy", files, back)
	}

	// 🔴 REPORTED, not asserted. See this test's header for why a wall-clock threshold on CI would be
	// a fence measuring the runner. The per-forge duration metric on /readyz is where production
	// answers this question.
	t.Logf("benchmark %d files: guard %d ms, archive %d ms (%d bytes), extract %d ms",
		files, guardMS, archiveMS, len(archive), extractMS)
}

// TestTheThreeModesConvergeOnOneExtractor is §7.8's structural half.
//
// The full mode-parity fence (same tree → identical IR through all three modes) needs discovery and
// lives in the acceptance suite. What is assertable HERE is the property that makes it true rather than
// coincidental: the clone path does not have its own extractor. It archives and hands the bytes to the
// same `BundleSource` a push does.
func TestTheThreeModesConvergeOnOneExtractor(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "src", "app.ts"), "export const x = 1\n")

	archive, err := archiveTree(ctx, root, skipGitMetadata)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Mode 1: the same bytes arriving as a pushed bundle.
	store := NewMemStore()
	pushed := Ref{TenantID: "t1", WorkflowID: "wf-push", SourceRevision: "r1"}
	if err := store.Put(ctx, pushed, archive); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Mode 2: the same bytes arriving as a derived snapshot.
	cloned := Ref{TenantID: "t1", WorkflowID: "wf-clone", SourceRevision: "r1"}
	if err := store.PutDerived(ctx, cloned, archive, "conn-1", 1<<40); err != nil {
		t.Fatalf("put derived: %v", err)
	}

	src, err := NewBundleSource(store, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	trees := map[string]map[string]string{}
	for name, ref := range map[string]Ref{"bundle": pushed, "clone": cloned} {
		m, merr := src.Materialize(ctx, ref)
		if merr != nil {
			t.Fatalf("materialize %s: %v", name, merr)
		}
		files := map[string]string{}
		if werr := filepath.Walk(m.Dir, func(p string, fi os.FileInfo, werr error) error {
			if werr != nil || fi.IsDir() {
				return werr
			}
			rel, _ := filepath.Rel(m.Dir, p)
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			files[filepath.ToSlash(rel)] = string(b)
			return nil
		}); werr != nil {
			t.Fatalf("walk %s: %v", name, werr)
		}
		m.Release()
		trees[name] = files
	}

	if len(trees["bundle"]) == 0 {
		t.Fatal("the bundle mode produced an empty tree; this comparison would be vacuous")
	}
	if len(trees["bundle"]) != len(trees["clone"]) {
		t.Fatalf("bundle produced %d files and clone produced %d", len(trees["bundle"]), len(trees["clone"]))
	}
	for path, body := range trees["bundle"] {
		if trees["clone"][path] != body {
			t.Errorf("%s differs between the bundle and clone modes — the two paths do not converge on "+
				"one extractor, and mode parity is a coincidence rather than a property", path)
		}
	}
}
