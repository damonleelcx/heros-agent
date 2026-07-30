package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// onboard_test.go covers the first ten minutes (P20 tasks 5.1–5.3, 5.6, 5.7, 6.1, 6.2).

// runEnv is `run` with a controllable environment, for the checks that must observe what a user has set.
func runEnv(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	lookup := func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
	code := Main(args, Streams{Out: &out, Err: &errb}, lookup, nil)
	return code, out.String(), errb.String()
}

// TestBareInvocationGreetsAndExitsZero — task 5.1. A tool that exits non-zero when run with no arguments
// teaches CI authors to append `|| true`, and after that a real failure is invisible too.
func TestBareInvocationGreetsAndExitsZero(t *testing.T) {
	code, stdout, stderr := run(t)
	if code != ExitOK {
		t.Fatalf("bare `heros` exited %d, want 0 — running a tool with no arguments is curiosity, not a malformed invocation", code)
	}
	env := decodeEnvelope(t, stdout)
	if env.Command != "greeting" || !env.OK {
		t.Fatalf("bare invocation emitted %+v", env)
	}
	var data GreetingData
	b, _ := json.Marshal(env.Data)
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	if data.NextCommand != "heros discover" {
		t.Errorf("greeting names %q as the next command", data.NextCommand)
	}
	if data.ConfigRequired {
		t.Error("the greeting claims a config is required — reaching a first discover must need no config file")
	}
	// It must name ONE command prominently. A greeting that lists eleven is a flag reference.
	if !strings.Contains(stderr, "cd your-repo && heros discover") {
		t.Errorf("the greeting does not show the one command to run:\n%s", stderr)
	}
	if strings.Count(stderr, "heros ") > 8 {
		t.Errorf("the greeting mentions too many commands to read as a starting point:\n%s", stderr)
	}
}

// TestHelpStillListsEveryCommand is the no-lose-function rule (task 6.2). Onboarding additions must not push
// existing subcommands out of `--help`: a command that disappears from help is a command users stop using, and
// nobody notices because nothing errors.
func TestHelpStillListsEveryCommand(t *testing.T) {
	_, _, stderr := run(t, "--help")
	for _, cmd := range []string{
		// everything that existed before P20…
		"discover", "apply", "author", "eval", "coverage", "status", "version", "login", "link",
		// …and everything P20 added.
		"init", "doctor", "verify-release", "upgrade",
	} {
		if !strings.Contains(stderr, cmd) {
			t.Errorf("`heros --help` does not list %q", cmd)
		}
	}
	// The exit-code contract must stay visible: it is the thing a CI author reads help for.
	if !strings.Contains(stderr, "Exit codes:") {
		t.Error("`heros --help` no longer documents the exit codes")
	}
}

// TestEveryDispatchedCommandIsInHelp closes the other half of no-lose-function: a command that works but is
// undocumented is as invisible as one that was removed.
func TestEveryDispatchedCommandIsInHelp(t *testing.T) {
	b, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	_, _, help := run(t, "--help")
	src := string(b)
	// Every `case "x":` in the dispatch switch, minus the help aliases which are not commands.
	skip := map[string]bool{"-h": true, "--help": true, "help": true}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `case "`) {
			continue
		}
		for _, part := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "case "), ":"), ",") {
			name := strings.Trim(strings.TrimSpace(part), `"`)
			if name == "" || skip[name] || strings.Contains(name, " ") {
				continue
			}
			if !strings.Contains(help, name) {
				t.Errorf("the dispatcher handles %q but `--help` does not mention it", name)
			}
		}
	}
}

