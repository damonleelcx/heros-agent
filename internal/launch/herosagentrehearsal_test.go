package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/herosagent"
)

// P30 — the activation gate, mounted in the product.

// 🔴 THE FENCE THIS WHOLE CHANGE EXISTS FOR.
//
// `herosagent.NewRehearsal` was constructed in exactly one file in the repository and that file was a
// proof binary, so no deployed path could move a published definition off `pending` and the operator
// console could never activate one. Nothing failed: the console rendered a correct refusal, the gate's
// own unit tests stayed green, and the capability was dark.
//
// This asserts the property that was missing — a NON-PROOF, non-test file constructs the rehearsal —
// rather than asserting that some particular function exists, because the second kind of test passes
// the moment somebody renames it.
func TestTheRehearsalIsConstructedOutsideTheProofBinaries(t *testing.T) {
	root := repoRootForTest(t)
	var callers []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				switch info.Name() {
				case ".git", "node_modules", "web", ".claude":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		// The proof binaries are exactly what this fence discounts: they are where the gate USED to
		// live, and a repository where they are the only callers is the repository this change fixed.
		if strings.HasPrefix(filepath.ToSlash(rel), "cmd/proof/") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if strings.Contains(string(b), "herosagent.NewRehearsal(") {
			callers = append(callers, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) == 0 {
		t.Fatal("NOTHING outside cmd/proof constructs a herosagent.Rehearsal. The activation gate is " +
			"then unreachable from any deployed path: a published definition stays `pending` for ever, " +
			"Publisher.Activate refuses it for ever, and the refusal is indistinguishable from a gate " +
			"that measured a definition and said no. That is the state this fence exists to prevent.")
	}
	t.Logf("the gate is constructed in: %v", callers)
}

// The gate refuses to exist rather than existing uselessly: no fixtures, no rehearsal, and the reason
// names the path it looked in.
func TestNoFixturesMeansNoGateAndAReason(t *testing.T) {
	t.Setenv("HEROS_CALIBRATION_ROOT", t.TempDir())
	_, err := calibrationRoot()
	if err == nil {
		t.Fatal("an empty calibration root produced a gate. A rehearsal with no fixtures measures " +
			"nothing, and a gate that measures nothing passes everything")
	}
	for _, want := range []string{"HEROS_CALIBRATION_ROOT", "go_chain", "refusing to activate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so a reader cannot act on it: %v", want, err)
		}
	}

	// And the builder refuses to hand back a gate built on it.
	if _, berr := newAgentRehearsal(agentRehearsalConfig{}); berr == nil {
		t.Error("newAgentRehearsal built a gate from an empty config")
	}
}

// 🔴 The IMAGE has to carry the fixtures, and the Dockerfile is the only place that can be true.
//
// The gate resolves `defaultCalibrationRoot` at runtime. If the image does not carry the trees, the
// deployment boots, logs one line and refuses every activation — which is safe and looks exactly like
// a deployment nobody has published a definition on. This asserts the two COPY lines exist and that
// they land at the path the code reads.
func TestTheAgentdImageCarriesTheCalibrationFixtures(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRootForTest(t), "deploy", "Dockerfile.agentd"))
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	// Both trees. The set loads WHOLE or fails, so copying one of the two is a set that cannot load.
	for _, want := range []string{
		"/internal/herosagent/testdata " + defaultCalibrationRoot + "/internal/herosagent/testdata",
		"/internal/discovery/testdata " + defaultCalibrationRoot + "/internal/discovery/testdata",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile.agentd does not copy %q. Without it the deployed gate finds no "+
				"fixtures, builds nothing, and every activation is refused with no measurement — the "+
				"exact failure that is invisible unless somebody reads the boot log", want)
		}
	}
}

// The fixture root the code looks in and the root the image writes to are ONE constant. A test that
// hard-coded the path would keep passing when the constant moved.
func TestTheCalibrationRootIsOneValue(t *testing.T) {
	if !strings.HasPrefix(defaultCalibrationRoot, "/") {
		t.Errorf("the default calibration root %q is not absolute; a relative root resolves against "+
			"the process's working directory, which is /app in the image", defaultCalibrationRoot)
	}
	// The floors the gate reads are the package's, not a second copy here.
	if herosagent.DefaultMinPrecision <= 0 || herosagent.DefaultMinRecall <= 0 {
		t.Error("a zero floor passes everything")
	}
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
