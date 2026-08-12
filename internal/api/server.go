// Package api serves the agentd HTTP surface.
//
// This is the minimal foundation left after the pivot to the LLM Agentic
// Workflow Evaluation & Configuration System: a health/readiness surface
// behind the existing auth policy. The retired agent's large route set
// (memory, collective, harness, vaults, catalog sync, …) has been removed.
// Discovery / config / runtime / eval endpoints are added per phase — see
// openspec/changes/* and docs/prd/.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/erroreport"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// Server is the agentd HTTP API.
type Server struct {
	// deviceSecrets holds an issued credential's plaintext between the approval that minted it and the
	// poll that collects it — in memory, on this process, and nowhere else. See internal/api/deviceauth.go
	// for why it is not a column.
	deviceSecrets deviceSecrets

	// DB is the SQLite dev ledger (auth keys, memory). NOT P2's store — see p2.go.
	DB      *sql.DB
	Cfg     config.Config
	Mux     *http.ServeMux
	Handler http.Handler

	// p2 is the Postgres-backed P2 read surface, mounted by MountConfigRuntime when available.
	configRuntime ConfigRuntimeStores

	// monitor is the P2.5 live run-monitoring read model, mounted by MountMonitor when available.
	monitor MonitorSource
	// monitorAbsent is why this deployment serves no live monitor. See MountMonitorAbsent.
	monitorAbsent string

	// p35 is the P3.5 pattern-classifier read model, mounted by MountPatternGraph when available.
	patternGraph PatternSource

	// p4 is the P4 eval-board read model, mounted by MountEvalBoard when available.
	evalBoard BoardSource
	// evalSet is the P30 eval-set read model, mounted by MountEvalSet when available.
	evalSet EvalSetSource

	// p45 is the P4.5 read-only scorecard read model, mounted by MountScorecard when available.
	scorecard ScorecardSource

	// p5 is the P5 interactive-graph-editor read+validate model, mounted by MountGraphEditor when available.
	graphEditor GraphEditorSource

	// p55 is the P5.5 ranked-recommendation + verification read model, mounted by MountProposals.
	proposals ProposalsSource

	// p6 is the P6 autonomous-optimizer governance surface (live monitor + grant/stop/rollback),
	// mounted by MountOptimizer when available.
	optimizer OptimizerSource

	// p7 is the P7 billing/usage read model (SUM, plan + entitlements, invoice breakdown, verified
	// gainshare evidence), mounted by MountBilling when available.
	billingView BillingSource

	// billing describes the live P7 rollout state (billing on/off, provider mode, gainshare,
	// auto-merge entitlement), reported by /readyz.
	//
	// The rollout state is a HEALTH SIGNAL, not a startup log line: "is this box charging real money"
	// is a question an operator asks about the box that is misbehaving, now — and a log line that
	// scrolled past three restarts ago cannot answer it.
	billing BillingRolloutDescriber

	// billingCapability names the live billing PROVIDER and the source its credentials come from
	// (P21 task 3.1), reported by /readyz.
	//
	// It is separate from `billing` above because the two answer different questions and can disagree in
	// a way that matters: the rollout says which gates are open, this says WHICH PROCESSOR is behind
	// them and WHERE its credential resolves from. The failure mode this makes checkable is the one
	// billing/secrets.go names — a deployment whose LLM credentials come from a manager while its
	// BILLING credentials quietly come from an environment variable, with a health endpoint confidently
	// wrong about both.
	billingCapability BillingCapabilityDescriber

	// billingWebhook is the ONE inbound-from-internet path — Stripe's webhook endpoint, mounted by
	// MountBillingWebhook when a deployment exposes it (P21 task 4.2). See p21.go for the posture.
	billingWebhook BillingWebhookSink

	// p21 is the P21 collection surface (plans by name, payment-method status, checkout), mounted by
	// MountPayments when available.
	payments PaymentsSource

	// p23 is the P23 consent surface — the ONLY new authenticated surface this phase adds, mounted by
	// RegisterConsent when available. Two endpoints, three fields, the caller's own tenant only (task 11.4).
	consent ConsentSource

	// secrets is the live provider-credential source, reported by /readyz.
	//
	// The SOURCE, never a credential: this holds the thing that can produce secrets precisely so the
	// endpoint can name it without anyone re-deriving it from configuration and getting a different
	// answer than the gateway did.
	secrets providergateway.Secrets

	// identity is the CUSTOMER identity provider's reachability, reported as its own /readyz component
	// (P22 task 7.1). Separate from `probes` because the answer is richer than up/down: an operator
	// diagnosing "nobody can sign in" needs the KIND and the ISSUER as well as the verdict, and a
	// component that reports only "degraded" sends them to read three dashboards to learn what this
	// response already knew.
	identity IdentityProbe

	// adminIdentity names the live OPERATOR identity provider (P22 task 6.4). The DOOR, never anything
	// behind it — no key, no secret id, no assertion.
	adminIdentity AdminIdentityDescriber

	// accounts is P27's identity surface: organizations, members, invitations and credentials. Nil means
	// this deployment does not mount the account system, and the routes are not registered at all — a
	// deployment answers 404 for a route it does not have rather than 503 for one it does.
	accounts AccountSurface

	// fallbackMail is the operator-visible mailer used when the mounted surface supplies none (P28).
	//
	// 🔴 It exists so there is NO nil-mailer path. A `if m != nil { send }` at six call sites is six places
	// a confirmation or reset link can be silently discarded, which is the single failure mode
	// `internal/mailer` was written to make impossible.
	fallbackMail mailer.Mailer

	// authRegistry is the ONE registry every authenticated request resolves against. Exposed so the boot
	// path can point it at the durable identity store after the schema is up.
	authRegistry *auth.Registry

	// accountSystem is the P27 posture: which identity store is live, whether self-serve sign-up is on,
	// and what the boot seed did. Reported as a VALUE beside `secrets_source` rather than as a gate,
	// because a deployment with self-serve off is configured, not degraded. Nil means this deployment
	// mounts no account system, which is stated by omission rather than by inventing a status for it.
	accountSystem *tenancy.Posture

	// errorReporter is the P24 error-reporting boundary. Nil means absent, which is the default and the
	// correct state on every substrate except the platform's own hosted deployment.
	errorReporter erroreport.Reporter
	// probes are the dependent components aggregated into /readyz (P9 FR25, P19 topology FR).
	//
	// The moment a second process sits in the request path, readiness has to cover it or it is
	// measuring the wrong thing: a Go service that reports "ready" while the surface users actually
	// reach is dead is a LYING HEALTH SIGNAL, and 🔴 health-signal-surface is explicit that a UI
	// dashboard is never itself the health judgement. A slice, because P19 deploys more than one
	// dependent component (customer console, operator console, object store, queue, vector/graph
	// stores, the air-gapped secret gateway) and the deployment must aggregate EVERY component it
	// ships — a partial aggregation is the lying signal again, one degraded store below the fold.
	// Empty when a deployment ships no dependent components — saying so by omission beats inventing a
	// status for one that does not exist.
	probes []ComponentProbe

	// agentReadiness resolves the P30 analysis agent's state by DOING what an inference does — reading
	// the active definition, resolving the credential through the gateway's own secrets source, and
	// comparing the real meter against the real ceiling (task 9.1).
	//
	// 🔴 A FUNCTION rather than a value, because every one of those is a live fact. A struct filled in
	// at boot would report the credential that resolved at boot, which is the readiness signal that
	// cannot go red — the exact failure P19 Decision 9 records for `components.postgres`.
	agentReadiness func(context.Context) herosagent.Readiness

	// p10 is the Postgres-backed prompt-authoring write surface (publish + timeline/diff/impact read
	// models), mounted by MountPromptRegistry when available. The platform API's first WRITE surface.
	promptRegistry PromptStore

	// p10matrix is the P10 studio MATRIX surface (node × model grid: models/nodes/run/bind), mounted by
	// MountStudioMatrix when available.
	studioMatrix StudioMatrix

	// p11 is the P11 run-linking ingest surface (POST /api/v1/run-links + GET /api/v1/whoami), mounted by
	// MountRunLinking when available. It attributes a linked run to the authenticated tenant server-side and
	// lands its events in the existing P2.5 substrate. The platform API's authenticated ingest surface.
	runLinking LinkIngestSource
	// workflowIR is the OPT-IN structure store, mounted by MountWorkflowIR when a deployment accepts it.
	// Separate from runLinking on purpose: accepting a run and accepting a workflow's shape are two
	// different policy decisions, and one mount must not imply the other.
	workflowIR WorkflowIRSource
	// herosAgent is the P30 analysis agent, mounted by MountHerosAgent. A SIXTH separate decision: a
	// deployment can accept structure and run no agent, which is the default — Q2 makes `disabled` the
	// default placement, so an unmounted agent and a mounted one with nobody enabled behave the same
	// way from a customer's terminal, and both are correct.
	herosAgent HerosAgentSource
	// verdicts is the P5.5 verdict ingest, mounted by MountVerdictIngest. A fourth separate decision:
	// this one accepts a MEASUREMENT that decides whether a change may be recommended and delivered, so
	// a deployment that serves the recommendation surface read-only leaves it nil and answers 503.
	verdicts VerdictSink
	// transformReceipts is the P29 opt-in transform-receipt store, mounted by MountTransformReceipts. A
	// FIFTH separate decision, and separate for the same reason as the other four: a deployment can
	// accept a run's numbers and refuse to hold what a customer's change did to their own tree.
	transformReceipts TransformReceiptSource
	// workflowIndex and linkedRuns are the P29 §4 enumerations — "what does this organization have?".
	// Mounted separately from the ingests they read, because a deployment can accept structure and not
	// serve a catalog, and because the read side must not be able to acquire a write.
	workflowIndex WorkflowIRIndex
	linkedRuns    linkingest.Store
	// accountProvisioner creates a Free account at the FIRST AUTHENTICATED ACT (P29 §7.1). Before it,
	// only organizations the config seed made had one — so a self-serve sign-up linked a run and the
	// billing read model could not find the customer it was attributed to.
	accountProvisioner AccountProvisioner
	// proposalGen is the platform-side generator, mounted by MountProposalGeneration. Separate from
	// MountProposals for the reason every other pair here is separate: reading a recommendation surface
	// and running a pass that WRITES proposals are different things to enable.
	proposalGen ProposalGenerator
	// proposalCompile is the codemod, mounted by MountProposalCompile. Separate from proposalGen: one
	// reads metrics and a graph, the other extracts a source snapshot and re-parses a repository, and a
	// caller who only wants to see what was found should not pay for the second.
	proposalCompile ProposalCompiler
	// sourcePush is the customer-pushed SOURCE snapshot store, mounted by MountSourcePush. A third
	// separate policy decision, and the largest one: accepting a run's numbers, accepting a workflow's
	// allowlisted shape, and accepting the customer's source are three different things to agree to, so
	// no mount implies another. A deployment that will not hold customer source leaves this nil.
	sourcePush SourcePushStore
	// sourceDiscovery runs discovery over a pushed snapshot. Independently nillable from sourcePush:
	// they need different collaborators and fail for different reasons.
	sourceDiscovery SourceDiscovery

	// p12 is the P12 forge-delivery surface (console delivery read model + CI-mediated fetch/report),
	// mounted by MountForgeDelivery when available. It holds no forge credential.
	forgeDelivery ForgeDeliverySource

	// p13authoring is the P13 13c user-authoring surface (preflight / submit / revert / history),
	// mounted by MountAuthoring when available. A deployment without it behaves exactly as it did
	// before 13c — which is what makes the wave independently revertible.
	authoring AuthoringSource
}

