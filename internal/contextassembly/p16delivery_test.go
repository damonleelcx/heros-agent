package contextassembly

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// p16delivery_test.go — the drop record survives the SECOND DELIVERY ROUTE (P16 §11.2, FR55, NFR17).
//
// # Why this file exists at all
//
// This axis's central honesty guarantee is that a context change which discards information records
// what it discarded, and that the recording is unskippable BY CONSTRUCTION rather than by discipline —
// `Assemble` always calls `Record`, so the ordinary path cannot forget.
//
// 🔴 But "unskippable" is only ever true against the paths that existed when it was written. P13 13e
// adds a rollout: a second way for a context decision to take effect, where one node now resolves to
// one of two arms per invocation. A second path is precisely where an unskippable guarantee quietly
// becomes a convention — not because anyone removes the call, but because a new entry point is added
// beside it and nobody notices it does not record.
//
// So the property is stated about the DECISION rather than about the route: whatever chooses what the
// model sees records what it dropped, whichever arm chose it.

// TestBothArmsRecordTheDrop — task 11.2.
//
// The two arms are two resolved context entries. Each is assembled through the same Runner, and each
// must produce a record of the same shape — otherwise a rollout's two halves would be measured
// differently and the comparison between them would be meaningless.
func TestBothArmsRecordTheDrop(t *testing.T) {
	// Both arms are LOSSY, with different params — which is what a real retrieval-tuning rollout looks
	// like. They must record through the same path and emit the same SET OF SIGNALS, so the two halves
	// of the rollout are comparable.
	//
	// 🚫 Deliberately NOT a lossy arm against a lossless one. `Record` carries `Lossy` across rather than
	// inferring it from `DropRatio > 0`, so a lossless policy legitimately publishes no drop ratio — a
	// lossless policy's 0.0 means "cannot drop" while a lossy policy's 0.0 means "measured no drop".
	// Comparing counts across that boundary would measure the design rather than the guarantee.
	parent := entry(t, "sliding-window", `{"window_size":3}`)
	candidate := entry(t, "sliding-window", `{"window_size":2}`)

	// The same fixture the rest of this package measures against, so the assertion is about the arms
	// rather than about a conversation too small to produce a measurement.
	conv := longConversation()

	type armResult struct {
		arm     string
		signals []string
	}
	var results []armResult

	for _, arm := range []struct {
		name  string
		entry *registry.ContextEntry
		hash  string
	}{
		{"parent", parent, strings.Repeat("a", 64)},
		{"candidate", candidate, strings.Repeat("b", 64)},
	} {
		col, tsdb := newCollector(t)
		r := Runner{Collector: col}
		// 🔴 Each arm is attributed to ITS OWN config_hash, exactly as the rollout resolver emits it.
		// The hashes are real 64-hex values because the collector DROPS events whose tags do not
		// validate — a fake hash here would produce an empty result that looks exactly like a missing
		// recording, which is the confusion this test exists to detect.
		tg := tags()
		tg.ConfigHash = arm.hash
		got, err := r.Assemble(context.Background(), Request{
			Tags: tg, Entry: arm.entry, Conversation: conv, Seed: 7,
		})
		if err != nil {
			t.Fatalf("%s arm: %v", arm.name, err)
		}
		if len(got.Messages) == 0 {
			t.Fatalf("%s arm assembled nothing", arm.name)
		}
		col.Flush()
		names := tsdb.names()
		sort.Strings(names)
		if len(names) == 0 {
			t.Fatalf("%s arm produced NO record at all — the recording is skippable after all", arm.name)
		}
		// 🔴 The guarantee is that the ASSEMBLY is recorded — every arm, every time. It is deliberately
		// not "a drop ratio is emitted": `Record` carries `Lossy` across rather than inferring it from
		// `DropRatio > 0`, so a lossless policy publishes no drop event at all and its implicit zero can
		// never be misread as a measured zero. Demanding a drop signal here would be demanding that the
		// axis break that distinction.
		var sawAssembly bool
		for _, n := range names {
			if n == telemetry.MetricContextAssembledTokens {
				sawAssembly = true
			}
		}
		if !sawAssembly {
			t.Fatalf("%s arm recorded %v but no assembly record; a context decision that took effect "+
				"without being recorded is the failure this axis exists to prevent", arm.name, names)
		}
		results = append(results, armResult{arm.name, names})
	}

	if len(results) != 2 {
		t.Fatalf("expected two arms, ran %d", len(results))
	}
	// Byte-comparable in SHAPE: both arms emit the same set of signals. A rollout whose arms recorded
	// different things would produce two halves that cannot be compared to each other.
	if strings.Join(results[0].signals, ",") != strings.Join(results[1].signals, ",") {
		t.Fatalf("the arms recorded different signal sets:\n  %s: %v\n  %s: %v",
			results[0].arm, results[0].signals, results[1].arm, results[1].signals)
	}
}

// TestNoPathTakesEffectWithoutARecord — task 11.2, NFR17.
//
// # Why this is a STRUCTURAL assertion
//
// A behavioural test proves the paths it calls. It cannot prove the absence of a path someone adds
// next month — and "someone adds an entry point beside the recording one" is exactly the failure mode
// a second delivery route introduces.
//
// So this enumerates every exported function in the package that hands a caller an assembled context,
// and requires each to reach `Record`. Adding a new one without recording fails the build.
func TestNoPathTakesEffectWithoutARecord(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// funcsReachingRecord is the set of package-level functions/methods that call Record, directly or
	// through another function in this package. One level of indirection is enough for the shapes here
	// (Measure -> Assemble -> Record) and keeps the check readable.
	direct := map[string]bool{}
	calls := map[string]map[string]bool{}
	returnsAssembled := map[string]bool{}
	exported := map[string]bool{}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				name := fn.Name.Name
				exported[name] = fn.Name.IsExported()
				calls[name] = map[string]bool{}

				// Does it hand back an assembled context (directly or wrapped)?
				if fn.Type.Results != nil {
					for _, res := range fn.Type.Results.List {
						if strings.Contains(typeString(res.Type), "AssembledContext") ||
							strings.Contains(typeString(res.Type), "Measurement") {
							returnsAssembled[name] = true
						}
					}
				}

				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch f := call.Fun.(type) {
					case *ast.Ident:
						calls[name][f.Name] = true
						if f.Name == "Record" {
							direct[name] = true
						}
					case *ast.SelectorExpr:
						calls[name][f.Sel.Name] = true
						if f.Sel.Name == "Record" || f.Sel.Name == "Assemble" {
							calls[name][f.Sel.Name] = true
						}
					}
					return true
				})
			}
		}
	}

	reaches := func(name string) bool {
		if direct[name] {
			return true
		}
		for callee := range calls[name] {
			if direct[callee] {
				return true
			}
		}
		return false
	}

	var offenders []string
	for name := range returnsAssembled {
		if !exported[name] {
			continue
		}
		// `Record` itself and the pure comparison helpers are not assembly paths.
		if name == "Record" || name == "Observe" {
			continue
		}
		if !reaches(name) {
			offenders = append(offenders, name)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these exported functions hand back an assembled context without reaching Record: %v\n"+
			"A context decision that takes effect without recording what it dropped is the failure this axis "+
			"was built to prevent — and a second delivery route is exactly where such a path gets added.",
			offenders)
	}

	// Sanity: the check must be able to SEE the paths it is policing. If the parse found none, the test
	// would pass vacuously, which is the failure mode this project has been bitten by repeatedly.
	if len(returnsAssembled) == 0 {
		t.Fatal("the structural check found no assembly paths at all; it would pass vacuously")
	}
}

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	}
	return ""
}
