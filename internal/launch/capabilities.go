package launch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/billingview"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/deliveryroute"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/hostedboard"
	"github.com/heros-foreal/agentd/internal/hostedcompile"
	"github.com/heros-foreal/agentd/internal/hostedproposals"
	"github.com/heros-foreal/agentd/internal/hostedscorecard"
	"github.com/heros-foreal/agentd/internal/legal"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/modelcatalog"
	"github.com/heros-foreal/agentd/internal/paymentsview"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/proposalgen"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sandbox"
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
func mountCapabilities(h *api.Server, pg *sql.DB, dataDir, consoleHealthURL string, secrets providergateway.Secrets) ([]Capability, error) {
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
	mountedVerdictIngest := false
	mountedProposalGen := false
	mountedProposals := false
	mountedForgeDelivery := false
	mountedProposalCompile := false
	mountedPayments := false
	// collectionWhy is the served() line when collection IS mounted; why is the absent() reason.
	collectionWhy := ""
	// Assembled inside the database block below; nil when this deployment cannot serve billing.
	var billingView *billingview.Source
	// The server-side entitlement gate, assembled with billing because both need the plan catalog. P12
	// delivery reads it too — nil means this deployment cannot decide whether a tenant may have a pull
	// request opened for them, which is a reason not to mount delivery rather than a reason to guess.
	var entGate *entitlement.Gate
	// Set when a published catalog exists but could not be parsed — billing, delivery and the
	// entitlement gate all refuse together, because they all resolve against it.
	var catalogUnloadable error
	// Set when this deployment declares no payment provider; read by the p21 capability line below.
	collectionAbsentWhy := "no payment provider is configured on this deployment; the durable ledger exists, but checkout and plan changes need a provider to call"
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
		// ── The billing stack ──────────────────────────────────────────────────────────────────────
		//
		// Every store here is durable, over tables migration 0013 created: PGLedger, account.PGStore,
		// metering.PGUsageStore. The two collaborators that are NOT durable are correct as they are:
		//
		//   - MemVerifiedDeltas holds the P6 optimizer's verified savings, and no optimizer loop runs on
		//     this platform. An empty ledger is the TRUE answer — the billing page reports "none
		//     verified", which is a fact rather than a lost record.
		//   - StubProvider is a placeholder the read model never calls. billingview performs no provider
		//     request (it reads the platform's own ledger, which is the authority for what was charged),
		//     and MountBilling registers only the read routes plus consent, which writes to the account
		//     store. Nothing mounted from this Service can reach the provider — which is why P21 stays
		//     absent rather than being quietly served by a stub.
		if catalog := planCatalogPath(); catalog != "" {
			plans := plancfg.NewResolver(plancfg.NewFileSource(catalog), nil)
			// 🔴 LOAD IT. A Resolver does not read its source on construction — `loaded` stays false and
			// every ResolvePlan returns ErrNoConfig until somebody calls Reload. Nothing in the deployed
			// path ever did: Reload appears only in four cmd/ binaries, so on a real install the catalog
			// was PUBLISHED (the boot stat found the file, and that is what mounted billing, delivery and
			// the entitlement gate) and then never read.
			//
			// The symptom is not an error anywhere. `Plans()` returns empty, so checkout answers "no plan
			// with that name in the published configuration" for every plan the file plainly contains,
			// and every entitlement decision resolves against nothing. That is precisely the outcome the
			// boot-time stat was added to prevent — a mounted surface that fails on its first read —
			// displaced one step: the file's EXISTENCE was proven and its CONTENTS never were.
			//
			// Failing here rather than per-request is the same posture launch takes for the secrets
			// source: a deployment whose catalog does not parse must not serve a billing page that
			// discovers it one customer at a time. billingAbsentReason and deliveryAbsentReason already
			// had a branch for this state; until now nothing could reach it.
			_, rerr := plans.Reload("launch")
			if rerr != nil {
				catalogUnloadable = rerr
			}
			if rerr == nil {
				acctStore, err := account.NewPGStore(pg)
				if err != nil {
					return nil, fmt.Errorf("account store: %w", err)
				}
				usageStore, err := metering.NewPGUsageStore(pg)
				if err != nil {
					return nil, fmt.Errorf("usage store: %w", err)
				}
				ledger, err := billing.NewPGLedger(pg)
				if err != nil {
					return nil, fmt.Errorf("billing ledger: %w", err)
				}
				deltas := metering.NewMemVerifiedDeltas()
				meter := metering.NewMeter(metering.NewMemCostEvents(), usageStore)

				// ── The payment provider, if this deployment declares one ──────────────────────────────
				//
				// Without a declaration the service keeps the stub: the P7 read model makes no provider
				// call, so billing stays fully served and only COLLECTION is absent.
				//
				// 🔴 THE CREDENTIAL DOES NOT PASS THROUGH HERE, and cannot. NewStripeProvider takes a
				// Secrets SEAM and resolves the key at the moment of use — its own contract says a billing
				// credential is "never read from code, config, or the environment" — so there is no field
				// on this path holding one that a log, a formatter or a panic dump could reach. What the
				// deployment declares is the MODE; what it supplies is a secret, through the same source
				// every other credential comes from.
				var mode billing.Mode
				var provider collectionDeps
				provider, mode, collectionAbsentWhy = collectionProvider(secrets)
				svc, err := billing.NewService(provider.provider, ledger, acctStore, plans, meter, provider.secrets)
				if err != nil {
					return nil, fmt.Errorf("billing service: %w", err)
				}
				// ── The webhook's two DURABLE stores ───────────────────────────────────────────────────
				//
				// 🔴 Without the delivery store the webhook endpoint is mounted, publicly reachable, and
				// refuses EVERY delivery: HandleWebhook's own rule is that a webhook which cannot be
				// deduped must not be processed. Nothing here attached one, so on a real deployment
				// checkout completed, the customer's card was charged, and the platform never learned the
				// subscription became active — with no error on either console, because from this side
				// nothing happened at all.
				//
				// They are wired HERE rather than beside the provider because they belong to the durable
				// billing stack: both need `pg`, and both are as necessary in `test` mode as in `live`.
				// Attaching the in-memory pair instead would be worse than either — a redelivery after a
				// restart re-applies an effect that was already applied, and Stripe redelivers for days.
				deliveries, derr := billing.NewPGDeliveries(pg)
				if derr != nil {
					return nil, fmt.Errorf("billing webhook deliveries: %w", derr)
				}
				states, serr := billing.NewPGStates(pg)
				if serr != nil {
					return nil, fmt.Errorf("billing state mirror: %w", serr)
				}
				svc.WithDeliveries(deliveries).WithStates(states)
				// ONE gate, shared with P12 delivery below. Two gates over the same plans and the same usage
				// would be two answers to "may this tenant do this", and the one the billing page shows is not
				// the one that decides whether a pull request is opened.
				entGate = entitlement.NewGate(acctStore, plans, usageStore)
				if billingView, err = billingview.New(acctStore, plans, usageStore, deltas, entGate, svc); err != nil {
					return nil, fmt.Errorf("billing view: %w", err)
				}

				// P21 collection, mounted only where a provider was DECLARED.
				//
				// The read model is internal/paymentsview, extracted for this: it existed only inside
				// cmd/proof/payments, so before it there was nothing to mount even with a provider in hand
				// — which is why p21's absent reason named only the provider and was incomplete.
				//
				// ⚠️ The webhook is mounted WITH checkout, never apart from it. It is the only route on
				// this platform that accepts unsolicited internet traffic, so publishing it on a
				// deployment that collects nothing would be an inbound door onto a surface with no reason
				// to exist; and mounting checkout without it would take a customer's card and never learn
				// that the subscription became active.
				if mode != "" {
					pv, perr := paymentsview.New(billingView, svc)
					if perr != nil {
						return nil, fmt.Errorf("payments view: %w", perr)
					}
					h.MountPayments(pv)
					h.MountBillingWebhook(svc)
					mountedPayments = true
					collectionWhy = "p21_payments (checkout, plan changes and the provider webhook; mode " + string(mode) + ")"
					startPricingPreflight(svc, mode)
				}
			}
		}

		h.MountEvalBoard(hostedboard.NewSource(linkStore))
		served("p4_eval_board (assembled from linked runs; no tie detection — replicates do not cross)")
		mountedEvalBoard = true
		h.MountScorecard(hostedscorecard.NewSource(linkStore))
		served("p45_scorecard (cost/latency attribution from linked runs; failure attribution stays local)")
		mountedScorecard = true

		// P5.5's verdict ingest — the endpoint `heros report-verdict` transmits to.
		//
		// Mounted for real, over a durable store (migrations 0012, 0025 and 0029). It is the ONLY way a
		// stored verdict can say `pass`: the verification gate needs the customer's eval cases, traces
		// and provider, so this platform can GENERATE a proposal and can never MEASURE one. Without this
		// route, every proposal the generator writes stays `candidate` forever and the recommendation
		// surface is permanently empty — which would look exactly like a product that finds nothing.
		//
		// The store is passed straight through as the sink: proposalstore.PGStore.PutVerdict already has
		// api.VerdictSink's signature, and an adapter here would be a place for the tenant argument to be
		// rewritten on its way to the WHERE clause that scopes it.
		verdictStore, err := proposalstore.NewPGStore(pg)
		if err != nil {
			return nil, fmt.Errorf("proposal store: %w", err)
		}
		h.MountVerdictIngest(verdictStore)
		served("p55_verdict_ingest (CI reports what it measured; the platform never authors a verdict)")
		mountedVerdictIngest = true

		// ── P5.5 and P12, mounted ──────────────────────────────────────────────────────────────────────
		//
		// Both were registered-and-unsourced with "no persistent adapter exists outside a demo binary".
		// That was true, and as with the pattern graph it was the smaller half: there was also no DATA.
		// Nothing generated a proposal, nothing recorded a verdict, and no table held a delivery route.
		// Migrations 0025 / 0029 / 0030, the verdict ingest, internal/proposalgen and
		// internal/deliveryroute are the other half; these two adapters are what render them.
		//
		// 🔴 They mount with a STATED LIMIT rather than in full, and the limit is the same fact for both:
		// this platform generates a candidate Variant Spec and never COMPILES it, so no proposal has a
		// diff. P5.5 therefore serves cards with no diff and an Open-PR action that refuses by name; P12
		// serves route conditions and delivery history, and reports every proposal as withheld with
		// `no_diff`. That is worth mounting — a customer can see what was proposed, verify it in their own
		// CI, and read exactly why it stops there — and it is emphatically better than the 503 it
		// replaces, which said the capability was not installed when the truth is that it is installed
		// and bounded.
		h.MountProposals(hostedproposals.NewSource(verdictStore, blobs))
		served("p55_proposals (recommendation surface over reported verdicts; no diff, so open-PR refuses)")
		mountedProposals = true

		// P12 delivery. Mounted only when an entitlement gate exists, which means only when a plan
		// catalog is published — the same condition billing carries, for a sharper reason: delivery opens
		// a pull request into a customer's repository, and `may this tenant have that done` is a question
		// with no safe default. Denying every delivery would tell a customer to upgrade a plan the
		// deployment cannot read; allowing them would open pull requests for tenants who are not entitled
		// to any. Neither is an answer, so the capability reports that it is not configured.
		//
		// Every other collaborator here is durable except the halt reader, and that one is
		// correct as it is: this deployment configures no kill switch, so NOTHING halts delivery, and
		// reporting `false` is the true answer rather than a stub. It is NOT the fail-closed direction by
		// accident — HaltReader's contract is that an ERROR means indeterminate, and a reader that cannot
		// fail cannot be indeterminate. A deployment that adds a kill switch wires adminops here.
		if entGate != nil {
			deliverer := forgedelivery.NewDeliverer(
				hostedproposals.NewGate(verdictStore),
				entGate,
				forgedelivery.HaltReaderFunc(func(string) (bool, string, error) { return false, "", nil }),
				deliveryrecord.NewPGStore(pg),
				forgedelivery.DefaultOpenPRBound,
			)
			routeStore, err := deliveryroute.NewPGStore(pg)
			if err != nil {
				return nil, fmt.Errorf("delivery route store: %w", err)
			}
			h.MountForgeDelivery(forgedelivery.NewService(
				deliverer, routeStore,
				hostedproposals.NewPending(verdictStore, blobs, originOf(consoleHealthURL)),
				originOf(consoleHealthURL),
			))
			served("p12_forge_delivery (routes, conditions and history; deliveries withheld as `no_diff` " +
				"until proposals are compiled)")
			mountedForgeDelivery = true
		}

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

		// The CODEMOD. It turns a stored proposal into the reviewable diff ADR-001 makes the product's
		// output, using the retained snapshot, the re-derived IR and the same registries every other
		// resolve goes through.
		//
		// ⚠️ It compiles and does NOT build: this image is distroless with no toolchain, so
		// hostedcompile's gate parses Go in-process and reports everything else — and every non-Go
		// language — as unbuilt with the reason. That keeps delivery gated where ADR-001 puts it. Giving
		// a deployment a real build gate means running the customer's build, which belongs inside
		// internal/sandbox in an image that carries the toolchain; it is not a line to change here.
		buildSandbox, gateWhy := compileSandbox()
		h.MountProposalCompile(&hostedcompile.Compiler{
			Runner:     runner,
			Store:      verdictStore,
			Blobs:      blobs,
			Registries: reg,
			Sandbox:    buildSandbox,
			GoBin:      os.Getenv("HEROS_GO_TOOLCHAIN"),
		})
		served("p55_proposal_compile (AST codemod over the pushed snapshot; " + gateWhy + ")")
		mountedProposalCompile = true

		// The platform-side proposal GENERATOR.
		//
		// It is mounted only when a model catalog is published, and that gate is the whole story of what
		// this deployment can and cannot do. `proposal.Menu.cheaperModels(tier)` is the entire input to
		// the model-downgrade operator — the one operator that fires on a cost bottleneck with no
		// diagnosis, and therefore the only one a hosted platform can drive. It selects on Tier and
		// CostPerRun, and the REGISTRY RECORDS NEITHER: registry.ModelCatalogEntry is
		// {VersionID, Name, Provider, ModelID, Params}. Every proposal.Menu in this repository is
		// hand-written inside a cmd/proof binary.
		//
		// A tier is a judgement about capability and a cost-per-run is a price. Neither is derivable from
		// a model entry, and inventing either would mean proposing changes to a customer's code on the
		// strength of numbers the platform made up. So they are published beside the plan catalog, on the
		// same terms: a path, never git-tracked, absent by default.
		//
		// Without one this answers 503 and the console says "no model catalog is published", which names
		// an action. Mounting it over an empty menu would answer 200 with an empty proposal list, which
		// reads as "we looked at your workflow and found nothing wrong".
		if mcSrc, ok := modelcatalog.FileSourceFromEnv(); ok {
			gen := &proposalgen.Generator{
				Runs:   linkStore,
				Graphs: graphStore,
				Menus:  menuSource{src: mcSrc, reg: reg},
				Sink:   verdictStore,
				// The candidate spec goes to the SAME blob store the diff and the prompt registry use.
				// A proposal recorded without it can never be compiled (migration 0031).
				Blobs: blobs,
			}
			h.MountProposalGeneration(gen)
			served("p55_proposal_generation (cost-bottleneck operators only; a diagnosis needs the eval " +
				"cases, which stay with the customer)")
			mountedProposalGen = true
		}
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
	if !mountedProposals {
		h.MountProposals(nil)
		absent("p55_proposals", "this deployment declares no platform database (DATABASE_URL is unset), "+
			"so there are no stored proposals to render")
	}
	if !mountedProposalCompile {
		h.MountProposalCompile(nil)
		absent("p55_proposal_compile", "this deployment declares no platform database (DATABASE_URL is "+
			"unset), so there are no stored proposals to compile and no snapshot to compile them against")
	}
	if !mountedProposalGen {
		h.MountProposalGeneration(nil)
		absent("p55_proposal_generation", proposalGenAbsentReason(pg))
	}
	if !mountedVerdictIngest {
		h.MountVerdictIngest(nil)
		absent("p55_verdict_ingest", "this deployment declares no platform database (DATABASE_URL is unset), "+
			"so there is no proposal for a reported verdict to attach to")
	}
	h.MountOptimizer(nil)
	absent("p6_optimizer", noAdapter)
	if !mountedPatternGraph {
		h.MountPatternGraph(nil)
		absent("p35_pattern_graph", noAdapter)
	}
	h.MountMonitor(nil)
	// The SAME sentence the boot log prints. It was composed for an operator and the customer got a
	// five-word stub; one reason, both audiences.
	h.MountMonitorAbsent(runMonitorAbsentReason)
	absent("p25_run_monitor", runMonitorAbsentReason)

	// ── P7 billing ─────────────────────────────────────────────────────────────────────────────────
	//
	// Mounted when this deployment has BOTH a platform database and a published plan catalog. The
	// stores are durable now (PGLedger, account.PGStore, metering.PGUsageStore over the tables 0013
	// created); what remains conditional is the catalog, because billing cannot resolve WHICH PLAN a
	// customer is on without one, and a billing page that cannot name the plan is not a degraded page —
	// it is a page that cannot say what anything costs.
	//
	// The catalog is a file, never git-tracked (plancfg.Source says so): it carries prices.
	if pg != nil && billingView != nil {
		h.MountBilling(billingView)
		served("p7_billing (durable ledger, accounts and meters; read model + consent)")
	} else {
		h.MountBilling(nil)
		absent("p7_billing", billingAbsentReason(pg, planCatalogPath(), catalogUnloadable))
	}
	// P21 payments stays unmounted, and NOT for the old reason. The ledger is durable now; what
	// checkout and plan-change need is a real payment PROVIDER, and this deployment configures none.
	// Mounting them over the stub would offer a customer a checkout button that mints nothing.
	//
	// The Stripe webhook stays unregistered with it — internal/api/p21.go's posture is that the single
	// inbound-from-internet route must not be published on every deployment, including air-gapped ones,
	// merely to answer 503. A durable ledger was necessary for that route, not sufficient.
	if mountedPayments {
		served(collectionWhy)
	} else {
		h.MountPayments(nil)
		absent("p21_payments", collectionAbsentWhy)
	}
	h.MountAuthoring(nil)
	absent("p13_authoring", noDurableStore)
	if !mountedForgeDelivery {
		h.MountForgeDelivery(nil)
		absent("p12_forge_delivery", deliveryAbsentReason(pg, planCatalogPath(), catalogUnloadable))
	}

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

// publishedCatalog returns path when it names a file that EXISTS, and "" otherwise.
//
// 🔴 A path that names nothing is NOT a published catalog, and the difference decides which of two very
// different things a customer sees. plancfg.NewResolver does not read the file — nothing does until the
// billing page asks — so a deployment that declares the variable and supplies no file would MOUNT
// billing and then fail on the first read, turning a clean "not configured, here is the variable" into
// a runtime error on a customer's invoice page.
//
// That matters now because the deploy manifests declare these paths: the variable is where the file
// GOES, and its presence is a convention, not a claim that somebody put one there.
//
// ⚠️ It is a check at BOOT, so a catalog published later needs a restart to be picked up. That is the
// same reload boundary plancfg already has (Resolver.Reload is an explicit operator action, not a
// watcher), and a capability that flickers between mounted and absent as a file appears would be worse.
func publishedCatalog(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return ""
	}
	return path
}

