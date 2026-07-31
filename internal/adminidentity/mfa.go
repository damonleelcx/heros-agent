package adminidentity

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // RFC 6238 specifies HMAC-SHA1 for TOTP; the digest is not a content hash
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// mfa.go is the PLATFORM-VERIFIED second factor (P22 task 6.2, Decision 5, NFR8).
//
// # The distinction this whole file exists to make
//
// `authn.go` already refuses a session when the IdP's assertion carries no MFA evidence. That is a
// real check and it is not enough, and the reason is written in `authn.go` itself: *"the IdP requires
// MFA" is a configuration claim about a system this code does not control, and a configuration claim
// is not an invariant.* If the admin IdP's MFA policy is misconfigured — a policy edited by somebody
// else, in a console we do not own, on a Tuesday — the IdP will happily assert a first-factor-only
// login, and a platform that trusts the claim will issue an operator session for it.
//
// So P22 makes the second factor something the PLATFORM verifies, against material the PLATFORM
// enrolled: a WebAuthn assertion signed by a credential we registered, or a TOTP code derived from a
// seed we generated. A misconfigured IdP MFA policy then still results in denial on the surface that
// can halt the autonomous fleet, which is the direction a mistake has to fail in here.
//
// # Why WebAuthn is preferred and TOTP is the fallback
//
// WebAuthn is origin-bound: the authenticator signs over the ORIGIN the browser was actually on, so a
// phishing site that relays a login cannot obtain a usable signature. TOTP is a shared secret and a
// six-digit code, and a convincing enough page gets both. That is not a small difference on an
// operator surface — it is the difference between "an operator was phished" being survivable and
// being total. TOTP is here because hardware keys are not universal in every operations team, not
// because the two are equivalent, and `Describe` on a session records WHICH was used so an auditor can
// tell a hardware-key login from a one-time-code login.
//
// # What is deliberately not here
//
// WebAuthn ATTESTATION. This package verifies an assertion; it does not adjudicate which authenticator
// models are acceptable. Enrollment stores the credential's public key in SPKI form — which is exactly
// what a browser hands the console from `PublicKeyCredential.getPublicKey()` — so no CBOR parser is
// needed on the verification path and no attestation statement is trusted. Saying so is the point: an
// attestation check that "sort of" works is worse than none, because it is claimed.

var (
	// ErrFactorInvalid means the presented factor did not verify. One error for a wrong code, a wrong
	// signature, a stale timestamp and a replayed one, so probing cannot tell them apart.
	ErrFactorInvalid = errors.New("adminidentity: the second factor did not verify")
	// ErrFactorNotEnrolled means the principal has no factor of the kind presented.
	ErrFactorNotEnrolled = errors.New("adminidentity: no such second factor is enrolled for this principal")
)

// FactorKind names a second-factor mechanism. The values are the strings recorded on a session.
const (
	// FactorWebAuthn is the preferred factor: origin-bound, phishing-resistant.
	FactorWebAuthn = "webauthn"
	// FactorTOTP is the fallback: a shared seed and a six-digit code.
	FactorTOTP = "totp"
)

// PresentedFactor is what the OPERATOR sends the platform, alongside their SSO assertion.
//
// Exactly one of the two is populated. Both empty is the `ErrMFARequired` case — the FR1 denial that
// P22 makes unconditional for a real IdP.
type PresentedFactor struct {
	// TOTP is a six-digit code.
	TOTP string `json:"-"`
	// WebAuthn is a signed assertion from a registered credential.
	WebAuthn *WebAuthnAssertion `json:"-"`
}

// Present reports whether any factor was supplied at all.
func (f PresentedFactor) Present() bool {
	return strings.TrimSpace(f.TOTP) != "" || f.WebAuthn != nil
}

// WebAuthnAssertion is a `navigator.credentials.get()` result, as the console forwards it.
//
// Raw bytes, already base64url-decoded by the transport layer, because re-deriving "which encoding was
// this in" at the verification step is how a signature check ends up covering the wrong bytes.
type WebAuthnAssertion struct {
	// CredentialID identifies which enrolled credential signed. Raw bytes.
	CredentialID []byte
	// AuthenticatorData is the authenticator's own signed statement: RP-ID hash, flags, sign count.
	AuthenticatorData []byte
	// ClientDataJSON is the browser's statement: type, challenge, origin.
	ClientDataJSON []byte
	// Signature is over `AuthenticatorData || SHA256(ClientDataJSON)`.
	Signature []byte
}

