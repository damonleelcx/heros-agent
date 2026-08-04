package hostedcompile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/sourceingest"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// ── the parse gate ──────────────────────────────────────────────────────────────────────────────────

func patch(files map[string][]byte) *transform.Patch {
	return &transform.Patch{Files: files, Diff: []byte("--- a\n+++ b\n"), DiffHash: strings.Repeat("d", 64)}
}

// 🔴 A parse PASS is `Unavailable`, never `Builds: true`. `built` is the claim ADR-001 hangs delivery
// on, and a parser cannot make it — reporting a parse as a build is how an unverified change becomes
// deliverable.
func TestAParsePassIsNotABuild(t *testing.T) {
	res, err := ParseGate{Language: "go"}.Check(context.Background(),
		patch(map[string][]byte{"main.go": []byte("package main\n\nfunc main() {}\n")}))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Builds {
		t.Fatal("the parse gate reported Builds=true — a parser cannot establish that a change compiles, " +
			"and `built` is what delivery reads")
	}
	if res.Unavailable == "" {
		t.Fatal("a parse pass must say what it did NOT prove; an empty Unavailable means a gate ran and " +
			"its verdict was Builds=false, which is a rejection this gate did not make")
	}
	for _, want := range []string{"PARSES", "toolchain"} {
		if !strings.Contains(res.Unavailable, want) {
			t.Errorf("the reason must name %q, got %q", want, res.Unavailable)
		}
	}
}

// ...and the gate can still REJECT. A codemod that emits unparseable Go is a real build_failed, and
// that is the failure mode this gate exists to catch before it reaches a pull request.
func TestTheParseGateRejectsACodemodBug(t *testing.T) {
	res, err := ParseGate{Language: "go"}.Check(context.Background(),
		patch(map[string][]byte{"broken.go": []byte("package main\n\nfunc main() {\n")}))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Builds {
		t.Fatal("unparseable Go passed the gate")
	}
	if res.Unavailable != "" {
		t.Errorf("a real rejection must not be reported as `no gate ran`: %q", res.Unavailable)
	}
	if !strings.Contains(res.Log, "broken.go") {
		t.Errorf("the log must name the file that failed, got %q", res.Log)
	}
}

// A language with no in-process parser is UNAVAILABLE, not failed. Reporting it as failed would
// permanently retire every proposal for every non-Go workflow with a verdict nobody established.
func TestAnUnparseableLanguageIsUnavailableNotFailed(t *testing.T) {
	for _, lang := range []string{"python", "typescript", "rust", ""} {
		res, err := ParseGate{Language: lang}.Check(context.Background(),
			patch(map[string][]byte{"main.py": []byte("def f(:\n")}))
		if err != nil {
			t.Fatalf("check %s: %v", lang, err)
		}
		if res.Builds || res.Unavailable == "" {
			t.Errorf("language %q: got Builds=%v Unavailable=%q; want unavailable", lang, res.Builds, res.Unavailable)
		}
	}
}

// A non-Go file in a Go workflow is skipped, not silently claimed. The gate must not report success
// over a file it never looked at.
func TestANonGoFileInAGoWorkflowIsNotClaimed(t *testing.T) {
	res, err := ParseGate{Language: "go"}.Check(context.Background(), patch(map[string][]byte{
		"main.go":     []byte("package main\n\nfunc main() {}\n"),
		"config.yaml": []byte("this: [is not, go\n"),
	}))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Builds {
		t.Error("the gate claimed a build over a file it did not parse")
	}
}

// The strength claim matches what ran. The zero Strength is deliberately invalid and un-appliable.
func TestStrengthMatchesWhatWasProved(t *testing.T) {
	if got := (ParseGate{Language: "go"}).Strength(); got != worktree.StrengthSyntaxChecked {
		t.Errorf("go strength = %q, want %q", got, worktree.StrengthSyntaxChecked)
	}
	if got := (ParseGate{Language: "go"}).Strength(); got == worktree.StrengthTypeChecked {
		t.Error("a parse gate claimed type-checked")
	}
	got := (ParseGate{Language: "python"}).Strength()
	if got.Valid() {
		t.Errorf("a language with no parser here claimed strength %q; the zero value is the honest "+
			"answer and it is deliberately invalid", got)
	}
}

// ── compiling ───────────────────────────────────────────────────────────────────────────────────────

type memBlobs struct {
	data map[string][]byte
	err  error
}

func newBlobs() *memBlobs { return &memBlobs{data: map[string][]byte{}} }

func (m *memBlobs) Put(_ context.Context, b []byte) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	m.data[h] = b
	return h, nil
}

func (m *memBlobs) Get(_ context.Context, h string) ([]byte, error) {
	b, ok := m.data[h]
	if !ok {
		return nil, errors.New("no such blob")
	}
	return b, nil
}

type memStore struct {
	scored []proposalstore.Scored
	put    []proposalstore.Record
	err    error
}

func (m *memStore) ForWorkflow(context.Context, string, string) ([]proposalstore.Scored, error) {
	return m.scored, m.err
}

func (m *memStore) Put(_ context.Context, r proposalstore.Record) error {
	m.put = append(m.put, r)
	return nil
}