// planCatalogPathEnv is the environment variable naming the published plan catalog, beside
// modelcatalog.PathEnv. Named once because it was spelled as a literal in three places — the getter and
// both absent-reasons — and a variable spelled two ways is a gate that silently never fires.
//
// ⚠️ NOT plancfg.PlanConfigPathEnv (HEROS_PLAN_CONFIG_PATH). That one is plancfg's own
// FileSourceFromEnv convenience, which nothing in the deployed path calls: launch resolves the path
// itself so it can tell "unset" from "names a file that is not there", and hands plancfg a
// NewFileSource. Wiring the other variable here would read a catalog no manifest publishes.
const planCatalogPathEnv = "PLAN_CATALOG_PATH"

// planCatalogPath is the published plan catalog this deployment resolves plans from.
//
// Read from the environment beside CONSOLE_HEALTH_URL rather than added to config.Config, matching how
// launch already passes deployment facts that only one capability needs. It is a FILE and never a
// git-tracked one — plancfg.Source says so in its own contract, because the catalog carries prices.
func planCatalogPath() string { return publishedCatalog(os.Getenv(planCatalogPathEnv)) }

// billingAbsentReason names the ONE next action for an operator whose billing surface is not served.
//
// Two different gaps, two different remedies: no database is a deployment-wide fact, and no catalog is a
// single file away. Collapsing them into "billing is unavailable" would send an operator to read the
// wrong runbook.
func billingAbsentReason(pg *sql.DB, catalog string, loadErr error) string {
	if pg == nil {
		return "this deployment declares no platform database (DATABASE_URL is unset)"
	}
	if catalog == "" {
		return "no plan catalog is published (" + catalogHint(planCatalogPathEnv) + ") — billing cannot " +
			"resolve which plan a customer is on, and a billing page that cannot name the plan cannot " +
			"price anything"
	}
	if loadErr != nil {
		// Reachable at last. This branch existed from the start and nothing could produce it, because
		// nothing loaded the catalog — so the one state it described was the one state that could not
		// occur. It names the parse error, which is the only actionable thing here.
		return "the published plan catalog at " + catalog + " could not be loaded: " + loadErr.Error()
	}
	return "the plan catalog could not be loaded"
}

