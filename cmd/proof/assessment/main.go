// Command assessment runs P33's whole assessment pipeline against a REAL repository over the REAL
// network.
//
// # What this is for
//
// Every fence in P33 is green and every one of them has been drilled red. Green fences prove the parts.
// This proves the WALK — the thing a customer actually gets — against
// `github.com/nousresearch/hermes-agent`, which is a repository nobody on this team wrote and whose
// shape nobody chose:
//
//	connect  →  clone over HTTPS  →  extract  →  discover  →  assess nine axes
//	         →  PERSIST  →  SELECT the findings back  →  assert nine axes and resolvable evidence
//	         →  assess AGAIN and prove the report is byte-identical  →  revoke
//
// # 🔴 The four things this run is designed to be able to REPORT BADLY
//
// A proof that can only succeed proves nothing. Each of these is a real possible outcome here, and the
// program prints it as a finding rather than as a crash:
//
//  1. **hermes-agent may have no call sites any shipped frontend recognises.** Then nine axes come back
//     `not_measured` naming what was missing, and that is the correct report.
//  2. **Its graph may have zero edges.** If the frontend that read it is syntactic, the finding names
//     the FRONTEND — never the repository as having a flat graph (design D6).
//  3. **`memory` and `harness` will be `not_measured`** on any repository, because a store read between
//     turns is not visible in a call site. That is the honest floor, not a defect.
//  4. **`loop` and `graph` will be `refused`** until P34 lands, and the refusal names P34.
//
// # 🚫 What is NOT exercised, stated where somebody reading the output is standing
//
// Inference and measurement. Both are built and neither is switched on: inference is gated on a
// holdout run that has not happened, and measurement needs the sandbox to execute customer code. So
// every finding below is STRUCTURAL, no provider is called, and nothing costs money. A run that
// pretended otherwise would be a demo of a fixture.
//
// The store is chosen at runtime: Postgres when `HEROS_TEST_POSTGRES_URL` is set, and the in-process
// path otherwise. The output says which, because "it worked" means less when nobody says against what.
//
//	go run ./cmd/proof/assessment
//	go run ./cmd/proof/assessment -repository nousresearch/hermes-agent -revision <sha>
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

const tenant = "proof-tenant"