// EnrolledFactor is one registered second factor for one admin principal.
type EnrolledFactor struct {
	AdminID string
	Kind    string
	// CredentialID is the WebAuthn credential handle. Empty for TOTP.
	CredentialID []byte
	// PublicKeySPKI is the WebAuthn credential's public key in SPKI DER — exactly what a browser
	// returns from `PublicKeyCredential.getPublicKey()`. Empty for TOTP.
	PublicKeySPKI []byte
	// SecretName is the reserved logical name under which the secrets manager holds this factor's TOTP
	// seed. The SEED ITSELF IS NEVER STORED HERE: this record is an index, and a directory record that
	// carried a seed would be a credential store with an ordinary backup policy.
	SecretName string
	// SignCount is the authenticator's last-seen counter, for clone detection.
	SignCount  uint32
	EnrolledAt time.Time
}

// FactorStore is the platform's enrollment directory.
//
// Small on purpose, exactly like `PrincipalStore`: enrollment is a fact about a principal, and putting
// anything about CAPABILITY here would make it a second, drifting place where authority is decided.
type FactorStore struct {
	mu sync.RWMutex
	// byAdmin holds every factor a principal has enrolled, newest last.
	byAdmin map[string][]EnrolledFactor
}

// NewFactorStore builds an empty enrollment directory.
func NewFactorStore() *FactorStore { return &FactorStore{byAdmin: map[string][]EnrolledFactor{}} }

// Enroll registers a factor. A principal may hold several — a hardware key and a TOTP fallback.
func (s *FactorStore) Enroll(f EnrolledFactor) error {
	if strings.TrimSpace(f.AdminID) == "" {
		return errors.New("adminidentity: an enrolled factor needs an admin_id")
	}
	switch f.Kind {
	case FactorWebAuthn:
		if len(f.CredentialID) == 0 || len(f.PublicKeySPKI) == 0 {
			return errors.New("adminidentity: a WebAuthn factor needs a credential id and a public key")
		}
	case FactorTOTP:
		if strings.TrimSpace(f.SecretName) == "" {
			return errors.New("adminidentity: a TOTP factor needs the logical name its seed is held under — the seed itself is never stored in the directory")
		}
	default:
		return fmt.Errorf("adminidentity: unknown factor kind %q", f.Kind)
	}
	if f.EnrolledAt.IsZero() {
		f.EnrolledAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byAdmin[f.AdminID] = append(s.byAdmin[f.AdminID], f)
	return nil
}

// For returns every factor a principal has enrolled.
func (s *FactorStore) For(adminID string) []EnrolledFactor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EnrolledFactor, len(s.byAdmin[adminID]))
	copy(out, s.byAdmin[adminID])
	return out
}

// recordSignCount advances a WebAuthn credential's counter after a successful verification.
func (s *FactorStore) recordSignCount(adminID string, credentialID []byte, count uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.byAdmin[adminID] {
		if f.Kind == FactorWebAuthn && subtle.ConstantTimeCompare(f.CredentialID, credentialID) == 1 {
			s.byAdmin[adminID][i].SignCount = count
			return
		}
	}
}

// FactorVerifier verifies a presented factor against the platform's own enrollment.
//
// An interface so a deployment can supply a different second-factor mechanism without the
// authenticator learning about it — the same seam argument one level down.
type FactorVerifier interface {
	// Verify returns the KIND of factor that verified, or an error. It never returns a factor's value.
	Verify(ctx context.Context, adminID string, presented PresentedFactor, challenge []byte) (string, error)
}

// PlatformFactors is the shipped verifier: WebAuthn preferred, TOTP fallback.
type PlatformFactors struct {
	factors *FactorStore
	secrets Secrets
	now     Clock
	// rpID is the WebAuthn Relying Party ID — the operator console's registrable domain.
	rpID string
	// origins are the exact origins a WebAuthn assertion may claim. An allowlist, never a suffix rule:
	// origin-binding is the ONE thing WebAuthn gives over TOTP, and a loose comparison gives it back.
	origins []string

	mu   sync.Mutex
	used map[string]time.Time
}

