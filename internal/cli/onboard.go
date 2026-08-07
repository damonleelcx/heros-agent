package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/distribution"
	"github.com/heros-foreal/agentd/internal/release"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// onboard.go is the first ten minutes: the no-arg greeting, `heros init` and `heros doctor`
// (P20 tasks 5.1–5.3, 6.1).
//
// # Who these are written for
//
// A person who has just run one install command and has never seen this tool. That reader does not want a
// flag reference — they want to know what to type next. So the greeting names ONE command, `init` writes a
// config with defaults that already work, and `doctor` answers "is this machine ready" with a single next
// action per gap rather than a list of everything it inspected.
//
// # What none of them do
//
// None of them touch the network. Not for a version check, not for telemetry, not for anything (task 5.6,
// D7): this package does not import net/http at all, so the guarantee is structural rather than a promise.
// `heros upgrade` is the one command that must fetch, and it therefore lives in internal/clilink and is
// injected — the same shape `login` and `link` already use.

// ── 5.1 the greeting ────────────────────────────────────────────────────────────────────────────────────

// GreetingData is the machine payload for a bare `heros`.
//
// Even the greeting emits the envelope, because a script that runs `heros` with no arguments — which happens,
// in Dockerfiles and in CI templates — must get parseable output rather than prose.
type GreetingData struct {
	ToolVersion string `json:"tool_version"`
	// NextCommand is the ONE command to run. Singular on purpose.
	NextCommand string `json:"next_command"`
	// ConfigRequired is false, and it is stated rather than implied: the single most common reason a new
	// user stops is believing they have to configure something first.
	ConfigRequired bool   `json:"config_required"`
	Platform       string `json:"platform"`
	// SupportedPlatform is false on a disclosed-limit row, with Limit explaining it. A user on Alpine gets
	// the answer here rather than after a confusing failure three commands later.
	SupportedPlatform bool   `json:"supported_platform"`
	Limit             string `json:"limit,omitempty"`
	LimitAnswer       string `json:"limit_answer,omitempty"`
}

// Greeting is what a bare `heros` prints (task 5.1).
//
// It exits 0. A tool that exits non-zero when run with no arguments teaches CI authors to add `|| true`, and
// then a real failure is invisible too. The old behaviour — usage text and exit 3 (invalid-config) — treated
// curiosity as a malformed invocation.
func Greeting(s Streams, env func(string) (string, bool), goos, goarch string) error {
	target, known := distribution.TargetFor(goos, goarch)
	data := GreetingData{
		ToolVersion:       ToolVersion,
		NextCommand:       "heros discover",
		ConfigRequired:    false,
		Platform:          goos + "/" + goarch,
		SupportedPlatform: known && target.Support == distribution.SupportShipped,
	}
	if known && target.Support == distribution.SupportLimit {
		data.Limit, data.LimitAnswer = target.Limit, target.Answer
	}

	// The mark first — for a user who installed with a package manager, this greeting is the only place
	// the product introduces itself. Drawn on the human stream only; see mark.go.
	s.narrateMark(isTerminal(s.Err), goos, env)
	s.Narratef("heros %s — find, measure and improve the agent workflow already in your repository.", ToolVersion)
	s.Narratef("")
	if !data.SupportedPlatform && data.Limit != "" {
		// Said first, because everything below it is advice this reader cannot follow.
		s.Narratef("⛔ This platform (%s) has no native build: %s", data.Platform, data.Limit)
		s.Narratef("   Instead: %s", data.LimitAnswer)
		s.Narratef("")
	}
	s.Narratef("Start here — no config file, no account, no network:")
	s.Narratef("")
	s.Narratef("    cd your-repo && heros discover")
	s.Narratef("")
	s.Narratef("That reads your code and prints what it found. Then:")
	s.Narratef("    heros doctor    check this machine is ready for the rest")
	s.Narratef("    heros eval      score the workflow it found, with your own keys")
	s.Narratef("    heros --help    every command")
	return s.EmitJSON("greeting", ExitOK, data, nil, nil)
}

// ── 5.2 init ────────────────────────────────────────────────────────────────────────────────────────────