// ComponentProbe reports whether a dependent component is reachable.
//
// A one-method interface rather than an import of the console's client: /readyz needs the ANSWER, not
// the type. It also keeps the aggregation testable without a network — the failure case that matters
// most here is the one where the component is unreachable, and that must be exercisable.
type ComponentProbe interface {
	// Name is the component's name as it appears on /readyz. Machine-readable, so a monitor can alert
	// on the specific component rather than on "not ready".
	Name() string
	// Probe returns nil when the component is reachable, or the reason it is not.
	Probe(ctx context.Context) error
}

// SetConsoleProbe wires the customer console into the readiness signal (P9 FR25). It is a thin alias
// for AddComponentProbe kept because P9's launch path and tests already call it by this name.
func (s *Server) SetConsoleProbe(p ComponentProbe) { s.AddComponentProbe(p) }

// AddComponentProbe registers one dependent component in the readiness aggregation (P19). Order of
// registration is the order components are reported; a nil probe is ignored so a launch path can wire
// a component conditionally without a guard at every call site.
func (s *Server) AddComponentProbe(p ComponentProbe) {
	if p == nil {
		return
	}
	s.probes = append(s.probes, p)
}

// HTTPComponentProbe probes a component's health endpoint over HTTP.
//
// The timeout is explicit and short. A readiness probe that can hang is not a readiness probe: the
// orchestrator's own probe deadline would fire first, and the answer an operator gets would be
// "timeout" rather than "the console is unreachable" — the same outcome with none of the information.
type HTTPComponentProbe struct {
	ComponentName string
	URL           string
	Client        *http.Client
}

