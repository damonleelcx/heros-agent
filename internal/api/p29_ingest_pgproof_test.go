//go:build pgproof

// P29 §3.7 — the FOUR-LAYER assertion on both opt-in ingest paths.
//
// # 🔴 A 2xx is not evidence of a write
//
// `e2e-acceptance-live-events` exists because of a specific, repeated failure: a handler answers 201, the
// test asserts 201, and nothing ever checks that a row appeared. Every layer below has been the one that
// was broken while the layers above it were green:
//
//  1. request accepted     — the handler ran. Says nothing about storage.
//  2. row present          — the INSERT landed. Says nothing about whether the read finds it.
//  3. read model returns it — the store's SELECT column list matches what was written. This is the layer
//     that breaks when a column is added to the write and forgotten in the read,
//     and it breaks SILENTLY: the row is there, the field comes back empty, and
//     the surface renders "not reported" for data the customer definitely sent.
//  4. surface renders it   — the console asks for it and gets it.
//
// Layers 1–3 are asserted here against a real Postgres. Layer 4 is asserted on the deployed cluster in
// §8.3, because a rendered page is the only honest evidence for it and this package cannot produce one.
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

func p29DB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// authed wraps a handler so the request carries a principal, exactly as auth.Compose would.
func p29Authed(h http.Handler, tenant string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithPrincipal(r.Context(), auth.Principal{TenantID: tenant})
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestP29StructureIngestLandsAndReadsBackAllFourWays(t *testing.T) {
	db := p29DB(t, "p29_ingest_ir")
	store := linkingest.NewPGWorkflowIRStore(db)

	s := New(nil, config.Config{})
	s.MountWorkflowIR(store)

	payload := runlink.WorkflowIRPayload{
		ContractVersion: runlink.WorkflowIRContractVersion,
		WorkflowID:      "wf-1", SourceRevision: "rev-1", IRVersion: "v1",
		CoverageVersion: "cov-deadbeef",
		Nodes: []runlink.WireIRNode{{
			NodeID: "n_1", Symbol: "handle", File: "a.py", LineStart: 3, LineEnd: 3,
			Provider: "openai", ModelID: "gpt-4o", ToolCount: 2,
			Language: "python",
			AxisVerdicts: []runlink.WireAxisVerdict{
				{Axis: "model", Status: runlink.VerdictApplies},
				{Axis: "memory", Status: runlink.VerdictRefused, Cause: "call-site-cannot-carry-it"},
			},
		}},
		Edges: []runlink.WireIREdge{},
	}
	body, _ := json.Marshal(payload)

	// ── LAYER 1: the request is accepted ────────────────────────────────────────────────────────
	req := httptest.NewRequest(http.MethodPost, runlink.WorkflowIRPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p29Authed(s.Mux, "t-1").ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest answered %d: %s", rec.Code, rec.Body.String())
	}

	// ── LAYER 2: the row is present, with the new column populated ──────────────────────────────
	var coverage sql.NullString
	var nodesJSON []byte
	err := db.QueryRow(`SELECT coverage_version, nodes_json FROM workflow_ir
	                     WHERE tenant_id='t-1' AND workflow_id='wf-1' AND source_revision='rev-1'`).
		Scan(&coverage, &nodesJSON)
	if err != nil {
		t.Fatalf("LAYER 2: the handler answered 201 and NO ROW EXISTS: %v", err)
	}
	if !coverage.Valid || coverage.String != "cov-deadbeef" {
		t.Errorf("LAYER 2: coverage_version stored as %v, want cov-deadbeef. The handler answered 201 "+
			"either way — which is why a 2xx is not evidence of a write.", coverage)
	}
	// Decoded rather than string-matched: PostgreSQL stores JSONB in its own normalised form and returns
	// it with its own whitespace and key order, so a byte comparison here asserts a fact about libpq's
	// formatting instead of about what was stored.
	var storedNodes []runlink.WireIRNode
	if err := json.Unmarshal(nodesJSON, &storedNodes); err != nil {
		t.Fatalf("LAYER 2: the stored nodes document does not decode: %v\n%s", err, nodesJSON)
	}
	if len(storedNodes) != 1 || storedNodes[0].Language != "python" {
		t.Errorf("LAYER 2: the stored nodes document carries no language: %s", nodesJSON)
	}
	if len(storedNodes) != 1 || len(storedNodes[0].AxisVerdicts) != 2 {
		t.Errorf("LAYER 2: the stored nodes document carries no axis verdicts: %s", nodesJSON)
	}

	// ── LAYER 3: the READ MODEL returns it ──────────────────────────────────────────────────────
	//
	// 🔴 This is the layer that breaks silently. A column added to the write and forgotten in the read's
	// SELECT list produces exactly this: the row is there, the field comes back empty, and every surface
	// renders `not reported` for data the customer definitely sent — a wrong claim, sourced from our own
	// omission, with nothing failing anywhere.
	got, ok, err := store.Latest("t-1", "wf-1")
	if err != nil {
		t.Fatalf("LAYER 3: read model errored: %v", err)
	}
	if !ok {
		t.Fatal("LAYER 3: the read model reports NO STRUCTURE for a row that is in the table")
	}
	if got.CoverageVersion != "cov-deadbeef" {
		t.Errorf("LAYER 3: the read model returned coverage version %q, and the table holds %q",
			got.CoverageVersion, coverage.String)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("LAYER 3: read model returned %d node(s), want 1", len(got.Nodes))
	}
	n := got.Nodes[0]
	if n.Language != "python" {
		t.Errorf("LAYER 3: the read model lost the node's language (%q)", n.Language)
	}
	if len(n.AxisVerdicts) != 2 {
		t.Fatalf("LAYER 3: the read model returned %d verdict(s), want 2", len(n.AxisVerdicts))
	}
	if n.AxisVerdicts[1].Cause != "call-site-cannot-carry-it" {
		t.Errorf("LAYER 3: the refusal cause did not survive the round trip: %+v", n.AxisVerdicts[1])
	}
}

func TestP29ReceiptIngestLandsAndReadsBackAllFourWays(t *testing.T) {
	db := p29DB(t, "p29_ingest_receipt")
	store := linkingest.NewPGTransformReceiptStore(db)

	s := New(nil, config.Config{})
	s.MountTransformReceipts(store)

	r := runlink.TransformReceipt{
		ContractVersion: runlink.TransformReceiptContractVersion,
		ConfigHash:      "h-1", SourceRevision: "rev-1", WorkflowID: "wf-1",
		ToolVersion: "0.11.0", CoverageVersion: "cov-deadbeef", Status: "applied",
		NodeOutcomes: []runlink.WireNodeOutcome{
			{NodeID: "n_1", Outcome: runlink.OutcomeApplied},
			{NodeID: "n_2", Outcome: runlink.OutcomeRefused, Cause: "call-site-cannot-carry-it"},
		},
		FilesChanged: 2, LinesAdded: 7, LinesRemoved: 3,
	}
	body, _ := json.Marshal(r)

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, runlink.TransformReceiptPath, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		p29Authed(s.Mux, "t-1").ServeHTTP(rec, req)
		return rec
	}

	// LAYER 1
	if rec := post(); rec.Code != http.StatusCreated {
		t.Fatalf("ingest answered %d: %s", rec.Code, rec.Body.String())
	}
	// Twice — §3.6's idempotence, asserted through the HANDLER rather than through the store, because
	// that is the path a retrying CI job actually takes.
	if rec := post(); rec.Code != http.StatusCreated {
		t.Fatalf("a second identical receipt answered %d: %s", rec.Code, rec.Body.String())
	}

	// LAYER 2
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM linked_transform WHERE tenant_id='t-1'`).Scan(&rows); err != nil {
		t.Fatalf("LAYER 2: %v", err)
	}
	if rows != 1 {
		t.Errorf("LAYER 2: two transmissions of one receipt left %d row(s), want 1", rows)
	}

	// LAYER 3
	got, ok, err := store.Get("t-1", "h-1", "rev-1")
	if err != nil {
		t.Fatalf("LAYER 3: %v", err)
	}
	if !ok {
		t.Fatal("LAYER 3: the read model reports NO RECEIPT for a row that is in the table")
	}
	if got.FilesChanged != 2 || got.LinesAdded != 7 || got.LinesRemoved != 3 {
		t.Errorf("LAYER 3: the diffstat did not survive: %+v", got)
	}
	if len(got.NodeOutcomes) != 2 || got.NodeOutcomes[1].Cause != "call-site-cannot-carry-it" {
		t.Errorf("LAYER 3: the node outcomes did not survive: %+v", got.NodeOutcomes)
	}
	if got.CoverageVersion != "cov-deadbeef" {
		t.Errorf("LAYER 3: coverage version did not survive: %q", got.CoverageVersion)
	}

	// And the tenant list is scoped.
	list, err := store.ListForTenant("t-1", 10)
	if err != nil || len(list) != 1 {
		t.Errorf("LAYER 3: ListForTenant returned %d row(s), err=%v", len(list), err)
	}
	other, err := store.ListForTenant("t-2", 10)
	if err != nil || len(other) != 0 {
		t.Errorf("LAYER 3: a SECOND organization's list returned %d row(s) — it must be empty", len(other))
	}
}

// An UNREPORTED coverage version reads back as absent, through the whole path — the platform never
// substitutes its own.
func TestP29AnUnreportedCoverageVersionStaysAbsentThroughTheWholePath(t *testing.T) {
	db := p29DB(t, "p29_absent_path")
	store := linkingest.NewPGWorkflowIRStore(db)
	s := New(nil, config.Config{})
	s.MountWorkflowIR(store)

	// A payload with NO coverage version — what a CLI built before this change sends.
	payload := runlink.WorkflowIRPayload{
		ContractVersion: runlink.WorkflowIRContractVersion,
		WorkflowID:      "wf-old", SourceRevision: "rev-old", IRVersion: "v1",
		Nodes: []runlink.WireIRNode{{NodeID: "n_1", Symbol: "s", File: "a.py"}},
		Edges: []runlink.WireIREdge{},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, runlink.WorkflowIRPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p29Authed(s.Mux, "t-1").ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("an older client's payload was REJECTED (%d): %s\n"+
			"Every field this change added is optional, and a CLI that predates it must keep working.",
			rec.Code, rec.Body.String())
	}

	var coverage sql.NullString
	if err := db.QueryRow(`SELECT coverage_version FROM workflow_ir WHERE workflow_id='wf-old'`).
		Scan(&coverage); err != nil {
		t.Fatal(err)
	}
	if coverage.Valid {
		t.Errorf("an unreported coverage version was stored as %q. NULL is the only honest value, and the "+
			"STALE label the projection renders is computed from exactly this column.", coverage.String)
	}
	got, ok, _ := store.Latest("t-1", "wf-old")
	if !ok || got.CoverageVersion != "" {
		t.Errorf("the read model returned coverage version %q for a payload that reported none",
			got.CoverageVersion)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Language != "" || len(got.Nodes[0].AxisVerdicts) != 0 {
		t.Errorf("an older client's node came back with fields it never sent: %+v", got.Nodes)
	}
}
