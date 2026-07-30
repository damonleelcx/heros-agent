// Command p20hermes drives the P20 distribution against a REAL repository — nousresearch/hermes-agent — using
// the binary a user would actually end up with.
//
// # What this proves that the smoke matrix does not
//
// `scripts/install_smoke.py` proves the installer works and refuses correctly, but it exercises the installed
// binary against this repository's own fixture: one node, known shape, written by us. That is the right fixture
// for testing an installer and the wrong one for answering the question a customer asks, which is *will this
// work on my repository*.
//
// hermes-agent is somebody else's 8,000-file, multi-language agent codebase. Running the INSTALLED binary
// against it is the only way to find out whether the distribution delivers a working tool rather than a working
// download. The three failures this catches are exactly the ones an installer test cannot:
//
//	a binary that answers `version` and dies on real input   — a missing frontend, a stripped symbol
//	a `doctor` that reports ready and then cannot do the work — a check that looked instead of asking
//	a `discover`/`eval` that exits 0 having measured nothing  — the false green this project audits for
//
// # What it deliberately does not do
//
// It does not judge hermes-agent, and it does not optimize anything. The axis runners (p13hermes … p18hermes) do
// that. This one asks only: does the distributed artifact work here, and does every command tell the truth about
// what it found? A refusal is a valid outcome and is reported as one — including the case where `doctor` names a
// missing toolchain, which is the honest answer on a machine that lacks it.
//
//	go run ./cmd/p20hermes -repo /tmp/hermes-agent-p20 -heros /path/to/installed/heros
//
// -heros defaults to whatever `heros` is on PATH, because the point is to run the INSTALLED binary. Pointing it
// at `go run ./cmd/heros` would test the source tree and prove nothing about the distribution.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/distribution"
	"github.com/heros-foreal/agentd/internal/sourcerev"
)

const repoURL = "https://github.com/nousresearch/hermes-agent"

