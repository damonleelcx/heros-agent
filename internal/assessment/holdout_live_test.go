//go:build holdout

// holdout_live_test.go is the REAL holdout run (§3.4, §3.5), behind a build tag because it needs a
// provider credential and spends money.
//
// # 🔴 Why this is a separate file and a separate tag
//
// `holdout_test.go` runs on every commit against a SCRIPTED analyst, and it says at the top exactly
// what that measures: the harness. This file is the only place a number about a MODEL can come from,
// and it is deliberately impossible to run by accident — a `go test ./...` cannot reach it, so nobody
// can point at a green CI run and believe the inference has been evaluated.
//
// Run it with: make assessment-holdout
//
// Required environment:
//
//	HEROS_HOLDOUT_PROVIDER  the provider name the gateway's secrets source resolves (e.g. `anthropic`)
//	HEROS_HOLDOUT_MODEL     the provider's model id (e.g. `claude-opus-5-20260501`)
//	…and whatever `providergateway`'s secrets source needs to resolve that provider's credential.
//
// # What it prints, and what it deliberately does not
//
// Three numbers per axis and no fourth number anywhere. §9.5: *"an aggregate over axes hides the one
// axis that is broken."* If you want one number out of this, the answer is that there is not one.

package assessment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

func TestHoldoutAgainstARealProvider(t *testing.T) {
	provider, model := os.Getenv("HEROS_HOLDOUT_PROVIDER"), os.Getenv("HEROS_HOLDOUT_MODEL")
	if provider == "" || model == "" {
		// 🔴 FATAL, not skipped. A skip here would let `make assessment-holdout` exit 0 having measured
		// nothing, and an operator would reasonably read that as a pass. "Nothing ran" and "everything
		// passed" must not produce the same exit code — that is exactly how a fence stops guarding.
		t.Fatal("HEROS_HOLDOUT_PROVIDER and HEROS_HOLDOUT_MODEL are unset, so this run would measure " +
			"nothing. It exits non-zero rather than skipping, because a skip that exits 0 reads as a pass.")
	}

	secrets, err := providergateway.NewSecretsFromEnv(context.Background())
	if err != nil {
		t.Fatalf("no secrets source: %v", err)
	}
	gw := providergateway.New(secrets)
	entry := &registry.ModelEntry{
		Name: "holdout",
		Spec: registry.ModelSpec{Provider: provider, ModelID: model},
	}
	analyst, err := NewGatewayAnalyst(gw, entry, DefaultConfidenceFloor)
	if err != nil {
		t.Fatalf("NewGatewayAnalyst: %v", err)
	}
	inf, err := NewHerosInference(analyst, DefaultConfidenceFloor)
	if err != nil {
		t.Fatalf("NewHerosInference: %v", err)
	}

	cases, err := LoadCases(filepath.Join("testdata", "holdout.json"))
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	rep, err := Run(context.Background(), inf, cases, func(fixture string) (Subject, error) {
		return subjectFor(t, fixture), nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	fmt.Printf("\nP33 holdout · %s/%s · floor %.2f\n\n", provider, model, DefaultConfidenceFloor)
	fmt.Printf("%-10s %6s %8s %8s %8s %10s %8s %10s\n",
		"axis", "cases", "answered", "correct", "WRONG", "abstained", "→right", "precision")
	for _, s := range rep.PerAxis {
		if s.Cases == 0 {
			continue
		}
		prec := "     n/a"
		if p := s.Precision(); p >= 0 {
			prec = fmt.Sprintf("%8.2f", p)
		}
		fmt.Printf("%-10s %6d %8d %8d %8d %10d %8d %10s\n",
			s.Axis, s.Cases, s.Answered, s.Correct, s.Wrong, s.Abstained, s.AbstainedCorrectly, prec)
	}
	if len(rep.UntestedAxes) > 0 {
		fmt.Printf("\n⚠️  UNTESTED: %v — no holdout case exists for these axes, so this run says nothing\n",
			rep.UntestedAxes)
	}
	fmt.Println("\n🚫 There is deliberately no overall figure. A mean over nine axes can sit high while one")
	fmt.Println("   of them is broken, and the mean is the number people stop reading at.")

	// The only assertion. A WRONG answer is the only failure this set defines: an abstention is a
	// success or a miss, and neither is a defect.
	for _, s := range rep.PerAxis {
		if s.Wrong > 0 {
			t.Errorf("%s produced %d WRONG answer(s) — a claim about a repository that cannot support "+
				"it, which is the one thing a reader cannot detect", s.Axis, s.Wrong)
		}
	}
}
