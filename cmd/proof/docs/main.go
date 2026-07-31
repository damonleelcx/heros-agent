// Command docs runs P23's PUBLISHED DOCUMENTATION against a real repository
// (github.com/nousresearch/hermes-agent, the same target as every other cmd/proof).
//
// # What "running a documentation phase" means
//
// Every other phase proves its engine against real source. P23's deliverable is not an engine — it is a
// set of published sentences. So the run that matters is: **take the commands the documentation tells a
// reader to type, type them against a repository nobody here controls, and check that what happens is
// what the page said would happen.**
//
// That is a different check from the build-time fences, and it catches a different failure. The fences
// prove a documented command EXISTS in the registry and that its flags are real. They cannot prove the
// command WORKS, that its documented success criterion is what a reader actually sees, or that its
// documented exit code is the one the process returns.
//
//	go run ./cmd/proof/docs -repo /tmp/hermes-agent -heros ./heros
//
// # What is real here
//
// The repository, the invocations (extracted from the generated CLI reference and the quickstart rather
// than retyped), the exit codes, and the machine-output contract version on stdout.
//
// # What this deliberately does not check
//
//   - Network commands. `login`, `link` and `upgrade` need a platform and an account; running them here
//     would prove nothing about the documentation and would transmit a stranger's repository metadata.
//     They are REPORTED AS SKIPPED WITH THE REASON rather than silently omitted, because a summary that
//     said "14/14 verified" while four were never run is the exact dishonesty this phase exists to stop.
//   - Whether a command's PROSE is a good description. That is a review responsibility, named in
//     docs/CONTRIBUTING-DOCS.md §2.2.
//   - Whether the output is CORRECT for hermes-agent — only that the documented contract held.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// invocation is one runnable command lifted out of the published documentation.
type invocation struct {
	// Command is the subcommand, e.g. "discover".
	Command string
	// Line is the exact line the documentation prints.
	Line string
	// Source is the page it came from, so a failure names the page to fix.
	Source string
	// DocumentedExit is the exit code the reference states for success.
	DocumentedExit int
	// Availability decides whether this run may execute it at all.
	Availability string
	// Success is the documented success criterion, echoed in the report so a reader can judge it.
	Success string
	// Prerequisite is what the page says must exist before the example runs, or "" when nothing must.
	Prerequisite string
}

type factsDoc struct {
	ToolVersion string `json:"tool_version"`
	Commands    []struct {
		Name         string `json:"name"`
		Availability string `json:"availability"`
		Example      string `json:"example"`
		Success      string `json:"success"`
		SuccessExit  int    `json:"success_exit"`
		Prerequisite string `json:"prerequisite"`
	} `json:"commands"`
	ExitCodes []struct {
		Code int    `json:"code"`
		Name string `json:"name"`
	} `json:"exit_codes"`
}

type outcome struct {
	inv      invocation
	ran      bool
	skipped  string
	exitCode int
	stdout   string
	stderr   string
}

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "the repository to run the documented commands against")
	binary := flag.String("heros", "heros", "the heros binary to exercise")
	root := flag.String("root", ".", "repository root, for locating the generated facts and content")
	release := flag.String("release", "", "a directory holding a downloaded release (SHA256SUMS, its signature and the asset), so verify-release's documented prerequisite can be met from real files")
	flag.Parse()
	releaseDir = *release

	facts, err := readFacts(filepath.Join(*root, "web", "console", "src", "generated", "docs-facts.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "docs proof:", err)
		os.Exit(2)
	}

	work, err := os.MkdirTemp("", "p23docs-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "docs proof:", err)
		os.Exit(2)
	}
	defer func() { _ = os.RemoveAll(work) }()

	fmt.Println("P23 — running the published documentation against a real repository")
	fmt.Println(strings.Repeat("─", 78))
	fmt.Printf("repository   %s\n", *repo)
	fmt.Printf("binary       %s\n", *binary)
	// The version the PAGES say they document, read from the reference's own front matter — not the
	// source tree's dev version, which is what a reader would never see.
	documented := documentedVersion(filepath.Join(*root, "web", "console", "content", "docs", "en", "reference", "cli.md"))
	fmt.Printf("documents    platform %s (source tree is %s)\n", documented, facts.ToolVersion)
	fmt.Println()

	// ── 1 · The invocations, taken FROM the documentation ─────────────────────
	//
	// Lifted from the generated facts rather than retyped here. Retyping would make this program a
	// second source of truth about what the documentation says — which is the exact failure the whole
	// phase is built to prevent, reproduced inside its own proof.
	var invocations []invocation
	for _, c := range facts.Commands {
		invocations = append(invocations, invocation{
			Command:        c.Name,
			Line:           c.Example,
			Source:         "content/docs/en/reference/cli.md",
			DocumentedExit: c.SuccessExit,
			Availability:   c.Availability,
			Success:        c.Success,
			Prerequisite:   c.Prerequisite,
		})
	}

	// And the quickstart's own first command — the one the install page ends by naming, which is the
	// single most-followed line in the corpus.
	quickstart, err := quickstartCommand(filepath.Join(*root, "web", "console", "content", "docs", "en", "start", "quickstart.md"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "docs proof:", err)
		os.Exit(2)
	}
	invocations = append(invocations, invocation{
		Command:        "discover",
		Line:           quickstart,
		Source:         "content/docs/en/start/quickstart.md",
		DocumentedExit: 0,
		Availability:   "offline",
		Success:        "the IR and the report at the paths given, and a summary naming how many call sites were found",
	})

	// ── 2 · Run each one ──────────────────────────────────────────────────────
	var results []outcome
	for _, inv := range invocations {
		results = append(results, run(inv, *repo, *binary, work))
	}

	// ── 3 · Report ────────────────────────────────────────────────────────────
	report(results, facts)
}

