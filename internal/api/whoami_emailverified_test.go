package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// `whoami` must report whether a person's address is confirmed, because the console's account page
// reads exactly that field and has no other source for it.
//
// # The defect this fences
//
// `whoami` did not carry `email_verified` at all. The console does `Boolean(who.data.email_verified)`,
// so an absent field is `false`, and the banner "Confirm your address to unlock inviting people and
// paid plans" was shown — permanently — to a person whose address WAS confirmed. Observed in
// production: `platform_user.email_verified_at` was written the moment the link was clicked, the
// `verify_email` token was marked consumed in the same second, and the banner never moved. The
// "Send it again" button beside it could not change the answer, because the answer was not being read.
//
// # Why the value is read from the store
//
// Verification happens AFTER a session is issued. Anything carried on the credential is a snapshot
// taken before the event it is meant to report, which is stale exactly when somebody is looking at it.
func TestWhoAmIReportsEmailVerified(t *testing.T) {
	s, surface := newSurface(t, true)
	s.MountRunLinking(nil)

	user, err := surface.store.UpsertUser(tenancy.User{
		Issuer:  tenancy.IssuerPassword,
		Subject: "person@example.com",
		Email:   "person@example.com",
	})
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}

	whoami := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/whoami", nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
			TenantID: "tenantA", Role: "owner", APIKeyID: "k", UserID: user.UserID,
		}))
		rec := httptest.NewRecorder()
		s.Mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("whoami status %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("whoami body: %v", err)
		}
		return body
	}

	// Before confirmation: present and false. PRESENT matters as much as false — an absent field is
	// what the console cannot distinguish from "no".
	before := whoami()
	got, ok := before["email_verified"]
	if !ok {
		t.Fatalf("whoami omits email_verified entirely — the console reads this field and renders a "+
			"missing one as unconfirmed: %v", before)
	}
	if got != false {
		t.Errorf("email_verified before confirmation = %v, want false", got)
	}

	// Confirm the address, exactly as consuming a verify_email token does.
	if _, err := surface.store.MarkEmailVerified(user.UserID, apiAt); err != nil {
		t.Fatalf("mark verified: %v", err)
	}

	after := whoami()
	if after["email_verified"] != true {
		t.Errorf("email_verified after confirmation = %v, want true — this is the assertion that was "+
			"false in production, where the banner never cleared", after["email_verified"])
	}
}

// A machine credential names no person, so the field is ABSENT rather than false: "this credential has
// no address" and "this person has not confirmed theirs" are different facts, and a caller that cannot
// tell them apart will ask a machine to check its email.
func TestWhoAmIOmitsEmailVerifiedForAMachineCredential(t *testing.T) {
	s, _ := newSurface(t, true)
	s.MountRunLinking(nil)

	req := httptest.NewRequest("GET", "/api/v1/whoami", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		TenantID: "tenantA", Role: "member", APIKeyID: "k",
	}))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, present := body["email_verified"]; present {
		t.Errorf("a machine credential has no address; email_verified must be absent, got %v", body)
	}
	if body["credential_kind"] != "machine" {
		t.Errorf("credential_kind = %v, want machine", body["credential_kind"])
	}
}
