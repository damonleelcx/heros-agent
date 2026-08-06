package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/password"
	"github.com/heros-foreal/agentd/internal/signup"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// passwordauth.go is P28's HTTP surface: the routes by which a person obtains, uses and recovers their own
// credential, with no operator in the loop.
//
// # Seven routes, six of them unauthenticated, and that is the point
//
//	POST /api/v1/auth/password/signup   public · creates person + organization + owner + free account + password
//	POST /api/v1/auth/password/signin   public · the CLI and the console both call it; only the CLI asks for a credential
//	POST /api/v1/auth/password/forgot   public · always the same answer
//	POST /api/v1/auth/password/reset    public · spends a token, sets a password, revokes everything that person held
//	POST /api/v1/auth/password/verify   public · spends a token, stamps the address
//	POST /api/v1/auth/password/resend   public · always the same answer
//	POST /api/v1/auth/password/change     AUTHENTICATED · requires the current password
//
// "Unauthenticated endpoint" reads as a finding until somebody explains it, so: these are the routes a person
// uses BEFORE they have anything to authenticate with. That is what a front door is. What guards them is that
// four of the six mint or reveal nothing without a secret only the right person holds (a password, or a
// 256-bit token delivered to their address), and the two that reveal nothing at all — `forgot` and `resend` —
// answer identically for every input.
//
// # 🔴 The three enumeration surfaces are closed, in body AND on the clock
//
// Sign-in, sign-up and forgot-password all reveal whether an address is registered if written naïvely. All
// three answer identically here. Sign-in additionally runs a full argon2id verification against a decoy when
// no user exists, because the oracle carefully closed on the response body is otherwise wide open on the clock
// — an unknown address returning in microseconds and a known one in ~50ms is not a subtle difference and does
// not need a lab to measure. `password.VerifyDecoy` exists for that one branch, and it returns nothing so a
// caller cannot rebuild the difference in its control flow.
//
// The ONE deliberate exception is the account lock, and it is argued in ADR-012 Decision 4: it discloses
// existence only to somebody who has already made ten failed attempts, and hiding it costs a real person the
// only information that explains why their correct password is being refused.
//
// ⚠️ Not rate-limited here. Sign-in is bounded per ACCOUNT by the lockout, which is the bound that matters for
// guessing; what is unbounded is the number of DISTINCT addresses one caller can probe, and the number of mails
// `forgot` can cause to be sent to one address. The platform has no request-rate mechanism for this layer to
// hook into — the same gap `deviceauth.go` records — so it is written down rather than implied to be handled.

// Reason codes this surface adds. Closed set: the console switches on them.
const (
	// ReasonBadCredentials is the ONE code for unknown address and wrong password.
	ReasonBadCredentials  = "bad_credentials"
	ReasonAccountLocked   = "account_locked"
	ReasonWeakPassword    = "weak_password"
	ReasonLinkUnusable    = "link_unusable"
	ReasonEmailUnverified = "email_unverified"
	ReasonNoPasswordSet   = "no_password_set"
)

// badCredentials is the ONE sentence for every sign-in refusal that is not a lock.
const badCredentials = "that email and password did not match — check them, or reset your password"

// neutralAck is what `forgot`, `resend` and a duplicate `signup` all say. It is deliberately true regardless
// of whether anything was sent: "if there is an account" is the whole construction.
const neutralAck = "if that address has an account, we have sent it an email"

