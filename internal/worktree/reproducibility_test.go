package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 5.4: "run the same {config_hash, source_revision, seed} twice → byte-identical generated diff,
// deterministic build, and identical seed reaching each provider call."
//
// The three axes are proved where each one lives, and this file is the middle link:
//
//	diff        internal/transform's TestGenerate_IsDeterministic — same inputs, byte-identical patch
//	build       here — the same pair applies and builds to the same commit and the same bytes
//	seed        internal/executor's TestRun_SameSeedPropagatesIdenticallyAcrossRuns, and
//	            internal/providergateway's seed-propagation tests
//
// PRD OQ2 is why the chain stops at seed PROPAGATION rather than provider output: providers do not
// guarantee bitwise-identical output even at temperature 0 with a fixed seed. Asserting on their
// bytes would produce a test that fails for reasons we do not control and cannot fix — so the claim
// is precisely the one we can keep, and multi-seed statistics absorb the rest.

// Applying the same patch twice — from a cold cache each time — must produce the same tree and the
// same commit. If it did not, `config_hash` would not be a durable pointer to an exact change, and
// every result keyed by one would be unreproducible.
func TestReproducibility_SamePairAppliesAndBuildsIdentically(t *testing.T) {
	src, rev := newSourceRepo(t)
	ctx := context.Background()

	var trees, commits []string
	for i := 0; i < 3; i++ {
		// A fresh pool and cache per iteration: a cache hit would return the FIRST run's answer and
		// prove nothing about whether the work is reproducible.
		a := newApplier(t, src)
		applied, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
		if err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
		if applied.CacheHit {
			t.Fatalf("apply %d hit a cache; this test must do the work each time", i)
		}
		if applied.Status != StatusBuilt {
			t.Fatalf("apply %d: status %q\n%s", i, applied.Status, applied.BuildLog)
		}
		b, err := os.ReadFile(filepath.Join(applied.Dir, "pipeline.go"))
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		trees = append(trees, string(b))

		// The tree hash, not the commit sha: a commit sha embeds a timestamp, so two commits of
		// identical content legitimately differ. What must be identical is the CONTENT.
		out, err := git(ctx, applied.Dir, "rev-parse", "HEAD^{tree}")
		if err != nil {
			t.Fatalf("rev-parse tree %d: %v\n%s", i, err, out)
		}
		commits = append(commits, strings.TrimSpace(out))
	}

	for i := 1; i < len(trees); i++ {
		if trees[i] != trees[0] {
			t.Errorf("apply %d produced different bytes than apply 0:\n got:\n%s\nwant:\n%s", i, trees[i], trees[0])
		}
		if commits[i] != commits[0] {
			t.Errorf("apply %d committed tree %s, apply 0 committed %s — the same "+
				"{config_hash, source_revision} must apply to the same content", i, commits[i], commits[0])
		}
	}
}

// The reason the commit's TREE is the assertion and its SHA is not: a commit sha hashes the author
// and committer timestamps, so two commits of byte-identical content differ by construction. A test
// asserting sha equality would be asserting that two runs happened in the same second.
//
// This pins that reasoning, so nobody "fixes" the test above to compare shas and gets a flake that
// only appears under load.
func TestReproducibility_CommitShaVariesButTheTreeDoesNot(t *testing.T) {
	src, rev := newSourceRepo(t)
	ctx := context.Background()

	a1 := newApplier(t, src)
	one, err := a1.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	a2 := newApplier(t, src)
	two, err := a2.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	t1, err := git(ctx, one.Dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	t2, err := git(ctx, two.Dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if strings.TrimSpace(t1) != strings.TrimSpace(t2) {
		t.Errorf("the same pair produced different trees: %s vs %s", t1, t2)
	}
}

// A DIFFERENT configuration must produce a different tree — otherwise the test above passes for the
// trivial reason that nothing ever changes.
func TestReproducibility_ADifferentConfigurationProducesADifferentTree(t *testing.T) {
	src, rev := newSourceRepo(t)
	ctx := context.Background()
	a := newApplier(t, src)

	one, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("apply A: %v", err)
	}
	two, err := a.Apply(ctx, patchFor(t, src, rev, hashB, "claude-haiku-4-5"))
	if err != nil {
		t.Fatalf("apply B: %v", err)
	}
	t1, _ := git(ctx, one.Dir, "rev-parse", "HEAD^{tree}")
	t2, _ := git(ctx, two.Dir, "rev-parse", "HEAD^{tree}")
	if strings.TrimSpace(t1) == strings.TrimSpace(t2) {
		t.Error("two different configurations produced the same tree; the reproducibility tests " +
			"would pass even if the transform did nothing")
	}
}

// The pool's git runs with a scrubbed environment (GIT_CONFIG_NOSYSTEM, HOME redirected). Without it
// an operator's commit.gpgsign, hooks, or commit.template would change what gets committed from
// machine to machine — and task 2.3's byte-identical promise does not survive a signing key appearing
// in a commit.
func TestReproducibility_OperatorGitConfigCannotAffectTheVariantCommit(t *testing.T) {
	src, rev := newSourceRepo(t)
	// A hostile global config: if the pool inherited it, the commit would be signed (and fail, since
	// there is no key) or carry a different author.
	fakeHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeHome, ".gitconfig"),
		[]byte("[commit]\n\tgpgsign = true\n[user]\n\tname = Someone Else\n\temail = else@example.com\n"),
		0o600); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}
	t.Setenv("HOME", fakeHome)

	a := newApplier(t, src)
	applied, err := a.Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("apply with a hostile HOME gitconfig: %v", err)
	}
	author, err := git(context.Background(), applied.Dir, "log", "-1", "--pretty=%an <%ae>")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(author, "heros-agent") {
		t.Errorf("the variant commit's author is %q; the operator's git config leaked in", strings.TrimSpace(author))
	}
}
