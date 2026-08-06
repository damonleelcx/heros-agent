package clilink

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
)

// passwordlogin_test.go drives `heros login` on the P28 path.
//
// Every case here runs with NO terminal attached — which is what `go test` gives us and, more importantly,
// what CI gives our users. So these tests exercise exactly the paths that must work without a person, and
// the refusal that must happen instead of a hidden prompt.

func passwordCfg(t *testing.T, kv map[string]string) cli.Config {
	t.Helper()
	r := cli.NewResolver(map[string]string{
		"repo": ".", "run": "", "token": "", "dry-run": "false", "device": "false", "email": "", "org": "",
	})
	for k, v := range kv {
		r.SetFlag(k, v)
	}
	return r.Resolve()
}

// signinServer answers the one route this path calls.
func signinServer(t *testing.T, wantEmail, wantPassword string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/password/signin" {
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// 🔴 The credential must not travel in the URL, ever. Asserted here rather than trusted, because a
		// query string reaches proxy logs, browser history and every intermediary in between.
		if r.URL.RawQuery != "" {
			t.Errorf("the sign-in put something in the query string: %q", r.URL.RawQuery)
		}
		var body struct {
			Email       string `json:"email"`
			Password    string `json:"password"`
			DeviceLabel string `json:"device_label"`
			Org         string `json:"organization_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		if body.Email != wantEmail || body.Password != wantPassword {
			t.Errorf("the CLI sent %q/%q, want %q/%q", body.Email, body.Password, wantEmail, wantPassword)
		}
		if strings.TrimSpace(body.DeviceLabel) == "" {
			t.Error("no device label was sent — the platform mints no credential without one, so this " +
				"sign-in would succeed and store nothing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": "org_1", "organization_id": "org_1", "organization_name": "Acme Inc",
			"user_id": "usr_1", "email": wantEmail, "email_verified": true, "role": "owner",
			"credential": map[string]any{"token": "heros_secret_value", "credential_id": "cred_1", "kind": "personal"},
		})
	}
}

func TestLoginWithEmailAndEnvPasswordStoresAPersonalCredential(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	t.Setenv(cli.EnvPrefix+"PLATFORM_TOKEN", "")
	t.Setenv(EnvPassword, "a reasonable passphrase")

	rt := &captureRT{handler: signinServer(t, "priya@example.com", "a reasonable passphrase")}
	c := Commands{RT: rt, Timeout: 5 * time.Second}
	var narration, envelope bytes.Buffer
	s := cli.Streams{Out: &envelope, Err: &narration}

	if err := c.Login(passwordCfg(t, map[string]string{"email": "priya@example.com"}), s); err != nil {
		t.Fatalf("login: %v", err)
	}

	stored, ok := cli.LoadCredential()
	if !ok {
		t.Fatal("no credential was stored")
	}
	if stored.Token != "heros_secret_value" {
		t.Fatalf("the wrong value was stored: %q", stored.Token)
	}
	// 🔴 PERSONAL is what makes "remove a member and their access ends" true in a terminal. A caller that
	// had to infer this would infer it wrong for the machine path.
	if stored.Kind != "personal" {
		t.Fatalf("credential kind %q, want personal", stored.Kind)
	}
	if stored.Identity != "priya@example.com" || stored.OrganizationName != "Acme Inc" {
		t.Errorf("the stored credential does not name the person or the organization: %+v", stored)
	}

	// 🔴 Neither the password nor the issued token appears in anything the user or a script reads.
	for _, stream := range map[string]string{"stderr": narration.String(), "stdout": envelope.String()} {
		for _, secret := range []string{"a reasonable passphrase", "heros_secret_value"} {
			if strings.Contains(stream, secret) {
				t.Errorf("a secret was printed: %q in %q", secret, stream)
			}
		}
	}
	var out struct {
		Data struct {
			Kind          string `json:"credential_kind"`
			Authenticated bool   `json:"authenticated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(envelope.Bytes(), &out); err != nil {
		t.Fatalf("the envelope is not JSON: %v — %s", err, envelope.String())
	}
	if !out.Data.Authenticated || out.Data.Kind != "personal" {
		t.Fatalf("the envelope does not state what happened: %s", envelope.String())
	}
}

