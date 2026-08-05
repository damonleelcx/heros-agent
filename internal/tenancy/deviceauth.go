package tenancy

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
)

// deviceauth.go is the device authorization a terminal uses to obtain a credential without ever holding
// a password, an assertion or an ID token (P27 task 13.1, account-registry FR).
//
// # Why the CLI cannot simply be given a token
//
// It could, and `--token` still is — that is the MACHINE path and it stays exactly as it was. What it
// cannot be is the path a person uses, because a person pasting a long-lived organization key into a
// terminal produces a credential that names nobody: it survives their offboarding, it attributes nothing,
// and § 4.5's promise — *remove a member and their access ends* — is simply false in a shell. Device
// authorization is what makes that sentence true in a terminal, which is the whole reason this exists.
//
// # The shape, and the three things it deliberately refuses
//
//	 request   the CLI asks for a code. It supplies a DEVICE LABEL and nothing else.
//	 approve   a person, already signed in to the console, enters the user code and picks an organization.
//	 poll      the CLI exchanges its device code for the credential, exactly once.
//
//   - 🔴 NOTHING THE CLIENT SUPPLIES BINDS THE CODE. There is no client id, no redirect, no PKCE
//     challenge, and the device label is a display string that is never compared. A code bound to
//     something the caller chose is a code an attacker can bind to something they chose.
//
//   - 🔴 THE USER CODE AND THE DEVICE CODE ARE DIFFERENT SECRETS. The user code is short because a human
//     retypes it, and short means guessable — so it can only ever be used to APPROVE, by somebody who is
//     already authenticated, and holding it grants nothing. The device code is 32 random bytes, is the
//     only thing that can COLLECT the credential, and is stored hashed like every other secret here.
//
//   - 🔴 DENIAL, EXPIRY AND AN UNKNOWN CODE ARE ONE ANSWER (task 13.7). The difference is useful only to
//     somebody enumerating codes; a real user's CLI says the same sentence in all three cases and tells
//     them to run `heros login` again.
//
// # Why the approval carries the organization
//
// A person may hold memberships in several organizations, so "which one is this terminal for" has no
// server-side default that is not a guess. The approver picks, and the picking is checked: an approval
// naming an organization the approver has no ACTIVE membership in is refused, which is what stops a
// removed member from selecting the organization they just left.

// DeviceAuthStatus is where one authorization has got to. There is no fourth value: a code is pending,
// it produced a credential, or it will never produce one.
type DeviceAuthStatus string

const (
	// DevicePending: minted, not yet approved, not yet expired.
	DevicePending DeviceAuthStatus = "pending"
	// DeviceApproved: a person approved it and a credential was issued. Terminal — the device code is
	// single-use, so a second collection finds this and refuses.
	DeviceApproved DeviceAuthStatus = "approved"
	// DeviceDenied: a person refused it. Terminal, and reported to the CLI identically to expiry.
	DeviceDenied DeviceAuthStatus = "denied"
)

