package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
)

// fakeP6 is a minimal P6Source that records the actions the UI drives, so the handler wiring — grant,
// monitor, stop, re-arm, rollback — is tested without a live loop (the loop itself is proven in
// internal/optimizer).
type fakeP6 struct {
	granted    bool
	stopped    bool
	rearmed    bool
	rolledBack string
	state      string
}

func (f *fakeP6) Monitor(runID string) (Monitor, bool) {
	if !f.granted {
		return Monitor{}, false
	}
	return f.snapshot(runID), true
}

func (f *fakeP6) Grant(req GrantRequest) (Monitor, error) {
	f.granted, f.state = true, "running"
	return f.snapshot(req.RunID), nil
}

func (f *fakeP6) Stop(runID, actor string) (Monitor, error) {
	f.stopped, f.state = true, "stopped"
	return f.snapshot(runID), nil
}

func (f *fakeP6) Rearm(runID, actor string) (Monitor, error) {
	f.rearmed = true
	return f.snapshot(runID), nil
}

func (f *fakeP6) Rollback(runID, mergeCommit, actor string) (Monitor, error) {
	f.rolledBack = mergeCommit
	return f.snapshot(runID), nil
}

func (f *fakeP6) snapshot(runID string) Monitor {
	m := Monitor{RunID: runID, WorkflowID: "wf", AutomationLevel: "autonomous", State: f.state,
		MergeEnabled: true, BudgetCeilingUSD: 0.5, CurrentIteration: 2, MaxIterations: 8, PRsMerged: 1,
		Merges: []MergeView{{MergeCommit: "merge001", Operator: "model_upgrade", Node: "reason", VerifiedDelta: 0.4}}}
	if f.rolledBack != "" {
		m.Merges[0].RolledBack, m.Merges[0].RevertCommit = true, "revert001"
	}
	return m
}

func mountFake() (*Server, *fakeP6) {
	s := New(nil, config.Config{})
	f := &fakeP6{}
	s.MountP6(f)
	return s, f
}

func TestP6_GrantThenMonitor(t *testing.T) {
	s, f := mountFake()

	// Before grant, the run does not exist → 404 (the UI shows the grant panel).
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/demo/optimizer", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("pre-grant monitor should 404, got %d", w.Code)
	}

	// Grant records constraints and starts the run.
	body, _ := json.Marshal(GrantRequest{RunID: "demo", WorkflowID: "wf", Actor: "damon",
		BudgetCeilingUSD: 0.5, ProviderAllowlist: []string{"anthropic"}, MinImprovement: 0.03, MaxIterations: 8})
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/optimizer/grants", bytes.NewReader(body)))
	if w.Code != http.StatusOK || !f.granted {
		t.Fatalf("grant should succeed, got %d granted=%v", w.Code, f.granted)
	}

	// Monitor now returns the live view.
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/demo/optimizer", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("post-grant monitor should 200, got %d", w.Code)
	}
	var m Monitor
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.State != "running" || m.PRsMerged != 1 {
		t.Fatalf("unexpected monitor: %+v", m)
	}
}

func TestP6_StopRearmRollback(t *testing.T) {
	s, f := mountFake()
	_, _ = f.Grant(GrantRequest{RunID: "demo"})

	// Stop fires the kill switch.
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/runs/demo/optimizer/stop", bytes.NewReader([]byte(`{"actor":"u"}`))))
	if w.Code != http.StatusOK || !f.stopped {
		t.Fatalf("stop should fire, got %d stopped=%v", w.Code, f.stopped)
	}

	// Re-arm after a halt.
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/runs/demo/optimizer/rearm", bytes.NewReader([]byte(`{"actor":"op"}`))))
	if w.Code != http.StatusOK || !f.rearmed {
		t.Fatalf("rearm should succeed, got %d rearmed=%v", w.Code, f.rearmed)
	}

	// Rollback a merged change via git revert.
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/runs/demo/optimizer/rollback",
		bytes.NewReader([]byte(`{"merge_commit":"merge001","actor":"op"}`))))
	if w.Code != http.StatusOK || f.rolledBack != "merge001" {
		t.Fatalf("rollback should target merge001, got %d rolledBack=%q", w.Code, f.rolledBack)
	}
	var m Monitor
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if !m.Merges[0].RolledBack {
		t.Fatal("rolled-back merge should be flagged in the refreshed monitor")
	}
}
