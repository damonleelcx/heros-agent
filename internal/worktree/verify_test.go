package worktree

// Tests for the verification-strength gate (ADR-003).
//
// # What runs here, and why these and not others
//
// This file is compiled by `make go`, so it may only use toolchains `make ci` ALREADY requires: Go
// (obviously) and python3 (`make schema` and `make discovery-ci` both shell out to it). The Rust,
// Java, Kotlin, TypeScript and JavaScript gates need toolchains CI does not currently install, so
// their live-execution proofs sit behind the `verifiers` build tag in verify_toolchain_test.go —
// exactly the arrangement `pgproof` already uses for the live-Postgres proofs, and for the same
// reason: with the dependency absent they FAIL rather than skip.
//
// The missing-toolchain tests need no toolchain at all — they need an ABSENCE, which an empty PATH
// produces identically on every machine. They are therefore in the always-run set, which is where the
// most safety-critical assertion in the package belongs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The rule (ADR-003 decision 5)
// ─────────────────────────────────────────────────────────────────────────────────────────────────

func TestStrength_AutonomousApplyRequiresTypeChecked(t *testing.T) {
	for _, tc := range []struct {
		strength   Strength
		autonomous bool
		valid      bool
	}{
		{StrengthTypeChecked, true, true},
		{StrengthSyntaxChecked, false, true},
		// The zero value is the one that matters. A gate that did not say what it proved must not
		// thereby be granted the strongest guarantee — which is exactly what would happen if this
		// returned true, or if the field had a default anywhere.
		{Strength(""), false, false},
		{Strength("type-checked "), false, false},
		{Strength("probably-fine"), false, false},
	} {
		t.Run(string(tc.strength), func(t *testing.T) {
			if got := tc.strength.AllowsAutonomousApply(); got != tc.autonomous {
				t.Errorf("AllowsAutonomousApply() = %v, want %v", got, tc.autonomous)
			}
			if got := tc.strength.RequiresHumanReview(); got == tc.autonomous {
				t.Errorf("RequiresHumanReview() = %v; it must be the exact inverse", got)
			}
			if got := tc.strength.Valid(); got != tc.valid {
				t.Errorf("Valid() = %v, want %v", got, tc.valid)
			}
		})
	}
}

// The dispatch table must cover every language Discovery has a frontend for. If it does not, that
// language's transforms are unverifiable — and the failure would show up as a submission error for a
// customer, not as a red test here.
func TestVerifierFor_CoversEveryDiscoveryLanguage(t *testing.T) {
	// Discovery's labels, verbatim (internal/discovery: Workflow.Language / frontendsByLanguage).
	for _, lang := range []string{"go", "python", "typescript", "javascript", "rust", "java", "kotlin"} {
		v, err := VerifierFor(lang)
		if err != nil {
			t.Errorf("VerifierFor(%q): %v", lang, err)
			continue
		}
		if v == nil {
			t.Errorf("VerifierFor(%q) returned no verifier", lang)
		}
	}
}

