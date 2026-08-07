package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// app.go is the command dispatcher and flag→config wiring. It is TTY-free and non-interactive by
// construction (PRD FR8): a FlagSet parses arguments, a missing required input exits invalid-config
// naming the input, and nothing ever prompts.
//
// The platform-facing commands (login, link, upgrade) are INJECTED as NetCommands rather than implemented
// here, so this package — the whole offline surface — does not link a network stack. That is the offline
// guarantee made structural: the code that runs discovery/apply/eval cannot reach the network, because there is
// nothing in its build that can open a connection.
//
// Two tests hold it, and they are not redundant — they fail for different reasons:
//
//	TestCLIPackageLinksNoNetworkStack        the BUILD cannot reach net/http, over the whole transitive graph
//	TestLocalWorkflowRunsWithNetworkingDenied  the BEHAVIOUR survives a deny-all dialer at run time
//
// 🔴 The structural half was FALSE from P13 until it was fixed, and the way it broke is worth knowing, because
// it will be how it breaks again. Nobody added a network call. `internal/telemetry` — five hops down the graph
// from `author.go` — imported `internal/providergateway` purely to NAME the struct its observer callback is
// handed, and providergateway links an HTTP client and the AWS SDK. One import, for one type name, in a package
// nobody editing the CLI would think to open. The behavioural test stayed green the whole time, which is exactly
// why the structural one has to exist.
//
// The fix was to extract the observation vocabulary into `internal/providercall`, a leaf package with no
// transport: telemetry can name what it is handed without linking what produced it. See that package's doc for
// the reasoning, and providergateway/observe.go for the aliases that kept its API unchanged.

// NetCommands are the platform-facing commands, supplied by a package that may import net/http. When
// nil, invoking a net command is an operational error rather than a panic.
type NetCommands interface {
	Login(cfg Config, s Streams) error
	Link(cfg Config, s Streams) error
	// PushSource transmits a SOURCE SNAPSHOT so the platform can run discovery itself. A separate method
	// from Link, not a flag on it: sending a run's numbers and sending the repository are different
	// things to agree to, and a reviewer must be able to point at the one entry point that sends source.
	PushSource(cfg Config, s Streams) error
	// ReportVerdict transmits a verification VERDICT the CI measured for one platform-issued proposal.
	// Its own method for the same reason PushSource is: a run link, a source snapshot and a measurement
	// that decides whether a change may be delivered are three different things to agree to, and a
	// reviewer must be able to point at the one entry point that sends each.
	ReportVerdict(cfg Config, s Streams) error
	// Upgrade is here rather than in this package for the same reason: it is the ONE command that must
	// reach the network, and keeping it out is what makes "no update check on the hot path" (P20 task 5.6)
	// structural instead of a promise — the code that runs discover/apply/eval does not link a network stack.
	Upgrade(cfg Config, s Streams) error
}

