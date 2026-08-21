//go:build pgproof

// P32 §7.10 (part 2 of 2) — the LIVE CLONE, against real git and real Postgres.
//
// # Why the clone half is here rather than in internal/api
//
// §7.10 says "clone → assert files on disk at the expected revision → run discovery → assert nodes",
// and a stubbed clone would let that pass with `GitSource` writing nothing at all — which is precisely
// the shape the requirement exists to refuse. So this uses REAL git against a REAL repository.
//
// Pointing it at one needed a seam, and the obvious seam was rejected: a `GitConfig` field naming an
// alternative remote is a way for a caller to redirect where a clone goes, which is a security-relevant
// widening bought with a test's convenience. Instead this file does what `cmd/heroslocallink` does for
// the run-link transport — *"it redirects the DIAL, not the URL"*:
//
//	git -c url.<local path>.insteadOf=https://github.com/acme/api.git
//
// The production code still builds `https://github.com/acme/api.git`, still embeds the credential in
// it, still runs every guard and every store write. Git resolves that URL to a path on disk. The pin is
// exercised rather than bypassed, and the ONLY thing not covered is the network — which is what the
// per-forge `DurationMaxMS` metric on `/readyz` answers from production.
//
// That is possible here and not in `internal/api` because `runGit` is unexported, so the redirect is
// reachable from this package's own tests and from nowhere else.
package sourceingest

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

func acceptanceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := pgtest.Open("p32_clone")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	for _, stmt := range []string{
		`DELETE FROM source_clone_record`,
		`DELETE FROM source_bundle`,
		`DELETE FROM source_connection`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
	return db
}

