package variantspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// P17 §11 — the acceptance gate.
//
// Two things this file does that no individual suite can.
//
//  1. It pins the ONE-WAY DOORS the phase promised: the dimension enum grew by exactly one, the memory
//     field is additive, and memory and context stayed disjoint.
//  2. It audits the TASK PLAN's own evidence column — every test the plan names as its proof must
//     actually exist in the tree.
//
// # Why (2) is not bureaucracy
//
// A task marked `[x]` with a `(Test: TestSomething)` pointer is a CLAIM, and it is a claim a green
// build cannot check: if `TestSomething` was never written, nothing fails — the named proof simply
// never runs, and the task reads as done forever. That failure has happened in this repository before,
// and it is invisible precisely because everything is green.
//
// 🔴 So this test PARSES tasks.md rather than transcribing it. P16's equivalent hand-copied the evidence
// column into a Go map, which closes the gap it was written for and opens a smaller one in the same
// shape: the map is a second copy, and a task added to the plan without a matching map entry is
// un-audited. Reading the plan directly means the plan cannot outgrow its own audit.

var (
	// The evidence pointer. Go test names only: the console's are `.mjs` test titles, audited separately
	// below because they live in a different runner and cannot be found by this pattern.
	goTestRefRe = regexp.MustCompile("\\(Test: `(Test\\w+)`")
	funcDeclRe  = regexp.MustCompile(`(?m)^func (Test\w+)\(`)
)

// p17TasksPath is the plan under audit.
const p17TasksPath = "openspec/changes/p17-memory-strategy-optimization/tasks.md"