func scored(id, specHash, diffHash, build string) proposalstore.Scored {
	return proposalstore.Scored{Record: proposalstore.Record{
		ProposalID: id, TenantID: "t1", WorkflowID: "wf",
		Operator: string(proposal.OpModelDowngrade), NodeID: "n_router", Pattern: "Routing",
		SourceRevision: "rev1", SpecBlobHash: specHash,
		SourceDiffBlobHash: diffHash, BuildStatus: build,
	}}
}

// Each "nothing was compiled" reason is its own state with a sentence.
func TestEachCompileStateIsDistinct(t *testing.T) {
	for name, tc := range map[string]struct {
		store *memStore
		want  State
	}{
		"no proposals": {&memStore{}, StateNoProposals},
		"all already compiled": {&memStore{scored: []proposalstore.Scored{
			scored("p1", strings.Repeat("a", 64), strings.Repeat("b", 64), proposalstore.BuildUnbuilt),
		}}, StateNothingToCompile},
	} {
		t.Run(name, func(t *testing.T) {
			c := &Compiler{Store: tc.store, Blobs: newBlobs()}
			res, err := c.Compile(context.Background(), "t1", "wf")
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if res.State != tc.want {
				t.Errorf("state = %q, want %q", res.State, tc.want)
			}
			if res.Detail == "" {
				t.Error("every state must carry a sentence")
			}
			if len(tc.store.put) != 0 {
				t.Errorf("a pass that compiled nothing wrote %d row(s)", len(tc.store.put))
			}
		})
	}
}

// 🔴 A proposal with no stored spec is UNBUILT with the reason — never re-derived. Re-deriving would
// compile a different change under an id a customer may already be verifying.
func TestAProposalWithNoSpecIsNotReDerived(t *testing.T) {
	store := &memStore{scored: []proposalstore.Scored{scored("p1", "", "", proposalstore.BuildUnbuilt)}}
	c := &Compiler{
		Store: store, Blobs: newBlobs(),
		Runner: runnerFunc(func(ctx context.Context, dir string) error { return nil }),
	}
	res, err := c.Compile(context.Background(), "t1", "wf")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(res.Outcomes) != 1 {
		t.Fatalf("outcomes = %+v", res.Outcomes)
	}
	out := res.Outcomes[0]
	if out.Status != proposalstore.BuildUnbuilt {
		t.Errorf("status = %q, want unbuilt — we did not write code that failed to compile", out.Status)
	}
	if out.DiffHash != "" {
		t.Error("a diff was recorded for a proposal whose spec is gone")
	}
	if !strings.Contains(out.Detail, "0031") {
		t.Errorf("the reason must name what is missing, got %q", out.Detail)
	}
}

// A proposal that already carries a diff is never recompiled: a second resolve could mint a different
// config hash and orphan a verdict a customer's CI is already producing.
func TestACompiledProposalIsNotRecompiled(t *testing.T) {
	store := &memStore{scored: []proposalstore.Scored{
		scored("done", strings.Repeat("a", 64), strings.Repeat("b", 64), proposalstore.BuildUnbuilt),
	}}
	c := &Compiler{Store: store, Blobs: newBlobs(),
		Runner: runnerFunc(func(context.Context, string) error {
			t.Fatal("the source was materialized for a workflow with nothing to compile")
			return nil
		})}
	res, err := c.Compile(context.Background(), "t1", "wf")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if res.State != StateNothingToCompile {
		t.Errorf("state = %q", res.State)
	}
}

// The spec round-trips: what the generator wrote is what the compiler reads.
func TestTheStoredSpecRoundTripsIntoACandidate(t *testing.T) {
	blobs := newBlobs()
	spec := map[string]any{
		"workflow_id": "wf", "source_revision": "rev1",
		"nodes": map[string]any{"n_router": map[string]any{"model_ref": strings.Repeat("2", 64)}},
	}
	raw, _ := json.Marshal(spec)
	hash, _ := blobs.Put(context.Background(), raw)

	c := &Compiler{Blobs: blobs}
	cand, err := c.candidateFrom(context.Background(), scored("p1", hash, "", proposalstore.BuildUnbuilt))
	if err != nil {
		t.Fatalf("candidateFrom: %v", err)
	}
	if cand.Spec == nil || cand.Spec.Nodes["n_router"].ModelRef != strings.Repeat("2", 64) {
		t.Fatalf("the spec did not survive: %+v", cand.Spec)
	}
	if cand.NodeID != "n_router" || string(cand.Operator) != string(proposal.OpModelDowngrade) {
		t.Errorf("the candidate lost its identity: %+v", cand)
	}
}

// runnerFunc adapts a function to SourceRunner. The IR is nil, which is enough for the paths under
// test here: they are refused before the codemod is reached.
type runnerFunc func(ctx context.Context, dir string) error

func (f runnerFunc) WithSource(ctx context.Context, _ sourceingest.Ref, fn func(string, *discovery.IR) error) error {
	if err := f(ctx, "/nonexistent"); err != nil {
		return err
	}
	return fn("/nonexistent", nil)
}