// mountPasswordAuth registers the seven routes.
//
// 🔴 UNEXPORTED and called from MountAccounts, for the reason `mountDeviceAuth` records: an exported `Mount*`
// announces an independently-mountable capability, and password identity is not one. Every route here writes
// a person, a membership or a credential, so a deployment with no account system would get seven routes that
// can only refuse.
func (s *Server) mountPasswordAuth() {
	s.Mux.HandleFunc("POST /api/v1/auth/password/signup", s.handlePasswordSignUp)
	s.Mux.HandleFunc("POST /api/v1/auth/password/signin", s.handlePasswordSignIn)
	s.Mux.HandleFunc("POST /api/v1/auth/password/forgot", s.handlePasswordForgot)
	s.Mux.HandleFunc("POST /api/v1/auth/password/reset", s.handlePasswordReset)
	s.Mux.HandleFunc("POST /api/v1/auth/password/verify", s.handlePasswordVerify)
	s.Mux.HandleFunc("POST /api/v1/auth/password/resend", s.handlePasswordResend)
	s.Mux.HandleFunc("POST /api/v1/auth/password/change", s.handlePasswordChange)
}

// mail returns the deployment's mailer, never nil.
//
// 🔴 A nil mailer would be a silent discard, which is the one behaviour `internal/mailer` exists to prevent —
// so the fallback is constructed here rather than left as a nil check at six call sites, any one of which
// could be written as `if m != nil { send }` and quietly drop the message.
func (s *Server) mail() mailer.Mailer {
	if s.accounts != nil {
		if m := s.accounts.Mailer(); m != nil {
			return m
		}
	}
	if s.fallbackMail == nil {
		s.fallbackMail = mailer.NewOperatorMailer(nil)
	}
	return s.fallbackMail
}

func (s *Server) consoleURL() string {
	if s.accounts != nil {
		if u := strings.TrimRight(strings.TrimSpace(s.accounts.ConsoleURL()), "/"); u != "" {
			return u
		}
	}
	return ""
}

// send delivers a message and never fails the act that produced it.
//
// The error is logged and dropped. A sign-up whose confirmation bounces is still a sign-up; rolling back an
// account because a mail server was down loses a customer for an outage that is ours (PRD FR17).
func (s *Server) send(ctx context.Context, to string, m mailer.Message) {
	m.To = to
	if err := s.mail().Send(ctx, m); err != nil {
		log.Printf("WARN mail: a %s message to %s could not be sent: %v (the action it belongs to stands; "+
			"the person can ask for it again)", m.Purpose, to, err)
	}
}