// InitData is the machine payload for `heros init`.
type InitData struct {
	Path string `json:"path"`
	// Created is false when a config already existed and was left alone.
	Created bool `json:"created"`
	// Unchanged is true when an existing file was kept. Distinct from Created so a caller can tell
	// "already set up" from "just set up" — an idempotent command must still report which happened.
	Unchanged bool `json:"unchanged"`
}

// starterConfig is the file `init` writes.
//
// Every value is a default that already works, and each carries the comment explaining what changing it does.
// A starter config full of `null`s that the tool then rejects is worse than no config, because it converts
// "run one command" into "read the reference".
const starterConfig = `# llm-eval.yaml — heros configuration.
#
# Everything here is OPTIONAL: heros works with this file absent. It exists so the values you care about live
# in your repository, reviewed like any other code, instead of in a shell history.

# Which paths discovery reads. Defaults to the whole repository.
# include:
#   - src/
#   - agents/

# Quality gates. A gate is YOUR policy, not ours: when one fails, "heros eval" exits 1 — distinct from 2
# (the tool broke) so your CI can tell "this change is worse" from "the tool is broken".
gates:
  # Fail the build if quality drops below this (0..1). Commented out: a gate you did not choose is a gate
  # that will fail for a reason you cannot explain.
  # min_quality: 0.7
  # max_cost_per_run: 0.05
  # latency_sla_ms: 20000

# Evaluation shape. More seeds cost more and narrow the confidence interval; these defaults are the ones
# "heros eval" uses when you pass nothing.
eval:
  seeds: 5
  cases: 8
`

