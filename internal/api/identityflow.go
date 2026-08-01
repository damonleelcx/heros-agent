package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// p22_adminflow.go is the operator console's federated sign-in surface (P22 follow-on).
//
// Two routes, and the split between them is the same one `oidcflow.go` argues for: the BFF owns the
// BROWSER (it holds `state`, the PKCE verifier and the nonce, because it is what set the cookie), and
// the platform owns the CREDENTIAL (the client secret, resolved through the Secrets seam at the moment
// of use). Neither can complete a sign-in alone.

// handleIdPStart returns the authorization URL for one browser flow.
//
// It is open — like `/admin/api/healthz` and `/admin/api/readyz` — because it must be reachable before
// anybody has a session, which is what signing in means. It reveals nothing a browser could not read
// off the redirect a moment later: the issuer's own authorization endpoint and a public client id.
//
// It does NOT mint `state`, the nonce, or the PKCE verifier. Those belong to the process that set the
// browser's cookie; minting them here would mean a server-side flow store keyed by something the
// browser sends, which is the BFF's store one hop further from the browser.
func (a *AdminAPI) handleIdPStart(w http.ResponseWriter, r *http.Request) {
	if a.deps.IdP == nil {
		writeAdminError(w, http.StatusNotFound, "not_federated",
			"this deployment has no federated admin identity provider — sign in through the configured mode", nil)
		return
	}
	var body struct {
		State         string `json:"state"`
		Nonce         string `json:"nonce"`
		CodeChallenge string `json:"code_challenge"`
		RedirectURI   string `json:"redirect_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	url, err := a.deps.IdP.AuthorizationURL(r.Context(), adminidentity.AuthorizationRequest{
		State: body.State, Nonce: body.Nonce, CodeChallenge: body.CodeChallenge, RedirectURI: body.RedirectURI,
	})
	if err != nil {
		// An off-allowlist redirect and an unreachable IdP are different pages at 3am, so they get
		// different statuses — but neither echoes anything the caller supplied.
		status := http.StatusBadRequest
		code := "redirect_not_allowed"
		if errors.Is(err, adminidentity.ErrIdPUnreachable) {
			status, code = http.StatusServiceUnavailable, "idp_unreachable"
		}
		writeAdminError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authorization_url": url})
}

// handleMFAChallenge mints a single-use WebAuthn challenge for one login attempt.
//
// Open, like `/admin/api/idp/start`: it must be reachable before anybody has a session, which is what
// signing in means. It gives away nothing — a random 32 bytes the platform will accept exactly once,
// which is worth nothing to anyone who cannot also produce a signature over it from an enrolled
// credential.
func (a *AdminAPI) handleMFAChallenge(w http.ResponseWriter, _ *http.Request) {
	if a.deps.Challenges == nil {
		writeAdminError(w, http.StatusNotFound, "not_mounted", "this deployment mints no MFA challenges", nil)
		return
	}
	id, value, err := a.deps.Challenges.Mint()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": id,
		// base64url so it round-trips into `navigator.credentials.get()` unchanged.
		"challenge": base64.RawURLEncoding.EncodeToString(value),
	})
}

// handleEnrollFactor registers a platform-verified second factor for an admin principal.
//
// # Why enrolment is gated on role.grant rather than on "it's my own account"
//
// Self-service enrolment is the obvious design and it is wrong here. An attacker who has obtained ONE
// authenticated session — a stolen laptop mid-shift, a session fixation, an insider — would use it to
// enrol their own factor and convert a temporary hold into permanent access. `adminidentity` already
// treats enrolment as "a deliberate act by a Superadmin", and `role.grant` is the capability that
// already means "can change who can do what", so this reuses it rather than inventing a second answer
// to the same question.
//
// The consequence is honest and worth stating: an operator cannot bootstrap their own first factor.
// That is a two-person operation by design on the surface that can halt the fleet.
func (a *AdminAPI) handleEnrollFactor(w http.ResponseWriter, r *http.Request) {
	if a.deps.Factors == nil {
		writeAdminError(w, http.StatusNotFound, "not_mounted", "this deployment has no MFA enrolment directory", nil)
		return
	}
	sess, err := adminidentity.RequireSession(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "no_session", err.Error(), nil)
		return
	}
	var body struct {
		AdminID string `json:"admin_id"`
		Kind    string `json:"kind"`
		// WebAuthn: the credential id and the SPKI public key a browser returns from
		// `PublicKeyCredential.getPublicKey()`. Base64url. No attestation is parsed or trusted.
		CredentialID string `json:"credential_id"`
		PublicKey    string `json:"public_key_spki"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	target := strings.TrimSpace(body.AdminID)
	// Authorized AFTER the decode so the decision names WHO was being enrolled. A denial that cannot
	// say its target is a denial nobody can audit.
	if decision := a.deps.Gate.Authorize(sess.AdminID, adminrbac.CapRoleGrant, target); !decision.Allowed {
		writeAdminError(w, http.StatusForbidden, "forbidden", decision.Reason, nil)
		return
	}
	factor := adminidentity.EnrolledFactor{AdminID: target, Kind: strings.TrimSpace(body.Kind)}
	switch factor.Kind {
	case adminidentity.FactorWebAuthn:
		credID, err1 := base64.RawURLEncoding.DecodeString(body.CredentialID)
		spki, err2 := base64.RawURLEncoding.DecodeString(body.PublicKey)
		if err1 != nil || err2 != nil {
			writeAdminError(w, http.StatusBadRequest, "bad_request", "credential id and public key are base64url", nil)
			return
		}
		factor.CredentialID, factor.PublicKeySPKI = credID, spki
	case adminidentity.FactorTOTP:
		// The SEED is not accepted here and never will be. It is provisioned into the secrets manager
		// under its reserved logical name; this route records only that the principal HAS one. A route
		// that accepted a seed would be an API endpoint that writes a credential, and the request body
		// would be a credential in a log the moment anybody enabled request logging.
		factor.SecretName = adminidentity.TOTPSeedName(factor.AdminID)
	default:
		writeAdminError(w, http.StatusBadRequest, "bad_request",
			fmt.Sprintf("kind must be %q or %q", adminidentity.FactorWebAuthn, adminidentity.FactorTOTP), nil)
		return
	}
	if err := a.deps.Factors.Enroll(factor); err != nil {
		writeAdminError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"admin_id": factor.AdminID, "kind": factor.Kind, "enrolled_by": sess.AdminID,
	})
}

// decodeWebAuthn turns the base64url fields the browser sends into the raw bytes the verifier signs
// over. One place, because "which encoding was this in" re-derived at the verification step is how a
// signature check ends up covering the wrong bytes.
func decodeWebAuthn(credentialID, authenticatorData, clientDataJSON, signature string) (*adminidentity.WebAuthnAssertion, error) {
	fields := map[string]string{
		"credential_id": credentialID, "authenticator_data": authenticatorData,
		"client_data_json": clientDataJSON, "signature": signature,
	}
	decoded := map[string][]byte{}
	for name, value := range fields {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
		if err != nil || len(raw) == 0 {
			return nil, fmt.Errorf("webauthn %s must be non-empty base64url", name)
		}
		decoded[name] = raw
	}
	return &adminidentity.WebAuthnAssertion{
		CredentialID:      decoded["credential_id"],
		AuthenticatorData: decoded["authenticator_data"],
		ClientDataJSON:    decoded["client_data_json"],
		Signature:         decoded["signature"],
	}, nil
}