// TestInitIsIdempotentAndNeverClobbers — task 5.2. "I ran init again out of habit" is a thing people do, and a
// tuned config is work that is not reproducible.
func TestInitIsIdempotentAndNeverClobbers(t *testing.T) {
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "llm-eval.yaml")

	code, stdout, _ := run(t, "init", "--repo", repo)
	if code != ExitOK {
		t.Fatalf("init exited %d", code)
	}
	first, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("init did not write %s: %v", cfgPath, err)
	}
	if env := decodeEnvelope(t, stdout); !env.OK {
		t.Errorf("init reported failure: %+v", env)
	}

	// The written config must be usable AS WRITTEN. A starter file the tool then rejects converts "run one
	// command" into "read the reference".
	if _, _, stderr := run(t, "status", "--repo", repo, "--config", cfgPath); strings.Contains(stderr, "invalid") {
		t.Errorf("the config init wrote is not accepted by the tool:\n%s", stderr)
	}

	// A second run must change nothing and still succeed.
	edited := append(first, []byte("\n# a human edited this\n")...)
	if err := os.WriteFile(cfgPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := run(t, "init", "--repo", repo)
	if code != ExitOK {
		t.Fatalf("second init exited %d: %s", code, stderr)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(edited) {
		t.Error("init overwrote an existing config — a human's edits were destroyed by a command they ran out of habit")
	}
	if !strings.Contains(stderr, "leaving it alone") {
		t.Errorf("init did not say it left the file alone:\n%s", stderr)
	}
	var data InitData
	b, _ := json.Marshal(decodeEnvelope(t, stdout).Data)
	_ = json.Unmarshal(b, &data)
	if !data.Unchanged || data.Created {
		t.Errorf("the second init reported created=%v unchanged=%v — an idempotent command must still say which happened",
			data.Created, data.Unchanged)
	}

	// --force is the explicit path, and it must work, or the safe default becomes a dead end.
	if code, _, _ := run(t, "init", "--repo", repo, "--force"); code != ExitOK {
		t.Fatal("init --force failed")
	}
	forced, _ := os.ReadFile(cfgPath)
	if string(forced) == string(edited) {
		t.Error("init --force did not overwrite")
	}
}

func doctorChecks(t *testing.T, env map[string]string, args ...string) []Check {
	t.Helper()
	code, stdout, stderr := runEnv(t, env, append([]string{"doctor"}, args...)...)
	if code != ExitOK {
		t.Fatalf("doctor exited %d — doctor reports, it is not a gate; a red exit reads as a broken install\n%s", code, stderr)
	}
	var data DoctorData
	b, _ := json.Marshal(decodeEnvelope(t, stdout).Data)
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	return data.Checks
}

// TestDoctorNamesOneNextActionPerGap — task 5.3's central requirement. A gap with no action is a support ticket.
func TestDoctorNamesOneNextActionPerGap(t *testing.T) {
	// A repository path that does not exist produces a real gap without needing to break the machine.
	checks := doctorChecks(t, nil, "--repo", filepath.Join(t.TempDir(), "does-not-exist"))
	gaps := 0
	for _, c := range checks {
		if c.State != CheckActionNeeded {
			continue
		}
		gaps++
		if c.NextAction == "" {
			t.Errorf("check %q reports a gap with no next action", c.Name)
		}
		if c.Detail == "" {
			t.Errorf("check %q reports a gap without saying what was found", c.Name)
		}
	}
	if gaps == 0 {
		t.Fatal("doctor found no gap for a nonexistent repository — it cannot be shown to report one")
	}
	// Every check must be present in every run: an omitted check reads as a passing one.
	names := map[string]bool{}
	for _, c := range checks {
		if names[c.Name] {
			t.Errorf("duplicate check %q", c.Name)
		}
		names[c.Name] = true
		if c.State != CheckReady && c.State != CheckActionNeeded && c.State != CheckNotApplicable {
			t.Errorf("check %q has an unknown state %q", c.Name, c.State)
		}
	}
	for _, want := range []string{"platform", "repository", "provider keys", "account"} {
		if !names[want] {
			t.Errorf("doctor omitted the %q check — an absent check reads as a passing one", want)
		}
	}
}

// TestDoctorReportsTheRealProviderPath is the AI-Engineer assertion (task 5.3): "a value is set" is not a
// working path, and a doctor that reported ✅ for a key this build never reads would give a user confidence
// that their eval scores reflect their model — the one thing they must not believe here.
func TestDoctorReportsTheRealProviderPath(t *testing.T) {
	repo := t.TempDir()

	// With no key set: the honest statement that none is used.
	for _, c := range doctorChecks(t, nil, "--repo", repo) {
		if c.Name != "provider keys" {
			continue
		}
		if c.State == CheckReady {
			t.Error("doctor reports provider keys as READY when this build reads none — that is the false " +
				"confidence the check exists to prevent")
		}
		if !strings.Contains(c.Detail, "reads no provider key") {
			t.Errorf("the provider check does not state the real path: %q", c.Detail)
		}
		if !strings.Contains(c.Detail, "not a measurement of a live model") {
			t.Errorf("the provider check does not warn what the scores are NOT: %q", c.Detail)
		}
	}

	// With a key set: it must say the key is NOT used. A user who set one deserves to be told, rather than
	// left to assume their eval hit their model.
	found := false
	for _, c := range doctorChecks(t, map[string]string{"OPENAI_API_KEY": "sk-not-a-real-key"}, "--repo", repo) {
		if c.Name != "provider keys" {
			continue
		}
		found = true
		if !strings.Contains(c.Detail, "OPENAI_API_KEY") || !strings.Contains(c.Detail, "NOT used") {
			t.Errorf("a set provider key is not reported as unused: %q", c.Detail)
		}
		if c.State == CheckActionNeeded {
			t.Error("a set-but-unused key is reported as a GAP — doctor must not demand a prerequisite the " +
				"command does not need, nor complain about one the user supplied")
		}
	}
	if !found {
		t.Fatal("no provider-keys check was emitted")
	}
}

// TestDoctorDoesNotDemandAnAccount — task 5.7. The free-tier promise is that the local commands need no
// account, so reporting "not logged in" as a gap would contradict the product.
func TestDoctorDoesNotDemandAnAccount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HEROS_CONFIG_DIR", dir) // no credential file exists here
	for _, c := range doctorChecks(t, nil, "--repo", t.TempDir()) {
		if c.Name == "account" && c.State == CheckActionNeeded {
			t.Errorf("doctor treats the absence of an account as a gap: %q → %q", c.Detail, c.NextAction)
		}
	}
}

