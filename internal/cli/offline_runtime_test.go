package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// offline_runtime_test.go proves the offline guarantee at RUNTIME, not by inspecting call sites (task
// 2.9 / 7.1). It installs a dialer that fails EVERY outbound connection, then runs discover, apply and
// eval and asserts they still succeed. A library that quietly resolves DNS or dials on init would fail
// this — which is exactly the failure an import review misses (design, QA lens).

func withNetworkDenied(t *testing.T) {
	t.Helper()
	deny := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("network denied by offline test: " + network + " " + addr)
	}
	origDefault := http.DefaultTransport
	origClient := http.DefaultClient
	http.DefaultTransport = &http.Transport{DialContext: deny, TLSHandshakeTimeout: time.Second}
	http.DefaultClient = &http.Client{Transport: http.DefaultTransport}
	t.Cleanup(func() {
		http.DefaultTransport = origDefault
		http.DefaultClient = origClient
	})
}

func TestLocalWorkflowRunsWithNetworkingDenied(t *testing.T) {
	withNetworkDenied(t)
	repo := fixtureRepo(t)

	// discover
	if code, _, stderr := run(t, "discover", "--repo", repo,
		"--out", filepath.Join(t.TempDir(), "ir.json"), "--report", filepath.Join(t.TempDir(), "r.json")); code != ExitOK {
		t.Fatalf("discover failed under network-denied: exit %d (%s)", code, stderr)
	}

	// eval
	if code, _, stderr := run(t, "eval", "--repo", repo, "--seeds", "5", "--cases", "4"); code != ExitOK {
		t.Fatalf("eval failed under network-denied: exit %d (%s)", code, stderr)
	}

	// apply a baseline spec (no registry-backed overrides → resolvable offline).
	specPath := filepath.Join(repo, "baseline.spec.json")
	spec := `{"workflow_id":"example.com/sample","source_revision":"0000000","order":["n_68fcfb1cd916ed9b"],"nodes":{},"edges":[]}`
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, "apply", "--repo", repo, "--spec", specPath,
		"--out", filepath.Join(t.TempDir(), "variant.diff")); code != ExitOK {
		t.Fatalf("apply failed under network-denied: exit %d (%s)", code, stderr)
	}
}
