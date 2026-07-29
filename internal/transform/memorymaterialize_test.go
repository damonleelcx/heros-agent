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

// TestGoMemoryRefusalNamesTheBlockedHalf — task 4.1's honest outcome.
func TestGoMemoryRefusalNamesTheBlockedHalf(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	for _, strategy := range realStrategies(t) {
		t.Run(strategy, func(t *testing.T) {
			p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
				ids["summarize"]: memoryOverride(t, strategy),
			}), root)
			if err == nil {
				t.Fatalf("the Go engine materialized a %s memory override; no response conversion is "+
					"declared, so the recorded turn could not contain what the call returned", strategy)
			}
			if p != nil {
				t.Error("a refused memory override produced a patch")
			}
			var re *RewriteError
			if !errors.As(err, &re) || re.Dim != string(variantspec.DimMemory) {
				t.Fatalf("refusal = %#v, want a memory RewriteError", err)
			}

			msg := err.Error()
			// 🔴 It names WHICH HALF. "Go is unsupported" would send the reader looking for general Go
			// work when the missing piece is one conversion.
			if !strings.Contains(msg, "record what this call returns") {
				t.Errorf("the refusal does not name the record half as the blocked one: %v", err)
			}
			// 🔴 And it says the read half WOULD work, which is the difference between a boundary and a
			// dead end — and which stops a reader concluding the axis is hopeless in Go.
			if !strings.Contains(msg, "read half would work") {
				t.Errorf("the refusal does not say the read half is ready: %v", err)
			}
			// It names the reason: two different static types.
			if !strings.Contains(msg, "DIFFERENT TYPES") {
				t.Errorf("the refusal does not explain why Go differs from Python: %v", err)
			}
			// And it points at the language that DOES materialize, so the reader can tell the axis works.
			if !strings.Contains(msg, "Python materializes this strategy today") {
				t.Errorf("the refusal does not name a language where this works: %v", err)
			}
			// 🚫 It must NOT claim the read half alone could be shipped as a partial capability.
			if strings.Contains(msg, "read half ALONE is never emitted") == false {
				t.Errorf("the refusal does not say why the ready half is not shipped by itself: %v", err)
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

// TestMemoryResponseFormTableIsEmptyAndSaysWhy — 🚫 the table ships with no rows, and that is evidence.
//
// This test exists so the emptiness is a DECISION with a reason attached, not an oversight someone
// "fixes" by pasting in a plausible-looking SDK call. The bar a row must clear is written down here,
// because the person adding one will read this test before they read the file comment.
func TestMemoryResponseFormTableIsEmptyAndSaysWhy(t *testing.T) {
	if len(memoryResponseForms) != 0 {
		// If you are here because you added a row: good — but a row is necessary and not sufficient.
		// This module cannot compile against a real SDK (the Go fixture is committed as .txt for exactly
		// that reason), so an unverified conversion would be emitted into a customer's repository as a
		// guess. ADR-001 names that as the top risk, with the wrong-but-compiling version the worse half.
		//
		// What a row owes, before this assertion is relaxed:
		//   1. a fixture that BUILDS against that SDK, so the emission is compiled rather than assumed;
		//   2. a conformance assertion that the materialized behaviour matches internal/memoryruntime,
		//      the same bar the Python module clears by execution;
		//   3. an sdkNote dating the spelling to an SDK generation, so it can be seen to rot.
		for cell, form := range memoryResponseForms {
			if form.convert == nil || form.sdkNote == "" {
				t.Errorf("(%s, %s) declares an incomplete response form; a spelling nobody can date to an "+
					"SDK generation rots silently", cell.language, cell.provider)
			}
		}
		t.Fatalf("memoryResponseForms has %d row(s). Adding one is necessary and NOT sufficient — see the "+
			"comment in this test for the three things a row still owes before Go can materialize memory.",
			len(memoryResponseForms))
	}
	if got := MemoryResponseProviders(); len(got) != 0 {
		t.Errorf("MemoryResponseProviders() = %v, want empty", got)
	}
	if memoryResponseProvidersDisplay() != "none" {
		t.Errorf("the refusal would render the declared set as %q, want %q",
			memoryResponseProvidersDisplay(), "none")
	}
}

// TestGoMemoryCoverageStatesTheMissingArtifact — the coverage table must carry the same specific reason
// the refusal does, so a reader who never triggers a refusal still learns it.
func TestGoMemoryCoverageStatesTheMissingArtifact(t *testing.T) {
	var checked int
	for _, c := range CoverageFor(string(variantspec.DimMemory)) {
		if c.Language != "go" || c.Form == "none" {
			continue
		}
		checked++
		if c.Status != CoverageRefuses {
			t.Errorf("[go/%s] coverage says %q; Go has no response conversion, so it cannot materialize",
				c.Form, c.Status)
		}
		if !strings.Contains(c.MissingArtifact, "response") {
			t.Errorf("[go/%s] the missing artifact is %q, which does not name the response conversion — the "+
				"one thing a reader would need to supply", c.Form, c.MissingArtifact)
		}
	}
	if checked == 0 {
		t.Fatal("no Go memory coverage cells were checked; the table has drifted")
	}
}
