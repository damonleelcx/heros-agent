// Command herosdemolink demonstrates run linking working END TO END against the REAL ingest logic.
//
// The production endpoint https://heros-agent.space is not running in a dev session, so this stands a
// local server in its place — serving that exact host via a RoundTripper that routes heros-agent.space
// to the local listener. Everything else is real: the real transport client, the real
// linkingest.Ingester, the real metering substrate and SUM derivation, and the real idempotency +
// coverage read models. It exists so "linking works" can be SEEN, not just asserted in a test.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/clilink"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/runlink"
)

func main() {
	repo := flag.String("repo", ".", "repo whose local run store to read")
	run := flag.String("run", "", "run id to link (must exist under <repo>/.heros/runs)")
	flag.Parse()

	// The real ingest stack: substrate + link store + ingester.
	sub := metering.NewMemCostEvents()
	links := linkingest.NewMemStore()
	ing := linkingest.New(sub, links, func(tenant, runID string) string {
		return runlink.PlatformBaseURL + "/app/runs/" + runID
	})

	// A server that speaks the platform's two P11 endpoints, backed by the real ingester.
	const identity = "acme-corp"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/whoami":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"identity": identity})
		case "/api/v1/run-links":
			var p runlink.Payload
			_ = json.NewDecoder(r.Body).Decode(&p)
			res, err := ing.Ingest(identity, p) // ← the real ingest: allowlist → P2.5 substrate
			if err != nil {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			code := 201
			if res.AlreadyLinked {
				code = 409
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted": res.Accepted, "already_linked": res.AlreadyLinked,
				"run_url": res.RunURL, "contract_version": res.ContractVersion,
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	// Route heros-agent.space → the local server, keeping the pin intact (the client still targets
	// https://heros-agent.space; only the transport is redirected — exactly how the test suite does it).
	rt := hostRewrite{target: target, base: http.DefaultTransport}
	cmds := clilink.Commands{RT: rt, Timeout: 10 * time.Second}

	// Credentials land in a temp config dir so this demo is self-contained.
	tmp, _ := os.MkdirTemp("", "herosdemolink-")
	_ = os.Setenv("HEROS_CONFIG_DIR", tmp)

	streams := cli.Streams{Out: os.Stdout, Err: os.Stderr}
	cfg := func(kv map[string]string) cli.Config {
		r := cli.NewResolver(map[string]string{"repo": ".", "run": "", "token": "", "dry-run": "false"})
		r.SetFlag("repo", *repo)
		for k, v := range kv {
			r.SetFlag(k, v)
		}
		return r.Resolve()
	}

	fmt.Fprintln(os.Stderr, "── login ──────────────────────────────────────────────")
	if err := cmds.Login(cfg(map[string]string{"token": "demo-token-not-a-real-secret"}), streams); err != nil {
		fmt.Fprintln(os.Stderr, "login error:", err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "\n── link (first time → transmitted) ────────────────────")
	if err := cmds.Link(cfg(map[string]string{"run": *run}), streams); err != nil {
		fmt.Fprintln(os.Stderr, "link error:", err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "\n── link again (idempotent → counted once) ─────────────")
	if err := cmds.Link(cfg(map[string]string{"run": *run}), streams); err != nil {
		fmt.Fprintln(os.Stderr, "link error:", err)
		os.Exit(2)
	}

	// Show the server-side truth: SUM from the substrate + coverage.
	period := metering.MonthPeriod(time.Now())
	sum, _ := metering.DeriveSUM(sub, identity, period)
	cov, covErr := links.Coverage(identity)
	if covErr != nil {
		fmt.Fprintf(os.Stderr, "link coverage: UNREADABLE — %v\n", covErr)
	}
	fmt.Fprintln(os.Stderr, "\n── server-side result (real substrate) ────────────────")
	fmt.Fprintf(os.Stderr, "SUM for %s this period: $%.4f (from %d distinct cost event(s))\n", identity, sum.Quantity, sum.EventCount)
	fmt.Fprintf(os.Stderr, "link coverage: %d/%d runs · complete=%v · known=%v\n", cov.RunsLinked, cov.RunsReported, cov.Complete, cov.Known)
}

type hostRewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (h hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "heros-agent.space" {
		req.URL.Scheme = h.target.Scheme
		req.URL.Host = h.target.Host
	}
	return h.base.RoundTrip(req)
}
