package adminlaunch

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminstore"
	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/legal"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/runqueue"
)

// services.go mounts the operator SERVICES — the pages behind the sign-in.
//
// # Why they were all nil, and why that was the wrong answer
//
// The first cut of `adminlaunch` wired identity, authorization and the command path, and left every
// service nil on the reasoning that their only implementations were the demo stores in
// `cmd/proof/operatorconsole` (five synthetic tenants and a stub payment provider). Putting invented
// tenants in front of an operator IS worse than an empty surface, so the reasoning was right — but the
// premise was wrong for most of them. `internal/launch/capabilities.go` already builds DURABLE
// Postgres stores for the customer API over the same tables: `account.PGStore`, `billing.PGLedger`,
// `metering.PGUsageStore`, `deliveryrecord.PGStore`, `legal.PGStore`. Nothing was missing except
// somebody handing them to the admin surface.
//
// So the honest split is per-service, not all-or-nothing, and this file draws it explicitly. Every
// service below is either mounted over a real durable store, or left nil for a REASON THAT NAMES WHAT
// IS MISSING — which is what the console renders as "this deployment does not carry the X surface".
//
// # The four that stay absent, and why each is a decision rather than an omission
//
//   - KILL SWITCH. Its only store is `MemKillSwitchStore`, and worse, nothing reads it: the delivery
//     path in capabilities.go passes a `HaltReaderFunc` that returns "not halted" unconditionally.
//     Mounting it would give an operator a fleet-wide brake connected to nothing, that also forgets it
//     was pulled when the pod restarts. A control that reports success and halts nothing is the single
//     most dangerous thing on this console.
//   - MODEL REGISTRY. `adminops.ModelRegistry` is an in-memory map with no durable store. It is a
//     WRITE surface — an operator adds a model and its price references — so mounting it means the
//     work is silently lost on the next restart, which is the same class of failure the admin
//     directory's own migration exists to remove.
//   - GDPR / DATA DELETION. `SubjectContentStore` has no implementation anywhere in the tree. An
//     erasure surface that cannot actually reach the subject's content would produce a completion
//     record for an erasure that did not happen.
//   - RELEASES and AXES. `ReleaseSource` and `AxisAdoptionSource` likewise have no implementation. The
//     data exists in the release pipeline and the coverage source; nothing carries it here yet.
//
// Those four print a reason an operator can act on. The rest now render real data.

// PlatformSources are the durable stores the operator services read.
//
// Built here from the same `*sql.DB` the customer capabilities use, rather than threaded out of
// `mountCapabilities`: every one is a stateless handle over shared tables, so a second instance reads
// exactly the same rows. What is deliberately NOT duplicated is anything holding state in the process
// — see `deltas` and `authored` below, which are honestly empty on both sides.
// AgentAdmin is the P30 wiring the OPERATOR console needs to do more than read: the durable version
// store behind its publish control, the publisher itself, and the rehearsal gate its activate control
// must pass.
//
// 🔴 It is carried out of here rather than rebuilt in `adminlaunch` because every piece already exists
// in this function — the registry, the provider gateway, the version store the platform source reads.
// A second construction would be a second set of stores answering the same questions, and the first
// time they disagreed the console would render one and the runtime would serve the other.
//
// Every field may be nil: a deployment with no platform database has no version store, and one with
// no calibration fixtures has no gate. Both cases leave the console refusing rather than pretending —
// see `newAgentRehearsal`.
type AgentAdmin struct {
	Versions  *herosagent.PGVersionStore
	Publisher *herosagent.Publisher
	Rehearse  adminops.RehearseFunc
	// RehearsalWhy is set when a gate could not be built, so the boot log names what to fix rather
	// than leaving an operator to discover it by pressing activate.
	RehearsalWhy string
}