// DeviceAuthorization is one in-flight terminal login.
//
// It holds two HASHES and no plaintext, for the reason `Credential` does: a struct with nowhere to put a
// secret cannot leak one through a log line somebody adds later.
type DeviceAuthorization struct {
	// DeviceID is the record's own identifier — safe to log, names no secret.
	DeviceID string `json:"device_id"`
	// UserCodeHash is the short human-typed code, hashed. Stored hashed even though it is short and
	// low-entropy: it is still a value somebody types, and a database read must not hand an attacker a
	// list of codes awaiting approval.
	UserCodeHash string `json:"-"`
	// DeviceCodeHash is the CLI's polling secret, hashed.
	DeviceCodeHash string `json:"-"`
	// Label is what the CLI reported about the machine — "damon@studio (darwin/arm64)". A DISPLAY string:
	// it is shown on the approval screen so a person can tell which terminal they are approving, and on
	// the credential afterwards so a revocation screen names something a human recognises. It is never
	// compared, never trusted, and never used to find a record.
	Label string `json:"label"`

	Status    DeviceAuthStatus `json:"status"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt time.Time        `json:"expires_at"`

	// ApprovedBy and TenantID are written together at approval, by the same call, from the approver's
	// VERIFIED principal and their explicit organization choice.
	ApprovedBy string     `json:"approved_by,omitempty"`
	TenantID   string     `json:"tenant_id,omitempty"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`

	// CredentialID is the credential this authorization issued. Present only once approval has produced
	// one, and it is what makes the flow single-use at the STORE rather than in caller logic.
	CredentialID string `json:"credential_id,omitempty"`
	// CollectedAt stamps the one successful poll. A second poll with the same device code finds this set
	// and is refused — the CLI already has the secret, and anything else re-issuing it would mean the
	// plaintext existed in two places.
	CollectedAt *time.Time `json:"collected_at,omitempty"`
}

// Pending reports whether this authorization can still be approved at `now`.
func (d DeviceAuthorization) Pending(now time.Time) bool {
	return d.Status == DevicePending && now.Before(d.ExpiresAt)
}

// Collectable reports whether a poll may still exchange this for a credential.
func (d DeviceAuthorization) Collectable(now time.Time) bool {
	return d.Status == DeviceApproved && d.CollectedAt == nil && d.CredentialID != "" && now.Before(d.ExpiresAt)
}

// DeviceCodeTTL is how long a code lives. Long enough to switch to a browser, sign in and read a screen;
// short enough that an unattended terminal does not leave an approvable code lying around all day.
const DeviceCodeTTL = 10 * time.Minute

// DevicePollInterval is what the server tells the CLI to wait between polls. It is advice with a floor
// behind it — see ErrDevicePollTooFast.
const DevicePollInterval = 2 * time.Second

// userCodeAlphabet excludes the characters a person mistypes when reading one off a screen: no O/0, no
// I/1/L, no U (which is read as V in some fonts). A code that is hard to retype produces a support
// ticket, and a support ticket about a login is the worst kind.
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

// NewUserCode mints the short code a person types, formatted `XXXX-XXXX`.
//
// 🔴 It carries about 39 bits, which is NOT enough to be a secret on its own — and it is not asked to be
// one. Holding a user code grants nothing: approving requires an authenticated person, approval can only
// select an organization that person is already a member of, and the code alone cannot collect the
// credential. The entropy that matters is on the device code.
func NewUserCode() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("tenancy: cannot mint a device user code: %w", err)
	}
	out := make([]byte, 0, 9)
	for i, v := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, userCodeAlphabet[int(v)%len(userCodeAlphabet)])
	}
	return string(out), nil
}

// NewDeviceCode mints the CLI's polling secret. Same 32 bytes as a credential, same prefix, because it
// is the same class of value and a scanner's rules should catch it in a paste.
func NewDeviceCode() (string, error) { return NewCredentialSecret() }

// NormalizeUserCode makes a typed code comparable: upper-cased, with spaces and hyphens removed, so
// `abcd efgh`, `ABCD-EFGH` and `abcdefgh` are one code.
//
// The hyphen is presentational. Normalizing here — in the package that hashes it — rather than at each
// caller is what stops the console and the API disagreeing about whether a code with a typo'd separator
// is the same code.
func NormalizeUserCode(s string) string {
	up := strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", " ", "", "\t", "").Replace(up)
}

// validateDeviceAuthorization is the store-side shape check every implementation runs first.
func validateDeviceAuthorization(d DeviceAuthorization) (DeviceAuthorization, error) {
	d.DeviceID = strings.TrimSpace(d.DeviceID)
	d.Label = strings.TrimSpace(d.Label)
	if d.DeviceID == "" {
		return DeviceAuthorization{}, fmt.Errorf("%w: device authorization needs an id", ErrEmptyField)
	}
	if d.UserCodeHash == "" || d.DeviceCodeHash == "" {
		return DeviceAuthorization{}, fmt.Errorf("%w: a device authorization needs both hashes", ErrEmptyField)
	}
	if d.ExpiresAt.IsZero() {
		return DeviceAuthorization{}, fmt.Errorf("%w: a device authorization needs an expiry", ErrEmptyField)
	}
	if d.Status == "" {
		d.Status = DevicePending
	}
	if d.Status != DevicePending && d.Status != DeviceApproved && d.Status != DeviceDenied {
		return DeviceAuthorization{}, fmt.Errorf("%w: %q is not a device authorization status", ErrEmptyField, d.Status)
	}
	// 🔴 A label is truncated, never rejected. It is a display string the CLI reports about a machine we
	// do not control, and failing a login because somebody's hostname is long would be the wrong trade —
	// but an unbounded one reaches a database column and a rendered screen.
	if len(d.Label) > 200 {
		d.Label = d.Label[:200]
	}
	return d, nil
}
