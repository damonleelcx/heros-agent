package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/tenancy"
)

// deviceauth.go is the HTTP half of device authorization (P27 task 13.1).
//
// # Three routes, and only one of them is authenticated
//
//	POST /api/v1/device/authorize   unauthenticated · the CLI has no credential yet — that is the point
//	POST /api/v1/device/token       unauthenticated · the CLI polls, holding a 256-bit device code
//	POST /api/v1/device/approve       AUTHENTICATED · a person decides, from the console
//
// The two unauthenticated routes are safe for different reasons, and both reasons are worth stating
// because "unauthenticated endpoint" reads as a finding until somebody explains it.
//
// `authorize` mints a row and returns two secrets it generated itself. It accepts one field — a display
// label — and that field binds nothing, is never compared, and is never used to find a record. There is
// no client id, no redirect, no PKCE challenge: a code bound to something the caller chose is a code an
// attacker can bind to something they chose.
//
// `token` is guarded by entropy alone, and the entropy is the whole design. The device code is 32 random
// bytes; guessing one is not a strategy. The SHORT code — the one a person retypes — cannot reach this
// route at all, which is why it is allowed to be short.
//
// ⚠️ Neither is rate-limited here. `authorize` mints a row per call, so a caller can fill a table with
// ten-minute-lived rows, and the platform has no request-rate mechanism for this layer to hook into. The
// row is small, the expiry is short and the sweep is indexed — but this is a real gap and it is written
// down rather than implied to be handled.
//
// # 🔴 One refusal, four causes (task 13.7)
//
// Denied, expired, already-collected and never-existed all answer identically. The difference is useful
// only to somebody enumerating codes, and a real user's next action — run `heros login` again — is the
// same in every case. The store returns one error value for all four so this layer cannot accidentally
// distinguish them by which branch it took.

// ReasonDeviceCode is the machine-readable form of that single refusal.
const ReasonDeviceCode = "device_code_unusable"

// deviceVerificationPath is where a person goes to approve. Rendered by the console, which is the only
// surface that has the person's session — the platform never sees their assertion.
const deviceVerificationPath = "/app/device"

// mountDeviceAuth registers the three routes.
//
// 🔴 UNEXPORTED, and called from MountAccounts rather than from the deployed path directly. That is not a
// style choice — `internal/launch`'s TestEveryMountFunctionIsCalledByTheDeployedPath caught the exported
// version and was right to: an exported `Mount*` announces an independently-mountable capability, and a
// deployment that forgot to call one gets a 404 with a green build and no other signal.
//
// Device authorization is not independently mountable. Its approval step requires an ACTIVE membership,
// so a deployment with no account system has nothing for an approval to select — mounting it alone would
// produce three routes that can only ever refuse. Making it part of the account system's mount says that
// in the type system instead of in a comment.
func (s *Server) mountDeviceAuth() {
	s.Mux.HandleFunc("POST /api/v1/device/authorize", s.handleDeviceAuthorize)
	s.Mux.HandleFunc("POST /api/v1/device/token", s.handleDeviceToken)
	s.Mux.HandleFunc("POST /api/v1/device/approve", s.handleDeviceApprove)
}