// DBComponentProbe reports a database's reachability under its own name.
//
// It exists so the platform database is probed rather than described. The alternative this replaces was
// naming the local ledger's ping "postgres" from an environment variable, which produced a component
// that reported ready while the database it named was down — a signal structurally incapable of failing.
// A probe is a connection or it is decoration.
type DBComponentProbe struct {
	ComponentName string
	DB            *sql.DB
}

// NewDBComponentProbe builds a probe over an open pool.
func NewDBComponentProbe(name string, db *sql.DB) *DBComponentProbe {
	return &DBComponentProbe{ComponentName: name, DB: db}
}

// Name reports the component's name.
func (p *DBComponentProbe) Name() string { return p.ComponentName }

// Probe pings the pool. Reachability, not traffic freshness, and it does not depend on the traffic
// /readyz gates — so readiness cannot deadlock on the very requests it admits (task 3.3).
func (p *DBComponentProbe) Probe(ctx context.Context) error {
	if p.DB == nil {
		return fmt.Errorf("no connection pool")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := p.DB.PingContext(ctx); err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	return nil
}

// NewHTTPComponentProbe builds a probe with a bounded client.
func NewHTTPComponentProbe(name, url string) *HTTPComponentProbe {
	return &HTTPComponentProbe{
		ComponentName: name,
		URL:           url,
		Client:        &http.Client{Timeout: 2 * time.Second},
	}
}

// Name reports the component's name.
func (p *HTTPComponentProbe) Name() string { return p.ComponentName }

// Probe performs one GET against the component's health endpoint.
func (p *HTTPComponentProbe) Probe(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return err
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}
	return nil
}

