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
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Server is the agentd HTTP API.
type Server struct {
	// DB is the SQLite dev ledger (auth keys, memory). NOT P2's store — see p2.go.
	DB      *sql.DB
	Cfg     config.Config
	Mux     *http.ServeMux
	Handler http.Handler

	// p2 is the Postgres-backed P2 read surface, mounted by MountP2 when available.
	p2 P2Stores

	// monitor is the P2.5 live run-monitoring read model, mounted by MountMonitor when available.
	monitor MonitorSource

	// p35 is the P3.5 pattern-classifier read model, mounted by MountP35 when available.
	p35 PatternSource

	// p4 is the P4 eval-board read model, mounted by MountP4 when available.
	p4 BoardSource

	// p45 is the P4.5 read-only scorecard read model, mounted by MountP45 when available.
	p45 ScorecardSource

	// p5 is the P5 interactive-graph-editor read+validate model, mounted by MountP5 when available.
	p5 P5Source

	// p55 is the P5.5 ranked-recommendation + verification read model, mounted by MountP55.
	p55 P55Source

	// p6 is the P6 autonomous-optimizer governance surface (live monitor + grant/stop/rollback),
	// mounted by MountP6 when available.
	p6 P6Source

	// p7 is the P7 billing/usage read model (SUM, plan + entitlements, invoice breakdown, verified
	// gainshare evidence), mounted by MountP7 when available.
	p7 P7Source

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

	// secrets is the live provider-credential source, reported by /readyz.
	//
	// The SOURCE, never a credential: this holds the thing that can produce secrets precisely so the
	// endpoint can name it without anyone re-deriving it from configuration and getting a different
	// answer than the gateway did.
	secrets providergateway.Secrets

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
	// models), mounted by MountP10 when available. The platform API's first WRITE surface.
	p10 P10Store

	// p10matrix is the P10 studio MATRIX surface (node × model grid: models/nodes/run/bind), mounted by
	// MountP10Matrix when available.
	p10matrix P10Matrix

	// p11 is the P11 run-linking ingest surface (POST /api/p11/link + GET /api/p11/whoami), mounted by
	// MountP11 when available. It attributes a linked run to the authenticated tenant server-side and
	// lands its events in the existing P2.5 substrate. The platform API's authenticated ingest surface.
	p11 LinkIngestSource

	// p12 is the P12 forge-delivery surface (console delivery read model + CI-mediated fetch/report),
	// mounted by MountP12 when available. It holds no forge credential.
	p12 P12Source
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

	var h http.Handler = s.Mux
	if cfg.AuthMode == "required" {
		reg := auth.NewRegistry(cfg)
		h = auth.Compose(reg, h) // gates /api/*; health paths stay open
	}
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

	// The primary datastore is the first aggregated component, NAMED, not a bare "db_unavailable"
	// status on the whole document. In the platform deployment this is Postgres (eval + lineage); the
	// name comes from HEROS_DATASTORE_NAME so /readyz names the store the operator actually runs
	// ("postgres") rather than a generic word, and defaults to "datastore" for the SQLite single-binary.
	if s.DB != nil {
		name := strings.TrimSpace(os.Getenv("HEROS_DATASTORE_NAME"))
		if name == "" {
			name = "datastore"
		}
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
	if s.billing != nil {
		// Absent rather than "unknown" when unset: a deployment that wired no billing rollout has none,
		// and saying so by omission beats inventing a status for it.
		body["billing_rollout"] = s.billing.Describe()
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
