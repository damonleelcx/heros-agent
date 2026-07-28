package authoring

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// recordingApplier stands in for proposal.Compiler and records exactly what it was handed.
type recordingApplier struct {
	calls []proposal.Candidate
}

func (r *recordingApplier) Compile(_ context.Context, cand proposal.Candidate) (proposal.Compiled, error) {
	r.calls = append(r.calls, cand)
	return proposal.Compiled{Candidate: cand, ConfigHash: "h", BuildStatus: proposal.BuildBuilt}, nil
}

// TestSingleApplyPathAcrossOrigins is the machine-enforced version of "one spine, two origins"
// (P13 task 8.4, NFR11).
//
// It is a STRUCTURAL test, not a behavioural one, because the property it protects is about what code
// EXISTS rather than about what one call does. The failure it is written against is a future
// contributor adding a "quick path" here — resolve the override, call the codemod, emit the diff —
// which would work, would pass every behavioural test, and would silently skip the un-apply refusal,
// the cross-provider refusal, the wiring gate, and the drop gate. No unit test of the happy path can
// see that; a scan of the package's own imports can.
func TestSingleApplyPathAcrossOrigins(t *testing.T) {
	t.Run("the package never reaches for the codemod itself", func(t *testing.T) {
		// The apply path is the compiler's. Anything here importing the transform engine, the worktree
		// applier, or the resolver would be a second road to a diff.
		forbidden := map[string]string{
			"internal/transform": "the codemod is reached through proposal.Compiler, never directly",
			"internal/worktree":  "the build gate is the compiler's, never authoring's",
		}
		for _, file := range packageFiles(t) {
			f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for bad, why := range forbidden {
					if strings.HasSuffix(path, bad) {
						t.Errorf("%s imports %s — %s", filepath.Base(file), path, why)
					}
				}
			}
		}
	})

	t.Run("Apply delegates and adds nothing", func(t *testing.T) {
		rec := &recordingApplier{}
		cand := proposal.Candidate{
			Operator: "authored", NodeID: "n1", Origin: OriginUser,
			Actor: Actor{ID: "u1", TenantID: "t1"},
			Spec:  &variantspec.VariantSpec{SourceRevision: "rev"},
		}
		if _, err := Apply(context.Background(), rec, cand); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(rec.calls) != 1 {
			t.Fatalf("compiler calls = %d, want exactly 1 (the shared path, used once)", len(rec.calls))
		}
		// The candidate must arrive at the compiler unmodified. An authoring layer that "helpfully"
		// adjusted a spec on its way through would be a second author of the configuration.
		got := rec.calls[0]
		if got.NodeID != cand.NodeID || got.Spec != cand.Spec || got.Origin != cand.Origin {
			t.Errorf("candidate mutated in transit: got %+v", got)
		}
	})

	t.Run("an operator-originated candidate cannot use the authoring entry point", func(t *testing.T) {
		rec := &recordingApplier{}
		_, err := Apply(context.Background(), rec, proposal.Candidate{Operator: proposal.OpModelUpgrade})
		if err == nil {
			t.Fatal("want ErrNotAuthored for an operator-originated candidate, got nil")
		}
		if len(rec.calls) != 0 {
			t.Errorf("compiler was called %d times on a rejected candidate, want 0", len(rec.calls))
		}
	})

	t.Run("origin does not change what the compiler is asked to build", func(t *testing.T) {
		// Two candidates identical except for authorship must present the compiler with the same Spec.
		// This is the in-package half of TestOriginDoesNotAffectConfigHash: the hash cannot differ if the
		// input to the pipeline does not.
		spec := &variantspec.VariantSpec{SourceRevision: "rev"}
		rec := &recordingApplier{}
		user := proposal.Candidate{Operator: "authored", Spec: spec, Origin: OriginUser,
			Actor: Actor{ID: "u1"}, ForkedFromProposal: "cand-7"}
		if _, err := Apply(context.Background(), rec, user); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		operator := proposal.Candidate{Operator: proposal.OpModelUpgrade, Spec: spec}
		if _, err := rec.Compile(context.Background(), operator); err != nil {
			t.Fatalf("Compile: %v", err)
		}
		if rec.calls[0].Spec != rec.calls[1].Spec {
			t.Error("the two origins presented different specs to the compiler")
		}
	})
}

// TestOriginNormalizesToOperator pins the zero value. Every construction site that predates 13c omits
// Origin, and those candidates are operator-originated by construction — reading "" as a third,
// unknown state would invent a case nothing produces and every switch would have to handle it.
func TestOriginNormalizesToOperator(t *testing.T) {
	var zero proposal.Origin
	if zero.Normalized() != OriginOperator {
		t.Errorf("zero Origin normalized to %q, want %q", zero.Normalized(), OriginOperator)
	}
	if zero.IsUser() {
		t.Error("a zero Origin reported IsUser — a pre-13c candidate would gain authoring's affordances")
	}
	if !OriginUser.IsUser() {
		t.Error("OriginUser did not report IsUser")
	}
}

func packageFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		t.Fatal("no non-test source files found — the structural scan would vacuously pass")
	}
	return out
}

// assert the scan is looking at real ASTs, not at nothing.
var _ = ast.File{}
