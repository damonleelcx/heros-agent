package improvementrun

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// eventname_test.go is task 5.9 and task 5.5: every event name comes from the central enum, and a merge
// is OBSERVED rather than inferred.

// TestEveryEmittedNameIsInTheCentralEnum reads the SOURCE rather than the values, because the failure
// being prevented is a string literal at a call site — and a test over the constants would not see one.
//
// 🔴 `eventname`'s own header states the shape that matters: an INVENTED name is a free-text field on
// the far side of a boundary. `slog.Info(fmt.Sprintf("agentd.run.%s", axis))` is one plausible line and
// an exfiltration path, and it is exactly the line somebody writes when adding a per-axis counter.
func TestEveryEmittedNameIsInTheCentralEnum(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing this package: %v", err)
	}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "emit" {
					return true
				}
				if len(call.Args) == 0 {
					return true
				}
				switch arg := call.Args[0].(type) {
				case *ast.SelectorExpr:
					// eventname.Something — the only admissible shape.
					if ident, ok := arg.X.(*ast.Ident); !ok || ident.Name != "eventname" {
						t.Errorf("%s: emit() is called with %s.%s rather than an `eventname` constant",
							filepath.Base(path), exprName(arg.X), arg.Sel.Name)
					}
				case *ast.BasicLit:
					lit, _ := strconv.Unquote(arg.Value)
					t.Errorf("%s: emit() is called with the LITERAL %q. A name that is not in the central "+
						"enum is a free-text field on the far side of a boundary — the exfiltration shape "+
						"`eventname` exists to close", filepath.Base(path), lit)
				default:
					t.Errorf("%s: emit() is called with a computed name (%T). An interpolated event name "+
						"is the same free-text field one step less obvious", filepath.Base(path), arg)
				}
				return true
			})
		}
	}
}

func exprName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}

// TestEveryLedgerKindMapsToACentralEventNameOrToNone asserts the table in ledger.go is TOTAL over the
// kinds — every kind either names a central event or explicitly names none. A kind falling through
// would emit nothing with nothing saying so.
func TestEveryLedgerKindMapsToACentralEventNameOrToNone(t *testing.T) {
	emitting := 0
	for _, k := range Kinds() {
		n, ok := EventFor(k)
		if !ok {
			continue
		}
		emitting++
		if !n.Valid() {
			t.Errorf("ledger kind %q maps to %q, which is not in the central enum", k, n)
		}
	}
	if emitting != 5 {
		t.Fatalf("%d ledger kinds emit an event and `tasks.md` 5.9 names five. The ledger has fourteen "+
			"kinds because the RECONCILIATION PASS reads it; an event stream is not a reconciliation "+
			"input, and emitting fourteen would be a second, worse copy of the ledger in a log index",
			emitting)
	}
}

func TestTheFiveNamesTaskFiveNineRequiresAreEmittable(t *testing.T) {
	for _, n := range []eventname.Name{
		eventname.RunPlanCreated, eventname.RunCandidateVerified, eventname.RunChangeWithdrawn,
		eventname.DeliveryPROpened, eventname.DeliveryDeduplicated,
	} {
		if !n.Valid() {
			t.Errorf("%q is not in the central enum, so nothing may emit it", n)
		}
	}
}

// ── 5.5 a merge is OBSERVED, never inferred from a pull request closing ──────────────────────────

// TestConversationalRun_MergeIsObservedNotInferred re-runs P12's property through P35's caller.
//
// 🔴 It is revenue correctness as well as a delivery property: P7 bills only merged-PR deltas, so a
// close inferred as a merge is an invoice for a change nobody shipped.
func TestConversationalRun_MergeIsObservedNotInferred(t *testing.T) {
	rec := deliveryrecord.NewMemStore()
	obs := forgedelivery.NewMergeObserver(rec)
	ctx := context.Background()

	if err := rec.Append(ctx, forgedelivery.Entry{
		DeliveryID: "d1", TenantID: "ten_1", ConfigHash: "cfg", SourceRevision: "rev",
		Target: "nousresearch/hermes-agent", ForgeRef: "nousresearch/hermes-agent#7",
		Mode: forgedelivery.ModeApp, State: forgedelivery.StateOpened, Actor: "app:p1",
	}); err != nil {
		t.Fatal(err)
	}

	// A pull request CLOSES. It is recorded as closed and nothing else.
	if err := obs.ObserveClose(ctx, "d1", "app-webhook", "the reviewer closed it"); err != nil {
		t.Fatal(err)
	}
	head, _, err := rec.Head(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if head.State == forgedelivery.StateMerged {
		t.Fatal("a pull request that CLOSED was recorded as merged. P7 bills merged-PR deltas, so this " +
			"is an invoice for a change nobody shipped")
	}
	if head.State != forgedelivery.StateClosed {
		t.Fatalf("a closed pull request is in state %q", head.State)
	}

	// 🔴 And a merge with no observed commit is refused outright — a merged state with no commit would
	// be an inference wearing an observation's clothes.
	if err := obs.ObserveMerge(ctx, "d1", "", "app-webhook"); err == nil {
		t.Fatal("a merge with no commit was recorded")
	}
}
