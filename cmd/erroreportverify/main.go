// Command erroreportverify is P24's LIVE verification harness (task 2.13).
//
// # What it does, and why it is a command rather than a test
//
// It starts the real `internal/api` server, with the real error-reporting boundary built from the real
// environment, registers one route that PANICS, and calls it over a real socket. Everything in the path
// is production code: the middleware that installs the trace identity, the panic recovery that
// constructs the event, the allowlist, the scrubber, the envelope and the HTTP transmit.
//
// It is a command and not a test for one reason: it transmits to whatever `HEROS_ERROR_REPORTING_DSN`
// names, and a test that reaches a real third-party inbox is a test that either fails in CI or quietly
// sends fixtures to production. So the live half is something a human runs deliberately, with the target
// on the command line, while the same code paths are asserted against a local capture endpoint in
// `internal/erroreport` and `internal/api` with no environment precondition at all.
//
// # 🔴 A panic endpoint does not exist in a served build
//
// The route below is registered HERE, in a verification harness, not in `internal/api`. A diagnostic
// endpoint that exists in one deployment shape and not another is a defect in this repository's terms,
// and a panic endpoint that exists in all of them is worse.
//
//	HEROS_ERROR_REPORTING_DSN=… HEROS_EDITION=dev HEROS_VERSION=… go run ./cmd/erroreportverify
//
// With no DSN it verifies the ABSENT path instead — which is a real answer, and the one every substrate
// except the platform's own hosted deployment should give.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/erroreport"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// theRunID makes the request run-scoped, so the trace identity the event carries is the RUN's — the
// same string `telemetry.TraceID` derives for that run's spans. That is the property task 2.5 is about,
// and verifying it live is the only way to know the middleware installs it on a real request.
const theRunID = "run-p24-live-verification"

func main() {
	dsn := strings.TrimSpace(os.Getenv(erroreport.EnvDSN))

	reporter, err := erroreport.FromEnv(func(format string, args ...any) {
		fmt.Printf("  [log] "+format+"\n", args...)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "erroreportverify: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = reporter.Close(ctx)
	}()

	state, class := reporter.State()
	fmt.Printf("erroreportverify: reporter state = %s%s\n", state, suffix(class))
	if dsn == "" {
		fmt.Printf("erroreportverify: %s is unset, so this run verifies the ABSENT path.\n", erroreport.EnvDSN)
	} else {
		fmt.Printf("erroreportverify: transmitting to %s\n", redactDSN(dsn))
	}

	srv := api.New(nil, config.Config{})
	srv.SetErrorReporter(reporter)
	srv.Mux.HandleFunc("GET /api/erroreportverify/runs/{runID}", func(http.ResponseWriter, *http.Request) {
		// A panic whose VALUE carries forbidden material on purpose. If any of it appears in the inbox,
		// the boundary is broken in the one way that matters.
		panic(fmt.Sprintf(
			"deliberate P24 verification panic: tenant %q key %s prompt %q",
			"Nous Research Ltd",
			"sk-ant-api03-erroreportverify-not-a-real-key-0000000000",
			strings.Repeat("summarise the attached contract ", 40),
		))
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "erroreportverify: listen: %v\n", err)
		os.Exit(1)
	}
	httpSrv := &http.Server{Handler: srv.Handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(listener) }()
	defer func() { _ = httpSrv.Close() }()

	base := "http://" + listener.Addr().String()
	resp, err := http.Get(base + "/api/erroreportverify/runs/" + theRunID) //nolint:noctx // a local, single-shot probe
	if err != nil {
		fmt.Fprintf(os.Stderr, "erroreportverify: the probe request failed: %v\n", err)
		os.Exit(1)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	fmt.Println()
	fmt.Println("── the served response ──────────────────────────────────────────")
	fmt.Printf("  status            : %d\n", resp.StatusCode)
	header := resp.Header.Get(telemetry.TraceHeader)
	fmt.Printf("  %-18s: %s\n", telemetry.TraceHeader, header)

	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	fmt.Printf("  body.code         : %v\n", parsed["code"])
	fmt.Printf("  body.trace_id     : %v\n", parsed["trace_id"])

	spanTrace := telemetry.TraceID(theRunID)
	fmt.Println()
	fmt.Println("── one identity, three places ───────────────────────────────────")
	fmt.Printf("  telemetry.TraceID(%q)\n    = %s\n", theRunID, spanTrace)
	ok := header == spanTrace && parsed["trace_id"] == spanTrace
	fmt.Printf("  header == span == body : %v\n", ok)
	if !ok {
		fmt.Fprintln(os.Stderr, "erroreportverify: the three identities are not one string")
		os.Exit(1)
	}
	if parsed["code"] != string(errorcode.PlatformPanic) {
		fmt.Fprintf(os.Stderr, "erroreportverify: the response code is %v, want %s\n", parsed["code"], errorcode.PlatformPanic)
		os.Exit(1)
	}

	// Flush before reporting the state: `Close` waits for the queued event to be transmitted, so the
	// state printed afterwards reflects whether the transmit actually succeeded.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := reporter.Close(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "erroreportverify: flush: %v\n", err)
		os.Exit(1)
	}
	state, class = reporter.State()

	fmt.Println()
	fmt.Println("── transmission ─────────────────────────────────────────────────")
	fmt.Printf("  reporter state    : %s%s\n", state, suffix(class))
	switch state {
	case erroreport.StateAbsent:
		fmt.Println("  nothing was transmitted, and nothing was logged. That is the correct and expected")
		fmt.Println("  state on every substrate except the platform's own hosted deployment.")
	case erroreport.StateConfigured:
		fmt.Printf("  the ingest endpoint accepted the envelope. Look for an issue carrying error.code=%s,\n",
			errorcode.PlatformPanic)
		fmt.Printf("  trace_id=%s, and platform frames from internal/api.\n", spanTrace)
		fmt.Println()
		fmt.Println("  🔴 What this run does NOT establish: whether the STORED payload contains a forbidden")
		fmt.Println("  shape. That requires reading the issue back through the vendor's API with an auth")
		fmt.Println("  token this harness deliberately does not hold. The transmitted BYTES are asserted")
		fmt.Println("  against the forbidden-shape fixture in internal/erroreport, off a real socket.")
	case erroreport.StateDegraded:
		fmt.Fprintf(os.Stderr, "erroreportverify: transmission failed (%s)\n", class)
		os.Exit(1)
	}
}

func suffix(class string) string {
	if class == "" {
		return ""
	}
	return " (" + class + ")"
}

// redactDSN prints where the events go without printing the key that authorises them.
func redactDSN(raw string) string {
	at := strings.Index(raw, "@")
	scheme := strings.Index(raw, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return "<unparseable DSN>"
	}
	return raw[:scheme+3] + "***@" + raw[at+1:]
}
