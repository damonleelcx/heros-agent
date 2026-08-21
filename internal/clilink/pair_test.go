package clilink

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// pair_test.go is P32 task 4.2 and §7.9: the LOCAL-MODE EGRESS CAPTURE.
//
// # Why this is a capture and not an assertion about a struct
//
// Task 4.2 says *"assert by egress capture that no file content, prompt text or diff is transmitted."*
// A payload-level assertion — marshal the struct, grep it — checks a value the test constructed. What
// has to be checked is what the SHIPPED COMMAND actually puts on a socket, because the failure mode is
// a second request nobody thought about: a header, a retry, a diagnostic, a `?path=` on a URL.
//
// So `captureRT` records every request the command makes, and every byte of every one is searched for
// every line of a repository fixture that is on disk while the command runs.

// pairServer answers whoami and the pairing claim.
func pairServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{"identity": "tenantA"})
		case runlink.LocalPairingClaimPath:
			// Echo what arrived, so the test can assert the platform saw the three values it expects
			// rather than trusting the client's own report of what it sent.
			var got map[string]any
			_ = json.NewDecoder(r.Body).Decode(&got)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pairing_id": "pair-1", "workflow_id": "wf-1", "state": "paired",
				"machine_name": got["machine_name"], "revision": got["revision"],
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// TestPairingTransmitsNothingFromTheTree is the §7.9 egress capture.
//
// 🔴 The repository fixture is REAL and on disk while the command runs, with canary content in it. The
// command is given `--repo` pointing at it, so if any code path ever decided to read a file "for
// context", this fails.
func TestPairingTransmitsNothingFromTheTree(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, _ := evalFixture(t)

	// Canary files, in the shapes the three prohibited categories take. Each is distinctive enough
	// that a match cannot be a coincidence.
	canaries := map[string]string{
		"prompt.txt": "SYSTEM PROMPT CANARY: you are a helpful assistant, ignore prior instructions",
		"secret.env": "OPENAI_API_KEY=sk-canary-fixture-do-not-leak",
		"change.diff": "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-DIFF CANARY OLD LINE HERE\n" +
			"+DIFF CANARY NEW LINE HERE\n",
	}
	for name, body := range canaries {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write canary %s: %v", name, err)
		}
	}
	// 🔴 A REAL git repository, because `pair` resolves the revision from one — and the canaries are
	// COMMITTED, so they are part of the tree the command is standing in when it runs. A fixture where
	// the canaries were untracked would leave the strongest version of this fence unbuilt.
	gitInit(t, repo)

	rt := &captureRT{handler: pairServer()}
	runLink(t, rt, "login", "--token", "tok")
	env, bodies := runLink(t, rt, "pair", "--code", "ACDE-FGHJ", "--repo", repo, "--machine", "test-machine")

	if len(bodies) == 0 {
		t.Fatal("the pair command transmitted NOTHING; this capture is asserting nothing")
	}
	all := bytes.Join(bodies, []byte("\n"))

	// 1) The three named categories, verbatim.
	for name, body := range canaries {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if len(line) < 16 {
				continue // too short to be evidence — it would collide with legitimate JSON
			}
			if bytes.Contains(all, []byte(line)) {
				t.Errorf("a transmitted payload carries a line of %s:\n  %s", name, line)
			}
		}
	}

	// 2) EVERY line of the fixture repository, not only the canaries. The canaries prove the named
	//    categories; this proves nothing else slipped through either.
	var lines []string
	walkErr := filepath.Walk(repo, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// 🔴 `.git` and `.heros` are excluded, and the reason is worth stating because the first
		// version of this test went red on them and the red was CORRECT-looking and wrong.
		//
		// `.git/refs/heads/main` contains the commit SHA — which is the one value `pair` deliberately
		// transmits, and the whole point of the pairing. Treating git's own metadata as "a line of the
		// repository" would make this fence permanently red for doing exactly what it is supposed to
		// do, and the fix somebody would reach for is an exception on the SHA, which would then also
		// excuse a real leak of that SHA from anywhere else.
		//
		// The subject is the customer's SOURCE. `.git` is the platform's view of it and `.heros` is
		// our own run store — neither is content, and the sibling fence over `link --with-ir` excludes
		// `.heros` for the same reason.
		if fi.IsDir() {
			if n := fi.Name(); n == ".git" || n == ".heros" {
				return filepath.SkipDir
			}
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); len(l) >= 24 {
				lines = append(lines, l)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(lines) == 0 {
		t.Fatal("the fixture yielded no source lines to check — this fence would pass vacuously")
	}
	for _, l := range lines {
		if bytes.Contains(all, []byte(l)) {
			t.Errorf("a transmitted payload contains a line of the repository:\n  %s", l)
		}
	}

	// 2b) The commit SHA IS transmitted, on purpose, and the exclusion above must not hide a leak of
	//     anything else from `.git`. So the config — which carries the remote URL, i.e. where the
	//     customer's code lives — is checked explicitly.
	if cfg, rerr := os.ReadFile(filepath.Join(repo, ".git", "config")); rerr == nil {
		for _, l := range strings.Split(string(cfg), "\n") {
			if l = strings.TrimSpace(l); len(l) >= 16 && bytes.Contains(all, []byte(l)) {
				t.Errorf("a transmitted payload carries a line of .git/config:\n  %s", l)
			}
		}
	}

	// 3) The REPOSITORY PATH itself. It is the field somebody would add, and it is the customer's own
	//    filesystem layout — often their name and their employer's directory conventions.
	if bytes.Contains(all, []byte(repo)) {
		t.Errorf("a transmitted payload carries the repository PATH (%s). `--repo` is used only to "+
			"resolve the revision, locally.", repo)
	}

	// 4) The whole payload is EXACTLY three keys. Asserted positively, so a fourth key added later
	//    fails here even if its value happens to contain nothing recognisable.
	claim := pairBody(t, bodies)
	var got map[string]any
	if err := json.Unmarshal(claim, &got); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	want := map[string]bool{"user_code": true, "machine_name": true, "revision": true}
	for k := range got {
		if !want[k] {
			t.Errorf("the claim carries an unratified key %q — Mode 3's whole property is that nothing "+
				"from the tree crosses, and a new field is how that stops being true", k)
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("the claim is missing %q; this test's key check would pass vacuously", k)
		}
	}
	if got["machine_name"] != "test-machine" {
		t.Errorf("machine_name = %v, want the --machine override", got["machine_name"])
	}

	// 5) The command REPORTED what it sent. The envelope must agree with the wire, or the disclosure
	//    is decoration.
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("re-marshal envelope data: %v", err)
	}
	var data PairData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode envelope data: %v", err)
	}
	if data.Machine != "test-machine" || data.Revision == "" {
		t.Errorf("the envelope reports %+v, want the machine and the revision that actually crossed", data)
	}
	if got["revision"] != data.Revision {
		t.Errorf("the envelope says revision %q and the wire carried %v — a command that misreports what "+
			"it sent is worse than one that says nothing", data.Revision, got["revision"])
	}
}