// fixtureGit runs git hermetically, skipping when git is unavailable.
func fixtureGit(t *testing.T, dir string, args ...string) string {
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

// realRepository builds a repository with one commit and returns its path and revision.
//
// The content is something DISCOVERY can classify, so the last leg of §7.10's walk has a node to find
// rather than an empty graph that would satisfy a weaker assertion.
func realRepository(t *testing.T) (dir, revision string) {
	t.Helper()
	dir = t.TempDir()
	writeFixture(t, filepath.Join(dir, "agent", "router.py"),
		"import anthropic\n\nclient = anthropic.Anthropic()\n\n"+
			"def route(question):\n"+
			"    return client.messages.create(\n"+
			"        model=\"claude-3-5-sonnet-20241022\",\n"+
			"        max_tokens=512,\n"+
			"        messages=[{\"role\": \"user\", \"content\": question}],\n"+
			"    )\n")
	writeFixture(t, filepath.Join(dir, "README.md"), "# fixture\n")
	fixtureGit(t, dir, "init", "--quiet")
	fixtureGit(t, dir, "add", "-A")
	fixtureGit(t, dir, "commit", "--quiet", "--no-gpg-sign", "-m", "fixture")
	revision = fixtureGit(t, dir, "rev-parse", "HEAD")
	if len(revision) != 40 {
		t.Fatalf("rev-parse returned %q, want a 40-character sha", revision)
	}
	return dir, revision
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// redirectedGit wraps the REAL runner, resolving the pinned forge URL to a local repository.
//
// 🔴 It calls `execGit` — the shipped runner, with the shipped hermetic environment — and adds two
// config flags. Everything the production path does, it does: `init`, `remote add` with the
// credentialed URL, `fetch --depth 1` of the exact revision, `checkout FETCH_HEAD`.
//
// # 🔴 The redirect key is built by `CloneURL`, and the first version got this wrong in an instructive way
//
// It keyed on `https://github.com/acme/api.git` — the PUBLIC form. The production path builds the
// CREDENTIALED form (`https://x-access-token:<token>@github.com/…`), git's `insteadOf` matches on URL
// prefix, and the two do not share one. So the redirect silently did not apply and **git went to the
// real github.com**, which answered 403 and the test reported `credential_rejected`.
//
// Two things follow, and both are worth keeping:
//
//   - the failure was LOUD and correct — the classifier read a real GitHub refusal and named the right
//     cause, which is the first evidence that leg works against the real thing;
//   - the fix is to build the key with `CloneURL` itself, so this test asserts that the URL production
//     hands to git is the URL this file expects. A hand-written key would drift the moment the basic-auth
//     username changed, and the drift would look exactly like the failure above.
func redirectedGit(t *testing.T, local, token string) gitRunner {
	t.Helper()
	credentialed, err := CloneURL(ForgeGitHub, "acme/api", "inst-1", token)
	if err != nil {
		t.Fatalf("clone url: %v", err)
	}
	return func(ctx context.Context, dir string, args ...string) (string, error) {
		redirect := append([]string{
			"-c", "url." + local + ".insteadOf=" + credentialed,
			// git refuses the `file` transport in some contexts by default; naming it explicitly keeps
			// this working across versions rather than mysteriously on one.
			"-c", "protocol.file.allow=always",
		}, args...)
		return execGit(ctx, dir, redirect...)
	}
}

// TestALiveCloneProducesFilesAtTheRevisionAndDiscoveryFindsThem is §7.10's clone and discovery legs.
func TestALiveCloneProducesFilesAtTheRevisionAndDiscoveryFindsThem(t *testing.T) {
	ctx := context.Background()
	db := acceptanceDB(t)
	repoDir, revision := realRepository(t)

	conns, err := NewPGConnectionStore(db)
	if err != nil {
		t.Fatalf("connection store: %v", err)
	}
	blobs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	bundleStore, err := NewPGBundleStore(db, blobs)
	if err != nil {
		t.Fatalf("bundle store: %v", err)
	}
	snaps, err := NewPGSnapshotStore(bundleStore)
	if err != nil {
		t.Fatalf("snapshot store: %v", err)
	}
	bundleSource, err := NewBundleSource(bundleStore, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	secrets := providergateway.NewMemForgeSecrets()
	svc, err := NewService(ServiceConfig{Connections: conns, Snapshots: snaps, Secrets: secrets})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	// ── connect, through the shipped lifecycle ───────────────────────────────────────────────────
	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t-clone", WorkflowID: "wf-clone", Repository: "acme/api",
		CreatedBy: "u-clone", ConsentShown: true,
		Authorization: Authorization{
			Forge: ForgeGitHub, GrantKind: GrantAppInstallation, ExternalID: "inst-1",
			Token: "ghs_live_probe_do_not_leak", Covers: []string{"acme/api"}, Scopes: []string{"contents:read"},
		},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	metrics := NewIngestMetrics()
	git, err := NewGitSource(GitConfig{
		Connections: conns, Snapshots: snaps, Secrets: secrets, Bundles: bundleSource,
		Scratch: t.TempDir(), Metrics: metrics,
	})
	if err != nil {
		t.Fatalf("git source: %v", err)
	}
	git.runGit = redirectedGit(t, repoDir, "ghs_live_probe_do_not_leak")

	// ── clone, and assert FILES ON DISK at the expected revision ─────────────────────────────────
	ref := Ref{TenantID: "t-clone", WorkflowID: "wf-clone", SourceRevision: revision}
	read := WithReadContext(ctx, ActorPerson, "u-clone", "p32 acceptance")
	m, err := git.Materialize(read, ref)
	if err != nil {
		t.Fatalf("the live clone failed: %v", err)
	}
	defer m.Release()

	got, err := os.ReadFile(filepath.Join(m.Dir, "agent", "router.py"))
	if err != nil {
		t.Fatalf("the cloned tree has no agent/router.py: %v", err)
	}
	if !strings.Contains(string(got), "claude-3-5-sonnet-20241022") {
		t.Errorf("the cloned file is not the committed one:\n%s", got)
	}
	// 🚫 `.git` is NOT in the tree handed downstream. The prune is a PRIVACY control as well as a
	// performance one: `.git/config` records the credentialed remote URL verbatim.
	if _, err := os.Stat(filepath.Join(m.Dir, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git reached the extracted tree (stat err = %v); it carries the remote URL", err)
	}
	// And nothing anywhere under the extracted tree carries the credential.
	assertTreeHasNoToken(t, m.Dir, "ghs_live_probe_do_not_leak")

	// ── the SNAPSHOT row, with its connection and its expiry ─────────────────────────────────────
	var snapConn sql.NullString
	var snapExpiry sql.NullInt64
	var snapSize int64
	if err := db.QueryRowContext(ctx,
		`SELECT connection_id, expires_at_ms, size_bytes FROM source_bundle
		  WHERE tenant_id = $1 AND workflow_id = $2 AND source_revision = $3`,
		ref.TenantID, ref.WorkflowID, ref.SourceRevision).Scan(&snapConn, &snapExpiry, &snapSize); err != nil {
		t.Fatalf("the clone succeeded and NO snapshot row exists: %v", err)
	}
	if !snapConn.Valid || snapConn.String != conn.ConnectionID {
		t.Errorf("the snapshot names connection %v, want %q — the cascade finds trees by this column",
			snapConn, conn.ConnectionID)
	}
	if !snapExpiry.Valid || snapExpiry.Int64 <= 0 {
		t.Errorf("the snapshot has no expiry (%v) — a derived tree held forever is the standing "+
			"capability ADR-013 bounded", snapExpiry)
	}
	if snapSize <= 0 {
		t.Errorf("size_bytes = %d for a tree with files", snapSize)
	}

	// ── the LEDGER row, with the actor FR9 exists to distinguish ─────────────────────────────────
	var actor, outcome, ledgerRev string
	var entries, bytesRead int64
	if err := db.QueryRowContext(ctx,
		`SELECT actor, outcome, revision, entries, bytes FROM source_clone_record WHERE connection_id = $1`,
		conn.ConnectionID).Scan(&actor, &outcome, &ledgerRev, &entries, &bytesRead); err != nil {
		t.Fatalf("the repository was READ and no ledger row exists: %v — the whole disclosure rests on it", err)
	}
	if actor != "person" || outcome != "succeeded" || ledgerRev != revision {
		t.Errorf("the ledger row is (%s, %s, %s), want (person, succeeded, %s)", actor, outcome, ledgerRev, revision)
	}
	if entries == 0 || bytesRead == 0 {
		t.Errorf("the ledger recorded %d entries / %d bytes for a tree that has files", entries, bytesRead)
	}

	// ── the per-forge METRIC recorded it (§7.7 at the live boundary) ─────────────────────────────
	h := metrics.Health()
	var github ForgeStats
	for _, s := range h.PerForge {
		if s.Forge == "github" {
			github = s
		}
	}
	if github.Succeeded != 1 {
		t.Errorf("github reports %d success(es) after one clone: %+v", github.Succeeded, github)
	}
	if github.Bytes == 0 {
		t.Errorf("github recorded 0 bytes for a snapshot of %d bytes", snapSize)
	}

	// ── DISCOVERY over the cloned tree — the last leg of §7.10 ───────────────────────────────────
	//
	// 🔴 Run over `m.Dir`, the tree the CLONE produced, not over the fixture directory. Running it on
	// the fixture would prove discovery works and prove nothing about the clone.
	assertDiscoveryFindsNodes(t, m.Dir)

	// ── a second read of the SAME revision does not clone again ──────────────────────────────────
	//
	// The grant is a cost, and spending it twice for one immutable revision is the cost this phase
	// measures. Asserted through the LEDGER, which is where a second read would appear.
	m2, err := git.Materialize(read, ref)
	if err != nil {
		t.Fatalf("re-reading a stored snapshot failed: %v", err)
	}
	m2.Release()
	var reads int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM source_clone_record WHERE connection_id = $1`, conn.ConnectionID).Scan(&reads); err != nil {
		t.Fatalf("count reads: %v", err)
	}
	if reads != 1 {
		t.Errorf("%d reads were recorded for two Materialize calls at one revision; a revision is "+
			"immutable and the stored tree is the same bytes", reads)
	}

	// ── REVOKE → the tree is absent and a read reports ErrNoSource ───────────────────────────────
	res, err := svc.Revoke(ctx, "t-clone", conn.ConnectionID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if res.SnapshotsDeleted != 1 {
		t.Errorf("SnapshotsDeleted = %d, want 1", res.SnapshotsDeleted)
	}
	if _, err := git.Materialize(ctx, ref); !errors.Is(err, ErrNoSource) {
		t.Fatalf("a read after revocation = %v, want ErrNoSource", err)
	}
}

// TestARevisionThatIsNotInTheRepositoryReportsThatCause is §7.5's sibling, live.
//
// The forge is reachable and the credential is fine; only the revision is wrong. Reporting this as a
// credential problem would send the customer to rotate a working token.
func TestARevisionThatIsNotInTheRepositoryReportsThatCause(t *testing.T) {
	ctx := context.Background()
	db := acceptanceDB(t)
	repoDir, _ := realRepository(t)

	conns, _ := NewPGConnectionStore(db)
	blobs, _ := registry.NewFSBlobStore(t.TempDir())
	bundleStore, _ := NewPGBundleStore(db, blobs)
	snaps, _ := NewPGSnapshotStore(bundleStore)
	bundleSource, _ := NewBundleSource(bundleStore, t.TempDir())
	secrets := providergateway.NewMemForgeSecrets()
	svc, err := NewService(ServiceConfig{Connections: conns, Snapshots: snaps, Secrets: secrets})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t-clone", WorkflowID: "wf-missing", Repository: "acme/api",
		CreatedBy: "u", ConsentShown: true,
		Authorization: Authorization{
			Forge: ForgeGitHub, GrantKind: GrantAppInstallation,
			Token: "ghs_live_probe_do_not_leak", Covers: []string{"acme/api"},
		},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	git, err := NewGitSource(GitConfig{
		Connections: conns, Snapshots: snaps, Secrets: secrets, Bundles: bundleSource,
		Scratch: t.TempDir(), Metrics: NewIngestMetrics(),
	})
	if err != nil {
		t.Fatalf("git source: %v", err)
	}
	git.runGit = redirectedGit(t, repoDir, "ghs_live_probe_do_not_leak")

	// A well-formed sha that is not in the repository.
	missing := strings.Repeat("de", 20)
	_, err = git.Materialize(ctx, Ref{TenantID: "t-clone", WorkflowID: "wf-missing", SourceRevision: missing})
	cause, ok := CauseOf(err)
	if !ok {
		t.Fatalf("Materialize = %v, want a *CloneError", err)
	}
	if cause != CauseRevisionNotFound {
		t.Errorf("cause = %q, want %q — real git said so, and a credential cause would send the customer "+
			"to rotate a working token", cause, CauseRevisionNotFound)
	}
	// 🔴 The FAILURE is in the ledger too, with the cause as its outcome. A ledger of successes only
	// cannot answer "when did it start failing".
	var outcome string
	if err := db.QueryRowContext(ctx,
		`SELECT outcome FROM source_clone_record WHERE connection_id = $1`, conn.ConnectionID).Scan(&outcome); err != nil {
		t.Fatalf("the read FAILED and no ledger row exists: %v", err)
	}
	if outcome != string(CauseRevisionNotFound) {
		t.Errorf("the ledger recorded outcome %q, want %q", outcome, CauseRevisionNotFound)
	}
	// And no snapshot was written for a revision that does not exist.
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_bundle`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d snapshot row(s) exist after a failed clone", n)
	}
}

// TestACloneOfATreeWithAnEscapingSymlinkIsRefused is §7.1 through REAL git.
//
// 🔴 The unit fence builds the hostile tree by hand. This one commits a symlink to a real repository
// and lets git deliver it — which is the transport that actually exists, and the one a reader would
// otherwise have to take on trust.
func TestACloneOfATreeWithAnEscapingSymlinkIsRefused(t *testing.T) {
	ctx := context.Background()
	db := acceptanceDB(t)

	repoDir := t.TempDir()
	writeFixture(t, filepath.Join(repoDir, "ok.py"), "x = 1\n")
	if err := os.Symlink("/etc/shadow", filepath.Join(repoDir, "secrets")); err != nil {
		t.Skipf("this platform cannot create symlinks: %v", err)
	}
	fixtureGit(t, repoDir, "init", "--quiet")
	fixtureGit(t, repoDir, "add", "-A")
	fixtureGit(t, repoDir, "commit", "--quiet", "--no-gpg-sign", "-m", "hostile")
	revision := fixtureGit(t, repoDir, "rev-parse", "HEAD")

	conns, _ := NewPGConnectionStore(db)
	blobs, _ := registry.NewFSBlobStore(t.TempDir())
	bundleStore, _ := NewPGBundleStore(db, blobs)
	snaps, _ := NewPGSnapshotStore(bundleStore)
	bundleSource, _ := NewBundleSource(bundleStore, t.TempDir())
	secrets := providergateway.NewMemForgeSecrets()
	svc, err := NewService(ServiceConfig{Connections: conns, Snapshots: snaps, Secrets: secrets})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	if _, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t-clone", WorkflowID: "wf-hostile", Repository: "acme/api",
		CreatedBy: "u", ConsentShown: true,
		Authorization: Authorization{
			Forge: ForgeGitHub, GrantKind: GrantAppInstallation,
			Token: "ghs_live_probe_do_not_leak", Covers: []string{"acme/api"},
		},
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	git, err := NewGitSource(GitConfig{
		Connections: conns, Snapshots: snaps, Secrets: secrets, Bundles: bundleSource,
		Scratch: t.TempDir(), Metrics: NewIngestMetrics(),
	})
	if err != nil {
		t.Fatalf("git source: %v", err)
	}
	git.runGit = redirectedGit(t, repoDir, "ghs_live_probe_do_not_leak")

	_, err = git.Materialize(ctx, Ref{TenantID: "t-clone", WorkflowID: "wf-hostile", SourceRevision: revision})
	if err == nil {
		t.Fatal("a repository containing a symlink to /etc/shadow was ACCEPTED")
	}
	if !strings.Contains(err.Error(), "may not contain links") {
		t.Errorf("err = %v, want the link refusal", err)
	}
	// 🔴 And NOTHING was stored. A refusal that still wrote the snapshot would mean the guard reported
	// a problem and the platform kept the tree anyway.
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_bundle`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d snapshot row(s) exist after a REFUSED tree", n)
	}
}

// assertDiscoveryFindsNodes runs the real discovery over a tree and requires it to find something.
//
// 🔴 It requires NODES, not merely a successful run. Discovery over an empty directory succeeds and
// returns nothing, so "no error" would be satisfied by a clone that produced no files — the exact
// outcome §7.10's file assertions exist to catch, arriving one layer later.
func assertDiscoveryFindsNodes(t *testing.T, dir string) {
	t.Helper()
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		t.Fatalf("discovery registry: %v", err)
	}
	res, err := discovery.Run(discovery.Options{Repo: dir, Registry: reg})
	if err != nil {
		t.Fatalf("discovery over the cloned tree failed: %v", err)
	}
	if len(res.IR.Nodes) == 0 {
		t.Fatalf("discovery found NO nodes in the cloned tree at %s — the clone produced a tree "+
			"discovery cannot see, which every assertion above would still have passed", dir)
	}
}

// assertTreeHasNoToken walks a materialized tree and refuses to find the credential.
func assertTreeHasNoToken(t *testing.T, root, token string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if strings.Contains(string(b), token) {
			t.Errorf("the forge credential is in the extracted tree, at %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
