package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// p32_health_test.go is §7.6's and §7.7's second halves: the signals must be READABLE FROM THE
// ENDPOINT, not merely computed.
//
// # 🔴 Why a job-level test is not enough
//
// `TestRetentionRemovesExpiredCloneSnapshotsAndNotPushedOnes` asserts the job records its last success.
// That is a property of a struct. `health-signal-surface` is about a property of the DEPLOYMENT: *"any
// pipeline health, connectivity or self-check signal cannot live only in logs, it must expose a
// readable endpoint."*
//
// The gap between the two is a real one and it is one line wide — `SetSourceIngestHealth` never called,
// or called with a nil, and the job runs perfectly while `/readyz` says nothing about it. Nothing
// fails. The dashboard is empty and reads as "no problems".
//
// # 🔴 And why neither is a GATE
//
// Every entry in `components` makes the whole signal not-ready and pulls the process out of its Service
// endpoints. Neither of these may do that: a broken forge adapter is a real problem for the customers
// using that forge and is not a reason to take the platform down for everyone else, and a sweep that
// has not run yet is a job to look at rather than an outage. Both are asserted to be at the TOP LEVEL,
// and the aggregate status is asserted to stay `ready` while one of them is escalated — which is the
// assertion that would fail if somebody "helpfully" moved them into `components`.

// p32Readyz drives /readyz and returns the decoded document.
//
// Named for this phase rather than reusing `readyzBody` from errorreporting_test.go: that helper is
// this file's neighbour and adding a second caller to it would make either file's edits able to break
// the other's assertions for reasons nothing in it mentions.
func p32Readyz(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /readyz (%d): %v\n%s", rec.Code, err, rec.Body.String())
	}
	return body
}