// Main is the process entry point. It parses args, resolves config with provenance, dispatches, and
// returns the exit code. It writes machine output to s.Out and narration to s.Err, and never calls
// os.Exit itself — the caller owns the process boundary.
func Main(args []string, s Streams, env func(string) (string, bool), net NetCommands) int {
	if len(args) < 1 {
		// A bare `heros` GREETS (P20 task 5.1). It used to print usage and exit 3 (invalid-config), which
		// treated curiosity as a malformed invocation — and a tool that exits non-zero when run with no
		// arguments teaches CI authors to append `|| true`, after which a real failure is invisible too.
		return codeOf(Greeting(s, env, runtime.GOOS, runtime.GOARCH), s, "greeting")
	}
	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "-h", "--help", "help":
		usage(s.Err)
		return ExitOK
	case "version":
		return codeOf(Version(s), s, "version")
	}

	cfg, gates, err := parse(cmd, rest, env, s)
	if err != nil {
		return report(err, s, cmd)
	}

	switch cmd {
	case "discover":
		return codeOf(Discover(cfg, s), s, cmd)
	case "apply":
		return codeOf(Apply(cfg, s), s, cmd)
	case "author":
		return codeOf(Author(cfg, s), s, cmd)
	case "eval":
		return codeOf(Eval(cfg, s, ReferenceRuntime{}, gates), s, cmd)
	case "coverage":
		return codeOf(Coverage(cfg, s), s, cmd)
	case "init":
		return codeOf(Init(cfg, s, env, runtime.GOOS), s, cmd)
	case "doctor":
		return codeOf(Doctor(cfg, s, env), s, cmd)
	case "verify-release":
		return codeOf(VerifyRelease(cfg, s), s, cmd)
	case "status":
		authed, id := credentialStatus(cfg, env)
		return codeOf(Status(cfg, s, authed, id), s, cmd)
	case "login":
		if net == nil {
			return report(operational("login is a platform command and is unavailable in this build", nil), s, cmd)
		}
		return codeOf(net.Login(cfg, s), s, cmd)
	case "link":
		if net == nil {
			return report(operational("link is a platform command and is unavailable in this build", nil), s, cmd)
		}
		return codeOf(net.Link(cfg, s), s, cmd)
	case "push-source":
		if net == nil {
			return report(operational("push-source is a platform command and is unavailable in this build", nil), s, cmd)
		}
		return codeOf(net.PushSource(cfg, s), s, cmd)
	case "report-verdict":
		if net == nil {
			return report(operational("report-verdict is a platform command and is unavailable in this build", nil), s, cmd)
		}
		return codeOf(net.ReportVerdict(cfg, s), s, cmd)
	case "upgrade":
		if net == nil {
			return report(operational("upgrade needs the network and is unavailable in this build; "+
				"reinstall with the install script instead", nil), s, cmd)
		}
		return codeOf(net.Upgrade(cfg, s), s, cmd)
	default:
		s.Narratef("heros: unknown command %q", cmd)
		usage(s.Err)
		return ExitInvalidCfg
	}
}

