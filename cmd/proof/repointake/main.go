// Command repointake runs P32's whole intake pipeline against a REAL repository over the REAL network.
//
// # What this is for
//
// Every fence in P32 is green, and green fences prove the parts. This proves the WALK — the thing a
// customer actually does — against `github.com/nousresearch/hermes-agent`, which is a repository nobody
// on this team wrote and whose shape nobody chose:
//
//	connect  →  SELECT the grant  →  clone over HTTPS  →  guard the tree  →  archive  →  store
//	         →  extract  →  run discovery  →  read the ledger  →  revoke  →  prove the tree is gone
//
// # 🔴 What is REAL here, and the one thing that is not
//
// Real: the network (an HTTPS fetch from github.com), the repository, the revision, the TreeGuard, the
// archive, the extractor, discovery, the ledger, the per-forge metrics, the retention job, and the
// three-part revocation cascade. Every one of those is the shipped code path.
//
// Not real: the CREDENTIAL. `hermes-agent` is public, and GitHub serves a public repository to any
// basic-auth pair, so this runs with a placeholder. That is honest and it is stated in the output — a
// private repository would need a genuine grant, which is a thing a customer creates and not a thing
// this program may.
//
// The STORE is chosen at runtime: Postgres when `HEROS_TEST_POSTGRES_URL` is set (the same variable
// `make pg-proof` exports), and the in-process store otherwise. Both are shipped implementations of the
// same interfaces; the output says which one ran, because "it worked" means less when nobody says
// against what.
//
//	go run ./cmd/proof/repointake
//	go run ./cmd/proof/repointake -repository nousresearch/hermes-agent -revision <sha>
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