// Init writes a starter config (task 5.2). Idempotent, and it never clobbers an existing file without being
// told to.
//
// The refusal is the point: a `heros init` that overwrote a tuned config would destroy work that is not
// reproducible, and "I ran init again out of habit" is a thing people do. `--force` exists for the case where
// overwriting is what was meant, so the safe default costs nothing.
func Init(cfg Config, s Streams, env func(string) (string, bool), goos string) error {
	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	path := cfg.Get("config")
	if path == "" {
		path = filepath.Join(repo, "llm-eval.yaml")
	}
	force := cfg.Get("force") == "true"

	if _, err := os.Stat(path); err == nil && !force {
		s.Narratef("heros init: %s already exists — leaving it alone.", path)
		s.Narratef("            Nothing was changed. Pass --force to overwrite it, or edit it directly.")
		return s.EmitJSON("init", ExitOK, InitData{Path: path, Created: false, Unchanged: true}, nil, nil)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return operational("init: cannot create "+filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(starterConfig), 0o644); err != nil {
		return operational("init: cannot write "+path, err)
	}
	// Drawn on the CREATE path only. `init` is idempotent and people re-run it out of habit; a wordmark over
	// "leaving it alone" would announce something that did not happen. This is the moment a repository first
	// has a heros config in it, which is the only first there is here.
	s.narrateMark(isTerminal(s.Err), goos, env)
	s.Narratef("heros init: wrote %s", path)
	s.Narratef("            Every value in it is already a working default; nothing needs editing to run.")
	s.Narratef("")
	s.Narratef("Next: heros discover")
	return s.EmitJSON("init", ExitOK, InitData{Path: path, Created: true}, nil, nil)
}

// ── 5.3 doctor ──────────────────────────────────────────────────────────────────────────────────────────

// CheckState is a doctor check's outcome.
type CheckState string

const (
	// CheckReady means the thing works. Proven, not assumed — see the toolchain check.
	CheckReady CheckState = "ready"
	// CheckActionNeeded means something is missing AND the single next action is named.
	CheckActionNeeded CheckState = "action-needed"
	// CheckNotApplicable means the check does not apply to this repository or this build. It is REPORTED
	// rather than omitted: an absent check reads as a passing one, and "we did not look" is a different
	// fact from "it is fine".
	CheckNotApplicable CheckState = "not-applicable"
)

// Check is one doctor finding.
type Check struct {
	// Name is stable and machine-readable.
	Name string `json:"name"`
	// State is ready / action-needed / not-applicable.
	State CheckState `json:"state"`
	// Detail is what was actually found — a version string, a path, a reason.
	Detail string `json:"detail"`
	// NextAction is the ONE thing to do. Required whenever State is action-needed, and asserted by
	// TestDoctorNamesOneNextActionPerGap: a gap with no action is a support ticket.
	NextAction string `json:"next_action,omitempty"`
}

// DoctorData is the machine payload for `heros doctor`.
type DoctorData struct {
	Ready    bool    `json:"ready"`
	Checks   []Check `json:"checks"`
	Platform string  `json:"platform"`
	// MatrixVersion names the support table this answer came from, so a console/CLI disagreement about
	// whether a platform is supported is diagnosable.
	MatrixVersion string `json:"matrix_version"`
}

// Doctor checks this machine and names the single next action per gap (task 5.3).
//
// # What it deliberately does not do
//
// It does not demand a prerequisite the commands do not need. A doctor that reported "no OPENAI_API_KEY" as a
// problem would be wrong for this build — `eval` uses the deterministic reference runtime and reads no
// provider key at all — and being told to configure something the tool ignores is worse than being told
// nothing, because the user then trusts the rest of the output less.
func Doctor(cfg Config, s Streams, env func(string) (string, bool)) error {
	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	var checks []Check
	checks = append(checks, checkPlatform(runtime.GOOS, runtime.GOARCH))
	checks = append(checks, checkRepoReadable(repo))
	checks = append(checks, checkWriteAccess(repo))
	checks = append(checks, checkToolchains(repo)...)
	checks = append(checks, checkProviderPath(env))
	checks = append(checks, checkAccount())

	ready := true
	for _, c := range checks {
		if c.State == CheckActionNeeded {
			ready = false
		}
	}

	s.Narratef("heros doctor — %s · %s", runtime.GOOS+"/"+runtime.GOARCH, ToolVersion)
	s.Narratef("")
	for _, c := range checks {
		switch c.State {
		case CheckReady:
			s.Narratef("  ✅ %-22s %s", c.Name, c.Detail)
		case CheckNotApplicable:
			s.Narratef("  ·  %-22s %s", c.Name, c.Detail)
		case CheckActionNeeded:
			s.Narratef("  ⛔ %-22s %s", c.Name, c.Detail)
			s.Narratef("     → %s", c.NextAction)
		}
	}
	s.Narratef("")
	if ready {
		s.Narratef("Ready. Next: heros discover")
	} else {
		s.Narratef("Not ready yet — each ⛔ above names the one thing to do.")
	}

	// Exit 0 either way. `doctor` reports; it is not a gate. A non-zero exit would make it unusable as the
	// first thing a new user runs (a red exit code reads as "the install is broken") and would collide with
	// the exit-code contract, where 1 means a gate the CUSTOMER configured failed.
	code := ExitOK
	data := DoctorData{Ready: ready, Checks: checks, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		MatrixVersion: distribution.MatrixVersion()}
	return s.EmitJSON("doctor", code, data, nil, nil)
}

func checkPlatform(goos, goarch string) Check {
	target, ok := distribution.TargetFor(goos, goarch)
	switch {
	case !ok:
		return Check{Name: "platform", State: CheckActionNeeded,
			Detail:     goos + "/" + goarch + " is not in the supported-target matrix",
			NextAction: "open an issue naming this platform — adding a row is a new native runner, not a redesign"}
	case target.Support == distribution.SupportLimit:
		return Check{Name: "platform", State: CheckActionNeeded,
			Detail: target.Platform + " — " + target.Limit, NextAction: target.Answer}
	default:
		return Check{Name: "platform", State: CheckReady, Detail: target.Platform}
	}
}

func checkRepoReadable(repo string) Check {
	info, err := os.Stat(repo)
	if err != nil {
		return Check{Name: "repository", State: CheckActionNeeded,
			Detail:     "cannot read " + repo + ": " + err.Error(),
			NextAction: "cd into a repository, or pass --repo <path>"}
	}
	if !info.IsDir() {
		return Check{Name: "repository", State: CheckActionNeeded, Detail: repo + " is not a directory",
			NextAction: "pass --repo <path-to-a-repository>"}
	}
	abs, _ := filepath.Abs(repo)
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		// Not an error: discovery reads source, not history. But `apply` derives the source revision from
		// git, so saying so now beats a confusing failure later.
		return Check{Name: "repository", State: CheckReady,
			Detail: abs + " (no .git — discover and eval work; apply needs a revision, so pass --commit)"}
	}
	return Check{Name: "repository", State: CheckReady, Detail: abs}
}

