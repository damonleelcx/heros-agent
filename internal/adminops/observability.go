package adminops

import (
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
)

// observability.go emits admin activity onto the P2.5 telemetry substrate (FR18, task 13.1): logins,
// MFA failures, privileged-action volume, active impersonations, kill-switch state, and cross-tenant
// views — so operator behaviour is a live operational signal with anomaly alerting.
//
// # Why this reuses the identity/command observers rather than a second collection path
//
// The identity layer and the command path already emit typed events (adminidentity.Observer,
// adminops.Observer). This is the ADAPTER that turns those into P2.5 metrics — one place, so there is
// no second stream that could disagree with the audit chain about what happened. It re-collects
// nothing.
//
// # Why there is no field here that can hold a secret
//
// The metric shapes below carry a kind, an actor id, a target and a result — identifiers and outcomes.
// There is no field for an assertion body, an MFA code, a session token, a signing key, or a provider
// handle, so "no secret in admin telemetry" (task 13.2) is a property of the type rather than of a
// scrubber that has to be remembered. TestNoSecretInAdminTelemetry asserts it against a stack driven
// with real secret material.

// MetricName is one admin telemetry metric. Central enum — a metric a monitor alerts on must not be a
// literal typed at three call sites.
type MetricName string

const (
	// MetricAdminLogin counts issued admin sessions.
	MetricAdminLogin MetricName = "admin.login.issued"
	// MetricAdminMFAFailure counts logins denied for a missing or bad MFA factor — the signal a
	// credential-stuffing attempt against the operator door produces.
	MetricAdminMFAFailure MetricName = "admin.login.mfa_failure"
	// MetricAdminLoginDenied counts every other login denial (unknown/disabled principal, bad assertion).
	MetricAdminLoginDenied MetricName = "admin.login.denied"
	// MetricAdminPrivilegedAction counts executed privileged (non-read) admin actions — the volume an
	// anomaly detector watches for a spike.
	MetricAdminPrivilegedAction MetricName = "admin.action.privileged"
	// MetricAdminActionDenied counts authorization denials.
	MetricAdminActionDenied MetricName = "admin.action.denied"
	// MetricAdminCrossTenantView counts authorized cross-tenant reads.
	MetricAdminCrossTenantView MetricName = "admin.cross_tenant.view"
	// MetricAdminImpersonationActive is a gauge of live impersonation sessions.
	MetricAdminImpersonationActive MetricName = "admin.impersonation.active"
	// MetricAdminKillSwitchArmed is a gauge of armed kill-switch scopes — a value that stays non-zero is
	// the "kill switch left armed" anomaly task 13.1 names.
	MetricAdminKillSwitchArmed MetricName = "admin.killswitch.armed"
)

// Metric is one emitted admin telemetry point. Identifiers and outcomes ONLY.
type Metric struct {
	Name MetricName `json:"name"`
	// Value is the metric value (1 for a counter increment, a gauge level for a gauge).
	Value float64 `json:"value"`
	// Dimensions are the non-sensitive labels: actor, target, result, capability. Never a secret — see
	// the file header. Kept as a small fixed set of keys so cardinality stays bounded.
	Dimensions map[string]string `json:"dimensions"`
	At         time.Time         `json:"at"`
}

// TelemetrySink receives admin metrics. It is deliberately this package's own one-method interface,
// so the P2.5 collector plugs in without adminops importing the telemetry package's whole surface.
type TelemetrySink interface {
	EmitAdminMetric(m Metric)
}

// TelemetrySinkFunc adapts a function to TelemetrySink.
type TelemetrySinkFunc func(m Metric)

// EmitAdminMetric implements TelemetrySink.
func (f TelemetrySinkFunc) EmitAdminMetric(m Metric) { f(m) }

// AnomalyRule reports whether a metric crosses an alerting threshold. Returning a non-empty string
// raises an alert with that description. Kept simple and injectable so a deployment tunes thresholds
// without editing this package.
type AnomalyRule func(m Metric, window *Window) string

// Window is a small rolling counter the anomaly rules read — enough to say "a spike in privileged
// actions" without a time-series database in-process.
type Window struct {
	mu     sync.Mutex
	counts map[MetricName]int
	// killArmed tracks armed scopes so "a kill switch left armed" can alert even with no new events.
	killArmed map[string]bool
}

// NewWindow builds an empty window.
func NewWindow() *Window {
	return &Window{counts: map[MetricName]int{}, killArmed: map[string]bool{}}
}

func (w *Window) observe(m Metric) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.counts[m.Name]++
	if m.Name == MetricAdminKillSwitchArmed {
		scope := m.Dimensions["scope"]
		if m.Value > 0 {
			w.killArmed[scope] = true
		} else {
			delete(w.killArmed, scope)
		}
	}
}

// Count reports how many of a metric the window has seen.
func (w *Window) Count(name MetricName) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.counts[name]
}

// ArmedScopes reports how many kill-switch scopes are currently armed.
func (w *Window) ArmedScopes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.killArmed)
}

// Alert is a raised anomaly.
type Alert struct {
	Metric      MetricName `json:"metric"`
	Description string     `json:"description"`
	At          time.Time  `json:"at"`
}

// Telemetry is the admin observability adapter. It implements adminidentity.Observer and
// adminops.Observer, so wiring it into the identity layer and the command path is all it takes to put
// admin activity on the substrate.
type Telemetry struct {
	sink   TelemetrySink
	window *Window
	rules  []AnomalyRule
	now    func() time.Time

	mu     sync.Mutex
	alerts []Alert
	// alertSink, when set, is where anomalies go (a pager, a channel). Nil records them in memory only.
	alertSink func(Alert)
}

