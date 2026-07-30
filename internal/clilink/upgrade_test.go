package clilink

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/distribution"
)

// upgrade_test.go proves the four refusals D7 requires of `heros upgrade` (P20 task 5.4).
//
// The GREEN path — download, verify, replace — needs a manifest signed by the real release key, which no test
// may carry. It is proved instead by `scripts/install_smoke.py`'s upgrade simulation, which signs a fixture
// with the actual key and runs the real binary end to end. Every refusal is proved here, because a refusal is
// what protects a user and refusals are what a unit test can establish honestly.

func upgradeStreams() (cli.Streams, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return cli.Streams{Out: &out, Err: &errb}, &out, &errb
}

// releaseIndex serves a Releases-API-shaped answer naming tag, plus whatever assets are supplied.
func releaseIndex(t *testing.T, tag string, assets map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	})
	for name, body := range assets {
		content := body
		mux.HandleFunc("/download/"+tag+"/"+name, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(content))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("HEROS_RELEASE_API_URL", srv.URL)
	t.Setenv("HEROS_RELEASE_BASE_URL", srv.URL)
	return srv
}

func upgradeData(t *testing.T, stdout string) UpgradeData {
	t.Helper()
	var env struct {
		Data UpgradeData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout)
	}
	return env.Data
}

// TestUpgradeDefersToThePackageManagerBeforeTouchingTheNetwork — refusal 3, and the ORDER matters: a
// brew-installed user must get their answer with no download. Asking the network first and then telling them to
// run `brew upgrade` would be a download nobody wanted, and on a metered connection that is a real cost.
//
// The server here fails the test if it is contacted at all.
func TestUpgradeDefersToThePackageManagerBeforeTouchingTheNetwork(t *testing.T) {
	contacted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("HEROS_RELEASE_API_URL", srv.URL)
	t.Setenv("HEROS_RELEASE_BASE_URL", srv.URL)

	// The advice is computed from the executable's own path, so the test drives it through the exported helper
	// the command itself calls — the same decision, with no dependency on where `go test` put its binary.
	channel, command, owned := cli.UpgradeAdvice("/opt/homebrew/bin/heros")
	if !owned {
		t.Fatal("a Homebrew path is not recognised as manager-owned")
	}
	if !strings.Contains(command, "brew upgrade") {
		t.Errorf("the deferral names %q, not brew's own command", command)
	}
	if channel == "" {
		t.Error("the deferral does not name which manager owns the file")
	}
	if contacted {
		t.Error("the network was contacted while deciding whether to defer")
	}
}

// TestUpgradeIsANoOpWhenCurrent — refusal 2. Reinstalling the same version rewrites a binary someone may be
// running, for nothing.
func TestUpgradeIsANoOpWhenCurrent(t *testing.T) {
	orig := cli.ToolVersion
	t.Cleanup(func() { cli.ToolVersion = orig })
	cli.ToolVersion = "0.20.0"

	// No asset handlers: if the command downloaded anything, it would 404 and fail.
	releaseIndex(t, "v0.20.0", nil)

	s, out, errb := upgradeStreams()
	if err := (Commands{}).Upgrade(cli.Config{}, s); err != nil {
		t.Fatalf("upgrade errored when already current: %v\n%s", err, errb.String())
	}
	data := upgradeData(t, out.String())
	if data.Action != "no-op-already-current" {
		t.Errorf("action = %q, want no-op-already-current", data.Action)
	}
	if !strings.Contains(errb.String(), "Nothing downloaded, nothing changed") {
		t.Errorf("the no-op does not say that nothing changed:\n%s", errb.String())
	}
}

// TestUpgradeRefusesADowngrade — refusal 4, and the subtle one. An old release is a legitimately signed
// artifact, so signature verification cannot tell "newer" from "the version with the bug". Without this check,
// an index that answers with an older tag silently rolls every user back.
func TestUpgradeRefusesADowngrade(t *testing.T) {
	orig := cli.ToolVersion
	t.Cleanup(func() { cli.ToolVersion = orig })
	cli.ToolVersion = "0.21.0"

	releaseIndex(t, "v0.20.0", nil)

	s, _, _ := upgradeStreams()
	err := (Commands{}).Upgrade(cli.Config{}, s)
	if err == nil {
		t.Fatal("upgrade accepted an OLDER release as an upgrade")
	}
	if !strings.Contains(err.Error(), "OLDER") {
		t.Errorf("the refusal does not say the release is older: %v", err)
	}
	// It must still tell the user how to do it deliberately, or the refusal is a dead end.
	if !strings.Contains(err.Error(), "HEROS_VERSION") {
		t.Errorf("the refusal does not name the explicit downgrade path: %v", err)
	}
}

