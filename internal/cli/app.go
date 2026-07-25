package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// app.go is the command dispatcher and flag→config wiring. It is TTY-free and non-interactive by
// construction (PRD FR8): a FlagSet parses arguments, a missing required input exits invalid-config
// naming the input, and nothing ever prompts.
//
// The two platform-facing commands (login, link) are INJECTED as NetCommands rather than implemented
// here, so this package — the whole offline surface — never imports net/http. That is the offline
// guarantee made structural: the code that runs discovery/apply/eval cannot reach the network because
// it does not link the network in.

// NetCommands are the platform-facing commands, supplied by a package that may import net/http. When
// nil, invoking a net command is an operational error rather than a panic.
type NetCommands interface {
	Login(cfg Config, s Streams) error
	Link(cfg Config, s Streams) error
}

// Main is the process entry point. It parses args, resolves config with provenance, dispatches, and
// returns the exit code. It writes machine output to s.Out and narration to s.Err, and never calls
// os.Exit itself — the caller owns the process boundary.
func Main(args []string, s Streams, env func(string) (string, bool), net NetCommands) int {
	if len(args) < 1 {
		usage(s.Err)
		return ExitInvalidCfg
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
	case "eval":
		return codeOf(Eval(cfg, s, ReferenceRuntime{}, gates), s, cmd)
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
	dryRun := fs.Bool("dry-run", false, "render the exact link payload without transmitting it")

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
	put("report", *report)
	put("spec", *spec)
	put("commit", *commit)
	put("repo-url", *repoURL)
	put("workflow-id", *workflowID)
	put("seeds", strconv.Itoa(*seeds))
	put("cases", strconv.Itoa(*cases))
	put("run", *run)
	put("token", *token)
	put("dry-run", strconv.FormatBool(*dryRun))

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
	fmt.Fprint(w, strings.TrimLeft(`
heros — the LLM-workflow evaluation CLI (free on every plan; works offline with no account)

Usage:
  heros <command> [flags]

Local commands (no account, no network):
  discover   extract the Workflow IR + discovery report from a repository
  apply      realize a Variant Spec as a reviewable diff (worktree-isolated)
  eval       run a scored, multi-seed evaluation with your own keys
  status     show effective config with provenance, and the fixed contract facts
  version    print the CLI version and the contract versions it implements

Platform commands (explicit, authenticated; transmit only to https://heros-agent.space):
  login      store a platform token
  link       transmit a run's allowlisted metrics + structure to the platform
             (use --dry-run to print the exact payload without sending it)

Exit codes: 0 ok · 1 configured-gate-failed · 2 operational-error · 3 invalid-config
`, "\n"))
}
