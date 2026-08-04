package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/heros-foreal/agentd/internal/adminlaunch"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/launch"
)

func main() {
	cfgPath := flag.String("config", "", "path to config.json (optional)")
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit 0/1 (for a distroless HEALTHCHECK with no shell)")
	// The operator bootstrap (P22). See internal/adminlaunch/bootstrap.go for the deadlock it breaks:
	// no operator session without a verified second factor, no factor enrolment without a session.
	bootstrapSubject := flag.String("admin-bootstrap-subject", "", "create the first operator, bound to this IdP subject claim (Okta issues 00u…); run it twice — see the output")
	bootstrapRole := flag.String("admin-bootstrap-role", "superadmin", "the role to grant the bootstrapped operator")
	bootstrapAdminID := flag.String("admin-bootstrap-id", "", "the platform's own id for the bootstrapped operator (default adm-<role>)")
	flag.Parse()

	cfg, cfgSrc, err := config.LoadAuto(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if cfgSrc != "" && !*healthcheck {
		log.Printf("config: %s", cfgSrc)
	}

	// -healthcheck: the container HEALTHCHECK path for the distroless image, which has no shell to run
	// wget/curl. It probes the SAME /healthz the orchestrator would, against the address this process
	// binds, and exits 0 (serving) or 1 (not). Liveness, not readiness: a health check that fails while
	// a dependency is down would make the orchestrator kill and restart a process that is working fine,
	// turning a dependency outage into a crash loop.
	if *healthcheck {
		os.Exit(runHealthcheck(cfg.ListenAddr))
	}

	// -admin-bootstrap-subject: create the first operator and exit. It does NOT start the server — this
	// is a deployment-time act run as its own process (a Kubernetes Job, a one-off `docker run`), and a
	// command that also began serving would make an operator choose between bootstrapping and a clean
	// process lifecycle.
	if strings.TrimSpace(*bootstrapSubject) != "" {
		err := launch.RunAdminBootstrap(context.Background(), launch.AdminBootstrapOptions{
			Subject: *bootstrapSubject, AdminID: *bootstrapAdminID, Role: *bootstrapRole,
		}, os.Stdout)
		if err != nil {
			// Pass 1 is not a crash — it is the command telling the operator what to provision — so it
			// exits non-zero (the bootstrap is not complete) without the `panic`-adjacent framing of
			// log.Fatal, and its instructions are already on stdout.
			if errors.Is(err, adminlaunch.ErrSeedNotProvisioned) {
				os.Exit(2)
			}
			log.Fatalf("operator bootstrap: %v", err)
		}
		return
	}

	srv, err := launch.StartAgentd(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// runHealthcheck probes /healthz on the given listen address and returns a process exit code. The
// address may bind 0.0.0.0 in a container; the probe reaches it over the loopback on the same port.
func runHealthcheck(listenAddr string) int {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: bad listen addr %q: %v\n", listenAddr, err)
		return 1
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: /healthz returned %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
