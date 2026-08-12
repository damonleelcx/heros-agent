// Command customerconsole serves the P9 customer console's platform API against the REAL
// github.com/NousResearch/hermes-agent repository.
//
// It follows the convention the other phase demos established (p5hermes, p6hermes, p7hermes,
// p8hermes): point the phase at an actual checkout rather than a fixture, because a fixture proves
// the code path and nothing about any real codebase.
//
// # What is REAL here, and what is NOT MOUNTED — stated, not implied
//
// REAL: P1 discovery over the checkout (the call sites, symbols, files and context policies are read
// out of the hermes source), the P3.5 classifier over that IR, the write-back into the IR document,
// and the GraphView built from the WRITTEN document — so the console renders what a consumer would
// actually read out of a stored IR, not an in-memory result that never survived a round trip.
//
// NOT MOUNTED: P2 (configure / diff / run), P2.5 (live monitor), P4 (eval board), P4.5 (scorecard),
// P5.5 (proposals) and P7 (billing). Those read models require a fan-out against a provider, and this
// command does not stub one — a board assembled from invented numbers would be exactly the demo that
// overstates, which is a support and churn cost that lands after the sale.
//
// That is not a gap in the demo; it is the demo. Each unmounted subsystem answers **503 with a
// not-mounted body**, and the console renders *"This subsystem is not mounted on this deployment"* —
// distinct from a 404 and distinct from a transport failure. Seeing the three failure classes stay
// distinguishable across a real network hop is one of the properties P9 exists to guarantee, and this
// is where it is visible rather than asserted.
//
// To measure hermes for real, run the fan-out demo against the same IR and point the console at it:
//
//	go run ./cmd/discover   -repo /path/to/hermes-agent -workflow-id nousresearch/hermes-agent -out hermes-ir.json
//	go run ./cmd/demo/evalboard -ir hermes-ir.json
//
// # Usage
//
//	go run ./cmd/proof/customerconsole -repo /path/to/hermes-agent           # discovers, then serves
//	go run ./cmd/proof/customerconsole -ir hermes-ir.json                    # serves a pre-discovered IR
//
// Then run the console against it:
//
//	cd web/console && PLATFORM_API_BASE=http://127.0.0.1:4321 \
//	  CONSOLE_PLATFORM_CREDENTIAL=p9hermes-demo-credential-do-not-ship \
//	  CONSOLE_TENANT_IDENTITY=dev npm run dev
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/studio"
)

// workflowID is the identifier the console addresses this workflow by. It is the repository, because
// that is what the customer calls it.
//
// 🔴 A var with a default, not a const, and the default is only a default. This command's whole
// premise is "point it at an actual checkout", and it was pinned to one repository's name — so
// serving a DIFFERENT repository's IR mounted the graph under `nousresearch/hermes-agent` while the
// IR called itself something else. The console would then open the id the IR carries and be told
// **no such workflow**, for a workflow that had just been discovered, classified and mounted. That is
// the same false "the identifier does not resolve" this phase is careful about everywhere else.
//
// Resolution order: an explicit `-workflow-id` wins; otherwise a pre-discovered IR's own
// `workflow.id` wins, because for a file already on disk the file is authoritative; otherwise this.
var workflowID = "nousresearch/hermes-agent"

