package transform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"

	// 🔴 A BLANK TEST IMPORT, and it is load-bearing rather than decoration.
	//
	// TestGoMemoryMaterializedOutputCompiles builds the materialized source in a temp module that requires
	// this SDK. Nothing in this repository's own packages imports it — the emission is a string until the
	// test writes it out — so `go mod tidy` sees no dependency and drops the require, and the next run of
	// that test fails with "this repo does not require anthropic-sdk-go" (which is exactly what happened,
	// and is the failure mode this import exists to prevent).
	//
	// Declaring it here keeps the module in go.mod and the package in the module cache, which is what lets
	// the compile check run OFFLINE. It is a test-only import: no production package links the SDK.
	_ "github.com/anthropics/anthropic-sdk-go"
)

// P18 §4 — the test that adding the SDK dependency was FOR.
//
// Every other assertion about the Go materializer is about bytes. This one COMPILES them, against the
// real anthropic-sdk-go, in a module of its own. It is the difference between "the emission looks right"
// and "the emission is valid Go that type-checks against the SDK it targets" — and it is the bar the
// response-conversion row had to clear before it could exist at all.
//
// 🔴 Without it, `resp.ToParam()` is a sentence someone remembered. `Message.ToParam` lives in
// messageutil.go, not message.go, and there is no `ToParam` on any other response type in the package —
// facts that were read out of the module cache rather than recalled, and that this test re-checks on
// every run by compiling the emitted call.

// goMemoryTargetSrc is a call site that CAN carry both halves: it writes its message list and assigns
// the call's result. The fixture in testdata deliberately does neither (it is a bare statement), which is
// why the refusal tests use that one and this test needs its own.
const goMemoryTargetSrc = `package target

import "github.com/anthropics/anthropic-sdk-go"

func Ask(client *anthropic.Client, question string) (*anthropic.Message, error) {
	resp, err := client.Messages.New(nil, anthropic.MessageNewParams{
		Model:    anthropic.ModelClaudeSonnet4_5,
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(question))},
	})
	return resp, err
}
`

// goMemoryTarget writes a self-contained module that builds against the real SDK.
//
// It reuses THIS repo's go.mod (renamed) and go.sum, so the build resolves to exactly the versions this
// repo already has in the module cache and runs with no network. A test that needed the network would be
// a test that fails for reasons unrelated to what it asserts.
func goMemoryTarget(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	sum, err := os.ReadFile(filepath.Join("..", "..", "go.sum"))
	if err != nil {
		t.Fatalf("read go.sum: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), sum, 0o600); err != nil {
		t.Fatal(err)
	}

	// 🔴 The fixture module inherits THIS repo's entire require graph, renamed — not a minimal
	// `require anthropic-sdk-go` line.
	//
	// A minimal go.mod is a different module, and MVS resolves it differently: with only the SDK's own
	// requirements to satisfy, it selects the LOWEST admissible version of each transitive dependency
	// (gjson pulls tidwall/pretty v1.2.0), while this repo's graph raises some of them (v1.2.1). The
	// go.sum copied above is this repo's, so the lower selections are neither in it nor in the module
	// cache — and the build runs with GOPROXY=off, on purpose, so nothing can be fetched to cover the
	// difference. That failed only on a clean machine: a developer whose cache happened to hold the
	// lower version saw it pass, and CI did not. Copying the graph makes the selection identical to the
	// one go.sum already covers, so the check stays offline and stays honest.
	repoMod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	gomod := regexp.MustCompile(`(?m)^module .*$`).ReplaceAll(repoMod, []byte("module heros.test/target"))
	if !strings.Contains(string(gomod), "module heros.test/target") {
		t.Fatal("the repo go.mod has no module line to rename; the fixture module would be unbuildable")
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), gomod, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte(goMemoryTargetSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// sdkVersion reads the SDK version THIS repo resolved, so the fixture and the row cannot target different
// generations without the mismatch showing up as a compile failure here.
func sdkVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "// indirect")))
		if len(f) >= 2 && f[0] == "github.com/anthropics/anthropic-sdk-go" {
			return f[1]
		}
	}
	t.Fatal("this repo does not require anthropic-sdk-go; the Go memory materializer's row cannot be verified")
	return ""
}

