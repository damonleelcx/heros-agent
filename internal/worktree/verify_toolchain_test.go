//go:build verifiers

// Live-toolchain proofs for the language gates (ADR-003 decision 2).
//
// # Why these are behind a tag, and why they FAIL rather than skip
//
// Every test here executes a REAL compiler or type checker against a REAL source tree. That is the
// only way to prove a gate: 禁止 Mock, and a gate proved against a fake toolchain proves the fake.
// The cost is that these need Rust, a JDK, Kotlin, Node, TypeScript and mypy actually installed —
// which `make go` does not require and must not start requiring silently.
//
// So they take the same shape `pgproof` already established for the live-Postgres proofs: their own
// build tag, their own make target (`make verifier-proof`), their own CI job, and — decisively — with
// the dependency ABSENT they FAIL rather than skip. A skipped safety proof is a proof that quietly
// stopped guarding, and the whole point of ADR-003 is that a quiet downgrade is the thing to fear.
//
// Every gate gets the same two fences, because a gate is only real if it can go both ways:
//
//	a genuine rewrite     -> PASSES, at the strength the ADR says that language earns
//	a genuinely broken one -> FAILS  (围栏必须能红: a fence that cannot go red is decoration)
//
// Install what these need:  make verifier-proof

package worktree

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// 🔴 Python, the type-checked half — the other side of the ADR's central claim
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// TestPython_ConfiguredTypeCheckerCatchesWhatPyCompileMissed closes the argument that
// verify_test.go's TestPython_TypeErrorPassesPyCompileAndIsRecordedAsSyntaxChecked opens.
//
// Same file. Same bytes. The ONLY difference is that this repository committed a mypy.ini — and that
// one file changes the platform's guarantee about the customer's diff completely:
//
//	no mypy.ini  -> py_compile PASSES the bug -> syntax-checked -> human-reviewed, forever
//	   mypy.ini  -> mypy CATCHES the bug      -> type-checked   -> may be auto-applied
//
// This is exactly what ADR-003 decision 4 means by "the strongest gate the REPOSITORY actually
// offers", and why strength could not be a property of the language: Python is both rows.
func TestPython_ConfiguredTypeCheckerCatchesWhatPyCompileMissed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pipeline.py", pyTypeError) // the identical fixture py_compile passed
	write(t, dir, "mypy.ini", "[mypy]\n")

	logs := &bytes.Buffer{}
	v, err := PythonVerifier{Log: bufLogger(logs)}.Verify(context.Background(), dir)

	if err == nil {
		t.Fatalf("mypy PASSED the misspelled keyword that py_compile passed. The two gates cannot be "+
			"equivalent — the whole ADR rests on them differing.\n%s", v.Log)
	}
	if v.Strength != StrengthTypeChecked {
		t.Fatalf("strength = %q, want type-checked: the repo configures mypy and mypy ran", v.Strength)
	}
	if !strings.Contains(v.Log, "modle") {
		t.Errorf("the type checker's reason must survive into the record, got: %q", v.Log)
	}
	// A type-checked transform is the ONLY kind the Autonomous loop may apply on its own.
	if !v.Strength.AllowsAutonomousApply() {
		t.Error("a type-checked transform is not autonomously appliable; the gate earns nothing")
	}
	// Nothing fell back, so nothing warns. A WARN here would be a false alarm — and a false alarm is
	// worse than none, because it trains people to ignore the real ones.
	if strings.Contains(logs.String(), "weaker gate") {
		t.Errorf("a fallback WARN was emitted although the strongest gate ran:\n%s", logs.String())
	}
	t.Logf("mypy CAUGHT the defect py_compile let through:\n%s", strings.TrimSpace(v.Log))
}