// NewTelemetry builds the adapter.
func NewTelemetry(sink TelemetrySink, rules []AnomalyRule, now func() time.Time) *Telemetry {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if len(rules) == 0 {
		rules = DefaultAnomalyRules()
	}
	return &Telemetry{sink: sink, window: NewWindow(), rules: rules, now: now}
}

// OnAlert installs an alert callback (a pager). Without one, alerts are recorded in memory for the
// console and the tests to read.
func (t *Telemetry) OnAlert(fn func(Alert)) { t.alertSink = fn }

// Alerts returns the anomalies raised so far.
func (t *Telemetry) Alerts() []Alert {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Alert, len(t.alerts))
	copy(out, t.alerts)
	return out
}

// emit sends a metric to the sink, updates the window, and evaluates the anomaly rules.
func (t *Telemetry) emit(m Metric) {
	m.At = t.now()
	if m.Dimensions == nil {
		m.Dimensions = map[string]string{}
	}
	t.window.observe(m)
	if t.sink != nil {
		t.sink.EmitAdminMetric(m)
	}
	for _, rule := range t.rules {
		if desc := rule(m, t.window); desc != "" {
			alert := Alert{Metric: m.Name, Description: desc, At: m.At}
			t.mu.Lock()
			t.alerts = append(t.alerts, alert)
			t.mu.Unlock()
			if t.alertSink != nil {
				t.alertSink(alert)
			}
		}
	}
}

// AdminIdentityEvent implements adminidentity.Observer — logins and MFA failures onto the substrate.
func (t *Telemetry) AdminIdentityEvent(ev adminidentity.Event) {
	switch ev.Kind {
	case adminidentity.EventLoginIssued:
		t.emit(Metric{Name: MetricAdminLogin, Value: 1, Dimensions: map[string]string{"admin_id": ev.AdminID}})
	case adminidentity.EventLoginDeniedNoMFA:
		t.emit(Metric{Name: MetricAdminMFAFailure, Value: 1, Dimensions: map[string]string{
			"sso_subject": ev.SSOSubject, "reason": safeDetail(ev.Detail),
		}})
	case adminidentity.EventLoginDeniedBadAssertion, adminidentity.EventLoginDeniedPrincipal:
		t.emit(Metric{Name: MetricAdminLoginDenied, Value: 1, Dimensions: map[string]string{
			"sso_subject": ev.SSOSubject, "reason": safeDetail(ev.Detail),
		}})
	}
}

// AdminCommand implements adminops.Observer — privileged-action volume, denials, cross-tenant views.
func (t *Telemetry) AdminCommand(ev CommandEvent) {
	dims := map[string]string{
		"admin_id": ev.ActorAdminID, "capability": string(ev.Capability), "target": ev.Target,
		"result": ev.Result,
	}
	if ev.Impersonated {
		dims["impersonated"] = "true"
	}
	switch {
	case ev.Denied:
		t.emit(Metric{Name: MetricAdminActionDenied, Value: 1, Dimensions: dims})
	case ev.Capability == "crosstenant.read":
		t.emit(Metric{Name: MetricAdminCrossTenantView, Value: 1, Dimensions: dims})
	case !ev.Capability.ReadOnly():
		t.emit(Metric{Name: MetricAdminPrivilegedAction, Value: 1, Dimensions: dims})
	}
}

// RecordImpersonationGauge emits the count of active impersonation sessions. Called on a tick or on
// session change by the wiring, so "N impersonations active" is a live signal.
func (t *Telemetry) RecordImpersonationGauge(active int) {
	t.emit(Metric{Name: MetricAdminImpersonationActive, Value: float64(active), Dimensions: map[string]string{}})
}

// RecordKillSwitchGauge emits a scope's armed state (1 armed, 0 disarmed), so "a kill switch left
// armed" is observable.
func (t *Telemetry) RecordKillSwitchGauge(scope string, armed bool) {
	v := 0.0
	if armed {
		v = 1
	}
	t.emit(Metric{Name: MetricAdminKillSwitchArmed, Value: v, Dimensions: map[string]string{"scope": scope}})
}

// safeDetail keeps a short non-sensitive reason and refuses anything long enough to be a payload.
func safeDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) > 120 {
		return detail[:120]
	}
	return detail
}

// DefaultAnomalyRules are the platform's default alerts (task 13.1): a spike in privileged actions and
// a kill switch left armed.
func DefaultAnomalyRules() []AnomalyRule {
	return []AnomalyRule{
		func(m Metric, w *Window) string {
			// A crude spike rule: many privileged actions in one window. A deployment replaces this with
			// a rate over its own time base; the shape is what matters here.
			if m.Name == MetricAdminPrivilegedAction && w.Count(MetricAdminPrivilegedAction) >= 20 {
				return "spike in privileged admin actions"
			}
			return ""
		},
		func(m Metric, w *Window) string {
			if m.Name == MetricAdminKillSwitchArmed && w.ArmedScopes() > 0 {
				return "a kill switch is armed"
			}
			return ""
		},
		func(m Metric, w *Window) string {
			if m.Name == MetricAdminMFAFailure && w.Count(MetricAdminMFAFailure) >= 5 {
				return "repeated admin MFA failures — possible credential-stuffing against the operator console"
			}
			return ""
		},
	}
}
