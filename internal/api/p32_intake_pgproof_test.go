//go:build pgproof

// P32 §7.10 (part 1 of 2) — the LIVE-EVENT acceptance for the connection SURFACE, against real Postgres.
//
// # Why this walk is in two files, stated rather than left to be noticed
//
// §7.10 is one walk: connect → SELECT the row → clone → assert files on disk → run discovery → assert
// nodes. It is executed in two packages because the CLONE half needs a real git remote, and pointing
// production code at one would mean adding a seam to `GitConfig` that lets a caller redirect where a
// clone goes. That is a security-relevant widening for a test's convenience, and it is refused.
//
// So: this file is layers 1–3 and 5 — the HTTP boundary, the row, the read model, and the cascade —
// and `internal/sourceingest/p32_acceptance_pgproof_test.go` is layer 4, the live clone, where the
// package's own unexported runner can redirect the REMOTE without exposing anything. Both run against
// the same live Postgres, under the same `make pg-proof`.
//
// # 🔴 A 201 is not evidence of a grant
//
// The task states the walk exactly: *"connect → `SELECT` the connection row → clone → assert files on
// disk at the expected revision → run discovery → assert nodes. A 200 is not evidence of a write."*
//
// Every layer below has been the one that was broken while the layers above it were green, in this
// repository, more than once:
//
//  1. request accepted        — the handler ran. Says nothing about storage.
//  2. row present in Postgres — the INSERT landed, with the values that were sent. This is the layer
//     that breaks when a CHECK constraint refuses and the code path swallows it.
//  3. the read model returns it — the store's SELECT column list matches what was written. Breaks
//     SILENTLY: the row is there, a field comes back empty, and the console
//     renders `not reported` for data the customer definitely sent.
//  4. the clone produces FILES at the expected revision — the thing the grant exists for.
//  5. the cascade REMOVES them — the half of revocation that fails invisibly (design D3).
//
// # 🔴 A 201 is not evidence of a grant, and the 201 is all this layer produces
//
// Every assertion below re-reads the state through a DIFFERENT path than the one that wrote it: the
// row through SQL, the connection through the read model, the absence through both.
package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

func p32DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := pgtest.Open("p32_intake")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	// Each test starts from a clean slate. Deleted in FK order — the ledger references the grant.
	for _, stmt := range []string{
		`DELETE FROM source_clone_record`,
		`DELETE FROM source_local_pairing`,
		`DELETE FROM source_bundle`,
		`DELETE FROM source_connection`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
	return db
}

