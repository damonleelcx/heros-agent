package tenancy

import (
	"errors"
	"testing"
	"time"
)

// suite_test.go is ONE behavioural suite, run against every implementation of Store.
//
// `tenancy_test.go` runs it against `MemStore`; `store_pg_pgproof_test.go` runs the identical function
// against `PGStore` behind the `pgproof` tag. That is the whole of NFR6 as this repository can express
// it: the parity axis here is not SQLite-versus-Postgres (there is no SQLite platform schema — see
// design.md § *A correction to the PRD*), it is memory-versus-durable, and an implementation that
// satisfies the interface but not this suite is not a second implementation, it is a second behaviour.
//
// Every assertion below is written so that a plausible wrong implementation fails it. Where that took
// deliberate effort — the last-owner race, the warm-cache revocation, the single-use invitation — the
// comment says what the wrong version looks like.

var t0 = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// storeSuite exercises one Store end to end. `fresh` must return an empty store.
func storeSuite(t *testing.T, fresh func(t *testing.T) Store) {
	t.Helper()

	t.Run("an organization is a row and survives being read back", func(t *testing.T) {
		s := fresh(t)
		got, err := s.CreateTenant(Tenant{TenantID: "acme", Name: "Acme Inc", CreatedAt: t0})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if got.Status != StatusActive {
			t.Errorf("a new organization should be active, got %q", got.Status)
		}
		back, err := s.GetTenant("acme")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if back.Name != "Acme Inc" {
			t.Errorf("name did not round-trip: %q", back.Name)
		}
		if _, err := s.CreateTenant(Tenant{TenantID: "acme", Name: "Impostor"}); !errors.Is(err, ErrExists) {
			t.Errorf("a second organization with the same id must be refused, got %v", err)
		}
		if _, err := s.GetTenant("nobody"); !errors.Is(err, ErrNotFound) {
			t.Errorf("an unknown organization must be ErrNotFound, got %v", err)
		}
	})

	t.Run("a person is keyed by the federated pair, and email is only a label", func(t *testing.T) {
		s := fresh(t)
		u1, err := s.UpsertUser(User{Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com", CreatedAt: t0})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		// Same identity, new address: one person, updated label.
		u2, err := s.UpsertUser(User{Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana.smith@acme.com", CreatedAt: t0})
		if err != nil {
			t.Fatalf("upsert again: %v", err)
		}
		if u2.UserID != u1.UserID {
			t.Fatalf("the same federated identity produced two people: %s then %s", u1.UserID, u2.UserID)
		}
		if u2.Email != "dana.smith@acme.com" {
			t.Errorf("the display address did not update: %q", u2.Email)
		}
		// Same address, different identity: two people. This is the case that makes email-as-identity
		// wrong — the new hire who inherits an address must not inherit the account.
		u3, err := s.UpsertUser(User{Issuer: "https://idp.acme", Subject: "sub-2", Email: "dana.smith@acme.com", CreatedAt: t0})
		if err != nil {
			t.Fatalf("upsert third: %v", err)
		}
		if u3.UserID == u1.UserID {
			t.Fatal("two different federated identities collapsed into one person because they shared an address")
		}
		found, err := s.FindUser("https://idp.acme", "sub-2")
		if err != nil || found.UserID != u3.UserID {
			t.Fatalf("find by federated identity: %v / %+v", err, found)
		}
	})

	t.Run("one person may belong to two organizations", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		mustTenant(t, s, "globex", "Globex")
		u := mustUser(t, s, "sub-1", "dana@acme.com")
		mustMember(t, s, u.UserID, "acme", RoleOwner)
		mustMember(t, s, u.UserID, "globex", RoleMember)

		ms, err := s.ListMembershipsFor(u.UserID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(ms) != 2 {
			t.Fatalf("a contractor must be able to hold two memberships, got %d", len(ms))
		}
		if ms[0].Role == ms[1].Role {
			t.Error("the two memberships should carry their own roles")
		}
	})

	t.Run("the last owner cannot be removed or demoted", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		owner := mustUser(t, s, "sub-owner", "owner@acme.com")
		member := mustUser(t, s, "sub-member", "member@acme.com")
		mustMember(t, s, owner.UserID, "acme", RoleOwner)
		mustMember(t, s, member.UserID, "acme", RoleMember)

		if _, err := s.SetRole(owner.UserID, "acme", RoleMember); !errors.Is(err, ErrLastOwner) {
			t.Fatalf("demoting the last owner must be refused with a NAMED error, got %v", err)
		}
		if _, err := s.RemoveMember(owner.UserID, "acme", t0); !errors.Is(err, ErrLastOwner) {
			t.Fatalf("removing the last owner must be refused with a NAMED error, got %v", err)
		}
		// Promote the member, and now the original owner may leave.
		if _, err := s.SetRole(member.UserID, "acme", RoleOwner); err != nil {
			t.Fatalf("promote: %v", err)
		}
		if _, err := s.RemoveMember(owner.UserID, "acme", t0); err != nil {
			t.Fatalf("with a second owner in place the first must be removable: %v", err)
		}
	})

	t.Run("removing a member revokes their sessions and personal credentials, and leaves machine ones", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		owner := mustUser(t, s, "sub-owner", "owner@acme.com")
		leaver := mustUser(t, s, "sub-leaver", "leaver@acme.com")
		mustMember(t, s, owner.UserID, "acme", RoleOwner)
		mustMember(t, s, leaver.UserID, "acme", RoleMember)

		personal := mustCredential(t, s, "acme", leaver.UserID, "leaver's laptop")
		machine := mustCredential(t, s, "acme", "", "CI deploy key")
		othersPersonal := mustCredential(t, s, "acme", owner.UserID, "owner's laptop")

		mustSession(t, s, "acme", leaver.UserID, "sess-leaver")
		mustSession(t, s, "acme", owner.UserID, "sess-owner")

		// The preview must state BOTH halves before anything happens.
		prev, err := s.PreviewRemoval(leaver.UserID, "acme")
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		if len(prev.PersonalCredentials) != 1 || prev.PersonalCredentials[0].CredentialID != personal.CredentialID {
			t.Errorf("preview should name the one personal credential, got %+v", prev.PersonalCredentials)
		}
		if len(prev.MachineCredentials) != 1 || prev.MachineCredentials[0].CredentialID != machine.CredentialID {
			t.Errorf("preview must name the machine credential removal will NOT revoke; an offboarding "+
				"screen that hides that is worse than none. got %+v", prev.MachineCredentials)
		}
		if prev.Sessions != 1 {
			t.Errorf("preview counted %d sessions, want 1", prev.Sessions)
		}
		if prev.LastOwner {
			t.Error("a plain member is not the last owner")
		}

		res, err := s.RemoveMember(leaver.UserID, "acme", t0)
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		if res.CredentialsRevoked != 1 || res.SessionsRevoked != 1 {
			t.Errorf("removal revoked %d credentials and %d sessions, want 1 and 1", res.CredentialsRevoked, res.SessionsRevoked)
		}

		// 🔴 The assertion that matters: read back through the RESOLUTION path, not the write's return
		// value. A removal that reports success and leaves the credential resolvable is exactly the
		// failure the offboarding claim rests on.
		gotPersonal, err := s.ResolveCredential(personal.Hash)
		if err != nil {
			t.Fatalf("resolve revoked credential: %v", err)
		}
		if !gotPersonal.Revoked() {
			t.Error("the removed person's credential still resolves as live")
		}
		gotMachine, err := s.ResolveCredential(machine.Hash)
		if err != nil || gotMachine.Revoked() {
			t.Error("the machine credential was revoked by removing a person — that breaks the customer's build")
		}
		gotOther, err := s.ResolveCredential(othersPersonal.Hash)
		if err != nil || gotOther.Revoked() {
			t.Error("another member's credential was revoked")
		}
		leaverSessions, err := s.ListSessionsFor(leaver.UserID, "acme")
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		for _, sess := range leaverSessions {
			if sess.RevokedAt == 0 {
				t.Error("the removed person still holds a live session")
			}
		}
		ownerSessions, _ := s.ListSessionsFor(owner.UserID, "acme")
		for _, sess := range ownerSessions {
			if sess.RevokedAt != 0 {
				t.Error("another member's session was revoked")
			}
		}
	})

	t.Run("an invitation is single-use and expires", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")

		inv, err := s.CreateInvitation(Invitation{
			InvitationID: NewID("inv"), TenantID: "acme", Email: "New.Hire@Acme.com",
			Role: RoleMember, CreatedAt: t0, ExpiresAt: t0.Add(48 * time.Hour),
		})
		if err != nil {
			t.Fatalf("create invitation: %v", err)
		}
		if inv.Email != "new.hire@acme.com" {
			t.Errorf("the address should be normalised for matching, got %q", inv.Email)
		}

		if _, err := s.AcceptInvitation(inv.InvitationID, t0.Add(time.Hour)); err != nil {
			t.Fatalf("accept: %v", err)
		}
		// 🔴 Single-use has to be a property of the STORE. In the caller it is a check-then-act race:
		// two concurrent acceptances both pass the check and both create a membership.
		if _, err := s.AcceptInvitation(inv.InvitationID, t0.Add(2*time.Hour)); !errors.Is(err, ErrInviteExpired) {
			t.Errorf("a second acceptance must be refused, got %v", err)
		}

		expired, err := s.CreateInvitation(Invitation{
			InvitationID: NewID("inv"), TenantID: "acme", Email: "late@acme.com",
			Role: RoleMember, CreatedAt: t0, ExpiresAt: t0.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("create expiring invitation: %v", err)
		}
		if _, err := s.AcceptInvitation(expired.InvitationID, t0.Add(2*time.Hour)); !errors.Is(err, ErrInviteExpired) {
			t.Errorf("an expired invitation must be refused, got %v", err)
		}

		if _, err := s.CreateInvitation(Invitation{
			InvitationID: NewID("inv"), TenantID: "acme", Email: "forever@acme.com", Role: RoleMember,
		}); err == nil {
			t.Error("an invitation with no expiry must be refused — a standing offer in an inbox is a way in nobody tracks")
		}
	})

	t.Run("a credential resolves by hash, is revocable, and never carries a secret", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		secret, err := NewCredentialSecret()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		created, err := s.CreateCredential(Credential{
			CredentialID: NewID("cred"), TenantID: "acme", Label: "CI", Role: RoleMember,
			Hash: HashSecret(secret), CreatedAt: t0,
		})
		if err != nil {
			t.Fatalf("create credential: %v", err)
		}
		if created.Personal() {
			t.Error("a credential with no user is a MACHINE credential")
		}
		if created.Kind() != "machine" {
			t.Errorf("Kind() should say machine, got %q", created.Kind())
		}

		got, err := s.ResolveCredential(HashSecret(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got.CredentialID != created.CredentialID {
			t.Errorf("resolved the wrong credential")
		}
		if _, err := s.ResolveCredential(HashSecret("heros_not-a-real-secret")); !errors.Is(err, ErrNotFound) {
			t.Errorf("an unknown hash must be ErrNotFound, got %v", err)
		}

		if _, err := s.RevokeCredential(created.CredentialID, t0); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		after, err := s.ResolveCredential(HashSecret(secret))
		if err != nil {
			t.Fatalf("resolve after revoke: %v", err)
		}
		if !after.Revoked() {
			t.Error("a revoked credential still resolves as live")
		}
	})

	t.Run("suspension is a state on the organization", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		got, err := s.SetTenantStatus("acme", StatusSuspended, t0)
		if err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if got.Active() {
			t.Error("a suspended organization reports itself active")
		}
		back, _ := s.GetTenant("acme")
		if !back.Status.Suspended() {
			t.Error("suspension did not persist")
		}
		if _, err := s.SetTenantStatus("acme", StatusActive, t0); err != nil {
			t.Fatalf("reactivate: %v", err)
		}
		if back, _ := s.GetTenant("acme"); !back.Active() {
			t.Error("reactivation did not persist")
		}
	})

	t.Run("a session resolves, revokes, and carries a person only when there is one", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-1", "dana@acme.com")
		mustMember(t, s, u.UserID, "acme", RoleOwner)

		withUser := mustSession(t, s, "acme", u.UserID, "sess-1")
		withoutUser := mustSession(t, s, "acme", "", "sess-2")
		if withUser.UserID == "" {
			t.Error("an interactive session must name the person")
		}
		if withoutUser.UserID != "" {
			t.Error("a machine principal's session must carry NO user — not a placeholder")
		}

		got, err := s.ResolveSession(withUser.TokenHash)
		if err != nil || got.SessionID != "sess-1" {
			t.Fatalf("resolve session: %v / %+v", err, got)
		}
		if !got.Live(t0.UnixMilli()) {
			t.Error("a fresh session should be live")
		}
		if err := s.RevokeSession(withUser.TokenHash, t0.UnixMilli()); err != nil {
			t.Fatalf("revoke session: %v", err)
		}
		after, _ := s.ResolveSession(withUser.TokenHash)
		if after.Live(t0.UnixMilli()) {
			t.Error("a revoked session is still live")
		}
	})

	t.Run("a read that finds nothing is an error, not an empty answer", func(t *testing.T) {
		s := fresh(t)
		if _, err := s.GetMembership("nobody", "nowhere"); !errors.Is(err, ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
		if _, err := s.GetInvitation("nope"); !errors.Is(err, ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
		if _, err := s.ResolveSession("nope"); !errors.Is(err, ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("an unknown role is refused everywhere it can be supplied", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-1", "dana@acme.com")
		if _, err := s.PutMembership(Membership{UserID: u.UserID, TenantID: "acme", Role: "superadmin"}); !errors.Is(err, ErrUnknownRole) {
			t.Errorf("membership: want ErrUnknownRole, got %v", err)
		}
		if _, err := s.CreateInvitation(Invitation{
			InvitationID: NewID("inv"), TenantID: "acme", Email: "x@acme.com",
			Role: "superadmin", ExpiresAt: t0.Add(time.Hour),
		}); !errors.Is(err, ErrUnknownRole) {
			t.Errorf("invitation: want ErrUnknownRole, got %v", err)
		}
	})
	// ── device authorization (task 13.1) ───────────────────────────────────────────────────────────
	//
	// Every assertion below is about a REFUSAL, because the happy path is the easy half and the refusals
	// are what make the flow safe: single-use at the store rather than in caller logic, and one
	// indistinguishable answer for denied, expired, already-used and unknown.

	t.Run("a device authorization is approved once and collected once", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-dev", "dev@acme.com")
		mustMember(t, s, u.UserID, "acme", RoleOwner)

		userCode, deviceCode := "ABCD-EFGH", "heros_devicecode_one"
		d, err := s.CreateDeviceAuthorization(DeviceAuthorization{
			DeviceID: "dev_1", UserCodeHash: HashSecret(NormalizeUserCode(userCode)),
			DeviceCodeHash: HashSecret(deviceCode), Label: "damon@studio (darwin/arm64)",
			CreatedAt: t0, ExpiresAt: t0.Add(DeviceCodeTTL),
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if d.Status != DevicePending {
			t.Fatalf("a new authorization is %q, want pending", d.Status)
		}

		// Polling before approval is the one non-terminal answer: pending, no error, keep waiting.
		got, err := s.CollectDevice(HashSecret(deviceCode), t0.Add(time.Second))
		if err != nil {
			t.Fatalf("polling a pending authorization must not error: %v", err)
		}
		if got.Status != DevicePending || got.CollectedAt != nil {
			t.Fatalf("a pending poll returned %+v", got)
		}

		found, err := s.FindDeviceByUserCode(HashSecret(NormalizeUserCode("abcd efgh")))
		if err != nil {
			t.Fatalf("the code a person retyped with different spacing did not resolve: %v", err)
		}
		if found.DeviceID != "dev_1" {
			t.Fatalf("resolved %q", found.DeviceID)
		}

		cred := mustCredential(t, s, "acme", u.UserID, "damon@studio (darwin/arm64)")
		decided, err := s.DecideDevice("dev_1", DeviceApproved, u.UserID, "acme", cred.CredentialID, t0.Add(time.Minute))
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		if decided.TenantID != "acme" || decided.ApprovedBy != u.UserID || decided.CredentialID != cred.CredentialID {
			t.Fatalf("the decision did not record who, where and what: %+v", decided)
		}

		// 🔴 A SECOND decision is refused. Two tabs, a double-clicked Approve or a retry after a timeout
		// all land here, and a second credential nobody remembers approving would be a working key.
		if _, err := s.DecideDevice("dev_1", DeviceApproved, u.UserID, "acme", "cred_second", t0.Add(2*time.Minute)); !errors.Is(err, ErrDeviceCode) {
			t.Fatalf("a second approval was accepted (%v) — single-use must live at the store", err)
		}

		collected, err := s.CollectDevice(HashSecret(deviceCode), t0.Add(2*time.Minute))
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if collected.CredentialID != cred.CredentialID || collected.CollectedAt == nil {
			t.Fatalf("collection returned %+v", collected)
		}
		// And a second collection is refused: the CLI has the plaintext, and re-issuing it would mean the
		// secret existed in two places.
		if _, err := s.CollectDevice(HashSecret(deviceCode), t0.Add(3*time.Minute)); !errors.Is(err, ErrDeviceCode) {
			t.Fatalf("a device code was collectable twice (%v)", err)
		}
	})

	t.Run("denied, expired and unknown are one answer", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-dev2", "dev2@acme.com")
		mustMember(t, s, u.UserID, "acme", RoleOwner)

		mk := func(id, user, device string, expires time.Time) {
			if _, err := s.CreateDeviceAuthorization(DeviceAuthorization{
				DeviceID: id, UserCodeHash: HashSecret(user), DeviceCodeHash: HashSecret(device),
				CreatedAt: t0, ExpiresAt: expires,
			}); err != nil {
				t.Fatalf("create %s: %v", id, err)
			}
		}
		mk("dev_denied", "CODE-DENY", "heros_dc_denied", t0.Add(DeviceCodeTTL))
		mk("dev_expired", "CODE-EXPD", "heros_dc_expired", t0.Add(time.Minute))

		if _, err := s.DecideDevice("dev_denied", DeviceDenied, u.UserID, "", "", t0.Add(time.Second)); err != nil {
			t.Fatalf("deny: %v", err)
		}

		// 🔴 The same error value for all four. A caller that could tell them apart would be an oracle for
		// somebody enumerating short codes — and a real user's next action is identical in every case.
		for _, c := range []struct {
			what string
			hash string
			at   time.Time
		}{
			{"denied", HashSecret("heros_dc_denied"), t0.Add(2 * time.Second)},
			{"expired", HashSecret("heros_dc_expired"), t0.Add(2 * time.Minute)},
			{"unknown", HashSecret("heros_dc_never_existed"), t0.Add(time.Second)},
		} {
			if _, err := s.CollectDevice(c.hash, c.at); !errors.Is(err, ErrDeviceCode) {
				t.Errorf("collecting a %s code returned %v, want the one indistinguishable refusal", c.what, err)
			}
		}

		// And an expired authorization can no longer be approved either.
		if _, err := s.DecideDevice("dev_expired", DeviceApproved, u.UserID, "acme", "cred_x", t0.Add(2*time.Minute)); !errors.Is(err, ErrDeviceCode) {
			t.Errorf("an expired authorization was approvable (%v)", err)
		}
	})

	t.Run("two live authorizations cannot share a code", func(t *testing.T) {
		s := fresh(t)
		first := DeviceAuthorization{
			DeviceID: "dev_a", UserCodeHash: HashSecret("SAME-CODE"), DeviceCodeHash: HashSecret("heros_dc_a"),
			CreatedAt: t0, ExpiresAt: t0.Add(DeviceCodeTTL),
		}
		if _, err := s.CreateDeviceAuthorization(first); err != nil {
			t.Fatalf("create: %v", err)
		}
		second := first
		second.DeviceID = "dev_b"
		second.DeviceCodeHash = HashSecret("heros_dc_b")
		if _, err := s.CreateDeviceAuthorization(second); !errors.Is(err, ErrExists) {
			t.Fatalf("a second authorization took the same USER code (%v) — \"which one did they approve\" "+
				"would have no answer", err)
		}
		third := first
		third.DeviceID = "dev_c"
		third.UserCodeHash = HashSecret("OTHER-CODE")
		if _, err := s.CreateDeviceAuthorization(third); !errors.Is(err, ErrExists) {
			t.Fatalf("a second authorization took the same DEVICE code (%v)", err)
		}
	})

	// ── P28: passwords and identity tokens ──────────────────────────────────────────────────────────

	// 🔴 The store refuses anything that is not an argon2id encoding. This is the code-side copy of the
	// database CHECK, and it is asserted against BOTH stores precisely so the in-memory one does not
	// quietly permit what the durable one forbids — the hex string below is what `HashSecret` produces,
	// which is the specific wrong value this guard exists to catch.
	t.Run("a stored password must be an argon2id encoding", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-pw", "dana@acme.com")
		for _, bad := range []string{
			"",
			HashSecret("hunter2hunter2"),
			"$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
			"plaintext-obviously",
		} {
			if _, err := s.SetPassword(u.UserID, bad, t0); err == nil {
				t.Errorf("the store accepted a non-argon2id stored password: %q", bad)
			}
		}
		if _, err := s.GetPassword(u.UserID); !errors.Is(err, ErrNoPassword) {
			t.Errorf("a person with no password must be ErrNoPassword, got %v", err)
		}
	})

	t.Run("a password round-trips and setting one clears the lockout", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-pw", "dana@acme.com")
		enc := "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2ExMg$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaDI"
		if _, err := s.SetPassword(u.UserID, enc, t0); err != nil {
			t.Fatalf("set: %v", err)
		}
		back, err := s.GetPassword(u.UserID)
		if err != nil || back.Encoded != enc {
			t.Fatalf("the encoding did not round-trip: %q / %v", back.Encoded, err)
		}

		// Lock it, then set a new password. 🔴 The lock must be gone: somebody who has just proved control
		// of their address by resetting must not still be locked out by the failures that made them reset.
		pol := LockoutPolicy{Threshold: 2, Window: time.Hour, LockFor: time.Hour}
		if _, err := s.RecordPasswordFailure(u.UserID, t0, pol); err != nil {
			t.Fatalf("failure: %v", err)
		}
		locked, err := s.RecordPasswordFailure(u.UserID, t0, pol)
		if err != nil {
			t.Fatalf("failure: %v", err)
		}
		if !locked.Locked(t0) {
			t.Fatalf("reaching the threshold did not lock: %+v", locked)
		}
		after, err := s.SetPassword(u.UserID, enc, t0)
		if err != nil {
			t.Fatalf("set after lock: %v", err)
		}
		if after.Locked(t0) || after.FailedAttempts != 0 {
			t.Fatalf("setting a password left the account locked: %+v", after)
		}
	})

	// The window is the sentence the copy promises — "ten failures WITHIN fifteen minutes" — so a failure
	// after the window has run out starts counting again rather than accumulating forever. A store that
	// implemented a plain counter would pass every other assertion here and fail this one.
	t.Run("the lockout window restarts rather than accumulating", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-pw", "dana@acme.com")
		enc := "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2ExMg$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaDI"
		if _, err := s.SetPassword(u.UserID, enc, t0); err != nil {
			t.Fatalf("set: %v", err)
		}
		pol := LockoutPolicy{Threshold: 3, Window: 10 * time.Minute, LockFor: time.Hour}
		if _, err := s.RecordPasswordFailure(u.UserID, t0, pol); err != nil {
			t.Fatalf("failure: %v", err)
		}
		if _, err := s.RecordPasswordFailure(u.UserID, t0.Add(time.Minute), pol); err != nil {
			t.Fatalf("failure: %v", err)
		}
		// Well outside the window: this is attempt ONE of a new run, not attempt three of the old one.
		out, err := s.RecordPasswordFailure(u.UserID, t0.Add(time.Hour), pol)
		if err != nil {
			t.Fatalf("failure: %v", err)
		}
		if out.FailedAttempts != 1 {
			t.Fatalf("a failure outside the window counted as %d, want 1 — the window is accumulating", out.FailedAttempts)
		}
		if out.Locked(t0.Add(time.Hour)) {
			t.Fatal("a single failure in a fresh window locked the account")
		}
		if err := s.ClearPasswordFailures(u.UserID); err != nil {
			t.Fatalf("clear: %v", err)
		}
		cleared, _ := s.GetPassword(u.UserID)
		if cleared.FailedAttempts != 0 || cleared.LockedUntil != nil {
			t.Fatalf("clearing left state behind: %+v", cleared)
		}
	})

	t.Run("a person is findable by address on the password issuer only", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		// Two people at ONE address under two issuers. 🔴 They are different people, and neither lookup may
		// return the other — this is what stops a federated identity and a password identity from merging.
		pw, err := s.UpsertUser(User{Issuer: IssuerPassword, Subject: PasswordSubject("Dana@Acme.com"),
			Email: "Dana@Acme.com", CreatedAt: t0})
		if err != nil {
			t.Fatalf("upsert password user: %v", err)
		}
		fed, err := s.UpsertUser(User{Issuer: "https://idp.acme", Subject: "sub-fed",
			Email: "dana@acme.com", CreatedAt: t0})
		if err != nil {
			t.Fatalf("upsert federated user: %v", err)
		}
		if pw.UserID == fed.UserID {
			t.Fatal("two issuers at one address collapsed into one person")
		}
		// Case-insensitive: the address was stored with capitals and is looked up without them.
		got, err := s.FindUserByEmail(IssuerPassword, "dana@acme.com")
		if err != nil || got.UserID != pw.UserID {
			t.Fatalf("FindUserByEmail returned %q/%v, want %q", got.UserID, err, pw.UserID)
		}
		if _, err := s.FindUserByEmail(IssuerPassword, "nobody@acme.com"); !errors.Is(err, ErrNotFound) {
			t.Errorf("an unknown address must be ErrNotFound, got %v", err)
		}
		if got.EmailVerified() {
			t.Error("a new person is not verified")
		}
		v, err := s.MarkEmailVerified(pw.UserID, t0)
		if err != nil || !v.EmailVerified() {
			t.Fatalf("MarkEmailVerified: %+v / %v", v, err)
		}
		// Idempotent, keeping the FIRST time: an audit reading this asks when the address was proved.
		again, err := s.MarkEmailVerified(pw.UserID, t0.Add(time.Hour))
		if err != nil {
			t.Fatalf("verify again: %v", err)
		}
		if !again.EmailVerifiedAt.Equal(*v.EmailVerifiedAt) {
			t.Errorf("a second confirmation moved the timestamp: %v -> %v", v.EmailVerifiedAt, again.EmailVerifiedAt)
		}
	})

	// 🔴 The single-use property. `ConsumeIdentityToken` must be the decider — a store that read the row,
	// checked it in Go and wrote it back would pass the sequential case below and lose the concurrent one.
	t.Run("an identity token is spent exactly once, and only for its own purpose", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		u := mustUser(t, s, "sub-pw", "dana@acme.com")
		mint := func(secret string, purpose TokenPurpose, expires time.Time) {
			if _, err := s.MintIdentityToken(IdentityToken{
				TokenHash: HashSecret(secret), UserID: u.UserID, Purpose: purpose,
				Email: "dana@acme.com", CreatedAt: t0, ExpiresAt: expires,
			}); err != nil {
				t.Fatalf("mint %s: %v", secret, err)
			}
		}
		mint("reset-secret", TokenResetPassword, t0.Add(time.Hour))
		mint("verify-secret", TokenVerifyEmail, t0.Add(24*time.Hour))
		mint("expired-secret", TokenResetPassword, t0.Add(-time.Minute))

		// Wrong purpose: refused, and NOT consumed — the right purpose still works afterwards.
		if _, err := s.ConsumeIdentityToken(HashSecret("reset-secret"), TokenVerifyEmail, t0); !errors.Is(err, ErrIdentityToken) {
			t.Fatalf("a reset token was accepted at the confirmation purpose: %v", err)
		}
		got, err := s.ConsumeIdentityToken(HashSecret("reset-secret"), TokenResetPassword, t0)
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		if got.UserID != u.UserID || got.Email != "dana@acme.com" || got.ConsumedAt == nil {
			t.Fatalf("the consumed token is wrong: %+v", got)
		}
		// Twice is refused.
		if _, err := s.ConsumeIdentityToken(HashSecret("reset-secret"), TokenResetPassword, t0); !errors.Is(err, ErrIdentityToken) {
			t.Fatalf("a token was spent twice: %v", err)
		}
		// Expired and unknown are the SAME error as spent — one answer, four causes.
		if _, err := s.ConsumeIdentityToken(HashSecret("expired-secret"), TokenResetPassword, t0); !errors.Is(err, ErrIdentityToken) {
			t.Fatalf("an expired token was accepted: %v", err)
		}
		if _, err := s.ConsumeIdentityToken(HashSecret("never-existed"), TokenResetPassword, t0); !errors.Is(err, ErrIdentityToken) {
			t.Fatalf("an unknown token gave a different error from a spent one: %v", err)
		}
		// A token minted with an unknown purpose is refused at the store.
		if _, err := s.MintIdentityToken(IdentityToken{
			TokenHash: HashSecret("x"), UserID: u.UserID, Purpose: "something_else",
			Email: "dana@acme.com", ExpiresAt: t0.Add(time.Hour),
		}); !errors.Is(err, ErrUnknownTokenPurpose) {
			t.Fatalf("an unknown token purpose was minted: %v", err)
		}
		// And one with no expiry: a link that never dies is a way in nobody is tracking.
		if _, err := s.MintIdentityToken(IdentityToken{
			TokenHash: HashSecret("y"), UserID: u.UserID, Purpose: TokenResetPassword, Email: "dana@acme.com",
		}); err == nil {
			t.Fatal("a token with no expiry was minted")
		}
	})

	// 🔴 What a reset actually does. The failure this catches is the one that matters: a reset that revokes
	// the sessions and leaves the personal credentials — or the reverse — tells somebody who is resetting
	// because they were compromised that they are now safe.
	t.Run("a reset ends every session and personal credential, across organizations, and reports what it left", func(t *testing.T) {
		s := fresh(t)
		mustTenant(t, s, "acme", "Acme")
		mustTenant(t, s, "beta", "Beta")
		u := mustUser(t, s, "sub-pw", "dana@acme.com")
		other := mustUser(t, s, "sub-other", "sam@acme.com")
		mustMember(t, s, u.UserID, "acme", RoleOwner)
		mustMember(t, s, u.UserID, "beta", RoleMember)
		mustMember(t, s, other.UserID, "acme", RoleOwner)

		mustSession(t, s, "acme", u.UserID, "sess-acme")
		mustSession(t, s, "beta", u.UserID, "sess-beta")
		mustSession(t, s, "acme", other.UserID, "sess-other")
		mustCredential(t, s, "acme", u.UserID, "dana laptop")
		mustCredential(t, s, "beta", u.UserID, "dana ci-personal")
		ciAcme := mustCredential(t, s, "acme", "", "acme deploy key")
		mustCredential(t, s, "acme", other.UserID, "sam laptop")

		rev, err := s.RevokeEverythingFor(u.UserID, t0.Add(time.Minute))
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if rev.SessionsRevoked != 2 {
			t.Errorf("revoked %d sessions, want 2 (both organizations)", rev.SessionsRevoked)
		}
		if rev.CredentialsRevoked != 2 {
			t.Errorf("revoked %d personal credentials, want 2 (both organizations)", rev.CredentialsRevoked)
		}
		if len(rev.MachineCredentials) != 1 || rev.MachineCredentials[0].CredentialID != ciAcme.CredentialID {
			t.Fatalf("the machine credentials left running were not reported: %+v", rev.MachineCredentials)
		}
		// 🔴 Somebody else's session and credential are untouched. A revocation scoped by person rather
		// than by organization is easy to write as "everything in these organizations".
		if sess, err := s.ResolveSession(HashSecret("sess-other-token")); err != nil || !sess.Live(t0.Add(time.Hour).UnixMilli()) {
			t.Fatalf("another person's session was revoked: %+v / %v", sess, err)
		}
		creds, err := s.ListCredentials("acme")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, c := range creds {
			switch c.Label {
			case "sam laptop", "acme deploy key":
				if c.Revoked() {
					t.Errorf("%q was revoked and should not have been", c.Label)
				}
			case "dana laptop":
				if !c.Revoked() {
					t.Errorf("%q survived the reset", c.Label)
				}
			}
		}
	})
}

