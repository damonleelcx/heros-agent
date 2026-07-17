package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The safety contract of the apply model (ADR-003 decision 2)
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
// ADR-001 requirement 2 says "a transform that fails to compile/build the target is rejected before it
// is ever proposed". That sentence carried the whole safety argument for "we edit your source
// automatically", and it was true only because the one implementation was `go build` — a FULL TYPE
// CHECK. A codemod that passes a string where an enum belongs cannot reach the customer, because the
// compiler rejects it.
//
// Python has no compiler. `py_compile` proves a file PARSES. `model="x"` -> `modle="x"` parses
// perfectly and fails at runtime. So the guarantee is a property of the TARGET LANGUAGE AND
// REPOSITORY, not of our engine, and it varies by an order of magnitude across the six languages
// Discovery supports.
//
// The dangerous design — the one that looks like "just make it work" — is to keep the gate a boolean.
// It silently converts a type-checked guarantee into a syntax-checked one for the majority of
// customers, with nothing in the diff, the record, or the UI revealing the downgrade; a reviewer
// extends trust earned by the Go path to a Python diff that never earned it. ADR-003 rejects that at
// L1/L2 as a 越级 violation: it degrades safety (L1/L2) to buy extensibility (L6).
//
// So a verification does not return a boolean. It returns a STRENGTH.

// Strength names what a verification actually PROVED about a transformed working copy.
//
// The vocabulary is closed and deliberately small: these are the only two claims a gate can make that
// mean anything different to a reviewer. Anything else ("we ran a linter") is not a verification.
type Strength string

const (
	// StrengthTypeChecked: a type checker proved the rewritten program is well-typed. A rewrite that
	// passes the wrong type, drops a required field, or misnames an argument was REJECTED.
	StrengthTypeChecked Strength = "type-checked"
	// StrengthSyntaxChecked: only parse validity was proved. A type error would NOT have been caught.
	// This is the honest verdict for JavaScript (no type system exists) and for a Python repository
	// that configures no type checker.
	StrengthSyntaxChecked Strength = "syntax-checked"
)

// Valid reports whether s is one of the two known strengths.
//
// The zero Strength ("") is deliberately invalid rather than defaulting to anything: a code path that
// forgot to state what it proved must not thereby claim the strongest guarantee — that is exactly the
// failure ADR-003 exists to prevent. Verification failures return the zero value, and it is
// unrecordable (the DB's CHECK refuses it) and un-appliable (AllowsAutonomousApply is false).
func (s Strength) Valid() bool { return s == StrengthTypeChecked || s == StrengthSyntaxChecked }

// AllowsAutonomousApply is ADR-003 decision 5: automation level is gated on STRENGTH, not on language.
//
// Autonomous auto-apply requires `type-checked`. A `syntax-checked` transform is always human-reviewed,
// at every automation level (Advisory / Assisted / Autonomous — see
// docs/decisions/product-north-star.md §3). This is the rule that keeps the weaker gate from silently
// becoming the customer's problem: the capability ships for every language, and the languages whose
// gate proves less simply do not get the level of automation that a compiler is what pays for.
//
// It is a method on Strength, and it is the ONE definition of the rule. A gate on language would be
// wrong twice over: a Python repo WITH pyright earns the same trust as Go, and a Go repo cannot lose
// it — the language is a proxy for the property, and we have the property itself.
//
// # Where this is enforced
//
// P2 has no auto-apply: nothing in this phase writes to the customer's repository, so there is no
// autonomous action to gate here, and inventing an automation-level framework to hold a rule with no
// caller would be 建了等未来用. The rule therefore lands in the two places the decision is actually
// made today:
//
//   - The REVIEW surface. GET /api/p2/transforms/{config_hash}/{source_revision} returns
//     `verification_strength` and `requires_human_review`, and the diff-review UI renders the weaker
//     gate as its own visible state. ADR-003 decision 3: "a reviewer looking at a diff must be able to
//     see, without asking, whether a compiler stood behind it".
//   - This predicate. P6's Autonomous loop, when it is built, calls THIS — it does not re-derive the
//     rule from the strength value, and it does not get a language allowlist.
func (s Strength) AllowsAutonomousApply() bool { return s == StrengthTypeChecked }

