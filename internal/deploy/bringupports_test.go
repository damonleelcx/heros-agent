package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bringupports_test.go fences the defect where up.sh VERIFIED A DIFFERENT DEPLOYMENT THAN IT STARTED.
//
// The three published ports are written into deploy/.env.local on first install and handed to every
// later `compose` call with --env-file, so the FILE decides what the containers publish. up.sh resolved
// them for its own curls with `${CONSOLE_PORT:-4320}` — from its shell, which on a re-run has no
// overrides in it. On a deployment installed with CONSOLE_PORT=14320 the script and the stack then
// disagreed, and the two halves of that failed very differently:
//
//   - Step 50 curled :4320, found nothing, and failed with "the customer console is not healthy on
//     :4320" about a console that was healthy on :14320. A false negative, and the mild half.
//   - The /readyz wait and the auth-enforcement assertion curled :4321, where an UNRELATED process on
//     the host happened to be listening. It answered `{"status":"ready"}` and the script printed
//     "/readyz: ready" having never contacted the deployment. up.sh's second stated rule is that it
//     never reports success it did not observe; this is what breaking it looks like.
//
// # This fence EXECUTES, unlike its neighbours in bringup_test.go
//
// Those are static by necessity — they assert a Dockerfile line or the absence of a GNU-ism. This one
// can do better, because the resolution is a self-contained prologue: it is sliced out of the real
// script and RUN, against a real .env.local, and the fence reads the ports that fall out. A grep for
// `persisted_env` would pass on a version that called it and then threw the answer away.
const platformCompose = "../../deploy/docker-compose.platform.yml"
const adminCompose = "../../deploy/docker-compose.admin-console.yml"

// resolvePorts runs up.sh's port prologue with envfile pointing at contents (empty for "no file yet")
// and env applied on top, and returns AGENTD/CONSOLE/ADMIN_CONSOLE as the script resolved them.
func resolvePorts(t *testing.T, envFileContents string, env ...string) (string, string, string) {
	t.Helper()
	src := readFileForTest(t, upScript)

	// The prologue, verbatim: from the first resolved variable to the line that ends the block.
	start := strings.Index(src, "READY_TIMEOUT=")
	end := strings.Index(src, `mkdir -p "$logdir"`)
	if start < 0 || end <= start {
		t.Fatal("could not locate up.sh's port prologue — this fence found nothing to run")
	}

	dir := t.TempDir()
	envfile := filepath.Join(dir, ".env.local")
	if envFileContents != "" {
		if err := writeFileForTest(envfile, envFileContents); err != nil {
			t.Fatalf("write fake .env.local: %v", err)
		}
	}

	script := "set -euo pipefail\n" +
		"envfile=" + shellQuote(envfile) + "\n" +
		src[start:end] +
		"\nprintf '%s\\n%s\\n%s\\n' \"$AGENTD_PORT\" \"$CONSOLE_PORT\" \"$ADMIN_CONSOLE_PORT\"\n"

	cmd := exec.Command("bash", "-c", script)
	// A CLEARED environment, not the test runner's: an AGENTD_PORT exported by whoever ran `go test`
	// would silently satisfy the assertions and the fence would prove nothing.
	cmd.Env = append([]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("up.sh's port prologue failed to run: %v\n%s", err, out)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 3 {
		t.Fatalf("expected three ports from the prologue, got %q", string(out))
	}
	return lines[0], lines[1], lines[2]
}

// TestTheScriptVerifiesThePortsComposePublishes is the property: for every input, the port up.sh curls
// is the port the containers publish. Compose's own precedence is shell > --env-file > compose default,
// and all three rungs are exercised.
func TestTheScriptVerifiesThePortsComposePublishes(t *testing.T) {
	// 1. THE DEFECT. A second run, no overrides in the shell, ports persisted by the first install.
	//    Compose reads the file; the script must too.
	a, c, ac := resolvePorts(t, "AGENTD_PORT=14321\nCONSOLE_PORT=14320\nADMIN_CONSOLE_PORT=14310\n")
	if a != "14321" || c != "14320" || ac != "14310" {
		t.Errorf("up.sh resolved %s/%s/%s while compose publishes 14321/14320/14310 from .env.local.\n"+
			"The script is about to verify a deployment it did not start: at best it reports a healthy "+
			"console as unhealthy, at worst it curls a port some unrelated host process holds and calls "+
			"that /readyz ready.", a, c, ac)
	}

	// 2. A shell override still wins, because it wins for compose too. Fixing (1) by always preferring
	//    the file would break `CONSOLE_PORT=9999 make deploy-up` in the other direction.
	a, c, ac = resolvePorts(t, "AGENTD_PORT=14321\nCONSOLE_PORT=14320\nADMIN_CONSOLE_PORT=14310\n",
		"AGENTD_PORT=9321", "CONSOLE_PORT=9320", "ADMIN_CONSOLE_PORT=9310")
	if a != "9321" || c != "9320" || ac != "9310" {
		t.Errorf("a shell override no longer wins: got %s/%s/%s, wanted 9321/9320/9310. Compose takes the "+
			"shell value over --env-file, so a script that prefers the file disagrees with the stack "+
			"again — just in the other direction.", a, c, ac)
	}

	// 3. FIRST INSTALL: no file, no overrides. The fallbacks must be the compose files' OWN defaults,
	//    read out of the manifests rather than repeated here — that is the pair that can drift.
	a, c, ac = resolvePorts(t, "")
	for _, want := range []struct {
		name, got, file string
	}{
		{"AGENTD_PORT", a, platformCompose},
		{"CONSOLE_PORT", c, platformCompose},
		{"ADMIN_CONSOLE_PORT", ac, adminCompose},
	} {
		def := composePortDefault(t, want.file, want.name)
		if want.got != def {
			t.Errorf("on a first install up.sh assumes %s=%s, but %s publishes on %s.\n"+
				"With no file and no override these two ARE the deployment, and they disagree.",
				want.name, want.got, filepath.Base(want.file), def)
		}
	}
}

// composePortDefault reads the `${NAME:-default}` a compose file publishes on.
func composePortDefault(t *testing.T, file, name string) string {
	t.Helper()
	re := regexp.MustCompile(`\$\{` + regexp.QuoteMeta(name) + `:-(\d+)\}`)
	m := re.FindStringSubmatch(readFileForTest(t, file))
	if m == nil {
		t.Fatalf("%s no longer publishes a port from ${%s:-…} — this fence found nothing to compare", file, name)
	}
	return m[1]
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func writeFileForTest(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o600)
}