// TestGoMemoryMaterializedOutputCompiles — 🔴 the load-bearing test of §4.
func TestGoMemoryMaterializedOutputCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	root := goMemoryTarget(t)

	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	var id string
	for k := range sites {
		id = k
	}
	if id == "" {
		t.Fatal("discovery found no Go call site in the fixture; every assertion below would pass for the wrong reason")
	}

	p, err := Generate(resolvedIn("go", map[string]variantspec.ResolvedOverride{
		id: memoryOverride(t, "scratchpad"),
	}), root)
	if err != nil {
		t.Fatalf("a Go call site that writes its messages AND assigns its result must materialize: %v", err)
	}

	after := string(p.Files["target.go"])
	for _, want := range []string{
		`agentmem.Recall(`,
		`agentmem.Record(`,
		`.ToParam()`,
		`import "heros.test/target/agentmem"`,
	} {
		if !strings.Contains(after, want) {
			t.Errorf("the materialized source is missing %q:\n%s", want, after)
		}
	}
	// 🔴 The import path was READ from the target's go.mod, not assumed. A guessed one compiles nowhere.
	if strings.Contains(after, `"agentmem"`) && !strings.Contains(after, `"heros.test/target/agentmem"`) {
		t.Errorf("the import is not module-qualified, so it would not resolve:\n%s", after)
	}
	for _, want := range []string{goMemoryModulePath, goMemoryDocPath} {
		if _, ok := p.Files[want]; !ok {
			t.Errorf("the patch does not carry %s. Files: %v", want, fileNames(p.Files))
		}
	}

	// ── THE ASSERTION: write the patch out and build it against the real SDK. ──
	for path, body := range p.Files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the materialized Go source does not compile against %s:\n%s\n--- target.go ---\n%s",
			sdkVersion(t), out, after)
	}
}

// TestGoHalfMaterializableRefusedWhole — task 4.2 🔴, now with a call site that CAN carry the recall.
//
// The fixture writes its message list but discards the response into `_`, so the record half has nothing
// to name. The recall half alone must NOT ship: it would recall from a store nothing fills, which behaves
// as `none` under scratchpad's config_hash.
func TestGoHalfMaterializableRefusedWhole(t *testing.T) {
	const discarded = `package target

import "github.com/anthropics/anthropic-sdk-go"

func Ask(client *anthropic.Client, question string) {
	_, _ = client.Messages.New(nil, anthropic.MessageNewParams{
		Model:    anthropic.ModelClaudeSonnet4_5,
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(question))},
	})
}
`
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module heros.test/target\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte(discarded), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	var id string
	for k := range sites {
		id = k
	}
	if id == "" {
		t.Fatal("discovery found no Go call site")
	}

	p, err := Generate(resolvedIn("go", map[string]variantspec.ResolvedOverride{
		id: memoryOverride(t, "scratchpad"),
	}), root)
	if err == nil {
		t.Fatalf("a call site that DISCARDS its response materialized. The record half would have to name "+
			"`_`, which does not compile — and shipping the recall alone gives a memory that reads a store "+
			"nothing fills, behaving as `none` under scratchpad's hash.\ngot: %v", p)
	}
	if p != nil {
		t.Error("a refused override produced a patch")
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.Dim != string(variantspec.DimMemory) {
		t.Fatalf("refusal = %#v, want a memory RewriteError", err)
	}
	if re.Cause != CauseCallSiteShape {
		t.Errorf("cause = %q, want %q — the author can assign the result", re.Cause, CauseCallSiteShape)
	}
}