// RequiresHumanReview is the inverse, named for the reviewer rather than the robot. Both exist because
// both readings appear in the product: the UI asks "must a human look at this?" and P6 asks "may I
// apply this myself?", and a caller negating the other one by hand is a caller that can get it
// backwards.
func (s Strength) RequiresHumanReview() bool { return !s.AllowsAutonomousApply() }

// Verification is what a Verifier proved. It is the value the transform record and the UI are built on.
type Verification struct {
	// Strength is the claim. Zero (invalid) when nothing was proved.
	Strength Strength
	// Tool names exactly what ran — "go build ./...", "pyright", "python -m compileall". Evidence
	// travels with the claim (product-north-star §2): "type-checked" is a claim, and the name of the
	// type checker is what makes it checkable by a human.
	Tool string
	// Log is the tool's combined output.
	Log string
	// FallbackReason names what was unavailable, when the repository offered no stronger gate. Empty
	// whenever the strongest gate for the language was used. This is what the WARN reports
	// (禁止静默回落默认值): a fallback that nobody can see is a fallback that nobody will fix.
	FallbackReason string
}

// Verifier proves what it can about a transformed working copy, and says what that was.
//
// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Why this shape (careful-api-creation: an interface is system surface area — justify it)
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
// This replaces `Builder`, whose contract was `Build(ctx, dir) (log string, err error)` — a boolean
// dressed as an error. Four decisions, each load-bearing:
//
//  1. It returns a Verification, not a bool/error pair. The strength is IN the return type, so a
//     caller physically cannot record a verdict without also handling what was proved. ADR-003's
//     rejected option B — "treat the gate as pass/fail" — is not a temptation here; it is a compile
//     error. This is the whole reason the seam changed rather than growing a `Strength()` method.
//
//  2. Strength is on the RESULT, not on the Verifier. It is tempting to write
//     `func (PythonVerifier) Strength() Strength` — and it is wrong, because it decides before looking
//     at the repository. The same Python verifier yields `type-checked` against a repo that configures
//     pyright and `syntax-checked` against one that does not (ADR-003 decision 4: "the strongest gate
//     the REPOSITORY actually offers"). A per-verifier strength would have to lie about one of them.
//
//  3. It takes a directory and nothing else. What gate a repository offers is a fact about that
//     repository — a tsconfig.json, a [tool.mypy] section, a Cargo.toml — and it is discovered by
//     looking, exactly as every build tool does. Passing in a language label instead would let a caller
//     assert a gate the tree cannot actually run.
//
//  4. Its errors are of two kinds, and they are typed apart — see ToolchainError. This is the single
//     most important distinction in the package.
//
// A non-nil error means the transform did not pass the gate. The Verification is still returned in
// that case when a gate ran: WHICH gate rejected it is information (a `syntax-checked` rejection means
// the rewrite does not even parse).
type Verifier interface {
	Verify(ctx context.Context, dir string) (Verification, error)
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The three kinds of failure, and why they must never be confused
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// ErrToolchainUnavailable: the WORKER is missing a tool it needs to verify with. This is an OPS
// failure, and it is not a statement about the customer's code at all.
//
// ADR-003 is explicit that this must fail loudly and never silently degrade to `syntax-checked`:
// "that would turn an ops problem into a safety problem". The failure mode it forbids is quiet and
// total — pyright is not installed on the worker, every Python transform records `syntax-checked`,
// every Python transform is (correctly!) held for human review, and the platform's strongest guarantee
// has evaporated for its largest market with no error anywhere. Months later the only evidence is a
// column full of the weaker value.
//
// It must equally never be recorded as `build-rejected`. "Your transform does not build" would be a
// lie about the user's spec, and it would send them to fix a codemod that was never the problem, for
// a container that was missing a package.
//
// The distinction this type carries:
//
//	repository configures no type checker  -> LEGITIMATE syntax-checked + WARN  (the customer's choice)
//	toolchain absent from the worker       -> ERROR, no record, no verdict      (our problem)
var ErrToolchainUnavailable = errors.New("worktree: this worker cannot verify: a required toolchain is missing")

// ToolchainError names the tool the worker does not have, so the operator can install THAT.
type ToolchainError struct {
	// Language is the target language whose gate could not run.
	Language string
	// Tool is the executable that is missing or unusable.
	Tool string
	// Reason is the underlying lookup failure.
	Reason string
	// Required names why this tool was needed, when the repository asked for it specifically — e.g.
	// "the repository configures pyright (pyrightconfig.json)". A missing tool the REPO named is a
	// different fix from a missing tool WE named, and the operator should not have to guess which.
	Required string
}

func (e *ToolchainError) Error() string {
	s := fmt.Sprintf("worktree: cannot verify a %s transform: %q is not available on this worker", e.Language, e.Tool)
	if e.Required != "" {
		s += " (" + e.Required + ")"
	}
	if e.Reason != "" {
		s += ": " + e.Reason
	}
	return s + "; install it — verification must not fall back to a weaker gate to work around a " +
		"missing toolchain (ADR-003)"
}

func (e *ToolchainError) Unwrap() error { return ErrToolchainUnavailable }

// ErrBaselineFails: the gate rejected the transformed tree, AND it rejects the UNMODIFIED tree at the
// same `source_revision`. The diff is therefore not what broke it, and no verdict about the diff can
// honestly be drawn from this run.
//
// This is the third kind, and it was found by running the gate against a real repository rather than a
// fixture — which is the only way it could have been found. Every fixture in this package is a small
// tree that the worker's toolchain can trivially parse, so the case cannot arise there.
//
// What happened: the worker's default `python3` was 3.9, the target used 3.10+ `match` statements, and
// `py_compile` failed on a file the diff never touched. The pipeline recorded `build-rejected` and told
// the user "this transform does not build, so it was never proposed and never run", citing a line in a
// file that is perfectly valid Python. `rejected_node_id` was empty, because there was no node to
// blame — the honest answer was not in the vocabulary.
//
// ErrToolchainUnavailable does not cover it: nothing was missing. `python3` was present and answered
// `--version` cheerfully. It was simply OLDER THAN THE LANGUAGE THE REPOSITORY IS WRITTEN IN, and a
// version probe cannot see that — "is this interpreter new enough for THIS repository?" is a question
// about a pairing, not about a tool.
//
// So the check is not a minimum-version table. A table would need a row per language, per feature, per
// release, would go stale the day a language ships new syntax, and would still miss every other reason
// a pristine tree fails a gate (a `Cargo.lock` needing a newer resolver, a `tsconfig` referencing a
// missing path, a repository that genuinely committed broken code). Verifying the BASELINE answers the
// general question directly and empirically: run the same gate on the unmodified source, and if it
// fails there too, the diff is exonerated — whatever the cause was.
//
// 🔴 It is deliberately NOT a ToolchainError, even though a stale interpreter usually is an ops
// problem. A baseline can also fail because the customer's repository is broken at that revision, and
// telling an operator to "install something" would then be as wrong as telling the customer their diff
// does not build. The honest claim — the only one true in both cases — is narrower: the gate could not
// evaluate this diff, and here is what the unmodified source did.
//
// The distinction this type carries, completing the set:
//
//	repository configures no type checker  -> LEGITIMATE syntax-checked + WARN  (the customer's choice)
//	toolchain absent from the worker       -> ERROR, no record, no verdict      (our problem)
//	pristine source fails the same gate    -> ERROR, no record, no verdict      (nobody's diff)
var ErrBaselineFails = errors.New("worktree: the unmodified source already fails this gate, so the diff cannot be judged")

// BaselineFailure carries what the gate did to the UNMODIFIED tree, so whoever reads it can tell a
// stale worker apart from a broken revision without re-running anything.
type BaselineFailure struct {
	// ConfigHash and SourceRevision identify the run that could not be judged.
	ConfigHash     string
	SourceRevision string
	// Tool names the gate that rejected both trees — "python3 -m py_compile", "tsc --noEmit". Not the
	// language: the tool is the thing an operator can actually check the version of, and it is what
	// Verification already carries. A Language field would mean threading one through seven verifiers to
	// restate what Tool already implies.
	Tool string
	// BaselineLog is the gate's output on the PRISTINE tree — the evidence that this is not the diff's
	// doing. It is the thing an operator actually needs: it names the file and the reason, and that file
	// is one the diff never touched.
	BaselineLog string
}

func (e *BaselineFailure) Error() string {
	return fmt.Sprintf("worktree: cannot judge %s at %s: the gate (%s) rejects the UNMODIFIED source at "+
		"this revision, so its rejection of the transformed source says nothing about the diff. Either "+
		"this worker's toolchain cannot parse this repository (check the version it pins) or the revision "+
		"does not pass its own gate. The gate's output on the pristine tree:\n%s",
		e.ConfigHash, e.SourceRevision, e.Tool, e.BaselineLog)
}

func (e *BaselineFailure) Unwrap() error { return ErrBaselineFails }

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Language dispatch
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// verifiersByLanguage is the single source of truth for which language gets which gate.
//
// A table rather than a switch (禁止 if/else 穷举替代配置表): adding the next frontend is a row, and the
// set of verifiable languages is readable in one place instead of reconstructed from control flow.
//
// The keys are Discovery's language labels verbatim (internal/discovery: `frontendsByLanguage`,
// `Workflow.Language`). Not a parallel vocabulary — a second spelling of "typescript" would make a
// workflow unverifiable for a reason no one could see.
var verifiersByLanguage = map[string]Verifier{
	"go":         GoVerifier{},
	"rust":       RustVerifier{},
	"java":       JavaVerifier{},
	"kotlin":     KotlinVerifier{},
	"typescript": TypeScriptVerifier{},
	"javascript": JavaScriptVerifier{},
	"python":     PythonVerifier{},
}

// ErrLanguageNotVerifiable is returned for a language with no gate.
var ErrLanguageNotVerifiable = errors.New("worktree: no verification gate is defined for this language")

// VerifierFor returns the gate for a Discovery language label.
//
// An unknown language is an ERROR, never a permissive default. A "verify nothing and call it
// syntax-checked" branch here would be the same safety-for-convenience trade the whole ADR rejects,
// one level up: it would let a language we have never gated ship a diff carrying a claim.
func VerifierFor(language string) (Verifier, error) {
	v, ok := verifiersByLanguage[strings.ToLower(strings.TrimSpace(language))]
	if !ok {
		return nil, fmt.Errorf("%w: %q (verifiable: %s)", ErrLanguageNotVerifiable, language,
			strings.Join(VerifiableLanguages(), ", "))
	}
	return v, nil
}

// VerifiableLanguages lists every language with a gate, sorted. For error messages and for the test
// that asserts the table covers every frontend Discovery ships.
func VerifiableLanguages() []string {
	out := make([]string, 0, len(verifiersByLanguage))
	for k := range verifiersByLanguage {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Shared plumbing
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// lookPath resolves a tool against the verifier's OWN environment.
//
// exec.LookPath consults the parent process's PATH, which is not necessarily the one the command will
// run with: every verifier here takes an Env that REPLACES the environment (PRD §7 requires a pinned
// toolchain — "whatever is first on PATH" is not pinned, and two runs of the same config_hash on two
// machines must not verify with different compilers). Resolving against a different PATH from the one
// we then execute with is how a "missing toolchain" check passes and the exec fails anyway.
func lookPath(bin string, env []string) (string, error) {
	if env == nil {
		return exec.LookPath(bin)
	}
	if strings.ContainsRune(bin, os.PathSeparator) {
		return exec.LookPath(bin)
	}
	var pathVal string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			pathVal = v
		}
	}
	// Restore the caller's PATH around the lookup rather than reimplementing exec.LookPath's rules
	// (executability, extension handling); getting those subtly wrong is how a tool that IS present
	// reports as missing and fails a submission for nothing.
	old, had := os.LookupEnv("PATH")
	if err := os.Setenv("PATH", pathVal); err != nil {
		return "", err
	}
	defer func() {
		if had {
			_ = os.Setenv("PATH", old)
		} else {
			_ = os.Unsetenv("PATH")
		}
	}()
	return exec.LookPath(bin)
}

// requireTool proves the toolchain is USABLE, or returns a *ToolchainError. Every verifier's first act.
//
// It returns the probe's OUTPUT, because "which version answered" is a second question the caller may
// have to ask (see requireLanguageFloor) and re-probing to ask it would put the tool's version
// arguments in two places — the drift that ends with one of them being wrong for a tool nobody
// re-tested.
//
// # Why a version probe and not just a PATH lookup
//
// "A file exists at that path" is not the same claim as "this tool works", and the gap between them
// is not hypothetical — it is the default state of macOS. /usr/bin/javac ships as a SHIM: it resolves,
// it is executable, and running it prints "Unable to locate a Java Runtime" and exits 1 because no JDK
// is installed.
//
// With a lookup-only check that shim sails through, javac "runs", it fails, and the failure is
// recorded as `build-rejected` — the platform telling a customer their transform does not compile,
// citing a message about installing Java. That is an ops problem laundered into a verdict about the
// customer's code, which is the precise thing ADR-003 forbids, arriving through a door the ADR did not
// think to name. Found by running these tests on a machine where `javac` was on PATH and unusable.
//
// So availability means: it resolves AND it can answer for itself. The probe is one exec, it is the
// tool's cheapest possible invocation, and it converts a whole class of broken-install into the loud
// ops error it always was.
//
// versionArgs are per-tool because there is no universal spelling (`go version` is a subcommand,
// `kotlinc -version` takes one dash). Callers pass the one their tool answers to.
func requireTool(language, bin string, env []string, required string, versionArgs ...string) (string, error) {
	fail := func(reason string) error {
		return &ToolchainError{Language: language, Tool: bin, Reason: reason, Required: required}
	}
	if _, err := lookPath(bin, env); err != nil {
		return "", fail(err.Error())
	}
	if len(versionArgs) == 0 {
		return "", nil
	}
	// A short timeout of its own: a toolchain probe that hangs must not hang the submission behind it.
	ctx, cancel := context.WithTimeout(context.Background(), toolProbeTimeout)
	defer cancel()
	out, err := run(ctx, "", env, bin, versionArgs...)
	if err != nil {
		return out, fail(fmt.Sprintf("%q is on PATH but does not run (%v): %s",
			bin, err, strings.TrimSpace(out)))
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The third thing a toolchain has to prove: that it speaks the repository's version of the language
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
// requireTool answers "is the tool there, and does it run". That is not the same question as "can it
// READ this repository", and the gap between them is not academic — it is the default state of this
// project's own worker:
//
//	$ python3 --version            ->  Python 3.9.6      (requireTool: fine)
//	$ python3 -m py_compile …      ->  SyntaxError: match message.root:
//
// hermes-agent declares `requires-python = ">=3.11"` and uses 3.10+ `match` statements. A 3.9
// interpreter answers `--version` perfectly and then cannot parse the language the file is written
// in. With only requireTool between us and the gate, that produced a `build-rejected` verdict citing
// `tools/mcp_tool.py:1891` — a file the diff never touched, which is valid Python — and the UI told
// the user "this transform does not build, so it was never proposed and never run".
//
// That sentence is false in every clause that matters, and it is the exact outcome ADR-003 forbids:
// "a missing toolchain must fail loudly, never silently degrade... that would turn an ops problem
// into a safety problem". An interpreter that cannot read the language IS a missing toolchain; it
// just fails a different way than being absent from PATH, and the ADR's rule is about the outcome,
// not the mechanism.
//
// # What SHOULD happen when the worker's interpreter is older than the repo needs
//
// Four options; settled by 八级法則 at the highest level where they differ.
//
//	A. *ToolchainError — loud, no verdict, no record.        <- chosen
//	B. Silently pick a newer interpreter off PATH.
//	C. Degrade to a weaker gate / skip the file.
//	D. Record `build-rejected` and blame the customer's file. <- the bug
//
// D loses at L1: it is an active lie about the customer's code. It sends someone to fix a codemod
// that was never wrong, for a container that is missing a package, and it burns the one thing the
// build gate exists to earn — a `build-rejected` a reviewer can believe. C loses at L1 too: it is
// verbatim the "ops problem into a safety problem" ADR-003 names. B is the interesting one, and it
// loses at L2 (stability) and L5: "whatever newer python is on PATH" is not a pinned toolchain, so
// two runs of the same config_hash on two machines could verify with different compilers and PRD §7's
// reproducibility is gone — and worse, the platform would keep working while misconfigured, so the
// ops problem is never seen and never fixed. Silence is the failure, not the error.
//
// A costs the operator a real install or a pin — an L4 cost, and one ADR-003 already accepted in
// writing ("the worker now needs the toolchains it verifies with... accepted deliberately"). L4 loses
// to L1/L2, and the fix is one line through a seam that already exists: `Verifiers` lets the operator
// pin `PythonVerifier{PythonBin: "python3.14"}` (PRD §7's pinned toolchain). The error names the
// tool, both versions, and the file that declared the requirement, so the operator is not made to
// guess which.

// version is a major.minor language version.
//
// Patch is deliberately not modelled. Language SYNTAX changes on minor releases (`match` arrived in
// 3.10) and never on patch ones, so major.minor is exactly the resolution the question has — carrying
// a patch number would invite comparisons that mean nothing and imply a precision we do not have.
type version struct{ major, minor int }

func (v version) String() string { return fmt.Sprintf("%d.%d", v.major, v.minor) }
func (v version) less(o version) bool {
	return v.major < o.major || (v.major == o.major && v.minor < o.minor)
}

// verRE reads the first `X.Y` in a string. Shared by the declaration and probe readers below.
var verRE = regexp.MustCompile(`(\d+)\.(\d+)`)

func parseVersion(s string) (version, bool) {
	m := verRE.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	maj, err1 := strconv.Atoi(m[1])
	min, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return version{}, false
	}
	return version{maj, min}, true
}

// languageFloor is one language's answer to two questions that must both be answerable before a
// version gate means anything: what does the REPOSITORY say it needs, and what IS the worker's tool?
//
// A table rather than an `if language == "python"`, for the same reason verifiersByLanguage is one
// (禁止 if/else 穷举替代配置表): the next language is a row. But note what the rows are NOT — they are
// not a to-do list. A row is only honest where a language has a STANDARD, machine-readable place for
// an author to declare a minimum, and where the tool that does the PARSING is the one whose version
// decides what parses. Where that is not true, the absence is the correct answer and is written down
// as such below.
type languageFloor struct {
	// declared reads the repo's own statement of the minimum language version it needs, and names the
	// file it read it from. found is false when the repository declares nothing — which is not a
	// failure and not a fallback: there is simply no claim to check against, and inventing one would
	// be a lazy default with the power to block a submission.
	declared func(dir string) (v version, source string, found bool)
	// fromProbe reads the tool's own version out of the --version output requireTool already captured.
	fromProbe func(probeOut string) (version, bool)
}

// languageFloors is the per-language data. See languageFloor for why one row is not a gap.
//
//	python  — a row. `requires-python` (PEP 621) is standard, universal and machine-readable, and the
//	          interpreter that runs py_compile IS the parser, so its version decides what parses.
//	          This is the reproduced bug.
//
// 🔴 No row for the others, and each absence is a different, deliberate answer — not six pending
// tasks. Recording them here because "why is there no Go row?" must stay answerable:
//
//	go        — go.mod's `go` directive is readable, but the Go toolchain RESOLVES this itself
//	            (GOTOOLCHAIN downloads the version go.mod asks for). A floor gate here would reject
//	            submissions the toolchain would have handled, which is a false ops error — the same
//	            class of lie as F2, pointed the other way.
//	rust      — Cargo.toml's `rust-version` is readable and cargo already enforces it with an
//	            accurate, loud message naming the toolchain. A row would be a second enforcer of a
//	            rule that already has one.
//	java      — the project's build tool owns the language level (`--release`, sourceCompatibility),
//	            and it is the build tool that compiles. There is no single file to read.
//	kotlin    — same as Java.
//	typescript— tsc's version is not a language floor: `tsconfig.json` has `target`/`lib`, which are
//	            output settings, and no standard field declares "the minimum tsc that can parse this".
//	javascript— `node --check` parses whatever the installed Node's parser accepts; `engines.node` in
//	            package.json is a RUNTIME statement about the deployed app, not about syntax. Reading
//	            it as a parse floor would be a guess wearing a table row.
//
// ⚠️ The Go/Rust/JVM reasoning above is analysis, not something this change measured — none of it was
// reproduced against a real repo the way the Python row was. It is reported as a residual gap rather
// than closed silently.
var languageFloors = map[string]languageFloor{
	"python": {
		declared:  pythonRequires,
		fromProbe: func(out string) (version, bool) { return parseVersion(out) }, // "Python 3.9.6"
	},
}

// requireLanguageFloor proves the tool can read the language version the REPOSITORY declares it uses,
// or returns a *ToolchainError.
//
// probeOut is requireTool's captured output, so this costs no extra exec and cannot disagree with the
// probe that already ran.
//
// It fails ONLY on a proven mismatch — the repo declared a floor, we read the tool's version, and the
// tool is below it. Every other path returns nil, because every other path lacks the evidence to
// accuse anyone: no row, no declaration, or an unreadable version is not grounds to block a
// submission. The unreadable cases WARN rather than pass in silence (禁止静默回落默认值): they mean
// this gate is not protecting the submission, and that is something an operator can fix.
func requireLanguageFloor(ctx context.Context, log *slog.Logger, language, bin, dir, probeOut string) error {
	f, ok := languageFloors[strings.ToLower(strings.TrimSpace(language))]
	if !ok {
		return nil
	}
	want, source, found := f.declared(dir)
	if !found {
		return nil // the repository declares no minimum: no claim to check, and none to invent
	}
	got, ok := f.fromProbe(probeOut)
	if !ok {
		if log == nil {
			log = slog.Default()
		}
		log.WarnContext(ctx, "worktree: could not read the toolchain's version from its own probe "+
			"output, so it was NOT checked against the version the repository requires; a too-old "+
			"toolchain would be reported as a build rejection about the customer's code (ADR-003)",
			"language", language, "tool", bin, "requires", want.String(), "declared_in", source,
			"probe_output", strings.TrimSpace(probeOut))
		return nil
	}
	if got.less(want) {
		return &ToolchainError{
			Language: language, Tool: bin,
			Required: fmt.Sprintf("%s declares %s %s or newer in %s", filepath.Base(dir), language,
				want.String(), source),
			Reason: fmt.Sprintf("it is %s %s, which cannot parse %s %s source — this is a fact about "+
				"THIS WORKER, not about the target's code: a %s file using syntax from %s would be "+
				"reported as a syntax error naming a file the transform never touched. Pin a %s %s+ "+
				"toolchain (worktree.Verifiers / PythonVerifier{PythonBin: …})",
				language, got.String(), language, want.String(), language, want.String(), language,
				want.String()),
		}
	}
	return nil
}

// pythonRequires reads the minimum Python a repository declares it needs.
//
// It reads the FLOOR and deliberately ignores any ceiling, which is a real decision and not an
// oversight. hermes-agent declares `requires-python = ">=3.11,<3.14"`, and that upper bound exists
// (its own comment says so) because uv resolves runtime WHEELS against it — a packaging concern. Our
// gate does not install anything; it parses. A newer interpreter reads older syntax fine, so
// enforcing the ceiling would reject a 3.14 worker for a repo whose source it can read perfectly,
// turning a packaging pin into a verification outage. The floor is the only clause that answers the
// question this gate asks: "does the source use syntax my parser does not have?"
//
// A `requires-python` we cannot find a `>=` clause in yields found=false rather than a guess. The
// exotic spellings (`~=3.11`, `>3.10`) are rare, and reading one wrong would block a submission over
// a pin format — the same over-reach in the opposite direction.
func pythonRequires(dir string) (version, string, bool) {
	for _, f := range []struct{ file, key string }{
		{"pyproject.toml", "requires-python"},
		{"setup.cfg", "python_requires"},
	} {
		b, err := os.ReadFile(filepath.Join(dir, f.file))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, f.key) {
				continue
			}
			_, spec, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			ge := strings.Index(spec, ">=")
			if ge < 0 {
				continue
			}
			if v, ok := parseVersion(spec[ge:]); ok {
				return v, f.file + " (" + f.key + ")", true
			}
		}
	}
	return version{}, "", false
}