func main() {
	log.SetFlags(0)
	repository := flag.String("repository", "nousresearch/hermes-agent", "owner/name to assess")
	revision := flag.String("revision", "", "the commit to assess; empty resolves the remote's HEAD")
	token := flag.String("token", "placeholder-public-repository", "the forge credential (a public repository needs none)")
	local := flag.String("local", "", "assess a directory already on disk instead of cloning")
	viewOut := flag.String("view-json", "", "write the CONSOLE's view of this assessment to a file")
	flag.Parse()

	ctx := context.Background()
	scratch, err := os.MkdirTemp("", "p33-proof-")
	if err != nil {
		log.Fatalf("scratch: %v", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	workflowID := "github.com/" + *repository

	step(0, "the stores")
	store, storeKind, closeStore := openAssessmentStore(ctx, scratch)
	defer closeStore()
	fmt.Printf("    assessments: %s\n", storeKind)

	// ── 1–3) get the source ──────────────────────────────────────────────────────────────────────
	var (
		tree string
		rev  string
		done func()
	)
	if *local != "" {
		// The escape hatch for a machine with no network. It reads a directory that is already there
		// and says so — a run that quietly fell back to a local copy would report a clone that never
		// happened.
		step(1, "read a LOCAL directory (no clone, no network)")
		tree = *local
		rev = "local-" + filepath.Base(*local)
		done = func() {}
		fmt.Printf("    %s\n", tree)
	} else {
		tree, rev, done = cloneReal(ctx, scratch, *repository, *revision, *token, workflowID)
	}
	defer done()

	// ── 4) discovery ─────────────────────────────────────────────────────────────────────────────
	step(4, "run discovery over the tree")
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		log.Fatalf("discovery registry: %v", err)
	}
	dstart := time.Now()
	res, err := discovery.Run(discovery.Options{Repo: tree, Registry: reg, WorkflowID: workflowID, CommitSHA: rev})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	fmt.Printf("    %d node(s), %d edge(s) in %s\n",
		len(res.IR.Nodes), len(res.IR.Edges), time.Since(dstart).Round(time.Millisecond))
	if len(res.Report.Frontends) == 0 {
		fmt.Printf("    ⚠️ no frontend contributed to this graph.\n")
	}
	for _, f := range res.Report.Frontends {
		fmt.Printf("    frontend: %-12s %-10s %d node(s), %d edge(s)\n", f.Language, f.AnalysisKind, f.Nodes, f.Edges)
	}

	// ── 5) assess ────────────────────────────────────────────────────────────────────────────────
	step(5, "assess nine axes — structural only, no provider call, no money")
	ir := res.IR
	subject := assessment.Subject{WorkflowID: workflowID, IR: &ir, Report: res.Report}

	metrics := assessment.NewMetrics()
	clock := func() int64 { return time.Now().UnixMilli() }
	runner, err := assessment.NewRunner(store, resolvesEverything{}, nil, clock, slog.New(slog.DiscardHandler))
	if err != nil {
		log.Fatalf("runner: %v", err)
	}
	runner = runner.WithMetrics(metrics)

	// 🔴 A RUN-SCOPED id, not a constant. The first version used `as-proof-1` and the second run of this
	// program failed — correctly — with *"the model finding was not written (0 rows)"*, because the
	// store refuses to silently overwrite a finding. That refusal is the product working: a conflict on
	// `(assessment_id, axis)` means either the report is being written twice with different content or
	// a column mismatch swallowed the row, and both look identical to a caller who ignores the count.
	//
	// The SHIPPED path never reaches it: `Service.Run` finds the pin and returns the stored report
	// before a runner is entered. This program calls the runner directly, so it mints its own ids.
	runID := time.Now().UTC().Format("20060102T150405")
	cfg := assessment.Config{
		AssessmentID:    "as-proof-" + runID + "-1",
		TenantID:        tenant,
		SourceRevision:  rev,
		AgentConfigHash: "structural-only",
		SpendCapUSD:     assessment.DefaultSpendCapUSD,
	}
	report, err := runner.Run(ctx, cfg, subject)
	if err != nil {
		log.Fatalf("assess: %v", err)
	}
	printReport(report)

	if *viewOut != "" {
		// 🔴 The SHIPPED view builder, not a copy. The ordering, the derived evidence path and the
		// `cannot fail` computation are exactly the parts a second renderer would reimplement
		// correctly and prove nothing about.
		b, err := json.MarshalIndent(api.AssessmentViewOf(report), "", "  ")
		if err != nil {
			log.Fatalf("render the console view: %v", err)
		}
		if err := os.WriteFile(*viewOut, b, 0o644); err != nil {
			log.Fatalf("write %s: %v", *viewOut, err)
		}
		fmt.Printf("    console view written to %s\n", *viewOut)
	}

	// ── 6) SELECT the findings back ──────────────────────────────────────────────────────────────
	//
	// 🔴 Read through the STORE, not from the value `Run` returned. A function's return value is not
	// evidence that anything was written, and §9.3's acceptance is exactly this walk: run → SELECT →
	// assert nine axes → assert each evidence reference resolves.
	step(6, "read the findings back out of the store, and check every evidence reference")
	back, ok, err := store.Get(ctx, tenant, report.AssessmentID)
	if err != nil || !ok {
		log.Fatalf("the assessment was produced and cannot be read back: ok=%v err=%v", ok, err)
	}
	if len(back.Findings) != len(assessment.Axes()) {
		log.Fatalf("🔴 the store holds %d findings, want %d — rows were swallowed while the run reported success",
			len(back.Findings), len(assessment.Axes()))
	}
	seen := map[assessment.Axis]bool{}
	for _, f := range back.Findings {
		seen[f.Axis()] = true
		if f.Evidence().Path() == "" {
			log.Fatalf("🔴 %s carries an evidence reference that resolves to no route", f.Axis())
		}
	}
	for _, axis := range assessment.Axes() {
		if !seen[axis] {
			log.Fatalf("🔴 %s is absent from the store", axis)
		}
	}
	fmt.Printf("    %d findings, nine axes, every evidence reference resolves to a route ✓\n", len(back.Findings))

	// ── 7) reproducibility ───────────────────────────────────────────────────────────────────────
	step(7, "assess the SAME revision again — the report must be byte-identical")
	again := cfg
	again.AssessmentID = "as-proof-" + runID + "-2"
	second, err := runner.Run(ctx, again, subject)
	if err != nil {
		log.Fatalf("second assessment: %v", err)
	}
	first, err := json.Marshal(stripIdentity(report))
	if err != nil {
		log.Fatal(err)
	}
	repeat, err := json.Marshal(stripIdentity(second))
	if err != nil {
		log.Fatal(err)
	}
	if string(first) != string(repeat) {
		log.Fatalf("🔴 two assessments of one revision differ:\n first: %s\nsecond: %s", first, repeat)
	}
	fmt.Printf("    identical, byte for byte ✓\n")
	fmt.Printf("    provider calls: 0 — this build makes none; inference is not wired\n")

	// ── 8) the health signal ─────────────────────────────────────────────────────────────────────
	step(8, "the health document an operator alerts on")
	h := metrics.Health()
	fmt.Printf("    started %d · completed %d · refused %d\n", h.Started, h.Completed, h.Refused)
	fmt.Printf("    nine-absence assessments: %d of %d (rate %.2f, alert above %.2f) → alerting: %v\n",
		h.AllNotMeasured, h.Completed, h.AllNotMeasuredRate, h.AlertAbove, h.Alerting)
	if h.Alerting {
		fmt.Printf("    ⚠️ ALERTING. Every axis came back not_measured on every run — which is the earliest\n")
		fmt.Printf("       signal that a language frontend or the sandbox broke, and it is invisible in a\n")
		fmt.Printf("       success rate: these runs completed, persisted nine rows, and errored nowhere.\n")
	}

	fmt.Printf("\n== P33 assessment: PASS against %s @ %s (%s) ==\n", *repository, shortRev(rev), storeKind)
	fmt.Printf("   🚫 No composite was produced, and none could be: there is no column, field or function\n")
	fmt.Printf("      that could carry one. What is above is nine findings and a tally.\n")
	fmt.Printf("   🚫 Every finding is STRUCTURAL. Inference is gated on a holdout run that has not\n")
	fmt.Printf("      happened, and measurement needs the sandbox to execute customer code — so no\n")
	fmt.Printf("      provider was called and nothing cost money. A run claiming otherwise would be a\n")
	fmt.Printf("      demo of a fixture.\n")
}

// printReport renders the nine, in evidence-strength order, with the tally above them.
func printReport(a assessment.Assessment) {
	t := a.Tally()
	fmt.Printf("    tally: %d measured · %d read from the code · %d not measured · %d refused (%d of them a model wrote)\n",
		t.Measured, t.Observed, t.NotMeasured, t.Refused, t.Inferred)
	fmt.Printf("    partial: %v · all-not-measured: %v · spend: $%.2f of $%.2f\n\n",
		a.Partial(), a.AllNotMeasured(), a.SpendUSD, a.SpendCapUSD)

	for _, f := range a.Ordered() {
		marker := "  "
		switch f.State() {
		case assessment.StateMeasured:
			marker = "◆ "
		case assessment.StateObserved:
			marker = "● "
		case assessment.StateNotMeasured:
			marker = "○ "
		case assessment.StateRefused:
			marker = "✖ "
		}
		origin := ""
		if f.Origin() == assessment.OriginInferred {
			origin = "  [inferred by " + f.ProviderModelVersion() + "]"
		}
		fmt.Printf("    %s%-9s %-13s%s\n", marker, f.Axis(), f.State(), origin)
		fmt.Printf("        %s\n", wrap(f.Claim(), 96, "        "))
		switch {
		case f.MissingInput() != "":
			fmt.Printf("        missing: %s\n", f.MissingInput())
		case f.RefusalCause() != "":
			fmt.Printf("        we lack: %s\n", f.RefusalCause())
		}
		fmt.Println()
	}
}

// stripIdentity removes the fields that legitimately differ between two runs — the id and the two
// timestamps — so the comparison is about the FINDINGS.
//
// 🔴 It is a struct copy with three fields cleared, NOT a re-marshal that drops keys. A comparison that
// dropped keys could pass while a field it did not know about differed.
func stripIdentity(a assessment.Assessment) assessment.Assessment {
	a.AssessmentID = ""
	a.StartedAtMS = 0
	a.CompletedAtMS = 0
	return a
}

// resolvesEverything is the evidence resolver for this run.
//
// 🔴 It says YES to everything, and that is a real limitation of this proof, stated rather than hidden.
// The shipped resolver asks the graph, board and scorecard sources whether the subject exists; this
// program mounts none of them, so it cannot ask. What step 6 checks instead is that every reference is
// well-formed and RESOLVES TO A ROUTE — which is the half a standalone binary can honestly prove. The
// other half is proved by `TestAnUnresolvableEvidenceReferenceFailsTheWrite` and by the console.
type resolvesEverything struct{}

func (resolvesEverything) Resolves(context.Context, string, assessment.EvidenceRef) (bool, error) {
	return true, nil
}

// cloneReal performs the P32 intake and returns the extracted tree.
func cloneReal(ctx context.Context, scratch, repository, revision, token, workflowID string) (string, string, func()) {
	step(1, "resolve the revision on github.com")
	rev := revision
	if rev == "" {
		var err error
		rev, err = remoteHead(ctx, repository)
		if err != nil {
			log.Fatalf("resolve HEAD of %s: %v", repository, err)
		}
	}
	fmt.Printf("    %s @ %s\n", repository, rev)

	step(2, "connect — one repository, read-only, revocable")
	mem := sourceingest.NewMemStore()
	secrets := providergateway.NewMemForgeSecrets()
	svc, err := sourceingest.NewService(sourceingest.ServiceConfig{
		Connections: mem, Snapshots: mem, Secrets: secrets,
	})
	if err != nil {
		log.Fatalf("connection service: %v", err)
	}
	conn, err := svc.Connect(ctx, sourceingest.ConnectRequest{
		TenantID: tenant, WorkflowID: workflowID, Repository: repository,
		CreatedBy: "cmd/proof/assessment", ConsentShown: true,
		Authorization: sourceingest.Authorization{
			Forge: sourceingest.ForgeGitHub, GrantKind: sourceingest.GrantAppInstallation,
			Token: token, Covers: []string{repository}, Scopes: []string{"contents:read", "metadata:read"},
		},
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	fmt.Printf("    connection %s · grant %s\n", conn.ConnectionID, conn.GrantKind)

	step(3, "clone over HTTPS, guard the tree, archive it, extract it")
	bundles, err := sourceingest.NewBundleSource(mem, filepath.Join(scratch, "extract"))
	if err != nil {
		log.Fatalf("bundle source: %v", err)
	}
	git, err := sourceingest.NewGitSource(sourceingest.GitConfig{
		Connections: mem, Snapshots: mem, Secrets: secrets, Bundles: bundles,
		Scratch: filepath.Join(scratch, "clone"), Metrics: sourceingest.NewIngestMetrics(),
	})
	if err != nil {
		log.Fatalf("git source: %v", err)
	}
	read := sourceingest.WithReadContext(ctx, sourceingest.ActorPerson, "cmd/proof/assessment", "P33 acceptance run")
	started := time.Now()
	m, err := git.Materialize(read, sourceingest.Ref{TenantID: tenant, WorkflowID: workflowID, SourceRevision: rev})
	if err != nil {
		if cause, ok := sourceingest.CauseOf(err); ok {
			log.Fatalf("clone failed, cause %q: %v", cause, err)
		}
		log.Fatalf("clone: %v", err)
	}
	files, bytes := countTree(m.Dir)
	fmt.Printf("    cloned and extracted in %s — %d files, %s\n",
		time.Since(started).Round(time.Millisecond), files, humanBytes(bytes))
	return m.Dir, rev, m.Release
}

// openAssessmentStore returns the assessment store, preferring live Postgres.
//
// 🔴 There is NO in-memory assessment store in the product, and this program does not invent one: a
// second Store implementation written for a proof is a second thing to keep correct, and the one place
// it would differ is exactly the place this run is trying to prove (the write actually landed). Without
// Postgres the run reports that it cannot do steps 6 and 7 and says so, rather than doing a weaker
// version of them under the same heading.
func openAssessmentStore(ctx context.Context, scratch string) (*assessment.PGStore, string, func()) {
	url := os.Getenv("HEROS_TEST_POSTGRES_URL")
	if url == "" {
		log.Fatalf("HEROS_TEST_POSTGRES_URL is unset.\n\n"+
			"This proof PERSISTS an assessment and reads it back, because a return value is not evidence\n"+
			"of a write. There is deliberately no in-memory assessment store to fall back to: writing one\n"+
			"for a proof would give the run a code path no deployment uses, and the place it would differ\n"+
			"is exactly the place this run exists to check.\n\n"+
			"Run it through `make assessment-hermes`, which stands up an ephemeral Postgres for you.\n%s", scratch)
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
	store, err := assessment.NewPGStore(db)
	if err != nil {
		log.Fatalf("assessment store: %v", err)
	}
	return store, "PostgreSQL (migrations applied by the shipped runner)", func() { _ = db.Close() }
}

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

// wrap re-flows a claim so a long sentence does not run off the terminal.
func wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	var out []string
	line := ""
	for _, w := range words {
		if line == "" {
			line = w
			continue
		}
		if len(line)+1+len(w) > width {
			out = append(out, line)
			line = w
			continue
		}
		line += " " + w
	}
	if line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n"+indent)
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