func main() {
	addr := flag.String("addr", "127.0.0.1:4321", "listen address for the platform API")
	repo := flag.String("repo", "", "path to a hermes-agent checkout; discovery runs over it")
	irPath := flag.String("ir", "", "path to an IR already emitted by cmd/discover (skips discovery)")
	placement := flag.String("placement", "disabled", "P30 placement for this organization: "+
		"platform | customer | disabled. The DEFAULT is `disabled`, which is what every real "+
		"organization has until an operator sets one — so the default run shows the state a customer "+
		"actually sees on day one")
	fixtureInference := flag.Bool("fixture-inference", false,
		"🔴 stamp a SYNTHETIC agent inference onto this graph so the inferred-edge treatment, the "+
			"mixed counts and the assessed narrative can be seen. It is a FIXTURE: no model was "+
			"consulted and nothing here is a claim about the repository being read. Off by default, "+
			"because a demo that shows invented findings as real ones is the demo that overstates")
	wantID := flag.String("workflow-id", "", "identifier the console addresses this workflow by "+
		"(default: the IR's own workflow.id, else nousresearch/hermes-agent)")
	flag.Parse()

	if *repo == "" && *irPath == "" {
		fmt.Fprintln(os.Stderr, "p9hermes: give -repo <checkout> or -ir <ir.json>")
		os.Exit(2)
	}

	// Resolved BEFORE anything mounts or discovers: discoverInto passes it to `cmd/discover
	// -workflow-id`, so deciding it later would key the mount and the IR differently.
	if *wantID != "" {
		workflowID = *wantID
	} else if *irPath != "" {
		if id, err := workflowIDIn(*irPath); err != nil {
			log.Fatalf("read %s: %v", *irPath, err)
		} else if id != "" {
			workflowID = id
		}
	}

	path, reportPath := *irPath, ""
	if path == "" {
		var err error
		path, reportPath, err = discoverInto(*repo)
		if err != nil {
			log.Fatalf("discovery over %s: %v", *repo, err)
		}
	}

	ir, result, account, err := loadAndClassify(context.Background(), path)
	if err != nil {
		log.Fatalf("classify %s: %v", path, err)
	}

	// Build the view from the WRITTEN document. The console must render what a consumer reads out of a
	// stored IR — an in-memory result that never round-tripped would hide exactly the write-back
	// defects this step exists to catch.
	labelled, err := patternclassifier.WriteBack(ir, result)
	if err != nil {
		log.Fatalf("write labels back into the IR: %v", err)
	}
	view := patternclassifier.BuildGraphView(labelled, result, loadReport(reportPath))

	// Register the console's credential → tenant, so a tenant-scoped read model (the studio matrix's
	// bindings) resolves a principal. The subject-keyed graph never needed one; the matrix does, because
	// a binding belongs to a tenant. The platform derives the tenant from the API key alone (there is no
	// X-Console-Tenant trust here), so this one line is what makes /api/p10 answer with a tenant.
	cfg := config.Config{
		// AuthMode "required" wires the auth middleware, which is what puts a tenant PRINCIPAL in the
		// request context. Without it the middleware never runs, so a tenant-scoped read model (the
		// studio bindings) sees no principal and answers 401 even for a valid key.
		AuthMode: "required",
		TenantCredentials: []config.TenantCredential{{
			TenantID: workflowID, APIKey: "p9hermes-demo-credential-do-not-ship", Role: "member", KeyID: "p9hermes",
		}},
	}
	// 🔴 P30 §8 — the SYNTHETIC inference, applied only under an explicit flag and labelled as such.
	//
	// Nothing about this comes from a model. It stamps `heros` authorship onto one edge and one label
	// already in the real graph, so the treatments workstream 8 introduces — an inferred edge's own
	// stroke and arrowhead, the composition's split counts, the `assessed` narrative — can be looked at
	// on a real workflow's shape. The flag's help text says so and the narrative below says so on the
	// page itself: a reader who runs this and screenshots it cannot accidentally present it as a finding.
	narrative := ""
	if *fixtureInference {
		narrative = stampFixtureInference(&view)
	}

	srv := api.New(nil, cfg)
	srv.MountPatternGraph(&graphSource{views: map[string]patternclassifier.GraphView{workflowID: view}})

	// P30 §8 — the agent panel. A REAL PlatformSource over in-memory stores: the placement comes from
	// the flag, and every refusal, sentence and state on the panel is the shipped code's, not a stub's.
	srv.MountHerosAgent(&demoAgentSource{
		placement: herosagent.Placement(*placement),
		narrative: narrative,
	})

	// P30 §9.1 · the agent's `/readyz` entry, resolved by DOING what an inference does. The stores are
	// in-memory here — that is what this demo honestly has — but the CHECK is the shipped one, so the
	// state, its sentence and the four-way distinction on the page are the real code's.
	demoPlacements := herosagent.NewMemPlacementStore()
	if herosagent.Placement(*placement) != herosagent.PlacementDisabled {
		if err := demoPlacements.Set(context.Background(), herosagent.TenantPlacement{
			TenantID: workflowID, Placement: herosagent.Placement(*placement),
			Reason: "set by the customerconsole proof's -placement flag", SetBy: "customerconsole",
		}); err != nil {
			log.Fatalf("demo placement: %v", err)
		}
	}
	demoCaps, cerr := herosagent.NewCapChecker(herosagent.NewMemCapStore(), herosagent.NewMemSpendStore(),
		func() int64 { return time.Now().UnixMilli() })
	if cerr != nil {
		log.Fatalf("demo cap checker: %v", cerr)
	}
	srv.SetAgentReadiness(func(ctx context.Context) herosagent.Readiness {
		return herosagent.Check(ctx, herosagent.ReadinessInput{
			Versions:   herosagent.NewMemVersionStore(),
			Placements: demoPlacements,
			// 🔴 A resolver that always FAILS, because this demo has no provider credential and never
			// calls one. Reporting `ready` here would be the assertion-from-configuration that task 9.1
			// exists to prevent — on the very binary somebody would use to check it.
			Credentials: demoUnresolvableCredential{},
			Caps:        demoCaps,
		})
	})

	// P10 Prompt & Model Studio MATRIX (P9 §11b) — mounted with the REAL discovered nodes as the
	// matrix COLUMNS, so the studio shows a model-per-node grid over the actual hermes call sites, not
	// a fixture. The ROWS are the model catalog, which is a provider list rather than customer data, so
	// a static set is honest. Test-run is deliberately NOT mounted (nil Runner): it needs a provider
	// fan-out, so the run action answers 503 — the same honest degradation the unmounted subsystems
	// below use, never an invented completion. Binding is real (an in-memory selection, "in force —
	// unverified", carrying no score). The Postgres-backed prompt registry (publish/timeline/diff) is
	// not mounted here; the matrix — the surface 11b is about — is.
	studioCat := studio.NewWorkflowCatalog()
	studioCat.Load(workflowID, labelled)
	srv.MountStudioMatrix(api.StudioMatrix{
		Store:     demoModels{},
		Workflows: studioCat,
		Binds:     studio.NewBindStore(),
		Runner:    nil,
	})

	// The other subsystems are mounted with NO SOURCE, deliberately — and this line is the whole
	// difference between an honest degradation and a misleading one.
	//
	// Leaving them unmounted entirely would leave their routes unregistered, so the mux would answer
	// **404** — and the console would render *"No such workflow"* for a workflow that plainly exists.
	// "This capability is not installed on this deployment" and "that identifier does not resolve" are
	// two different facts with two different next actions, and collapsing them is exactly the failure
	// R5 forbids. Registering the routes with a nil source makes each answer **503 with a not-mounted
	// body**, which is the truth.
	//
	// Found by opening the board in a browser and reading a 404 that should have been a 503.
	srv.MountConfigRuntime(api.ConfigRuntimeStores{})
	srv.MountMonitor(nil)
	srv.MountEvalBoard(nil)
	srv.MountScorecard(nil)
	srv.MountProposals(nil)
	srv.MountBilling(nil)

	fmt.Print(account)
	fmt.Printf("\nplatform API on http://%s\n", *addr)
	fmt.Printf("  MOUNTED     P3.5 pattern graph   GET /api/v1/workflows/%s/pattern-graph\n", workflowID)
	fmt.Printf("  MOUNTED     P10 studio matrix    GET /api/v1/workflows/%s/nodes  (real nodes; test-run 503)\n", workflowID)
	fmt.Printf("  NOT MOUNTED P2, P2.5, P4, P4.5, P5.5, P7 — each answers 503 with a not-mounted body,\n")
	fmt.Printf("              which the console renders as a subsystem that is absent on this deployment,\n")
	fmt.Printf("              distinct from a 404 and from a transport failure.\n\n")
	fmt.Printf("console:\n")
	fmt.Printf("  cd web/console && PLATFORM_API_BASE=http://%s \\\n", *addr)
	fmt.Printf("    CONSOLE_PLATFORM_CREDENTIAL=p9hermes-demo-credential-do-not-ship \\\n")
	fmt.Printf("    CONSOLE_TENANT_IDENTITY=dev npm run dev\n")
	fmt.Printf("  then open http://127.0.0.1:4320/app/workflows/%s/graph\n\n",
		strings.ReplaceAll(workflowID, "/", "%2F"))

	log.Fatal(http.ListenAndServe(*addr, srv.Handler))
}

