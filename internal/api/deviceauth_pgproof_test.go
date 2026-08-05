//go:build pgproof

// Live proof of the whole terminal login (P27 task 13.8), against real Postgres and over HTTP.
//
// # The one assertion this file exists for
//
// *Log in by device flow, run a command, remove the member, and observe the NEXT request refused.*
//
// That is § 4.5's promise — "remove a member and their access ends at their next request" — asserted in
// the place it was previously FALSE. Before this phase a person authenticated a terminal by pasting an
// organization key: a credential naming nobody, which survives their offboarding, attributes nothing, and
// makes the sentence a lie in every shell it was ever pasted into. Device authorization is what makes it
// true, so proving it end to end is the point of the whole section rather than a nice extra.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/tenancy"
	_ "github.com/lib/pq"
)

// devicePost issues a request with an optional principal and decodes the body. Named for this file
// rather than `post`, which `grapheditor_test.go` already owns in this package with a different shape.
func devicePost(t *testing.T, s *Server, path string, body any, p *auth.Principal) (int, map[string]any) {
	t.Helper()
	return call(t, s, http.MethodPost, path, body, p)
}

// deviceLogin runs the whole flow and returns the issued secret.
func deviceLogin(t *testing.T, s *Server, approver auth.Principal, tenantID, label string) (secret, credentialID string) {
	t.Helper()

	// 1 · the terminal asks, carrying no credential at all.
	code, minted := devicePost(t, s, "/api/v1/device/authorize", map[string]any{"label": label}, nil)
	if code != http.StatusCreated {
		t.Fatalf("authorize answered %d: %v", code, minted)
	}
	userCode, _ := minted["user_code"].(string)
	deviceCode, _ := minted["device_code"].(string)
	if userCode == "" || deviceCode == "" {
		t.Fatalf("authorize returned no codes: %v", minted)
	}

	// 2 · polling before approval is pending, and is NOT an error.
	code, pending := devicePost(t, s, "/api/v1/device/token", map[string]any{"device_code": deviceCode}, nil)
	if code != http.StatusOK || pending["status"] != "pending" {
		t.Fatalf("a poll before approval answered %d %v; the CLI must keep waiting, not fail", code, pending)
	}

	// 3 · a person approves, from the console, into an organization they are a member of.
	code, approved := devicePost(t, s, "/api/v1/device/approve",
		map[string]any{"user_code": userCode, "tenant_id": tenantID, "approve": true}, &approver)
	if code != http.StatusOK {
		t.Fatalf("approve answered %d: %v", code, approved)
	}

	// 4 · the terminal collects, once.
	code, issued := devicePost(t, s, "/api/v1/device/token", map[string]any{"device_code": deviceCode}, nil)
	if code != http.StatusOK || issued["status"] != "approved" {
		t.Fatalf("collect answered %d: %v", code, issued)
	}
	secret, _ = issued["token"].(string)
	credentialID, _ = issued["credential_id"].(string)
	if secret == "" || credentialID == "" {
		t.Fatalf("collection returned no credential: %v", issued)
	}

	// And a SECOND collection is refused: the CLI has the plaintext, and re-issuing it would put the
	// secret in two places.
	if code, again := devicePost(t, s, "/api/v1/device/token", map[string]any{"device_code": deviceCode}, nil); code == http.StatusOK && again["token"] != nil {
		t.Fatalf("a device code was collectable twice — the second poll returned a token")
	}
	return secret, credentialID
}

