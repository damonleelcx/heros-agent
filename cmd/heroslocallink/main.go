// Command heroslocallink links a run from a local run store into a SELF-HOSTED deployment — the one
// `make deploy-up` stands up — using the real payload builder and the real transport client.
//
// # Why this exists
//
// `heros link` transmits to https://heros-agent.space and nowhere else. That pin is deliberate and it
// is enforced twice (runlink.IsLinkTarget, and transport.assertLinkTarget re-checking immediately
// before the request goes out), so a linked payload can never be redirected to a host the developer did
// not choose. Nothing overrides it: not a flag, not an environment variable, not a config key.
//
// 🔴 The consequence is that the platform `make deploy-up` brings up on 127.0.0.1 CANNOT BE LINKED TO by
// the shipped CLI, even though deploy/README.md lists P11 run linking as served on it. That is a real
// product gap and this binary does not close it — closing it means deciding whether a self-hosted
// endpoint may be named, which is a boundary decision and not a deployment detail.
//
// What this does instead is exactly what cmd/herosdemolink already does for the in-memory ingester, one
// step further: it redirects the DIAL, not the URL. The client still builds
// https://heros-agent.space/api/v1/run-links, still runs assertLinkTarget against it, and still refuses
// anything else — the RoundTripper below simply carries that request to the local listener. The pin is
// exercised rather than bypassed, and every byte on the wire is the one BuildPayload produced.
//
// # What it is for
//
// Getting per-node metrics into a self-hosted platform, which is what the P4.5 scorecard renders. A run
// linked without them gets `state: empty` and the message "This run was linked without per-node
// metrics" — correct, and not something the platform can fix from its side.
//
//	go run ./cmd/heroslocallink -repo ../hermes-agent -addr 127.0.0.1:14321 -token "$KEY"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/runlink/transport"
)

// localDial carries a request addressed to the pinned platform host to a local listener.
//
// It rewrites a COPY of the request. Mutating the original would edit the URL the client already
// asserted, which is the one thing this file must not do: the assertion has to stay true of the request
// that was built, or the guard is being defeated rather than satisfied.
type localDial struct{ addr string }

func (d localDial) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != mustHost(runlink.PlatformBaseURL) {
		return nil, fmt.Errorf("heroslocallink: refusing to carry a request for %q — this transport "+
			"exists to reach a local deployment on behalf of the PINNED host and nothing else", req.URL.Host)
	}
	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	out.URL.Host = d.addr
	out.Host = req.URL.Host // keep the pinned host in the Host header, so the server sees what was sent
	return http.DefaultTransport.RoundTrip(out)
}

func mustHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic("heroslocallink: PlatformBaseURL is not a URL: " + err.Error())
	}
	return u.Host
}

func main() {
	log.SetFlags(0)
	repo := flag.String("repo", ".", "repository whose local run store (<repo>/.heros/runs) to read")
	run := flag.String("run", "", "run id to link; defaults to the newest record in the store")
	addr := flag.String("addr", "127.0.0.1:14321", "host:port of the self-hosted agentd")
	token := flag.String("token", os.Getenv("HEROS_PLATFORM_TOKEN"), "platform token (or $HEROS_PLATFORM_TOKEN)")
	flag.Parse()

	if strings.TrimSpace(*token) == "" {
		log.Fatal("heroslocallink: no token — pass -token or set HEROS_PLATFORM_TOKEN. The deployment's " +
			"tenant credentials are in deploy/config/config.json.")
	}

	store := cli.OpenRunStore(*repo)
	runID := *run
	if runID == "" {
		ids, err := store.List()
		if err != nil {
			log.Fatalf("heroslocallink: read the run store under %s: %v", *repo, err)
		}
		if len(ids) == 0 {
			log.Fatalf("heroslocallink: no runs in %s/.heros/runs — run `heros eval` there first", *repo)
		}
		sort.Strings(ids)
		runID = ids[len(ids)-1]
	}

	record, err := store.Get(runID)
	if err != nil {
		log.Fatalf("heroslocallink: read run %s: %v", runID, err)
	}

	// Built by the SHIPPED builder. A payload assembled here by hand would prove nothing about what the
	// CLI transmits, which is the only interesting question.
	payload := runlink.BuildPayload(record)

	// 🔴 Reported before transmitting, because a run whose record predates the per-node or eval fields
	// links happily and lands as a scorecard that says "no per-node metrics" — the same empty state as a
	// broken pipeline, reached for a completely different reason. Say which one this is, up front.
	if len(payload.Metrics.PerNode) == 0 {
		log.Printf("⚠️  %s carries NO per-node metrics. It will link, and the scorecard will report "+
			"`state: empty`. Re-run `heros eval` with a current build to record them.", runID)
	}
	log.Printf("link: %s (%s) — %d node(s) attributed, %d eval case(s), gate %s",
		runID, record.WorkflowID, len(payload.Metrics.PerNode), payload.Eval.CaseCount, payload.Eval.GateOutcome)

	client := transport.NewClient(*token, transport.WithRoundTripper(localDial{addr: *addr}))
	res, err := client.Link(context.Background(), payload)
	if err != nil {
		log.Fatalf("heroslocallink: %v", err)
	}
	log.Printf("accepted=%v already_linked=%v run_url=%s", res.Accepted, res.AlreadyLinked, res.RunURL)
}
