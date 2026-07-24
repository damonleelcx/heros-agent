// Command p2uidemo serves the bare P2 UI against a live Postgres seeded with one record of each
// terminal state (task 8.3: "drive the bare UI against a live run; confirm submit -> diff review ->
// per-node I/O -> terminal + error + empty + build-rejected states").
//
// It exists because those states cannot be verified by asserting on markup — a test that greps the
// HTML for "build-rejected" proves the string I wrote is the string I wrote. What has to be checked
// is that a real browser, fed real rows through the real API, renders four visibly different things.
// So this stands the whole path up and lets a human (or a browser driver) look at it.
//
// Not part of the shipped service: a demo harness. It seeds data, which nothing in production may do.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
	_ "github.com/lib/pq"
)

const (
	hashBuilt    = "1111111111111111111111111111111111111111111111111111111111111111"
	hashRejected = "2222222222222222222222222222222222222222222222222222222222222222"
	hashBaseline = "3333333333333333333333333333333333333333333333333333333333333333"
	// A Python transform from a repo that configures no type checker: built, and only syntax-checked.
	// It is here for the same reason build-rejected is — the state cannot be verified by grepping the
	// markup for a string I wrote. What has to be checked is that a real browser, fed a real row
	// through the real API, renders a syntax-checked diff VISIBLY DIFFERENTLY from a type-checked one.
	// That distinction is ADR-003 decision 3, and this is where a human can look at it.
	hashSyntaxOnly = "4444444444444444444444444444444444444444444444444444444444444444"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8477", "listen address")
	pgURL := flag.String("pg", os.Getenv("HEROS_TEST_POSTGRES_URL"), "postgres url")
	flag.Parse()
	if *pgURL == "" {
		log.Fatal("set -pg or HEROS_TEST_POSTGRES_URL")
	}

	db, err := sql.Open("postgres", *pgURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	for _, f := range []string{"0001_p0_lineage", "0002_p2_registries", "0003_p2_variant_spec",
		"0004_p2_transform", "0005_p2_run", "0006_p2_run_queue",
		"0007_p2_verification_strength"} {
		b, err := os.ReadFile("db/migrations/postgres/" + f + ".up.sql")
		if err != nil {
			log.Fatal(err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			log.Fatalf("%s: %v", f, err)
		}
	}

	fsBlobs, err := registry.NewFSBlobStore("/tmp/p2uidemo-blobs")
	if err != nil {
		log.Fatal(err)
	}
	// A CATALOGING store: node_execution's FK to blob(content_hash) requires every referenced blob to
	// have a catalog row, so writing bytes alone is not enough.
	blobs := registry.NewCatalogingBlobStore(db, fsBlobs, "application/json")
	if err := seed(ctx, db, blobs); err != nil {
		log.Fatal(err)
	}

	s := api.New(nil, config.Config{})
	s.MountP2(api.P2Stores{
		Transforms: worktree.NewStore(db, blobs),
		Runs:       executor.NewStore(db),
		Specs:      variantspec.NewStore(db),
	})
	// The embedded UI page this demo used to open was removed in the P9 cutover: the console is now a
	// separate component, so a demo can serve the read models but cannot serve the screen. It prints the
	// API it is serving and how to point the console at it — the same shape `cmd/p9hermes` uses.
	fmt.Printf("p2 API: http://%s\n\n", *addr)
	fmt.Printf("  cd web/console && PLATFORM_API_BASE=http://%s \\\n", *addr)
	fmt.Printf("    CONSOLE_PLATFORM_CREDENTIAL=demo CONSOLE_TENANT_IDENTITY=dev npm run dev\n\n")
	fmt.Printf("  then, at http://127.0.0.1:4320\n")
	fmt.Printf("    built + succeeded:  /app/transforms/%s/rev1  and  /app/runs/run_ok\n", hashBuilt)
	fmt.Printf("    build-rejected:     /app/transforms/%s/rev1\n", hashRejected)
	fmt.Printf("    halted:             /app/runs/run_halted\n")
	fmt.Printf("    baseline/empty:     /app/transforms/%s/rev1  and  /app/runs/run_empty\n", hashBaseline)
	fmt.Printf("    human review needed: /app/transforms/%s/rev1\n", hashSyntaxOnly)
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}

func seed(ctx context.Context, db *sql.DB, blobs *registry.CatalogingBlobStore) error {
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	if err := exec(`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		VALUES ('wf','local://demo','rev1','go','1.0.0') ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	if err := exec(`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v','wf','demo')
		ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	for _, h := range []string{hashBuilt, hashRejected, hashBaseline, hashSyntaxOnly} {
		if err := exec(`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
			VALUES ($1,'v','wf','1.0.0','{}') ON CONFLICT DO NOTHING`, h); err != nil {
			return err
		}
		if err := exec(`INSERT INTO variant_spec (config_hash, source_revision, spec_json)
			VALUES ($1,'rev1','{}') ON CONFLICT DO NOTHING`, h); err != nil {
			return err
		}
	}

	ts := worktree.NewStore(db, blobs)
	diff := []byte("--- a/pipeline.go\n+++ b/pipeline.go\n@@ -7,3 +7,3 @@\n func classify(client *anthropic.Client) {\n-\tclient.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeOpus4_6})\n+\tclient.Messages.New(nil, anthropic.MessageNewParams{Model: \"claude-sonnet-5\"})\n }\n")
	if err := ts.Put(ctx, &worktree.Applied{
		ConfigHash: hashBuilt, SourceRevision: "rev1", Dir: "/wt/built",
		Branch: "variant/111111111111", Commit: "abc1234", Diff: diff, Status: worktree.StatusBuilt,
		Strength: worktree.StrengthTypeChecked, VerifierTool: "go build ./...",
	}, nil); err != nil {
		return err
	}
	rejLog := "./pipeline.go:8:62: cannot use \"claude-sonnet-5\" (untyped string constant) as anthropic.Model value"
	if err := ts.Put(ctx, &worktree.Applied{
		ConfigHash: hashRejected, SourceRevision: "rev1", Dir: "/wt/rejected",
		Branch: "variant/222222222222", Commit: "def5678", Diff: diff,
		Status: worktree.StatusBuildRejected, BuildLog: rejLog,
		Strength: worktree.StrengthTypeChecked, VerifierTool: "go build ./...",
	}, &worktree.BuildRejection{ConfigHash: hashRejected, NodeID: "n_classify", Dim: "model", Log: rejLog}); err != nil {
		return err
	}
	// The baseline: no overrides, so no diff. Its own state, not a failure to load.
	if err := ts.Put(ctx, &worktree.Applied{
		ConfigHash: hashBaseline, SourceRevision: "rev1", Dir: "/wt/base",
		Branch: "variant/333333333333", Commit: "ba5e000", Status: worktree.StatusBuilt,
		Strength: worktree.StrengthTypeChecked, VerifierTool: "go build ./...",
	}, nil); err != nil {
		return err
	}

	// Built, but only syntax-checked: the same `built` badge as the first row, and a guarantee an order
	// of magnitude weaker. Before ADR-003 these two rows were indistinguishable to every consumer.
	pyDiff := []byte(`--- a/pipeline.py
+++ b/pipeline.py
@@ -7,3 +7,3 @@
 def classify(client):
-    return client.messages.create(model="claude-opus-4-8", messages=m)
+    return client.messages.create(model="claude-sonnet-5", messages=m)
`)
	if err := ts.Put(ctx, &worktree.Applied{
		ConfigHash: hashSyntaxOnly, SourceRevision: "rev1", Dir: "/wt/syntax",
		Branch: "variant/444444444444", Commit: "9a7f001", Diff: pyDiff, Status: worktree.StatusBuilt,
		Strength: worktree.StrengthSyntaxChecked, VerifierTool: "python3 -m py_compile",
	}, nil); err != nil {
		return err
	}

	es := executor.NewStore(db)
	// A succeeded run with per-node I/O.
	_ = es.Start(ctx, "run_ok", hashBuilt, "rev1", 42)
	for i, n := range []string{"n_classify", "n_resolve"} {
		in, _ := blobs.Put(ctx, []byte(fmt.Sprintf(`{"query":"ticket %d"}`, i)))
		out, _ := blobs.Put(ctx, []byte(fmt.Sprintf(`{"answer":"triaged %d"}`, i)))
		if err := es.RecordNode(ctx, "run_ok", executor.NodeResult{
			NodeID: n, AttemptGroup: 0, Status: executor.StatusSucceeded,
			InputBlobHash: in, OutputBlobHash: out,
			IdempotencyKey: executor.IdempotencyKey("run_ok", n, 0),
		}); err != nil {
			return err
		}
	}
	if err := es.Finish(ctx, &executor.Run{RunID: "run_ok", Status: executor.StatusSucceeded}); err != nil {
		return err
	}

	// A halted run — stopped by us on a contract violation, naming the node.
	_ = es.Start(ctx, "run_halted", hashBuilt, "rev1", 42)
	in, _ := blobs.Put(ctx, []byte(`{"query":"ticket"}`))
	out, _ := blobs.Put(ctx, []byte(`{"answer":42}`))
	if err := es.RecordNode(ctx, "run_halted", executor.NodeResult{
		NodeID: "n_classify", AttemptGroup: 0, Status: executor.StatusHalted,
		InputBlobHash: in, OutputBlobHash: out,
		IdempotencyKey: executor.IdempotencyKey("run_halted", "n_classify", 0),
		Error:          `output violates the node's io_contract: at '/answer': got number, want string`,
	}); err != nil {
		return err
	}
	if err := es.Finish(ctx, &executor.Run{RunID: "run_halted", Status: executor.StatusHalted,
		HaltedNodeID: "n_classify",
		HaltedReason: `output violates the node's io_contract: at '/answer': got number, want string`}); err != nil {
		return err
	}

	// A run with no node executions — the empty state.
	_ = es.Start(ctx, "run_empty", hashBaseline, "rev1", 1)
	return nil
}