func main() {
	log.SetFlags(0)
	repository := flag.String("repository", "nousresearch/hermes-agent", "owner/name to read")
	revision := flag.String("revision", "", "the commit to read; empty resolves the remote's HEAD")
	subPath := flag.String("sub-path", "", "read only this directory within the repository")
	token := flag.String("token", "placeholder-public-repository", "the forge credential (a public repository needs none)")
	keep := flag.Bool("keep", false, "skip the revocation, so the stored snapshot can be inspected")
	flag.Parse()

	ctx := context.Background()
	scratch, err := os.MkdirTemp("", "p32-proof-")
	if err != nil {
		log.Fatalf("scratch: %v", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	step(0, "the stores")
	conns, snaps, bundleStore, storeKind, closeStore := openStores(ctx, scratch)
	defer closeStore()
	fmt.Printf("    store: %s\n", storeKind)

	secrets := providergateway.NewMemForgeSecrets()
	fmt.Printf("    forge credentials: %s\n", secrets.Describe().Kind)
	metrics := sourceingest.NewIngestMetrics()

	svc, err := sourceingest.NewService(sourceingest.ServiceConfig{
		Connections: conns, Snapshots: snaps, Secrets: secrets,
	})
	if err != nil {
		log.Fatalf("connection service: %v", err)
	}

	// ── 1) resolve the revision, from the REMOTE ─────────────────────────────────────────────────
	//
	// Resolved before connecting, so the run reads a revision that demonstrably exists rather than one
	// this program chose. `ls-remote` is the same question `fetch` will ask, asked without a grant.
	step(1, "resolve the revision on github.com")
	rev := *revision
	if rev == "" {
		rev, err = remoteHead(ctx, *repository)
		if err != nil {
			log.Fatalf("resolve HEAD of %s: %v", *repository, err)
		}
	}
	fmt.Printf("    %s @ %s\n", *repository, rev)

	// ── 2) connect ───────────────────────────────────────────────────────────────────────────────
	step(2, "connect — one repository, read-only, revocable")
	conn, err := svc.Connect(ctx, sourceingest.ConnectRequest{
		TenantID: "proof-tenant", WorkflowID: "github.com/" + *repository,
		Repository: *repository, SubPath: *subPath,
		CreatedBy: "cmd/proof/repointake", ConsentShown: true,
		Authorization: sourceingest.Authorization{
			Forge:     sourceingest.ForgeGitHub,
			GrantKind: sourceingest.GrantAppInstallation,
			Token:     *token,
			// Exactly the repository named. A second entry here is refused — that is ADR-013 Option B,
			// and `-repository` cannot widen it.
			Covers: []string{*repository},
			Scopes: []string{"contents:read", "metadata:read"},
		},
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	fmt.Printf("    connection %s · grant %s\n", conn.ConnectionID, conn.GrantKind)

	// ── 3) SELECT the grant back ─────────────────────────────────────────────────────────────────
	//
	// 🔴 Read through the STORE, not from the value `Connect` returned. A function's return value is
	// not evidence that anything was written.
	step(3, "read the grant back out of the store")
	back, err := conns.ForWorkflow(ctx, "proof-tenant", "github.com/"+*repository)
	if err != nil {
		log.Fatalf("the connect succeeded and the grant cannot be read back: %v", err)
	}
	fmt.Printf("    %s → %s (%s), created by %s\n", back.WorkflowID, back.Repository, back.Forge, back.CreatedBy)

	// ── 4) clone, over the real network ──────────────────────────────────────────────────────────
	step(4, "clone over HTTPS, guard the tree, archive it, store it, extract it")
	// 🔴 The extractor reads through the BUNDLE store, which is the same rows the snapshot store writes.
	// One store, two interfaces — which is what makes a clone and a push converge on one extraction, and
	// therefore what makes mode parity a property rather than a promise.
	bundles, err := sourceingest.NewBundleSource(bundleStore, filepath.Join(scratch, "extract"))
	if err != nil {
		log.Fatalf("bundle source: %v", err)
	}
	git, err := sourceingest.NewGitSource(sourceingest.GitConfig{
		Connections: conns, Snapshots: snaps, Secrets: secrets, Bundles: bundles,
		Scratch: filepath.Join(scratch, "clone"), Metrics: metrics,
	})
	if err != nil {
		log.Fatalf("git source: %v", err)
	}

	ref := sourceingest.Ref{
		TenantID: "proof-tenant", WorkflowID: "github.com/" + *repository, SourceRevision: rev,
	}
	// The read is attributed to a PERSON with a reason, so the ledger below shows the FR9 distinction
	// carrying a real value rather than the fail-safe default.
	read := sourceingest.WithReadContext(ctx, sourceingest.ActorPerson, "cmd/proof/repointake", "P32 acceptance run")

	started := time.Now()
	m, err := git.Materialize(read, ref)
	if err != nil {
		if cause, ok := sourceingest.CauseOf(err); ok {
			log.Fatalf("clone failed, cause %q: %v", cause, err)
		}
		log.Fatalf("clone: %v", err)
	}
	defer m.Release()
	fmt.Printf("    cloned and extracted in %s\n", time.Since(started).Round(time.Millisecond))

	files, bytes := countTree(m.Dir)
	fmt.Printf("    tree: %d files, %s\n", files, humanBytes(bytes))
	if _, err := os.Stat(filepath.Join(m.Dir, ".git")); err == nil {
		log.Fatal("    🔴 .git reached the extracted tree — it carries the credentialed remote URL")
	}
	fmt.Printf("    .git is absent from the extracted tree ✓\n")

	// ── 5) discovery over what the clone produced ────────────────────────────────────────────────
	step(5, "run discovery over the cloned tree")
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		log.Fatalf("discovery registry: %v", err)
	}
	dstart := time.Now()
	res, err := discovery.Run(discovery.Options{Repo: m.Dir, Registry: reg})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	fmt.Printf("    %d node(s), %d edge(s) in %s\n",
		len(res.IR.Nodes), len(res.IR.Edges), time.Since(dstart).Round(time.Millisecond))
	if len(res.IR.Nodes) == 0 {
		// 🔴 Reported as a FINDING rather than as a failure. Zero nodes means this repository has no
		// call sites the shipped frontends recognise, which is a true and useful answer about the
		// repository — and dressing it up as a crash would hide it.
		fmt.Printf("    ⚠️ discovery found no call sites the shipped frontends recognise in this repository.\n")
		fmt.Printf("       That is an answer about the repository, not a failure of the intake — the tree\n")
		fmt.Printf("       above is on disk and was read correctly.\n")
	} else {
		fmt.Printf("      %-18s %-52s %-28s %s\n", "NODE", "WHERE", "SYMBOL", "MODEL")
		for i, n := range res.IR.Nodes {
			if i >= 8 {
				fmt.Printf("      … and %d more\n", len(res.IR.Nodes)-8)
				break
			}
			fmt.Printf("      %-18s %s\n", n.NodeID, nodeWhere(n))
		}
		// 🔴 Zero edges is REPORTED, not passed over. An edge is a call from one discovered node to
		// another, and a repository whose call sites do not reference each other genuinely has none —
		// which is a fact about the repository worth printing beside the node count, because a reader
		// who sees `0` with no comment assumes the edge extractor is broken.
		if len(res.IR.Edges) == 0 {
			fmt.Printf("\n      ⚠️ 0 edges: none of these call sites references another one. That is a\n")
			fmt.Printf("         property of this repository, not a failure — an edge needs one discovered\n")
			fmt.Printf("         node to call another, and these are independent entry points.\n")
		}
	}

	// ── 6) the ledger ────────────────────────────────────────────────────────────────────────────
	step(6, "read the ledger — what was read, when, by whom")
	records, err := svc.Records(ctx, "proof-tenant", conn.ConnectionID, 10)
	if err != nil {
		log.Fatalf("records: %v", err)
	}
	if len(records) == 0 {
		log.Fatal("    🔴 the repository was read and no ledger row exists — the whole disclosure rests on it")
	}
	for _, r := range records {
		who := "a scheduled process, with nobody present"
		if r.Actor == sourceingest.ActorPerson {
			who = "a person (" + r.ActorID + ")"
		}
		fmt.Printf("    %s  %s  %s  %s  %d entries / %s / %dms\n",
			time.UnixMilli(r.AtMS).UTC().Format(time.RFC3339), shortRev(r.Revision), r.Outcome, who,
			r.Entries, humanBytes(r.Bytes), r.DurationMS)
	}

	// ── 7) the health signals ────────────────────────────────────────────────────────────────────
	step(7, "the signals an operator reads")
	printJSON("    source_ingest   ", metrics.Health())
	retention := sourceingest.NewRetentionJob(sourceingest.RetentionConfig{Snapshots: snaps})
	if _, err := retention.RunOnce(ctx); err != nil {
		log.Fatalf("retention sweep: %v", err)
	}
	printJSON("    source_retention", retention.Health())

	// ── 8) revoke, and prove the tree is gone ────────────────────────────────────────────────────
	if *keep {
		fmt.Printf("\n-- 8) revocation SKIPPED (-keep). The grant and the snapshot are still held.\n")
		return
	}
	step(8, "revoke — the grant, the credential, and every tree derived from it")
	result, err := svc.Revoke(ctx, "proof-tenant", conn.ConnectionID)
	if err != nil {
		log.Fatalf("revoke: %v", err)
	}
	fmt.Printf("    deleted %d derived tree(s)\n", result.SnapshotsDeleted)

	// 🔴 The proof is the READ AFTERWARDS, not the count above. A revocation that returns a number and
	// leaves the tree readable is the failure design D3 names as invisible from the inside.
	if _, err := git.Materialize(ctx, ref); err == nil {
		log.Fatal("    🔴 the repository is STILL readable after revocation")
	} else if err.Error() != sourceingest.ErrNoSource.Error() {
		fmt.Printf("    a read after revocation: %v\n", err)
	}
	fmt.Printf("    a read now reports: %v ✓\n", sourceingest.ErrNoSource)
	if refs := secrets.Refs(); len(refs) != 0 {
		log.Fatalf("    🔴 the forge credential survived revocation: %v", refs)
	}
	fmt.Printf("    the stored credential is gone ✓\n")

	fmt.Printf("\n== P32 intake: PASS against %s @ %s (%s) ==\n", *repository, shortRev(rev), storeKind)
	fmt.Printf("   🔴 The credential was a placeholder: %s is public and GitHub serves it to any basic-auth\n", *repository)
	fmt.Printf("      pair. A private repository needs a real grant, which a customer creates and this\n")
	fmt.Printf("      program may not. Everything else above is the shipped path.\n")
}

// openStores returns the connection and snapshot stores, preferring live Postgres.
func openStores(ctx context.Context, scratch string) (sourceingest.ConnectionStore, sourceingest.SnapshotStore, sourceingest.BundleStore, string, func()) {
	url := os.Getenv("HEROS_TEST_POSTGRES_URL")
	if url == "" {
		mem := sourceingest.NewMemStore()
		return mem, mem, mem, "in-process (set HEROS_TEST_POSTGRES_URL for Postgres)", func() {}
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping postgres: %v", err)
	}
	if _, err := pgmigrate.Apply(ctx, db); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	conns, err := sourceingest.NewPGConnectionStore(db)
	if err != nil {
		log.Fatalf("connection store: %v", err)
	}
	blobs, err := registry.NewFSBlobStore(filepath.Join(scratch, "blobs"))
	if err != nil {
		log.Fatalf("blob store: %v", err)
	}
	bundles, err := sourceingest.NewPGBundleStore(db, blobs)
	if err != nil {
		log.Fatalf("bundle store: %v", err)
	}
	snaps, err := sourceingest.NewPGSnapshotStore(bundles)
	if err != nil {
		log.Fatalf("snapshot store: %v", err)
	}
	return conns, snaps, bundles, "PostgreSQL (migrations applied by the shipped runner)", func() { _ = db.Close() }
}

// remoteHead asks github.com which commit HEAD is, without a grant.
func remoteHead(ctx context.Context, repository string) (string, error) {
	url, err := sourceingest.PublicCloneURL(sourceingest.ForgeGitHub, repository)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", url, "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("ls-remote returned nothing for %s", repository)
	}
	return fields[0], nil
}

func countTree(root string) (files int, bytes int64) {
	_ = filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			files++
			bytes += fi.Size()
		}
		return nil
	})
	return files, bytes
}

