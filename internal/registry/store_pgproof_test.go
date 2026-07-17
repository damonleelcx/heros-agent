//go:build pgproof

// Live-Postgres proof for the four registries (tasks 1.1, 1.6, 1.7).
//
// This is not a unit test of Go logic — it applies the real migrations to a real server and proves
// the invariants hold end to end, through both lines of defense: this package's API (which offers no
// mutation) and 0002's structural guards (which reject one anyway).
//
// It is behind the `pgproof` build tag rather than an env-var skip. A test that quietly skips when
// its dependency is missing reports green for a thing it never checked; this one is either compiled
// and run by `make pg-proof` — where a missing database is a FAILURE, not a skip — or it is not part
// of the run at all. There is no configuration under which it passes without proving anything.
//
//	make pg-proof                       # boots an ephemeral Postgres in Docker and runs this
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/registry/
package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/lib/pq"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	// Its own schema: `go test` runs packages concurrently, and this proof and internal/variantspec's
	// both apply 0001 — whose DDL is bare CREATE TABLE — against the same server. See internal/pgtest.
	db, err := pgtest.Open("proof_registry")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	if err := applyMigrations(db); err != nil {
		fmt.Fprintf(os.Stderr, "apply migrations: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func migrationPath(name string) string {
	return filepath.Join("..", "..", "db", "migrations", "postgres", name)
}

// applyMigrations runs the real .sql files — not an inline copy of them. A test that builds its own
// schema proves only that the test's schema works; the shipped migration is what production applies.
func applyMigrations(db *sql.DB) error {
	for _, f := range []string{"0001_p0_lineage.up.sql", "0002_p2_registries.up.sql"} {
		sqlBytes, err := os.ReadFile(migrationPath(f))
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		// lib/pq sends a parameterless query via the simple protocol, so the whole file — dollar-quoted
		// function bodies and all — executes as one script.
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("apply %s: %w", f, err)
		}
	}
	return nil
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	blobs, err := NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	return NewStore(testDB, blobs)
}

func sqlState(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

// constraintName reports which named constraint rejected a statement. SQLSTATE alone is not enough
// for the CHECKs: model_entry has three, and all of them raise 23514 — asserting only the code would
// let a row rejected by the hex-format check pass as proof that content-addressing is enforced.
func constraintName(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Constraint
	}
	return ""
}

func modelSpec(provider, id string) ModelSpec {
	return ModelSpec{Provider: provider, ModelID: id,
		Params: ModelParams{Temperature: ptrF(0), MaxTokens: ptrI(1024)}}
}

// ── Task 1.2 / FR9 ───────────────────────────────────────────────────────────────────────────────

func TestPG_RegisterModel_PinsProviderIDAndParams(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seed := int64(7)
	spec := ModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8",
		Params: ModelParams{Temperature: ptrF(0.2), MaxTokens: ptrI(2048), ThinkingBudget: ptrI(4096), Seed: &seed}}

	id, err := s.RegisterModel(ctx, "pg-pins", spec)
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}

	got, err := s.ResolveModel(ctx, id)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	// The registries spec: "the provider, model ID, and all inference params resolve exactly as
	// stored in that version".
	if got.Name != "pg-pins" || got.Spec.Provider != "anthropic" || got.Spec.ModelID != "claude-opus-4-8" {
		t.Errorf("resolved to %+v", got.Spec)
	}
	if *got.Spec.Params.Temperature != 0.2 || *got.Spec.Params.MaxTokens != 2048 ||
		*got.Spec.Params.ThinkingBudget != 4096 || *got.Spec.Params.Seed != 7 {
		t.Errorf("params did not round-trip: %+v", got.Spec.Params)
	}
}

func TestPG_RegisterModel_IsIdempotentForIdenticalContent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	spec := modelSpec("anthropic", "claude-opus-4-8")

	first, err := s.RegisterModel(ctx, "pg-idem", spec)
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	second, err := s.RegisterModel(ctx, "pg-idem", spec)
	if err != nil {
		t.Fatalf("re-registering identical content must be a no-op, not an error: %v", err)
	}
	if first != second {
		t.Errorf("identical content got two ids: %s / %s", first, second)
	}
	if vs, err := s.ModelVersions(ctx, "pg-idem"); err != nil {
		t.Fatalf("ModelVersions: %v", err)
	} else if len(vs) != 1 {
		t.Errorf("identical content published %d rows, want 1: %v", len(vs), vs)
	}
}