// An unknown language is an error, never a permissive default. A "verify nothing, call it
// syntax-checked" branch here would let a language we have never gated ship a diff carrying a claim.
func TestVerifierFor_UnknownLanguageIsAnError(t *testing.T) {
	_, err := VerifierFor("cobol")
	if !errors.Is(err, ErrLanguageNotVerifiable) {
		t.Fatalf("want ErrLanguageNotVerifiable, got %v", err)
	}
	if !strings.Contains(err.Error(), "go") {
		t.Errorf("the error should tell the caller what IS verifiable, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// 🔴 THE critical test — the entire point of ADR-003
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// pyTypeError is the ADR's own example, made executable.
//
// ADR-003: "A rewrite that changes `model="x"` to `modle="x"` compiles perfectly and fails at runtime
// — or worse, silently takes a different code path."
//
// It PARSES (py_compile is happy). It is NOT well-typed (mypy/pyright reject it). It BLOWS UP when
// run. That gap between "parses" and "is correct" is the entire reason `Strength` exists.
const pyTypeError = `def call_model(model: str, prompt: str) -> str:
    return model + ":" + prompt


# The codemod's rewrite: the keyword was misspelled. Syntactically flawless.
print(call_model(modle="claude-sonnet-5", prompt="triage this ticket"))
`

// TestPython_TypeErrorPassesPyCompileAndIsRecordedAsSyntaxChecked is the assertion this whole change
// exists to make true.
//
// It proves, in one run against the real interpreter, all four halves of the ADR's argument:
//
//  1. py_compile PASSES a program that is not well-typed. The gate is genuinely weaker — not
//     theoretically, not "in principle": here is the code and here is the pass.
//  2. We record it as `syntax-checked`, NOT `type-checked`. The engine does not claim what it did not
//     prove.
//  3. The program really is broken — running it raises TypeError. So the thing the gate missed is a
//     real defect that would reach a customer, not a pedantic annotation quibble.
//  4. Because it is `syntax-checked`, it can never be auto-applied (decision 5). The weaker gate does
//     not silently become the customer's problem; it becomes a human's review.
//
// If someone later "simplifies" the gate back to a boolean, or defaults an unset strength to
// type-checked, this test goes red at (2) and (4).
func TestPython_TypeErrorPassesPyCompileAndIsRecordedAsSyntaxChecked(t *testing.T) {
	requireBin(t, "python3")
	dir := t.TempDir()
	write(t, dir, "pipeline.py", pyTypeError)

	logs := &bytes.Buffer{}
	v, err := PythonVerifier{Log: bufLogger(logs)}.Verify(context.Background(), dir)

	// (1) The gate PASSED code that is not well-typed.
	if err != nil {
		t.Fatalf("py_compile rejected code that parses — the premise of this test is broken: %v\n%s",
			err, v.Log)
	}

	// (2) And it said so honestly.
	if v.Strength != StrengthSyntaxChecked {
		t.Fatalf("strength = %q, want %q.\n\nThis is the ADR-003 failure in its exact form: a type "+
			"error just passed the gate, and the record would tell a reviewer a compiler stood behind "+
			"the diff.", v.Strength, StrengthSyntaxChecked)
	}
	if !strings.Contains(v.Tool, "py_compile") {
		t.Errorf("Tool = %q, want it to name py_compile — evidence travels with the claim", v.Tool)
	}

	// (3) The program the gate passed is really broken. Run it.
	out, runErr := exec.Command("python3", filepath.Join(dir, "pipeline.py")).CombinedOutput()
	if runErr == nil {
		t.Fatalf("the fixture was supposed to fail at runtime but ran fine:\n%s", out)
	}
	if !strings.Contains(string(out), "TypeError") {
		t.Fatalf("want a TypeError proving the gate missed a REAL defect, got:\n%s", out)
	}
	t.Logf("py_compile PASSED this program. Running it:\n%s", strings.TrimSpace(string(out)))

	// (4) So it is human-reviewed, at every automation level. Forever.
	if v.Strength.AllowsAutonomousApply() {
		t.Error("a syntax-checked transform is autonomously appliable; ADR-003 decision 5 says a type " +
			"error like this one would then reach a customer with nobody in the loop")
	}

	// And the fallback is not silent (禁止静默回落默认值).
	assertFallbackWARN(t, logs, "no pyright or mypy")
}

// The same verifier, the same language, a rewrite that is genuinely fine: still syntax-checked,
// because the REPOSITORY offers nothing stronger. The strength describes the gate, not the outcome.
func TestPython_ValidRewritePassesAndIsStillOnlySyntaxChecked(t *testing.T) {
	requireBin(t, "python3")
	dir := t.TempDir()
	write(t, dir, "pipeline.py", `def call_model(model: str, prompt: str) -> str:
    return model + ":" + prompt


print(call_model(model="claude-sonnet-5", prompt="triage this ticket"))
`)
	v, err := PythonVerifier{Log: discard()}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("a valid rewrite was rejected: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthSyntaxChecked {
		t.Errorf("strength = %q, want syntax-checked", v.Strength)
	}
}

// 围栏必须能红: the gate must actually reject something. A gate that passes everything is decoration.
func TestPython_BrokenSyntaxIsRejected(t *testing.T) {
	requireBin(t, "python3")
	dir := t.TempDir()
	write(t, dir, "pipeline.py", "def call_model(model:\n    return model +\n")

	v, err := PythonVerifier{Log: discard()}.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("py_compile accepted a file that does not parse")
	}
	if v.Strength != StrengthSyntaxChecked {
		t.Errorf("strength = %q; a rejection must still say WHICH gate rejected it", v.Strength)
	}
	if !strings.Contains(v.Log, "SyntaxError") {
		t.Errorf("the reason must survive into the record, got: %q", v.Log)
	}
}

// py_compile writes .pyc files. They must not land in the worktree, or task 3.10's "no residue"
// property is false: `git status` is dirty and a revert leaves them behind. Same rule as GoVerifier's
// `-o <tmp>`, and it was a real bug there.
func TestPython_LeavesNoResidueInTheWorktree(t *testing.T) {
	requireBin(t, "python3")
	dir := t.TempDir()
	write(t, dir, "pipeline.py", "x = 1\n")
	if _, err := (PythonVerifier{Log: discard()}).Verify(context.Background(), dir); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var residue []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error {
		if rel, _ := filepath.Rel(dir, p); rel != "." && rel != "pipeline.py" {
			residue = append(residue, rel)
		}
		return nil
	})
	if len(residue) > 0 {
		t.Errorf("the gate dirtied the worktree with %v; a revert would leave them behind", residue)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// 🔴 The distinction the ADR calls the difference between an ops problem and a safety problem
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// A repository that CONFIGURES a type checker the worker does not have must fail loudly.
//
// This is the failure ADR-003 spends a paragraph on, and it is quiet and total: pyright is missing
// from the image, every Python transform records `syntax-checked`, every one is (correctly!) held for
// human review, and the platform's strongest guarantee has evaporated for its largest market with no
// error anywhere. The only evidence, months later, is a column full of the weaker value.
//
// Each subtest is one of the five ways a repo can ask for a checker. All five must reach the SAME
// loud failure, because a config shape we forgot to read is a config shape that silently downgrades.
func TestPython_ConfiguredTypeCheckerMissingFromTheWorkerIsLoudNotADowngrade(t *testing.T) {
	for _, tc := range []struct{ name, file, content, wantTool string }{
		{"pyrightconfig.json", "pyrightconfig.json", `{"include": ["."]}`, "pyright"},
		{"pyproject [tool.pyright]", "pyproject.toml", "[tool.pyright]\ninclude = [\".\"]\n", "pyright"},
		{"mypy.ini", "mypy.ini", "[mypy]\nstrict = true\n", "mypy"},
		{"pyproject [tool.mypy]", "pyproject.toml", "[tool.mypy]\nstrict = true\n", "mypy"},
		{"setup.cfg [mypy]", "setup.cfg", "[mypy]\nstrict = true\n", "mypy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "pipeline.py", pyTypeError)
			write(t, dir, tc.file, tc.content)

			logs := &bytes.Buffer{}
			// An empty PATH is a worker with no tools. Not a mock of anything: the tool is genuinely
			// unresolvable, which is what a container missing the package actually looks like.
			v, err := PythonVerifier{Env: emptyPATH(), Log: bufLogger(logs)}.Verify(context.Background(), dir)

			var te *ToolchainError
			if !errors.As(err, &te) {
				t.Fatalf("a repo configuring %s on a worker without it returned err=%v, strength=%q.\n\n"+
					"ADR-003: a missing toolchain must fail loudly, never silently degrade to "+
					"syntax-checked — that would turn an ops problem into a SAFETY problem.",
					tc.wantTool, err, v.Strength)
			}
			if !errors.Is(err, ErrToolchainUnavailable) {
				t.Errorf("the error must be identifiable as a toolchain failure by the caller: %v", err)
			}
			if te.Tool != tc.wantTool {
				t.Errorf("Tool = %q, want %q — the operator has to know what to install", te.Tool, tc.wantTool)
			}
			if !strings.Contains(te.Required, tc.wantTool) {
				t.Errorf("Required = %q; it must say the REPOSITORY asked for this tool, which is a "+
					"different fix from a tool we asked for", te.Required)
			}

			// The two failure modes it must NOT have degraded into:
			if v.Strength != "" {
				t.Errorf("strength = %q; a run that never verified anything must return NO verdict, so "+
					"nothing downstream can record one", v.Strength)
			}
			if strings.Contains(logs.String(), "weaker gate") {
				t.Error("it emitted the legitimate-fallback WARN for a MISSING TOOLCHAIN; that is the " +
					"exact confusion the ADR forbids — the two look identical in the log and are opposite " +
					"in meaning")
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// F2 — an interpreter that runs fine and cannot read the language is ALSO a missing toolchain
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// pyMatchStatement is 3.10+ syntax. A 3.9 interpreter reports it as a SyntaxError — the shape of the
// real failure, where hermes-agent's tools/mcp_tool.py:1891 was blamed for a `match` statement.
const pyMatchStatement = `def handle(message):
    match message:
        case {"role": role}:
            return role
        case _:
            return None
`

// TestPython_InterpreterOlderThanTheRepoRequiresIsAnOpsErrorNotABuildRejection is the F2 fence.
//
// # What actually happened
//
// The worker's default python3 is 3.9.6. hermes-agent declares `requires-python = ">=3.11"` and uses
// `match`. requireTool asked `python3 --version`, 3.9.6 answered perfectly, and the gate proceeded to
// py_compile 3,055 files with an interpreter that cannot read the language. The user was told:
//
//	"This transform does not build, so it was never proposed and never run"
//	  — citing tools/mcp_tool.py:1891, a file the diff never touches, which is valid Python.
//	  rejected_node_id was empty, because no rewrite was responsible. Nothing was.
//
// Every clause of that is false, and it is precisely what ADR-003 forbids: "a missing toolchain must
// fail loudly, never silently degrade — that would turn an ops problem into a safety problem." An
// interpreter too old to parse the source IS a missing toolchain; it merely fails a different way
// than being absent from PATH, and the ADR's rule is about the outcome, not the mechanism.
//
// # Why this is a REAL interpreter and not a stub (禁止 Mock)
//
// It probes the box's actual python3, then writes a pyproject.toml declaring a floor ABOVE whatever
// answered. Real interpreter, real version probe, real PEP 621 declaration, real comparison — and
// deterministic on any machine, whether its python3 is 3.9 or 3.14. A stub printing "Python 3.9.6"
// would have proved the stub.
func TestPython_InterpreterOlderThanTheRepoRequiresIsAnOpsErrorNotABuildRejection(t *testing.T) {
	have := livePythonVersion(t)
	dir := t.TempDir()
	write(t, dir, "pipeline.py", pyMatchStatement)
	// The repository asks for one minor version newer than this worker has. This is the customer's
	// legitimate, standard declaration — the same clause hermes-agent ships.
	write(t, dir, "pyproject.toml", fmt.Sprintf(
		"[project]\nname = \"x\"\nrequires-python = \">=%d.%d\"\n", have.major, have.minor+1))

	logs := &bytes.Buffer{}
	v, err := PythonVerifier{Log: bufLogger(logs)}.Verify(context.Background(), dir)

	var te *ToolchainError
	if !errors.As(err, &te) {
		t.Fatalf("an interpreter older than the repository declares it needs returned err=%v, "+
			"strength=%q, want a *ToolchainError.\n\n"+
			"Without this the SyntaxError from a file the diff never touched is recorded as "+
			"`build-rejected` and the user is told THEIR transform does not build. That is an ops "+
			"problem laundered into a verdict about the customer's code (ADR-003).", err, v.Strength)
	}
	if !errors.Is(err, ErrToolchainUnavailable) {
		t.Errorf("the caller must be able to identify this as a toolchain failure: %v", err)
	}

	// 🔴 The verdict, and its absence, is the whole point. A *ToolchainError is not a statement about
	// the customer's code, so there must be NO strength to record and no row to write.
	if v.Strength != "" {
		t.Errorf("strength = %q; a gate that never ran must return no verdict, so nothing downstream "+
			"can record one", v.Strength)
	}

	// The operator has to be able to act on this without reading our source.
	if te.Language != "python" || te.Tool == "" {
		t.Errorf("Language=%q Tool=%q; the error must name what to fix", te.Language, te.Tool)
	}
	msg := te.Error()
	for _, want := range []string{
		have.String(), // what the worker HAS
		fmt.Sprintf("%d.%d", have.major, have.minor+1), // what the repo NEEDS
		"pyproject.toml", // where that requirement is declared
		"THIS WORKER",    // whose problem it is
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the ops error does not mention %q — the operator must not have to guess.\ngot: %s",
				want, msg)
		}
	}

	// 🚫 And it must not have degraded instead. Both of the wrong outcomes, asserted apart:
	if strings.Contains(logs.String(), "weaker gate") {
		t.Error("it emitted the legitimate-fallback WARN for a broken toolchain; the two look identical " +
			"in a log and are opposite in meaning (ADR-003 decision 4 vs the ops rule)")
	}
	if strings.Contains(msg, "SyntaxError") || strings.Contains(v.Log, "SyntaxError") {
		t.Errorf("the failure quotes a SyntaxError from the customer's source; the gate must not have "+
			"run at all.\ngot: %s", msg)
	}
}

// The other direction, and it is not a formality: a gate that refuses everything is as useless as one
// that refuses nothing. An interpreter that MEETS the declared floor must verify normally, at the
// strength ADR-003 gives a Python repo with no type checker.
func TestPython_InterpreterMeetingTheDeclaredFloorVerifiesNormally(t *testing.T) {
	have := livePythonVersion(t)
	dir := t.TempDir()
	write(t, dir, "pipeline.py", "x = 1\n")
	// Declares exactly what this worker has — the boundary case, where `less` must not fire.
	write(t, dir, "pyproject.toml", fmt.Sprintf(
		"[project]\nname = \"x\"\nrequires-python = \">=%s\"\n", have.String()))

	v, err := PythonVerifier{Log: bufLogger(&bytes.Buffer{})}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("an interpreter that satisfies the declared floor must verify, not refuse: %v", err)
	}
	if v.Strength != StrengthSyntaxChecked {
		t.Errorf("strength = %q, want %q", v.Strength, StrengthSyntaxChecked)
	}
}

// A repository that declares nothing is the common case and must be left alone: there is no claim to
// check, and inventing a floor would be a lazy default with the power to block a submission.
func TestPython_RepoDeclaringNoFloorIsNotGated(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pipeline.py", "x = 1\n")

	v, err := PythonVerifier{Log: bufLogger(&bytes.Buffer{})}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("a repo with no requires-python must not be gated on a version it never asked for: %v", err)
	}
	if v.Strength != StrengthSyntaxChecked {
		t.Errorf("strength = %q, want %q", v.Strength, StrengthSyntaxChecked)
	}
}

// pythonRequires against the declaration shapes that actually appear in the wild — including the
// verbatim clause hermes-agent ships, whose upper bound must NOT be read as a floor.
func TestPythonRequires_ReadsTheFloorAndIgnoresTheCeiling(t *testing.T) {
	for _, tc := range []struct {
		name, file, content string
		want                string // "" => no floor found
	}{
		{"hermes-agent verbatim", "pyproject.toml",
			"[project]\nname = \"hermes-agent\"\nrequires-python = \">=3.11,<3.14\"\n", "3.11"},
		{"floor only", "pyproject.toml", "[project]\nrequires-python = \">=3.9\"\n", "3.9"},
		{"setup.cfg", "setup.cfg", "[options]\npython_requires = >=3.8\n", "3.8"},
		{"no declaration", "pyproject.toml", "[project]\nname = \"x\"\n", ""},
		// 🔴 A ceiling alone is NOT a floor. Reading "<3.14" as "3.14" would invert the gate: every
		// worker below 3.14 would be refused, for a repo that asked for no minimum at all.
		{"ceiling only", "pyproject.toml", "[project]\nrequires-python = \"<3.14\"\n", ""},
		// An exotic spelling we do not parse yields nothing rather than a guess: blocking a submission
		// over a pin format is the same over-reach pointed the other way.
		{"compatible-release", "pyproject.toml", "[project]\nrequires-python = \"~=3.11\"\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tc.file, tc.content)
			v, source, found := pythonRequires(dir)
			if tc.want == "" {
				if found {
					t.Fatalf("read a floor of %s from %q, but this declares none — a floor we invent can "+
						"block a submission the repository never asked us to block", v, tc.content)
				}
				return
			}
			if !found {
				t.Fatalf("no floor read from %q, want %s", tc.content, tc.want)
			}
			if v.String() != tc.want {
				t.Errorf("floor = %s, want %s (from %q)", v, tc.want, tc.content)
			}
			if !strings.Contains(source, tc.file) {
				t.Errorf("source = %q, want it to name %s so the operator knows where the requirement "+
					"came from", source, tc.file)
			}
		})
	}
}

// livePythonVersion asks the box's real python3 what it is. `make go` already requires python3
// (`make schema`, `make discovery-ci`), so this is a toolchain the always-run set may rely on.
func livePythonVersion(t *testing.T) version {
	t.Helper()
	out, err := exec.Command("python3", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("python3 --version: %v (%s)", err, out)
	}
	v, ok := parseVersion(string(out))
	if !ok {
		t.Fatalf("could not read a version out of %q", out)
	}
	return v
}

// Every gate, not just Python's: a missing toolchain is a *ToolchainError with no verdict.
//
// Table-driven over all seven, because "we handled it in the one we were thinking about" is how the
// sixth verifier ships with a silent fallback. Note this test needs no toolchain to be PRESENT — it
// needs them absent, which an empty PATH guarantees on every machine, including CI.
func TestEveryVerifier_MissingToolchainIsLoudAndProducesNoVerdict(t *testing.T) {
	dir := t.TempDir()
	// One source file per language, so no verifier bails out with "nothing to verify" before it ever
	// reaches its toolchain lookup — that would make this test pass for the wrong reason.
	write(t, dir, "go.mod", "module x\n\ngo 1.22\n")
	write(t, dir, "x.go", "package x\n")
	write(t, dir, "Cargo.toml", "[package]\nname = \"x\"\nversion = \"0.1.0\"\n")
	write(t, dir, "src/lib.rs", "pub fn x() {}\n")
	write(t, dir, "X.java", "class X {}\n")
	write(t, dir, "X.kt", "fun x() {}\n")
	write(t, dir, "x.ts", "export const x = 1;\n")
	write(t, dir, "x.js", "export const x = 1;\n")
	write(t, dir, "x.py", "x = 1\n")

	for _, tc := range []struct {
		lang string
		v    Verifier
		tool string
	}{
		{"go", GoVerifier{Env: emptyPATH()}, "go"},
		{"rust", RustVerifier{Env: emptyPATH()}, "cargo"},
		{"java", JavaVerifier{Env: emptyPATH()}, "javac"},
		{"kotlin", KotlinVerifier{Env: emptyPATH()}, "kotlinc"},
		{"typescript", TypeScriptVerifier{Env: emptyPATH()}, "tsc"},
		{"javascript", JavaScriptVerifier{Env: emptyPATH()}, "node"},
		{"python", PythonVerifier{Env: emptyPATH()}, "python3"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			v, err := tc.v.Verify(context.Background(), dir)
			var te *ToolchainError
			if !errors.As(err, &te) {
				t.Fatalf("%s with no toolchain: err=%v strength=%q, want a *ToolchainError",
					tc.lang, err, v.Strength)
			}
			if te.Tool != tc.tool {
				t.Errorf("Tool = %q, want %q", te.Tool, tc.tool)
			}
			if te.Language != tc.lang {
				t.Errorf("Language = %q, want %q", te.Language, tc.lang)
			}
			if v.Strength != "" {
				t.Errorf("strength = %q, want none: nothing was proved", v.Strength)
			}
			if !strings.Contains(te.Error(), "install") {
				t.Errorf("the error must tell the operator what to do: %q", te.Error())
			}
		})
	}
}

// TypeScript's ordering trap, pinned. A repo with no tsconfig takes the syntax-checked fallback — so
// if the toolchain check came second, a MISSING tsc would surface as a legitimate-looking downgrade
// instead of an error. It is one `if` away at all times, so it gets its own fence.
func TestTypeScript_MissingToolchainOutranksTheMissingTsconfigFallback(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "x.ts", "export const x: number = 1;\n") // deliberately NO tsconfig.json
	logs := &bytes.Buffer{}
	v, err := TypeScriptVerifier{Env: emptyPATH(), Log: bufLogger(logs)}.Verify(context.Background(), dir)
	if !errors.Is(err, ErrToolchainUnavailable) {
		t.Fatalf("err = %v, strength = %q; a missing tsc must not hide behind the no-tsconfig fallback",
			err, v.Strength)
	}
	if v.Strength != "" {
		t.Errorf("strength = %q, want none", v.Strength)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Go — the gate that was already here, now saying what it always proved
// ─────────────────────────────────────────────────────────────────────────────────────────────────

func TestGo_WellTypedProgramIsTypeChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/x\n\ngo 1.22\n")
	write(t, dir, "pipeline.go", "package x\n\ntype Model string\n\nfunc Call(m Model) string { return string(m) }\n\nvar _ = Call(Model(\"claude-sonnet-5\"))\n")

	v, err := GoVerifier{GoBin: goBin()}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("a well-typed program was rejected: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q, want type-checked: `go build` IS a full type check", v.Strength)
	}
}