// handleDeviceAuthorize mints a pending authorization and hands back both plaintexts, once.
func (s *Server) handleDeviceAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff,
			"this deployment does not mount the account system")
		return
	}
	var req struct {
		// Label is what the CLI reports about the machine. Optional, truncated by the store, never
		// compared — see DeviceAuthorization.Label.
		Label string `json:"label"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		refuse(w, http.StatusBadRequest, "", "the request is not valid: "+err.Error())
		return
	}

	userCode, err := tenancy.NewUserCode()
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "a device code could not be minted")
		return
	}
	deviceCode, err := tenancy.NewDeviceCode()
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "a device code could not be minted")
		return
	}
	now := s.accounts.Now()
	if _, err := s.accounts.Store().CreateDeviceAuthorization(tenancy.DeviceAuthorization{
		DeviceID:       tenancy.NewID("dev"),
		UserCodeHash:   tenancy.HashSecret(tenancy.NormalizeUserCode(userCode)),
		DeviceCodeHash: tenancy.HashSecret(deviceCode),
		Label:          req.Label,
		Status:         tenancy.DevicePending,
		CreatedAt:      now,
		ExpiresAt:      now.Add(tenancy.DeviceCodeTTL),
	}); err != nil {
		refuse(w, http.StatusInternalServerError, "", "the device authorization could not be created")
		return
	}

	// 🔴 Both plaintexts leave here and are never readable again — the store holds only hashes. The user
	// code goes on the terminal for a person to read; the device code stays in the CLI's memory.
	writeJSON(w, http.StatusCreated, map[string]any{
		"user_code":          userCode,
		"device_code":        deviceCode,
		"verification_uri":   deviceVerificationPath,
		"interval_seconds":   int(tenancy.DevicePollInterval / time.Second),
		"expires_in_seconds": int(tenancy.DeviceCodeTTL / time.Second),
	})
}

// handleDeviceToken is the CLI's poll.
func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff,
			"this deployment does not mount the account system")
		return
	}
	var req struct {
		DeviceCode string `json:"device_code"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid: "+err.Error())
		return
	}
	if strings.TrimSpace(req.DeviceCode) == "" {
		refuse(w, http.StatusBadRequest, "", "device_code is required")
		return
	}

	store := s.accounts.Store()
	d, err := store.CollectDevice(tenancy.HashSecret(strings.TrimSpace(req.DeviceCode)), s.accounts.Now())
	if err != nil {
		// The one refusal. 400 rather than 404 or 403: a status that distinguished "no such code" from
		// "denied" would be the oracle this whole design avoids.
		refuse(w, http.StatusBadRequest, ReasonDeviceCode,
			"that device code is no longer usable — run `heros login` again")
		return
	}
	if d.Status == tenancy.DevicePending {
		// Not an error, and deliberately a 200: the CLI is doing the right thing and must keep going.
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	}

	// Approved and collected. The plaintext the CLI needs was handed to the APPROVAL, which minted it —
	// so this returns the credential's identity, not a secret it does not hold.
	//
	// 🔴 That is the shape, and it is why `pendingSecrets` exists below rather than a column: the
	// plaintext must reach the CLI exactly once and must never be at rest. It lives in memory on the
	// process that minted it, keyed by the device id, until the poll collects it.
	secret, ok := s.takeDeviceSecret(d.DeviceID)
	if !ok {
		// The approval happened on another replica, or this process restarted between approval and poll.
		// Refused with the same sentence: the CLI's action is to start over, and a partial success it
		// cannot use is worse than a refusal it can.
		refuse(w, http.StatusBadRequest, ReasonDeviceCode,
			"that device code is no longer usable — run `heros login` again")
		return
	}
	body := map[string]any{
		"status":          "approved",
		"token":           secret,
		"identity":        d.TenantID,
		"organization_id": d.TenantID,
		"credential_id":   d.CredentialID,
	}
	if t, terr := store.GetTenant(d.TenantID); terr == nil {
		body["organization_name"] = t.Name
	}
	writeJSON(w, http.StatusOK, body)
}