func TestPG_ResolveModel_UnknownRefIsErrNotFound(t *testing.T) {
	// FR11: the Loader fails closed on a dangling ref before any transform is generated. This is the
	// error it keys on.
	_, err := newTestStore(t).ResolveModel(context.Background(), strings.Repeat("0", 64))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for a dangling model_ref, got %v", err)
	}
}

// ── Task 1.6 — immutability ──────────────────────────────────────────────────────────────────────

// The registries spec: "Editing produces a new version, old one intact".
func TestPG_Immutability_EditPublishesNewVersionAndOldStillResolves(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	v1, err := s.RegisterModel(ctx, "pg-edit", modelSpec("anthropic", "claude-opus-4-8"))
	if err != nil {
		t.Fatalf("RegisterModel v1: %v", err)
	}
	// "Edit" the entry: same name, changed content.
	v2, err := s.RegisterModel(ctx, "pg-edit", modelSpec("openai", "gpt-5"))
	if err != nil {
		t.Fatalf("RegisterModel v2: %v", err)
	}
	if v1 == v2 {
		t.Fatal("editing an entry reused the version_id")
	}

	old, err := s.ResolveModel(ctx, v1)
	if err != nil {
		t.Fatalf("the original version no longer resolves after an edit: %v", err)
	}
	if old.Spec.Provider != "anthropic" || old.Spec.ModelID != "claude-opus-4-8" {
		t.Errorf("the original version resolved to edited content: %+v", old.Spec)
	}
	if vs, _ := s.ModelVersions(ctx, "pg-edit"); len(vs) != 2 {
		t.Errorf("want 2 versions of the edited name, got %v", vs)
	}
}