// IdentityStatus is what /readyz reports about the customer identity provider.
//
// Kind, issuer, reachability — and nothing else. Never a client id, never a redirect allowlist, never
// a secret's logical name: a readiness endpoint is public by necessity (a probe behind authentication
// cannot be probed by the thing that most needs to probe it), so everything it says is said to
// everybody.
type IdentityStatus struct {
	Kind      string `json:"kind"`
	Issuer    string `json:"issuer"`
	Reachable bool   `json:"reachable"`
	// Detail is why it is unreachable, when it is. Absent when it is fine.
	Detail string `json:"detail,omitempty"`
}

// IdentityProbe reports the customer identity provider's reachability.
//
// # The two properties this signal must have, and why they are stated here
//
// It measures REACHABILITY — can the IdP's discovery/JWKS or metadata be fetched and validated — and
// not traffic freshness. A console with no sign-ins all night is not unhealthy; a console whose IdP
// died an hour ago is, and a signal derived from sign-in volume gets both backwards.
//
// And it does not depend on the traffic it gates. The probe is an HTTP GET against a health endpoint,
// so readiness can never deadlock on the very logins it is meant to admit.
type IdentityProbe interface {
	Identity(ctx context.Context) IdentityStatus
}

// SetIdentityProbe wires the customer identity provider into the readiness signal (P22 task 7.1).
func (s *Server) SetIdentityProbe(p IdentityProbe) { s.identity = p }

// AdminIdentityDescriber names the live operator IdP for /readyz. Satisfied by
// *adminidentity.Authenticator, whose Describe reports the DOOR — kind, issuer, test mode — and never
// a key, a secret id, or an assertion.
type AdminIdentityDescriber interface {
	Describe() adminidentity.ProviderInfo
}

