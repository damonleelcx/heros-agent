package worktree

// The seven language gates (ADR-003 decision 2 + the table in its "hard problem" section).
//
//	Go          go build ./...                    type-checked
//	Rust        cargo check                       type-checked
//	Java        project build tool, else javac    type-checked
//	Kotlin      project build tool, else kotlinc  type-checked
//	TypeScript  tsc --noEmit -p .                 type-checked   (only with a usable tsconfig.json)
//	Python      pyright / mypy, if configured     type-checked
//	            else py_compile                   syntax-checked + WARN
//	JavaScript  node --check                      syntax-checked  (no type system exists)
//
// Two rules run through every one of them, and they are NOT the same rule:
//
//	the REPOSITORY offers no stronger gate  -> the weaker gate, recorded honestly, WITH A WARN
//	the WORKER is missing a toolchain       -> *ToolchainError. Loud. No verdict, no record.
//
// The first is the customer's legitimate choice and the platform accommodates it (ADR-003 decision 4:
// we do not require a type checker, and we do not pretend to have used one). The second is our
// operational failure, and degrading it into the first would convert an ops problem into a safety
// problem — the platform would keep working, quietly, at a fraction of the guarantee it claims.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// warn emits the fallback WARN required by ADR-003 decision 4 (禁止静默回落默认值).
//
// The log is NOT the surface — the strength lands on the transform row and in the API, because a
// health signal that only exists in a log line is one nobody reads (health-signal-surface). The WARN
// is the OPERATOR's copy: "this repo has no type checker, so its diffs will never auto-apply" is
// something an operator can act on, and a silent fallback is something nobody ever will.
func warn(ctx context.Context, log *slog.Logger, v Verification, language, dir string) {
	if log == nil {
		log = slog.Default()
	}
	log.WarnContext(ctx, "worktree: verified with a weaker gate than type-checked; this transform "+
		"will always require human review",
		"language", language, "dir", dir, "strength", string(v.Strength),
		"tool", v.Tool, "unavailable", v.FallbackReason)
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Go — `go build ./...` -> type-checked
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// GoVerifier runs `go build ./...` in the worktree. Always type-checked: `go build` IS a full type
// check, which is the property that made ADR-001 requirement 2 defensible in the first place.
type GoVerifier struct {
	// GoBin is the toolchain to use. Explicit because PRD §7 requires a "deterministic build (pinned
	// toolchain)" — whatever is first on PATH is not pinned, and two runs of the same config_hash on
	// two machines must not build with different compilers.
	GoBin string
	// Env, when non-nil, replaces the build environment entirely.
	Env []string
	Log *slog.Logger
}

func (v GoVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	bin := v.GoBin
	if bin == "" {
		bin = "go"
	}
	if _, err := requireTool("go", bin, v.Env, "", "version"); err != nil {
		return Verification{}, err
	}

	// Build OUTPUT must not land in the worktree.
	//
	// A plain `go build ./...` writes each main package's executable into the working directory. That
	// binary is then an untracked file in the variant worktree: `git status` is dirty, a later
	// `git add -A` would commit a multi-megabyte artifact onto the variant branch, and task 3.10's
	// "no residue" property is false — a revert restores the source and the binary stays.
	//
	// Found by the end-to-end test's rollback assertion, the only place the whole sequence
	// (apply -> build -> revert -> check clean) actually happens. The build gate exists to leave the
	// tree pristine; it was the thing dirtying it.
	//
	// `-o <dir>` redirects the executables — but ONLY when there is something to redirect: with no main
	// packages `go build -o dir ./...` fails outright with "no main packages to build", which would
	// reject every library-only target as unbuildable. So the flag is used exactly when it applies, and
	// a library-only target takes the plain form, which writes nothing anyway.
	args := []string{"build", "./..."}
	if hasMain, err := v.hasMainPackage(ctx, bin, dir); err == nil && hasMain {
		out, err := os.MkdirTemp("", "heros-build-*")
		if err != nil {
			return Verification{}, fmt.Errorf("worktree: build output dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(out) }()
		args = []string{"build", "-o", out + string(os.PathSeparator), "./..."}
	}

	log, err := run(ctx, dir, v.Env, bin, args...)
	return Verification{Strength: StrengthTypeChecked, Tool: bin + " build ./...", Log: log}, err
}

// hasMainPackage reports whether the target builds any executables.
//
// An error is NOT propagated: if `go list` cannot answer (a broken module, a missing dependency), the
// build itself is about to fail with a far better message, and rejecting here would replace the
// compiler's diagnosis with ours. The caller falls back to the plain form, which is safe.
func (v GoVerifier) hasMainPackage(ctx context.Context, bin, dir string) (bool, error) {
	out, err := run(ctx, dir, v.Env, bin, "list", "-e", "-f", "{{.Name}}", "./...")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "main" {
			return true, nil
		}
	}
	return false, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Rust — `cargo check` -> type-checked
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// RustVerifier runs `cargo check` in the worktree.
//
// `check` rather than `build`: it runs the full front end — parse, resolve, borrow-check, TYPE-check —
// and stops before codegen. Same rejection power, a fraction of the time, and (like GoVerifier's
// `-o <tmp>`) it leaves no artifact in the tree beyond `target/`, which is gitignored in every
// cargo-generated repo.
//
// A repository with no Cargo.toml gets cargo's own error as a rejection, which is the same thing
// GoVerifier does for a repo with no go.mod. That is a deliberate consistency and not an oversight:
// "Discovery called this Rust but it has no manifest" is a real, visible answer, and inventing a
// third outcome for it would add a state to the record for a case the tree has never produced.
type RustVerifier struct {
	CargoBin string
	Env      []string
	Log      *slog.Logger
}

func (v RustVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	bin := v.CargoBin
	if bin == "" {
		bin = "cargo"
	}
	if _, err := requireTool("rust", bin, v.Env, "", "--version"); err != nil {
		return Verification{}, err
	}
	log, err := run(ctx, dir, v.Env, bin, "check", "--quiet")
	return Verification{Strength: StrengthTypeChecked, Tool: bin + " check", Log: log}, err
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// JVM — Java and Kotlin
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// jvmBuildTool is the source of truth for how a JVM project is compiled: the project's OWN build tool,
// in preference order, else nothing (and the caller invokes the bare compiler).
//
// A table, in order, rather than a chain of ifs — the order IS the policy and it should be readable as
// one. Wrappers first: `./gradlew` and `./mvnw` are the version the repository pins, and using the
// operator's `gradle` instead of the one the repo asks for is how a build "works on the worker" and
// nowhere else.
//
// Why the build tool at all, rather than always the bare compiler: `javac Foo.java` on a real project
// fails with "package com.example does not exist" — not because our rewrite is wrong, but because
// javac has no classpath. That failure would be recorded as `build-rejected` and blame the user's
// spec for our missing dependency resolution. The build tool knows the classpath; the bare compiler is
// the fallback for a project that has no build tool, where there is nothing to resolve.
//
// ADR-003 accepts the cost this carries: Gradle/Maven may need NETWORK to resolve dependencies. That
// is in tension with the least-privilege posture of the discovery worker, and it is why verification
// runs in the APPLY worker — the two postures are deliberately different and must not be conflated.
var jvmBuildTools = []struct {
	// marker is the repo-relative file whose presence selects this tool.
	marker string
	// exe is the executable. When it starts with "./" it is the repo's own wrapper and needs no
	// toolchain lookup; otherwise it must be present on the worker.
	exe string
	// args are the fixed arguments; gradleTask (per language) is appended when task is true.
	args []string
	task bool
}{
	{marker: "gradlew", exe: "./gradlew", args: []string{"--console=plain"}, task: true},
	{marker: "mvnw", exe: "./mvnw", args: []string{"-B", "compile"}},
	{marker: "build.gradle", exe: "gradle", args: []string{"--console=plain"}, task: true},
	{marker: "build.gradle.kts", exe: "gradle", args: []string{"--console=plain"}, task: true},
	{marker: "pom.xml", exe: "mvn", args: []string{"-B", "compile"}},
}

// jvmBuildToolFor returns the argv for the project's build tool, or nil when it has none.
// A *ToolchainError is returned when the project NAMES a build tool the worker does not have.
func jvmBuildToolFor(language, dir string, env []string, gradleTask string) ([]string, error) {
	for _, t := range jvmBuildTools {
		if !fileExists(dir, t.marker) {
			continue
		}
		if !strings.HasPrefix(t.exe, "./") {
			if _, err := requireTool(language, t.exe, env, "the repository is built by "+t.marker, "--version"); err != nil {
				return nil, err
			}
		}
		argv := append([]string{t.exe}, t.args...)
		if t.task {
			argv = append(argv, gradleTask)
		}
		return argv, nil
	}
	return nil, nil
}

// JavaVerifier compiles with the project's build tool, or with `javac` when it has none. Either way
// the gate is a type checker, so the strength is type-checked and there is no fallback to warn about —
// javac is not a weaker CLAIM than Gradle, it is a narrower one.
type JavaVerifier struct {
	JavacBin string
	Env      []string
	Log      *slog.Logger
}

func (v JavaVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	argv, err := jvmBuildToolFor("java", dir, v.Env, "compileJava")
	if err != nil {
		return Verification{}, err
	}
	if argv != nil {
		log, err := run(ctx, dir, v.Env, argv[0], argv[1:]...)
		return Verification{Strength: StrengthTypeChecked, Tool: strings.Join(argv, " "), Log: log}, err
	}

	bin := v.JavacBin
	if bin == "" {
		bin = "javac"
	}
	if _, err := requireTool("java", bin, v.Env, "", "--version"); err != nil {
		return Verification{}, err
	}
	files, err := sourceFiles(dir, ".java")
	if err != nil {
		return Verification{}, err
	}
	if len(files) == 0 {
		return Verification{}, fmt.Errorf("worktree: nothing to verify: no .java files under %s", dir)
	}
	// -d <tmp>: class files must not land in the worktree. Same residue rule as GoVerifier's -o.
	out, err := os.MkdirTemp("", "heros-javac-*")
	if err != nil {
		return Verification{}, fmt.Errorf("worktree: javac output dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(out) }()
	log, err := run(ctx, dir, v.Env, bin, append([]string{"-d", out}, files...)...)
	return Verification{Strength: StrengthTypeChecked, Tool: bin, Log: log}, err
}

// KotlinVerifier compiles with the project's build tool, or with `kotlinc` when it has none.
type KotlinVerifier struct {
	KotlincBin string
	Env        []string
	Log        *slog.Logger
}

func (v KotlinVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	argv, err := jvmBuildToolFor("kotlin", dir, v.Env, "compileKotlin")
	if err != nil {
		return Verification{}, err
	}
	if argv != nil {
		log, err := run(ctx, dir, v.Env, argv[0], argv[1:]...)
		return Verification{Strength: StrengthTypeChecked, Tool: strings.Join(argv, " "), Log: log}, err
	}

	bin := v.KotlincBin
	if bin == "" {
		bin = "kotlinc"
	}
	if _, err := requireTool("kotlin", bin, v.Env, "", "-version"); err != nil {
		return Verification{}, err
	}
	files, err := sourceFiles(dir, ".kt", ".kts")
	if err != nil {
		return Verification{}, err
	}
	if len(files) == 0 {
		return Verification{}, fmt.Errorf("worktree: nothing to verify: no .kt files under %s", dir)
	}
	out, err := os.MkdirTemp("", "heros-kotlinc-*")
	if err != nil {
		return Verification{}, fmt.Errorf("worktree: kotlinc output dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(out) }()
	log, err := run(ctx, dir, v.Env, bin, append([]string{"-d", out, "-nowarn"}, files...)...)
	return Verification{Strength: StrengthTypeChecked, Tool: bin, Log: log}, err
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// TypeScript — `tsc --noEmit` -> type-checked, but ONLY with a usable tsconfig.json
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// TypeScriptVerifier type-checks with the project's tsconfig, or falls back to a per-file check.
type TypeScriptVerifier struct {
	// TSCBin overrides tsc. Empty means: the repository's own `node_modules/.bin/tsc` if it has one,
	// else `tsc` on PATH. The repo's copy first because it is the version the project pins — checking
	// a TS 4 codebase with the worker's TS 5 produces errors about code that is fine.
	TSCBin string
	Env    []string
	Log    *slog.Logger
}

const localTSC = "node_modules/.bin/tsc"

func (v TypeScriptVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	bin := v.TSCBin
	if bin == "" {
		if fileExists(dir, localTSC) {
			bin = "./" + localTSC
		} else {
			bin = "tsc"
		}
	}
	// The toolchain check comes FIRST, before the tsconfig question. Order matters: if tsc is missing
	// and we asked about tsconfig first, a repo without one would take the fallback path and report
	// syntax-checked — hiding a missing toolchain behind a legitimate-looking downgrade. That is the
	// exact confusion ADR-003 forbids, and it is one `if` away at all times.
	if !strings.HasPrefix(bin, "./") {
		if _, err := requireTool("typescript", bin, v.Env, "", "--version"); err != nil {
			return Verification{}, err
		}
	}

	if fileExists(dir, "tsconfig.json") {
		log, err := run(ctx, dir, v.Env, bin, "--noEmit", "-p", ".")
		return Verification{Strength: StrengthTypeChecked, Tool: bin + " --noEmit -p .", Log: log}, err
	}

	// No tsconfig.json: ADR-003 says type-checked "if the repo has a usable tsconfig.json", and it does
	// not. Without one, tsc has no lib, no target, no module resolution and no include set — the
	// project's types are simply not knowable, and `tsc --noEmit` on bare files would report errors
	// about a configuration we invented rather than about the rewrite.
	//
	// `--noResolve` is the honest fallback: it parses and checks each file in isolation, with every
	// import typed as `any`. That is STRONGER than pure syntax (a local type error is still caught) and
	// far weaker than a project check (nothing crossing a module boundary is), so it is recorded as
	// `syntax-checked` — the strongest claim we can actually defend. Under-claiming is the only
	// direction this design is allowed to be wrong in.
	files, err := sourceFiles(dir, ".ts", ".tsx", ".mts", ".cts")
	if err != nil {
		return Verification{}, err
	}
	if len(files) == 0 {
		return Verification{}, fmt.Errorf("worktree: nothing to verify: no TypeScript files under %s", dir)
	}
	args := append([]string{"--noEmit", "--noResolve", "--skipLibCheck"}, files...)
	log, err := run(ctx, dir, v.Env, bin, args...)
	ver := Verification{
		Strength: StrengthSyntaxChecked, Tool: bin + " --noEmit --noResolve", Log: log,
		FallbackReason: "the repository has no tsconfig.json, so tsc cannot type-check the project " +
			"(no lib, target, or module resolution)",
	}
	warn(ctx, v.Log, ver, "typescript", dir)
	return ver, err
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// JavaScript — `node --check` -> syntax-checked. Always. There is nothing stronger.
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// JavaScriptVerifier runs `node --check` over the repository's JavaScript.
//
// This is the ONE language where `syntax-checked` is not a fallback and carries no WARN: JavaScript
// has no type system, so nothing was unavailable and there is nothing for an operator to install.
// FallbackReason is empty for exactly that reason — a WARN here would be a false alarm, and a false
// alarm is worse than no alarm because it trains people to ignore the real ones.
//
// The consequence is not softened anywhere: a JS transform is `syntax-checked`, so it is human-reviewed
// at every automation level, forever. That is ADR-003 decision 5 doing its job, not a gap to close.
type JavaScriptVerifier struct {
	NodeBin string
	Env     []string
	Log     *slog.Logger
}

func (v JavaScriptVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	bin := v.NodeBin
	if bin == "" {
		bin = "node"
	}
	if _, err := requireTool("javascript", bin, v.Env, "", "--version"); err != nil {
		return Verification{}, err
	}
	files, err := sourceFiles(dir, ".js", ".jsx", ".mjs", ".cjs")
	if err != nil {
		return Verification{}, err
	}
	if len(files) == 0 {
		return Verification{}, fmt.Errorf("worktree: nothing to verify: no JavaScript files under %s", dir)
	}
	ver := Verification{Strength: StrengthSyntaxChecked, Tool: bin + " --check"}
	// One file per invocation: `node --check` takes exactly one. The loop stops at the first failure —
	// the reviewer needs the first thing that broke, and a hundred more diagnostics do not help.
	var logs []string
	for _, f := range files {
		out, err := run(ctx, dir, v.Env, bin, "--check", f)
		if err != nil {
			ver.Log = trimLog(strings.Join(append(logs, out), "\n"))
			return ver, err
		}
		if s := strings.TrimSpace(out); s != "" {
			logs = append(logs, s)
		}
	}
	ver.Log = strings.Join(logs, "\n")
	return ver, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Python — the case the whole ADR is about
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// pythonTypeCheckers is the source of truth for "does this repository configure a type checker?".
//
// The question is about the REPOSITORY, not about the worker: it is the author saying "my code is
// type-checked", written down in a file they committed. Read it, do not guess it — and above all do
// not answer it with "is pyright installed here?", which is a question about our container.
//
// pyright before mypy: when a repo configures both, pyright is the stricter of the two, and the gate
// should be the strongest one the repository offers (ADR-003 decision 4).
var pythonTypeCheckers = []struct {
	tool string
	// configuredBy returns the file that configures this tool, or "" when the repo does not.
	configuredBy func(dir string) string
	args         []string
}{
	{
		tool: "pyright",
		configuredBy: func(dir string) string {
			if fileExists(dir, "pyrightconfig.json") {
				return "pyrightconfig.json"
			}
			if fileContains(dir, "pyproject.toml", "[tool.pyright]") {
				return "pyproject.toml ([tool.pyright])"
			}
			return ""
		},
		args: []string{"."},
	},
	{
		tool: "mypy",
		configuredBy: func(dir string) string {
			for _, f := range []string{"mypy.ini", ".mypy.ini"} {
				if fileExists(dir, f) {
					return f
				}
			}
			if fileContains(dir, "pyproject.toml", "[tool.mypy]") {
				return "pyproject.toml ([tool.mypy])"
			}
			if fileContains(dir, "setup.cfg", "[mypy]") {
				return "setup.cfg ([mypy])"
			}
			return ""
		},
		args: []string{"."},
	},
}

// PythonVerifier is the reason `Strength` exists.
//
// Python has no compiler. `python -m py_compile` proves a file PARSES — it does not prove the call is
// well-formed. A rewrite of `model="x"` to `modle="x"` compiles perfectly and fails at runtime, or
// worse, silently takes a different code path. So this verifier can return EITHER strength, for the
// same language, depending entirely on what the repository asked for:
//
//	pyright/mypy configured   -> run it              -> type-checked
//	configured but NOT INSTALLED -> *ToolchainError  -> LOUD. never a downgrade.
//	nothing configured        -> py_compile + WARN   -> syntax-checked
//
// The middle row is the one that matters. A repo that configures pyright is a repo whose author
// believes their code is type-checked; if our worker lacks pyright, quietly recording `syntax-checked`
// would be technically true, permanently invisible, and would strip the platform's strongest guarantee
// from its largest market with no error anywhere.
type PythonVerifier struct {
	// PythonBin is the interpreter for the py_compile fallback.
	PythonBin string
	// TypeCheckerBin overrides the resolved pyright/mypy executable. For pinning and for tests.
	TypeCheckerBin string
	Env            []string
	Log            *slog.Logger
}

func (v PythonVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	for _, tc := range pythonTypeCheckers {
		cfg := tc.configuredBy(dir)
		if cfg == "" {
			continue
		}
		bin := v.TypeCheckerBin
		if bin == "" {
			bin = tc.tool
		}
		// The repository named this tool. Not having it is OUR failure, and it stops here: no fallback,
		// no verdict, no row. ADR-003: "a missing toolchain must fail loudly, never silently degrade to
		// syntax-checked — that would turn an ops problem into a safety problem."
		if _, err := requireTool("python", bin, v.Env, "the repository configures "+tc.tool+" ("+cfg+")", "--version"); err != nil {
			return Verification{}, err
		}
		log, err := run(ctx, dir, v.Env, bin, tc.args...)
		return Verification{Strength: StrengthTypeChecked, Tool: bin, Log: log}, err
	}

	// No type checker configured. This is legitimate and extremely common, and ADR-003 decision 4 is
	// explicit that we neither require one nor pretend to have used one. What we do NOT do is let it
	// pass unremarked.
	bin := v.PythonBin
	if bin == "" {
		bin = "python3"
	}
	probe, err := requireTool("python", bin, v.Env, "", "--version")
	if err != nil {
		return Verification{}, err
	}
	// The interpreter runs the parse, so ITS version decides what parses. An interpreter older than the
	// repository declares it needs cannot read the source, and every file using newer syntax comes back
	// as a SyntaxError — which, without this, is recorded as `build-rejected` and blamed on a customer
	// file the diff never touched. That is an ops problem laundered into a verdict about their code;
	// ADR-003 forbids exactly it. See requireLanguageFloor for why this is loud rather than a fallback.
	//
	// 🔴 Only on this path, deliberately. The pyright/mypy branch above returns before reaching here,
	// and must: there the TYPE CHECKER does the parsing, it reads `requires-python` itself, and
	// `python3` is not in the loop at all. Gating that branch on the interpreter's version would refuse
	// a correctly-configured repo for a tool it does not use.
	if err := requireLanguageFloor(ctx, v.Log, "python", bin, dir, probe); err != nil {
		return Verification{}, err
	}
	files, err := sourceFiles(dir, ".py", ".pyi")
	if err != nil {
		return Verification{}, err
	}
	if len(files) == 0 {
		return Verification{}, fmt.Errorf("worktree: nothing to verify: no Python files under %s", dir)
	}
	// PYTHONPYCACHEPREFIX redirects the .pyc files py_compile writes. Without it they land in
	// __pycache__ INSIDE the worktree, and task 3.10's "no residue" property is false — `git status`
	// is dirty and a revert would leave them behind. Same rule as GoVerifier's `-o <tmp>`; the gate
	// exists to leave the tree pristine.
	cache, err := os.MkdirTemp("", "heros-pycache-*")
	if err != nil {
		return Verification{}, fmt.Errorf("worktree: pycache dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(cache) }()
	env := v.Env
	if env == nil {
		env = os.Environ()
	}
	env = append(append([]string{}, env...), "PYTHONPYCACHEPREFIX="+cache)

	log, err := run(ctx, dir, env, bin, append([]string{"-m", "py_compile"}, files...)...)
	ver := Verification{
		Strength: StrengthSyntaxChecked, Tool: bin + " -m py_compile", Log: log,
		FallbackReason: "the repository configures no type checker (no pyright or mypy configuration " +
			"found), so only parse validity could be proved",
	}
	warn(ctx, v.Log, ver, "python", dir)
	return ver, err
}