// checkWriteAccess proves write access by WRITING, not by reading permission bits.
//
// A permission check on a read-only mount, a full disk, or an SELinux-restricted directory can report writable
// and then fail — and it fails at the end of a long `discover`, after the work is done, which is the most
// expensive moment to find out.
func checkWriteAccess(repo string) Check {
	probe := filepath.Join(repo, ".heros-write-probe")
	if err := os.WriteFile(probe, []byte("heros doctor"), 0o600); err != nil {
		return Check{Name: "write access", State: CheckActionNeeded,
			Detail: "cannot write into " + repo + ": " + err.Error(),
			NextAction: "run from a writable checkout, or pass --out/--report paths on a writable filesystem " +
				"(discover writes the IR and the report)"}
	}
	_ = os.Remove(probe)

	// The config directory matters too, but only for `login` — so its absence is not a gap for a user who
	// never links a run, and reporting it as one would be demanding a prerequisite the local commands do not
	// need.
	cfgDir := filepath.Dir(credentialPath())
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return Check{Name: "write access", State: CheckReady,
			Detail: repo + " is writable (config dir " + cfgDir + " is not — only `heros login` needs it)"}
	}
	return Check{Name: "write access", State: CheckReady, Detail: repo + " and " + cfgDir}
}

// languageMarkers maps a registered language to the files that mean "this repository is written in it".
//
// A heuristic, and named as one in the output. The alternative — running full discovery inside `doctor` — would
// make the first command a new user runs the slowest, and would fail for the very repositories doctor exists to
// diagnose.
var languageMarkers = map[string][]string{
	"go":         {"go.mod"},
	"python":     {"pyproject.toml", "requirements.txt", "setup.py", "Pipfile"},
	"typescript": {"tsconfig.json"},
	"javascript": {"package.json"},
	"rust":       {"Cargo.toml"},
	"java":       {"pom.xml", "build.gradle", "build.gradle.kts"},
	"kotlin":     {"build.gradle.kts", "settings.gradle.kts"},
}

// checkToolchains probes the verification toolchain for each language the repository appears to use.
//
// # Why it runs the REAL verifier probe
//
// Because `exec.LookPath` is not the question. On stock macOS `/usr/bin/javac` exists, is executable, and
// prints "Unable to locate a Java Runtime" when run: a lookup-based check passes and the verification later
// fails, and — per ADR-003 — that failure would be recorded as "your transform does not compile", which is an
// ops problem laundered into a verdict about the user's code.
//
// So doctor asks the verifier itself, on an empty directory, and treats ONLY a *worktree.ToolchainError as a
// finding. Any other outcome means the toolchain answered for itself, which is the property being checked.
func checkToolchains(repo string) []Check {
	registered := map[string]bool{}
	for _, l := range transform.RegisteredLanguages() {
		registered[l] = true
	}

	var langs []string
	for lang, markers := range languageMarkers {
		if !registered[lang] {
			continue
		}
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(repo, m)); err == nil {
				langs = append(langs, lang)
				break
			}
		}
	}
	sort.Strings(langs)

	if len(langs) == 0 {
		return []Check{{Name: "toolchains", State: CheckNotApplicable,
			Detail: "no language marker found in this repository (looked for go.mod, pyproject.toml, " +
				"package.json, Cargo.toml, pom.xml, build.gradle…) — discovery still reads the source, and " +
				"`apply` will name the toolchain it needs if one is missing"}}
	}

	empty, err := os.MkdirTemp("", "heros-doctor-probe-")
	if err != nil {
		return []Check{{Name: "toolchains", State: CheckActionNeeded,
			Detail:     "cannot create a temporary directory to probe the toolchains: " + err.Error(),
			NextAction: "check that TMPDIR points somewhere writable"}}
	}
	defer func() { _ = os.RemoveAll(empty) }()

	var out []Check
	for _, lang := range langs {
		v, verr := worktree.VerifierFor(lang)
		if verr != nil {
			out = append(out, Check{Name: "toolchain: " + lang, State: CheckNotApplicable,
				Detail: "this build has no verification gate for " + lang + " — `apply` produces a diff, " +
					"and nothing here claims to have type-checked it"})
			continue
		}
		// A short budget: doctor is the first command a new user runs, and a toolchain probe that hangs
		// would make the tool look broken at the exact moment its job is to reassure.
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		_, err := v.Verify(ctx, empty)
		cancel()

		var te *worktree.ToolchainError
		if errorsAs(err, &te) {
			out = append(out, Check{Name: "toolchain: " + lang, State: CheckActionNeeded,
				Detail:     te.Tool + " is missing or unusable: " + te.Reason,
				NextAction: installHintFor(lang, te.Tool)})
			continue
		}
		// Any other error is a verdict about the empty directory, not about the toolchain — which means the
		// toolchain ran, which is what was being checked.
		out = append(out, Check{Name: "toolchain: " + lang, State: CheckReady,
			Detail: "the " + lang + " gate's toolchain answered for itself"})
	}
	return out
}

