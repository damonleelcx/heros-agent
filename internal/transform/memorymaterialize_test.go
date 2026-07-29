package transform

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §4 — the Go engine's memory refusal, and why it is a BETTER refusal rather than a materialization.
//
// The point of this file is that "Go is unsupported" is false and would hide the operative fact. Go's
// READ half would work: a generic recall over any SDK's message slice is type-safe without importing it.
// The WRITE half is blocked on one thing — converting the SDK's response value into its message value —
// which does not arise in Python, where a response and a message are the same duck-typed dict.

// TestGoMemoryRefusalsArePerStrategy — Go's two refusal reasons, each on its own fixture case.
//
// The Go fixture's `summarize` is a BARE STATEMENT call — `client.Messages.New(nil, …)` with no
// assignment — so even a content-blind strategy has no variable to record the response from.
func TestGoMemoryRefusalsArePerStrategy(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	for _, strategy := range realStrategies(t) {
		t.Run(strategy, func(t *testing.T) {
			p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
				ids["summarize"]: memoryOverride(t, strategy),
			}), root)
			if err == nil {
				t.Fatalf("a %s override materialized at a call site with no variable holding the response", strategy)
			}
			if p != nil {
				t.Error("a refused memory override produced a patch")
			}
			var re *RewriteError
			if !errors.As(err, &re) || re.Dim != string(variantspec.DimMemory) {
				t.Fatalf("refusal = %#v, want a memory RewriteError", err)
			}
			msg := err.Error()

			if !memoryContentBlindStrategies[strategy] {
				// PERMANENT: a Go message is the customer's SDK type.
				if re.Cause != CauseNotAtCallSite {
					t.Errorf("cause = %q, want %q for a content-reading strategy", re.Cause, CauseNotAtCallSite)
				}
				if !strings.Contains(msg, "reading message TEXT") {
					t.Errorf("the refusal does not explain that the strategy needs message text: %v", err)
				}
				// 🔴 It must name where this DOES work, or a reader concludes the axis is hopeless.
				if !strings.Contains(msg, "Python materializes") {
					t.Errorf("the refusal does not name a language where this strategy works: %v", err)
				}
				return
			}

			// CONTENT-BLIND: the blocker is this call site's shape, which the author can change.
			if re.Cause != CauseCallSiteShape {
				t.Errorf("cause = %q, want %q — the author can assign the result", re.Cause, CauseCallSiteShape)
			}
			if !strings.Contains(msg, "not assigned at statement level") {
				t.Errorf("the refusal does not name the missing assignment: %v", err)
			}
			// 🔴 And it says why the ready read half is not shipped alone.
			if !strings.Contains(msg, "behaves exactly\nlike `none`") && !strings.Contains(msg, "behaves exactly like `none`") {
				t.Errorf("the refusal does not say why the read half alone is withheld: %v", err)
			}
		})
	}
}

// TestGoMemoryNoneStillAppliesAsNoOp — the identity strategy is unaffected by any of this.
func TestGoMemoryNoneStillAppliesAsNoOp(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: memoryOverride(t, "none"),
	}), root)
	if err != nil {
		t.Fatalf("`none` was refused in the Go engine: %v", err)
	}
	if p != nil && len(p.Diff) != 0 {
		t.Errorf("`none` produced a diff:\n%s", p.Diff)
	}
}

// TestMemoryResponseFormRowsAreVerified — every row must be complete, and an UNDECLARED provider must
// refuse rather than fall back to another provider's spelling.
//
// 🔴 The bar a row clears, recorded here because the next person adding one reads the test first:
//  1. the conversion is VERIFIED against the SDK, not written from memory — the anthropic row exists
//     because `func (r Message) ToParam() MessageParam` was read out of messageutil.go in the module
//     cache. It is not in message.go, which is where it would have been guessed;
//  2. a fixture BUILDS against that SDK, so the emission is compiled rather than assumed
//     (TestGoMemoryMaterializedOutputCompiles);
//  3. an sdkNote dates the spelling, so it can be seen to rot past a major version.
func TestMemoryResponseFormRowsAreVerified(t *testing.T) {
	if len(memoryResponseForms) == 0 {
		t.Fatal("no response conversion is declared; Go cannot record a turn without one")
	}
	for cell, form := range memoryResponseForms {
		if form.convert == nil {
			t.Errorf("(%s, %s) declares no conversion", cell.language, cell.provider)
			continue
		}
		if form.sdkNote == "" {
			t.Errorf("(%s, %s) has no sdkNote; a spelling nobody can date to an SDK generation rots silently",
				cell.language, cell.provider)
		}
		if got := form.convert("resp"); !strings.Contains(got, "resp") {
			t.Errorf("(%s, %s) converts to %q, which does not use the response variable it was given",
				cell.language, cell.provider, got)
		}
	}

	// 🚫 An undeclared provider must refuse, not borrow anthropic's spelling.
	if hasMemoryResponseForm("go", "openai") {
		t.Error("a conversion is declared for (go, openai) without a fixture that builds against it")
	}
	if hasMemoryResponseForm("go", "") {
		t.Error("an empty provider resolves to a conversion; a call site with no provider hint would then " +
			"borrow whichever spelling happened to be first")
	}
}

// TestGoMemoryCoverageIsPerStrategy — Go's cells differ BY STRATEGY, and the table must say so.
//
// A content-blind strategy materializes; a content-reading one refuses permanently. Collapsing them
// would either claim Go reads message text or claim it cannot remember at all — both false.
func TestGoMemoryCoverageIsPerStrategy(t *testing.T) {
	var checked int
	for _, c := range CoverageFor(string(variantspec.DimMemory)) {
		if c.Language != "go" {
			continue
		}
		checked++
		blind := memoryContentBlindStrategies[c.Form]
		if blind {
			if c.Status != CoverageMaterializes {
				t.Errorf("[go/%s] says %q; a content-blind strategy runs on a content-blind runtime",
					c.Form, c.Status)
			}
			// 🔴 It must still state the provider precondition, which the cell cannot know.
			if c.Form != "none" && !strings.Contains(c.Note, "response conversion") {
				t.Errorf("[go/%s] does not state that the record half needs a per-provider response "+
					"conversion: %q", c.Form, c.Note)
			}
			continue
		}
		if c.Status != CoverageRefuses {
			t.Errorf("[go/%s] says %q; a strategy that reads message text cannot run on a content-blind "+
				"runtime", c.Form, c.Status)
		}
		// 🔴 Permanent, so NO missing artifact — naming one would promise work that would not help.
		if c.Cause != CauseNotAtCallSite {
			t.Errorf("[go/%s] cause = %q, want %q: reading a Go SDK's message text from generated code is "+
				"not something we can build for you", c.Form, c.Cause, CauseNotAtCallSite)
		}
		if c.MissingArtifact != "" {
			t.Errorf("[go/%s] names a missing artifact (%q) for a permanent refusal; the asymmetry between "+
				"the cause classes is the point", c.Form, c.MissingArtifact)
		}
	}
	if checked == 0 {
		t.Fatal("no Go memory coverage cells were checked; the table has drifted")
	}
}