// The P11 contract, kept: with no terminal and nothing supplied, the command REFUSES and names every
// non-interactive way in. 🚫 It must not block on a prompt nobody can see, which in CI is a job that hangs
// until its timeout with no output at all.
func TestLoginWithoutATerminalRefusesAndNamesTheWaysIn(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	t.Setenv(cli.EnvPrefix+"PLATFORM_TOKEN", "")
	t.Setenv(EnvPassword, "")
	t.Setenv(EnvEmail, "")

	c := Commands{RT: &captureRT{handler: func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request was made before any credential was resolved: %s", r.URL.Path)
	}}, Timeout: 5 * time.Second}
	var narration, envelope bytes.Buffer
	err := c.Login(passwordCfg(t, nil), cli.Streams{Out: &envelope, Err: &narration})
	if err == nil {
		t.Fatal("a login with no terminal and no credentials succeeded")
	}
	var exit *cli.ExitError
	if !asExit(err, &exit) || exit.Code != cli.ExitInvalidCfg {
		t.Fatalf("exit code %v, want the invalid-configuration code", err)
	}
	msg := err.Error()
	for _, want := range []string{"--email", EnvPassword, "stdin", "--token", "--device"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q — a person told only 'no terminal' has no next "+
				"action:\n%s", want, msg)
		}
	}
	if _, ok := cli.LoadCredential(); ok {
		t.Error("a refused login stored a credential")
	}
}

// 🔴 There is no --password flag, and this is the test that keeps it that way. A password in argv is a
// password in the shell history and in every `ps` on the machine.
func TestThereIsNoPasswordFlag(t *testing.T) {
	// The catalogue is what `heros --help` and the generated CLI reference render from, so a flag that
	// exists and is undocumented would still be caught: `app.go`'s FlagSet and this catalogue are held in
	// agreement by internal/cli's own tests.
	for _, name := range []string{"password", "pass", "pwd", "secret"} {
		f, ok := cli.FlagSpec(name)
		if !ok {
			continue
		}
		{
			t.Fatalf("a flag named %q exists. A password supplied as an argument is written to the shell's "+
				"history file and is readable by every other user on the machine via the process list. "+
				"Use $%s, stdin, or the hidden prompt.", f.Name, EnvPassword)
		}
	}
}

// The stdin contract, both meanings, so neither can be broken without noticing.
func TestStdinKeepsBothItsMeanings(t *testing.T) {
	t.Run("two lines are an address and a password", func(t *testing.T) {
		email, pw, err := resolveCredentials(passwordCfg(t, nil), []string{"priya@example.com", "a reasonable passphrase"})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if email != "priya@example.com" || pw != "a reasonable passphrase" {
			t.Fatalf("got %q/%q", email, pw)
		}
	})
	t.Run("one line with an address supplied is the password", func(t *testing.T) {
		email, pw, err := resolveCredentials(passwordCfg(t, map[string]string{"email": "priya@example.com"}),
			[]string{"a reasonable passphrase"})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if email != "priya@example.com" || pw != "a reasonable passphrase" {
			t.Fatalf("got %q/%q", email, pw)
		}
	})
	// The one-line-no-address case is handled in Login itself (it becomes a token, the pre-P28 meaning) and
	// is covered by the machine-path test in link_test.go.
}

// A wrong password produces ONE message, and it does not say which half was wrong. The server declines to
// disclose that; the CLI must not helpfully reconstruct it.
func TestABadPasswordIsOneMessageWithANextAction(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	t.Setenv(cli.EnvPrefix+"PLATFORM_TOKEN", "")
	t.Setenv(EnvPassword, "wrong")

	refuse := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "that email and password did not match", "reason_code": "bad_credentials",
		})
	}
	c := Commands{RT: &captureRT{handler: refuse}, Timeout: 5 * time.Second}
	var narration, envelope bytes.Buffer
	err := c.Login(passwordCfg(t, map[string]string{"email": "priya@example.com"}),
		cli.Streams{Out: &envelope, Err: &narration})
	if err == nil {
		t.Fatal("a refused sign-in produced no error")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "forgot-password") {
		t.Errorf("the refusal offers no way forward: %q", msg)
	}
	for _, leak := range []string{"no such", "unknown account", "not registered", "wrong password"} {
		if strings.Contains(msg, leak) {
			t.Errorf("the message says %q — an unknown address and a wrong password are ONE answer: %q", leak, msg)
		}
	}
	if _, ok := cli.LoadCredential(); ok {
		t.Error("a refused login stored a credential")
	}
}

// A platform with no password endpoint answers 404, and "404" on a sign-in reads as "wrong URL" when it
// means "this install does not offer this". The two have completely different next actions — and this is
// exactly what a customer sees when an ingress does not route the auth paths.
func TestA404SaysWhatItMeans(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	t.Setenv(cli.EnvPrefix+"PLATFORM_TOKEN", "")
	t.Setenv(EnvPassword, "a reasonable passphrase")

	c := Commands{RT: &captureRT{handler: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}}, Timeout: 5 * time.Second}
	var narration, envelope bytes.Buffer
	err := c.Login(passwordCfg(t, map[string]string{"email": "priya@example.com"}),
		cli.Streams{Out: &envelope, Err: &narration})
	if err == nil {
		t.Fatal("a 404 produced no error")
	}
	if !strings.Contains(err.Error(), "--device") {
		t.Errorf("the message does not offer the flow that still works: %q", err.Error())
	}
}