// TestOnboardingWorksWithNetworkingDenied — tasks 5.6/5.7. Nothing in the onboarding path may need the network,
// and this asserts it at RUNTIME rather than by reading imports: a library that resolved DNS on init would pass
// an import review and fail here.
func TestOnboardingWorksWithNetworkingDenied(t *testing.T) {
	withNetworkDenied(t)
	repo := t.TempDir()
	for _, args := range [][]string{
		{},
		{"init", "--repo", repo},
		{"doctor", "--repo", repo},
		{"version"},
		{"--help"},
	} {
		code, _, stderr := run(t, args...)
		if code != ExitOK {
			t.Errorf("`heros %s` exited %d with networking denied: %s", strings.Join(args, " "), code, stderr)
		}
	}
}

// TestCLIPackageNetworkLinkageIsNotWidened is the structural half of task 5.6 — with a documented exception
// this test exists to make visible rather than to launder.
//
// # What was found, and why the test is a baseline rather than a ban
//
// `internal/cli`'s own doc comment used to claim that this package "never imports net/http", making the offline
// guarantee structural. That claim STOPPED BEING TRUE before P20: `author.go` (P13) imports internal/authoring,
// which reaches internal/providergateway, which links net/http for the provider adapters and the AWS SDK. So
// the import graph of the offline surface can, today, reach a network stack.
//
// Nothing dials — `TestLocalWorkflowRunsWithNetworkingDenied` proves the BEHAVIOUR under a deny-all dialer, and
// that is the guarantee users actually have. But the structural version of it is gone, and P20 will not pretend
// otherwise: the comment in app.go has been corrected to say what is true.
//
// So this test does the one useful thing available: it pins the KNOWN chain and fails if the offline surface
// grows a NEW one. P20's own additions (distribution, release, worktree, transform) each link no network stack,
// and `heros upgrade` is injected through NetCommands precisely so it stays that way.
//
// Restoring the structural guarantee means breaking authoring's dependency on providergateway. That is a change
// inside P13's design, not a distribution change, so it is recorded rather than attempted here.
func TestCLIPackageNetworkLinkageIsNotWidened(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	// knownNetworkImporters are the direct imports of this package that already reach a network stack. The
	// list is the baseline; anything else is a regression this test must catch.
	knownNetworkImporters := map[string]string{
		"github.com/heros-foreal/agentd/internal/authoring":     "P13 `author` → authoring → providergateway",
		"github.com/heros-foreal/agentd/internal/authoringwire": "P13 `author` → authoringwire → authoring → providergateway",
	}

	direct := exec.Command("go", "list", "-f", "{{range .Imports}}{{.}}\n{{end}}", ".")
	direct.Env = append(os.Environ(), "GOWORK=off")
	out, err := direct.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, imp := range strings.Fields(string(out)) {
		if !strings.HasPrefix(imp, "github.com/heros-foreal/") {
			// A direct standard-library network import would be an outright violation with no history.
			switch imp {
			case "net/http", "net/smtp", "net/rpc":
				t.Errorf("internal/cli imports %s DIRECTLY — a command that needs the network belongs in "+
					"internal/clilink, injected as a NetCommand", imp)
			}
			continue
		}
		deps := exec.Command("go", "list", "-deps", imp)
		deps.Env = append(os.Environ(), "GOWORK=off")
		dout, derr := deps.CombinedOutput()
		if derr != nil {
			continue
		}
		if !strings.Contains(string(dout), "\nnet/http\n") && !strings.HasPrefix(string(dout), "net/http\n") {
			continue
		}
		if why, known := knownNetworkImporters[imp]; known {
			t.Logf("known network linkage: %s (%s) — behaviour is covered by the network-denied runtime test", imp, why)
			continue
		}
		t.Errorf("internal/cli gained a NEW import that links net/http: %s.\n"+
			"The offline command surface must not widen its reach to a network stack. If the command needs the "+
			"network, put it in internal/clilink and inject it as a NetCommand — that is what `upgrade` does.", imp)
	}
}