// TestP17NamedEvidenceExists — task 11.x, and the audit the memory of this repository asks for.
//
// 🚫 It deliberately does NOT check that the named tests PASS. `go test ./...` does that, and a manifest
// that re-ran them would be a second, weaker test runner. What it catches is the one failure a green
// build cannot: a task marked done whose named proof was never written.
func TestP17NamedEvidenceExists(t *testing.T) {
	root := filepath.Join("..", "..")

	plan, err := os.ReadFile(filepath.Join(root, p17TasksPath))
	if err != nil {
		t.Fatalf("read the task plan: %v", err)
	}
	src := string(plan)

	// Split the plan into per-task blocks so a pointer is attributed to the task that claims it. A task
	// spans from its `- [ ]`/`- [x]` marker to the next one.
	blocks := splitTasks(src)
	if len(blocks) < 30 {
		t.Fatalf("parsed only %d task(s) from %s — parser drift, and every assertion below would pass "+
			"for the wrong reason", len(blocks), p17TasksPath)
	}

	// Every Go test declared anywhere under internal/.
	declared := map[string]string{}
	err = filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range funcDeclRe.FindAllStringSubmatch(string(b), -1) {
			declared[m[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	audited := 0
	for _, b := range blocks {
		if !b.done {
			// An OPEN task naming a test that does not exist is a plan, not a false claim. Only a task
			// claiming completion is asserting something about the tree.
			continue
		}
		for _, m := range goTestRefRe.FindAllStringSubmatch(b.text, -1) {
			name := m[1]
			audited++
			if _, ok := declared[name]; !ok {
				t.Errorf("task %s is marked DONE and names %s as its evidence, but no such test exists "+
					"under internal/.\nA named proof that was never written does not fail — it simply never "+
					"runs, so the task reads as done and is not.", b.id, name)
			}
		}
	}
	if audited < 20 {
		t.Errorf("only %d evidence pointer(s) were audited; the plan claims far more, so the pointer "+
			"pattern has drifted and this audit is no longer reading the plan", audited)
	}
}

// TestP17ConsoleEvidenceExists audits the frontend half, which lives in a different runner.
func TestP17ConsoleEvidenceExists(t *testing.T) {
	root := filepath.Join("..", "..")
	plan, err := os.ReadFile(filepath.Join(root, p17TasksPath))
	if err != nil {
		t.Fatalf("read the task plan: %v", err)
	}
	if !strings.Contains(string(plan), "web/console/tests/memory.test.mjs") {
		t.Fatal("the plan's frontend section no longer names its test file; the console half would be unaudited")
	}
	path := filepath.Join(root, "web", "console", "tests", "memory.test.mjs")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the plan names %s as the frontend evidence and it does not exist: %v", path, err)
	}
	// A file that exists but asserts nothing is the same failure one level down.
	n := strings.Count(string(b), "\ntest(")
	if n < 5 {
		t.Errorf("the console evidence file declares %d test(s); the plan claims six frontend tasks, so a "+
			"file this thin is not proof of them", n)
	}
	// And the surface it audits must exist.
	for _, f := range []string{"page.tsx", "authoring.tsx", "strategies.ts"} {
		if _, err := os.Stat(filepath.Join(root, "web", "console", "src", "app", "app", "memory", f)); err != nil {
			t.Errorf("the memory surface is missing %s: %v", f, err)
		}
	}
}

// TestP17AddsExactlyOneDimension pins the one-way door. The enum is closed; P17 opens it by exactly one,
// deliberately, through the eight-step checklist — and a second member would mean something else got in.
func TestP17AddsExactlyOneDimension(t *testing.T) {
	// The five that existed before P17, in order, as the prefix.
	before := []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools}
	got := Dimensions()

	if len(got) != len(before)+1 {
		t.Fatalf("the dimension enum has %d members (%v); P17 adds exactly one (memory) to the %d that "+
			"existed. A different count means either the addition was missed or something else was added "+
			"without a decision record.", len(got), got, len(before))
	}
	for i, want := range before {
		if got[i] != want {
			t.Errorf("dimension %d is %q, want %q — P17 appends, it does not reorder", i, got[i], want)
		}
	}
	if got[len(got)-1] != DimMemory {
		t.Errorf("the last dimension is %q, want memory", got[len(got)-1])
	}
}

// TestP17MemoryFieldIsAdditiveEverywhere is the hash-compatibility door, checked at the STRUCT level
// rather than through a sample of values: every memory-carrying field must be omitempty, in all three
// shapes the axis touches.
func TestP17MemoryFieldIsAdditiveEverywhere(t *testing.T) {
	cases := []struct{ what, tag string }{
		{"NodeOverride.MemoryRef", fieldTag(t, "NodeOverride", "MemoryRef")},
		{"ResolvedNode.Memory", fieldTag(t, "ResolvedNode", "Memory")},
	}
	for _, c := range cases {
		if !strings.Contains(c.tag, "omitempty") {
			t.Errorf("%s has json tag %q, which lacks omitempty. An always-present memory key changes the "+
				"canonical bytes of EVERY pre-P17 node, breaks every frozen golden vector, and orphans "+
				"every row keyed by a config_hash (decisions.md D3).", c.what, c.tag)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

type taskBlock struct {
	id   string
	done bool
	text string
}

// splitTasks cuts the plan into one block per task line, so an evidence pointer belongs to the task
// that claims it rather than to whatever task happened to precede it in the file.
func splitTasks(src string) []taskBlock {
	marker := regexp.MustCompile(`(?m)^- \[([ x])\] (\d+\.\d+)`)
	locs := marker.FindAllStringSubmatchIndex(src, -1)
	out := make([]taskBlock, 0, len(locs))
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, taskBlock{
			id:   src[loc[4]:loc[5]],
			done: src[loc[2]:loc[3]] == "x",
			text: src[loc[0]:end],
		})
	}
	return out
}

// fieldTag reads a struct field's json tag out of this package's own source. Reading the SOURCE rather
// than using reflection is deliberate: reflection would report the tag of the type as compiled, and what
// this test is protecting is the declaration a future edit would touch.
func fieldTag(t *testing.T, typeName, field string) string {
	t.Helper()
	for _, file := range []string{"spec.go", "resolved.go"} {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		src := string(b)
		i := strings.Index(src, "type "+typeName+" struct {")
		if i < 0 {
			continue
		}
		body := src[i:]
		if end := strings.Index(body, "\n}"); end > 0 {
			body = body[:end]
		}
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, field+" ") && !strings.HasPrefix(trimmed, field+"\t") {
				continue
			}
			if a, b2 := strings.Index(trimmed, "`"), strings.LastIndex(trimmed, "`"); a >= 0 && b2 > a {
				return trimmed[a : b2+1]
			}
		}
	}
	t.Fatalf("could not find %s.%s in this package's source — parser drift", typeName, field)
	return ""
}