// toolProbeTimeout bounds the version probe. Generous — a cold JVM start is slow — but finite: an
// unbounded probe turns a wedged toolchain into a wedged worker.
const toolProbeTimeout = 30 * time.Second

// run executes a tool in the worktree and returns its combined output.
func run(ctx context.Context, dir string, env []string, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// skipDir names directories no verifier should descend into.
//
// Two reasons, and the second is the one that bites. Vendored trees (node_modules, target, .venv) are
// not the customer's source and were never rewritten — verifying them turns a 3,000-file repo into a
// 300,000-file one. And `.git` holds the worktree's own administrative state: walking it is how a
// verifier ends up "checking" a packfile.
var skipDir = map[string]bool{
	".git": true, "node_modules": true, "target": true, "build": true, "dist": true,
	"__pycache__": true, ".venv": true, "venv": true, ".tox": true, ".mypy_cache": true,
	"vendor": true, ".gradle": true, "out": true,
}

// sourceFiles lists the repo-relative source files with any of exts, sorted.
//
// Sorted because a verifier's argv must be deterministic: an unordered walk makes the tool's output —
// which lands in build_log and in the UI — differ run to run for the same config_hash, and PRD §7's
// reproducibility is asserted on exactly that.
func sourceFiles(dir string, exts ...string) ([]string, error) {
	want := make(map[string]bool, len(exts))
	for _, e := range exts {
		want[e] = true
	}
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !want[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("worktree: list source files under %s: %w", dir, err)
	}
	sort.Strings(out)
	return out, nil
}

// fileExists reports whether a repo-relative path is a regular file in the worktree.
func fileExists(dir, rel string) bool {
	st, err := os.Stat(filepath.Join(dir, rel))
	return err == nil && st.Mode().IsRegular()
}

// fileContains reports whether a repo-relative file exists and contains needle.
//
// Used to read a repository's INTENT out of its config — a `[tool.pyright]` section in pyproject.toml
// is the repo saying "I am type-checked". Deliberately a substring test and not a TOML parse: the
// question is "did the author configure this tool", the section header is unambiguous, and a TOML
// dependency to answer it would be a parser we then have to keep correct for a yes/no.
func fileContains(dir, rel, needle string) bool {
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), needle)
}
