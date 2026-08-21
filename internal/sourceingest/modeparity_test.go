package sourceingest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// modeparity_test.go is P32 §7.8: **the same tree through all three modes produces an identical IR.**
//
// # Why this needs discovery and not a byte comparison
//
// `TestTheThreeModesConvergeOnOneExtractor` already asserts the two stored modes produce the same
// FILES, which is the structural half. It is not the requirement. The requirement is about the IR,
// because that is what every consumer downstream actually reads — and the ways the modes could diverge
// are ways the files could be identical and the IR not:
//
//   - a path prefix (an extra directory level from an archive root) changes every node's file span;
//   - a mode bit or a mtime difference changes what a frontend decides to parse;
//   - the local path is the customer's own directory, and a discovery that leaked the absolute path
//     into the IR would make Mode 3's output differ from the other two by construction.
//
// The third is the one worth naming: it is not hypothetical, it is what "same tree, different root"
// does to any analysis that records where it found something.
//
// # Why all three modes and not two
//
// Mode 3 never produces a `Materialized` at all — the tree is read in place on the customer's machine.
// So its leg here runs discovery directly on the fixture directory, which is exactly what the local
// agent does. Leaving it out would test the two modes that already share an extractor and skip the one
// that does not.

// TestTheSameTreeThroughAllThreeModesProducesAnIdenticalIR is §7.8.
func TestTheSameTreeThroughAllThreeModesProducesAnIdenticalIR(t *testing.T) {
	ctx := context.Background()

	// One tree, on disk. Everything below reads THIS.
	origin := t.TempDir()
	writeParityFixture(t, origin)

	// ── Mode 3 · LOCAL. Discovery runs where the tree already is. ────────────────────────────────
	localIR := irOf(t, origin)

	// ── Mode 1 · BUNDLE. The tree is archived and pushed, then extracted by the shipped extractor. ─
	archive, err := archiveTree(ctx, origin, skipGitMetadata)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	store := NewMemStore()
	pushed := Ref{TenantID: "t", WorkflowID: "wf-bundle", SourceRevision: "rev1"}
	if err := store.Put(ctx, pushed, archive); err != nil {
		t.Fatalf("put: %v", err)
	}
	bundles, err := NewBundleSource(store, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	bundleM, err := bundles.Materialize(ctx, pushed)
	if err != nil {
		t.Fatalf("materialize bundle: %v", err)
	}
	defer bundleM.Release()
	bundleIR := irOf(t, bundleM.Dir)

	// ── Mode 2 · CLONE. Real git, from a real repository, through the whole GitSource path. ───────
	repo := t.TempDir()
	writeParityFixture(t, repo)
	parityGit(t, repo, "init", "--quiet")
	parityGit(t, repo, "add", "-A")
	parityGit(t, repo, "commit", "--quiet", "--no-gpg-sign", "-m", "fixture")
	revision := parityGit(t, repo, "rev-parse", "HEAD")

	secrets := providergateway.NewMemForgeSecrets()
	svc, err := NewService(ServiceConfig{Connections: store, Snapshots: store, Secrets: secrets})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	if _, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t", WorkflowID: "wf-clone", Repository: "acme/api",
		CreatedBy: "u", ConsentShown: true,
		Authorization: Authorization{
			Forge: ForgeGitHub, GrantKind: GrantAppInstallation,
			Token: parityToken, Covers: []string{"acme/api"},
		},
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	git, err := NewGitSource(GitConfig{
		Connections: store, Snapshots: store, Secrets: secrets, Bundles: bundles,
		Scratch: t.TempDir(), Metrics: NewIngestMetrics(),
	})
	if err != nil {
		t.Fatalf("git source: %v", err)
	}
	git.runGit = parityRedirect(t, repo)

	cloneM, err := git.Materialize(ctx, Ref{TenantID: "t", WorkflowID: "wf-clone", SourceRevision: revision})
	if err != nil {
		t.Fatalf("materialize clone: %v", err)
	}
	defer cloneM.Release()
	cloneIR := irOf(t, cloneM.Dir)

	// ── the comparison ───────────────────────────────────────────────────────────────────────────
	//
	// 🔴 The control first. Without it, a discovery that found NOTHING in all three would satisfy every
	// equality below — three empty IRs are identical, and that is the failure this whole phase is about.
	if localIR == "" || !strings.Contains(localIR, "\"nodes\"") {
		t.Fatalf("discovery over the fixture produced no IR — every comparison below would be vacuous:\n%s", localIR)
	}
	var probe struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(localIR), &probe); err != nil {
		t.Fatalf("decode IR: %v", err)
	}
	if len(probe.Nodes) == 0 {
		t.Fatal("discovery found NO nodes in the fixture — the parity assertions would compare three empty IRs")
	}

	if bundleIR != localIR {
		t.Errorf("the BUNDLE mode's IR differs from the LOCAL mode's.\n%s", firstDifference(localIR, bundleIR))
	}
	if cloneIR != localIR {
		t.Errorf("the CLONE mode's IR differs from the LOCAL mode's.\n%s", firstDifference(localIR, cloneIR))
	}
}

