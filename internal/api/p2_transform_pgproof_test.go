//go:build pgproof

// Live-Postgres proof that the review surface tells a reviewer what the gate PROVED (ADR-003
// decision 3).
//
// # Why this test is here and not a unit test
//
// The claim is not "the struct has a field". It is: a row written by the apply path comes back out of
// the API, over HTTP, carrying the strength — because that response IS what the diff-review UI reads,
// and decision 3 is that "a reviewer looking at a diff must be able to see, WITHOUT ASKING, whether a
// compiler stood behind it".
//
// Every link is real: the real migration chain, the real store, the real handler, a real request. A
// unit test with a hand-built Record would prove the mapping and skip the two places this can
// actually break — the column not being read, and the field not being serialized.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/worktree"
	_ "github.com/lib/pq"
)

var transformDB *sql.DB

func openTransformDB(t *testing.T) *sql.DB {
	t.Helper()
	if transformDB != nil {
		return transformDB
	}
	db, err := pgtest.Open("proof_api_transform")
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	for _, f := range []string{"0001_p0_lineage.up.sql", "0002_p2_registries.up.sql",
		"0003_p2_variant_spec.up.sql", "0004_p2_transform.up.sql",
		"0007_p2_verification_strength.up.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	transformDB = db
	return db
}

// TestPG_TransformView_TellsTheReviewerWhatTheGateProved.
//
// The two rows differ in ONE thing, and it is the thing that matters: both are `built`, and only one
// had a type checker behind it. Before ADR-003 the API could not tell them apart — which is precisely
// how a reviewer ends up extending trust earned by the Go path to a Python diff that never earned it.
func TestPG_TransformView_TellsTheReviewerWhatTheGateProved(t *testing.T) {
	ctx := context.Background()
	db := openTransformDB(t)
	blobs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	store := worktree.NewStore(db, blobs)

	s := New(nil, config.Config{})
	s.MountP2(P2Stores{Transforms: store})

	for _, tc := range []struct {
		name        string
		hash        string
		strength    worktree.Strength
		wantReview  bool
		wantBadgeIs string
	}{
		{"go, type-checked", cfgTypeChecked, worktree.StrengthTypeChecked, false, "type-checked"},
		{"python, syntax-checked", cfgSyntaxChecked, worktree.StrengthSyntaxChecked, true, "syntax-checked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedTransformLineage(t, db, tc.hash)
			if err := store.Put(ctx, &worktree.Applied{
				ConfigHash: tc.hash, SourceRevision: "rev1", Dir: "/wt/x", Branch: "variant/x",
				Commit: "c0ffee", Diff: []byte("--- a/x\n+++ b/x\n-a\n+b\n"),
				Status: worktree.StatusBuilt, Strength: tc.strength,
			}, nil); err != nil {
				t.Fatalf("Put: %v", err)
			}

			rec := httptest.NewRecorder()
			s.Handler.ServeHTTP(rec, httptest.NewRequest("GET",
				"/api/v1/transforms/"+tc.hash+"/rev1", nil))
			if rec.Code != 200 {
				t.Fatalf("GET transform: %d %s", rec.Code, rec.Body)
			}
			var got struct {
				Status               string `json:"status"`
				VerificationStrength string `json:"verification_strength"`
				RequiresHumanReview  bool   `json:"requires_human_review"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}

			// Both rows say `built`. That is the point: `built` is not the answer to the reviewer's
			// question, and on its own it never was.
			if got.Status != "built" {
				t.Fatalf("status = %q, want built", got.Status)
			}
			if got.VerificationStrength != tc.wantBadgeIs {
				t.Errorf("verification_strength = %q, want %q — the response the diff-review UI reads "+
					"does not say what the gate proved", got.VerificationStrength, tc.wantBadgeIs)
			}
			if got.RequiresHumanReview != tc.wantReview {
				t.Errorf("requires_human_review = %v, want %v (ADR-003 decision 5)",
					got.RequiresHumanReview, tc.wantReview)
			}
		})
	}
}

func seedTransformLineage(t *testing.T, db *sql.DB, hash string) {
	t.Helper()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		  VALUES ('wf','local://t','abc','go','1.0.0') ON CONFLICT DO NOTHING`, nil},
		{`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v','wf','base')
		  ON CONFLICT DO NOTHING`, nil},
		{`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		  VALUES ($1,'v','wf','1.0.0','{}') ON CONFLICT DO NOTHING`, []any{hash}},
		{`INSERT INTO variant_spec (config_hash, source_revision, spec_json) VALUES ($1,'rev1','{}')
		  ON CONFLICT DO NOTHING`, []any{hash}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// 64-char lowercase hex, which 0001 CHECKs. Spelled the way the other proofs spell it.
const (
	cfgTypeChecked   = "00000000000000000000000000000000000000000000000000000000000000aa"
	cfgSyntaxChecked = "00000000000000000000000000000000000000000000000000000000000000bb"
)