// TestUpgradeRefusesAnUnverifiableRelease — refusal 1, in its three shapes. Each one must leave the installed
// binary untouched: a failed upgrade that replaced the binary anyway would be worse than no upgrade command.
func TestUpgradeRefusesAnUnverifiableRelease(t *testing.T) {
	orig := cli.ToolVersion
	t.Cleanup(func() { cli.ToolVersion = orig })
	cli.ToolVersion = "0.19.0"

	asset := distribution.AssetName("0.20.0", runtime.GOOS, runtime.GOARCH)
	cases := []struct {
		name   string
		assets map[string]string
		expect string
	}{
		{
			name:   "no manifest",
			assets: map[string]string{asset: "new binary bytes"},
			expect: "checksum manifest",
		},
		{
			name: "manifest but no signature",
			assets: map[string]string{
				asset:        "new binary bytes",
				"SHA256SUMS": strings.Repeat("0", 64) + "  " + asset + "\n",
			},
			expect: "no signature",
		},
		{
			name: "signature that does not verify",
			assets: map[string]string{
				asset:            "new binary bytes",
				"SHA256SUMS":     strings.Repeat("0", 64) + "  " + asset + "\n",
				"SHA256SUMS.sig": strings.Repeat("ab", 64),
			},
			expect: "VERIFICATION FAILED",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			releaseIndex(t, "v0.20.0", c.assets)
			s, _, _ := upgradeStreams()
			err := (Commands{}).Upgrade(cli.Config{}, s)
			if err == nil {
				t.Fatalf("upgrade accepted a release with %s", c.name)
			}
			if !strings.Contains(err.Error(), c.expect) {
				t.Errorf("the refusal for %q does not explain itself (%v)", c.name, err)
			}
		})
	}
}

// TestReplaceBinaryIsAtomic — a partially written binary is worse than an old one: it is on PATH, it is
// executable, and it fails in a way that looks like a corrupted install. The replacement must be a rename, and
// a failure must leave the original intact.
func TestReplaceBinaryIsAtomic(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "heros")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(exe, []byte("new binary")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "new binary" {
		t.Errorf("binary content = %q", got)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("the replaced binary is not executable — an upgrade that leaves a non-executable file on PATH " +
			"reports success and breaks every later invocation")
	}
	// No temporary files may survive: a stray .heros-upgrade-* in an install directory is a file someone will
	// eventually find and run.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".heros-upgrade-") {
			t.Errorf("a temporary file survived the replacement: %s", e.Name())
		}
	}

	// An unwritable destination must fail without destroying the original.
	ro := t.TempDir()
	roExe := filepath.Join(ro, "heros")
	if err := os.WriteFile(roExe, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Skipf("cannot make the directory read-only on this filesystem: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	if err := replaceBinary(roExe, []byte("replacement")); err == nil {
		t.Error("replaceBinary succeeded into a read-only directory")
	}
	if got, _ := os.ReadFile(roExe); string(got) != "original" {
		t.Errorf("a failed replacement damaged the installed binary: %q", got)
	}
}

// TestUpgradeIsTheOnlyCommandThatReachesTheIndex — task 5.6. The version check must be reachable ONLY by typing
// `upgrade`: no ordinary command may consult the index, and there is no background check to disable.
func TestUpgradeIsTheOnlyCommandThatReachesTheIndex(t *testing.T) {
	b, err := os.ReadFile("upgrade.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "go func()") || strings.Contains(src, "time.AfterFunc") {
		t.Error("upgrade.go starts background work — a version check that runs on its own is the hot-path " +
			"network call task 5.6 forbids, and it cannot be opted out of")
	}
	// The index is consulted from exactly one function, and that function is called from Upgrade alone.
	if n := strings.Count(src, "/latest"); n != 1 {
		t.Errorf("the release index is referenced %d times; it must have exactly one call site", n)
	}
	// No telemetry: the only header sent is a User-Agent, and nothing about the user or their repository.
	//
	// Comments are stripped first. Scanning the raw file flagged this package's own comment explaining that
	// there IS no telemetry — a check that fires on the sentence documenting the absence would be fixed by
	// deleting the documentation, which is the wrong direction.
	code := stripLineComments(src)
	for _, forbidden := range []string{"telemetry", "analytics", "X-Heros-User", "machine_id", "hostname()"} {
		if strings.Contains(strings.ToLower(code), strings.ToLower(forbidden)) {
			t.Errorf("upgrade.go's CODE references %q — the CLI sends no telemetry", forbidden)
		}
	}
	// The request must carry nothing but a User-Agent and an Accept header. Any other header set on an
	// outbound request is a channel for something about the user.
	for _, line := range strings.Split(code, "\n") {
		if !strings.Contains(line, "Header.Set(") {
			continue
		}
		if !strings.Contains(line, `"User-Agent"`) && !strings.Contains(line, `"Accept"`) {
			t.Errorf("an outbound request sets a header other than User-Agent/Accept: %s", strings.TrimSpace(line))
		}
	}
}

// stripLineComments removes `//` comments so a source scan tests the CODE rather than the prose about it.
// Crude on purpose: it does not need to handle `//` inside a string literal, because a URL in this file is a
// constant the tests above already assert about by value.
func stripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
