package cli

import "sort"

// registry.go is the COMMAND REGISTRY — the single declarative description of this binary's command
// surface, and the artifact P23's CLI reference is generated from.
//
// # Why this exists rather than a documentation page somebody maintains
//
// P23 Decision 14: the CLI fence runs in BOTH directions. Documentation naming a command that does not
// exist is the failure people expect; the one that actually accumulates is the inverse — **a command that
// exists and is undocumented** — because adding a subcommand is a normal Tuesday and remembering the
// reference is not.
//
// Both directions need one machine-readable answer to "what commands are there". That is this file:
//
//	cmd/docsfacts                  emits it as JSON for the console's generators
//	web/console/scripts/scan-cli   fails the build on a registry entry with no reference entry,
//	                               on documentation naming a command or flag that is not here,
//	                               and on a documented exit code whose meaning disagrees with exit.go
//
// # The drift guard
//
// A registry that lists a command `Main` does not dispatch, or misses one it does, would document a
// fiction. `TestRegistryMatchesDispatch` parses this package's own dispatch switch and asserts the two
// sets are equal — so adding a `case` without a registry entry is a RED BUILD, which is the only version
// of "remember the docs" that survives a busy week.

// Availability says what a command needs before it can do anything.
type Availability string

const (
	// AvailOffline: runs with no account and no network. The whole local surface is this, and it is
	// structural rather than aspirational — this package links no network stack.
	AvailOffline Availability = "offline"
	// AvailNetwork: needs the network and a platform account. Injected as NetCommands, so a build without
	// them reports "unavailable in this build" rather than panicking.
	AvailNetwork Availability = "network"
	// AvailNetworkNoAccount: needs the network but no account (upgrade fetches a public release).
	AvailNetworkNoAccount Availability = "network-no-account"
)

// Flag is one documented flag. Every field here is something a reader needs and cannot infer.
type Flag struct {
	Name string `json:"name"`
	// Type is the value's shape: string | int | float | bool | path | duration.
	Type string `json:"type"`
	// Default is the value used when nothing sets it. "" means "unset, and the command says so".
	Default string `json:"default"`
	// Env is the environment variable that sets the same value, or "" when there is none.
	Env     string `json:"env"`
	Summary string `json:"summary"`
}

// Command is one subcommand of `heros`.
type Command struct {
	Name    string       `json:"name"`
	Summary string       `json:"summary"`
	Avail   Availability `json:"availability"`
	// Flags are the flags this command reads. A flag the command ignores is not listed: a reference that
	// lists every flag on every command teaches readers that flags do nothing.
	Flags []string `json:"flags"`
	// Example is a RUNNABLE invocation (P23 task 6.5). Not a template, not a placeholder-in-angle-brackets.
	Example string `json:"example"`
	// Success is what the reader should see when it worked.
	Success string `json:"success"`
	// SuccessExit is the exit code on success. Always 0; stated per command because a reference that only
	// documents failures leaves a CI author guessing at the one they branch on most.
	SuccessExit int `json:"success_exit"`
	// Prerequisite states what must already exist for Example to run, or "" when nothing must.
	//
	// 🔴 This field exists because running the documentation against a real repository found three
	// examples that exit non-zero when a reader types them cold — `apply` and `author` need a Variant
	// Spec on disk, and `verify-release` needs a downloaded manifest. Each was documented as "exit code
	// 0", which is the sentence a reader tests first.
	//
	// A "runnable invocation" (task 6.5) that cannot run is not one. Stating the prerequisite is the
	// honest fix: the reader learns what to have ready instead of learning that our examples do not work.
	Prerequisite string `json:"prerequisite,omitempty"`
	// Unavailable is the exact outcome when this command is not in the build (network commands only).
	// Empty for offline commands. Documenting it is task 6.3: a reader should meet this sentence in the
	// reference rather than at their terminal.
	Unavailable string `json:"unavailable,omitempty"`
}

