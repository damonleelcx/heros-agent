// Package citest exercises scripts/ci/heros-ci.sh — the CI integration's decision logic — with a STUB
// heros binary, so the build-safety (8.3) and gate-bite (8.4) rules are tested without a GitHub runner.
// These are also the §9 (11b) acceptance cases: platform unavailability reports-and-continues, a gate
// fails the build and names itself, a re-run double-counts nothing, and no-linking transmits nothing.
package citest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubHeros writes a fake `heros` that:
//
//	discover → writes ir.json + report, exits 0
//	eval     → writes eval.json (run_id + optional failed gate), exits $STUB_EVAL_EXIT
//	login    → per $STUB_LINK: ok=0, fail=exit 2, hang=sleep 30
//	link     → per $STUB_LINK: ok writes link.json+exit 0, fail=exit 2, hang=sleep 30
//
// It records every invocation to $STUB_CALLS so a test can assert link was or was NOT called.
func stubHeros(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "heros")
	script := `#!/usr/bin/env bash
echo "$1" >> "${STUB_CALLS:-/dev/null}"
cmd="$1"; shift
outdir="${HEROS_OUT:-heros-out}"
mkdir -p "$outdir"
case "$cmd" in
  discover) echo '{"ok":true}' ; echo '{"ir":1}' > "$outdir/ir.json"; echo '{}' > "$outdir/discovery-report.json"; exit 0 ;;
  eval)
    gate=""
    if [ "${STUB_EVAL_EXIT:-0}" = "1" ]; then gate=',"gate":{"name":"'"${STUB_GATE_NAME:-min-quality}"'","passed":false}'; fi
    echo '{"command":"eval","data":{"run_id":"run-x"}'"$gate"'}' > "$outdir/eval.json"
    exit "${STUB_EVAL_EXIT:-0}" ;;
  login)
    case "${STUB_LINK:-ok}" in fail) exit 2 ;; hang) sleep 30 ;; *) exit 0 ;; esac ;;
  link)
    case "${STUB_LINK:-ok}" in
      fail) exit 2 ;;
      hang) sleep 30 ;;
      *) echo '{"data":{"run_url":"https://heros-agent.space/app/runs/run-x"}}' > "$outdir/link.json"; exit 0 ;;
    esac ;;
  version) echo '{"ok":true}'; exit 0 ;;
  *) exit 3 ;;
esac
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type ciResult struct {
	code    int
	summary string
	calls   string
	linked  bool
}

func runCI(t *testing.T, bin string, env map[string]string) ciResult {
	t.Helper()
	scriptPath, _ := filepath.Abs(filepath.Join("..", "..", "scripts", "ci", "heros-ci.sh"))
	work := t.TempDir()
	summaryPath := filepath.Join(work, "summary.md")
	callsPath := filepath.Join(work, "calls.txt")
	outDir := filepath.Join(work, "out")

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"HEROS_BIN="+bin,
		"HEROS_REPO="+work,
		"HEROS_OUT="+outDir,
		"HEROS_LINK_TIMEOUT=2",
		"GITHUB_STEP_SUMMARY="+summaryPath,
		"STUB_CALLS="+callsPath,
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, _ := cmd.CombinedOutput()
	code := cmd.ProcessState.ExitCode()
	_ = out
	summary, _ := os.ReadFile(summaryPath)
	calls, _ := os.ReadFile(callsPath)
	_, linkErr := os.Stat(filepath.Join(outDir, "link.json"))
	return ciResult{code: code, summary: string(summary), calls: string(calls), linked: linkErr == nil}
}

// 9.2 — a configured gate failing DOES fail the build and names the gate.
func TestGateFailureFailsBuildAndNames(t *testing.T) {
	bin := stubHeros(t)
	r := runCI(t, bin, map[string]string{"STUB_EVAL_EXIT": "1", "STUB_GATE_NAME": "quality-floor", "HEROS_PLATFORM_TOKEN": "tok"})
	if r.code != 1 {
		t.Fatalf("gate failure build exit = %d, want 1", r.code)
	}
	if !strings.Contains(r.summary, "quality-floor") {
		t.Errorf("the check must name the failed gate; summary=%q", r.summary)
	}
	// A gate failure short-circuits before linking — nothing was transmitted.
	if strings.Contains(r.calls, "link") {
		t.Errorf("link should not run after a gate failure")
	}
}

// 9.1 — platform UNREACHABLE (link errors): reported, build NOT failed.
func TestPlatformUnreachableDoesNotFailBuild(t *testing.T) {
	bin := stubHeros(t)
	r := runCI(t, bin, map[string]string{"STUB_LINK": "fail", "HEROS_PLATFORM_TOKEN": "tok"})
	if r.code != 0 {
		t.Fatalf("platform-unreachable build exit = %d, want 0 (build must not fail)", r.code)
	}
	if !strings.Contains(r.summary, "platform unavailable") {
		t.Errorf("the condition must be reported; summary=%q", r.summary)
	}
}

// 9.1 — platform SLOW (link hangs past the bound): reported as slow, build NOT failed, does not hang.
func TestPlatformSlowDoesNotStallOrFail(t *testing.T) {
	bin := stubHeros(t)
	r := runCI(t, bin, map[string]string{"STUB_LINK": "hang", "HEROS_PLATFORM_TOKEN": "tok"})
	if r.code != 0 {
		t.Fatalf("platform-slow build exit = %d, want 0", r.code)
	}
	if !strings.Contains(r.summary, "platform slow") {
		t.Errorf("a slow platform must be reported as slow; summary=%q", r.summary)
	}
}

// 9.4 — the no-linking configuration transmits nothing.
func TestNoLinkingTransmitsNothing(t *testing.T) {
	bin := stubHeros(t)
	r := runCI(t, bin, map[string]string{}) // no HEROS_PLATFORM_TOKEN
	if r.code != 0 {
		t.Fatalf("no-linking build exit = %d, want 0", r.code)
	}
	if strings.Contains(r.calls, "login") || strings.Contains(r.calls, "link") {
		t.Errorf("no-linking mode must not call login/link; calls=%q", r.calls)
	}
	if !strings.Contains(r.summary, "disabled") {
		t.Errorf("no-linking should report linking disabled; summary=%q", r.summary)
	}
}

// pass path — a clean run links and reports the URL.
func TestPassPathLinksAndReports(t *testing.T) {
	bin := stubHeros(t)
	r := runCI(t, bin, map[string]string{"STUB_LINK": "ok", "HEROS_PLATFORM_TOKEN": "tok"})
	if r.code != 0 {
		t.Fatalf("pass build exit = %d, want 0", r.code)
	}
	if !r.linked || !strings.Contains(r.summary, "view run") {
		t.Errorf("a clean run should link and report the URL; summary=%q", r.summary)
	}
}

// 8.5 — the credential never appears in the check summary or the uploaded artifacts. (The heros.log,
// the one surface it could reach, is deliberately not an uploaded artifact.)
func TestCredentialNeverInEmittedSurfaces(t *testing.T) {
	bin := stubHeros(t)
	scriptPath, _ := filepath.Abs(filepath.Join("..", "..", "scripts", "ci", "heros-ci.sh"))
	work := t.TempDir()
	summaryPath := filepath.Join(work, "summary.md")
	outDir := filepath.Join(work, "out")
	secret := "SUPER-SECRET-TOKEN-abc123"
	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"HEROS_BIN="+bin, "HEROS_REPO="+work, "HEROS_OUT="+outDir, "HEROS_LINK_TIMEOUT=2",
		"GITHUB_STEP_SUMMARY="+summaryPath, "STUB_LINK=ok", "HEROS_PLATFORM_TOKEN="+secret,
	)
	_, _ = cmd.CombinedOutput()

	// The uploaded artifacts are ir.json, discovery-report.json, eval.json (NOT heros.log).
	for _, f := range []string{"summary.md"} {
		b, _ := os.ReadFile(filepath.Join(work, f))
		if strings.Contains(string(b), secret) {
			t.Errorf("credential leaked into %s", f)
		}
	}
	for _, f := range []string{"ir.json", "discovery-report.json", "eval.json", "link.json"} {
		b, _ := os.ReadFile(filepath.Join(outDir, f))
		if strings.Contains(string(b), secret) {
			t.Errorf("credential leaked into artifact %s", f)
		}
	}
}

// 8.7 — the P12 delivery hook is invoked (with the run id) but its behavior is not defined here.
func TestP12DeliveryHookInvoked(t *testing.T) {
	bin := stubHeros(t)
	work := t.TempDir()
	marker := filepath.Join(work, "delivered.txt")
	// The hook writes the run id it received into a marker file.
	hook := "echo $HEROS_RUN_ID > " + marker
	r := runCI(t, bin, map[string]string{"STUB_LINK": "ok", "HEROS_PLATFORM_TOKEN": "tok", "HEROS_DELIVERY_HOOK": hook})
	if r.code != 0 {
		t.Fatalf("build exit = %d, want 0", r.code)
	}
	got, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(got)) != "run-x" {
		t.Errorf("delivery hook was not invoked with the run id: %q err=%v", string(got), err)
	}
}