// The pass half: a real type checker, a rewrite that is genuinely correct, recorded type-checked.
func TestPython_ConfiguredTypeCheckerPassesAGoodRewrite(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pipeline.py", `def call_model(model: str, prompt: str) -> str:
    return model + ":" + prompt


print(call_model(model="claude-sonnet-5", prompt="triage this ticket"))
`)
	write(t, dir, "mypy.ini", "[mypy]\n")

	v, err := PythonVerifier{Log: discard()}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("mypy rejected a correct rewrite: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q, want type-checked", v.Strength)
	}
	if !strings.Contains(v.Tool, "mypy") {
		t.Errorf("Tool = %q, want it to name mypy — evidence travels with the claim", v.Tool)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Rust — cargo check -> type-checked
// ─────────────────────────────────────────────────────────────────────────────────────────────────

func rustCrate(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", "[package]\nname = \"pipeline\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[dependencies]\n")
	write(t, dir, "src/lib.rs", body)
	return dir
}

func TestRust_WellTypedCrateIsTypeChecked(t *testing.T) {
	dir := rustCrate(t, `pub struct Params { pub model: String, pub prompt: String }

pub fn call_model(p: Params) -> String { format!("{}:{}", p.model, p.prompt) }

pub fn run() -> String {
    call_model(Params { model: "claude-sonnet-5".to_string(), prompt: "triage".to_string() })
}
`)
	v, err := RustVerifier{}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("cargo check rejected a well-typed crate: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q, want type-checked", v.Strength)
	}
}

// The same defect as the Python and Go fixtures — a misspelled field — caught by a third type checker.
func TestRust_TypeErrorIsRejected(t *testing.T) {
	dir := rustCrate(t, `pub struct Params { pub model: String, pub prompt: String }

pub fn call_model(p: Params) -> String { format!("{}:{}", p.model, p.prompt) }

pub fn run() -> String {
    call_model(Params { modle: "claude-sonnet-5".to_string(), prompt: "triage".to_string() })
}
`)
	v, err := RustVerifier{}.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("cargo check accepted a misspelled struct field")
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q; a rejection still names the gate that rejected it", v.Strength)
	}
	if !strings.Contains(v.Log, "modle") {
		t.Errorf("rustc's reason must survive into the record, got: %q", v.Log)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Java — javac (no build tool) -> type-checked
// ─────────────────────────────────────────────────────────────────────────────────────────────────

func TestJava_WellTypedProgramIsTypeChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Pipeline.java", `class Pipeline {
    static String callModel(String model, String prompt) { return model + ":" + prompt; }
    public static void main(String[] a) { System.out.println(callModel("claude-sonnet-5", "triage")); }
}
`)
	v, err := JavaVerifier{}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("javac rejected a well-typed program: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q, want type-checked", v.Strength)
	}
}

func TestJava_TypeErrorIsRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Pipeline.java", `class Pipeline {
    static String callModel(String model, int retries) { return model + ":" + retries; }
    // The codemod's rewrite: a string where an int belongs.
    public static void main(String[] a) { System.out.println(callModel("claude-sonnet-5", "triage")); }
}
`)
	v, err := JavaVerifier{}.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("javac accepted a String where an int was required")
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q; a rejection still names the gate", v.Strength)
	}
	if !strings.Contains(v.Log, "incompatible types") {
		t.Errorf("javac's reason must survive into the record, got: %q", v.Log)
	}
}

// javac writes .class files. Like GoVerifier's `-o <tmp>` and PythonVerifier's PYTHONPYCACHEPREFIX,
// they must not land in the worktree, or task 3.10's "no residue" property is false.
func TestJava_LeavesNoResidueInTheWorktree(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Pipeline.java", "class Pipeline { }\n")
	if _, err := (JavaVerifier{}).Verify(context.Background(), dir); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	got, err := sourceFiles(dir, ".class")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) > 0 {
		t.Errorf("javac dirtied the worktree with %v; a revert would leave them behind", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Kotlin — kotlinc (no build tool) -> type-checked
// ─────────────────────────────────────────────────────────────────────────────────────────────────

func TestKotlin_WellTypedProgramIsTypeChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Pipeline.kt", `data class Params(val model: String, val prompt: String)

fun callModel(p: Params): String = "${p.model}:${p.prompt}"

val out = callModel(Params(model = "claude-sonnet-5", prompt = "triage"))
`)
	v, err := KotlinVerifier{}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("kotlinc rejected a well-typed program: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q, want type-checked", v.Strength)
	}
}

// The same misspelled-name defect again, on the seventh gate.
func TestKotlin_TypeErrorIsRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Pipeline.kt", `data class Params(val model: String, val prompt: String)

fun callModel(p: Params): String = "${p.model}:${p.prompt}"

val out = callModel(Params(modle = "claude-sonnet-5", prompt = "triage"))
`)
	v, err := KotlinVerifier{}.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("kotlinc accepted a misspelled named argument")
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q; a rejection still names the gate", v.Strength)
	}
	if !strings.Contains(v.Log, "modle") {
		t.Errorf("kotlinc's reason must survive into the record, got: %q", v.Log)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// JavaScript — node --check -> syntax-checked, and that is the ceiling
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// The JS counterpart of the Python critical test, and the reason JavaScript can NEVER do better: the
// language has no type system, so there is no configuration a repository could add that would let this
// bug be caught statically. `node --check` is not a fallback here — it is the strongest gate that
// exists.
//
// Note what is NOT asserted: no WARN. Nothing was unavailable and there is nothing for an operator to
// install, so a WARN would be a false alarm.
func TestJavaScript_TypeErrorPassesAndIsRecordedAsSyntaxChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pipeline.js", `function callModel({ model, prompt }) {
  return model + ":" + prompt;
}

// The codemod's rewrite: the key was misspelled. Nothing can catch this in JavaScript.
module.exports = callModel({ modle: "claude-sonnet-5", prompt: "triage" });
`)
	logs := &bytes.Buffer{}
	v, err := JavaScriptVerifier{Log: bufLogger(logs)}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("node --check rejected a file that parses: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthSyntaxChecked {
		t.Fatalf("strength = %q, want syntax-checked: JavaScript has no type system to check against",
			v.Strength)
	}
	if v.Strength.AllowsAutonomousApply() {
		t.Error("a JavaScript transform is autonomously appliable; nothing proved this diff is correct")
	}
	if strings.Contains(logs.String(), "weaker gate") {
		t.Errorf("a fallback WARN was emitted for JavaScript. Nothing was unavailable — this is the "+
			"strongest gate that exists — and a false alarm trains operators to ignore real ones:\n%s",
			logs.String())
	}
}

func TestJavaScript_BrokenSyntaxIsRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pipeline.js", "function callModel({ model,, {\n  return model\n")
	v, err := JavaScriptVerifier{Log: discard()}.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("node --check accepted a file that does not parse")
	}
	if v.Strength != StrengthSyntaxChecked {
		t.Errorf("strength = %q; a rejection still names the gate", v.Strength)
	}
	if !strings.Contains(v.Log, "SyntaxError") {
		t.Errorf("node's reason must survive into the record, got: %q", v.Log)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// TypeScript — type-checked ONLY with a usable tsconfig.json
// ─────────────────────────────────────────────────────────────────────────────────────────────────

const tsTypeError = `interface Params { model: string; prompt: string }

export function callModel(p: Params): string {
  return p.model + ":" + p.prompt;
}

// The codemod's rewrite: the property was misspelled.
export const out = callModel({ modle: "claude-sonnet-5", prompt: "triage" } as unknown as Params);
`

func TestTypeScript_WithTsconfigIsTypeChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true,"target":"es2020"},"include":["*.ts"]}`)
	write(t, dir, "pipeline.ts", `interface Params { model: string; prompt: string }

export function callModel(p: Params): string {
  return p.model + ":" + p.prompt;
}

export const out = callModel({ model: "claude-sonnet-5", prompt: "triage" });
`)
	v, err := TypeScriptVerifier{Log: discard()}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("tsc rejected a well-typed project: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q, want type-checked: the repo has a usable tsconfig.json", v.Strength)
	}
	if v.FallbackReason != "" {
		t.Errorf("nothing was unavailable, but it reported a fallback: %q", v.FallbackReason)
	}
}

