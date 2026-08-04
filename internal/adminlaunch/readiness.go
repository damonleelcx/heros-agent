package adminlaunch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
)

// readiness.go reads the platform's OWN readiness surface for the oversight page.
//
// # Why this exists and why it reads /readyz specifically
//
// The oversight page's own text says the reporting-health answer is "read from the platform's own
// readiness surface", and `IntegrationRow.Source` carries a 🔴 comment saying it must be the platform's
// own surface "never a third party's dashboard — which is the least available part of the system during
// an incident". No implementation existed, so the page reported that no readiness surface was wired —
// correctly, and unhelpfully, because the surface it wanted has been served on `/readyz` all along.
//
// # Why an unreachable probe is DEGRADED and not ABSENT
//
// `absent` on this page means "this deployment configures no such integration", which is a legitimate
// and silent state. "We could not ask" is a different answer, and collapsing the two would tell an
// operator mid-incident that reporting is deliberately off when in fact the platform is unreachable.
// `IntegrationRow.FailureClass` is required for exactly that distinction, so it is always set here.

// readinessSource reads agentd's own /readyz.
type readinessSource struct {
	url    string
	client *http.Client
}

// newReadinessSource points at the local readiness endpoint.
//
// Loopback, not the Service DNS name: this runs INSIDE the agentd process, so the surface it is asking
// about is its own. Going out through the cluster network to ask itself would make an answer about
// reporting health depend on DNS and the CNI, which is precisely the machinery most likely to be broken
// when somebody opens this page.
func newReadinessSource(listenAddr string) *readinessSource {
	return &readinessSource{
		url:    "http://" + loopbackOf(listenAddr) + "/readyz",
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

// Integrations implements adminops.ReadinessSource.
func (r *readinessSource) Integrations() []adminops.IntegrationRow {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return []adminops.IntegrationRow{r.unreadable(err)}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return []adminops.IntegrationRow{r.unreadable(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	// 🔴 A non-2xx is NOT a failure to read. `/readyz` answers 503 when a component is unhealthy, and
	// its body is exactly the picture this page wants — refusing to parse it would blank the page at the
	// one moment it matters. Only a transport error or an unparseable body is "we could not ask".
	var body struct {
		ErrorReporting struct {
			State  string `json:"state"`
			Detail string `json:"detail"`
		} `json:"error_reporting"`
		SecretsSource struct {
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		} `json:"secrets_source"`
		Components map[string]struct {
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"components"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return []adminops.IntegrationRow{r.unreadable(err)}
	}

	rows := []adminops.IntegrationRow{}

	// Error reporting is the P24 three-state entry, carried through verbatim. `absent` here is a
	// DECISION on every substrate except the platform's own hosted deployment, so it is reported as
	// absent rather than as a degradation.
	rows = append(rows, adminops.IntegrationRow{
		Name:         "error_reporting",
		State:        integrationState(body.ErrorReporting.State),
		FailureClass: failureClass(body.ErrorReporting.State, body.ErrorReporting.Detail),
		Source:       r.url,
	})

	// The secrets source is an integration in the sense this page means: configured, reachable, or not.
	// Its Describe names the DOOR and never a credential, which is what makes it safe to surface here.
	if body.SecretsSource.Kind != "" {
		rows = append(rows, adminops.IntegrationRow{
			Name: "secrets_source (" + body.SecretsSource.Kind + ")",
			// A source that answered at all is configured; its REACHABILITY appears as its own component
			// below when the deployment wires a health URL for it.
			State:  adminops.IntegrationConfigured,
			Source: r.url,
		})
	}

	// Every aggregated component, by the name /readyz gives it — so a monitor and this page name the
	// same thing, and an operator reading "admin_console degraded" here can grep for it there.
	for name, c := range body.Components {
		state := adminops.IntegrationConfigured
		class := ""
		if c.Status != "ok" && c.Status != "ready" && c.Status != "" {
			state = adminops.IntegrationDegraded
			class = "configured but " + c.Status
			if c.Detail != "" {
				class += ": " + c.Detail
			}
		}
		rows = append(rows, adminops.IntegrationRow{
			Name: name, State: state, FailureClass: class, Source: r.url,
		})
	}
	return rows
}

// unreadable is the row for "we could not ask", which is never spelled `absent`.
func (r *readinessSource) unreadable(err error) adminops.IntegrationRow {
	return adminops.IntegrationRow{
		Name:         "platform_readiness",
		State:        adminops.IntegrationDegraded,
		FailureClass: fmt.Sprintf("the platform's own readiness surface could not be read (%v) — this is NOT 'nothing is configured'", err),
		Source:       r.url,
	}
}

func integrationState(state string) adminops.IntegrationState {
	switch state {
	case "absent", "":
		return adminops.IntegrationAbsent
	case "healthy", "ok", "configured":
		return adminops.IntegrationConfigured
	default:
		return adminops.IntegrationDegraded
	}
}

func failureClass(state, detail string) string {
	switch state {
	case "absent", "", "healthy", "ok", "configured":
		return ""
	default:
		if detail != "" {
			return "configured but " + state + ": " + detail
		}
		return "configured but " + state
	}
}

// Describe implements adminops.ReadinessSource.
func (r *readinessSource) Describe() string { return "the platform's own /readyz (" + r.url + ")" }

// loopbackOf turns a listen address into a loopback address on the same port.
//
// A container binds 0.0.0.0; reaching that literal address is not portable, so the host is replaced and
// only the port is kept — the same reduction `cmd/agentd -healthcheck` already performs, for the same
// reason.
func loopbackOf(listenAddr string) string {
	port := listenAddr
	for i := len(listenAddr) - 1; i >= 0; i-- {
		if listenAddr[i] == ':' {
			port = listenAddr[i+1:]
			break
		}
	}
	if port == "" || port == listenAddr {
		port = "4321"
	}
	return "127.0.0.1:" + port
}