func TestPGDevice_LogInRunRemoveAndTheNextRequestIsRefused(t *testing.T) {
	s, surf, _ := newLiveSurface(t)
	tenantID, owner := seedLiveOrg(t, s, surf, "Hooli", "sub-boss", "boss@hooli.com")

	// The developer: a real member, with a real console identity, who is about to log a terminal in.
	dev := principalForNewUser(t, surf, tenantID, "sub-dev", "dev@hooli.com")
	mustJoin(t, surf, tenantID, dev.UserID, tenancy.RoleMember)

	secret, credentialID := deviceLogin(t, s, dev, tenantID, "dev@laptop (darwin/arm64)")

	// ── the credential names the PERSON (task 13.3) ──────────────────────────────────────────────────
	reg := auth.NewRegistry(config.Config{}).WithSource(surf.store.(auth.CredentialSource))
	p, ok := reg.Lookup(secret)
	if !ok {
		t.Fatal("the credential device authorization issued does not authenticate")
	}
	if p.TenantID != tenantID {
		t.Errorf("the issued credential is scoped to %q", p.TenantID)
	}
	if p.UserID != dev.UserID {
		t.Fatalf("the issued credential names %q, not the approving person. A credential that names "+
			"nobody survives their offboarding — which is the whole thing this flow exists to fix.", p.UserID)
	}

	// It is listed as PERSONAL, with the label the terminal reported, so a revocation screen names
	// something a human recognises.
	creds, err := surf.store.ListCredentials(tenantID)
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	var found *tenancy.Credential
	for i := range creds {
		if creds[i].CredentialID == credentialID {
			found = &creds[i]
		}
	}
	if found == nil {
		t.Fatal("the issued credential is not in the organization's credential list")
	}
	if found.Kind() != "personal" {
		t.Errorf("the issued credential is listed as %q", found.Kind())
	}
	if found.Label != "dev@laptop (darwin/arm64)" {
		t.Errorf("the credential's label is %q, not the device the CLI reported", found.Label)
	}

	// ── run a command ────────────────────────────────────────────────────────────────────────────────
	// `whoami` is the command, because it is the one every other command's authentication runs through —
	// and because task 13.5 says what it must now say.
	code, who := getWithToken(t, s, "/api/v1/whoami", p)
	if code != http.StatusOK {
		t.Fatalf("whoami answered %d: %v", code, who)
	}
	if who["identity"] != tenantID {
		t.Errorf("`identity` is %v — it must keep its name, meaning and value", who["identity"])
	}
	if who["user_id"] != dev.UserID || who["credential_kind"] != "personal" {
		t.Errorf("whoami does not name the person and the kind: %v", who)
	}
	if who["organization_name"] != "Hooli" {
		t.Errorf("whoami does not name the organization: %v", who)
	}

	// ── remove the member ────────────────────────────────────────────────────────────────────────────
	code, removed := call(t, s, http.MethodDelete, "/api/v1/organization/members/"+dev.UserID, nil, &owner)
	if code != http.StatusOK {
		t.Fatalf("removal answered %d: %v", code, removed)
	}

	// ── 🔴 and the NEXT request is refused ───────────────────────────────────────────────────────────
	if _, ok := reg.Lookup(secret); ok {
		t.Fatal("the terminal's credential still authenticates after its owner was removed.\n" +
			"This is the assertion the whole of section 13 exists for: before device authorization a " +
			"person's CLI held an ORGANIZATION key, which no removal touches — so \"remove a member and " +
			"their access ends\" was false in a shell, and nothing in the product said so.")
	}
	// No restart, no grace window, and our own logs say why while the wire does not.
	if _, cause := reg.LookupWithCause(secret); cause != auth.RefusalRevoked {
		t.Errorf("our logs report %q; a removed member's credential is revoked", cause)
	}
}

// getWithToken issues an authenticated GET as the principal a credential resolved to.
func getWithToken(t *testing.T, s *Server, path string, p auth.Principal) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil).
		WithContext(auth.WithPrincipal(context.Background(), p))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, r)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// TestPGDevice_OnlyAMemberMayApproveAndOnlyIntoTheirOwnOrganization is the authorization half.
