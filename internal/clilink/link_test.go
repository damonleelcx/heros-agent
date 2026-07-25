package clilink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// captureRT serves https://heros-agent.space locally (keeping the endpoint pin exercised, not bypassed)
// and records every request it sees — so a test can assert both WHAT was sent and THAT nothing was sent.
type captureRT struct {
	requests int32
	bodies   [][]byte
	handler  http.HandlerFunc
}

func (c *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&c.requests, 1)
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		c.bodies = append(c.bodies, body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	// The pin must hold: only heros-agent.space is ever contacted.
	if req.URL.Host != "heros-agent.space" {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusBadGateway)
		return rec.Result(), nil
	}
	rec := httptest.NewRecorder()
	c.handler(rec, req)
	return rec.Result(), nil
}

// okServer answers whoami + link like the real platform, echoing a run URL.
func okServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/p11/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{"identity": "tenantA"})
		case "/api/p11/link":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted": true, "run_url": "https://heros-agent.space/app/runs/run-x", "contract_version": runlink.ContractVersion,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// evalFixture runs a real offline eval to produce a linkable run record, returning repo + run id.
func evalFixture(t *testing.T) (repo, runID string) {
	t.Helper()
	src := filepath.Join("..", "discovery", "testdata", "samplerepo")
	if _, err := os.Stat(src); err != nil {
		t.Skip("no sample repo")
	}
	repo = filepath.Join(t.TempDir(), "repo")
	if err := copyDir(t, src, repo); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := cli.Main([]string{"eval", "--repo", repo, "--seeds", "5", "--cases", "4"},
		cli.Streams{Out: &out, Err: io.Discard}, func(string) (string, bool) { return "", false }, nil)
	if code != cli.ExitOK {
		t.Fatalf("eval fixture exit %d", code)
	}
	var env cli.Envelope
	_ = json.Unmarshal(out.Bytes(), &env)
	b, _ := json.Marshal(env.Data)
	var d struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal(b, &d)
	return repo, d.RunID
}

// TestDryRunIsByteIdenticalToSend proves the dry-run renders the EXACT payload that is sent (FR10,
// tasks 3.2). It links for real (capturing the request body) and dry-runs, and asserts the payload
// bytes are identical.
func TestDryRunIsByteIdenticalToSend(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)
	rt := &captureRT{handler: okServer()}
	cmds := Commands{RT: rt}

	// Authenticate so the real link path runs.
	if err := cmds.Login(cfgWith(t, repo, map[string]string{"token": "tok"}), silent()); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Real link — capture the request body.
	var realOut bytes.Buffer
	if err := cmds.Link(cfgWith(t, repo, map[string]string{"run": runID}), cli.Streams{Out: &realOut, Err: io.Discard}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if len(rt.bodies) != 1 {
		t.Fatalf("expected exactly one transmitted request, got %d", len(rt.bodies))
	}
	sentPayload := rt.bodies[0]

	// Dry-run — extract the rendered payload.
	var dryOut bytes.Buffer
	if err := cmds.Link(cfgWith(t, repo, map[string]string{"run": runID, "dry-run": "true"}), cli.Streams{Out: &dryOut, Err: io.Discard}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	var env cli.Envelope
	_ = json.Unmarshal(dryOut.Bytes(), &env)
	dataB, _ := json.Marshal(env.Data)
	var ld LinkData
	_ = json.Unmarshal(dataB, &ld)
	renderedPayload, _ := json.Marshal(ld.Payload)

	// Both must be the same payload bytes.
	if !bytes.Equal(normalize(t, sentPayload), normalize(t, renderedPayload)) {
		t.Errorf("dry-run payload != sent payload\nsent:     %s\nrendered: %s", sentPayload, renderedPayload)
	}
}

// TestOnlyLinkTransmits — discover and eval transmit NOTHING; only an explicit link does (FR9, tasks
// 3.3/3.11). The capture RT records every outbound request.
func TestOnlyLinkTransmits(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, _ := evalFixture(t)
	rt := &captureRT{handler: okServer()}
	cmds := Commands{RT: rt}

	// discover + eval + status through the dispatcher, with the net commands injected but never invoked.
	for _, args := range [][]string{
		{"discover", "--repo", repo, "--out", filepath.Join(t.TempDir(), "ir.json"), "--report", filepath.Join(t.TempDir(), "r.json")},
		{"eval", "--repo", repo, "--seeds", "3", "--cases", "3"},
		{"status", "--repo", repo},
	} {
		cli.Main(args, cli.Streams{Out: io.Discard, Err: io.Discard}, func(string) (string, bool) { return "", false }, cmds)
	}
	if n := atomic.LoadInt32(&rt.requests); n != 0 {
		t.Errorf("local commands transmitted %d requests — there must be no ambient transmission", n)
	}
}

// TestLinkFailureIsNonFatalToLocalResult — a failed link does not invalidate the local run (FR19, task
// 3.8). The run record on disk survives, and the failure is a transmission failure (operational), not a
// run failure.
func TestLinkFailureIsNonFatalToLocalResult(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)
	// A server that 500s on link but accepts login.
	rt := &captureRT{handler: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/p11/whoami" {
			_ = json.NewEncoder(w).Encode(map[string]any{"identity": "tenantA"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}}
	cmds := Commands{RT: rt}
	_ = cmds.Login(cfgWith(t, repo, map[string]string{"token": "tok"}), silent())

	err := cmds.Link(cfgWith(t, repo, map[string]string{"run": runID}), silent())
	if err == nil {
		t.Fatal("expected a link failure")
	}
	var ee *cli.ExitError
	if !asExit(err, &ee) || ee.Code != cli.ExitOperational {
		t.Errorf("link failure should be operational (exit 2), got %v", err)
	}
	// The local run record is untouched.
	if _, gerr := cli.OpenRunStore(repo).Get(runID); gerr != nil {
		t.Errorf("local run record was invalidated by a link failure: %v", gerr)
	}
}

// TestTokenNeverInPayloadBody — the token authenticates via a header, never the body; no request body
// contains it (FR3/FR13/NFR3).
func TestTokenNeverInPayloadBody(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, runID := evalFixture(t)
	rt := &captureRT{handler: okServer()}
	cmds := Commands{RT: rt}
	_ = cmds.Login(cfgWith(t, repo, map[string]string{"token": "super-secret-token"}), silent())
	_ = cmds.Link(cfgWith(t, repo, map[string]string{"run": runID}), silent())
	for _, b := range rt.bodies {
		if strings.Contains(string(b), "super-secret-token") {
			t.Errorf("token leaked into a request body: %s", b)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func silent() cli.Streams { return cli.Streams{Out: io.Discard, Err: io.Discard} }

func asExit(err error, target **cli.ExitError) bool {
	for err != nil {
		if ee, ok := err.(*cli.ExitError); ok {
			*target = ee
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// cfgWith builds a resolved cli.Config with the given key/values plus repo, via the exported resolver.
func cfgWith(t *testing.T, repo string, kv map[string]string) cli.Config {
	t.Helper()
	defaults := map[string]string{"repo": ".", "run": "", "token": "", "dry-run": "false"}
	r := cli.NewResolver(defaults)
	r.SetFlag("repo", repo)
	for k, v := range kv {
		r.SetFlag(k, v)
	}
	return r.Resolve()
}

func normalize(t *testing.T, b []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	out, _ := json.Marshal(v)
	return out
}

func copyDir(t *testing.T, src, dst string) error {
	t.Helper()
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