// PlatformFactorsConfig configures the shipped verifier.
type PlatformFactorsConfig struct {
	Factors *FactorStore
	// Secrets supplies TOTP seeds under their reserved logical names.
	Secrets Secrets
	// RPID is the WebAuthn Relying Party ID. Required when WebAuthn is used.
	RPID string
	// Origins are the exact operator-console origins a WebAuthn assertion may name.
	Origins []string
	Now     Clock
}

// NewPlatformFactors builds the shipped verifier.
func NewPlatformFactors(cfg PlatformFactorsConfig) (*PlatformFactors, error) {
	if cfg.Factors == nil {
		return nil, errors.New("adminidentity: a factor verifier needs the platform's enrollment directory — verifying against the IdP's claim is what P22 exists to stop")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("adminidentity: a factor verifier needs a secrets source for TOTP seeds")
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PlatformFactors{
		factors: cfg.Factors, secrets: cfg.Secrets, now: now,
		rpID: cfg.RPID, origins: append([]string(nil), cfg.Origins...),
		used: map[string]time.Time{},
	}, nil
}

// Verify implements FactorVerifier.
func (v *PlatformFactors) Verify(ctx context.Context, adminID string, presented PresentedFactor, challenge []byte) (string, error) {
	enrolled := v.factors.For(adminID)
	if len(enrolled) == 0 {
		return "", ErrFactorNotEnrolled
	}
	if presented.WebAuthn != nil {
		return FactorWebAuthn, v.verifyWebAuthn(adminID, enrolled, presented.WebAuthn, challenge)
	}
	if strings.TrimSpace(presented.TOTP) != "" {
		return FactorTOTP, v.verifyTOTP(ctx, adminID, enrolled, presented.TOTP)
	}
	return "", ErrMFARequired
}

// verifyWebAuthn checks a `navigator.credentials.get()` result, in the order the checks matter.
func (v *PlatformFactors) verifyWebAuthn(adminID string, enrolled []EnrolledFactor, a *WebAuthnAssertion, challenge []byte) error {
	var credential *EnrolledFactor
	for i := range enrolled {
		if enrolled[i].Kind == FactorWebAuthn && subtle.ConstantTimeCompare(enrolled[i].CredentialID, a.CredentialID) == 1 {
			credential = &enrolled[i]
			break
		}
	}
	if credential == nil {
		return ErrFactorNotEnrolled
	}

	var clientData struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}
	if err := json.Unmarshal(a.ClientDataJSON, &clientData); err != nil {
		return fmt.Errorf("%w: client data", ErrFactorInvalid)
	}
	// `webauthn.create` is a REGISTRATION ceremony's type. Accepting it here would let a registration
	// response be replayed as an authentication, which is a documented WebAuthn pitfall.
	if clientData.Type != "webauthn.get" {
		return fmt.Errorf("%w: not an authentication ceremony", ErrFactorInvalid)
	}
	wantChallenge := base64.RawURLEncoding.EncodeToString(challenge)
	if len(challenge) == 0 || subtle.ConstantTimeCompare([]byte(clientData.Challenge), []byte(wantChallenge)) != 1 {
		return fmt.Errorf("%w: challenge", ErrFactorInvalid)
	}
	if !v.allowedOrigin(clientData.Origin) {
		// The origin check IS the phishing resistance. Without it WebAuthn is a slower TOTP.
		return fmt.Errorf("%w: origin", ErrFactorInvalid)
	}

	if len(a.AuthenticatorData) < 37 {
		return fmt.Errorf("%w: authenticator data", ErrFactorInvalid)
	}
	rpIDHash := sha256.Sum256([]byte(v.rpID))
	if subtle.ConstantTimeCompare(a.AuthenticatorData[:32], rpIDHash[:]) != 1 {
		return fmt.Errorf("%w: relying party", ErrFactorInvalid)
	}
	flags := a.AuthenticatorData[32]
	const (
		flagUserPresent  = 0x01
		flagUserVerified = 0x04
	)
	if flags&flagUserPresent == 0 {
		return fmt.Errorf("%w: no user presence", ErrFactorInvalid)
	}
	if flags&flagUserVerified == 0 {
		// User VERIFICATION — a PIN or a biometric at the authenticator — not merely a touch. On the
		// fleet-halting surface, "somebody tapped the key" is not a second factor; "somebody unlocked
		// the key" is.
		return fmt.Errorf("%w: no user verification", ErrFactorInvalid)
	}
	signCount := binary.BigEndian.Uint32(a.AuthenticatorData[33:37])
	if credential.SignCount != 0 && signCount != 0 && signCount <= credential.SignCount {
		// A counter that did not advance means either a replay or a cloned authenticator. Both are
		// denials, and the WebAuthn specification says to treat them as such rather than to guess.
		return fmt.Errorf("%w: signature counter did not advance", ErrFactorInvalid)
	}

	pub, err := x509.ParsePKIXPublicKey(credential.PublicKeySPKI)
	if err != nil {
		return fmt.Errorf("%w: enrolled key", ErrFactorInvalid)
	}
	clientDataHash := sha256.Sum256(a.ClientDataJSON)
	signed := append(append([]byte(nil), a.AuthenticatorData...), clientDataHash[:]...)
	digest := sha256.Sum256(signed)
	switch key := pub.(type) {
	case *ecdsa.PublicKey:
		// WebAuthn ES256 signatures are ASN.1 DER, unlike JWS's raw R‖S. The two encodings are the
		// classic way one of these verifiers silently rejects everything.
		if !ecdsa.VerifyASN1(key, digest[:], a.Signature) {
			return fmt.Errorf("%w: signature", ErrFactorInvalid)
		}
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], a.Signature); err != nil {
			return fmt.Errorf("%w: signature", ErrFactorInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported credential key", ErrFactorInvalid)
	}

	v.factors.recordSignCount(adminID, a.CredentialID, signCount)
	return nil
}