func TestPGDevice_OnlyAMemberMayApproveAndOnlyIntoTheirOwnOrganization(t *testing.T) {
	s, surf, _ := newLiveSurface(t)
	acme, acmeOwner := seedLiveOrg(t, s, surf, "Acme", "sub-acme", "owner@acme.com")
	globex, _ := seedLiveOrg(t, s, surf, "Globex", "sub-globex", "owner@globex.com")

	newCode := func() (string, string) {
		code, minted := devicePost(t, s, "/api/v1/device/authorize", map[string]any{"label": "probe"}, nil)
		if code != http.StatusCreated {
			t.Fatalf("authorize answered %d", code)
		}
		u, _ := minted["user_code"].(string)
		d, _ := minted["device_code"].(string)
		return u, d
	}

	// Acme's owner approving into GLOBEX, where they hold no membership at all.
	userCode, _ := newCode()
	if code, body := devicePost(t, s, "/api/v1/device/approve",
		map[string]any{"user_code": userCode, "tenant_id": globex, "approve": true}, &acmeOwner); code != http.StatusForbidden {
		t.Fatalf("a non-member approved a terminal into another organization (%d %v)", code, body)
	}

	// A MACHINE credential — no person — may not approve at all: the credential it would issue names a
	// person, and a CI key has none to name.
	machine := auth.Principal{TenantID: acme, APIKeyID: "cred_ci"}
	userCode, _ = newCode()
	if code, _ := devicePost(t, s, "/api/v1/device/approve",
		map[string]any{"user_code": userCode, "tenant_id": acme, "approve": true}, &machine); code != http.StatusForbidden {
		t.Fatalf("a machine credential approved a terminal login (%d)", code)
	}

	// 🔴 A REMOVED member cannot approve into the organization they just left. This is the same check as
	// the first, one state change later, and it is the one an offboarded person would actually try.
	leaver := principalForNewUser(t, surf, acme, "sub-leaver", "leaver@acme.com")
	mustJoin(t, surf, acme, leaver.UserID, tenancy.RoleMember)
	if _, err := surf.store.RemoveMember(leaver.UserID, acme, flowAt); err != nil {
		t.Fatalf("remove: %v", err)
	}
	userCode, _ = newCode()
	if code, _ := devicePost(t, s, "/api/v1/device/approve",
		map[string]any{"user_code": userCode, "tenant_id": acme, "approve": true}, &leaver); code != http.StatusForbidden {
		t.Fatalf("a removed member approved a terminal into the organization they left (%d)", code)
	}
}

// TestPGDevice_DenialExpiryAndAnUnknownCodeAreOneAnswer is task 13.7, over HTTP.
//
// The store already refuses to distinguish them. This asserts the TRANSPORT does not reintroduce the
// difference through a status code or a reason — a caller that could tell a denial from an unknown code
// would be the enumeration oracle the design avoided one layer down.
func TestPGDevice_DenialExpiryAndAnUnknownCodeAreOneAnswer(t *testing.T) {
	s, surf, _ := newLiveSurface(t)
	tenantID, owner := seedLiveOrg(t, s, surf, "Initech", "sub-init", "owner@initech.com")

	// A denied code.
	code, minted := devicePost(t, s, "/api/v1/device/authorize", map[string]any{"label": "denied"}, nil)
	if code != http.StatusCreated {
		t.Fatalf("authorize answered %d", code)
	}
	userCode, _ := minted["user_code"].(string)
	deniedDevice, _ := minted["device_code"].(string)
	if c, _ := devicePost(t, s, "/api/v1/device/approve",
		map[string]any{"user_code": userCode, "tenant_id": tenantID, "approve": false}, &owner); c != http.StatusOK {
		t.Fatalf("denial answered %d", c)
	}

	// An expired one, written directly with an expiry in the past — the only way to age a code without
	// a fifteen-minute test.
	expiredCode := "heros_expired_device_code_for_the_probe"
	if _, err := surf.store.CreateDeviceAuthorization(tenancy.DeviceAuthorization{
		DeviceID:       tenancy.NewID("dev"),
		UserCodeHash:   tenancy.HashSecret("EXPI-RED1"),
		DeviceCodeHash: tenancy.HashSecret(expiredCode),
		CreatedAt:      flowAt.Add(-time.Hour),
		ExpiresAt:      flowAt.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed the expired authorization: %v", err)
	}

	var answers []string
	for _, probe := range []struct{ what, deviceCode string }{
		{"denied", deniedDevice},
		{"expired", expiredCode},
		{"unknown", "heros_this_device_code_never_existed_at_all"},
	} {
		status, body := devicePost(t, s, "/api/v1/device/token", map[string]any{"device_code": probe.deviceCode}, nil)
		if status != http.StatusBadRequest {
			t.Errorf("a %s code answered %d, want one shared status", probe.what, status)
		}
		answers = append(answers, body["reason_code"].(string)+"|"+body["error"].(string))
	}
	for i := 1; i < len(answers); i++ {
		if answers[i] != answers[0] {
			t.Fatalf("the three refusals differ:\n  %s\n  %s\n"+
				"Denied, expired and unknown must be ONE answer — the difference is useful only to "+
				"somebody enumerating codes, and a real user's next action is identical in all three.",
				answers[0], answers[i])
		}
	}
}