// menuSource joins the published model catalog onto the registry's entries. It is here rather than in
// internal/modelcatalog because it is a WIRING decision: which registry this deployment resolves refs
// against is a property of the deployment, and modelcatalog takes the lister as an argument precisely so
// it does not have to know.
type menuSource struct {
	src modelcatalog.Source
	reg modelcatalog.ModelLister
}

func (m menuSource) Menu(ctx context.Context) (proposal.Menu, string, error) {
	menu, rep, err := modelcatalog.Menu(ctx, m.src, m.reg)
	if err != nil {
		return proposal.Menu{}, "", err
	}
	// The report is returned as the DETAIL the generator quotes when the menu is empty. "3 models are
	// registered and none has a published tier" is an action; "no candidates" is not.
	return menu, fmt.Sprintf("%d model(s) registered, %d published, %d usable; unjudged: %v",
		rep.Registered, rep.Published, rep.Usable, rep.Unjudged), nil
}

// proposalGenAbsentReason names the ONE next action for an operator whose generator is not served.
// Two different gaps, two different remedies — the same split billingAbsentReason makes.
//
// ⚠️ It reports the catalog through catalogHint for the same reason the plan catalog does, and the
// reason is sharper here than it looks: this sentence used to say "MODEL_CATALOG_PATH is unset"
// unconditionally, which was true only while nothing set the variable. The deploy manifests now DO set
// it, so the common state on a fresh install is "set, and no file behind it" — and the old sentence
// told that operator to set a variable that was already set, which reads as a broken deployment rather
// than as one file away.
func proposalGenAbsentReason(pg *sql.DB) string {
	if pg == nil {
		return "this deployment declares no platform database (DATABASE_URL is unset)"
	}
	return "no model catalog is published (" + catalogHint(modelcatalog.PathEnv) + "). A proposal that a " +
		"cheaper model would do the job needs a capability tier and a cost estimate per model, and the " +
		"model registry records neither — so without a published catalog nothing can be proposed, and " +
		"an empty proposal list would read as 'we found nothing wrong'"
}