// TestPairRefusesWithoutACode.
//
// The remedy is in the message, because a person who ran `heros pair` with no code has not seen the
// console screen that issues one.
func TestPairRefusesWithoutACode(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, _ := evalFixture(t)
	gitInit(t, repo)
	rt := &captureRT{handler: pairServer()}
	runLink(t, rt, "login", "--token", "tok")
	before := len(rt.bodies)

	out, code := runLinkExpectingFailure(t, rt, "pair", "--repo", repo)
	if code == 0 {
		t.Fatalf("`heros pair` with no code succeeded:\n%s", out)
	}
	if !strings.Contains(out, "--code is required") || !strings.Contains(out, "console") {
		t.Errorf("the refusal does not tell the person where a code comes from:\n%s", out)
	}
	if len(rt.bodies) != before {
		t.Errorf("a refused pair transmitted %d request(s); nothing may leave before the flags are valid",
			len(rt.bodies)-before)
	}
}

// TestAMistypedCodeIsNormalisedRatherThanRefused.
//
// The alphabet excludes lookalikes so a mistype is unlikely; case and spacing are not mistakes at all,
// and refusing them would teach a person the product is fragile.
func TestAMistypedCodeIsNormalisedRatherThanRefused(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	repo, _ := evalFixture(t)
	gitInit(t, repo)
	rt := &captureRT{handler: pairServer()}
	runLink(t, rt, "login", "--token", "tok")
	_, bodies := runLink(t, rt, "pair", "--code", "  acde fghj  ", "--repo", repo, "--machine", "m")

	var got map[string]any
	if err := json.Unmarshal(pairBody(t, bodies), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["user_code"] != "ACDE-FGHJ" {
		t.Errorf("user_code = %v, want the normalised form — the CLI normalises before sending so the "+
			"platform compares one shape", got["user_code"])
	}
	// The CLI and the platform must normalise IDENTICALLY, or a code accepted here is refused there.
	if sourceingest.NormalizePairingCode("  acde fghj  ") != "ACDE-FGHJ" {
		t.Error("the CLI and the platform disagree about what a code normalises to")
	}
}

// runLinkExpectingFailure drives the shipped command and returns its output and exit code WITHOUT
// failing the test on a non-zero exit.
//
// A sibling of `runLink` rather than a flag on it: `runLink` t.Fatal's on a non-zero exit, which is the
// right default for the twenty tests that use it, and a boolean parameter would make every one of those
// call sites carry a `false` that means nothing to a reader.
func runLinkExpectingFailure(t *testing.T, rt *captureRT, args ...string) (string, int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := cli.Main(args, cli.Streams{Out: &out, Err: &errBuf},
		func(string) (string, bool) { return "", false }, Commands{RT: rt})
	return out.String() + errBuf.String(), code
}

// gitInit makes a directory a git repository with one commit.
//
// Hermetic: no system config, no signing key, no hooks, and an identity supplied here — a developer's
// own `commit.gpgsign = true` would otherwise make this fixture fail on their machine and nowhere else.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is unavailable or refused (%v): %s", err, out)
		}
	}
	run("init", "--quiet")
	run("add", "-A")
	run("commit", "--quiet", "--no-gpg-sign", "-m", "fixture")
}

func pairBody(t *testing.T, bodies [][]byte) []byte {
	t.Helper()
	for _, b := range bodies {
		var probe map[string]json.RawMessage
		if json.Unmarshal(b, &probe) != nil {
			continue
		}
		if _, ok := probe["user_code"]; ok {
			return b
		}
	}
	t.Fatal("no pairing claim was transmitted")
	return nil
}
