package distribution

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// portability_test.go asserts the CLI still COMPILES for every target the matrix says is shipped.
//
// # The failure this exists for, which nothing caught for four days
//
// internal/sandbox stops a runaway child by killing its process group: syscall.Kill and SysProcAttr.Setpgid,
// neither of which exists on Windows. That was fine as long as no Windows-shipped binary linked it — and none
// did, until `heros report-verdict` needed to name a verification.Verdict. internal/verification imports the
// eval runner, the eval runner imports the sandbox, and the whole chain arrived in cmd/heros behind a
// type-only reference:
//
//	cmd/heros → internal/clilink → internal/verification → internal/evalrun → internal/sandbox
//
// The Windows build of the CLI stopped compiling entirely. Every test stayed green, because every test runs on
// the host. The one job that would have seen it — install.ps1 · windows-2022 — is path-filtered to
// scripts/install.{sh,ps1}, so it did not run again until an unrelated change touched an installer, four days
// and a dozen merges later. A break that can only be seen by a job that rarely runs is a break that gets
// found by a customer.
//
// # Why the target list is read from the matrix
//
// Targets() is what the release pipeline builds and what the README promises. A hard-coded list here would be
// a second answer to "which platforms do we ship", and the two would part company the first time a row is
// added — in the direction where the new platform is the untested one.

// TestTheCLICompilesForEveryShippedTarget type-checks cmd/heros's own packages for each shipped GOOS/GOARCH.
//
// CGO is off, so the tree-sitter language frontends drop out and every package that needs them fails inside
// the module cache. Those lines are filtered: they are an artifact of cross-compiling without a C toolchain,
// not a portability defect, and the native runners build them with CGO_ENABLED=1. Only diagnostics attributed
// to files in THIS module are read — which is exactly where the sandbox break appeared.
func TestTheCLICompilesForEveryShippedTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-target build; skipped under -short")
	}
	for _, target := range Targets() {
		if target.Support != SupportShipped {
			continue
		}
		t.Run(target.GOOS+"_"+target.GOARCH, func(t *testing.T) {
			if out := buildForTarget(t, target.GOOS, target.GOARCH, ourPackagesOf(t, "./cmd/heros")...); out != "" {
				t.Errorf("the heros CLI does not compile for %s/%s, which the matrix lists as SHIPPED:\n\n%s\n"+
					"A shipped target that does not build is a release asset that cannot be produced, and the "+
					"job that would notice runs only when an install script changes.", target.GOOS, target.GOARCH, out)
			}
		})
	}
}

// TestThePortabilityCheckCanActuallyFail is the gate's own red-check.
//
// The check above filters compiler output, and a filter that is slightly too broad turns the whole test into a
// green light that means nothing. So it is pointed at internal/sandbox — a package that genuinely cannot
// compile for Windows — and required to SEE the failure. If the sandbox is ever made portable this test fails,
// and the right response is to repoint it at whatever is Unix-only then, not to delete it.
func TestThePortabilityCheckCanActuallyFail(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-target build; skipped under -short")
	}
	out := buildForTarget(t, "windows", "amd64", "github.com/heros-foreal/agentd/internal/sandbox")
	if out == "" {
		t.Error("internal/sandbox now compiles for windows/amd64, so this test no longer proves that " +
			"TestTheCLICompilesForEveryShippedTarget can see a real failure. Repoint it at a package that is " +
			"still Unix-only — do not delete it, or the portability gate becomes unfalsifiable.")
	}
	if !strings.Contains(out, "internal/sandbox") {
		t.Errorf("the check reported a failure that does not name internal/sandbox, so it is not seeing what "+
			"it thinks it is:\n%s", out)
	}
}

// moduleRoot is where both helpers run `go`.
//
// It is not a tidiness preference. The compiler reports file positions RELATIVE TO THE WORKING DIRECTORY, so
// run from internal/distribution the sandbox break reads `../sandbox/subprocess.go:150` — and a filter looking
// for `internal/` drops it. That is not a missed detail, it is a gate that reports success on the exact defect
// it was written to catch. TestThePortabilityCheckCanActuallyFail is what found it.
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("locating the module root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// ourPackagesOf returns the transitive dependencies of pkg that belong to this module. Third-party packages
// are excluded because their portability is not ours to assert and their cross-compilation without CGO is
// meaningless.
func ourPackagesOf(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = moduleRoot(t)
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=windows")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	var ours []string
	for _, line := range strings.Fields(string(out)) {
		if strings.HasPrefix(line, "github.com/heros-foreal/agentd/") {
			ours = append(ours, line)
		}
	}
	if len(ours) == 0 {
		t.Fatalf("no packages from this module in the dependency set of %s — the check is inspecting nothing", pkg)
	}
	return ours
}

// buildForTarget compiles pkgs for one target and returns the diagnostics attributed to files in this module,
// or "" when there are none.
func buildForTarget(t *testing.T, goos, goarch string, pkgs ...string) string {
	t.Helper()
	args := append([]string{"build"}, pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = moduleRoot(t)
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	var ours []string
	for _, line := range strings.Split(string(out), "\n") {
		// A diagnostic from this module is reported relative to the module root: `internal/…` or `cmd/…`.
		// Anything else came out of the module cache, which is the CGO artifact described above.
		if strings.HasPrefix(line, "internal/") || strings.HasPrefix(line, "cmd/") {
			ours = append(ours, line)
		}
	}
	return strings.Join(ours, "\n")
}