// parse builds the resolved Config and Gates for a command from flags → env → project file → defaults.
func parse(cmd string, args []string, env func(string) (string, bool), s Streams) (Config, Gates, error) {
	fs := flag.NewFlagSet("heros "+cmd, flag.ContinueOnError)
	fs.SetOutput(s.Err)

	// Common flags. All optional at the flag layer; requiredness is enforced at use via cfg.Require, so
	// a missing input names itself rather than producing a generic flag error.
	repo := fs.String("repo", "", "path to the target repository (default: .)")
	config := fs.String("config", "", "path to llm-eval.yaml")
	out := fs.String("out", "", "output path (IR for discover, diff for apply)")
	report := fs.String("report", "", "discovery report output path")
	spec := fs.String("spec", "", "path to a Variant Spec JSON (apply)")
	commit := fs.String("commit", "", "source revision (default: derived from .git)")
	repoURL := fs.String("repo-url", "", "workflow repo url (default: derived from .git)")
	workflowID := fs.String("workflow-id", "", "workflow id (default: module path)")
	seeds := fs.Int("seeds", 5, "eval seeds")
	cases := fs.Int("cases", 8, "eval cases")
	run := fs.String("run", "", "run id to link")
	token := fs.String("token", "", "platform token (login)")
	// P28 sign-in. 🚫 There is deliberately NO --password: a password in argv is a password in the shell
	// history and in every `ps` on the machine. It arrives by $HEROS_PASSWORD, stdin, or a hidden prompt.
	email := fs.String("email", "", "email address to sign in as (login)")
	device := fs.Bool("device", false, "approve this terminal from a browser instead of typing a password (login)")
	org := fs.String("org", "", "organization to sign in to, when you belong to more than one (login)")
	withIR := fs.String("with-ir", "", "ALSO transmit this workflow's structure as a second, opt-in payload (link)")
	dryRun := fs.Bool("dry-run", false, "render the exact link payload without transmitting it")

	// Onboarding + release-verification flags (P20 section 5).
	force := fs.Bool("force", false, "overwrite an existing config (init)")
	manifest := fs.String("manifest", "", "path to a downloaded SHA256SUMS (verify-release)")
	sig := fs.String("sig", "", "path to the detached signature (verify-release; default <manifest>.sig)")
	asset := fs.String("asset", "", "comma-separated asset names to check (verify-release; default: every listed asset present on disk)")

	// Authoring flags (P13 13c). Every value is optional at this layer; `author` enforces that at least
	// one of them is present, so "you passed nothing to change" names itself rather than arriving as a
	// generic flag error.
	node := fs.String("node", "", "node id to change (author)")
	model := fs.String("model", "", "set the node's model ref (author)")
	prompt := fs.String("prompt", "", "set the node's prompt version ref (author)")
	contextPolicy := fs.String("context-policy", "", "set the node's context policy ref (author)")
	skillRefs := fs.String("skills", "", "set the node's bound skills, comma-separated, in order (author)")
	toolKeep := fs.String("tools", "", "keep only these discovered tools, comma-separated (author)")
	applyMode := fs.String("apply-mode", "", "inline | bound (author)")
	dropTolerance := fs.String("drop-tolerance", "", "declare the node's context drop tolerance, 0..1 (author)")
	clearDrop := fs.Bool("clear-drop-tolerance", false, "remove a declared drop tolerance — NOT the same as 0 (author)")
	applyAuthored := fs.Bool("apply", false, "write the diff; without it, author previews and writes nothing")

	// Gate flags (customer-configured quality gates).
	minQuality := fs.Float64("min-quality", -1, "fail the build if quality is below this (0..1)")
	maxCost := fs.Float64("max-cost-per-run", -1, "fail the build if cost/run exceeds this (USD)")
	latencySLA := fs.Float64("latency-sla-ms", -1, "fail the build if latency exceeds this (ms)")

	if err := fs.Parse(args); err != nil {
		return Config{}, Gates{}, invalidConfig("invalid flags: " + err.Error())
	}

	defaults := map[string]string{
		"repo": ".", "config": "", "out": "", "report": "", "spec": "",
		"commit": "", "repo-url": "", "workflow-id": "", "seeds": "5", "cases": "8",
		"run": "", "dry-run": "false",
		"force": "false", "manifest": "", "sig": "", "asset": "",
		"node": "", "model": "", "prompt": "", "context-policy": "", "skills": "", "tools": "",
		"apply-mode": "", "drop-tolerance": "", "clear-drop-tolerance": "false", "apply": "false",
	}
	r := NewResolver(defaults)
	r.LoadEnv(env)
	// Project file resolved relative to the (flag or default) repo.
	repoForFile := *repo
	if repoForFile == "" {
		if v, ok := env(EnvPrefix + "REPO"); ok {
			repoForFile = v
		} else {
			repoForFile = "."
		}
	}
	if err := r.LoadFile(repoForFile + string(os.PathSeparator) + ProjectFile); err != nil {
		return Config{}, Gates{}, err
	}

	// Record only flags the user actually set (flag.Visit), so a default flag value does not shadow env
	// or the project file — precedence must reflect intent, not the zero value.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	put := func(name, val string) {
		if set[name] {
			r.SetFlag(name, val)
		}
	}
	put("repo", *repo)
	put("config", *config)
	put("out", *out)
	put("node", *node)
	put("model", *model)
	put("prompt", *prompt)
	put("context-policy", *contextPolicy)
	put("skills", *skillRefs)
	put("tools", *toolKeep)
	put("apply-mode", *applyMode)
	put("drop-tolerance", *dropTolerance)
	put("clear-drop-tolerance", strconv.FormatBool(*clearDrop))
	put("apply", strconv.FormatBool(*applyAuthored))
	put("report", *report)
	put("spec", *spec)
	put("commit", *commit)
	put("repo-url", *repoURL)
	put("workflow-id", *workflowID)
	put("seeds", strconv.Itoa(*seeds))
	put("cases", strconv.Itoa(*cases))
	put("run", *run)
	put("token", *token)
	put("email", *email)
	put("device", strconv.FormatBool(*device))
	put("org", *org)
	put("dry-run", strconv.FormatBool(*dryRun))
	put("with-ir", *withIR)
	put("force", strconv.FormatBool(*force))
	put("manifest", *manifest)
	put("sig", *sig)
	put("asset", *asset)

	cfg := r.Resolve()

	gates := Gates{}
	if set["min-quality"] && *minQuality >= 0 {
		v := *minQuality
		gates.MinQuality = &v
	}
	if set["max-cost-per-run"] && *maxCost >= 0 {
		v := *maxCost
		gates.MaxCostPerRun = &v
	}
	if set["latency-sla-ms"] && *latencySLA >= 0 {
		v := *latencySLA
		gates.LatencySLAMs = &v
	}
	return cfg, gates, nil
}