// workflowIDIn reads just `workflow.id` out of an IR document.
//
// Separate from loadAndClassify because it has to answer before the mount key is chosen, and because
// a malformed IR should fail here — naming the file — rather than three steps later as an empty graph.
func workflowIDIn(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var doc struct {
		Workflow struct {
			ID string `json:"id"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse IR: %w", err)
	}
	return doc.Workflow.ID, nil
}

// discoverInto runs P1 discovery over the checkout and returns the IR path.
//
// It shells out to cmd/discover rather than calling the package directly, so what this command serves
// is byte-for-byte what a customer running the CLI would get. A second in-process code path would be
// a second thing that could differ from the shipped one.
func discoverInto(repo string) (irPath, reportPath string, err error) {
	out := filepath.Join(os.TempDir(), "p9hermes-ir.json")
	report := filepath.Join(os.TempDir(), "p9hermes-report.json")
	cmd := exec.Command("go", "run", "./cmd/discover",
		"-repo", repo, "-workflow-id", workflowID, "-out", out, "-report", report)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", "", err
	}
	return out, report, nil
}

// loadReport reads the discovery run report beside the IR.
//
// 🔴 A missing or unreadable report is NOT an error here and is NOT silently equivalent to a healthy
// one: the zero value flows into BuildGraphView, which renders "discovery recorded no contributing
// frontend" rather than inventing an explanation. That is the whole reason the report is a value
// parameter — see BuildGraphView.
func loadReport(path string) discovery.DiscoveryReport {
	var rep discovery.DiscoveryReport
	if path == "" {
		return rep
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return discovery.DiscoveryReport{}
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return discovery.DiscoveryReport{}
	}
	return rep
}

// loadAndClassify reads the IR and classifies it, returning a human-readable account of what was
// actually found.
//
// The account is the point. Running against a real repository surfaces facts a fixture never would —
// that P1 emits call sites with no edges because inter-node flow is P5's, that most models are
// unresolved because a syntactic frontend cannot follow a variable to its assignment — and a demo
// that printed only a node count would let a reader conclude the graph is more complete than it is.
func loadAndClassify(ctx context.Context, path string) (*discovery.IR, patternclassifier.Result, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, patternclassifier.Result{}, "", err
	}
	var ir discovery.IR
	if err := json.Unmarshal(raw, &ir); err != nil {
		return nil, patternclassifier.Result{}, "", fmt.Errorf("parse IR %s: %w", path, err)
	}

	// No skill registry is stood up here, so the honest answer to "does this tools_skills entry name a
	// skill that exists?" is NO for every name. A resolver that said yes would manufacture Tool Use
	// labels out of unresolvable strings — a false claim about the customer's workflow.
	opts := patternclassifier.Options{
		Skills: patternclassifier.NewStaticSkillResolver(),
		// No fallback model. With none configured the classifier consults nothing, `llm_calls` is 0,
		// and every label on the graph is rule-derived — which the console shows as
		// "0 LLM calls — fully rule-covered". That is a stronger claim than a model-assisted one, and
		// it is true here.
	}
	result, err := patternclassifier.Classify(ctx, &ir, opts)
	if err != nil {
		return nil, patternclassifier.Result{}, "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n=== %s ===\n", workflowID)
	fmt.Fprintf(&b, "  %d call site(s) discovered, %d edge(s)\n", len(ir.Nodes), len(ir.Edges))
	if len(ir.Edges) == 0 {
		fmt.Fprintf(&b, "  no edges: P1 discovery is static, and inter-node flow arrives with P5 tracing.\n")
		fmt.Fprintf(&b, "  The graph therefore shows call sites and their labels, not a dataflow.\n")
	}
	unresolved := 0
	for _, node := range ir.Nodes {
		if node.Model.ModelID == "" || node.Model.ModelID == "unresolved" {
			unresolved++
		}
	}
	fmt.Fprintf(&b, "  %d of %d call site(s) have an unresolved model id — a syntactic frontend cannot\n", unresolved, len(ir.Nodes))
	fmt.Fprintf(&b, "  follow a variable to its assignment, and the graph says so rather than guessing.\n")
	// `Residue` is the honest denominator here: the subgraphs no rule detector covered. Reporting only
	// the labels would let a reader infer the rest were examined and found to be nothing.
	fmt.Fprintf(&b, "  %d label(s) across %d subgraph(s); %d region(s) no rule covered; %d LLM call(s) used.\n",
		len(result.Labels), len(result.Subgraphs), len(result.Residue), result.LLMCalls)
	return &ir, result, b.String(), nil
}

// graphSource is the P3.5 read surface for exactly the workflows this command classified.
type graphSource struct {
	views map[string]patternclassifier.GraphView
}

// GraphView answers for a known workflow and NOT for an unknown one.
//
// Returning this workflow's graph under a different id would make the 404 path unreachable — and
// would show a user someone else's graph under their own workflow id, which is the `wf-demo` defect
// in a different costume.
func (g *graphSource) GraphView(_, id string) (patternclassifier.GraphView, bool) {
	view, ok := g.views[id]
	return view, ok
}

// demoModels is a minimal StudioModelStore for the studio MATRIX (P9 §11b): it serves the model CATALOG —
// the matrix rows — which is a provider list, not customer data, so a static set is honest here. The
// figures a run would produce are never fabricated: `ResolveModel`/`StudioRender` are reached only by
// the test-run path, which this demo gates off (nil Runner answers 503 first), so they return the
// not-available error rather than a made-up completion.
type demoModels struct{}

func (demoModels) ModelCatalog(ctx context.Context) ([]registry.ModelCatalogEntry, error) {
	return []registry.ModelCatalogEntry{
		{VersionID: "m_sonnet5", Name: "Claude Sonnet 5", Provider: "anthropic", ModelID: "claude-sonnet-5"},
		{VersionID: "m_opus5", Name: "Claude Opus 5", Provider: "anthropic", ModelID: "claude-opus-5"},
		{VersionID: "m_haiku45", Name: "Claude Haiku 4.5", Provider: "anthropic", ModelID: "claude-haiku-4-5"},
		{VersionID: "m_gpt5", Name: "GPT-5", Provider: "openai", ModelID: "gpt-5"},
	}, nil
}

func (demoModels) ResolveModel(ctx context.Context, versionID string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}

func (demoModels) StudioRender(ctx context.Context, versionID string, bindings map[string]string) (string, error) {
	return "", registry.ErrNotFound
}

// ── P30 §8 · the agent panel and a labelled synthetic inference ──────────────────────────────────

// demoAgentSource answers the three questions the graph page's agent panel asks.
//
// 🔴 It implements the SHIPPED interface, so every sentence, state and refusal a reader sees on the
// panel is produced by `internal/api`'s own `agentPanelFor` — not by this file. What this supplies is
// the two inputs a deployment would have: a placement, and whatever narrative the last inference wrote.
type demoAgentSource struct {
	placement herosagent.Placement
	narrative string
}

func (d *demoAgentSource) PlacementFor(context.Context, string) (herosagent.Placement, error) {
	return d.placement, nil
}

func (d *demoAgentSource) ActiveDefinition(context.Context) (runlink.AgentDefinition, bool, error) {
	// Nothing published on this demo, which is the honest answer: `heros analyse` against it reports
	// that no definition is active rather than running something invented.
	return runlink.AgentDefinition{}, false, nil
}

func (d *demoAgentSource) Accept(context.Context, herosagent.Submission) (herosagent.IngestResult, error) {
	return herosagent.IngestResult{}, fmt.Errorf("this demo accepts no submissions")
}

func (d *demoAgentSource) NarrativeFor(context.Context, string, string) (string, bool, error) {
	return d.narrative, d.narrative != "", nil
}

// stampFixtureInference marks one edge and one label as agent-authored, and returns the narrative to
// show beside them.
//
// 🚫 IT INVENTS NOTHING. Every edge and label it touches was already in the graph, established by the
// real classifier over the real repository; all it changes is the recorded AUTHOR, so the console's
// authorship-dependent treatments have something to render. It adds no edge, removes none, and changes
// no confidence except to give the marked edge one — an inferred edge carries a confidence and a
// measured one does not.
func stampFixtureInference(view *patternclassifier.GraphView) string {
	for i := range view.Edges {
		if view.Edges[i].Author == "" || view.Edges[i].Author == string(discovery.AuthorFrontend) {
			view.Edges[i].Author = string(discovery.AuthorHEROS)
			view.Edges[i].Confidence = 0.86
			break
		}
	}
	for i := range view.Regions {
		for j := range view.Regions[i].Labels {
			if view.Regions[i].Labels[j].Author != discovery.AuthorHEROS {
				view.Regions[i].Labels[j].Author = discovery.AuthorHEROS
				// The composition is recomputed so its split counts reflect the stamp — otherwise the
				// page would draw an inferred edge above a composition that says nothing was inferred,
				// which is the exact inconsistency §8 exists to prevent.
				view.Composition = patternclassifier.RebuildComposition(*view)
				return "This graph is being shown with a SYNTHETIC inference stamped onto it, so the " +
					"treatments for inferred facts can be seen. No model was consulted and nothing " +
					"marked inferred here is a claim about this repository."
			}
		}
	}
	view.Composition = patternclassifier.RebuildComposition(*view)
	return ""
}

// demoUnresolvableCredential is the readiness resolver this demo honestly has: none.
//
// 🔴 It FAILS rather than succeeding. This binary reaches no provider, so a resolver that returned nil
// would make `/readyz` report `ready` on a process that could not run a single inference — which is
// precisely the assertion-from-configuration task 9.1 exists to prevent, reproduced on the binary an
// operator would use to check for it.
type demoUnresolvableCredential struct{}

func (demoUnresolvableCredential) Resolve(context.Context, string) error {
	return fmt.Errorf("this proof binary configures no secrets source and reaches no provider")
}

func (demoUnresolvableCredential) Describe() string { return "none (proof binary)" }