// The contrast that makes `Strength` mean something — and it is deliberately the SAME DEFECT, not
// merely a similar one.
//
// The Python fixture misspells a keyword argument: `call_model(modle=...)`. This is that, in Go: a
// misspelled field in a struct literal, `Params{Modle: ...}`. Identical mistake, identical cause (a
// codemod that rewrote a name wrongly), identical runtime consequence — and Go's gate catches it at
// build time while Python's py_compile cannot see it at all.
//
// That is ADR-003's entire thesis, and both halves of it run against real toolchains in this one
// package: the same bug, one language away, and the guarantee differs by an order of magnitude. Which
// is precisely why `built` could not be allowed to keep meaning both.
func TestGo_TypeErrorIsRejected_TheSameDefectPythonLetsThrough(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/x\n\ngo 1.22\n")
	write(t, dir, "pipeline.go", `package x

type Model string

type Params struct {
	Model  Model
	Prompt string
}

func Call(p Params) string { return string(p.Model) + ":" + p.Prompt }

// The codemod's rewrite: the field name was misspelled. The exact defect the Python fixture has.
var _ = Call(Params{Modle: "claude-sonnet-5", Prompt: "triage this ticket"})
`)

	v, err := GoVerifier{GoBin: goBin()}.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("go build accepted a misspelled struct field")
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q; a rejection still names the gate that rejected it", v.Strength)
	}
	if !strings.Contains(v.Log, "Modle") {
		t.Errorf("the compiler's reason must survive into the record, got: %q", v.Log)
	}
	t.Logf("go build CAUGHT the misspelled name that Python's py_compile gate let through:\n%s",
		strings.TrimSpace(v.Log))
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Apply — the seam where a toolchain failure must never become a verdict
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// toolchainlessVerifier is a worker with a tool missing. It fakes no toolchain and no verdict — it
// returns exactly what the seven real verifiers return when a lookup fails, which is the condition
// under test.
type toolchainlessVerifier struct{}