// flags is the flag catalogue, keyed by name. Declared once so two commands that share a flag cannot
// describe it differently — which is how a reference ends up with two defaults for `--repo`.
var flags = map[string]Flag{
	"repo":        {Name: "repo", Type: "path", Default: ".", Env: EnvPrefix + "REPO", Summary: "the target repository"},
	"config":      {Name: "config", Type: "path", Default: "", Env: EnvPrefix + "CONFIG", Summary: "path to llm-eval.yaml"},
	"out":         {Name: "out", Type: "path", Default: "", Env: EnvPrefix + "OUT", Summary: "output path (IR for discover, diff for apply)"},
	"report":      {Name: "report", Type: "path", Default: "", Env: EnvPrefix + "REPORT", Summary: "discovery report output path"},
	"spec":        {Name: "spec", Type: "path", Default: "", Env: EnvPrefix + "SPEC", Summary: "path to a Variant Spec JSON"},
	"commit":      {Name: "commit", Type: "string", Default: "", Env: EnvPrefix + "COMMIT", Summary: "source revision (default: derived from .git)"},
	"repo-url":    {Name: "repo-url", Type: "string", Default: "", Env: EnvPrefix + "REPO_URL", Summary: "workflow repo url (default: derived from .git)"},
	"workflow-id": {Name: "workflow-id", Type: "string", Default: "", Env: EnvPrefix + "WORKFLOW_ID", Summary: "workflow id (discover/apply default to the module path; push-source REQUIRES it, because guessing would file a snapshot under the wrong workflow)"},
	"seeds":       {Name: "seeds", Type: "int", Default: "5", Env: EnvPrefix + "SEEDS", Summary: "evaluation seeds"},
	"cases":       {Name: "cases", Type: "int", Default: "8", Env: EnvPrefix + "CASES", Summary: "evaluation cases"},
	"run":         {Name: "run", Type: "string", Default: "", Env: EnvPrefix + "RUN", Summary: "run id to link"},
	"token":       {Name: "token", Type: "string", Default: "", Env: EnvPrefix + "TOKEN", Summary: "platform token (login)"},
	// One catalogue entry, two commands, so the wording must be true of both: `link` prints the exact
	// payload, `push-source` prints what the snapshot contains (it is far too large to print). Saying
	// "the exact link payload" here documented a behaviour push-source does not have.
	"dry-run":              {Name: "dry-run", Type: "bool", Default: "false", Env: EnvPrefix + "DRY_RUN", Summary: "show exactly what would be transmitted, and transmit nothing (link: the payload itself; push-source: the snapshot's revision, file count and size)"},
	"with-ir":              {Name: "with-ir", Type: "path", Default: "", Env: EnvPrefix + "WITH_IR", Summary: "ALSO transmit this workflow's structure (symbols, files, line spans, models, tool counts) as a second, opt-in payload — never prompt text or source (link)"},
	"force":                {Name: "force", Type: "bool", Default: "false", Env: EnvPrefix + "FORCE", Summary: "overwrite an existing config"},
	"proposal":             {Name: "proposal", Type: "string", Default: "", Env: EnvPrefix + "PROPOSAL", Summary: "the platform-issued proposal id a verdict measures (report-verdict). Taken from the flag, never from the verdict file: where the two disagree the command refuses rather than attaching a measurement to the wrong change"},
	"from":                 {Name: "from", Type: "string", Default: "", Env: EnvPrefix + "FROM", Summary: "path to the verdict your verification run produced, or `-` for stdin (report-verdict). Its case ids and free-text reason are NOT transmitted"},
	"revision":             {Name: "revision", Type: "string", Default: "", Env: EnvPrefix + "REVISION", Summary: "the commit the verification ran at (report-verdict) — a revision id, never the code at it"},
	"forget":               {Name: "forget", Type: "bool", Default: "false", Env: EnvPrefix + "FORGET", Summary: "DELETE a previously pushed source snapshot from the platform instead of sending one (push-source)"},
	"manifest":             {Name: "manifest", Type: "path", Default: "", Env: EnvPrefix + "MANIFEST", Summary: "path to a downloaded SHA256SUMS"},
	"sig":                  {Name: "sig", Type: "path", Default: "", Env: EnvPrefix + "SIG", Summary: "detached signature; defaults to the manifest path with .sig appended"},
	"asset":                {Name: "asset", Type: "string", Default: "", Env: EnvPrefix + "ASSET", Summary: "comma-separated asset names to check"},
	"node":                 {Name: "node", Type: "string", Default: "", Env: EnvPrefix + "NODE", Summary: "node id to change"},
	"model":                {Name: "model", Type: "string", Default: "", Env: EnvPrefix + "MODEL", Summary: "set the node's model ref"},
	"prompt":               {Name: "prompt", Type: "string", Default: "", Env: EnvPrefix + "PROMPT", Summary: "set the node's prompt version ref"},
	"context-policy":       {Name: "context-policy", Type: "string", Default: "", Env: EnvPrefix + "CONTEXT_POLICY", Summary: "set the node's context policy ref"},
	"skills":               {Name: "skills", Type: "string", Default: "", Env: EnvPrefix + "SKILLS", Summary: "bound skills, comma-separated, in order"},
	"tools":                {Name: "tools", Type: "string", Default: "", Env: EnvPrefix + "TOOLS", Summary: "keep only these discovered tools, comma-separated"},
	"apply-mode":           {Name: "apply-mode", Type: "string", Default: "", Env: EnvPrefix + "APPLY_MODE", Summary: "inline | bound"},
	"drop-tolerance":       {Name: "drop-tolerance", Type: "float", Default: "", Env: EnvPrefix + "DROP_TOLERANCE", Summary: "declare the node's context drop tolerance, 0..1"},
	"clear-drop-tolerance": {Name: "clear-drop-tolerance", Type: "bool", Default: "false", Env: EnvPrefix + "CLEAR_DROP_TOLERANCE", Summary: "remove a declared drop tolerance — NOT the same as 0"},
	"apply":                {Name: "apply", Type: "bool", Default: "false", Env: EnvPrefix + "APPLY", Summary: "write the diff; without it, author previews and writes nothing"},
	// The gate flags are deliberately NOT env-settable. A quality gate that a stray environment variable
	// can relax is a gate that silently stops gating on somebody's laptop, and the failure is invisible:
	// the build goes green. `-1` is "unset", which is why the default reads as it does.
	"min-quality":      {Name: "min-quality", Type: "float", Default: "unset", Env: "", Summary: "fail if quality is below this (0..1)"},
	"max-cost-per-run": {Name: "max-cost-per-run", Type: "float", Default: "unset", Env: "", Summary: "fail if cost per run exceeds this (USD)"},
	"latency-sla-ms":   {Name: "latency-sla-ms", Type: "float", Default: "unset", Env: "", Summary: "fail if latency exceeds this (ms)"},
}