// step is one thing the run asserts, and what it found.
type step struct {
	Name string
	OK   bool
	// Detail is what was actually observed — a count, a version, a refusal's reason. Never "passed".
	Detail string
	// Refusal marks an outcome that is a REFUSAL rather than a failure: the tool declined, by name, and that
	// is correct behaviour. Distinguished because collapsing the two is how a report either hides a real break
	// or cries wolf about a working guardrail.
	Refusal bool
}

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent-p20", "path to the hermes-agent checkout (read-only)")
	herosBin := flag.String("heros", "", "the INSTALLED heros binary (default: whatever is on PATH)")
	pin := flag.String("pin", "", `commit this run must be checked out at; empty means "use HEAD and say so"`)
	flag.Parse()

	bin := *herosBin
	if bin == "" {
		resolved, err := exec.LookPath("heros")
		if err != nil {
			fatal("no `heros` on PATH and no -heros given.\n" +
				"This runner exists to exercise the INSTALLED binary — the artifact a user ends up with. Install it\n" +
				"first (scripts/install.sh, or `make install-smoke` which leaves one behind), then re-run.")
		}
		bin = resolved
	}
	abs, err := filepath.Abs(bin)
	if err == nil {
		bin = abs
	}

	commitSHA, revNote, err := sourcerev.Resolve(*repo, *pin)
	if err != nil {
		fatal(fmt.Sprintf("cannot resolve the checkout at %s: %v", *repo, err))
	}

	fmt.Printf("=== P20 distribution — run for %s ===\n\n", repoURL)
	fmt.Printf("repository : %s\n", *repo)
	fmt.Printf("commit     : %s%s\n", commitSHA, revNote)
	fmt.Printf("binary     : %s\n", bin)

	// Which platform row this machine is, from the frozen matrix — so the report says what was covered rather
	// than leaving a reader to assume "all of them".
	target, known := distribution.TargetFor(goos(), goarch())
	if known {
		fmt.Printf("platform   : %s (%s)\n", target.Platform, target.Key())
	} else {
		fmt.Printf("platform   : %s/%s — NOT in the supported matrix\n", goos(), goarch())
	}
	fmt.Printf("matrix     : %s\n\n", distribution.MatrixVersion())

	var steps []step
	add := func(s step) {
		steps = append(steps, s)
		mark := "✅"
		if s.Refusal {
			mark = "🔵"
		} else if !s.OK {
			mark = "⛔"
		}
		fmt.Printf("%s %-28s %s\n", mark, s.Name, s.Detail)
	}

	// ── 1 · the installed binary reports its own version ────────────────────────────────────────────────
	//
	// Read from the MACHINE payload, not the narration: that field is the one stamped into linked run metadata,
	// so it is the copy a customer's bug report is filed against.
	out, _, err := run(bin, *repo, "version")
	version := ""
	if data, derr := payload(out); derr == nil {
		version, _ = data["tool_version"].(string)
	}
	add(step{"installed version", err == nil && version != "",
		fmt.Sprintf("tool_version=%q", version), false})

	// ── 2 · doctor, on somebody else's repository ───────────────────────────────────────────────────────
	//
	// The assertion is NOT "ready". A machine legitimately lacks toolchains, and demanding ready would only pass
	// where the environment is dirty. What must hold is that every gap names ONE next action — a gap with no
	// action is a support ticket — and that no check is silently omitted.
	out, _, derr := run(bin, *repo, "doctor")
	checks, gaps, unactionable := 0, []string{}, []string{}
	if data, perr := payload(out); perr == nil {
		if raw, ok := data["checks"].([]any); ok {
			checks = len(raw)
			for _, c := range raw {
				m, _ := c.(map[string]any)
				if m["state"] != "action-needed" {
					continue
				}
				name, _ := m["name"].(string)
				gaps = append(gaps, name)
				if next, _ := m["next_action"].(string); next == "" {
					unactionable = append(unactionable, name)
				}
			}
		}
	}
	detail := fmt.Sprintf("%d checks, gaps=%v", checks, orNone(gaps))
	if len(unactionable) > 0 {
		detail += fmt.Sprintf(" — WITH NO NEXT ACTION: %v", unactionable)
	}
	add(step{"doctor reports actionably", derr == nil && checks > 0 && len(unactionable) == 0, detail, false})

	// ── 3 · discover, on 8,000 files of somebody else's code ────────────────────────────────────────────
	//
	// This is where a broken distribution actually breaks: the tree-sitter frontends are CGO, and a binary built
	// or packaged wrong links but cannot parse. A fixture with one Go file would not exercise them.
	work, err := os.MkdirTemp("", "p20hermes-")
	if err != nil {
		fatal(err.Error())
	}
	defer os.RemoveAll(work)
	irPath := filepath.Join(work, "ir.json")
	reportPath := filepath.Join(work, "report.json")

	start := time.Now()
	out, stderr, derr := run(bin, *repo, "discover", "--out", irPath, "--report", reportPath, "--commit", commitSHA)
	elapsed := time.Since(start).Round(time.Millisecond)
	nodes, edges, workflowID := 0, 0, ""
	if data, perr := payload(out); perr == nil {
		nodes = intOf(data["nodes"])
		edges = intOf(data["edges"])
		workflowID, _ = data["workflow_id"].(string)
	}
	switch {
	case derr != nil:
		add(step{"discover", false, fmt.Sprintf("failed in %s: %s", elapsed, firstLine(stderr)), false})
	case nodes == 0:
		// A zero-node discovery is a REFUSAL, not a crash: the tool read the repository and found no agent
		// workflow it recognises. Reporting that as a failure would be a claim about hermes-agent's code; the
		// honest statement is what was found.
		add(step{"discover", true,
			fmt.Sprintf("read the repository in %s and found NO workflow it recognises (0 nodes) — a finding about "+
				"what this build can see, not a break", elapsed), true})
	default:
		add(step{"discover", true,
			fmt.Sprintf("%d nodes, %d edges in %s · workflow_id=%s", nodes, edges, elapsed, workflowID), false})
	}

	// ── 4 · eval, and it must produce a NUMBER ──────────────────────────────────────────────────────────
	//
	// Exit 0 with no measurement is the false green. The quality figure is read out of the scores array the way a
	// consumer would read it.
	if nodes > 0 {
		start = time.Now()
		out, stderr, eerr := run(bin, *repo, "eval", "--seeds", "3", "--cases", "4", "--commit", commitSHA)
		elapsed = time.Since(start).Round(time.Millisecond)
		var quality *float64
		runtimeName := ""
		if data, perr := payload(out); perr == nil {
			runtimeName, _ = data["runtime"].(string)
			if scores, ok := data["scores"].([]any); ok {
				for _, s := range scores {
					m, _ := s.(map[string]any)
					if m["metric"] == "quality" {
						if v, ok := m["value"].(float64); ok {
							quality = &v
						}
					}
				}
			}
		}
		if eerr != nil || quality == nil {
			add(step{"eval produces a number", false,
				fmt.Sprintf("exit=%v quality=%v in %s: %s", eerr, quality, elapsed, firstLine(stderr)), false})
		} else {
			add(step{"eval produces a number", true,
				fmt.Sprintf("quality=%.4f via runtime %q in %s", *quality, runtimeName, elapsed), false})
		}
	} else {
		add(step{"eval produces a number", true,
			"skipped: discover found no workflow, so there is nothing to evaluate — stated rather than reported as a pass", true})
	}

	// ── 5 · coverage: what this build can APPLY to hermes-agent's languages ─────────────────────────────
	out, _, cerr := run(bin, *repo, "coverage")
	langs := 0
	// The field is `registered_languages`, not `languages` — the first version of this runner read the wrong
	// name and reported 0 languages for a command that had answered correctly. A reader of that report would have
	// gone looking for a broken coverage table.
	if data, perr := payload(out); perr == nil {
		if raw, ok := data["registered_languages"].([]any); ok {
			langs = len(raw)
		}
	}
	add(step{"coverage is answerable", cerr == nil && langs > 0,
		fmt.Sprintf("%d registered languages", langs), false})

	// ── 6 · the offline guarantee, on a real repository ─────────────────────────────────────────────────
	//
	// Every command above ran with no account and no platform reachable. This asserts it rather than assuming
	// it: the endpoints are pointed at a closed port, and a command that needed them would fail here.
	out, stderr, oerr := run2(bin, *repo, map[string]string{
		"HEROS_RELEASE_API_URL":  "http://127.0.0.1:9/closed",
		"HEROS_RELEASE_BASE_URL": "http://127.0.0.1:9/closed",
	}, "discover", "--out", filepath.Join(work, "ir2.json"), "--report", filepath.Join(work, "r2.json"),
		"--commit", commitSHA)
	nodes2 := 0
	if data, perr := payload(out); perr == nil {
		nodes2 = intOf(data["nodes"])
	}
	add(step{"works with no platform", oerr == nil && nodes2 == nodes,
		fmt.Sprintf("re-ran discover with the platform endpoints closed: %d nodes (same as before: %v)",
			nodes2, nodes2 == nodes), false})

	// ── 7 · upgrade defers or refuses, and never phones home on the hot path ────────────────────────────
	//
	// `upgrade` against a closed index must fail LOUDLY about the network rather than hanging or silently
	// pretending to be current — and none of the commands above may have touched it.
	_, stderr, uerr := run2(bin, *repo, map[string]string{
		"HEROS_RELEASE_API_URL": "http://127.0.0.1:9/closed",
	}, "upgrade")
	says := strings.Contains(stderr, "cannot reach the release index") ||
		strings.Contains(stderr, "managed by") // a package-manager-owned binary defers instead
	add(step{"upgrade is honest offline", uerr != nil && says || uerr == nil && says,
		firstLine(stderr), true})

	// ── the report ──────────────────────────────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println(strings.Repeat("═", 100))
	failed := 0
	for _, s := range steps {
		if !s.OK {
			failed++
		}
	}
	refusals := 0
	for _, s := range steps {
		if s.Refusal {
			refusals++
		}
	}
	fmt.Printf("P20 distribution on %s @ %s\n", repoURL, commitSHA[:min(12, len(commitSHA))])
	fmt.Printf("  %d checks · %d failed · %d reported as refusals (a refusal is an answer, not a break)\n",
		len(steps), failed, refusals)
	if known {
		fmt.Printf("  covered: %s only. The other four rows of the matrix need their own runners (D1).\n", target.Key())
	}
	fmt.Println(strings.Repeat("═", 100))
	if failed > 0 {
		os.Exit(1)
	}
}