type PlatformSources struct {
	Accounts   account.Store
	Plans      *plancfg.Resolver
	Usage      *metering.PGUsageStore
	Ledger     billing.Ledger
	Meter      *metering.Meter
	Billing    *billing.Service
	Deltas     metering.VerifiedDeltaLedger
	Authored   authoring.Recorder
	Delivery   *deliveryrecord.PGStore
	Queue      *runqueue.Queue
	Legal      *legal.Service
	Admission  *adminops.Admission
	KillSwitch *adminstore.KillSwitch
	Models     *adminops.ModelRegistry
	Releases   *adminstore.Releases
	Axes       *adminstore.AxisAdoption
	Subjects   *adminstore.SubjectContent
	// AgentSpend is the P30 placement + cap + meter surface, wired to the durable stores (task 9.3).
	// Nil on a deployment with no platform database, where the console renders the absence it already
	// knows how to show.
	AgentSpend adminops.AgentSpendSource
	// AgentInferences counts pinned inferences for the `/agent` overview. Nil reads as UNKNOWN, never
	// as zero — a zero there would claim the agent has never done anything.
	AgentInferences *herosagent.PGInferenceStore
	// CatalogWhy is set when the plan catalog is absent or unreadable, which is what stops the three
	// plan-dependent services from mounting. Named so the log says which file to publish.
	CatalogWhy string
}

// jobQueue adapts *runqueue.Queue to adminops.JobQueue.
//
// The queue has List/Requeue/Cancel/Stats already; the one method it lacks is `Describe`, which the
// operator surface requires precisely so an operator can confirm the console is pointed at the REAL
// queue rather than a fixture. That is a sentence about the deployment, not behaviour the queue owes
// its other callers, so it is added here rather than pushed down into `internal/runqueue`.
type jobQueue struct{ *runqueue.Queue }

func (jobQueue) Describe() string {
	return "postgres run_queue (the same queue the P4/P6 workers lease from)"
}