// SetAdminIdentity records the live operator IdP so /readyz can report it (P22 task 6.4).
//
// # Why the operator IdP appears on the PLATFORM's readiness endpoint as well as the admin surface's
//
// The admin surface already reports it at `/admin/api/readyz`, and that endpoint is behind the
// operator console's own origin and its platform credential. The question "is this deployment pointed
// at the real operator IdP, or still at the test-mode fixture" is asked by whoever is looking at the
// deployment, not by whoever is already inside the operator console — and 🔴 health-signal-surface
// wants that answer readable from the box in question, by a monitor, without a credential.
func (s *Server) SetAdminIdentity(d AdminIdentityDescriber) { s.adminIdentity = d }

// SetAccountSystem records the P27 posture for /readyz (task 3.5).
//
// The seed result is passed in rather than recomputed: the endpoint must report what the seed ACTUALLY
// did on this boot, and a second run to find out would both lie and write.
func (s *Server) SetAccountSystem(p tenancy.Posture) { s.accountSystem = &p }

// AuthRegistry returns the registry this server authenticates against, so the boot path can point it at
// the durable identity store. It is never nil.
func (s *Server) AuthRegistry() *auth.Registry { return s.authRegistry }

// HTTPIdentityProbe reads the customer console's health endpoint and extracts its identity block.
//
// # Why the platform asks the console rather than resolving the IdP itself
//
// The customer identity provider is the CONSOLE's dependency — the console holds the seam, performs
// the flow and verifies the assertion (ADR-008). A Go service that independently probed the customer's
// IdP would be measuring a dependency it does not have, and would report healthy or degraded for
// reasons the console does not share. Asking the component that actually depends on it keeps one
// answer rather than two that can disagree.
type HTTPIdentityProbe struct {
	URL    string
	Client *http.Client
}

// NewHTTPIdentityProbe builds a probe with a bounded client. A readiness probe that can hang is not a
// readiness probe.
func NewHTTPIdentityProbe(url string) *HTTPIdentityProbe {
	return &HTTPIdentityProbe{URL: url, Client: &http.Client{Timeout: 2 * time.Second}}
}

