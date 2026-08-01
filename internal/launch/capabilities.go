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
	"github.com/heros-foreal/agentd/internal/legal"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/registry"
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
		// The console URL the ingester prints back is built from the SAME origin the readiness probe
		// names, so a linked run's URL and the console we health-check can never disagree.
		linkStore := linkingest.NewPGStore(pg)
		consoleOrigin := originOf(consoleHealthURL)
		h.MountRunLinking(linkingest.New(nil, linkStore, func(_, runID string) string {
			if consoleOrigin == "" {
				return ""
			}
			return consoleOrigin + "/app/runs/" + url.PathEscape(runID)
		}))
		served("p11_run_linking")
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

	h.MountEvalBoard(nil)
	absent("p4_eval_board", noAdapter)
	h.MountScorecard(nil)
	absent("p45_scorecard", noAdapter)
	h.MountGraphEditor(nil)
	absent("p5_graph_editor", noAdapter)
	h.MountProposals(nil)
	absent("p55_proposals", noAdapter)
	h.MountOptimizer(nil)
	absent("p6_optimizer", noAdapter)
	h.MountPatternGraph(nil)
	absent("p35_pattern_graph", noAdapter)
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
