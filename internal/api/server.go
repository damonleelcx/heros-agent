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
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Server is the agentd HTTP API.
type Server struct {
	// DB is the SQLite dev ledger (auth keys, memory). NOT P2's store — see p2.go.
	DB      *sql.DB
	Cfg     config.Config
	Mux     *http.ServeMux
	Handler http.Handler

	// p2 is the Postgres-backed P2 read surface, mounted by MountConfigRuntime when available.
	configRuntime ConfigRuntimeStores

	// monitor is the P2.5 live run-monitoring read model, mounted by MountMonitor when available.
	monitor MonitorSource

	// p35 is the P3.5 pattern-classifier read model, mounted by MountPatternGraph when available.
	patternGraph PatternSource

	// p4 is the P4 eval-board read model, mounted by MountEvalBoard when available.
	evalBoard BoardSource

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
	if cfg.AuthMode == "required" {
		reg := auth.NewRegistry(cfg)
		h = auth.Compose(reg, h) // gates /api/*; health paths stay open
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
	// The error-reporting integration's three-state entry. Reported at the top level beside
	// `secrets_source` rather than inside `components`, because it is deliberately NOT a gate — see
	// errorReporterState.
	body["error_reporting"] = s.errorReporterState()
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