// parityToken is the credential the clone leg authorizes with.
const parityToken = "ghs_parity_probe_do_not_leak"

// writeParityFixture writes the tree every mode reads.
//
// Two languages and a nested directory, because the ways the modes could diverge are all path-shaped:
// a prefix from an archive root, a directory level lost in extraction, a frontend that decides what to
// parse from where a file sits.
func writeParityFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"agent/router.py": "import anthropic\n\nclient = anthropic.Anthropic()\n\n" +
			"def route(question):\n" +
			"    return client.messages.create(\n" +
			"        model=\"claude-3-5-sonnet-20241022\",\n" +
			"        max_tokens=512,\n" +
			"        messages=[{\"role\": \"user\", \"content\": question}],\n" +
			"    )\n",
		"agent/tools/search.py": "def search(q):\n    return []\n",
		"README.md":             "# parity fixture\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// irOf runs discovery over a tree and returns its IR as canonical JSON.
//
// 🔴 Compared as JSON rather than with reflect.DeepEqual, for a reason that is about the FAILURE
// message rather than about correctness: when two IRs differ, "not deep equal" sends somebody to a
// debugger, and a first-differing-line does not.
func irOf(t *testing.T, dir string) string {
	t.Helper()
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		t.Fatalf("discovery registry: %v", err)
	}
	res, err := discovery.Run(discovery.Options{Repo: dir, Registry: reg})
	if err != nil {
		t.Fatalf("discovery over %s: %v", dir, err)
	}
	b, err := json.MarshalIndent(res.IR, "", "  ")
	if err != nil {
		t.Fatalf("marshal IR: %v", err)
	}
	// The tree's own root is the ONE thing that legitimately differs between the three, because each
	// mode materializes into a different directory. Normalised away rather than compared, and named
	// here so nobody later reads its absence as an oversight: what §7.8 asks about is the IR's CONTENT,
	// and a path that is by definition per-materialization is not content.
	return strings.ReplaceAll(string(b), dir, "<root>")
}

// firstDifference renders the first differing line of two documents, with its neighbours.
func firstDifference(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var lw, lg string
		if i < len(w) {
			lw = w[i]
		}
		if i < len(g) {
			lg = g[i]
		}
		if lw != lg {
			return "  first difference at line " + itoa(i+1) + ":\n    local: " + lw + "\n    other: " + lg
		}
	}
	return "  the documents are equal (this message should be unreachable)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// parityGit runs git hermetically, skipping when it is unavailable.
func parityGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("git is unavailable or refused (%v): %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// parityRedirect resolves the pinned forge URL to a local repository, keyed on what `CloneURL` builds.
//
// The same mechanism the pgproof acceptance uses, and the same reason: production still builds the
// credentialed `https://github.com/…` URL and git resolves it to a path. See
// `p32_acceptance_pgproof_test.go` for why the key must come from `CloneURL` rather than be written out.
func parityRedirect(t *testing.T, local string) gitRunner {
	t.Helper()
	credentialed, err := CloneURL(ForgeGitHub, "acme/api", "", parityToken)
	if err != nil {
		t.Fatalf("clone url: %v", err)
	}
	return func(ctx context.Context, dir string, args ...string) (string, error) {
		return execGit(ctx, dir, append([]string{
			"-c", "url." + local + ".insteadOf=" + credentialed,
			"-c", "protocol.file.allow=always",
		}, args...)...)
	}
}