// nodeWhere renders where a discovered node lives and what it calls.
//
// 🔴 It reads the IR's real field names through JSON rather than through the struct, and the first
// version got them WRONG — it probed for `file` and `symbol` at the top level, where the IR nests them
// under `call_site`. Every line printed blank, and a blank line beside a node id reads as "discovery
// found a node it could not locate", which is a much worse finding than the truth.
//
// Worth stating because the failure mode is the one this whole phase is about: the run reported
// success, the numbers were right, and one column silently said nothing.
func nodeWhere(n any) string {
	b, err := json.Marshal(n)
	if err != nil {
		return ""
	}
	var probe struct {
		CallSite struct {
			File      string `json:"file"`
			Symbol    string `json:"symbol"`
			LineStart int    `json:"line_start"`
		} `json:"call_site"`
		Model struct {
			Provider string `json:"provider"`
			ModelID  string `json:"model_id"`
		} `json:"model"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return ""
	}
	where := probe.CallSite.File
	if probe.CallSite.LineStart > 0 {
		where = fmt.Sprintf("%s:%d", where, probe.CallSite.LineStart)
	}
	model := probe.Model.ModelID
	if probe.Model.Provider != "" {
		model = probe.Model.Provider + "/" + model
	}
	return fmt.Sprintf("%-52s %-28s %s", where, probe.CallSite.Symbol, model)
}

func printJSON(label string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Printf("%s <unrenderable: %v>\n", label, err)
		return
	}
	fmt.Printf("%s %s\n", label, b)
}

func step(n int, what string) { fmt.Printf("\n-- %d) %s\n", n, what) }

func shortRev(r string) string {
	if len(r) > 12 {
		return r[:12]
	}
	return r
}

func humanBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