func (toolchainlessVerifier) Verify(context.Context, string) (Verification, error) {
	return Verification{}, &ToolchainError{Language: "python", Tool: "pyright",
		Required: "the repository configures pyright (pyrightconfig.json)", Reason: "executable file not found in $PATH"}
}

// A missing toolchain must not be recorded as "your transform does not build".
//
// That would be a lie about the user's spec, and an expensive one: it sends them to fix a codemod that
// was never the problem, for a container that was missing a package. Worse, the row is IMMUTABLE — the
// false rejection would be permanent, and the cache would keep serving it long after the operator
// installed the tool.
func TestApply_MissingToolchainIsNotABuildRejection(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	a := NewApplier(pool, toolchainlessVerifier{}, cache)

	applied, err := a.Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if !errors.Is(err, ErrToolchainUnavailable) {
		t.Fatalf("err = %v, want a toolchain failure the caller can tell apart from a rejection", err)
	}
	var rej *BuildRejection
	if errors.As(err, &rej) {
		t.Error("our missing toolchain was reported as a rejection of the USER's transform")
	}
	// submit.Submit keys on exactly this: a nil Applied is "no terminal build state at all", which it
	// surfaces as a server error. A non-nil one would be recorded.
	if applied != nil {
		t.Errorf("Apply returned an Applied (status %q, strength %q); an unverified transform must not "+
			"reach the record at all", applied.Status, applied.Strength)
	}
	// And nothing was cached, so installing the tool fixes it — rather than the fleet serving a
	// permanent false rejection out of the cache.
	if e := cache.Get(hashA, rev); e != nil {
		t.Errorf("a transform that was never verified was cached as %+v", e)
	}
}