// commands is the registry. Adding a `case` to Main's dispatch without adding an entry here fails
// TestRegistryMatchesDispatch; adding an entry here without a reference page fails `scan-cli`.
var commands = []Command{
	{
		Name: "help", Summary: "print the command surface and the exit-code contract", Avail: AvailOffline,
		Example: "heros help", Success: "the usage text, listing every command and the four exit codes", SuccessExit: 0,
	},
	{
		Name: "version", Summary: "print the CLI version and the platform contract version it implements", Avail: AvailOffline,
		Example: "heros version", Success: "the tool version and the contract version, one per line", SuccessExit: 0,
	},
	{
		Name: "init", Summary: "write a starter llm-eval.yaml whose defaults already work", Avail: AvailOffline,
		Flags:       []string{"repo", "config", "force"},
		Example:     "heros init --repo .",
		Success:     "a new llm-eval.yaml, and a line naming the file it wrote. It never clobbers an existing one without --force.",
		SuccessExit: 0,
	},
	{
		Name: "doctor", Summary: "check this machine is ready, and name the ONE next action for each gap", Avail: AvailOffline,
		Flags:       []string{"repo", "config"},
		Example:     "heros doctor",
		Success:     "one line per check, and for each gap the single next action that closes it",
		SuccessExit: 0,
	},
	{
		Name: "discover", Summary: "extract the Workflow IR and discovery report from a repository", Avail: AvailOffline,
		Flags:       []string{"repo", "config", "out", "report", "commit", "repo-url", "workflow-id"},
		Example:     "heros discover --repo . --out ir.json --report discovery.json",
		Success:     "the IR at --out and the report at --report, and a summary naming how many call sites were found",
		SuccessExit: 0,
	},
	{
		Name: "apply", Summary: "realize a Variant Spec as a reviewable diff, in an isolated worktree", Avail: AvailOffline,
		Flags:        []string{"repo", "config", "out", "spec", "commit", "repo-url", "workflow-id"},
		Example:      "heros apply --repo . --spec variant.json --out change.diff",
		Prerequisite: "a Variant Spec JSON at --spec — see the schema reference for its shape.",
		Success:      "a unified diff at --out. Your working tree is untouched — the change is realized in a worktree.",
		SuccessExit:  0,
	},
	{
		Name: "author", Summary: "make a change yourself: preflight it, and with --apply write its diff", Avail: AvailOffline,
		Flags: []string{
			"repo", "config", "out", "spec", "commit", "repo-url", "workflow-id",
			"node", "model", "prompt", "context-policy", "skills", "tools", "apply-mode",
			"drop-tolerance", "clear-drop-tolerance", "apply",
		},
		// 🔴 The `--spec` was MISSING from this example until running the documentation against a real
		// repository exited 3 with `missing required input "spec"`. The line could never have worked as
		// printed, and no fence could see it: the flag is real, the command is real, and only running it
		// tells you the invocation is incomplete.
		Example:      "heros author --repo . --spec variant.json --node n_triage --model anthropic/claude-sonnet-5",
		Prerequisite: "a Variant Spec JSON at --spec. `author` changes an existing spec; it does not create the first one.",
		Success:      "one of three verdicts — admissible, refused by name, or not yet measurable. Without --apply it writes nothing.",
		SuccessExit:  0,
	},
	{
		Name: "eval", Summary: "run a scored, multi-seed evaluation with your own provider keys", Avail: AvailOffline,
		Flags:       []string{"repo", "config", "seeds", "cases", "commit", "repo-url", "workflow-id", "min-quality", "max-cost-per-run", "latency-sla-ms"},
		Example:     "heros eval --repo . --seeds 5 --cases 8",
		Success:     "a scorecard with a confidence interval per metric. A gate you configured that fails exits 1, not 0.",
		SuccessExit: 0,
	},
	{
		Name: "coverage", Summary: "show what this build can apply, per axis and language", Avail: AvailOffline,
		Example:     "heros coverage",
		Success:     "a matrix of axis by language; every registered language appears on every axis, and a gap names what is missing",
		SuccessExit: 0,
	},
	{
		Name: "status", Summary: "show effective config with provenance, and the fixed contract facts", Avail: AvailOffline,
		Flags:       []string{"repo"},
		Example:     "heros status --repo .",
		Success:     "each setting with the source that won — flag, env, file or default — and which sources were overridden",
		SuccessExit: 0,
	},
	{
		Name: "verify-release", Summary: "verify a downloaded release: checksums, then the signature over the manifest", Avail: AvailOffline,
		Flags:        []string{"manifest", "sig", "asset", "config"},
		Example:      "heros verify-release --manifest SHA256SUMS --sig SHA256SUMS.sig",
		Prerequisite: "the release's SHA256SUMS and SHA256SUMS.sig, downloaded next to the asset — the install page gets them.",
		Success:      "one line per asset confirming its checksum, then the manifest signature verified against the release key compiled into this binary. Any failure is a hard stop.",
		SuccessExit:  0,
	},
	{
		Name: "login", Summary: "store a platform token", Avail: AvailNetwork,
		Flags:       []string{"token"},
		Example:     "heros login --token <your platform token>",
		Success:     "the token stored, and the non-secret identity it resolves to echoed back. The token value is never printed.",
		SuccessExit: 0,
		Unavailable: "login is a platform command and is unavailable in this build",
	},
	{
		Name: "link", Summary: "transmit a run's allowlisted metrics and structure to the platform", Avail: AvailNetwork,
		Flags:       []string{"run", "dry-run", "with-ir", "repo", "config"},
		Example:     "heros link --run run-7 --dry-run",
		Success:     "with --dry-run, the exact payload printed and nothing transmitted. Without it, the run URL the platform stored.",
		SuccessExit: 0,
		Unavailable: "link is a platform command and is unavailable in this build",
	},
	{
		Name: "push-source", Summary: "send a source snapshot so the platform can discover and classify this workflow itself", Avail: AvailNetwork,
		Flags:   []string{"repo", "commit", "workflow-id", "dry-run", "forget"},
		Example: "heros push-source --repo . --dry-run",
		// The success line names what was TRANSMITTED and what came back, in that order. A summary that
		// only reported the graph would let a reader forget that the input was their source.
		Success:      "with --dry-run, what the snapshot contains and its size, and nothing transmitted. Without it, the snapshot pushed and the classified graph the platform discovered from it.",
		SuccessExit:  0,
		Prerequisite: "a git repository with at least one commit — the snapshot is `git archive` of a revision, never the working directory",
		Unavailable:  "push-source is a platform command and is unavailable in this build",
	},
	{
		Name: "report-verdict", Summary: "report a verification verdict your CI measured for a proposal the platform issued", Avail: AvailNetwork,
		Flags:   []string{"proposal", "from", "revision", "dry-run"},
		Example: "heros report-verdict --proposal prop-abc --from verdict.json --dry-run",
		// The success line names what was WITHHELD as well as what was sent. The whole point of the
		// command is that it transmits less than the file it reads, and a summary that only reported the
		// transmission would let a reader assume the file went across.
		Success:      "with --dry-run, the exact payload and the account of what stayed local, and nothing transmitted. Without it, the verdict recorded and the gate result the platform stored.",
		SuccessExit:  0,
		Prerequisite: "a verdict file from your own verification run, and a proposal id the platform issued for your tenant",
		Unavailable:  "report-verdict is a platform command and is unavailable in this build",
	},
	{
		Name: "upgrade", Summary: "fetch the latest release, verify it, and replace this binary in place", Avail: AvailNetworkNoAccount,
		Example:     "heros upgrade",
		Success:     "a no-op when already current. When a package manager owns this file it defers and prints that manager's command rather than overwriting it.",
		SuccessExit: 0,
		Unavailable: "upgrade needs the network and is unavailable in this build; reinstall with the install script instead",
	},
}