// ── fixtures ────────────────────────────────────────────────────────────────────────────────────────

func mustTenant(t *testing.T, s Store, id, name string) Tenant {
	t.Helper()
	out, err := s.CreateTenant(Tenant{TenantID: id, Name: name, CreatedAt: t0})
	if err != nil {
		t.Fatalf("fixture tenant %s: %v", id, err)
	}
	return out
}

func mustUser(t *testing.T, s Store, subject, email string) User {
	t.Helper()
	out, err := s.UpsertUser(User{Issuer: "https://idp.acme", Subject: subject, Email: email, CreatedAt: t0})
	if err != nil {
		t.Fatalf("fixture user %s: %v", subject, err)
	}
	return out
}

func mustMember(t *testing.T, s Store, userID, tenantID string, role Role) Membership {
	t.Helper()
	out, err := s.PutMembership(Membership{UserID: userID, TenantID: tenantID, Role: role, JoinedAt: t0})
	if err != nil {
		t.Fatalf("fixture membership %s/%s: %v", userID, tenantID, err)
	}
	return out
}

func mustCredential(t *testing.T, s Store, tenantID, userID, label string) Credential {
	t.Helper()
	secret, err := NewCredentialSecret()
	if err != nil {
		t.Fatalf("mint secret: %v", err)
	}
	out, err := s.CreateCredential(Credential{
		CredentialID: NewID("cred"), TenantID: tenantID, UserID: userID, Label: label,
		Role: RoleMember, Hash: HashSecret(secret), CreatedAt: t0,
	})
	if err != nil {
		t.Fatalf("fixture credential %s: %v", label, err)
	}
	return out
}

func mustSession(t *testing.T, s Store, tenantID, userID, sessionID string) Session {
	t.Helper()
	out, err := s.CreateSession(Session{
		TokenHash: HashSecret(sessionID + "-token"), SessionID: sessionID,
		TenantID: tenantID, UserID: userID,
		IssuedAt: t0.UnixMilli(), ExpiresAt: t0.Add(8 * time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("fixture session %s: %v", sessionID, err)
	}
	return out
}