// installHintFor names the ONE command to install a missing tool, per platform. Generic advice ("install the
// Java toolchain") is what a user could already infer; the value is in the line they can paste.
func installHintFor(lang, tool string) string {
	perOS := map[string]map[string]string{
		"darwin": {
			"go": "brew install go", "python3": "brew install python@3.12", "mypy": "pipx install mypy",
			"pyright": "brew install pyright", "node": "brew install node", "tsc": "npm i -g typescript",
			"cargo": "brew install rust", "javac": "brew install --cask temurin", "kotlinc": "brew install kotlin",
		},
		"linux": {
			"go":      "install Go from https://go.dev/dl/ (distro packages are often too old)",
			"python3": "apt-get install -y python3", "mypy": "pipx install mypy",
			"pyright": "npm i -g pyright", "node": "apt-get install -y nodejs",
			"tsc": "npm i -g typescript", "cargo": "apt-get install -y cargo",
			"javac": "apt-get install -y default-jdk", "kotlinc": "install from https://kotlinlang.org/docs/command-line.html",
		},
		"windows": {
			"go": "winget install GoLang.Go", "python3": "winget install Python.Python.3.12",
			"node": "winget install OpenJS.NodeJS", "tsc": "npm i -g typescript",
			"cargo": "winget install Rustlang.Rustup", "javac": "winget install EclipseAdoptium.Temurin.21.JDK",
			"kotlinc": "install from https://kotlinlang.org/docs/command-line.html",
		},
	}
	if hint, ok := perOS[runtime.GOOS][tool]; ok {
		return "install " + tool + ": " + hint
	}
	return "install " + tool + " and make sure it is on PATH — the " + lang +
		" verification gate cannot run without it, and heros will not silently downgrade to a weaker gate"
}

// checkProviderPath reports the REAL provider-key path for this build (task 5.3, AI Engineer lens).
//
// # The honest answer, and why it matters that it is honest
//
// This build's `eval` runs `ReferenceRuntime` — a deterministic, offline node runtime. It reads NO provider
// key. So the truthful report is not "your key is configured" or "your key is missing": it is that no key is
// used, and that the numbers `eval` produces are therefore comparable between variants but are not a
// measurement of a live model.
//
// A doctor that checked `OPENAI_API_KEY != ""` and reported ✅ would be the exact failure the AI Engineer lens
// names: a value being set, presented as a working path. Worse, it would give a user confidence that their
// eval scores reflect their model, which is the one thing they must not believe here.
func checkProviderPath(env func(string) (string, bool)) Check {
	// Named because users DO set them, and someone who has set one deserves to be told it is not read rather
	// than left to assume it is.
	var present []string
	for _, k := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY", "AZURE_OPENAI_API_KEY"} {
		if v, ok := env(k); ok && strings.TrimSpace(v) != "" {
			present = append(present, k)
		}
	}
	runtimeName := ReferenceRuntime{}.Name()
	detail := "`eval` in this build runs the " + runtimeName + " runtime: deterministic, offline, and it " +
		"reads no provider key. Scores are comparable between variants but are not a measurement of a live model"
	if len(present) > 0 {
		detail += ". " + strings.Join(present, " and ") + " is set in this environment and is NOT used by this build"
	}
	return Check{Name: "provider keys", State: CheckNotApplicable, Detail: detail}
}

// checkAccount states the free-tier fact rather than checking for a credential (task 5.7).
//
// Reporting "not logged in" as a gap would be demanding a prerequisite the local commands do not need — and
// the whole free-tier promise is that they do not.
func checkAccount() Check {
	if cred, ok := LoadCredential(); ok {
		return Check{Name: "account", State: CheckReady,
			Detail: "linked as " + cred.Identity + " (only `heros link` uses it; every local command works without it)"}
	}
	return Check{Name: "account", State: CheckReady,
		Detail: "no account, and none needed — discover, apply, eval, coverage, doctor and init are free and offline"}
}