// Commands returns the registry, sorted by name so a generated reference has a stable order.
func Commands() []Command {
	out := make([]Command, len(commands))
	copy(out, commands)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FlagSpec looks a flag up in the catalogue.
func FlagSpec(name string) (Flag, bool) {
	f, ok := flags[name]
	return f, ok
}

// ExitCode is one documented exit code and the remedy that goes with it.
type ExitCode struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Meaning string `json:"meaning"`
	// Remedy is what the reader should DO. Three remedies must never share a code, which is the whole
	// reason 1 and 2 are separate: "your gate failed" and "our tool broke" are opposite actions, and a CI
	// step that fails for an unclear reason gets disabled.
	Remedy string `json:"remedy"`
}

// ExitCodes is the exit-code contract, as a contract. It mirrors the constants in exit.go, and
// TestExitCodesMatchConstants asserts the numbers agree — a reference that drifts from the binary is
// worse than none, because a CI pipeline branches on it.
func ExitCodes() []ExitCode {
	return []ExitCode{
		{Code: ExitOK, Name: "ok", Meaning: "the command did what it was asked; no gate failed and nothing broke.", Remedy: "nothing. This is success."},
		{
			Code: ExitGateFailed, Name: "configured-gate-failed",
			Meaning: "a quality gate YOU configured failed — for example --min-quality was not met.",
			Remedy:  "fix the regression, or change the gate. This is not a tool failure, and retrying will not change it.",
		},
		{
			Code: ExitOperational, Name: "operational-error",
			Meaning: "the tool broke, or a platform-facing command could not reach the platform.",
			Remedy:  "retry, check connectivity, or file a bug. Your workflow is not necessarily worse than it was.",
		},
		{
			Code: ExitInvalidCfg, Name: "invalid-config",
			Meaning: "the invocation is malformed: a missing required input, an unreadable config file, a flag out of range.",
			Remedy:  "fix the invocation. The message names the input that was missing rather than reporting a generic flag error.",
		},
	}
}

// ConfigPrecedence is the resolution order, highest first. Documented because "which wins" is the
// question a reader has when a value is set in two places, and it is not guessable.
func ConfigPrecedence() []string {
	return []string{string(SourceFlag), string(SourceEnv), string(SourceFile), string(SourceDefault)}
}
