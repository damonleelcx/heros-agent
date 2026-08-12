package herosagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// 🔴 EVERY CODE AND EVENT COMES FROM errors.go (task 4.11).
//
// A code a monitor matches on, or a console switches on, that is typed at three call sites is three
// codes as far as anything downstream is concerned — and the drift is invisible, because each of the
// three works perfectly at the moment it is written.
//
// The fence parses the package rather than trusting the convention, because the convention is exactly
// the thing that fails: it holds until somebody writes `return "credential_unresolved"` at 6pm.
func TestEveryCodeAndEventComesFromTheCentralEnumeration(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go") && fi.Name() != "errors.go"
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}

	// The literal VALUES of every Code, AbstentionReason and Event. A file other than errors.go writing
	// one of these as a string is the drift being prevented.
	reserved := map[string]bool{}
	for _, c := range []Code{
		CodeOK, CodeDisabled, CodeCredentialUnresolved, CodeModelUnregistered, CodeNoActiveDefinition,
		CodeRehearsalPending, CodeCapReached, CodeBudgetExceeded, CodeProviderFailed,
		CodeOutputRejected, CodeNothingToInfer,
	} {
		reserved[string(c)] = true
	}
	for _, r := range []AbstentionReason{
		AbstainBelowFloor, AbstainNoCandidate, AbstainOutOfVocabulary, AbstainUnknownNode,
		AbstainFrontendOwns,
	} {
		reserved[string(r)] = true
	}
	for _, e := range []Event{
		EventInferenceStarted, EventInferenceStored, EventInferenceCacheHit, EventInferenceAborted,
		EventOutputRejected, EventAbstained, EventCapReached, EventDefinitionPublish,
		EventDefinitionActivate, EventRehearsalFailed,
	} {
		reserved[string(e)] = true
	}
	if len(reserved) < 25 {
		t.Fatalf("only %d reserved values were collected — the fence is not seeing the enumeration", len(reserved))
	}

	var offenders []string
	for _, p := range pkgs {
		for name, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v := strings.Trim(lit.Value, `"`)
				if reserved[v] {
					offenders = append(offenders,
						name+":"+fset.Position(lit.Pos()).String()+" -> "+v)
				}
				return true
			})
		}
	}
	if len(offenders) > 0 {
		t.Errorf("these files write a code/event as a LITERAL instead of using the constant in "+
			"errors.go:\n  %s\n\n  Three call sites typing one value is three values as far as a monitor "+
			"is concerned, and each works perfectly on the day it is written.",
			strings.Join(offenders, "\n  "))
	}
}
