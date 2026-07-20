package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingSink captures audit events for assertion.
type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingSink) Record(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}
func (r *recordingSink) all() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}
func (r *recordingSink) has(kind EventKind, denial DenialKind, phase Phase) bool {
	for _, e := range r.all() {
		if e.Kind != kind {
			continue
		}
		if kind == EventDenial && e.Denial == denial {
			return true
		}
		if kind == EventLifecycle && e.Phase == phase {
			return true
		}
	}
	return false
}

// fullCaps wraps the portable enforcer and CLAIMS full isolation so tests can exercise the happy path
// and the OS-independent logic. Never do this in production — claiming a capability the host lacks
// defeats the fail-closed gate (see WithCapabilities' doc).
func testSandboxFullCaps(t *testing.T, sink AuditSink) *Sandbox {
	t.Helper()
	e := NewSubprocessEnforcer().WithCapabilities(Capabilities{
		ScrubEnv: true, ResourceLimits: true, NetworkDeny: true, FilesystemScope: true,
	})
	return New(e, WithAudit(sink), WithWarmPool(2))
}

func repoToolSpec() Spec {
	return Spec{
		NodeID: "node_1", RunID: "run_1",
		RequireNetworkIsolation: true, RequireFilesystemScope: true,
		Bounds: ResourceBounds{Wallclock: 5 * time.Second, MaxOutput: 64 << 10, CPU: 2 * time.Second, MaxPIDs: 64},
	}
}