// handleDeviceApprove is the console's call, made for a signed-in person.
func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	// 🔴 A machine credential may not approve a terminal login. The credential it would issue names a
	// person, and a CI key has no person to name — the attribution would be a placeholder, which is the
	// one thing this phase refuses everywhere else too.
	if p.UserID == "" {
		refuse(w, http.StatusForbidden, ReasonNotAMember,
			"approving a terminal login needs a signed-in person; a machine credential names nobody")
		return
	}
	var req struct {
		UserCode string `json:"user_code"`
		TenantID string `json:"tenant_id"`
		// Approve false is an explicit DENIAL, which is a different outcome from letting the code expire:
		// it ends the CLI's wait immediately instead of making somebody watch a spinner for ten minutes.
		Approve bool `json:"approve"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid: "+err.Error())
		return
	}
	code := tenancy.NormalizeUserCode(req.UserCode)
	if code == "" {
		refuse(w, http.StatusBadRequest, "", "user_code is required")
		return
	}

	now := s.accounts.Now()
	d, err := store.FindDeviceByUserCode(tenancy.HashSecret(code))
	if err != nil || !d.Pending(now) {
		refuse(w, http.StatusBadRequest, ReasonDeviceCode,
			"that code is not waiting for approval — check it, or ask for a new one")
		return
	}

	if !req.Approve {
		if _, err := store.DecideDevice(d.DeviceID, tenancy.DeviceDenied, p.UserID, "", "", now); err != nil {
			refuse(w, http.StatusBadRequest, ReasonDeviceCode, "that code is no longer waiting for approval")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "denied"})
		return
	}

	// The organization the approver chose, defaulting to the one their token is scoped to. Checked either
	// way — a default is not an authorization.
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = p.TenantID
	}
	// 🔴 An ACTIVE membership in the organization being selected. This is what stops somebody who was
	// removed this morning from approving a terminal into the organization they just left, and it is
	// checked against the store rather than against anything the request said.
	m, err := store.GetMembership(p.UserID, tenantID)
	if err != nil || !m.Active() {
		refuse(w, http.StatusForbidden, ReasonNotAMember,
			"you are not an active member of that organization")
		return
	}
	if t, terr := store.GetTenant(tenantID); terr != nil || t.Status.Suspended() {
		refuse(w, http.StatusForbidden, ReasonOrgSuspended, "that organization is suspended")
		return
	}

	// The credential is minted BEFORE the decision is recorded, so a decision never names a credential
	// that does not exist. If the decision then loses a race, the credential is revoked rather than left
	// as an orphan somebody could still authenticate with.
	secret, err := tenancy.NewCredentialSecret()
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "a credential could not be minted")
		return
	}
	label := strings.TrimSpace(d.Label)
	if label == "" {
		label = "command line"
	}
	cred, err := store.CreateCredential(tenancy.Credential{
		CredentialID: tenancy.NewID("cred"),
		TenantID:     tenantID,
		// 🔴 The approving PERSON. This is the whole point of task 13.3: the credential is personal, so
		// removing this member revokes it (4.5) and the credentials list shows it as personal (4.6).
		UserID:    p.UserID,
		Label:     label,
		Role:      m.Role,
		Hash:      tenancy.HashSecret(secret),
		CreatedAt: now,
	})
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "a credential could not be created")
		return
	}

	if _, err := store.DecideDevice(d.DeviceID, tenancy.DeviceApproved, p.UserID, tenantID, cred.CredentialID, now); err != nil {
		// Somebody else decided it first. Revoke what we just minted rather than leave a working key
		// nobody approved.
		_, _ = store.RevokeCredential(cred.CredentialID, now)
		refuse(w, http.StatusBadRequest, ReasonDeviceCode, "that code is no longer waiting for approval")
		return
	}
	s.putDeviceSecret(d.DeviceID, secret)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "approved",
		"organization_id": tenantID,
		"device_label":    label,
		"credential_id":   cred.CredentialID,
	})
}

// ── the plaintext hand-off ───────────────────────────────────────────────────────────────────────────

// deviceSecrets holds an issued plaintext between the approval that minted it and the poll that collects
// it. In memory, on the process that minted it, and nowhere else.
//
// 🔴 Not a column, and the difference matters. Storing the plaintext — even briefly, even encrypted —
// would put a working credential at rest in a table, which is the one thing `Credential` was shaped to
// make impossible (it has a Hash and no secret field). The cost is stated rather than hidden: an approval
// on replica A and a poll routed to replica B cannot complete, and the CLI is told to start over. That is
// a worse user experience and a much better security property, and the alternative was never a real
// option.
type deviceSecrets struct {
	mu   sync.Mutex
	vals map[string]deviceSecret
}

type deviceSecret struct {
	secret string
	at     time.Time
}

func (s *Server) putDeviceSecret(deviceID, secret string) {
	s.deviceSecrets.mu.Lock()
	defer s.deviceSecrets.mu.Unlock()
	if s.deviceSecrets.vals == nil {
		s.deviceSecrets.vals = map[string]deviceSecret{}
	}
	now := time.Now().UTC()
	// Sweep on write: an approval nobody ever polls for would otherwise hold a plaintext in memory until
	// the process ends. Bounded by the code's own TTL, so nothing here outlives the row it belongs to.
	for id, v := range s.deviceSecrets.vals {
		if now.Sub(v.at) > tenancy.DeviceCodeTTL {
			delete(s.deviceSecrets.vals, id)
		}
	}
	s.deviceSecrets.vals[deviceID] = deviceSecret{secret: secret, at: now}
}

// takeDeviceSecret returns the plaintext and REMOVES it. Once, by construction.
func (s *Server) takeDeviceSecret(deviceID string) (string, bool) {
	s.deviceSecrets.mu.Lock()
	defer s.deviceSecrets.mu.Unlock()
	v, ok := s.deviceSecrets.vals[deviceID]
	if !ok {
		return "", false
	}
	delete(s.deviceSecrets.vals, deviceID)
	return v.secret, true
}
