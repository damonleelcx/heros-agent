package launch

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/hostedboard"
	"github.com/heros-foreal/agentd/internal/hostedscorecard"
	"github.com/heros-foreal/agentd/internal/legal"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourceingest"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// Capability is one platform surface as this deployment carries it, for the boot log and for
// deploy/README.md's "what a fresh install serves" table.
type Capability struct {
	Name   string
	Served bool
	// Why is stated for an unserved capability: an operator reading "not mounted" needs to know whether
	// to configure something or to wait for a phase.
	Why string
}

// mountCapabilities registers EVERY capability surface the platform ships (P19 Decision 10).
//
// # Why an unsourced capability is registered rather than left out
//
// An unregistered route is not a neutral absence — the mux answers **404**, and 404 already means
// something else. The customer console calls /api/p2, /api/p10, /api/p12, /api/p21 and /api/p25; before
// this function existed, every one of them fell through to 404 on a real deployment, which the console
// renders as "No such workflow" for a workflow that plainly exists. Registering with a nil source makes
// each answer **503 with a not-mounted body**, which is the truth: "this capability is not installed on
// this deployment" and "that identifier does not resolve" have different next actions, and every handler
// in internal/api already has the nil guard that produces the first one. cmd/proof/customerconsole found
// this by opening a browser and reading a 404 that should have been a 503; the deployment never
// inherited the lesson.
//
// # Why so many are still unsourced
//
// A capability is mounted for real here only when a DURABLE store for it exists. Six read surfaces (P4,
// P4.5, P5, P5.5, P6, P3.5) have no adapter outside a demo binary; billing (P7/P21) has exactly one
// Ledger implementation and one account Store, both in-memory, so mounting it would record a payment and
// forget it on restart; P11 and P13's recorders are in-memory for the same reason; P12's gate and pending
// providers read verification state that has no store yet. Mounting those over memory would turn "not
// installed" into "installed and quietly lossy", which is a worse lie than the 404 this replaces. They
// are PRD Q6, and deploy/README.md lists them.
func mountCapabilities(h *api.Server, pg *sql.DB, dataDir, consoleHealthURL string) ([]Capability, error) {
	caps := make([]Capability, 0, 16)
	served := func(name string) { caps = append(caps, Capability{Name: name, Served: true}) }
	absent := func(name, why string) { caps = append(caps, Capability{Name: name, Why: why}) }

	// ── The Postgres-backed surfaces ────────────────────────────────────────────────────────────────
	//
	// These need the platform database. A deployment that declares no DSN gets them registered and
	// unsourced, exactly like the ones below — "no database configured" is a deployment fact the
	// operator can act on, and a 503 says so where a 404 would not.
	mountedPatternGraph := false
	mountedGraphEditor := false
	mountedEvalBoard := false
	mountedScorecard := false
	if pg != nil {
		fsBlobs, err := registry.NewFSBlobStore(filepath.Join(dataDir, "blobs"))
		if err != nil {
			return nil, fmt.Errorf("blob store: %w", err)
		}
		// A CATALOGING blob store: 0001's node_execution FK to blob(content_hash) requires every
		// referenced blob to have a catalog row, so writing the bytes alone is not enough.
		blobs := registry.NewCatalogingBlobStore(pg, fsBlobs, "application/json")

		reg := registry.NewStore(pg, blobs)
		h.MountPromptRegistry(reg)
		served("p10_prompt_registry")

		// The studio matrix's model catalog and render are the registry's; the workflow catalog, the
		// binding store and the test-runner are not mounted — the first two are in-memory and nothing in
		// the deployed path loads them, and the runner needs a provider fan-out. Each answers 503 on its
		// own, which is why StudioMatrix is a struct of independently-nillable parts rather than one source.
		h.MountStudioMatrix(api.StudioMatrix{Store: reg})
		served("p10_studio_matrix (models + render; workflows/bindings/run not mounted)")

		// P2 read views over the lineage schema. Submit stays nil: it is the write path into a target
		// repository, and this type's own comment already rules that a deployment with no repository to
		// transform mounts the read views and no submit.
		h.MountConfigRuntime(api.ConfigRuntimeStores{
			Transforms: worktree.NewStore(pg, blobs),
			Runs:       executor.NewStore(pg),
			Specs:      variantspec.NewStore(pg),
		})
		served("p2_config_runtime (read views; submit not mounted)")

		// P11 run linking — the surface the `heros` CLI's `login` and `link` commands reach.
		//
		// Mounted for real now that linkingest has a Postgres store (migration 0020). It was
		// registered-and-unsourced for exactly one reason: its only Store was in-memory, so mounting it
		// would have accepted a developer's linked run, answered 200, and forgotten it on the next
		// restart. A durable store is what changes that, not a decision to be less careful.
		//
		// 🔴 THE RUN URL IS THE PUBLIC ONE, NOT THE HEALTH-CHECK ORIGIN.
		//
		// This used to build the URL from originOf(consoleHealthURL), reasoning that "a linked run's URL
		// and the console we health-check can never disagree". They must disagree: CONSOLE_HEALTH_URL is
		// the IN-CLUSTER address a pod probes (http://console:4320/api/health), and the run URL is handed
		// back to a developer's terminal. `heros link` printed
		// `http://console:4320/app/runs/run-…` — a Service DNS name that resolves on no machine the user
		// has. It looks like a URL, so nobody reads it as a bug until they click it.
		//
		// runlink.PlatformBaseURL is the right origin and needs no new variable: the CLI's own allowlist
		// refuses to transmit anywhere else (internal/runlink/allowlist.go), so for any client that can
		// reach this handler at all, that constant IS the public origin by construction. Passing nil
		// selects exactly that in Ingester.route.
		linkStore := linkingest.NewPGStore(pg)
		// 🔴 The sink was `nil` here, and it panicked on the first link of any run — found by actually
		// running `heros link` against a deployed platform. Ingest writes cost/latency/token events on
		// first link only, so re-linking an existing run answered 409 correctly and only the first one
		// failed, as an opaque PLATFORM_PANIC with nothing in the log. linkingest.New now refuses a nil
		// sink outright so this cannot come back quietly.
		//
		// ⚠️ MemCostEvents is IN-MEMORY, and that limit is real but bounded: the LINKED RUN itself is
		// durable (Postgres, migration 0020) — what does not survive a restart is the derived metering
		// series the console's spend figure reads. That is a smaller loss than refusing to mount, which
		// would leave `heros link` answering 503 on a platform whose store is durable. It becomes moot
		// when a durable metering substrate exists; internal/metering has only MemCostEvents today.
		h.MountRunLinking(linkingest.New(metering.NewMemCostEvents(), linkStore, nil))
		served("p11_run_linking (links durable; metering series in-memory until a durable substrate exists)")

		// P4 and P4.5, mounted for the first time — over LINKED RUNS, which is what they were always
		// going to be about on a platform that does not execute the customer's workflow.
		//
		// Their reason for being unsourced was "no persistent adapter exists outside a demo binary", and
		// as with the pattern graph the smaller half was the adapter: the platform held scores and none
		// of the EVIDENCE that qualifies them. A board needs the case count and the gate verdict; a
		// scorecard needs per-node attribution. All three were computed by the CLI and thrown away.
		// Migration 0023 and the `eval` section of internal/runlink/allowlist.go are the deliberate,
		// reviewed widening that brings them across; these two adapters are what render them.
		//
		// Both are careful about what they still CANNOT say — the board reports
		// `tie_analysis: unavailable` because bootstrap replicates do not cross, and the scorecard
		// reports `failure_attribution: unavailable` because per-node correctness does not. Those are
		// stated as data rather than left to look like findings of "no ties" and "no node at fault".
		h.MountEvalBoard(hostedboard.NewSource(linkStore))
		served("p4_eval_board (assembled from linked runs; no tie detection — replicates do not cross)")
		mountedEvalBoard = true
		h.MountScorecard(hostedscorecard.NewSource(linkStore))
		served("p45_scorecard (cost/latency attribution from linked runs; failure attribution stays local)")
		mountedScorecard = true

		// The OPT-IN workflow structure (`heros link --with-ir`), durable since migration 0021, and the
		// pattern graph drawn from it.
		//
		// 🔴 The graph was registered-but-unmounted with the reason "no persistent adapter exists outside
		// a demo binary". True, and the smaller half: there was also no DATA. Nothing ever sent this
		// platform a workflow's shape, so an adapter would have had a store, a route, and nothing to put
		// in either. Both halves are answered here — a customer who opts in gets their graph, and one who
		// does not still gets 404 from a mounted route, which reads as "you have not sent this" rather
		// than as a broken deployment.
		irStore := linkingest.NewPGWorkflowIRStore(pg)
		h.MountWorkflowIR(irStore)
		served("p11_workflow_ir (opt-in structure: `heros link --with-ir`)")

		// Platform-side discovery (migration 0022) — the source the platform never had, and the
		// LABELLED graph that follows from it.
		//
		// The line above used to end "no labels — the classifier's inputs do not cross the boundary",
		// and that was true of the only input the platform was ever given. It is no longer the only
		// one: a customer can push a source snapshot, and discovery then runs HERE, so prompt text and
		// tool names are read on this side of the boundary instead of being transmitted. The wire
		// contract in internal/runlink is untouched.
		//
		// Both sources stay mounted. A tenant who pushes source gets a classified graph; a tenant who
		// only sends the opt-in structure gets exactly the view they got before. See platformgraph.go
		// for why the two are preferred rather than merged.
		graphStore := hostdiscovery.NewPGGraphStore(pg)
		h.MountPatternGraph(newPlatformGraphSource(graphStore, newWorkflowGraphSource(irStore)))
		served("p35_pattern_graph (labelled when source is pushed; opt-in structure otherwise)")
		mountedPatternGraph = true

		// The source-snapshot ingest and the discovery runner behind it.
		//
		// Mounted together here because this deployment has what both need, but kept independently
		// nillable in the API: accepting a snapshot needs a database and a blob store, running discovery
		// over it additionally needs the skill registry. A deployment that will not hold customer source
		// removes this block and the routes answer 503, which is a policy answer rather than a fault.
		bundleStore, err := sourceingest.NewPGBundleStore(pg, blobs)
		if err != nil {
			return nil, fmt.Errorf("source bundle store: %w", err)
		}
		bundleSource, err := sourceingest.NewBundleSource(bundleStore, filepath.Join(dataDir, "source-scratch"))
		if err != nil {
			return nil, fmt.Errorf("source scratch: %w", err)
		}
		// reg is the SAME registry.Store mounted as the prompt registry above. Deliberately shared: the
		// classifier must resolve tool bindings against the registry this deployment actually serves,
		// and a second store pointed at the same tables would be a second answer to one question.
		runner, err := hostdiscovery.NewRunner(bundleSource, hostdiscovery.RegistrySkills(reg), graphStore)
		if err != nil {
			return nil, fmt.Errorf("discovery runner: %w", err)
		}
		h.MountSourcePush(bundleStore, newDiscoveryAdapter(runner))
		served("p1_source_discovery (customer-pushed snapshots; discovery and classification run here)")

		// P5's editor, mounted for the first time. Its reason for being unsourced was the same as the
		// pattern graph's and had the same second half: no adapter, and nothing to adapt. It needs the
		// FULL IR — io_contracts and all — which is more than the platform stores on purpose, so the
		// source re-derives it per request from the retained snapshot. See editorIRSource.
		h.MountGraphEditor(newEditorIRSource(graphStore, runner))
		served("p5_graph_editor (IR re-derived from the pushed snapshot; needs source, not just a graph)")
		mountedGraphEditor = true
		// No separate readiness probe for this store, deliberately. An earlier draft added one, because
		// linkingest.Store's reads returned no error and a failure had nowhere else to go. The interface
		// now returns errors on every method, so a failed read fails its CALLER — and the `postgres`
		// probe already reports the database. A second component reporting the same dependency would be
		// two signals that can disagree, which is worse than one.

		// P23 consent. The manifest lives on the customer console's origin — which the readiness health
		// URL already names exactly, so the origin is DERIVED from it rather than configured twice. Two
		// variables for one origin is two things to keep in step, and they would not stay in step.
		if origin := originOf(consoleHealthURL); origin != "" {
			h.RegisterConsent(legal.NewService(
				legal.NewPGStore(pg),
				legal.NewHTTPManifestSource(origin),
				time.Now,
				newConsentID,
			))
			served("p23_consent")
		} else {
			h.RegisterConsent(nil)
			absent("p23_consent", "no customer console on this deployment, so no published legal manifest to record against")
		}
	} else {
		h.MountPromptRegistry(nil)
		h.MountStudioMatrix(api.StudioMatrix{})
		h.MountConfigRuntime(api.ConfigRuntimeStores{})
		h.MountRunLinking(nil)
		h.RegisterConsent(nil)
		const noDB = "this deployment declares no platform database (DATABASE_URL is unset)"
		absent("p10_prompt_registry", noDB)
		absent("p10_studio_matrix", noDB)
		absent("p2_config_runtime", noDB)
		absent("p11_run_linking", noDB)
		absent("p23_consent", noDB)
	}

	// ── Registered, unsourced ───────────────────────────────────────────────────────────────────────
	//
	// Each is a real capability with a real domain package and, in most cases, real Postgres tables. What
	// is missing is the adapter between them, and P19 will not invent one — that is a product decision
	// wearing a deployment label. Registering them is what makes the absence legible.
	const noAdapter = "no persistent adapter exists outside a demo binary (PRD Q6)"
	const noDurableStore = "its only store implementation is in-memory, so mounting it would record and then forget"

	if !mountedEvalBoard {
		h.MountEvalBoard(nil)
		absent("p4_eval_board", noAdapter)
	}
	if !mountedScorecard {
		h.MountScorecard(nil)
		absent("p45_scorecard", noAdapter)
	}
	if !mountedGraphEditor {
		h.MountGraphEditor(nil)
		absent("p5_graph_editor", noAdapter)
	}
	h.MountProposals(nil)
	absent("p55_proposals", noAdapter)
	h.MountOptimizer(nil)
	absent("p6_optimizer", noAdapter)
	if !mountedPatternGraph {
		h.MountPatternGraph(nil)
		absent("p35_pattern_graph", noAdapter)
	}
	h.MountMonitor(nil)
	absent("p25_run_monitor", noAdapter)

	h.MountBilling(nil)
	absent("p7_billing", noDurableStore)
	h.MountPayments(nil)
	absent("p21_payments", noDurableStore)
	h.MountAuthoring(nil)
	absent("p13_authoring", noDurableStore)
	h.MountForgeDelivery(nil)
	absent("p12_forge_delivery", "its gate and pending providers read verification state that has no store yet")

	// The Stripe webhook is the ONE surface deliberately NOT registered when it has no source, and the
	// exception is the author's, not a lapse: internal/api/p21.go states that the path is the single
	// inbound-from-internet route and that "a deployment that does not expose it simply does not call
	// this — there is no flag that half-enables an endpoint". Registering it unsourced would publish an
	// internet-facing path on every deployment, including air-gapped ones, to answer 503. It is mounted
	// when billing has a durable ledger to mount it over; today it has none.

	return caps, nil
}

// originOf returns scheme://host for a URL, or "" if it is not one. Used to turn the customer console's
// health URL into the console's origin — an exact derivation, not a guess: the health endpoint is served
// by the console, so its origin IS the console's.
func originOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// newConsentID mints an acceptance id. Random rather than sequential: an acceptance id appears in an
// evidentiary record, and a sequential one leaks how many acceptances a deployment has recorded.
func newConsentID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a condition to paper over with a timestamp — a predictable id in an
		// evidentiary record is worse than a loud panic during boot, which is when this runs.
		panic("launch: crypto/rand unavailable while minting a consent id: " + err.Error())
	}
	return "acc_" + hex.EncodeToString(b[:])
}