// errorsAs is errors.As, wrapped so this file does not import errors purely for one call and so the intent
// reads at the call site: we are asking "is this specifically a toolchain problem?".
func errorsAs(err error, target **worktree.ToolchainError) bool {
	for err != nil {
		if te, ok := err.(*worktree.ToolchainError); ok {
			*target = te
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ── 5.5 verify-release: the shared routine, as a command ────────────────────────────────────────────────

// VerifyReleaseData is the machine payload for `heros verify-release`.
type VerifyReleaseData struct {
	SigningKeyID    string   `json:"signing_key_id"`
	Checked         []string `json:"checked"`
	ManifestEntries int      `json:"manifest_entries"`
}

// VerifyRelease verifies a downloaded release with the one shared routine (task 5.5).
//
// It exists as a command for two reasons. It gives a customer's security reviewer a single thing to run
// instead of a two-command runbook they might do half of. And it is what `scripts/install.sh` calls when a
// previously-installed heros is present — the only non-circular way a shell script can reach an ed25519
// verifier that predates the download it is checking.
//
// Offline: it reads files and verifies bytes. Nothing here opens a socket.
func VerifyRelease(cfg Config, s Streams) error {
	manifestPath, err := cfg.Require("manifest")
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return operational("verify-release: cannot read the manifest "+manifestPath, err)
	}
	sigPath := cfg.Get("sig")
	if sigPath == "" {
		sigPath = manifestPath + ".sig"
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return operational("verify-release: cannot read the signature "+sigPath+
			" — checksums alone prove the download is intact, not who produced it", err)
	}

	// Which assets to check: the named ones, or every asset in the manifest that exists next to it. The
	// second form is what a reviewer wants ("check everything I downloaded"); it never invents an asset,
	// so a partial download reports as fewer checked rather than as a failure.
	dir := filepath.Dir(manifestPath)
	assets := map[string][]byte{}
	if named := cfg.Get("asset"); named != "" {
		for _, name := range strings.Split(named, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			data, rerr := os.ReadFile(filepath.Join(dir, filepath.Base(name)))
			if rerr != nil {
				return operational("verify-release: cannot read the asset "+name, rerr)
			}
			assets[filepath.Base(name)] = data
		}
	} else {
		for name := range release.ParseSums(string(manifest)) {
			data, rerr := os.ReadFile(filepath.Join(dir, name))
			if rerr != nil {
				continue // not downloaded; reported as "checked N of M" rather than as a failure
			}
			assets[name] = data
		}
	}

	out, verr := release.VerifyBundle(release.Bundle{
		Manifest: manifest, SignatureHex: string(sig), Assets: assets,
	})
	if verr != nil {
		s.Narratef("heros verify-release: ⛔ %v", verr)
		return &ExitError{Code: ExitOperational, Msg: "release verification FAILED — do not use these files", Err: verr}
	}
	s.Narratef("heros verify-release: ✅ %s", out.Describe())
	return s.EmitJSON("verify-release", ExitOK, VerifyReleaseData{
		SigningKeyID: out.SigningKeyID, Checked: out.Checked, ManifestEntries: out.ManifestEntries,
	}, nil, nil)
}

// UpgradeAdvice tells a caller how an installed binary should be upgraded, computed from where it lives.
//
// It is in this package — the offline one — because the DECISION needs no network: a manager-owned binary must
// never be replaced in place (D7), and knowing that requires only the path. `heros upgrade` in
// internal/clilink calls this before it fetches anything, so a brew-installed user is told to run
// `brew upgrade` without a single byte being downloaded.
func UpgradeAdvice(exePath string) (channel string, command string, managerOwned bool) {
	c, owned := distribution.ManagerOwnedChannelFor(exePath)
	if !owned {
		return "", "", false
	}
	return c.Label, distribution.Command(c.Upgrade, ToolVersion), true
}

// ExecutablePath is where this binary lives, resolved through symlinks so a manager's shim is recognised as
// the manager's. Returns "" when it cannot be determined, which callers must treat as "not manager-owned"
// rather than guessing.
func ExecutablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}