// TestRetentionsLastSuccessIsReadableFromTheHealthEndpoint is §7.6's second half.
func TestRetentionsLastSuccessIsReadableFromTheHealthEndpoint(t *testing.T) {
	ctx := context.Background()
	store := sourceingest.NewMemStore()
	now := int64(1_700_000)
	job := sourceingest.NewRetentionJob(sourceingest.RetentionConfig{
		Snapshots: store,
		NowMS:     func() int64 { return now },
	})
	metrics := sourceingest.NewIngestMetrics()

	s := New(nil, config.Config{})
	s.SetSourceIngestHealth(metrics, job)

	// Before any run. 🔴 `never_run` is a DISTINCT state: a process that started ninety seconds ago has
	// not swept yet and nothing is wrong, while one up for a day and never swept has a dead goroutine.
	body := p32Readyz(t, s)
	retention, ok := body["source_retention"].(map[string]any)
	if !ok {
		t.Fatalf("/readyz carries no `source_retention` — the job can run perfectly and nothing would "+
			"say so. Body: %v", body)
	}
	if retention["status"] != sourceingest.RetentionNeverRun {
		t.Errorf("status = %v before any sweep, want %q", retention["status"], sourceingest.RetentionNeverRun)
	}
	if retention["last_success_ms"] != float64(0) {
		t.Errorf("last_success_ms = %v before any sweep, want 0", retention["last_success_ms"])
	}
	// The window and the interval are published so a monitor can compute "should it have run by now"
	// without hard-coding this deployment's configuration.
	if retention["window_hours"] != float64(int64(sourceingest.DefaultCloneRetention.Hours())) {
		t.Errorf("window_hours = %v, want %d", retention["window_hours"], int64(sourceingest.DefaultCloneRetention.Hours()))
	}
	if retention["interval_seconds"] == float64(0) {
		t.Errorf("interval_seconds = 0 — a monitor cannot tell whether a sweep is overdue")
	}

	// After a sweep that deleted NOTHING — which is the normal result, and the reason the signal cannot
	// be "did it delete anything".
	if _, err := job.RunOnce(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	body = p32Readyz(t, s)
	retention = body["source_retention"].(map[string]any)
	if retention["status"] != sourceingest.RetentionReady {
		t.Errorf("status = %v after a successful sweep, want %q", retention["status"], sourceingest.RetentionReady)
	}
	if retention["last_success_ms"] != float64(now) {
		t.Errorf("last_success_ms = %v, want %d — this is the ONE value that distinguishes a live job "+
			"from a dead one, because zero deletions is the normal outcome", retention["last_success_ms"], now)
	}
	if retention["deleted"] != float64(0) {
		t.Errorf("deleted = %v, want 0", retention["deleted"])
	}

	// 🔴 And it is NOT a gate: the document's own status stays `ready`.
	if body["status"] != "ready" {
		t.Errorf("/readyz status = %v — the retention job must not be able to take the deployment down", body["status"])
	}
	if comps, ok := body["components"].(map[string]any); ok {
		if _, present := comps["source_retention"]; present {
			t.Error("source_retention is in `components`, which makes it a GATE — a sweep that has not " +
				"run yet would pull the process out of its Service endpoints")
		}
	}
}

// TestIngestMetricsAreReadableFromTheHealthEndpointBrokenOutPerForge is §7.7's second half.
//
// 🔴 It asserts the BREAKDOWN exists on the endpoint, because the aggregate is what gets built if
// nobody checks — and an aggregate is exactly what a dashboard would render if the breakdown were
// computed and not published.
func TestIngestMetricsAreReadableFromTheHealthEndpointBrokenOutPerForge(t *testing.T) {
	metrics := sourceingest.NewIngestMetrics()
	job := sourceingest.NewRetentionJob(sourceingest.RetentionConfig{Snapshots: sourceingest.NewMemStore()})
	s := New(nil, config.Config{})
	s.SetSourceIngestHealth(metrics, job)

	body := p32Readyz(t, s)
	ingest, ok := body["source_ingest"].(map[string]any)
	if !ok {
		t.Fatalf("/readyz carries no `source_ingest` — clone duration, bytes and failure cause would be "+
			"computed and unreadable. Body: %v", body)
	}
	perForge, ok := ingest["per_forge"].([]any)
	if !ok {
		t.Fatalf("`source_ingest.per_forge` is not a list: %v — §7.7 asks for the breakdown, and an "+
			"aggregate-only document is what gets built if nobody checks", ingest["per_forge"])
	}
	if len(perForge) != len(sourceingest.Forges()) {
		t.Fatalf("per_forge has %d entries with no traffic, want %d — a forge that has never been used "+
			"must appear at zero, or its absence reads as `everyone uses the other one`",
			len(perForge), len(sourceingest.Forges()))
	}
	for _, entry := range perForge {
		e := entry.(map[string]any)
		byCause, ok := e["by_cause"].(map[string]any)
		if !ok {
			t.Fatalf("%v has no by_cause breakdown", e["forge"])
		}
		if len(byCause) != len(sourceingest.CloneCauses()) {
			t.Errorf("%v.by_cause has %d keys, want %d — an absent key and a zero are indistinguishable "+
				"to a dashboard that renders whatever it finds", e["forge"], len(byCause), len(sourceingest.CloneCauses()))
		}
	}
	// The aggregate is PRESENT and is never the only figure.
	if _, ok := ingest["total"].(map[string]any); !ok {
		t.Errorf("`source_ingest.total` is missing — an operator does want one number for a tile")
	}
	// The escalation threshold is published so a monitor need not hard-code it.
	if ingest["escalate_after"] != float64(sourceingest.EscalateAfterConsecutiveFailures) {
		t.Errorf("escalate_after = %v, want %d", ingest["escalate_after"], sourceingest.EscalateAfterConsecutiveFailures)
	}

	// 🔴 One forge failing repeatedly ESCALATES and NAMES ITSELF — and the deployment stays ready.
	for i := 0; i < sourceingest.EscalateAfterConsecutiveFailures; i++ {
		metrics.Observe(sourceingest.ForgeBitbucket, sourceingest.CauseCredentialRejected, 0, 0)
	}
	metrics.Observe(sourceingest.ForgeGitHub, "", 100, 0)

	body = p32Readyz(t, s)
	ingest = body["source_ingest"].(map[string]any)
	escalated, _ := ingest["escalated"].([]any)
	if len(escalated) != 1 || escalated[0] != "bitbucket" {
		t.Errorf("escalated = %v, want exactly [bitbucket] — `something is escalated` without a subject "+
			"sends an operator to read three dashboards to learn what this response already knew", escalated)
	}
	if body["status"] != "ready" {
		t.Errorf("/readyz status = %v while one forge is escalated — a broken adapter must not take the "+
			"deployment down for tenants who do not use that forge", body["status"])
	}
	if comps, ok := body["components"].(map[string]any); ok {
		if _, present := comps["source_ingest"]; present {
			t.Error("source_ingest is in `components`, which makes it a GATE")
		}
	}
}

// TestADeploymentThatDoesNotCloneReportsNeitherSignal.
//
// Absent rather than a zeroed document, which is the same posture `secrets_source` and `billing_rollout`
// take: a deployment that wired no clone path HAS no ingest health, and saying so by omission beats
// inventing a status for it. A monitor alerting on `source_retention.status != ready` must not fire on
// every deployment that does not offer connections.
func TestADeploymentThatDoesNotCloneReportsNeitherSignal(t *testing.T) {
	s := New(nil, config.Config{})
	body := p32Readyz(t, s)
	if _, present := body["source_ingest"]; present {
		t.Error("a deployment with no clone path reports `source_ingest`; absence is the honest answer")
	}
	if _, present := body["source_retention"]; present {
		t.Error("a deployment with no clone path reports `source_retention`; absence is the honest answer")
	}
}