// deliveryAbsentReason names the ONE next action for an operator whose delivery surface is not served.
// The same split billingAbsentReason makes, for the same reason: no database is deployment-wide, and no
// plan catalog is one file away.
func deliveryAbsentReason(pg *sql.DB, catalog string, loadErr error) string {
	if pg == nil {
		return "this deployment declares no platform database (DATABASE_URL is unset), so there is no " +
			"verification state to gate a delivery on and no route registry to deliver through"
	}
	if catalog == "" {
		return "no plan catalog is published (" + catalogHint(planCatalogPathEnv) + "), so this " +
			"deployment cannot decide whether a tenant is entitled to have a pull request opened for " +
			"them — and delivery writes into a customer's repository, which is not a question to answer " +
			"by default"
	}
	if loadErr != nil {
		return "the published plan catalog at " + catalog + " could not be loaded, so no entitlement can " +
			"be decided: " + loadErr.Error()
	}
	return "the plan catalog could not be loaded"
}

// SandboxContainedEnv is the operator's DECLARATION that this process runs inside a container which
// denies network egress and mounts only a read-only working set.
const SandboxContainedEnv = "HEROS_SANDBOX_CONTAINED"

// compileSandbox returns the isolate the build gate runs the customer's compiler inside, and the phrase
// the capability line reports.
//
// 🔴 IT NEVER ASSUMES CONTAINMENT. sandbox.NewContainedEnforcer ADVERTISES network denial and filesystem
// scope as in force — it does not provide them; the outer container does. Its own doc says using it on
// a bare host "would claim containment that is not there — the one thing the fail-closed design exists
// to prevent". So it is selected only when an operator has declared the posture, and the declaration is
// an environment variable rather than a guess, because nothing this process can observe distinguishes
// "no egress" from "egress that happens to be idle".
//
// ⚠️ THE DECLARATION IS A REAL CLAIM, and a deployment that sets it wrongly gets a build gate that runs
// a customer's compiler with network access and a writable host. It belongs on a container that has no
// route out — which is NOT the API server, because the API server needs the database. That is a
// deployment topology (a separate compile worker), and until one exists this variable stays unset and
// the gate falls back to parsing, which is the honest state rather than a broken one.
func compileSandbox() (*sandbox.Sandbox, string) {
	if strings.TrimSpace(os.Getenv(SandboxContainedEnv)) != "1" {
		return nil, "diff is parsed, not built — " + SandboxContainedEnv + " is unset, so no isolate can " +
			"hold a customer's compiler"
	}
	// The enforcer still checks what it can guarantee on its own (env scrub, resource bounds) and the
	// gate still fails closed on top of that: declaring the posture does not skip the gate, it satisfies
	// the two capabilities only the runtime can provide.
	return sandbox.New(sandbox.NewContainedEnforcer()), "diff is compiled inside a declared isolate"
}

