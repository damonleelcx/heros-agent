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
//	go run ./cmd/heroslocallink -repo ../hermes-agent -addr 127.0.0.1:14321 -token "$KEY" \
//	    -login -with-ir
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/clilink"
	"github.com/heros-foreal/agentd/internal/runlink"
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
	login := flag.Bool("login", false, "authenticate and STORE the token first, exactly as `heros login` does")
	device := flag.Bool("device", false, "run the DEVICE authorization instead: print a code, wait for somebody to approve it in the console, and store the personal credential it issues (P27 §13). Implies -login and needs no -token")
	withIR := flag.Bool("with-ir", false, "ALSO transmit the workflow STRUCTURE, as `heros link --with-ir` does")
	irPath := flag.String("ir", "", "path to the IR to transmit with -with-ir (default <repo>/ir.json)")
	flag.Parse()

	// 🔴 The device flow is the PERSON path and deliberately has no token: that is the whole point of it.
	// `heros login` with no --token does exactly this, and this binary carries it for the same reason it
	// carries `login` — so the local deployment can be driven through the SHIPPED command path rather
	// than through a hand-rolled transport call.
	if *device {
		cmds := clilink.Commands{RT: localDial{addr: *addr}, Timeout: 30 * time.Second}
		r := cli.NewResolver(map[string]string{"repo": ".", "run": "", "token": "", "dry-run": "false"})
		if err := cmds.Login(r.Resolve(), cli.Streams{Out: os.Stdout, Err: os.Stderr}); err != nil {
			log.Fatalf("heroslocallink: device login: %v", err)
		}
		return
	}

	if strings.TrimSpace(*token) == "" {
		log.Fatal("heroslocallink: no token — pass -token or set HEROS_PLATFORM_TOKEN, or use -device to " +
			"sign in as a PERSON through the console. The deployment's tenant credentials are in " +
			"deploy/config/config.json.")
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

	// ── The two SHIPPED commands, over the redirected dial ──────────────────────────────────────────
	//
	// `login` and `link` are run through clilink.Commands — the same value cmd/heros injects as
	// cli.NetCommands — rather than by calling the transport directly. The difference is not cosmetic:
	// the command path is where the credential is validated and stored 0600, where the payload
	// self-check runs, and where "link: <run> linked · view it at …" is printed. A hand-rolled
	// transport call skips all of it and then proves only that a POST works.
	//
	// 🔴 `login` here validates against the SAME /api/v1/whoami the console's `platform` identity seam
	// asks (see web/console/src/lib/idp/platformToken.ts). That is the point of doing it at all: "the
	// CLI accepted this token" and "the console accepts this token" must stay one question, and the
	// only way to observe that they are is to authenticate the CLI and then sign the console in with
	// the same string.
	cmds := clilink.Commands{RT: localDial{addr: *addr}, Timeout: 30 * time.Second}
	streams := cli.Streams{Out: os.Stdout, Err: os.Stderr}
	cfg := func(kv map[string]string) cli.Config {
		r := cli.NewResolver(map[string]string{"repo": ".", "run": "", "token": "", "dry-run": "false", "with-ir": ""})
		r.SetFlag("repo", *repo)
		for k, v := range kv {
			r.SetFlag(k, v)
		}
		return r.Resolve()
	}

	if *login {
		if err := cmds.Login(cfg(map[string]string{"token": *token}), streams); err != nil {
			log.Fatalf("heroslocallink: login: %v", err)
		}
	}

	linkFlags := map[string]string{"run": runID}
	if *withIR {
		p := *irPath
		if p == "" {
			p = filepath.Join(*repo, "ir.json")
		}
		linkFlags["with-ir"] = p
	}
	if err := cmds.Link(cfg(linkFlags), streams); err != nil {
		log.Fatalf("heroslocallink: link: %v", err)
	}
}