// TestUpgradeAdviceDefersToTheOwningManager — the decision D7 requires, made from the path alone and therefore
// before any network use: a brew-installed user gets `brew upgrade` without a byte downloaded.
func TestUpgradeAdviceDefersToTheOwningManager(t *testing.T) {
	if _, cmd, owned := UpgradeAdvice("/opt/homebrew/bin/heros"); !owned || !strings.Contains(cmd, "brew upgrade") {
		t.Errorf("a Homebrew-installed binary is not deferred to brew (owned=%v cmd=%q)", owned, cmd)
	}
	if _, _, owned := UpgradeAdvice("/usr/local/bin/heros"); owned {
		t.Error("a binary the install script placed is treated as manager-owned — upgrade would refuse to " +
			"replace a file it is responsible for")
	}
	if _, _, owned := UpgradeAdvice(""); owned {
		t.Error("an unknown path was treated as manager-owned; the conservative default must be 'not owned'")
	}
}

// TestVerifyReleaseRefusesWhatItCannotVerify — the shared routine, reached through the command. Each case is a
// way a release can be wrong, and each must be a refusal with a non-zero exit.
func TestVerifyReleaseRefusesWhatItCannotVerify(t *testing.T) {
	dir := t.TempDir()
	asset := "heros-9.9.9-linux-amd64"
	if err := os.WriteFile(filepath.Join(dir, asset), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "SHA256SUMS")

	// A manifest whose checksum does not match the asset.
	if err := os.WriteFile(manifest, []byte(strings.Repeat("a", 64)+"  "+asset+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest+".sig", []byte(strings.Repeat("b", 128)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "verify-release", "--manifest", manifest)
	if code == ExitOK {
		t.Error("verify-release accepted a manifest whose checksum does not match the asset")
	}
	if !strings.Contains(stderr, "MISMATCH") {
		t.Errorf("the refusal does not name the mismatch:\n%s", stderr)
	}

	// A missing signature must refuse, not fall back to checksums-only.
	if err := os.Remove(manifest + ".sig"); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, "verify-release", "--manifest", manifest); code == ExitOK {
		t.Errorf("verify-release accepted an unsigned release:\n%s", stderr)
	}

	// A missing manifest is an invocation problem, and must name the input.
	if code, _, stderr := run(t, "verify-release"); code != ExitInvalidCfg {
		t.Errorf("verify-release with no --manifest exited %d, want %d (%s)", code, ExitInvalidCfg, stderr)
	}
}
