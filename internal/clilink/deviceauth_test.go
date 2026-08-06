package clilink

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/cli"
)

// deviceauth_test.go drives `heros login` with NO token — the person path (P27 tasks 13.2, 13.4, 13.6).
//
// Everything goes through the real command, the real transport and the endpoint pin: `captureRT` serves
// https://heros-agent.space locally rather than letting a test bypass the pin, so the assertions below
// are about the shipped path and not about a mock of it.

// deviceServer answers the three device routes, holding the code back for `pendingPolls` rounds so the
// CLI's WAITING behaviour is exercised rather than assumed.
func deviceServer(t *testing.T, pendingPolls int32) http.HandlerFunc {
	t.Helper()
	var polls int32
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/device/authorize":
			var body struct {
				Label string `json:"label"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.TrimSpace(body.Label) == "" {
				t.Error("the CLI reported no device label; the approval screen would name nothing a person recognises")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_code": "ABCD-EFGH", "device_code": "heros_device_code_value",
				"verification_uri": "/app/device", "interval_seconds": 0, "expires_in_seconds": 600,
			})
		case "/api/v1/device/token":
			if atomic.AddInt32(&polls, 1) <= pendingPolls {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "approved", "token": "heros_issued_credential_value",
				"identity": "org_acme", "organization_id": "org_acme",
				"organization_name": "Acme", "credential_id": "cred_dev",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestLoginWithNoTokenRunsTheDeviceFlowAndStoresAPersonalCredential(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	// 🔴 Cleared explicitly. If the developer running this has one exported, the command would take the
	// machine path and this test would pass while asserting nothing about the flow it names.
	t.Setenv(cli.EnvPrefix+"PLATFORM_TOKEN", "")

	rt := &captureRT{handler: deviceServer(t, 2)}
	c := Commands{RT: rt, Timeout: 5 * time.Second}
	var out, envelope bytes.Buffer
	s := cli.Streams{Out: &envelope, Err: &out}

	if err := c.Login(deviceCfg(t, nil), s); err != nil {
		t.Fatalf("device login: %v", err)
	}

	// It POLLED rather than succeeding on the first answer: one authorize + two pending + one approved.
	if n := atomic.LoadInt32(&rt.requests); n != 4 {
		t.Errorf("the CLI made %d requests, want 4 (authorize + 2 pending polls + the approval)", n)
	}

	stored, ok := cli.LoadCredential()
	if !ok {
		t.Fatal("no credential was stored")
	}
	if stored.Token != "heros_issued_credential_value" {
		t.Errorf("the stored token is not the issued one")
	}
	if stored.Kind != "personal" {
		t.Errorf("the stored credential kind is %q, want personal — the kind decides whether removing "+
			"this person ends this login, and a caller that has to infer it will infer it wrong", stored.Kind)
	}
	if stored.OrganizationName != "Acme" {
		t.Errorf("the organization name was not recorded: %q", stored.OrganizationName)
	}

	// 🔴 Neither secret is ever printed. The user code and the URL are — those are what a person acts on
	// — and the device code and the issued credential are not: a token in a terminal is a token in a
	// scrollback buffer, a screen recording and a support paste.
	printed := out.String()
	if !strings.Contains(printed, "ABCD-EFGH") || !strings.Contains(printed, "/app/device") {
		t.Errorf("the CLI did not show the code and the URL a person needs:\n%s", printed)
	}
	for _, secret := range []string{"heros_device_code_value", "heros_issued_credential_value"} {
		if strings.Contains(printed, secret) {
			t.Errorf("a secret was printed to the terminal:\n%s", printed)
		}
	}
}

func TestADeniedOrExpiredCodeIsOneMessage(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())
	t.Setenv(cli.EnvPrefix+"PLATFORM_TOKEN", "")

	// Task 13.7 at the CLI: the server refuses without saying why, and the CLI must not invent a reason
	// by reading the status code.
	refuse := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/device/authorize":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_code": "WXYZ-2345", "device_code": "heros_dc", "verification_uri": "/app/device",
				"interval_seconds": 0, "expires_in_seconds": 600,
			})
		case "/api/v1/device/token":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":       "that device code is no longer usable — run `heros login` again",
				"reason_code": "device_code_unusable",
			})
		}
	}
	c := Commands{RT: &captureRT{handler: refuse}, Timeout: 5 * time.Second}
	var out, envelope bytes.Buffer
	s := cli.Streams{Out: &envelope, Err: &out}

	err := c.Login(deviceCfg(t, nil), s)
	if err == nil {
		t.Fatal("a refused device code produced no error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "run `heros login` again") {
		t.Errorf("the message does not tell the user what to do next: %q", msg)
	}
	// The four causes must not be distinguishable from the sentence the user reads.
	for _, leak := range []string{"denied", "expired", "unknown", "already"} {
		if strings.Contains(strings.ToLower(msg), leak) {
			t.Errorf("the message names %q — denied, expired, already-used and unknown are ONE answer, "+
				"because the difference helps only somebody guessing codes: %q", leak, msg)
		}
	}
	if _, ok := cli.LoadCredential(); ok {
		t.Error("a refused login stored a credential")
	}
}

func TestTheTokenPathIsUnchangedAndReportsAMachineCredential(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir())

	// Task 13.4: `--token` behaves exactly as before. What is added is what the platform SAYS about the
	// credential — and a platform that says nothing (a pre-P27 one) still works, defaulting to machine,
	// which is what a token supplied on a command line is by construction.
	var sawDeviceCall bool
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/v1/device/") {
			sawDeviceCall = true
		}
		if r.URL.Path == "/api/v1/whoami" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"identity": "org_ci", "organization_id": "org_ci", "credential_kind": "machine",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
	c := Commands{RT: &captureRT{handler: h}, Timeout: 5 * time.Second}
	var out, envelope bytes.Buffer
	s := cli.Streams{Out: &envelope, Err: &out}

	if err := c.Login(deviceCfg(t, map[string]string{"token": "heros_machine_token"}), s); err != nil {
		t.Fatalf("token login: %v", err)
	}
	if sawDeviceCall {
		t.Error("`--token` reached a device-authorization route; the machine path must not have changed")
	}
	stored, ok := cli.LoadCredential()
	if !ok {
		t.Fatal("no credential stored")
	}
	if stored.Identity != "org_ci" || stored.Token != "heros_machine_token" {
		t.Errorf("the stored credential changed shape: %+v", stored)
	}
	if stored.Kind != "machine" {
		t.Errorf("kind = %q, want machine", stored.Kind)
	}
	if stored.UserID != "" {
		t.Errorf("a machine credential recorded a person (%q) — a placeholder in an attribution field "+
			"reads as somebody who did not act", stored.UserID)
	}
}

// deviceCfg builds the resolved config the dispatcher would hand the command.
// deviceCfg builds the configuration for a device login.
//
// ⚠️ It sets `--device` explicitly, and that is a P28 CHANGE to what these tests exercised. Before P28,
// `heros login` with no token ran the device flow by default; the default is now the email-and-password
// path, because the device flow's second step is "sign in to the console" and on the production seam that
// meant obtaining a shared string from a cluster operator — so the default led to a door most people had no
// key to. The flow itself is unchanged and every assertion below still holds against it; what moved is how
// it is asked for. The no-terminal refusal names `--device` among the four ways in.
func deviceCfg(t *testing.T, kv map[string]string) cli.Config {
	t.Helper()
	r := cli.NewResolver(map[string]string{"repo": ".", "run": "", "token": "", "dry-run": "false", "device": "true"})
	for k, v := range kv {
		r.SetFlag(k, v)
	}
	return r.Resolve()
}

// TestTheIdentityEndpointStaysReadableByItsTwoExistingCallers is task 13.5's other half.
//
// `whoami` grew four fields in this phase. The requirement is that it grew them ADDITIVELY — `identity`
// keeps its name, its meaning and its value — and the way that requirement dies is not somebody renaming
// the field. It is somebody deciding the endpoint should return `{organization: {...}}` because that is
// tidier, and nothing in the platform's own tests noticing that two callers outside it read a flat
// `identity`.
//
// So this drives the CLI's `Validate` — one of those two callers, unchanged since P11 — against a
// response carrying everything P27 added.
func TestTheIdentityEndpointStaysReadableByItsTwoExistingCallers(t *testing.T) {
	full := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/whoami" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"identity":          "org_acme",
			"organization_id":   "org_acme",
			"organization_name": "Acme",
			"user_id":           "usr_1",
			"credential_kind":   "personal",
		})
	}
	c := Commands{RT: &captureRT{handler: full}, Timeout: 5 * time.Second}

	// Caller one: the CLI's token validation. It reads `identity` and nothing else, and it must keep
	// working without being taught about any of the new fields.
	id, err := c.client("heros_some_token").Validate(context.Background())
	if err != nil {
		t.Fatalf("the CLI's Validate no longer reads the identity endpoint: %v", err)
	}
	if id != "org_acme" {
		t.Errorf("Validate returned %q; `identity` must keep its name, meaning and value", id)
	}

	// Caller two — the console's platform-token seam — reads the same flat field over HTTP. Its own suite
	// covers it; what this asserts is that the field it reads is still THERE and still flat, which is the
	// property both callers share and the one an "improvement" to this response would break.
	who, err := c.client("heros_some_token").WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if who.Identity != id {
		t.Errorf("the two readers of this endpoint disagree about the identity: %q vs %q", who.Identity, id)
	}
	if who.CredentialKind != "personal" || who.UserID != "usr_1" || who.OrganizationName != "Acme" {
		t.Errorf("the additive fields did not survive the round trip: %+v", who)
	}
}