// catalogHint names the ONE next action for an unpublished catalog, and the two cases are different
// actions: an unset variable is a deployment that has not declared where the file goes, and a set one
// pointing at nothing is a deployment that has and nobody put the file there. The manifests declare
// these paths, so the second is now the common case and telling them apart is what makes the message
// useful rather than a restatement.
func catalogHint(env string) string {
	if p := strings.TrimSpace(os.Getenv(env)); p != "" {
		return env + " names " + p + ", and no file is there"
	}
	return env + " is unset"
}

// runMonitorAbsentReason is why the LIVE run monitor is the one read surface this deployment cannot
// serve — stated in full because the generic "no adapter exists outside a demo binary" was the same
// stale sentence P5.5 and P12 carried, and it is wrong here for a different and more interesting
// reason: an adapter would not help.
//
// Two independent blockers, and neither is a wiring gap:
//
//   - IT IS NOT LIVE, AND CANNOT BE. The platform learns of a run when the CLI LINKS it, which happens
//     after the run finished. `/monitor/stream` is SSE that streams snapshots "until the run is
//     terminal"; over a linked run it would emit one frame of a finished run and close, every time. A
//     live endpoint that is never live is a worse answer than 503 — the 503 says "not installed", and
//     the stream would say "this is what watching your run looks like".
//
//   - PER-NODE STATE IS EVAL DATA. RunMonitorNode.State is ok | failed | timed_out, driven by the
//     reliability signal on a span. The boundary carries cost, latency and tokens per node — that is
//     what the scorecard renders — and carries no per-node correctness at all, which is why
//     hostedscorecard reports `failure_attribution: unavailable` in so many words. Filling State with
//     "ok" for every node would invent the one field the view exists to show.
//
// What the platform CAN show of a linked run is already mounted: the linked-run record (its scores with
// intervals) and the scorecard (cost and latency attributed per node). This capability is the part that
// needs the platform to have RUN the workflow, and it does not.
const runMonitorAbsentReason = "this platform does not execute customer workflows: it learns of a run " +
	"only when the CLI links it, which is after the run has finished, so there is nothing live to " +
	"stream — and per-node state (ok/failed/timed_out) is derived from per-node correctness, which is " +
	"eval data that stays on the machine that ran the eval. The parts of a linked run this deployment " +
	"CAN show are mounted: the run's scores (p11_run_linking) and its per-node cost and latency " +
	"(p45_scorecard)"