func TestTypeScript_WithTsconfigRejectsATypeError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true,"target":"es2020"},"include":["*.ts"]}`)
	write(t, dir, "pipeline.ts", `interface Params { model: string; prompt: string }

export function callModel(p: Params): string {
  return p.model + ":" + p.prompt;
}

export const out = callModel({ modle: "claude-sonnet-5", prompt: "triage" });
`)
	v, err := TypeScriptVerifier{Log: discard()}.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("tsc accepted a misspelled property")
	}
	if v.Strength != StrengthTypeChecked {
		t.Errorf("strength = %q; a rejection still names the gate", v.Strength)
	}
	if !strings.Contains(v.Log, "modle") {
		t.Errorf("tsc's reason must survive into the record, got: %q", v.Log)
	}
}

// ADR-003: type-checked "if the repo has a usable tsconfig.json". This repo does not. So the gate is
// weaker, it says so, and it WARNS naming what was missing — the same rule Python's fallback follows,
// for a different reason.
func TestTypeScript_WithoutTsconfigFallsBackToSyntaxCheckedAndWarns(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pipeline.ts", tsTypeError)

	logs := &bytes.Buffer{}
	v, err := TypeScriptVerifier{Log: bufLogger(logs)}.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("the fallback rejected a file that parses: %v\n%s", err, v.Log)
	}
	if v.Strength != StrengthSyntaxChecked {
		t.Fatalf("strength = %q, want syntax-checked: with no tsconfig there is no project to type-check",
			v.Strength)
	}
	if v.Strength.AllowsAutonomousApply() {
		t.Error("a TypeScript transform verified without a tsconfig is autonomously appliable")
	}
	assertFallbackWARN(t, logs, "no tsconfig.json")
}