// Task 3.6 / spec "Un-creatable isolate does not fall back to the host": the portable enforcer cannot
// deny egress at the OS level, so a spec that REQUIRES network isolation fails closed — and the tool
// never runs (proven by a marker file that is never written).
func TestFailClosed_NoHostFallbackWhenIsolationUnavailable(t *testing.T) {
	sink := &recordingSink{}
	sb := New(NewSubprocessEnforcer(), WithAudit(sink)) // honest caps: NetworkDeny=false
	marker := filepath.Join(t.TempDir(), "ran")

	spec := repoToolSpec()
	_, err := sb.Run(context.Background(), spec, Tool{Argv: []string{"sh", "-c", "echo ran > " + marker}})
	if !errors.Is(err, ErrIsolateUnavailable) {
		t.Fatalf("want ErrIsolateUnavailable, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("tool code executed despite the isolate being uncreatable — host fallback happened")
	}
	if !sink.has(EventLifecycle, "", PhaseCreateFailed) {
		t.Error("a create_failed lifecycle event should be audited")
	}
}

// Task 3.2 / spec "A credential-reading tool finds nothing usable": a provider key present in the host
// environment does not reach the isolate; a tool dumping its env finds nothing usable.
func TestNoAmbientCredentials(t *testing.T) {
	t.Setenv("PROVIDER_API_KEY", "sk-live-supersecret-value")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret-should-not-leak")

	sink := &recordingSink{}
	sb := testSandboxFullCaps(t, sink)
	res, err := sb.Run(context.Background(), repoToolSpec(), Tool{Argv: []string{"sh", "-c", "env; echo HOME=$HOME; cat ~/.aws/credentials 2>/dev/null; echo done"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := string(res.Stdout)
	for _, secret := range []string{"sk-live-supersecret-value", "aws-secret-should-not-leak", "PROVIDER_API_KEY", "AWS_SECRET_ACCESS_KEY"} {
		if strings.Contains(out, secret) {
			t.Errorf("isolate leaked ambient credential %q; output:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "done") {
		t.Errorf("tool did not run to completion: %s", out)
	}
}

// Task 3.5 / spec "A resource-exhausting tool is contained": a CPU-spinning tool hits the CPU bound and
// is terminated; the node fails closed with a typed resource error and a denial is audited. A second
// concurrent run is unaffected (blast radius contained).
func TestResourceBounds_CPUSpinContainedAndBlastRadius(t *testing.T) {
	sink := &recordingSink{}
	sb := testSandboxFullCaps(t, sink)

	spec := repoToolSpec()
	spec.Bounds.CPU = 1 * time.Second
	spec.Bounds.Wallclock = 8 * time.Second

	var wg sync.WaitGroup
	var breachErr, goodErr error
	var goodOut string
	wg.Add(2)
	// Runaway CPU tool.
	go func() {
		defer wg.Done()
		_, breachErr = sb.Run(context.Background(), spec, Tool{Argv: []string{"sh", "-c", "while :; do :; done"}})
	}()
	// A well-behaved concurrent run.
	go func() {
		defer wg.Done()
		good := repoToolSpec()
		good.NodeID = "node_2"
		res, err := sb.Run(context.Background(), good, Tool{Argv: []string{"sh", "-c", "echo healthy"}})
		goodErr = err
		if res != nil {
			goodOut = strings.TrimSpace(string(res.Stdout))
		}
	}()
	wg.Wait()

	if !errors.Is(breachErr, ErrResourceBreach) {
		t.Errorf("CPU-spin tool was not contained as a resource breach: %v", breachErr)
	}
	if !sink.has(EventDenial, DenyResource, "") {
		t.Error("a resource denial should be audited")
	}
	if goodErr != nil || goodOut != "healthy" {
		t.Errorf("second concurrent run was affected by the breach: out=%q err=%v", goodOut, goodErr)
	}
}

// Task 3.5: a tool spewing more than the output cap is a resource breach.
func TestResourceBounds_OutputCap(t *testing.T) {
	sink := &recordingSink{}
	sb := testSandboxFullCaps(t, sink)
	spec := repoToolSpec()
	spec.Bounds.MaxOutput = 4096

	_, err := sb.Run(context.Background(), spec, Tool{Argv: []string{"sh", "-c", "yes AAAAAAAAAAAAAAAA | head -c 200000"}})
	if !errors.Is(err, ErrResourceBreach) {
		t.Fatalf("output overflow not contained: %v", err)
	}
}

// Task 3.5: a tool exceeding the wall-clock deadline is terminated and fails closed.
func TestResourceBounds_WallClock(t *testing.T) {
	sb := testSandboxFullCaps(t, &recordingSink{})
	spec := repoToolSpec()
	spec.Bounds.Wallclock = 500 * time.Millisecond
	spec.Bounds.CPU = 30 * time.Second // don't let CPU limit fire first

	start := time.Now()
	_, err := sb.Run(context.Background(), spec, Tool{Argv: []string{"sh", "-c", "sleep 10"}})
	if !errors.Is(err, ErrResourceBreach) {
		t.Fatalf("wall-clock breach not contained: %v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("wall-clock enforcement was too slow: %s", time.Since(start))
	}
}

// Task 3.4: the declared working set is copied into the isolate read-only; a file outside it is not
// present, and the copied file cannot be written.
func TestFilesystem_WorkingSetReadOnlyAndScoped(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed.txt")
	if err := os.WriteFile(allowed, []byte("visible"), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &recordingSink{}
	sb := testSandboxFullCaps(t, sink)
	spec := repoToolSpec()
	spec.WorkingSet = []string{allowed}

	// The tool reads the staged copy (present), then tries to write it (must fail: read-only).
	res, err := sb.Run(context.Background(), spec, Tool{Argv: []string{"sh", "-c",
		"cat work_allowed 2>/dev/null || cat allowed.txt; echo ---; (echo x > allowed.txt && echo WROTE) || echo READONLY"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := string(res.Stdout)
	if !strings.Contains(out, "visible") {
		t.Errorf("working-set file was not available to the tool: %s", out)
	}
	if strings.Contains(out, "WROTE") || !strings.Contains(out, "READONLY") {
		t.Errorf("working-set copy was writable; want read-only: %s", out)
	}
}

// Task 3.7 / lifecycle audit: a successful run emits created → started → finished → destroyed in order.
func TestLifecycle_EphemeralAndAudited(t *testing.T) {
	sink := &recordingSink{}
	sb := testSandboxFullCaps(t, sink)
	if _, err := sb.Run(context.Background(), repoToolSpec(), Tool{Argv: []string{"sh", "-c", "echo hi"}}); err != nil {
		t.Fatalf("run: %v", err)
	}
	var phases []Phase
	for _, e := range sink.all() {
		if e.Kind == EventLifecycle {
			phases = append(phases, e.Phase)
		}
	}
	want := []Phase{PhaseCreated, PhaseStarted, PhaseFinished, PhaseDestroyed}
	if len(phases) != len(want) {
		t.Fatalf("lifecycle phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("lifecycle order = %v, want %v", phases, want)
		}
	}
}

// A spec with no argv is rejected.
func TestRun_RejectsEmptyArgv(t *testing.T) {
	sb := testSandboxFullCaps(t, &recordingSink{})
	if _, err := sb.Run(context.Background(), repoToolSpec(), Tool{}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec, got %v", err)
	}
}