// buildSources opens every durable store the operator services need.
//
// A store that cannot be constructed is a hard error, not a silently absent service: these are all
// plain handles over tables the schema just migrated, so a failure here means the database is not what
// the process thinks it is, and serving a half-populated operator console over that is worse than not
// starting.
func buildSources(pg *sql.DB, secretsSrc providergateway.Secrets, now func() time.Time) (*PlatformSources, error) {
	src := &PlatformSources{}

	accounts, err := account.NewPGStore(pg)
	if err != nil {
		return nil, fmt.Errorf("account store: %w", err)
	}
	src.Accounts = accounts

	usage, err := metering.NewPGUsageStore(pg)
	if err != nil {
		return nil, fmt.Errorf("usage store: %w", err)
	}
	src.Usage = usage

	ledger, err := billing.NewPGLedger(pg)
	if err != nil {
		return nil, fmt.Errorf("billing ledger: %w", err)
	}
	src.Ledger = ledger

	// ⚠️ Both of these are IN-MEMORY on this deployment and both are honestly empty rather than wrong.
	//
	// `MemVerifiedDeltas` holds the P6 optimizer's verified savings and no optimizer loop runs here, so
	// an empty ledger is the TRUE answer — the cross-tenant page reports "none verified", which is a
	// fact. `MemRecorder` is the authored-change record, and `CrossTenantConfig.Authored` documents
	// that nil yields no improvement rows rather than zeroes, for the same reason. Neither fabricates.
	src.Deltas = metering.NewMemVerifiedDeltas()
	src.Authored = authoring.NewMemRecorder()
	src.Meter = metering.NewMeter(metering.NewMemCostEvents(), usage)

	// P30 §9.3 · the agent's durable placement, ceiling and meter stores. Built here with the same
	// `*sql.DB` everything else uses, so the operator console's controls reach the tables the runtime
	// reads rather than the nothing they reached before.
	agentPlacements, aperr := herosagent.NewPGPlacementStore(pg)
	if aperr != nil {
		return nil, fmt.Errorf("agent placement store: %w", aperr)
	}
	agentCaps, acerr := herosagent.NewPGCapStore(pg)
	if acerr != nil {
		return nil, fmt.Errorf("agent cap store: %w", acerr)
	}
	agentSpendStore, aserr := herosagent.NewPGSpendStore(pg)
	if aserr != nil {
		return nil, fmt.Errorf("agent spend store: %w", aserr)
	}
	agentInferences, aierr := herosagent.NewPGInferenceStore(pg)
	if aierr != nil {
		return nil, fmt.Errorf("agent inference store: %w", aierr)
	}
	src.AgentInferences = agentInferences
	src.AgentSpend = newAgentSpend(agentPlacements, agentCaps, agentSpendStore, agentInferences, now,
		"operator-console")

	src.Delivery = deliveryrecord.NewPGStore(pg)
	src.Queue = runqueue.New(pg)

	// The fleet brake, now durable (migration 0014's table finally has a writer). Held in memory it was
	// disarmed by every restart WHILE THE CONSOLE STILL SHOWED the operator's last state — see
	// adminstore/killswitch.go.
	ks, err := adminstore.NewKillSwitch(pg)
	if err != nil {
		return nil, fmt.Errorf("kill switch: %w", err)
	}
	src.KillSwitch = ks

	// The model registry, durable over migration 0036. It is a WRITE surface whose price references
	// decide what a run cost, so in memory it lost an operator's work AND silently changed SUM.
	models := adminops.NewModelRegistry(now)
	mw, err := adminstore.NewModelRegistry(pg)
	if err != nil {
		return nil, fmt.Errorf("model registry store: %w", err)
	}
	if err := models.SetWriter(mw); err != nil {
		return nil, fmt.Errorf("model registry: %w", err)
	}
	rows, err := mw.Models(context.Background())
	if err != nil {
		return nil, fmt.Errorf("model registry: %w", err)
	}
	closed, err := mw.ClosedPeriods(context.Background())
	if err != nil {
		return nil, fmt.Errorf("model registry: %w", err)
	}
	if err := adminops.LoadModels(models, rows, closed); err != nil {
		return nil, fmt.Errorf("model registry: %w", err)
	}
	if src.Releases, err = adminstore.NewReleases(pg); err != nil {
		return nil, fmt.Errorf("release record: %w", err)
	}
	if src.Axes, err = adminstore.NewAxisAdoption(pg); err != nil {
		return nil, fmt.Errorf("axis adoption: %w", err)
	}
	if src.Subjects, err = adminstore.NewSubjectContent(pg); err != nil {
		return nil, fmt.Errorf("subject content: %w", err)
	}

	src.Models = models
	log.Printf("operator console: model registry loaded %d model(s) and %d closed period(s) from the platform database",
		len(rows), len(closed))

	// Admission with NO kill-switch reader, which is the correct wiring here and not an oversight.
	//
	// `AllowMerge` checks the switch FIRST and treats an unreadable state as halt; passing a store that
	// nothing else consults would make this console the only place a brake appears to exist. With nil
	// it reads tenant status alone — exactly what it can actually answer for.
	adm, err := adminops.NewAdmission(accounts, nil)
	if err != nil {
		return nil, fmt.Errorf("admission: %w", err)
	}
	src.Admission = adm

	// ── The plan catalog, which three services resolve against ──
	//
	// 🔴 A Resolver does NOT read its source on construction: `Reload` is what loads it, and without
	// that call every ResolvePlan returns ErrNoConfig while the surface looks mounted. That exact defect
	// is recorded in capabilities.go for the customer side; the same call is required here for the same
	// reason.
	catalog := strings.TrimSpace(os.Getenv("PLAN_CATALOG_PATH"))
	//
	// Three distinct reasons, never collapsed into "no catalog": unset is a deployment-wide fact, an
	// unreadable file is a mount or permission problem, and one that does not parse is a content bug.
	// They send an operator to three different places, and `CatalogWhy` is what the boot log quotes.
	if catalog == "" {
		src.CatalogWhy = "PLAN_CATALOG_PATH is unset on this deployment, so no plan configuration is published"
	} else if _, statErr := os.Stat(catalog); statErr != nil {
		src.CatalogWhy = fmt.Sprintf("the published plan catalog %s cannot be read (%v)", catalog, statErr)
	} else {
		plans := plancfg.NewResolver(plancfg.NewFileSource(catalog), nil)
		if _, rerr := plans.Reload("adminlaunch"); rerr != nil {
			src.CatalogWhy = fmt.Sprintf("the published plan catalog %s does not parse (%v)", catalog, rerr)
		} else {
			src.Plans = plans
		}
	}

	// The billing SERVICE, for the oversight read model.
	//
	// Its provider is deliberately the stub: `BillingService` is a READ model over the platform's own
	// ledger — which is the authority for what was charged — and none of its routes make a provider
	// call. A real provider here would be a credential this surface has no use for.
	if src.Plans != nil {
		// The DEPLOYMENT's own secrets source, not a stub. `NewManagedSecrets` resolves at the moment of
		// use, so passing the real source costs nothing on a read-only surface and means that if a write
		// path is ever mounted here it authenticates rather than discovering a placeholder.
		secrets, serr := billing.NewManagedSecrets(secretsSrc)
		if serr != nil {
			return nil, fmt.Errorf("billing secrets: %w", serr)
		}
		svc, berr := billing.NewService(billing.NewStubProvider(), ledger, accounts, src.Plans, src.Meter, secrets)
		if berr != nil {
			return nil, fmt.Errorf("billing service: %w", berr)
		}
		src.Billing = svc
	}

	// Consent state, for the oversight page's "which legal versions each tenant owes".
	if origin := strings.TrimSpace(os.Getenv("CONSOLE_HEALTH_URL")); origin != "" {
		// newID is supplied rather than left nil: `Service.Record` calls it unconditionally, so a nil
		// would be a panic on the first consent write. The oversight page only READS today, which is
		// exactly why the nil would have survived review and fired later.
		src.Legal = legal.NewService(legal.NewPGStore(pg), legal.NewHTTPManifestSource(originOf(origin)), now,
			func() string { return "consent-" + strconv.FormatInt(now().UnixNano(), 36) })
	}
	return src, nil
}