// ── P21 collection: the declaration, the provider, and why it is a declaration ───────────────────────

// BillingModeEnv names the mode this deployment collects payments in: `test` or `live`.
//
// 🔴 ITS PRESENCE IS THE DECLARATION. Unset means this deployment collects nothing, which is the
// default and the state every open-core install stays in — see deploy/README.md. Set, it mounts
// checkout, plan changes and the provider webhook.
//
// ⚠️ `live` MOVES REAL MONEY. It is spelled out rather than inferred from anything — not from a key
// prefix, not from the hostname, not from NODE_ENV — for the same reason HEROS_SANDBOX_CONTAINED is a
// declaration: nothing this process can observe distinguishes "the operator meant live" from "the
// operator pasted the wrong key", and guessing wrong charges a customer. billing.NewStripeProvider
// normalizes an empty mode to test on its own, so every path that forgets this variable charges
// nothing real; this one refuses to mount at all instead, because a checkout button that silently
// runs in test mode is worse than no button.
const BillingModeEnv = "BILLING_MODE"

// collectionDeps is what a declared provider contributes to the billing service: the provider itself
// and the secrets seam the SERVICE also needs for webhook signature verification.
type collectionDeps struct {
	provider billing.Provider
	secrets  billing.Secrets
}

// collectionProvider resolves this deployment's payment provider from its declaration.
//
// It returns the stub and a REASON when nothing is declared or the declaration cannot be honoured. A
// refusal here is never fatal: P7 billing stays mounted and fully served either way, because reading
// what a customer owes does not require the ability to charge them.
func collectionProvider(secrets providergateway.Secrets) (collectionDeps, billing.Mode, string) {
	stub := collectionDeps{provider: billing.NewStubProvider()}
	const notDeclared = "no payment provider is configured on this deployment (" + BillingModeEnv +
		" is unset); the durable ledger exists, but checkout and plan changes need a provider to call"

	declared := strings.TrimSpace(os.Getenv(BillingModeEnv))
	if declared == "" {
		return stub, "", notDeclared
	}
	mode := billing.Mode(strings.ToLower(declared))
	if mode != billing.ModeTest && mode != billing.ModeLive {
		// Refused rather than defaulted. Defaulting a typo to test would mount a checkout button that
		// takes no real money on a deployment whose operator believes it does; defaulting it to live is
		// unthinkable. Neither guess is safe, so neither is made.
		return stub, "", BillingModeEnv + "=" + declared + " is not a billing mode (test|live), so no " +
			"payment provider was configured — this is refused rather than defaulted, because both " +
			"defaults are wrong in a way that is only discovered by a customer"
	}
	if secrets == nil {
		return stub, "", "a billing mode is declared (" + BillingModeEnv + "=" + declared + ") but this " +
			"deployment resolved no secrets source, and a billing credential is never read from code, " +
			"config or the environment"
	}
	ms, err := billing.NewManagedSecrets(secrets)
	if err != nil {
		return stub, "", "a billing mode is declared (" + BillingModeEnv + "=" + declared + ") but the " +
			"secrets source could not be adapted for billing: " + err.Error()
	}
	p, err := billing.NewStripeProvider(ms, mode, time.Now)
	if err != nil {
		return stub, "", "a billing mode is declared (" + BillingModeEnv + "=" + declared + ") but the " +
			"payment provider could not be built: " + err.Error()
	}
	return collectionDeps{provider: p, secrets: ms}, mode, ""
}