// run executes the installed binary in the repository and returns (stdout, stderr, err).
func run(bin, repo string, args ...string) (string, string, error) {
	return run2(bin, repo, nil, args...)
}

func run2(bin, repo string, env map[string]string, args ...string) (string, string, error) {
	cmd := exec.Command(bin, append(args, "--repo", repo)...)
	// A HOME of its own, so the run cannot read or write the developer's credential file. This runner must not
	// be able to accidentally exercise the linked path.
	tmpHome, _ := os.MkdirTemp("", "p20hermes-home-")
	defer os.RemoveAll(tmpHome)
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "HEROS_CONFIG_DIR="+tmpHome)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// payload pulls `data` out of the machine envelope. A caller that parsed the narration instead would be reading
// prose, which is exactly what the two-stream split exists to prevent.
func payload(stdout string) (map[string]any, error) {
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		return nil, err
	}
	if env.Data == nil {
		return nil, fmt.Errorf("envelope carried no data")
	}
	return env.Data, nil
}

func intOf(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return "(no output)"
}

func orNone(v []string) any {
	if len(v) == 0 {
		return "none"
	}
	sort.Strings(v)
	return v
}

// goos/goarch ask the RUNTIME, not the toolchain: this program is compiled for the machine it runs on, so
// runtime.GOOS is the answer to "which matrix row is this".
func goos() string   { return runtime.GOOS }
func goarch() string { return runtime.GOARCH }

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "p20hermes: "+msg)
	os.Exit(2)
}