// originOf reduces a health URL to its scheme+host, which is what the manifest source wants.
func originOf(healthURL string) string {
	if i := strings.Index(healthURL, "://"); i >= 0 {
		if j := strings.Index(healthURL[i+3:], "/"); j >= 0 {
			return healthURL[:i+3+j]
		}
	}
	return healthURL
}

// mountServices fills in every operator service that has a real source, and logs the ones that do not.
func mountServices(exec *adminops.Executor, src *PlatformSources, sessions *adminidentity.SessionStore,
	identity adminidentity.ProviderInfo, readiness adminops.ReadinessSource, agentAdmin *AgentAdmin,
	deps *serviceSet) error {

	// ── Two that need nothing but the command path ──
	//
	// These were nil purely because the first cut mounted nothing. The audit page and impersonation
	// both read through the executor's own audit chain and gate.
	audit, err := adminops.NewAuditService(exec)
	if err != nil {
		return fmt.Errorf("audit service: %w", err)
	}
	deps.Audit = audit

	imp, err := adminops.NewImpersonationService(exec)
	if err != nil {
		return fmt.Errorf("impersonation service: %w", err)
	}
	deps.Impersonation = imp

	// ── Durable, over Postgres ──
	if src.Plans != nil {
		tenants, terr := adminops.NewTenantService(exec, src.Accounts, src.Plans, src.Admission)
		if terr != nil {
			return fmt.Errorf("tenant service: %w", terr)
		}
		deps.Tenants = tenants

		ents, eerr := adminops.NewEntitlementService(exec, src.Accounts, src.Plans)
		if eerr != nil {
			return fmt.Errorf("entitlement service: %w", eerr)
		}
		deps.Entitlements = ents

		bill, berr := adminops.NewBillingService(exec, src.Billing, src.Deltas, nil)
		if berr != nil {
			return fmt.Errorf("billing oversight: %w", berr)
		}
		deps.Billing = bill
	} else {
		log.Printf("operator console: tenants, entitlements and billing are NOT mounted — %s", src.CatalogWhy)
	}

	cross, err := adminops.NewCrossTenantService(exec, adminops.CrossTenantConfig{
		Accounts: src.Accounts, Meter: src.Meter, Ledger: src.Ledger,
		Admission: src.Admission, Authored: src.Authored, Deltas: src.Deltas,
	})
	if err != nil {
		return fmt.Errorf("cross-tenant service: %w", err)
	}
	deps.CrossTenant = cross

	delivery, err := adminops.NewDeliveryService(exec, src.Delivery, src.Accounts)
	if err != nil {
		return fmt.Errorf("delivery service: %w", err)
	}
	deps.Delivery = delivery

	jobs, err := adminops.NewJobService(exec, jobQueue{src.Queue})
	if err != nil {
		return fmt.Errorf("job service: %w", err)
	}
	deps.Jobs = jobs

	// The fleet brake. It is mounted now that the state is durable — but see the note in Build about
	// what it does and does not yet halt, which is stated on the boot log rather than left implied.
	kill, err := adminops.NewKillSwitchService(exec, src.KillSwitch, adminops.DefaultKillSwitchPolicy())
	if err != nil {
		return fmt.Errorf("kill switch service: %w", err)
	}
	deps.KillSwitch = kill
	// Admission consults the SAME store the console writes, so a tenant's halt state is one answer
	// rather than two. Attached after construction because that is the order the dependencies resolve.
	src.Admission.SetKillSwitch(kill)

	registry, err := adminops.NewRegistryService(exec, src.Models)
	if err != nil {
		return fmt.Errorf("model registry service: %w", err)
	}
	deps.Registry = registry

	release, err := adminops.NewReleaseService(exec, src.Releases)
	if err != nil {
		return fmt.Errorf("release service: %w", err)
	}
	deps.Release = release

	axis, err := adminops.NewAxisService(exec, src.Axes)
	if err != nil {
		return fmt.Errorf("axis service: %w", err)
	}
	deps.Axis = axis

	// P30 · the analysis agent.
	//
	// 🔴 Mounted with a version store and NOTHING ELSE on this deployment: no publisher, no spend
	// source, no inference counter, no kill-switch reader. Every one of those reads as its own honest
	// state rather than as a zero — an unknown inference count, an empty spend table, an unarmed
	// switch — and the console renders them that way. Mounting the surface with a publisher it does not
	// have would offer a Publish control that fails at the moment somebody presses it.
	//
	// The store is in-memory here for the same reason: `heros_agent_version` exists (migration 0046) and
	// nothing in this launch path opens it yet, so an in-memory store is what this deployment honestly
	// has. It is stated rather than implied — a restart loses published definitions, which is why
	// nothing here activates one.
	// 🔴 The SPEND SOURCE and the INFERENCE COUNTER are wired now (task 9.3). Both were nil, so the
	// console's placement column and its cap editors wrote to nothing: §6 shipped the controls, §7 and
	// §9.2 shipped the tables, and there was no wire between them. A surface that accepts a decision
	// and drops it is worse than one that refuses — the operator comes away believing the fleet is
	// configured.
	//
	// The publisher stays nil and the version store stays in-memory, which remains the honest state of
	// THIS deployment: nothing here opens `heros_agent_version`, so a Publish control would fail at the
	// moment somebody pressed it. Stated rather than quietly wired to a store that would lose its rows
	// on restart.
	//
	// 🔴 THE VERSION STORE AND THE PUBLISHER ARE NOW THE DURABLE ONES when the capability graph could
	// build them (P30 §6.4). What stood here was an in-memory store and a nil publisher, and the
	// comment above said what that meant: "a Publish control would fail at the moment somebody pressed
	// it". `heros_agent_version` (migration 0046) was open two packages away the whole time, read by
	// the platform half — so the console rendered an empty list of definitions on a deployment that
	// could have had them, and the operator surface for configuring the agent could not configure it.
	//
	// A deployment with no platform database still gets the in-memory pair, and the console still
	// refuses at publish rather than losing rows silently.
	agentVersions := adminops.AgentVersions(herosagent.NewMemVersionStore())
	var agentPublisher adminops.AgentPublisher
	var rehearse adminops.RehearseFunc
	if agentAdmin != nil {
		if agentAdmin.Versions != nil {
			agentVersions = agentAdmin.Versions
		}
		if agentAdmin.Publisher != nil {
			agentPublisher = agentAdmin.Publisher
		}
		rehearse = agentAdmin.Rehearse
	}
	agent, err := adminops.NewAgentService(exec, agentVersions,
		agentPublisher, src.AgentSpend, src.AgentInferences, nil, herosagent.RunnerHosts{})
	if err != nil {
		return fmt.Errorf("agent service: %w", err)
	}
	// 🔴 The activation gate. Without it `Publisher.Activate` refuses every `pending` version for ever
	// — safe, and indistinguishable from a gate that measured and said no. The boot log says which of
	// the two this deployment is.
	if rehearse != nil {
		agent = agent.WithRehearsal(rehearse)
		log.Printf("operator console: the agent activation gate is ARMED — pressing activate runs the " +
			"pinned calibration set against a live model, on this deployment's own provider credential")
	} else {
		why := "this deployment carries no rehearsal gate"
		if agentAdmin != nil && agentAdmin.RehearsalWhy != "" {
			why = agentAdmin.RehearsalWhy
		}
		log.Printf("operator console: 🔴 NO AGENT ACTIVATION GATE — %s. A definition can be published "+
			"and can NEVER be activated, because nothing can move its rehearsal state off `pending`", why)
	}
	deps.Agent = agent

	gdpr, err := adminops.NewGDPRService(exec, src.Subjects)
	if err != nil {
		return fmt.Errorf("gdpr service: %w", err)
	}
	deps.GDPR = gdpr

	// Oversight tolerates a nil Legal/Readiness/Deployments and says so on the page rather than
	// inventing a row — which is why it mounts even where those are absent.
	oversight, err := adminops.NewOversightService(exec, adminops.OversightConfig{
		Sessions: sessions, Identity: identity, Legal: src.Legal,
		Tenants:   func() []string { return tenantIDs(src.Accounts) },
		Readiness: readiness,
	})
	if err != nil {
		return fmt.Errorf("oversight service: %w", err)
	}
	deps.Oversight = oversight

	// ── The four that stay absent. Named, with the reason an operator can act on. ──
	// 🔴 The kill switch is now DURABLE but is not yet CONNECTED to every merge path. Admission reads it
	// (set above), and the P12 delivery path in capabilities.go still passes a HaltReaderFunc that never
	// halts. Said out loud on every boot, because an operator who arms a brake is entitled to know
	// exactly what it stops — and "partially wired" is the state a console must never imply is "wired".
	log.Printf("operator console: the kill switch is durable and read by Admission, but the P12 delivery " +
		"path still passes a halt reader that never halts — arming stops admission-gated merges, not that path")
	// Every operator surface is now mounted. What each one can HONESTLY answer differs, and the two
	// limits worth knowing are stated here rather than discovered from a surprising zero.
	log.Printf("operator console: every surface is mounted. Two limits: axis ADOPTION is attributable " +
		"only through proposal.refusal_dimension (accepted proposals carry no axis column, so a zero " +
		"means 'not attributable', not 'unused'); the release record is empty until the release pipeline " +
		"writes to release_record/release_artefact")
	log.Printf("operator console: a data-subject erasure removes content across %d tables and "+
		"deliberately RETAINS audit, billing and consent records, which are held by obligation", 10)
	return nil
}