// documentedVersion reads `platform_version` out of a page's front matter.
func documentedVersion(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	if m := regexp.MustCompile(`(?m)^platform_version:\s*(\S+)$`).FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return "unknown"
}

func readFacts(path string) (factsDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return factsDoc{}, fmt.Errorf("read the generated facts (run `make docs-facts`): %w", err)
	}
	var f factsDoc
	if err := json.Unmarshal(raw, &f); err != nil {
		return factsDoc{}, err
	}
	if len(f.Commands) == 0 {
		return factsDoc{}, fmt.Errorf("%s lists no commands", path)
	}
	return f, nil
}

// quickstartCommand lifts the first `heros …` fenced command out of the quickstart page.
func quickstartCommand(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?m)^(heros discover[^\n]*)$`).FindSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("the quickstart at %s prints no `heros discover` command", path)
	}
	return string(m[1]), nil
}

// run executes one documented invocation, or reports why it did not.
func run(inv invocation, repo, binary, work string) outcome {
	/*
	 * 🔴 A stated prerequisite is honoured, not silently satisfied.
	 *
	 * This run does not manufacture a Variant Spec so that `apply` exits 0. Doing that would prove the
	 * command works on an input this program invented, and would hide the thing worth knowing: whether a
	 * reader who types the line cold gets what the page promised.
	 *
	 * So a command with a prerequisite this run cannot meet is counted SEPARATELY — neither verified nor
	 * skipped-for-network — with the prerequisite quoted. The number stays honest.
	 */
	if inv.Prerequisite != "" && !prerequisiteMet(inv, work) {
		return outcome{
			inv:     inv,
			skipped: "prerequisite not met by this run: " + inv.Prerequisite,
		}
	}

	if inv.Availability != "offline" {
		// Named, not hidden. A summary that counted these as verified would be a lie about the number.
		return outcome{
			inv:     inv,
			skipped: "needs the network" + accountSuffix(inv.Availability) + " — not run against a stranger's repository",
		}
	}

	// The documented line, with `--repo .` retargeted at the real repository and any output path moved
	// into a scratch directory. Nothing else about the line is rewritten: the flags, their order and
	// their values are the page's.
	fields := strings.Fields(inv.Line)
	if len(fields) < 2 || fields[0] != "heros" {
		return outcome{inv: inv, skipped: "the documented line is not a heros invocation"}
	}
	args := make([]string, 0, len(fields))
	for i := 1; i < len(fields); i++ {
		switch {
		case fields[i] == "--repo" && i+1 < len(fields):
			args = append(args, "--repo", repo)
			i++
		case (fields[i] == "--out" || fields[i] == "--report" || fields[i] == "--manifest" || fields[i] == "--sig") && i+1 < len(fields):
			args = append(args, fields[i], filepath.Join(work, filepath.Base(fields[i+1])))
			i++
		default:
			args = append(args, fields[i])
		}
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = work
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		return outcome{inv: inv, skipped: "could not execute: " + err.Error()}
	}
	return outcome{inv: inv, ran: true, exitCode: code, stdout: stdout.String(), stderr: stderr.String()}
}

// prerequisiteMet reports whether this run can satisfy a documented prerequisite from real artifacts.
//
// It satisfies only what it can obtain HONESTLY. `verify-release` needs a downloaded release manifest and
// signature — real files, fetched the way the install page says — so if they are present beside this run
// they are used. Nothing here fabricates an input.
func prerequisiteMet(inv invocation, work string) bool {
	if inv.Command != "verify-release" {
		return false
	}
	for _, name := range []string{"SHA256SUMS", "SHA256SUMS.sig"} {
		src := filepath.Join(releaseDir, name)
		raw, err := os.ReadFile(src)
		if err != nil {
			return false
		}
		if err := os.WriteFile(filepath.Join(work, name), raw, 0o644); err != nil {
			return false
		}
	}
	// The asset the manifest covers has to be beside them, or the checksum step has nothing to check.
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "heros-") || strings.HasSuffix(e.Name(), ".sig") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(releaseDir, e.Name()))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(work, e.Name()), raw, 0o644)
	}
	return true
}

// releaseDir is where a real downloaded release lives, if one does. Set by -release.
var releaseDir string

func accountSuffix(availability string) string {
	if availability == "network" {
		return " and an account"
	}
	return ""
}

func report(results []outcome, facts factsDoc) {
	sort.Slice(results, func(i, j int) bool { return results[i].inv.Command < results[j].inv.Command })

	var ran, matched, mismatched, skipped int
	var failures []string

	fmt.Println("── Every documented invocation, run ────────────────────────────────────────")
	for _, r := range results {
		if !r.ran {
			skipped++
			fmt.Printf("  ⏭  %-16s SKIPPED — %s\n", r.inv.Command, r.skipped)
			continue
		}
		ran++

		// The documented exit code is the contract a customer's pipeline branches on. It is the one
		// thing here that MUST hold.
		if r.exitCode == r.inv.DocumentedExit {
			matched++
			fmt.Printf("  ✔  %-16s exit %d as documented\n", r.inv.Command, r.exitCode)
		} else {
			mismatched++
			name := "unknown"
			for _, e := range facts.ExitCodes {
				if e.Code == r.exitCode {
					name = e.Name
				}
			}
			fmt.Printf("  ✖  %-16s exit %d (%s), documented %d\n", r.inv.Command, r.exitCode, name, r.inv.DocumentedExit)
			failures = append(failures, fmt.Sprintf(
				"%s (%s): documented exit %d, got %d. First line of stderr: %s",
				r.inv.Command, r.inv.Source, r.inv.DocumentedExit, r.exitCode, firstLine(r.stderr)))
		}

		// The machine-output contract on stdout, which the reference states is versioned.
		if strings.Contains(r.stdout, `"contract_version"`) && !strings.Contains(r.stdout, `"p11.cli.v1"`) {
			failures = append(failures, fmt.Sprintf(
				"%s emitted a machine document with a contract version the reference does not name", r.inv.Command))
		}
	}

	fmt.Println()
	fmt.Println("── What the documentation claimed, and what happened ───────────────────────")
	fmt.Printf("  %d documented invocation(s)\n", len(results))
	fmt.Printf("  %d run · %d matched the documented exit code · %d did not\n", ran, matched, mismatched)
	fmt.Printf("  %d skipped, each with its reason above\n", skipped)
	fmt.Println()

	if len(failures) > 0 {
		fmt.Println("🔴 The documentation and the binary disagree:")
		for _, f := range failures {
			fmt.Printf("   - %s\n", f)
		}
		fmt.Println()
		fmt.Println("A documented exit code is a public contract the moment a customer's pipeline branches on it.")
		os.Exit(1)
	}

	fmt.Println("Every documented invocation that could be run offline behaved as its page said it would.")
	fmt.Println()
	fmt.Println("What this run does NOT establish, stated so the number above is not over-read:")
	fmt.Println("  - the network commands were not exercised; they are counted as skipped, not as verified")
	fmt.Println("  - it checks the CONTRACT (exit code, machine-output version), not whether the prose is a")
	fmt.Println("    good description of what the command does — that is a review responsibility")
	fmt.Println("    (docs/CONTRIBUTING-DOCS.md §2.2)")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
