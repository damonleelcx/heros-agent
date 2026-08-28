package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// p37_save_proof_test.go is task 6.6 — **a 200 is not evidence of a write.**
//
// # The failure this exists to catch, which this repository has produced before
//
// `INSERT OR IGNORE` over a constraint violation returns success. The handler returns 200, the surface
// renders a `config_hash`, the reader pins it, and nothing was written. Every layer reports success and
// the product has silently lost the change.
//
// So the save path is asserted in FOUR layers, and each one is a different claim:
//
//	1. HTTP           the route answers 200 and returns a `config_hash`
//	2. the RECORD     a row exists for that change, with that hash, stamped `unverified`
//	3. the VARIANT    the row's hash is the one the derived spec actually fingerprints to
//	4. the SURFACE    the hash the reader is shown is the hash those rows produce
//
// Layer 3 is the one a shallower test skips, and it is the one that matters: a handler that echoed the
// request's own parent hash back would satisfy 1, 2 and 4 and be completely wrong.
//
// # 🔴 Why this drives the REAL Submitter rather than `fakeAuthoring`
//
// `authoring_test.go`'s fake exists to test the handler's status-code MAPPING, and it is right for that.
// It cannot be used here: a fake that returns a `Submission` proves the handler forwards a struct, which
// is precisely the claim that is not in doubt. The whole point of task 6.6 is that the row exists.

// saveProofServer mounts the real submitter over an in-memory recorder and returns both.
func saveProofServer(t *testing.T) (*Server, authoring.Recorder, *variantspec.VariantSpec) {
	t.Helper()
	parent := saveProofParent()
	rec := authoring.NewMemRecorder()
	src := &realAuthoring{
		submitter: authoring.Submitter{
			Preflight: authoring.Preflighter{Resolver: saveProofResolver{}, Materializer: saveProofMaterializer{}},
			Applier:   saveProofApplier{},
			Record:    rec,
			Auth:      saveProofAuth{},
		},
		parent: parent,
	}
	s := &Server{Mux: http.NewServeMux()}
	s.MountAuthoring(src)
	return s, rec, parent
}

func TestASaveIsProvedByAReadOfTheRowsItWrote(t *testing.T) {
	s, rec, parent := saveProofServer(t)
	ctx := context.Background()

	body := `{"workflow_id":"wf","parent_variant_id":"` + saveProofHash(parent) +
		`","concurrency_token":"","edits":{"n1":{"model_ref":"m-new"}}}`

	// ── Layer 1 · HTTP ─────────────────────────────────────────────────────────────────────────
	req := httptest.NewRequest(http.MethodPost, "/api/v1/authoring/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Mux.ServeHTTP(rr, req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "t1", APIKeyID: "k1"})))

	if rr.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", rr.Code, rr.Body.String())
	}
	var view AuthoringChangeView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ConfigHash == "" {
		t.Fatal("the response carries no config_hash, so the surface has nothing to render")
	}

	// ── Layer 2 · the RECORD ───────────────────────────────────────────────────────────────────
	//
	// 🔴 This is the layer a 200 does not imply. A handler that returned a hash and wrote nothing
	// passes layer 1 and fails here.
	rows, err := rec.ListByTenant(ctx, "t1")
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the save wrote %d record rows, want exactly 1", len(rows))
	}
	row := rows[0]
	if row.ConfigHash != view.ConfigHash {
		t.Fatalf("the stored hash is %q and the response said %q — the reader would pin a configuration "+
			"the store does not have", row.ConfigHash, view.ConfigHash)
	}
	if row.ChangeID != view.ChangeID {
		t.Errorf("the stored change id is %q and the response said %q", row.ChangeID, view.ChangeID)
	}
	// 🔴 FR16 — `unverified`, and there is no parameter that changes it. A submission cannot assert its
	// own quality.
	if row.VerificationState != authoring.StateUnverified {
		t.Errorf("the stored change is %q, want unverified", row.VerificationState)
	}
	if row.ActorID == "" {
		t.Error("the change was recorded against nobody, so it is not auditable")
	}

	// ── Layer 3 · the VARIANT ──────────────────────────────────────────────────────────────────
	//
	// 🔴 The hash must be the DERIVED SPEC's own fingerprint, not the parent's echoed back and not a
	// value the handler invented. A test that skipped this would pass on a handler that returned its
	// input.
	derived, err := authoring.Draft{
		WorkflowID: "wf", ParentVariantID: saveProofHash(parent),
		Actor: authoring.Actor{ID: "k1", TenantID: "t1"},
		Edits: map[string]authoring.Edit{"n1": {ModelRef: saveProofStr("m-new")}},
	}.Derive(parent)
	if err != nil {
		t.Fatalf("deriving the variant this save should have produced: %v", err)
	}
	want := saveProofHash(derived)
	if row.ConfigHash != want {
		t.Fatalf("the stored hash is %q; the variant this change derives to fingerprints to %q. The row "+
			"exists and describes a different configuration.", row.ConfigHash, want)
	}
	if row.ConfigHash == saveProofHash(parent) {
		t.Fatal("the saved hash equals the PARENT's — the change hashed to what it started from, so " +
			"either nothing changed or the handler echoed its input")
	}

	// ── Layer 4 · the SURFACE ──────────────────────────────────────────────────────────────────
	//
	// What the reader is shown is `saved.config_hash`, rendered as received. Asserted here as the
	// identity between the wire value and the stored one; `tests/p37-surfaces.test.mjs` asserts that the
	// kit renders that field and computes nothing.
	if view.ConfigHash != row.ConfigHash {
		t.Fatalf("the surface would render %q over a stored %q", view.ConfigHash, row.ConfigHash)
	}
}