// tenantIDs lists the tenants the oversight page reports consent for.
func tenantIDs(accounts account.Store) []string {
	if accounts == nil {
		return nil
	}
	rows, err := accounts.List()
	if err != nil {
		// Logged, not fatal: the oversight page's own contract is that an unavailable read is reported
		// as unavailable rather than as an empty result, and returning nil here is what produces that.
		log.Printf("operator console: the tenant list for oversight could not be read: %v", err)
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, a := range rows {
		out = append(out, a.CustomerID)
	}
	return out
}

// serviceSet is the subset of api.AdminDeps this file fills, kept separate so Build's call site reads
// as one assignment rather than fifteen.
type serviceSet struct {
	Tenants       *adminops.TenantService
	Entitlements  *adminops.EntitlementService
	Billing       *adminops.BillingService
	Jobs          *adminops.JobService
	Impersonation *adminops.ImpersonationService
	CrossTenant   *adminops.CrossTenantService
	Audit         *adminops.AuditService
	Delivery      *adminops.DeliveryService
	Oversight     *adminops.OversightService
	KillSwitch    *adminops.KillSwitchService
	Registry      *adminops.RegistryService
	Release       *adminops.ReleaseService
	Axis          *adminops.AxisService
	Agent         *adminops.AgentService
	GDPR          *adminops.GDPRService
}