// The registries spec: "In-place mutation rejected — the write is rejected AND the stored content
// for that version_id is unchanged".
//
// This package has no API that could attempt this, which is the first line of defense. So the proof
// goes around it and issues the UPDATE directly — the case a future caller, a migration script, or a
// psql session would produce. The DB is the last line, and this is the test that it is really there.
func TestPG_Immutability_RawUpdateOfAPublishedVersionIsRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterModel(ctx, "pg-nomutate", modelSpec("anthropic", "claude-opus-4-8"))
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	before, err := s.ResolveModel(ctx, id)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}

	for _, tc := range []struct {
		name string
		q    string
	}{
		{"rewrite the envelope", `UPDATE model_entry SET envelope = $2 WHERE version_id = $1`},
		{"rename the entry", `UPDATE model_entry SET name = 'renamed' WHERE version_id = $1`},
		{"backdate it", `UPDATE model_entry SET created_at = now() WHERE version_id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if strings.Contains(tc.q, "$2") {
				_, err = testDB.ExecContext(ctx, tc.q, id, []byte(`{"kind":"model","name":"pg-nomutate","spec":{}}`))
			} else {
				_, err = testDB.ExecContext(ctx, tc.q, id)
			}
			if err == nil {
				t.Fatal("a published version was mutated in place; immutability is not enforced")
			}
			if got := sqlState(err); got != "HR001" {
				t.Errorf("rejected, but by %s rather than the immutability guard (HR001): %v", got, err)
			}
		})
	}

	// "AND the stored content for that version_id is unchanged."
	after, err := s.ResolveModel(ctx, id)
	if err != nil {
		t.Fatalf("ResolveModel after the rejected mutations: %v", err)
	}
	if string(after.Envelope) != string(before.Envelope) {
		t.Errorf("content changed despite the rejection:\n before %s\n after  %s", before.Envelope, after.Envelope)
	}
}

// Deleting is mutation too: a Variant Spec pinned months ago must keep resolving (FR10), which a
// DELETE would break just as thoroughly as an UPDATE.
func TestPG_Immutability_RawDeleteOfAPublishedVersionIsRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterModel(ctx, "pg-nodelete", modelSpec("anthropic", "claude-opus-4-8"))
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	_, err = testDB.ExecContext(ctx, `DELETE FROM model_entry WHERE version_id = $1`, id)
	if err == nil {
		t.Fatal("a published version was deleted; every Variant Spec pinning it would now dangle")
	}
	if got := sqlState(err); got != "HR001" {
		t.Errorf("rejected, but by %s rather than the immutability guard (HR001): %v", got, err)
	}
	if _, err := s.ResolveModel(ctx, id); err != nil {
		t.Errorf("the version stopped resolving after a rejected delete: %v", err)
	}
}

// TRUNCATE is the hole a row-level trigger does not cover: BEFORE UPDATE OR DELETE ... FOR EACH ROW
// does not fire for it, so without a statement-level guard `TRUNCATE model_entry` would erase every
// published version — and every pinned Variant Spec with it — while every other guard reported the
// registry as immutable.
func TestPG_Immutability_TruncateIsRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterModel(ctx, "pg-notruncate", modelSpec("anthropic", "claude-opus-4-8"))
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	for _, table := range []string{"model_entry", "prompt_entry", "skill_entry", "context_entry"} {
		t.Run(table, func(t *testing.T) {
			_, err := testDB.ExecContext(ctx, fmt.Sprintf(`TRUNCATE %s`, table))
			if err == nil {
				t.Fatalf("TRUNCATE %s succeeded; every published version in it is gone", table)
			}
			if got := sqlState(err); got != "HR001" {
				t.Errorf("rejected, but by %s rather than the immutability guard (HR001): %v", got, err)
			}
		})
	}
	if _, err := s.ResolveModel(ctx, id); err != nil {
		t.Errorf("the version stopped resolving after a rejected truncate: %v", err)
	}
}

// The other way to attempt a mutation: keep the id, supply different content. Content-addressing
// makes this a lie the CHECK catches, so "mutate a published version" has no expressible form —
// UPDATE is refused, and INSERT-with-new-content is simply a different version_id.
func TestPG_Immutability_InsertingContentThatDoesNotHashToItsIDIsRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterModel(ctx, "pg-mismatch", modelSpec("anthropic", "claude-opus-4-8"))
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	// A DIFFERENT id (so the PK does not catch it first) that is well-formed hex (so the format CHECK
	// does not catch it either), with a coherent name (so the trigger does not catch it either) —
	// leaving the content-address CHECK as the only guard that can reject this.
	otherID := strings.Repeat("b", 64)
	_, err = testDB.ExecContext(ctx,
		`INSERT INTO model_entry (version_id, name, envelope) VALUES ($1, $2, $3)`,
		otherID, "pg-mismatch", []byte(`{"kind":"model","name":"pg-mismatch","spec":{"provider":"evil"}}`))
	if err == nil {
		t.Fatal("a row whose version_id does not address its content was accepted; content-addressing is not enforced")
	}
	if got := sqlState(err); got != "23514" {
		t.Errorf("rejected, but by %s rather than a CHECK (23514): %v", got, err)
	}
	if got := constraintName(err); got != "model_entry_content_addressed" {
		t.Errorf("rejected by constraint %q, want model_entry_content_addressed: %v", got, err)
	}
	_ = id
}

// The denormalized columns are a projection of the hashed envelope. If they could drift, the
// (name, version_id) index would point at content that names something else.
func TestPG_Coherence_ColumnsMustMatchTheHashedEnvelope(t *testing.T) {
	ctx := context.Background()
	env := []byte(`{"kind":"model","name":"real-name","spec":{"model_id":"m","provider":"p"}}`)
	id := hashBytes(env)

	_, err := testDB.ExecContext(ctx,
		`INSERT INTO model_entry (version_id, name, envelope) VALUES ($1, $2, $3)`,
		id, "lying-name", env)
	if err == nil {
		t.Fatal("a row whose name column disagrees with its envelope was accepted")
	}
	if got := sqlState(err); got != "HR002" {
		t.Errorf("rejected, but by %s rather than the coherence guard (HR002): %v", got, err)
	}

	// Same guard, other axis: a prompt envelope filed in the model registry.
	promptEnv := []byte(`{"kind":"prompt","name":"x","spec":{}}`)
	_, err = testDB.ExecContext(ctx,
		`INSERT INTO model_entry (version_id, name, envelope) VALUES ($1, $2, $3)`,
		hashBytes(promptEnv), "x", promptEnv)
	if err == nil {
		t.Fatal("a prompt envelope was accepted into the model registry")
	}
	if got := sqlState(err); got != "HR002" {
		t.Errorf("rejected, but by %s rather than the coherence guard (HR002): %v", got, err)
	}
}

// ── Task 1.7 — expand-contract evolution ─────────────────────────────────────────────────────────

// The registries spec: "Old spec resolves after a new version is published — WHEN a new model entry
// version is published and the model schema gains an optional field, THEN a Variant Spec pinning the
// older model version_id still resolves and executes unchanged."
//
// Both halves are exercised against the live table: an entry published by an older build (its
// envelope has no `params` key at all), and the schema growing an optional field (a `routing_tier`
// this build's ModelSpec does not know).
func TestPG_ExpandContract_OldPinnedVersionResolvesAfterANewOneIsPublished(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// An entry as an OLDER build would have sealed it: no params.
	type oldModelSpec struct {
		Provider string `json:"provider"`
		ModelID  string `json:"model_id"`
	}
	oldID, oldEnv, err := seal(KindModel, "pg-evolve", oldModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := s.publish(ctx, tableModel, oldID, "pg-evolve", oldEnv, "", nil); err != nil {
		t.Fatalf("publish the old-build entry: %v", err)
	}

	// A Variant Spec pins oldID. Now the registry evolves: a new version of the same name is
	// published, carrying a field this build's ModelSpec has never heard of.
	type futureModelSpec struct {
		Provider    string      `json:"provider"`
		ModelID     string      `json:"model_id"`
		Params      ModelParams `json:"params"`
		RoutingTier string      `json:"routing_tier"`
	}
	newID, newEnv, err := seal(KindModel, "pg-evolve", futureModelSpec{
		Provider: "anthropic", ModelID: "claude-opus-4-8",
		Params: ModelParams{Temperature: ptrF(0.5)}, RoutingTier: "premium"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := s.publish(ctx, tableModel, newID, "pg-evolve", newEnv, "", nil); err != nil {
		t.Fatalf("publish the new-build entry: %v", err)
	}
	if oldID == newID {
		t.Fatal("test bug: the two versions must differ")
	}

	// The pinned old version still resolves, to its ORIGINAL content, unchanged.
	old, err := s.ResolveModel(ctx, oldID)
	if err != nil {
		t.Fatalf("a Variant Spec pinning the older version no longer resolves: %v", err)
	}
	if old.Spec.Provider != "anthropic" || old.Spec.ModelID != "claude-opus-4-8" {
		t.Errorf("the old version resolved to the wrong content: %+v", old.Spec)
	}
	if old.Spec.Params.Temperature != nil {
		t.Errorf("the old version gained a param it never had: %+v", old.Spec.Params)
	}
	if string(old.Envelope) != string(oldEnv) {
		t.Errorf("the old version's bytes changed:\n got %s\nwant %s", old.Envelope, oldEnv)
	}

	// And the newer version, with its unknown field, resolves too — ignored, not rejected.
	fresh, err := s.ResolveModel(ctx, newID)
	if err != nil {
		t.Fatalf("an entry carrying an unknown field failed to resolve: %v", err)
	}
	if fresh.Spec.Params.Temperature == nil || *fresh.Spec.Params.Temperature != 0.5 {
		t.Errorf("known fields of the newer version decoded wrong: %+v", fresh.Spec.Params)
	}
}

// ── Task 1.3 — prompts ───────────────────────────────────────────────────────────────────────────

func TestPG_RegisterPrompt_StoresBodyAsBlobAndRendersDeterministically(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	body := "Answer {{query}} in a {{tone}} tone.\nBe brief."

	id, err := s.RegisterPrompt(ctx, "pg-prompt", body)
	if err != nil {
		t.Fatalf("RegisterPrompt: %v", err)
	}

	// The body is a content-hashed blob referenced by hash — PRD §7: prompts may carry PII and are
	// never inlined in a row.
	got, err := s.ResolvePrompt(ctx, id)
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if got.Spec.BodyBlobHash != hashBytes([]byte(body)) {
		t.Errorf("body_blob_hash %s does not address the body", got.Spec.BodyBlobHash)
	}
	var envelopeText string
	if err := testDB.QueryRowContext(ctx,
		`SELECT convert_from(envelope,'UTF8') FROM prompt_entry WHERE version_id = $1`, id).Scan(&envelopeText); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	if strings.Contains(envelopeText, "Answer") {
		t.Errorf("the template body was inlined into the row: %s", envelopeText)
	}

	// The blob is catalogued, so prompt_entry's FK to blob(content_hash) has a target.
	var n int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) FROM blob WHERE content_hash = $1`, got.Spec.BodyBlobHash).Scan(&n); err != nil {
		t.Fatalf("count blob: %v", err)
	}
	if n != 1 {
		t.Errorf("body blob is not catalogued (count=%d)", n)
	}

	out, err := got.Template.Render(map[string]string{"query": "why", "tone": "curt"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "Answer why in a curt tone.\nBe brief." {
		t.Errorf("render = %q", out)
	}
}

// The escape hazard that ruled out a TEXT envelope: a template full of backslashes and multi-byte
// characters must round-trip and keep hashing to its version_id. If the envelope column mangled a
// byte, the content-address CHECK would reject the row outright.
func TestPG_RegisterPrompt_BodyWithEscapesAndUnicodeRoundTrips(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	body := "Backslash: \\ and \\\\ and \\x41\nQuote: \" — em-dash, héllo, 世界, emoji 🙂\nTab:\there"

	id, err := s.RegisterPrompt(ctx, "pg-escapes", body)
	if err != nil {
		t.Fatalf("RegisterPrompt: %v", err)
	}
	got, err := s.ResolvePrompt(ctx, id)
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	rendered, err := got.Template.Render(nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered != body {
		t.Errorf("body did not round-trip:\n got %q\nwant %q", rendered, body)
	}
	// The DB agrees with Go about what these bytes hash to — the CHECK accepted the row, and this
	// asserts the two computed the same digest rather than merely not colliding.
	var dbSaysOK bool
	if err := testDB.QueryRowContext(ctx,
		`SELECT version_id = encode(sha256(envelope),'hex') FROM prompt_entry WHERE version_id = $1`, id).Scan(&dbSaysOK); err != nil {
		t.Fatalf("re-derive the content address in SQL: %v", err)
	}
	if !dbSaysOK {
		t.Error("Postgres and Go disagree about the content address of this entry")
	}
}

func TestPG_RegisterPrompt_MalformedTemplateIsRejectedAndPublishesNothing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterPrompt(ctx, "pg-bad-template", "Answer {{ query }}")
	if err == nil {
		t.Fatalf("a malformed template was published as %s", id)
	}
	if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("want ErrInvalidEntry, got %v", err)
	}
	if vs, _ := s.PromptVersions(ctx, "pg-bad-template"); len(vs) != 0 {
		t.Errorf("a rejected template left %d rows behind: %v", len(vs), vs)
	}
}

// ── Tasks 1.4 / 1.5 — skills and context policies ────────────────────────────────────────────────

func TestPG_RegisterSkill_RoundTripsContractAndValidatesArguments(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterSkill(ctx, "pg-search", SkillSpec{
		ImplHandle:   "builtin:search",
		InputSchema:  json.RawMessage(validInputSchema),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"]}`),
	})
	if err != nil {
		t.Fatalf("RegisterSkill: %v", err)
	}
	got, err := s.ResolveSkill(ctx, id)
	if err != nil {
		t.Fatalf("ResolveSkill: %v", err)
	}
	if got.Spec.ImplHandle != "builtin:search" {
		t.Errorf("impl_handle = %q", got.Spec.ImplHandle)
	}
	if err := got.ValidateInput(map[string]any{"query": "x"}); err != nil {
		t.Errorf("a conforming argument object was rejected: %v", err)
	}
	if err := got.ValidateInput(map[string]any{"limit": 1}); err == nil {
		t.Error("an argument object missing a required field was accepted")
	}
	if err := got.ValidateOutput(map[string]any{"hits": []any{}}); err != nil {
		t.Errorf("a conforming result was rejected: %v", err)
	}
	if err := got.ValidateOutput(map[string]any{}); err == nil {
		t.Error("a result violating the output contract was accepted")
	}
}

// A skill's CONTRACT is part of its version, so tightening a schema cannot retroactively change what
// an already-pinned skill_ref means.
func TestPG_RegisterSkill_ContractChangeIsANewVersion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	v1, err := s.RegisterSkill(ctx, "pg-contract", SkillSpec{ImplHandle: "builtin:search",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`)})
	if err != nil {
		t.Fatalf("RegisterSkill v1: %v", err)
	}
	// Same name, same impl — only the contract tightens.
	v2, err := s.RegisterSkill(ctx, "pg-contract", SkillSpec{ImplHandle: "builtin:search",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`)})
	if err != nil {
		t.Fatalf("RegisterSkill v2: %v", err)
	}
	if v1 == v2 {
		t.Fatal("tightening a skill's input schema did not produce a new version")
	}
	// The pinned old contract is unchanged: it still accepts what it always accepted.
	old, err := s.ResolveSkill(ctx, v1)
	if err != nil {
		t.Fatalf("ResolveSkill v1: %v", err)
	}
	if err := old.ValidateInput(map[string]any{}); err != nil {
		t.Errorf("the older, looser contract changed under a pinned ref: %v", err)
	}
}