// preflightTimeout bounds the boot-time pricing check. Generous enough for four to ten sequential
// provider reads on a cold connection, short enough that a hung provider cannot leave the report
// permanently in "never ran".
const preflightTimeout = 45 * time.Second

// startPricingPreflight runs the configured price references against the provider ONCE, in the
// background, as soon as collection is mounted.
//
// ## Why this call has to exist here
//
// billing.PreflightPricing and the two readers of its cache (Service.PricingStatus, which the payments
// view renders as a pricing issue, and Service.UnpurchasablePlans, which stops a customer being sent to
// a checkout that cannot complete) were all reachable ONLY from cmd/proof/payments. Nothing in the
// deployed binary ever produced the report they read, so on every real deployment PricingStatus reported
// "never ran" forever and UnpurchasablePlans returned nothing — not "nothing is wrong", but "nothing has
// looked". The whole preflight decision was shipped and then not wired, and the shape of that gap is
// specific: the first code to discover a price reference that does not resolve was RaiseCharge, at the
// first charge of the period, against a customer who had already accrued it.
//
// Under BILLING_MODE=live that gap costs real money in both directions — a customer sent to a checkout
// that fails, or a period that cannot be closed.
//
// ## Three properties, matching what preflight.go promises about itself
//
//  1. BACKGROUND, never blocking the boot. The platform must come up with the provider unreachable; a
//     readiness that waited on an outside network would make somebody else's outage look like ours.
//  2. IT DOES NOT GATE ANYTHING. The report is stored and read by surfaces that already know how to say
//     "not checked". A failed preflight must not refuse a charge the provider would have accepted.
//  3. RUN ONCE, not polled. The result is a fact about the CONFIGURATION, and the configuration changes
//     when an operator publishes a catalog — which already requires a restart (publishedCatalog stats at
//     boot). A poll would add provider load to answer a question whose input cannot change underneath it.
func startPricingPreflight(svc *billing.Service, mode billing.Mode) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
		defer cancel()
		rep, err := svc.PreflightPricing(ctx)
		switch {
		case err != nil:
			// Loud, and it names the mode: an unresolved reference under `live` is an operator's next
			// action, not a line to scroll past. The error already carries the first offending plan,
			// kind and reference — the three things needed to fix it.
			log.Printf("billing pricing preflight (mode %s): %s", mode, err)
		case !rep.Verified:
			log.Printf("billing pricing preflight (mode %s): NOT CHECKED — %s", mode, rep.Detail)
		default:
			log.Printf("billing pricing preflight (mode %s): %s", mode, rep.Summary())
		}
	}()
}