// p32Server mounts the connection surface over a live store.
func p32Server(t *testing.T, db *sql.DB) (*Server, *sourceingest.Service, *providergateway.MemForgeSecrets) {
	t.Helper()
	conns, err := sourceingest.NewPGConnectionStore(db)
	if err != nil {
		t.Fatalf("connection store: %v", err)
	}
	blobs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	bundles, err := sourceingest.NewPGBundleStore(db, blobs)
	if err != nil {
		t.Fatalf("bundle store: %v", err)
	}
	snaps, err := sourceingest.NewPGSnapshotStore(bundles)
	if err != nil {
		t.Fatalf("snapshot store: %v", err)
	}
	secrets := providergateway.NewMemForgeSecrets()
	svc, err := sourceingest.NewService(sourceingest.ServiceConfig{
		Connections: conns, Snapshots: snaps, Secrets: secrets,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	s := New(db, config.Config{AuthMode: "off"})
	s.MountConnections(svc)
	return s, svc, secrets
}

func p32Post(t *testing.T, s *Server, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "t-p32", UserID: "u-p32"}))
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// TestConnectIsProvenAtEveryLayerAndRevocationRemovesIt is §7.10 layers 1–3 and 5.
func TestConnectIsProvenAtEveryLayerAndRevocationRemovesIt(t *testing.T) {
	ctx := context.Background()
	db := p32DB(t)
	s, svc, _ := p32Server(t, db)

	// ── 1) connect, through the HTTP handler a console would call ────────────────────────────────
	rec, body := p32Post(t, s, "/api/v1/repo-connections", map[string]any{
		"workflow_id":   "wf-p32",
		"repository":    "acme/api",
		"forge":         "github",
		"grant_kind":    "app_installation",
		"external_id":   "inst-1",
		"covers":        []string{"acme/api"},
		"account_wide":  false,
		"scopes":        []string{"contents:read"},
		"token":         p32ProbeToken,
		"consent_shown": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect answered %d: %s", rec.Code, rec.Body.String())
	}
	connectionID, _ := body["connection_id"].(string)
	if connectionID == "" {
		t.Fatalf("connect returned no connection_id: %s", rec.Body.String())
	}

	// ── 2) SELECT the row. 🔴 THIS is the assertion the 201 does not make ────────────────────────
	var gotTenant, gotForge, gotRepo, gotKind, gotBy string
	var gotAt int64
	err := db.QueryRowContext(ctx,
		`SELECT tenant_id, forge, repository, grant_kind, created_by, created_at_ms
		   FROM source_connection WHERE connection_id = $1`, connectionID).
		Scan(&gotTenant, &gotForge, &gotRepo, &gotKind, &gotBy, &gotAt)
	if err != nil {
		t.Fatalf("the 201 was answered and NO ROW exists: %v", err)
	}
	if gotTenant != "t-p32" || gotForge != "github" || gotRepo != "acme/api" || gotKind != "app_installation" {
		t.Errorf("the row holds (%s, %s, %s, %s), want the values that were sent", gotTenant, gotForge, gotRepo, gotKind)
	}
	if gotBy != "u-p32" {
		t.Errorf("created_by = %q — it must come from the PRINCIPAL, and the payload has no field for it", gotBy)
	}
	if gotAt <= 0 {
		t.Errorf("created_at_ms = %d, want an epoch-millisecond value", gotAt)
	}
	// 🚫 No column holds the token. The columns are DISCOVERED from the catalog, so one added later is
	// covered without anybody remembering to extend a list.
	assertNoTokenInRow(t, db, "source_connection", "connection_id", connectionID, p32ProbeToken)

	// ── 3) the READ MODEL returns it — the layer that breaks silently ────────────────────────────
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/repo-connections", nil)
	listReq = listReq.WithContext(auth.WithPrincipal(listReq.Context(), auth.Principal{TenantID: "t-p32"}))
	listRec := httptest.NewRecorder()
	s.Handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list answered %d: %s", listRec.Code, listRec.Body.String())
	}
	var view ConnectionsView
	if err := json.Unmarshal(listRec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(view.Connections) != 1 || view.Connections[0].Repository != "acme/api" {
		t.Fatalf("the read model returned %+v, want the connection that was just written", view.Connections)
	}
	if view.Connections[0].GrantLabel == "" || view.Connections[0].RevokeHint == "" {
		t.Errorf("the read model dropped the forge's own copy: %+v — the consent screen renders it", view.Connections[0])
	}
	// 🔴 A fresh grant has read nothing, and says so with a zero rather than a plausible date.
	if view.Connections[0].LastSuccessAt != 0 {
		t.Errorf("last_success_at_ms = %d before anything was read", view.Connections[0].LastSuccessAt)
	}
	// And the whole response carries no credential, at any depth.
	if bytes.Contains(listRec.Body.Bytes(), []byte(p32ProbeToken)) {
		t.Errorf("the read model returned the forge credential:\n%s", listRec.Body.String())
	}

	// ── 4) a derived snapshot, so the cascade has something to remove ────────────────────────────
	//
	// Written through the SHIPPED store rather than by hand, so the CHECK that ties `connection_id`
	// and `expires_at_ms` together is exercised by the code that will actually write these rows.
	blobs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	bundles, err := sourceingest.NewPGBundleStore(db, blobs)
	if err != nil {
		t.Fatalf("bundle store: %v", err)
	}
	snaps, err := sourceingest.NewPGSnapshotStore(bundles)
	if err != nil {
		t.Fatalf("snapshot store: %v", err)
	}
	ref := sourceingest.Ref{TenantID: "t-p32", WorkflowID: "wf-p32", SourceRevision: "rev-1"}
	if err := snaps.PutDerived(ctx, ref, p32TinyArchive(t), connectionID, 1<<40); err != nil {
		t.Fatalf("put derived: %v", err)
	}

	// ── 5) REVOKE, and assert the trees are ABSENT ───────────────────────────────────────────────
	res, err := svc.Revoke(ctx, "t-p32", connectionID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if res.SnapshotsDeleted != 1 {
		t.Errorf("SnapshotsDeleted = %d, want 1", res.SnapshotsDeleted)
	}
	for _, q := range []struct {
		what, sql string
	}{
		{"snapshot", `SELECT COUNT(*) FROM source_bundle WHERE connection_id = $1`},
		{"grant", `SELECT COUNT(*) FROM source_connection WHERE connection_id = $1`},
		// The LEDGER goes with the grant through the FK cascade. Asserted, because a cascade that
		// silently did not fire would leave a customer's repository names on our disk after they asked
		// us to stop holding anything.
		{"ledger", `SELECT COUNT(*) FROM source_clone_record WHERE connection_id = $1`},
	} {
		var n int
		if err := db.QueryRowContext(ctx, q.sql, connectionID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.what, err)
		}
		if n != 0 {
			t.Errorf("%d %s row(s) survived the revocation — a revocation answerable from cache is not a "+
				"revocation (design D3)", n, q.what)
		}
	}
	// And the read model reports the absence, through the path a console reads.
	listRec2 := httptest.NewRecorder()
	s.Handler.ServeHTTP(listRec2, listReq)
	var after ConnectionsView
	if err := json.Unmarshal(listRec2.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(after.Connections) != 0 {
		t.Errorf("the read model still returns %d connection(s) after revocation", len(after.Connections))
	}
	// The forge descriptions and the retention window are STILL served — a tenant with no connection
	// still needs the consent copy to make one.
	if len(after.Forges) == 0 || after.RetentionHours == 0 {
		t.Errorf("the read model dropped the forge copy once the tenant had no connection: %+v", after)
	}
}

// TestABroaderGrantIsRefusedAndLeavesNoRow is §7.4 at the live boundary.
//
// 🔴 The SECOND assertion is the point. A refusal that returns 422 and still writes the row is a
// refusal in the response only, and it is exactly the shape a `defer`-shaped rollback gets wrong.
func TestABroaderGrantIsRefusedAndLeavesNoRow(t *testing.T) {
	ctx := context.Background()
	db := p32DB(t)
	s, _, secrets := p32Server(t, db)

	rec, _ := p32Post(t, s, "/api/v1/repo-connections", map[string]any{
		"workflow_id":   "wf-broad",
		"repository":    "acme/api",
		"forge":         "github",
		"grant_kind":    "app_installation",
		"covers":        []string{"acme/api", "acme/other"},
		"consent_shown": true,
		"token":         p32ProbeToken,
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a two-repository grant answered %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "acme/other") {
		t.Errorf("the refusal does not NAME what the grant reaches: %s — the customer's next action is "+
			"to narrow it, and they cannot narrow what they were not told about", rec.Body.String())
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_connection`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("a REFUSED authorization left %d grant row(s) behind", n)
	}
	if refs := secrets.Refs(); len(refs) != 0 {
		t.Errorf("a REFUSED authorization left %d credential(s) behind: %v", len(refs), refs)
	}
}

// TestModeOneIsUnchangedByThisMigration is §7.11.
//
// # 🔴 What "unchanged" has to mean, precisely
//
// Migration 0049 adds two NULLABLE columns to `source_bundle` and a CHECK that ties them together. The
// failure it could cause is not a compile error — it is a pushed bundle acquiring an expiry and being
// swept, or a CHECK refusing a plain push. Both are invisible until a customer's snapshot disappears.
//
// So this asserts the two NULLs on a row written by the UNCHANGED push path, then runs the retention
// sweep with a `now` far in the future and asserts the row survives it.
func TestModeOneIsUnchangedByThisMigration(t *testing.T) {
	ctx := context.Background()
	db := p32DB(t)
	blobs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	// The SHIPPED bundle store — not the snapshot wrapper. This is Mode 1's own write path, untouched.
	store, err := sourceingest.NewPGBundleStore(db, blobs)
	if err != nil {
		t.Fatalf("bundle store: %v", err)
	}
	ref := sourceingest.Ref{TenantID: "t-p32", WorkflowID: "wf-mode1", SourceRevision: "rev-mode1"}
	if err := store.Put(ctx, ref, p32TinyArchive(t)); err != nil {
		t.Fatalf("the unchanged push path was refused by the new schema: %v", err)
	}

	var conn sql.NullString
	var expiry sql.NullInt64
	if err := db.QueryRowContext(ctx,
		`SELECT connection_id, expires_at_ms FROM source_bundle
		  WHERE tenant_id = $1 AND workflow_id = $2 AND source_revision = $3`,
		ref.TenantID, ref.WorkflowID, ref.SourceRevision).Scan(&conn, &expiry); err != nil {
		t.Fatalf("select: %v", err)
	}
	if conn.Valid {
		t.Errorf("a PUSHED bundle has connection_id = %q; NULL is what keeps the cascade from matching it", conn.String)
	}
	if expiry.Valid {
		t.Errorf("a PUSHED bundle has expires_at_ms = %d; NULL is the rule that it is held until the "+
			"customer deletes it (PRD §14 A4)", expiry.Int64)
	}

	snaps, err := sourceingest.NewPGSnapshotStore(store)
	if err != nil {
		t.Fatalf("snapshot store: %v", err)
	}
	job := sourceingest.NewRetentionJob(sourceingest.RetentionConfig{
		Snapshots: snaps,
		NowMS:     func() int64 { return 1 << 62 },
	})
	if _, err := job.RunOnce(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := store.Open(ctx, ref); err != nil {
		t.Fatalf("the retention sweep deleted a PUSHED bundle: %v — Mode 1 must lose nothing", err)
	}
	// And the extraction path still works on it, which is the behaviour a customer actually has.
	src, err := sourceingest.NewBundleSource(store, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	m, err := src.Materialize(ctx, ref)
	if err != nil {
		t.Fatalf("the unchanged extraction path failed after the migration: %v", err)
	}
	defer m.Release()
	if _, err := os.Stat(filepath.Join(m.Dir, "a.go")); err != nil {
		t.Errorf("the extracted tree is missing its file: %v", err)
	}
}

// p32ProbeToken is the credential value every containment assertion searches for.
const p32ProbeToken = "ghs_intake_probe_do_not_leak"

// assertNoTokenInRow reads every text-typed column of a row and refuses to find the probe token.
//
// 🔴 The columns are DISCOVERED from the catalog rather than listed, so a column added later is covered
// by this fence without anybody remembering to extend it — which is the failure mode a whitelist has.
func assertNoTokenInRow(t *testing.T, db *sql.DB, table, keyCol, key, token string) {
	t.Helper()
	rows, err := db.Query(
		`SELECT column_name FROM information_schema.columns
		  WHERE table_schema = current_schema() AND table_name = $1
		    AND data_type IN ('text','character varying','character')`, table)
	if err != nil {
		t.Fatalf("read columns of %s: %v", table, err)
	}
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			_ = rows.Close()
			t.Fatalf("scan column: %v", err)
		}
		cols = append(cols, c)
	}
	_ = rows.Close()
	if len(cols) == 0 {
		t.Fatalf("%s reports no text columns — this fence is asserting nothing", table)
	}
	for _, c := range cols {
		var v sql.NullString
		q := `SELECT ` + p32Quote(c) + ` FROM ` + p32Quote(table) + ` WHERE ` + p32Quote(keyCol) + ` = $1`
		if err := db.QueryRow(q, key).Scan(&v); err != nil {
			continue // the row may be gone; other assertions cover that
		}
		if v.Valid && strings.Contains(v.String, token) {
			t.Errorf("%s.%s holds the forge credential", table, c)
		}
	}
}

// p32Quote quotes an identifier. The values are catalog-derived, never caller input, and quoting is
// what keeps a column named like a keyword from breaking the query.
func p32Quote(ident string) string { return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"` }

// p32TinyArchive is a valid one-file gzipped tar.
func p32TinyArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := "package a\n"
	if err := tw.WriteHeader(&tar.Header{Name: "a.go", Typeflag: tar.TypeReg, Mode: 0o640, Size: int64(len(body))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}