func decodeInto(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		refuse(w, http.StatusBadRequest, "", "the request is not valid: "+err.Error())
		return false
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Sign up
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// handlePasswordSignUp creates everything, or nothing.
//
// The atomicity is `signup.Service.Create`'s, which already writes the organization, the owner membership and
// the free account inside one transaction. The password joins it as the last step INSIDE the same call's
// success path — see the comment at the write for why that ordering is the honest compromise available here.
func (s *Server) handlePasswordSignUp(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeInto(w, r, &req) {
		return
	}
	email := tenancy.NormalizeEmail(req.Email)
	name := strings.TrimSpace(req.Name)

	// 🔴 The posture is checked FIRST, before anything about the address is looked up. An air-gapped install
	// must not become a way to ask "does this address have an account here" simply because sign-up is off.
	if !s.accounts.SelfServeEnabled() || s.accounts.SignUp() == nil {
		refuse(w, http.StatusForbidden, ReasonSelfServeDisabled,
			"this install does not offer sign-up — ask whoever runs it for an invitation")
		return
	}
	if name == "" {
		refuse(w, http.StatusBadRequest, "", "an organization needs a name")
		return
	}
	if !plausibleEmail(email) {
		refuse(w, http.StatusBadRequest, "", "that does not look like an email address")
		return
	}
	if err := password.CheckPolicy(req.Password, email); err != nil {
		refuse(w, http.StatusBadRequest, ReasonWeakPassword, err.Error())
		return
	}

	store := s.accounts.Store()
	now := s.accounts.Now()

	// An address that already has a password. 🔴 The response is INDISTINGUISHABLE from success, and the
	// information goes to the address instead — the one party entitled to it. Registration must not answer
	// "does this person have an account here".
	if existing, err := store.FindUserByEmail(tenancy.IssuerPassword, email); err == nil {
		if _, perr := store.GetPassword(existing.UserID); perr == nil {
			s.send(r.Context(), email, mailer.SignupAttempt(s.consoleURL(), s.consoleURL()+"/forgot-password"))
			writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "message": neutralAck})
			return
		}
		// The person exists with no password — a federated row cannot reach here (different issuer), so this
		// is an interrupted sign-up. Setting the password below completes it rather than refusing somebody
		// who is stuck in a state our own crash created.
	}

	encoded, err := password.Hash(req.Password)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the account could not be created")
		return
	}

	res, err := s.accounts.SignUp().Create(r.Context(), signup.Request{
		Name: name,
		// 🔴 The address IS the subject on this seam. There is no identity provider and no `sub` to be stable
		// instead — see tenancy/passwordidentity.go and ADR-012 Decision 5.
		Issuer:  tenancy.IssuerPassword,
		Subject: tenancy.PasswordSubject(email),
		Email:   email,
	})
	if err != nil {
		switch {
		case errors.Is(err, signup.ErrNameRequired):
			refuse(w, http.StatusBadRequest, "", "an organization needs a name")
		case errors.Is(err, signup.ErrDisabled):
			refuse(w, http.StatusForbidden, ReasonSelfServeDisabled,
				"this install does not offer sign-up — ask whoever runs it for an invitation")
		case errors.Is(err, signup.ErrNoFreePlan):
			refuse(w, http.StatusServiceUnavailable, "", "this deployment has no free plan to start an account on")
		default:
			refuse(w, http.StatusInternalServerError, "", "the account could not be created")
		}
		return
	}

	// ⚠️ The password is written AFTER the organization transaction commits, and that is a stated compromise
	// rather than an oversight. `tenancy.OrganizationCreator` lends its transaction through a closure that
	// runs before the user id exists — the account row can be written inside it because sign-up mints the
	// TENANT id itself, but the USER id is minted by the store during the same call, so a password row
	// keyed on it cannot be written until the call returns.
	//
	// The failure this leaves is bounded and recoverable: an organization exists whose owner has no password.
	// That person's next sign-up attempt completes it (see the branch above), and their next `forgot` is
	// refused rather than mis-sent. The alternative — widening the creator contract to hand the user id into
	// the closure — is a change to a P27 interface with four implementations for a window measured in
	// microseconds, and is written down here as the follow-up it is rather than done silently.
	if _, err := store.SetPassword(res.Organization.Owner.UserID, encoded, now); err != nil {
		log.Printf("WARN password: organization %s was created but its owner's password could not be stored: %v",
			res.Organization.Tenant.TenantID, err)
		refuse(w, http.StatusInternalServerError, "", "the account was created but the password could not be stored — "+
			"use 'forgot password' to set one")
		return
	}

	s.mintAndSendVerification(r.Context(), store, res.Organization.Owner, email, now)

	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":                true,
		"message":           neutralAck,
		"tenant_id":         res.Organization.Tenant.TenantID,
		"organization_name": res.Organization.Tenant.Name,
		"user_id":           res.Organization.Owner.UserID,
		"email":             email,
		"email_verified":    false,
	})
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Sign in
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// handlePasswordSignIn verifies an address and a password.
//
// # Two callers, one route, one difference
//
// The CONSOLE calls it and takes only the principal — it then issues its own session exactly as it does for
// every other seam, so nothing above ADR-008's seam moves. The CLI calls it with `device_label` and takes a
// PERSONAL CREDENTIAL, because a terminal needs something durable and revocable.
//
// The distinction is the presence of `device_label` rather than a mode flag: the label is what the credential
// is called on the revocation screen, so a caller that wants a credential necessarily has one, and a caller
// that does not cannot accidentally mint one.
func (s *Server) handlePasswordSignIn(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		// DeviceLabel, when present, asks for a personal credential and names it. Absent means "just tell me
		// who this is" — the console's case.
		DeviceLabel string `json:"device_label"`
		// OrganizationID selects among several memberships. Absent picks the earliest joined, and the full
		// list comes back either way so a caller can offer a switch.
		OrganizationID string `json:"organization_id"`
	}
	if !decodeInto(w, r, &req) {
		return
	}
	email := tenancy.NormalizeEmail(req.Email)
	store := s.accounts.Store()
	now := s.accounts.Now()

	user, err := store.FindUserByEmail(tenancy.IssuerPassword, email)
	if err != nil {
		// 🔴 The decoy. A full argon2id verification on the "no such address" branch, so this refusal costs
		// what a real one costs. Without it the enumeration oracle closed on the body below is open on the
		// clock. `VerifyDecoy` returns nothing so this branch cannot differ from the other in shape either.
		password.VerifyDecoy(req.Password)
		logSecurity("password_signin_unknown_address", "")
		refuse(w, http.StatusUnauthorized, ReasonBadCredentials, badCredentials)
		return
	}
	rec, err := store.GetPassword(user.UserID)
	if err != nil {
		// A person who exists and authenticates another way, or whose sign-up did not finish. Same decoy,
		// same answer: which of the two it is helps nobody signing in and helps an attacker enumerate.
		password.VerifyDecoy(req.Password)
		logSecurity("password_signin_no_password", "")
		refuse(w, http.StatusUnauthorized, ReasonBadCredentials, badCredentials)
		return
	}
	if rec.Locked(now) {
		// The ONE distinguishable state. ADR-012 Decision 4: it discloses existence only to somebody who has
		// already spent ten guesses, and hiding it costs a real person the only explanation for why their
		// correct password is being refused.
		refuse(w, http.StatusTooManyRequests, ReasonAccountLocked, lockMessage(rec.LockRemaining(now)))
		return
	}

	ok, rehash, verr := password.Verify(rec.Encoded, req.Password)
	if verr != nil {
		// A stored value this build cannot parse. An operator problem, not a user one — and refused rather
		// than treated as a mismatch, so it appears in the log as what it is.
		log.Printf("WARN password: the stored value for user %s is not verifiable: %v", user.UserID, verr)
		refuse(w, http.StatusInternalServerError, "", "that account cannot be verified — contact whoever runs this install")
		return
	}
	if !ok {
		next, ferr := store.RecordPasswordFailure(user.UserID, now, tenancy.DefaultLockout)
		if ferr != nil {
			log.Printf("WARN password: a failed attempt for %s could not be recorded: %v — the lockout is not "+
				"counting this attempt", user.UserID, ferr)
		} else if next.Locked(now) {
			refuse(w, http.StatusTooManyRequests, ReasonAccountLocked, lockMessage(next.LockRemaining(now)))
			return
		}
		refuse(w, http.StatusUnauthorized, ReasonBadCredentials, badCredentials)
		return
	}

	if err := store.ClearPasswordFailures(user.UserID); err != nil {
		log.Printf("WARN password: the failure counters for %s could not be cleared: %v", user.UserID, err)
	}
	if rehash {
		// Raising the cost is a deploy, not a migration: the encoding carries its own parameters, so a
		// successful sign-in against stale ones re-hashes here. A failure is logged and ignored — the person
		// is correctly signed in either way, and refusing them over a housekeeping write would be absurd.
		if enc, herr := password.Hash(req.Password); herr == nil {
			if _, serr := store.SetPassword(user.UserID, enc, now); serr != nil {
				log.Printf("WARN password: could not upgrade the stored parameters for %s: %v", user.UserID, serr)
			}
		}
	}

	orgs, member, ok := s.resolveMembership(store, user.UserID, req.OrganizationID)
	if !ok {
		// Authenticated, and a member of nothing. Distinguishable from a bad password on purpose: the
		// password was right, and telling somebody "wrong password" here would send them round a reset loop
		// that cannot fix it.
		refuse(w, http.StatusForbidden, ReasonNotAMember,
			"you are not an active member of any organization on this install — ask an owner to invite you")
		return
	}
	tenant, terr := store.GetTenant(member.TenantID)
	if terr != nil || tenant.Status.Suspended() {
		refuse(w, http.StatusForbidden, ReasonOrgSuspended, "that organization is suspended")
		return
	}

	body := map[string]any{
		"tenant_id":         member.TenantID,
		"organization_id":   member.TenantID,
		"organization_name": tenant.Name,
		"user_id":           user.UserID,
		"email":             user.Email,
		"email_verified":    user.EmailVerified(),
		"role":              string(member.Role),
		"organizations":     orgs,
	}

	if label := strings.TrimSpace(req.DeviceLabel); label != "" {
		secret, cerr := tenancy.NewCredentialSecret()
		if cerr != nil {
			refuse(w, http.StatusInternalServerError, "", "a credential could not be minted")
			return
		}
		cred, cerr := store.CreateCredential(tenancy.Credential{
			CredentialID: tenancy.NewID("cred"),
			TenantID:     member.TenantID,
			// 🔴 PERSONAL. This is what makes "remove a member and their access ends" true in a terminal —
			// the property that was simply false while the only way in was a shared assertion string.
			UserID:    user.UserID,
			Label:     label,
			Role:      member.Role,
			Hash:      tenancy.HashSecret(secret),
			CreatedAt: now,
		})
		if cerr != nil {
			refuse(w, http.StatusInternalServerError, "", "a credential could not be created")
			return
		}
		body["credential"] = map[string]any{
			// The plaintext leaves here once and is never readable again — the store holds only the hash.
			"token":         secret,
			"credential_id": cred.CredentialID,
			"kind":          cred.Kind(),
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func lockMessage(d time.Duration) string {
	mins := int(d / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return "too many attempts — try again in " + itoa(mins) + " minute(s), or reset your password to sign in sooner"
}

// resolveMembership picks the organization a sign-in lands in, and lists them all.
//
// Absent a requested id it takes the EARLIEST joined active membership — deterministic, so the same person
// lands in the same place every time, which "the first one the map yielded" would not be.
func (s *Server) resolveMembership(store tenancy.Store, userID, requested string) ([]map[string]any, tenancy.Membership, bool) {
	all, err := store.ListMembershipsFor(userID)
	if err != nil {
		return nil, tenancy.Membership{}, false
	}
	active := make([]tenancy.Membership, 0, len(all))
	for _, m := range all {
		if m.Active() {
			active = append(active, m)
		}
	}
	if len(active) == 0 {
		return nil, tenancy.Membership{}, false
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].JoinedAt.Equal(active[j].JoinedAt) {
			return active[i].TenantID < active[j].TenantID
		}
		return active[i].JoinedAt.Before(active[j].JoinedAt)
	})
	chosen := active[0]
	if requested = strings.TrimSpace(requested); requested != "" {
		found := false
		for _, m := range active {
			if m.TenantID == requested {
				chosen, found = m, true
				break
			}
		}
		if !found {
			// A requested organization they are not an active member of. Refused rather than silently
			// redirected to another one — landing somewhere other than where you asked is worse than being
			// told no.
			return nil, tenancy.Membership{}, false
		}
	}
	orgs := make([]map[string]any, 0, len(active))
	for _, m := range active {
		entry := map[string]any{"tenant_id": m.TenantID, "role": string(m.Role)}
		if t, terr := store.GetTenant(m.TenantID); terr == nil {
			entry["name"] = t.Name
		}
		orgs = append(orgs, entry)
	}
	return orgs, chosen, true
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Forgot / reset
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// handlePasswordForgot answers identically for every address.
//
// 🔴 Every branch below ends at the same `neutralAck` with the same status. There is no error path a caller
// can reach that distinguishes a registered address from an unregistered one — including a malformed one,
// which is accepted and ignored rather than refused, because "that is not a valid address" is itself a
// distinguishable answer.
func (s *Server) handlePasswordForgot(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if !decodeInto(w, r, &req) {
		return
	}
	email := tenancy.NormalizeEmail(req.Email)
	store, now := s.accounts.Store(), s.accounts.Now()

	if user, err := store.FindUserByEmail(tenancy.IssuerPassword, email); err == nil {
		secret, token, terr := s.mintToken(store, user.UserID, email, tenancy.TokenResetPassword, tenancy.ResetTokenTTL, now)
		if terr != nil {
			log.Printf("WARN password: a reset token for %s could not be minted: %v", user.UserID, terr)
		} else {
			_ = token
			s.send(r.Context(), email, mailer.ResetPassword(s.consoleURL(),
				s.consoleURL()+"/reset-password?t="+secret, tenancy.ResetTokenTTL))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": neutralAck})
}

// handlePasswordReset spends a token, sets the password, and ends everything that person held.
func (s *Server) handlePasswordReset(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeInto(w, r, &req) {
		return
	}
	store, now := s.accounts.Store(), s.accounts.Now()

	// 🔴 The token is spent BEFORE the new password is validated. A caller who submits a weak password would
	// otherwise burn nothing and could keep trying — which is fine — but the ORDER that matters is that no
	// path validates first and consumes later, because that is the shape in which two concurrent requests
	// both pass the check. The cost is that a rejected weak password needs a new link, and the copy says so.
	tok, err := store.ConsumeIdentityToken(tenancy.HashSecret(strings.TrimSpace(req.Token)),
		tenancy.TokenResetPassword, now)
	if err != nil {
		refuse(w, http.StatusBadRequest, ReasonLinkUnusable,
			"this link is no longer usable — request a new one")
		return
	}
	if err := password.CheckPolicy(req.Password, tok.Email); err != nil {
		refuse(w, http.StatusBadRequest, ReasonWeakPassword, err.Error()+" (this link has been used — request another)")
		return
	}
	encoded, err := password.Hash(req.Password)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the password could not be stored")
		return
	}
	if _, err := store.SetPassword(tok.UserID, encoded, now); err != nil {
		refuse(w, http.StatusInternalServerError, "", "the password could not be stored")
		return
	}

	// 🔴 Everything that person held ends here. The common reason to reset is that something was
	// compromised, and a reset that leaves the attacker's session live has done nothing.
	rev, err := store.RevokeEverythingFor(tok.UserID, now)
	if err != nil {
		log.Printf("WARN password: the password for %s was reset but their sessions and credentials could NOT "+
			"be revoked: %v — this reset did not end the access it promised to end", tok.UserID, err)
		refuse(w, http.StatusInternalServerError, "", "the password was changed but existing sessions could not be "+
			"ended — tell whoever runs this install before signing in")
		return
	}
	// A reset also proves control of the address, so it verifies it. Somebody who received the link at that
	// address has demonstrated exactly what the confirmation link demonstrates.
	if _, err := store.MarkEmailVerified(tok.UserID, now); err != nil {
		log.Printf("WARN password: %s reset their password but the address could not be marked verified: %v", tok.UserID, err)
	}
	log.Printf("password reset: user=%s sessions_revoked=%d credentials_revoked=%d machine_credentials_untouched=%d",
		tok.UserID, rev.SessionsRevoked, rev.CredentialsRevoked, len(rev.MachineCredentials))

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"sessions_revoked":    rev.SessionsRevoked,
		"credentials_revoked": rev.CredentialsRevoked,
		// 🔴 What was NOT revoked, by name. A screen that lists what it ended and hides what it left running
		// tells somebody who is resetting because they were compromised that they are now safe.
		"machine_credentials_untouched": machineCredentialNames(rev.MachineCredentials),
		"email":                         tok.Email,
	})
}

func machineCredentialNames(creds []tenancy.Credential) []map[string]any {
	out := make([]map[string]any, 0, len(creds))
	for _, c := range creds {
		label := c.Label
		if strings.TrimSpace(label) == "" {
			label = c.CredentialID
		}
		out = append(out, map[string]any{"credential_id": c.CredentialID, "label": label})
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Confirm an address
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *Server) handlePasswordVerify(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decodeInto(w, r, &req) {
		return
	}
	store, now := s.accounts.Store(), s.accounts.Now()
	tok, err := store.ConsumeIdentityToken(tenancy.HashSecret(strings.TrimSpace(req.Token)),
		tenancy.TokenVerifyEmail, now)
	if err != nil {
		refuse(w, http.StatusBadRequest, ReasonLinkUnusable, "this link is no longer usable — request a new one")
		return
	}
	user, err := store.MarkEmailVerified(tok.UserID, now)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the address could not be confirmed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user_id": user.UserID, "email": user.Email, "email_verified": true})
}

// handlePasswordResend answers identically for every address, exactly as `forgot` does.
func (s *Server) handlePasswordResend(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if !decodeInto(w, r, &req) {
		return
	}
	email := tenancy.NormalizeEmail(req.Email)
	store, now := s.accounts.Store(), s.accounts.Now()
	if user, err := store.FindUserByEmail(tenancy.IssuerPassword, email); err == nil && !user.EmailVerified() {
		s.mintAndSendVerification(r.Context(), store, user, email, now)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": neutralAck})
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Change, while signed in
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// handlePasswordChange requires the current password and ends every OTHER session.
func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	if p.UserID == "" {
		refuse(w, http.StatusForbidden, ReasonNotAMember,
			"changing a password needs a signed-in person; a machine credential names nobody")
		return
	}
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeInto(w, r, &req) {
		return
	}
	now := s.accounts.Now()
	rec, err := store.GetPassword(p.UserID)
	if err != nil {
		refuse(w, http.StatusBadRequest, ReasonNoPasswordSet,
			"this account signs in another way and has no password to change")
		return
	}
	if rec.Locked(now) {
		refuse(w, http.StatusTooManyRequests, ReasonAccountLocked, lockMessage(rec.LockRemaining(now)))
		return
	}
	valid, _, verr := password.Verify(rec.Encoded, req.Current)
	if verr != nil || !valid {
		// A wrong CURRENT password counts toward the lockout too. Otherwise an authenticated session is an
		// unbounded oracle for the password it already half-holds.
		if _, ferr := store.RecordPasswordFailure(p.UserID, now, tenancy.DefaultLockout); ferr != nil {
			log.Printf("WARN password: a failed change attempt for %s could not be recorded: %v", p.UserID, ferr)
		}
		refuse(w, http.StatusUnauthorized, ReasonBadCredentials, "that is not your current password")
		return
	}
	user, uerr := store.GetUser(p.UserID)
	emailForPolicy := ""
	if uerr == nil {
		emailForPolicy = user.Email
	}
	if err := password.CheckPolicy(req.New, emailForPolicy); err != nil {
		refuse(w, http.StatusBadRequest, ReasonWeakPassword, err.Error())
		return
	}
	encoded, err := password.Hash(req.New)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the password could not be stored")
		return
	}
	if _, err := store.SetPassword(p.UserID, encoded, now); err != nil {
		refuse(w, http.StatusInternalServerError, "", "the password could not be stored")
		return
	}

	// Every OTHER session ends; this one does not. A person who changes their password from a settings page
	// and is immediately signed out of it learns nothing about whether it worked.
	revoked := 0
	sessions, serr := store.ListSessionsFor(p.UserID, p.TenantID)
	if serr != nil {
		log.Printf("WARN password: %s changed their password but their other sessions could not be listed: %v", p.UserID, serr)
	}
	for _, sess := range sessions {
		if sess.SessionID == p.APIKeyID || !sess.Live(now.UnixMilli()) {
			continue
		}
		if err := store.RevokeSession(sess.TokenHash, now.UnixMilli()); err != nil {
			log.Printf("WARN password: a session of %s could not be revoked after a password change: %v", p.UserID, err)
			continue
		}
		revoked++
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "other_sessions_revoked": revoked})
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Shared
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// mintToken mints a plaintext, stores its HASH, and returns the plaintext for the one message that carries it.
func (s *Server) mintToken(store tenancy.Store, userID, email string, purpose tenancy.TokenPurpose,
	ttl time.Duration, now time.Time) (string, tenancy.IdentityToken, error) {
	secret, err := tenancy.NewCredentialSecret()
	if err != nil {
		return "", tenancy.IdentityToken{}, err
	}
	tok, err := store.MintIdentityToken(tenancy.IdentityToken{
		TokenHash: tenancy.HashSecret(secret),
		UserID:    userID,
		Purpose:   purpose,
		Email:     email,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	})
	if err != nil {
		return "", tenancy.IdentityToken{}, err
	}
	return secret, tok, nil
}

func (s *Server) mintAndSendVerification(ctx context.Context, store tenancy.Store, user tenancy.User, email string, now time.Time) {
	secret, _, err := s.mintToken(store, user.UserID, email, tenancy.TokenVerifyEmail, tenancy.VerifyTokenTTL, now)
	if err != nil {
		log.Printf("WARN password: a confirmation token for %s could not be minted: %v", user.UserID, err)
		return
	}
	s.send(ctx, email, mailer.VerifyEmail(s.consoleURL(), s.consoleURL()+"/verify-email?t="+secret, tenancy.VerifyTokenTTL))
}

// confirmedAddress gates the two actions an unconfirmed address may not take. It writes the refusal and
// returns false when the caller must stop.
//
// # Why it PASSES when it cannot tell
//
// A principal with no person (a machine credential), a person who cannot be read, or a person who signs in
// through a seam that never had an address to confirm — all pass. 🔴 That is deliberate and it is the
// difference between a gate and an outage: this check exists to stop an *unverified password account* from
// spending, not to become a new way for every federated and machine caller to be refused because an identity
// read failed. The federated seams prove an address through their identity provider, which is a stronger
// proof than a link we mailed, and a machine credential has no address at all.
//
// The consequence is stated rather than hidden: if the identity store is unreachable, this gate is open.
// Everything it guards is guarded by something else too — an invitation needs an inviting role and a seat, a
// plan change needs a payment method — so an open gate here is a missing confirmation, not a missing
// authorization.
func (s *Server) confirmedAddress(w http.ResponseWriter, store tenancy.Store, userID, action string) bool {
	if strings.TrimSpace(userID) == "" {
		return true
	}
	user, err := store.GetUser(userID)
	if err != nil {
		log.Printf("WARN password: could not read %s to check whether their address is confirmed (%v) — "+
			"%q is being allowed", userID, err, action)
		return true
	}
	if user.Issuer != tenancy.IssuerPassword || user.EmailVerified() {
		return true
	}
	refuse(w, http.StatusForbidden, ReasonEmailUnverified,
		"confirm your email before "+action+" — we sent a link to "+user.Email+", and you can ask for another")
	return false
}

// plausibleEmail is a SHAPE check and nothing more.
//
// 🔴 It deliberately does not try to validate an address. RFC 5322 permits things no validator gets right, and
// every over-strict regex in this industry has rejected somebody's real address. What actually proves an
// address is the confirmation link; this only rejects input that cannot be one, so the sign-up form can say
// "that does not look like an address" instead of sending mail into nowhere.
func plausibleEmail(e string) bool {
	at := strings.LastIndex(e, "@")
	if at <= 0 || at == len(e)-1 {
		return false
	}
	domain := e[at+1:]
	return strings.Contains(domain, ".") && !strings.ContainsAny(e, " \t\r\n,;")
}