// credentialStatus reports whether a stored credential exists and its non-secret identity. It reads the
// credential file's presence only; it never reads or reveals the token value.
func credentialStatus(cfg Config, env func(string) (string, bool)) (bool, string) {
	if _, ok := env(EnvPrefix + "PLATFORM_TOKEN"); ok {
		return true, "env-token"
	}
	id, ok := storedIdentity()
	return ok, id
}

// codeOf maps a command's error to an exit code and reports a failure envelope/narration when needed.
func codeOf(err error, s Streams, cmd string) int {
	if err == nil {
		return ExitOK
	}
	return report(err, s, cmd)
}

// report writes the failure to stderr (narration) and returns the exit code. If the error already
// emitted a machine envelope (gate path), it is not re-emitted.
func report(err error, s Streams, cmd string) int {
	var ee *ExitError
	if asExit(err, &ee) {
		s.Narratef("heros %s: %s", cmd, ee.Error())
		return ee.Code
	}
	s.Narratef("heros %s: %s", cmd, err.Error())
	return ExitOperational
}

func asExit(err error, target **ExitError) bool {
	for err != nil {
		if ee, ok := err.(*ExitError); ok {
			*target = ee
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

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimLeft(`
heros — the LLM-workflow evaluation CLI (free on every plan; works offline with no account)

Usage:
  heros <command> [flags]

Getting started (no account, no network, no config file):
  init       write a starter llm-eval.yaml whose defaults already work (never clobbers yours)
  doctor     check this machine is ready, and name the ONE next action for each gap

Local commands (no account, no network):
  discover   extract the Workflow IR + discovery report from a repository
  apply      realize a Variant Spec as a reviewable diff (worktree-isolated)
  author     make a change yourself: preflight it, and with --apply write its diff
             (answers admissible / refused-by-name / not-yet-measurable; never merges)
  eval       run a scored, multi-seed evaluation with your own keys
  coverage   show what this build can APPLY, per axis and language — every registered
             language appears on every axis, and a gap names what is missing
  status     show effective config with provenance, and the fixed contract facts
  version    print the CLI version and the contract versions it implements
  verify-release  verify a downloaded release: checksums, then the signature over the manifest,
             against the release key compiled into this binary (offline; the same routine
             the installer and 'heros upgrade' use)

Update (the only command that reaches the network for a version; nothing else ever checks):
  upgrade    fetch the latest release, verify it, replace this binary in place — a no-op when
             current, and it defers to your package manager when one owns this file

Platform commands (explicit, authenticated; transmit only to https://heros-agent.space):
  login      sign in and store a credential (0600). Three ways, in this order:
               --email you@example.com   with $HEROS_PASSWORD, stdin, or a hidden prompt.
                                         Stores a PERSONAL credential: removing you from the
                                         organization ends it at the next request.
               --token <credential>      the MACHINE path, for CI. Names nobody, so it survives
                                         the offboarding of whoever created it.
               --device                  approve this terminal from a browser instead of typing
                                         a password.
             There is no --password flag: a password in an argument is in your shell history
             and in every process listing on the machine.
  link       transmit a run's allowlisted metrics + structure to the platform
             (use --dry-run to print the exact payload without sending it)
  push-source  send a SOURCE SNAPSHOT (git archive of a revision) so the platform can run
             discovery and pattern classification itself — this is what makes the hosted
             graph carry real labels rather than unclassified dots. The largest thing this
             CLI ever sends; --dry-run reports exactly what it contains and sends nothing,
             and --forget deletes a snapshot the platform is holding.
  report-verdict  report a verification verdict your CI measured, for a proposal the platform
             issued. The platform cannot measure one itself — the gate needs your eval cases,
             your traces and your provider, and none of them leaves your environment. It
             transmits LESS than the file it reads: the case ids and the free-text reason stay
             on your machine and the counts cross instead. --dry-run prints the exact payload
             and the account of what was withheld, and sends nothing.

Exit codes: 0 ok · 1 configured-gate-failed · 2 operational-error · 3 invalid-config
`, "\n"))
}