func (v *PlatformFactors) allowedOrigin(origin string) bool {
	for _, allowed := range v.origins {
		if subtle.ConstantTimeCompare([]byte(allowed), []byte(origin)) == 1 {
			return true
		}
	}
	return false
}

// verifyTOTP checks an RFC 6238 code against the seed the secrets manager holds.
func (v *PlatformFactors) verifyTOTP(ctx context.Context, adminID string, enrolled []EnrolledFactor, code string) error {
	code = strings.TrimSpace(code)
	var factor *EnrolledFactor
	for i := range enrolled {
		if enrolled[i].Kind == FactorTOTP {
			factor = &enrolled[i]
			break
		}
	}
	if factor == nil {
		return ErrFactorNotEnrolled
	}
	raw, err := v.secrets.Named(ctx, factor.SecretName)
	if err != nil {
		// Fail closed. No seed means no verification, and no verification means no session — never a
		// fallback to accepting the IdP's word for it.
		return err
	}
	seed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(string(raw))))
	if err != nil {
		return fmt.Errorf("%w: the enrolled seed is not base32", ErrFactorInvalid)
	}

	now := v.now()
	step := now.Unix() / 30
	// One step either side. A wider window is a larger brute-force surface and a smaller one fails a
	// user whose phone clock drifts by fifteen seconds; ±1 is the interoperable answer.
	matched := false
	for _, s := range []int64{step - 1, step, step + 1} {
		if subtle.ConstantTimeCompare([]byte(totpCode(seed, s)), []byte(code)) == 1 {
			matched = true
			step = s
			break
		}
	}
	if !matched {
		return ErrFactorInvalid
	}

	// A TOTP code is valid for a whole step, so without this a captured code is usable twice — by the
	// operator, and by whoever was watching. One use per (principal, step).
	key := fmt.Sprintf("%s|%d", adminID, step)
	v.mu.Lock()
	defer v.mu.Unlock()
	for k, at := range v.used {
		if now.Sub(at) > 2*time.Minute {
			delete(v.used, k)
		}
	}
	if _, seen := v.used[key]; seen {
		return fmt.Errorf("%w: that code has already been used", ErrFactorInvalid)
	}
	v.used[key] = now
	return nil
}

// totpCode is RFC 6238 / RFC 4226: HMAC-SHA1 over the step counter, dynamically truncated to 6 digits.
func totpCode(seed []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, seed)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}