// A gate that returns no strength and no toolchain error is a bug in that gate. Apply refuses rather
// than letting the DB's CHECK catch it four steps later, as a constraint name, long after the worktree
// that could have explained it was reused.
type muteVerifier struct{}

func (muteVerifier) Verify(context.Context, string) (Verification, error) {
	return Verification{Tool: "silence"}, nil
}

func TestApply_AGateThatDoesNotSayWhatItProvedIsRefused(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	applied, err := NewApplier(pool, muteVerifier{}, cache).
		Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err == nil {
		t.Fatalf("a gate that proved nothing produced %+v", applied)
	}
	if !strings.Contains(err.Error(), "state what it proved") {
		t.Errorf("the error should say what the gate got wrong, got: %v", err)
	}
}

// A cache hit must answer "what did the gate prove?" too. Serving a cached `built` with no strength
// would make a repeat variant unrecordable; defaulting one would make it a lie. The cache exists to
// avoid rework, never to change the answer.
func TestApply_CacheHitCarriesTheStrength(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	a := NewApplier(pool, GoVerifier{GoBin: goBin()}, cache)
	ctx := context.Background()

	first, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	second, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply (cached): %v", err)
	}
	if !second.CacheHit {
		t.Fatal("the second apply was not a cache hit; this test proves nothing")
	}
	if second.Strength != first.Strength || second.Strength != StrengthTypeChecked {
		t.Errorf("cached strength = %q, first = %q, want both type-checked",
			second.Strength, first.Strength)
	}
	if second.VerifierTool != first.VerifierTool {
		t.Errorf("cached tool = %q, want %q — a hit carries the same evidence the first run did",
			second.VerifierTool, first.VerifierTool)
	}
	// And it survives a round trip through the on-disk record, which is what a restart reads.
	raw, err := os.ReadFile(filepath.Join(root, "cache", cacheFile(hashA, rev)))
	if err != nil {
		t.Fatalf("read cache record: %v", err)
	}
	var e CacheEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Strength != StrengthTypeChecked {
		t.Errorf("the persisted cache record lost the strength: %q", e.Strength)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────────────────────────

func cacheFile(configHash, rev string) string {
	return configHash + "." + sanitize(rev) + ".json"
}

// emptyPATH is a worker with no tools on it. Not a mock: the tools are genuinely unresolvable, which
// is exactly what a container missing the package looks like to exec.
func emptyPATH() []string { return []string{"PATH=", "HOME=" + os.TempDir()} }

func bufLogger(b *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// assertFallbackWARN pins 禁止静默回落默认值: a fallback to a weaker gate must NAME what was
// unavailable. Asserting the level and the message is not enough — a WARN that says "verified with a
// weaker gate" and not WHY leaves the operator with nothing to act on, which is the same dead end as
// no WARN at all.
func assertFallbackWARN(t *testing.T, logs *bytes.Buffer, wantReason string) {
	t.Helper()
	s := logs.String()
	if !strings.Contains(s, "level=WARN") {
		t.Fatalf("the fallback to a weaker gate emitted no WARN (禁止静默回落默认值). Log:\n%s", s)
	}
	if !strings.Contains(s, "weaker gate") {
		t.Errorf("the WARN does not say a weaker gate was used:\n%s", s)
	}
	if !strings.Contains(s, wantReason) {
		t.Errorf("the WARN does not name what was unavailable (want %q) — an operator cannot act on "+
			"it. Log:\n%s", wantReason, s)
	}
	if !strings.Contains(s, "human review") {
		t.Errorf("the WARN should say what it COSTS (these transforms can never auto-apply):\n%s", s)
	}
}

func requireBin(t *testing.T, bin string) {
	t.Helper()
	// Fails rather than skips. python3 is already a hard `make ci` dependency (`make schema` and
	// `make discovery-ci` both shell out to it), so its absence is a broken environment, and a skipped
	// safety test is a test that silently stops guarding.
	if _, err := exec.LookPath(bin); err != nil {
		t.Fatalf("%s is required to run this gate's proof and is not on PATH: %v", bin, err)
	}
}