func TestPG_RegisterContextPolicy_FullPolicyRoundTrips(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterContextPolicy(ctx, "pg-ctx", "full", nil)
	if err != nil {
		t.Fatalf("RegisterContextPolicy: %v", err)
	}
	got, err := s.ResolveContextPolicy(ctx, id)
	if err != nil {
		t.Fatalf("ResolveContextPolicy: %v", err)
	}
	if got.Spec.Policy != "full" {
		t.Errorf("policy = %q, want full", got.Spec.Policy)
	}
	if got.Policy == nil || got.Policy.Name() != "full" {
		t.Errorf("the policy implementation was not bound: %+v", got.Policy)
	}
	if string(got.Spec.Params) != "{}" {
		t.Errorf("params = %s, want {}", got.Spec.Params)
	}
}

// ── The down migration ───────────────────────────────────────────────────────────────────────────

// Runs last (Go runs tests in source order within a file, and this is the final file position) —
// it drops the registry tables, so nothing may follow it.
func TestPG_ZZ_DownMigrationRemovesEverythingItAdded(t *testing.T) {
	ctx := context.Background()
	down, err := os.ReadFile(migrationPath("0002_p2_registries.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, string(down)); err != nil {
		t.Fatalf("apply down: %v", err)
	}
	for _, table := range []string{"model_entry", "prompt_entry", "skill_entry", "context_entry"} {
		var exists bool
		if err := testDB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if exists {
			t.Errorf("%s survived the down migration", table)
		}
	}
	// 0002 is expand-only: rolling it back must leave 0001's tables untouched.
	var blobExists bool
	if err := testDB.QueryRowContext(ctx, `SELECT to_regclass('blob') IS NOT NULL`).Scan(&blobExists); err != nil {
		t.Fatalf("check blob: %v", err)
	}
	if !blobExists {
		t.Error("the down migration removed a table from 0001; 0002 must be expand-only")
	}
	var n int
	if err := testDB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE id = 2`).Scan(&n); err != nil {
		t.Fatalf("check schema_migrations: %v", err)
	}
	if n != 0 {
		t.Error("the down migration left its schema_migrations row behind")
	}
}