// Identity performs one GET and reads the console's `identity_provider` block.
func (p *HTTPIdentityProbe) Identity(ctx context.Context) IdentityStatus {
	unreachable := func(detail string) IdentityStatus {
		// The console being unreachable is reported as the IDENTITY provider being unreachable, and
		// that is correct rather than sloppy: from this endpoint's point of view the question is "can a
		// customer sign in", and the answer is no either way. The `customer_console` component names
		// the other half, so an operator reading both learns which layer is down.
		return IdentityStatus{Reachable: false, Detail: detail}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return unreachable(err.Error())
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return unreachable("unreachable: " + err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return unreachable(fmt.Sprintf("health endpoint returned %d", resp.StatusCode))
	}
	var body struct {
		Identity IdentityStatus `json:"identity_provider"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return unreachable("health endpoint did not report an identity provider")
	}
	if body.Identity.Kind == "" {
		// A console too old to report the block. Named as such rather than assumed healthy: an
		// unreported signal is not a green one.
		return unreachable("health endpoint reported no identity provider kind")
	}
	return body.Identity
}

// BillingRolloutDescriber reports the P7 rollout gates. A one-method interface rather than an import
// of the billing package: /readyz needs the words, not the type.
type BillingRolloutDescriber interface {
	Describe() map[string]string
}

// SetBillingRollout records the live P7 rollout so /readyz can report which gates are open.
func (s *Server) SetBillingRollout(d BillingRolloutDescriber) { s.billing = d }

// BillingCapabilityDescriber names the live billing provider and its credential source. Satisfied by
// *billing.Service.Describe; a one-method interface rather than an import, for the same reason
// BillingRolloutDescriber is one: /readyz needs the words, not the type.
type BillingCapabilityDescriber interface {
	Describe() map[string]string
}

// SetBillingCapability records the live billing capability so /readyz can report which processor is
// wired and which source its credentials resolve from (P21 task 3.1).
//
// It reports the source's IDENTITY, never a credential and never a credential's id — the same rule
// SetSecretsSource follows, and the reason billing.Service.Describe has no field that could carry one.
func (s *Server) SetBillingCapability(d BillingCapabilityDescriber) { s.billingCapability = d }

// SetSecretsSource records which secrets source is live so /readyz can report it.
//
// This exists because health-signal-surface is not satisfied by a log line at boot: "the deployment
// is on AWS Secrets Manager" is only useful if it can be checked NOW, by a monitor, on the box that
// is actually misbehaving — and a log line scrolled past three restarts ago cannot be checked at all.
func (s *Server) SetSecretsSource(src providergateway.Secrets) { s.secrets = src }

// New builds the minimal agentd HTTP surface. Health/readiness are public;
// future /api/* routes are gated by API-key auth when auth_mode=required.
func New(db *sql.DB, cfg config.Config) *Server {
	s := &Server{DB: db, Cfg: cfg, Mux: http.NewServeMux()}
	s.Mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.Mux.HandleFunc("GET /readyz", s.handleReadyz)
	// P13 13d — the total coverage read model. Registered beside health because, like health, it is a
	// property of this BUILD rather than of a tenant: it takes no tenant, no plan and no role, which is
	// what makes "coverage is identical on every plan" structural instead of a policy.
	s.Mux.HandleFunc("GET /api/v1/coverage", s.handleCoverage)
	s.Mux.HandleFunc("GET /api/v1/change-delivery", s.handleDelivery)
	// P17 20c — the memory-authoring read model, registered here for the same reason: the strategy
	// vocabulary and the applicability boundary are properties of this BUILD, not of a tenant, so no
	// plan or role can move them. It is a READ only; a memory change is authored through the existing
	// /api/v1/authoring routes, because there is one spine and two origins.
	s.Mux.HandleFunc("GET /api/v1/memory", s.handleMemory)
	// P20 — the install/distribution read model, registered here for the same reason: the supported-target
	// matrix, the install channels and the trust posture are properties of the RELEASE, not of a tenant, so no
	// entitlement can move a row. It takes no tenant, no plan and no role.
	s.Mux.HandleFunc("GET /api/v1/install", s.handleInstall)

	var h http.Handler = s.Mux
	// The registry is KEPT, not just used. P27's boot path points it at the durable identity store with
	// `AuthRegistry().WithSource(...)` — which mutates this same object, so the composed handler needs no
	// rebuilding and there is exactly one registry a request can be resolved against. Two registries is
	// how a deployment ends up authenticating against the configuration file on one path and the
	// database on another.
	s.authRegistry = auth.NewRegistry(cfg)
	if cfg.AuthMode == "required" {
		h = auth.Compose(s.authRegistry, h) // gates /api/*; health paths stay open
	}
	// OUTERMOST, so a panic in the auth layer is reported too and every response — including a refusal
	// — carries the trace id a customer can quote back.
	h = s.traceAndReport(h)
	s.Handler = h
	return s
}

// handleHealthz reports liveness — the process is up and serving.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleReadyz reports readiness — dependencies (the SQLite ledger) are reachable — and which
// secrets source is live.
//
// The secrets source is reported HERE rather than from a new endpoint on purpose (careful-api-
// creation: a new endpoint is new surface area, and this is one field on a document that already
// answers "what state is this process in"). It reports the source's IDENTITY, not its health: probing
// the secrets manager on every readiness check would make an AWS latency spike look like an agentd
// outage, and would fetch a credential to prove we can fetch a credential.
//
// It is absent rather than "unknown" when unset — a deployment that never wired a source has no
// secrets source, and saying so by omission beats inventing a status for it.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"status": "ready"}
	components := map[string]any{}
	degraded := make([]string, 0, 1)

	// The local ledger is the first aggregated component, NAMED, not a bare "db_unavailable" status on
	// the whole document.
	//
	// 🔴 The name is FIXED, and that is the point. It used to come from HEROS_DATASTORE_NAME, which the
	// deploy manifests set to "postgres" — so this ping of the SQLite ledger was reported as
	// `components.postgres: ready` on a process that had never opened a Postgres connection. That signal
	// could not go red no matter what happened to Postgres, which is the one thing a readiness signal may
	// never be. A component's name on /readyz now always identifies the dependency actually probed
	// (P19 Decision 9); the platform database reports separately, under `postgres`, from its own probe.
	if s.DB != nil {
		const name = "ledger"
		if err := s.DB.PingContext(r.Context()); err != nil {
			components[name] = map[string]any{"status": "degraded", "detail": "ping failed: " + err.Error()}
			degraded = append(degraded, name)
		} else {
			components[name] = map[string]any{"status": "ready"}
		}
	}
	if s.secrets != nil {
		body["secrets_source"] = s.secrets.Describe()
	}
	if s.adminIdentity != nil {
		// The operator IdP, named on the platform's own readiness surface. Absent rather than "unknown"
		// when this deployment ships no operator console.
		body["admin_idp"] = s.adminIdentity.Describe()
	}
	if s.identity != nil {
		// P22 task 7.1. An unreachable customer IdP makes the whole signal not-ready and NAMES itself,
		// because "not ready" without a subject sends an operator to read three dashboards to learn
		// what this response already knew.
		status := s.identity.Identity(r.Context())
		entry := map[string]any{"status": "ready", "kind": status.Kind, "issuer": status.Issuer, "reachable": status.Reachable}
		if !status.Reachable {
			entry["status"] = "not_ready"
			if status.Detail != "" {
				entry["detail"] = status.Detail
			}
			degraded = append(degraded, "identity_provider")
		}
		components["identity_provider"] = entry
	}
	if s.billing != nil {
		// Absent rather than "unknown" when unset: a deployment that wired no billing rollout has none,
		// and saying so by omission beats inventing a status for it.
		body["billing_rollout"] = s.billing.Describe()
	}
	if s.accountSystem != nil {
		// P27 task 3.5. Which identity store is live, whether self-serve sign-up is on, and what the
		// boot seed did — as values, so nobody has to read the process environment during an incident
		// to learn whether this deployment can create an organization.
		body["account_system"] = s.accountSystem.Describe()
	}
	// The error-reporting integration's three-state entry. Reported at the top level beside
	// `secrets_source` rather than inside `components`, because it is deliberately NOT a gate — see
	// errorReporterState.
	body["error_reporting"] = s.errorReporterState()
	if s.agentReadiness != nil {
		// P30 task 9.1. Top level beside `secrets_source`, and 🚫 NOT in `components` — every entry in
		// that map is a GATE, and none of the agent's states may take a deployment down.
		//
		// `disabled` is the default (Q2), so gating on it would page somebody about the configuration
		// every deployment ships with. `capped` is a ceiling working as intended. Even
		// `credential_unresolved` is contained: HEROS is optional, every other surface is rule-derived,
		// and taking a platform down because an optional subsystem cannot reach its vendor is a bigger
		// outage than the one being reported.
		body["heros_agent"] = s.agentReadiness(r.Context())
	}
	if s.billingCapability != nil {
		// Which processor, and where its credentials come from. Absent rather than "unknown" when
		// unset, exactly like the two signals above.
		body["billing_provider"] = s.billingCapability.Describe()
	}

	// Component aggregation (P9 FR25, P19 topology). The service's OWN health is not the deployment's
	// health once a second process sits in the request path, so a degraded component makes the whole
	// signal not-ready — and it is NAMED, because "not ready" without a subject sends an operator to
	// read three dashboards to learn what this response already knew. Every wired component is probed;
	// a partial aggregation is the same lying signal, just one store below the fold.
	//
	// Probes read REACHABILITY, not traffic freshness, and none of them depends on the traffic /readyz
	// gates — an HTTP GET against a health endpoint, a datastore ping — so readiness can never deadlock
	// on the very traffic it is meant to admit (task 3.3).
	for _, p := range s.probes {
		if err := p.Probe(r.Context()); err != nil {
			components[p.Name()] = map[string]any{"status": "degraded", "detail": err.Error()}
			degraded = append(degraded, p.Name())
		} else {
			components[p.Name()] = map[string]any{"status": "ready"}
		}
	}
	if len(components) > 0 {
		body["components"] = components
	}
	if len(degraded) > 0 {
		body["status"] = "degraded"
		body["degraded_components"] = degraded
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}

	writeJSON(w, http.StatusOK, body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