// 🔴 The other direction, and the one that makes the four layers a fence rather than a happy path: a
// REFUSED save must write NOTHING. A record row for a change that was refused is a configuration a spec
// could reference forever without it ever having been admissible.
func TestARefusedSaveWritesNothingAndSaysSo(t *testing.T) {
	parent := saveProofParent()
	rec := authoring.NewMemRecorder()
	src := &realAuthoring{
		submitter: authoring.Submitter{
			// A materializer that refuses: the engine's own answer, not a transport failure.
			Preflight: authoring.Preflighter{Resolver: saveProofResolver{}, Materializer: saveProofRefuser{}},
			Applier:   saveProofApplier{},
			Record:    rec,
			Auth:      saveProofAuth{},
		},
		parent: parent,
	}
	s := &Server{Mux: http.NewServeMux()}
	s.MountAuthoring(src)

	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(restore)

	body := `{"workflow_id":"wf","parent_variant_id":"` + saveProofHash(parent) +
		`","concurrency_token":"","edits":{"n1":{"model_ref":"m-new"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/authoring/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Mux.ServeHTTP(rr, req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "t1", APIKeyID: "k1"})))

	if rr.Code == http.StatusOK {
		t.Fatalf("a refused save answered 200: %s", rr.Body.String())
	}
	rows, _ := rec.ListByTenant(context.Background(), "t1")
	if len(rows) != 0 {
		t.Fatalf("a refused save wrote %d record rows, want 0 — an id minted for content that was never "+
			"written is an id a spec could reference forever without resolving", len(rows))
	}

	// §5.6 — the refusal is COUNTED, with the three correlation identities (§5.5).
	line := findLog(t, buf.String(), eventname.ConsoleAxisSaveRefused.String())
	for _, key := range []string{"request_id", "trace_id", "span_id", "cause", "workflow_id"} {
		if _, ok := line[key]; !ok {
			t.Errorf("the save-refused event carries no %q", key)
		}
	}
}

// TestASuccessfulSaveIsCountedAfterTheWrite is §5.6's other half.
//
// 🔴 The event is emitted AFTER the write returns. An event on the way in counts INTENT rather than
// EFFECT, which is the same mistake as trusting the 200 — one layer further out.
func TestASuccessfulSaveIsCountedAfterTheWrite(t *testing.T) {
	s, rec, parent := saveProofServer(t)

	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(restore)

	body := `{"workflow_id":"wf","parent_variant_id":"` + saveProofHash(parent) +
		`","concurrency_token":"","edits":{"n1":{"model_ref":"m-new"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/authoring/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Mux.ServeHTTP(rr, req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "t1", APIKeyID: "k1"})))
	if rr.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", rr.Code, rr.Body.String())
	}

	line := findLog(t, buf.String(), eventname.ConsoleAxisSaved.String())
	for _, key := range []string{"request_id", "trace_id", "span_id", "config_hash", "verification_state"} {
		if _, ok := line[key]; !ok {
			t.Errorf("the save event carries no %q", key)
		}
	}
	if line["verification_state"] != string(authoring.StateUnverified) {
		t.Errorf("the event reports %q; a saved change is unverified", line["verification_state"])
	}
	rows, _ := rec.ListByTenant(context.Background(), "t1")
	if len(rows) != 1 {
		t.Fatalf("the event was emitted over %d rows, want 1", len(rows))
	}
}

// ── The doubles ─────────────────────────────────────────────────────────────────────────────────
//
// 🔴 Every one of them stands in for a SUBSYSTEM this package must not import (the codemod, the
// registries, the entitlement store) — never for the thing under test. `authoring.Submitter`, its
// `Preflighter`, its `Derive` and its recorder are the real ones, which is what makes the row this test
// reads a row the production path would have written.

// realAuthoring adapts the real Submitter to the handler's `AuthoringSource`.
type realAuthoring struct {
	submitter authoring.Submitter
	parent    *variantspec.VariantSpec
}

func (r *realAuthoring) Preflight(ctx context.Context, d authoring.Draft) (authoring.Result, error) {
	return r.submitter.Preflight.Preflight(ctx, d, r.parent)
}
func (r *realAuthoring) Submit(ctx context.Context, d authoring.Draft) (authoring.Submission, error) {
	return r.submitter.Submit(ctx, d, r.parent)
}
func (r *realAuthoring) Revert(context.Context, string, authoring.Actor) (authoring.Reversal, error) {
	return authoring.Reversal{}, nil
}
func (r *realAuthoring) History(context.Context, string, string) ([]authoring.Entry, error) {
	return nil, nil
}
func (r *realAuthoring) Parent(context.Context, string, string) (*variantspec.VariantSpec, error) {
	return r.parent, nil
}

func saveProofParent() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"n1", "n2"},
		Nodes: map[string]variantspec.NodeOverride{"n1": {ModelRef: "m-old"}},
	}
}

// saveProofHash is the same total fingerprint the authoring package's own tests use: sensitive to
// exactly what a draft can change, and to nothing else.
func saveProofHash(s *variantspec.VariantSpec) string {
	var b strings.Builder
	b.WriteString(s.SourceRevision)
	for _, id := range s.Order {
		o := s.Nodes[id]
		b.WriteString("|" + id + ":" + o.ModelRef + "/" + o.PromptRef + "/" + o.ContextPolicy + "/" + string(o.ApplyMode))
		b.WriteString("/" + strings.Join(o.SkillRefs, "+") + "/" + strings.Join(o.ToolSelection, "+"))
	}
	return b.String()
}

func saveProofStr(s string) *string { return &s }

type saveProofResolver struct{}

func (saveProofResolver) Resolve(s *variantspec.VariantSpec) (*variantspec.Resolved, error) {
	return &variantspec.Resolved{ConfigHash: saveProofHash(s), Language: "go", SourceRevision: s.SourceRevision}, nil
}

type saveProofMaterializer struct{}

func (saveProofMaterializer) Probe(context.Context, *variantspec.Resolved) (authoring.Refusal, error) {
	return authoring.Refusal{}, nil
}

// saveProofRefuser is the ENGINE refusing — a computed answer with a cause, not a transport failure.
type saveProofRefuser struct{}

func (saveProofRefuser) Probe(context.Context, *variantspec.Resolved) (authoring.Refusal, error) {
	return authoring.Refusal{
		Cause:  `node "n1", dim model: this call site applies inline, where provider parameters are code rather than data`,
		NodeID: "n1", Field: "model",
	}, nil
}

type saveProofApplier struct{}

func (saveProofApplier) Compile(_ context.Context, c proposal.Candidate) (proposal.Compiled, error) {
	return proposal.Compiled{Candidate: c, DiffHash: "diff-1", BuildStatus: proposal.BuildBuilt}, nil
}

type saveProofAuth struct{}

func (saveProofAuth) MayAuthor(context.Context, authoring.Actor) error { return nil }
