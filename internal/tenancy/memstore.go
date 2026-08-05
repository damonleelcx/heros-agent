package tenancy

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemStore is the hermetic, default-path implementation of Store.
//
// It is not a test double. It is the store a deployment runs on when it has no Postgres — the same
// relationship `account.MemStore` has to `account.PGStore` — which is why the behavioural suite runs
// against both and why every method here returns an error it can never produce. A map cannot fail; the
// signature exists so the durable store has somewhere to report that it did, and so the two stay
// interchangeable.
type MemStore struct {
	mu          sync.RWMutex
	tenants     map[string]Tenant
	users       map[string]User       // user_id -> user
	byFederated map[string]string     // issuer\x00subject -> user_id
	members     map[string]Membership // user_id\x00tenant_id -> membership
	invites     map[string]Invitation
	creds       map[string]Credential // credential_id -> credential
	byHash      map[string]string     // hash -> credential_id
	sessions    map[string]Session    // token_hash -> session
	// Device authorizations, indexed by both hashes: the short code a person types and the long one the
	// CLI polls with. Two indexes rather than a scan, because the PGStore has two unique indexes and the
	// shared suite runs against both — a store that found a code by scanning would pass a test the other
	// fails under concurrency.
	devices      map[string]DeviceAuthorization
	byUserCode   map[string]string
	byDeviceCode map[string]string
	nextID       int
}

// NewMemStore builds an empty identity store.
func NewMemStore() *MemStore {
	return &MemStore{
		tenants:     map[string]Tenant{},
		users:       map[string]User{},
		byFederated: map[string]string{},
		members:     map[string]Membership{},
		invites:     map[string]Invitation{},
		creds:       map[string]Credential{},
		byHash:      map[string]string{},
		sessions:    map[string]Session{},

		devices:      map[string]DeviceAuthorization{},
		byUserCode:   map[string]string{},
		byDeviceCode: map[string]string{},
	}
}

func fedKey(issuer, subject string) string { return issuer + "\x00" + subject }
func memKey(userID, tenantID string) string {
	return userID + "\x00" + tenantID
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Organizations
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *MemStore) CreateTenant(t Tenant) (Tenant, error) {
	t, err := validateTenant(t)
	if err != nil {
		return Tenant{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[t.TenantID]; ok {
		return Tenant{}, fmt.Errorf("%w: organization %s", ErrExists, t.TenantID)
	}
	s.tenants[t.TenantID] = t
	return t, nil
}

func (s *MemStore) GetTenant(tenantID string) (Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[tenantID]
	if !ok {
		return Tenant{}, fmt.Errorf("%w: organization %s", ErrNotFound, tenantID)
	}
	return t, nil
}

func (s *MemStore) ListTenants() ([]Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tenant, 0, len(s.tenants))
	for _, t := range s.tenants {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}

func (s *MemStore) SetTenantStatus(tenantID string, st Status, _ time.Time) (Tenant, error) {
	if st != StatusActive && st != StatusSuspended {
		return Tenant{}, fmt.Errorf("tenancy: unknown tenant status %q", st)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tenantID]
	if !ok {
		return Tenant{}, fmt.Errorf("%w: organization %s", ErrNotFound, tenantID)
	}
	t.Status = st
	s.tenants[tenantID] = t
	return t, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// People
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// UpsertUser creates a person or refreshes an existing one's DISPLAY attributes.
//
// 🔴 It never changes `UserID`. The federated pair is the identity and the internal id is the key every
// other row references; swapping the key on an identity-provider migration would rewrite every one of
// them.
func (s *MemStore) UpsertUser(u User) (User, error) {
	u, err := validateUser(u)
	if err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byFederated[fedKey(u.Issuer, u.Subject)]; ok {
		cur := s.users[existing]
		cur.Email = u.Email
		s.users[existing] = cur
		return cur, nil
	}
	if u.UserID == "" {
		s.nextID++
		u.UserID = fmt.Sprintf("usr_%06d", s.nextID)
	}
	if _, clash := s.users[u.UserID]; clash {
		return User{}, fmt.Errorf("%w: user %s", ErrExists, u.UserID)
	}
	s.users[u.UserID] = u
	s.byFederated[fedKey(u.Issuer, u.Subject)] = u.UserID
	return u, nil
}

func (s *MemStore) GetUser(userID string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[userID]
	if !ok {
		return User{}, fmt.Errorf("%w: user %s", ErrNotFound, userID)
	}
	return u, nil
}

func (s *MemStore) FindUser(issuer, subject string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byFederated[fedKey(issuer, subject)]
	if !ok {
		return User{}, fmt.Errorf("%w: no user for that identity", ErrNotFound)
	}
	return s.users[id], nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Memberships
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *MemStore) PutMembership(m Membership) (Membership, error) {
	m, err := validateMembership(m)
	if err != nil {
		return Membership{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[m.TenantID]; !ok {
		return Membership{}, fmt.Errorf("%w: organization %s", ErrNotFound, m.TenantID)
	}
	if _, ok := s.users[m.UserID]; !ok {
		return Membership{}, fmt.Errorf("%w: user %s", ErrNotFound, m.UserID)
	}
	s.members[memKey(m.UserID, m.TenantID)] = m
	return m, nil
}

func (s *MemStore) GetMembership(userID, tenantID string) (Membership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[memKey(userID, tenantID)]
	if !ok {
		return Membership{}, fmt.Errorf("%w: membership", ErrNotFound)
	}
	return m, nil
}

func (s *MemStore) ListMembers(tenantID string) ([]Membership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listMembersLocked(tenantID), nil
}

func (s *MemStore) listMembersLocked(tenantID string) []Membership {
	out := []Membership{}
	for _, m := range s.members {
		if m.TenantID == tenantID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out
}

func (s *MemStore) ListMembershipsFor(userID string) ([]Membership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Membership{}
	for _, m := range s.members {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}

// activeOwnersLocked counts active owners other than `excluding`.
func (s *MemStore) activeOwnersLocked(tenantID, excluding string) int {
	n := 0
	for _, m := range s.members {
		if m.TenantID == tenantID && m.UserID != excluding && m.Active() && m.Role == RoleOwner {
			n++
		}
	}
	return n
}

// SetRole refuses to demote the last owner.
//
// An organization with no owner has nobody who can restore it, which makes the mistake unrecoverable
// through the product — so the refusal is named rather than generic, and the surface can say *last
// owner* instead of *forbidden*.
func (s *MemStore) SetRole(userID, tenantID string, role Role) (Membership, error) {
	if !KnownRole(role) {
		return Membership{}, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memKey(userID, tenantID)
	m, ok := s.members[k]
	if !ok {
		return Membership{}, fmt.Errorf("%w: membership", ErrNotFound)
	}
	if m.Active() && m.Role == RoleOwner && role != RoleOwner && s.activeOwnersLocked(tenantID, userID) == 0 {
		return Membership{}, ErrLastOwner
	}
	m.Role = role
	s.members[k] = m
	return m, nil
}

func (s *MemStore) PreviewRemoval(userID, tenantID string) (RemovalPreview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[memKey(userID, tenantID)]
	if !ok {
		return RemovalPreview{}, fmt.Errorf("%w: membership", ErrNotFound)
	}
	p := RemovalPreview{
		UserID:              userID,
		Email:               s.users[userID].Email,
		PersonalCredentials: []Credential{},
		MachineCredentials:  []Credential{},
	}
	for _, c := range s.creds {
		if c.TenantID != tenantID || c.Revoked() {
			continue
		}
		// The two states a removal preview must distinguish, and there is deliberately no default: a
		// credential belonging to a DIFFERENT person in this organization is neither revoked by this
		// removal nor left running by it, so it belongs on neither list.
		switch c.UserID {
		case userID:
			p.PersonalCredentials = append(p.PersonalCredentials, c)
		case "":
			p.MachineCredentials = append(p.MachineCredentials, c)
		}
	}
	sortCreds(p.PersonalCredentials)
	sortCreds(p.MachineCredentials)
	for _, sess := range s.sessions {
		if sess.UserID == userID && sess.TenantID == tenantID && sess.RevokedAt == 0 {
			p.Sessions++
		}
	}
	p.LastOwner = m.Active() && m.Role == RoleOwner && s.activeOwnersLocked(tenantID, userID) == 0
	return p, nil
}

// RemoveMember is one atomic step across membership, sessions and credentials.
//
// The window this closes is small and real: with three separate writes, somebody removed from the member
// list keeps a working key for as long as the second write takes, and an error between them leaves a
// person who is gone from the screen and still authenticated.
func (s *MemStore) RemoveMember(userID, tenantID string, at time.Time) (RemovalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memKey(userID, tenantID)
	m, ok := s.members[k]
	if !ok {
		return RemovalResult{}, fmt.Errorf("%w: membership", ErrNotFound)
	}
	if m.Active() && m.Role == RoleOwner && s.activeOwnersLocked(tenantID, userID) == 0 {
		return RemovalResult{}, ErrLastOwner
	}
	ts := at.UTC()
	m.Status = MemberRemoved
	m.RemovedAt = &ts
	s.members[k] = m

	res := RemovalResult{Membership: m}
	for id, c := range s.creds {
		if c.TenantID != tenantID || c.Revoked() {
			continue
		}
		switch c.UserID {
		case userID:
			c.RevokedAt = &ts
			s.creds[id] = c
			res.CredentialsRevoked++
		case "":
			// Counted, never revoked. An offboarding screen that hid the CI key it leaves running would
			// have the person confirming it sign an attestation that is wrong.
			res.MachineCredsUnknown++
		}
	}
	millis := ts.UnixMilli()
	for h, sess := range s.sessions {
		if sess.UserID == userID && sess.TenantID == tenantID && sess.RevokedAt == 0 {
			sess.RevokedAt = millis
			s.sessions[h] = sess
			res.SessionsRevoked++
		}
	}
	return res, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Invitations
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *MemStore) CreateInvitation(i Invitation) (Invitation, error) {
	i, err := validateInvitation(i)
	if err != nil {
		return Invitation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[i.TenantID]; !ok {
		return Invitation{}, fmt.Errorf("%w: organization %s", ErrNotFound, i.TenantID)
	}
	if _, ok := s.invites[i.InvitationID]; ok {
		return Invitation{}, fmt.Errorf("%w: invitation", ErrExists)
	}
	s.invites[i.InvitationID] = i
	return i, nil
}

func (s *MemStore) GetInvitation(id string) (Invitation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.invites[id]
	if !ok {
		return Invitation{}, fmt.Errorf("%w: invitation", ErrNotFound)
	}
	return i, nil
}

func (s *MemStore) ListInvitations(tenantID string) ([]Invitation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Invitation{}
	for _, i := range s.invites {
		if i.TenantID == tenantID {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].InvitationID < out[b].InvitationID })
	return out, nil
}

// AcceptInvitation is single-use AT THE STORE. Putting the check in the caller means two concurrent
// acceptances of one invitation both pass their check and both create a membership.
func (s *MemStore) AcceptInvitation(id string, at time.Time) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.invites[id]
	if !ok {
		return Invitation{}, fmt.Errorf("%w: invitation", ErrNotFound)
	}
	if !i.Pending(at) {
		return Invitation{}, ErrInviteExpired
	}
	ts := at.UTC()
	i.AcceptedAt = &ts
	s.invites[id] = i
	return i, nil
}

func (s *MemStore) RevokeInvitation(id string, at time.Time) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.invites[id]
	if !ok {
		return Invitation{}, fmt.Errorf("%w: invitation", ErrNotFound)
	}
	if i.RevokedAt == nil {
		ts := at.UTC()
		i.RevokedAt = &ts
		s.invites[id] = i
	}
	return i, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Credentials
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *MemStore) CreateCredential(c Credential) (Credential, error) {
	c, err := validateCredential(c)
	if err != nil {
		return Credential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[c.TenantID]; !ok {
		return Credential{}, fmt.Errorf("%w: organization %s", ErrNotFound, c.TenantID)
	}
	if c.UserID != "" {
		if _, ok := s.users[c.UserID]; !ok {
			return Credential{}, fmt.Errorf("%w: user %s", ErrNotFound, c.UserID)
		}
	}
	if _, ok := s.creds[c.CredentialID]; ok {
		return Credential{}, fmt.Errorf("%w: credential", ErrExists)
	}
	if _, ok := s.byHash[c.Hash]; ok {
		return Credential{}, fmt.Errorf("%w: a credential with that hash", ErrExists)
	}
	s.creds[c.CredentialID] = c
	s.byHash[c.Hash] = c.CredentialID
	return c, nil
}

// ResolveCredential looks up by HASH. A revoked credential is returned rather than hidden, so the caller
// can distinguish "no such credential" from "revoked" in its own logs while answering the wire
// identically — a revoked credential must not be a probing oracle.
func (s *MemStore) ResolveCredential(hash string) (Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byHash[hash]
	if !ok {
		return Credential{}, fmt.Errorf("%w: credential", ErrNotFound)
	}
	return s.creds[id], nil
}

func (s *MemStore) ListCredentials(tenantID string) ([]Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Credential{}
	for _, c := range s.creds {
		if c.TenantID == tenantID {
			out = append(out, c)
		}
	}
	sortCreds(out)
	return out, nil
}

func (s *MemStore) RevokeCredential(credentialID string, at time.Time) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.creds[credentialID]
	if !ok {
		return Credential{}, fmt.Errorf("%w: credential", ErrNotFound)
	}
	if c.RevokedAt == nil {
		ts := at.UTC()
		c.RevokedAt = &ts
		s.creds[credentialID] = c
	}
	return c, nil
}

func sortCreds(cs []Credential) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].CredentialID < cs[j].CredentialID })
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Console sessions
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *MemStore) CreateSession(sess Session) (Session, error) {
	sess.TokenHash = trimmed(sess.TokenHash)
	sess.SessionID = trimmed(sess.SessionID)
	sess.TenantID = trimmed(sess.TenantID)
	if sess.TokenHash == "" || sess.SessionID == "" || sess.TenantID == "" {
		return Session{}, fmt.Errorf("%w: a session needs a token hash, an id and an organization", ErrEmptyField)
	}
	if sess.Purpose == "" {
		sess.Purpose = PurposeUpstream
	}
	if !KnownPurpose(sess.Purpose) {
		return Session{}, fmt.Errorf("tenancy: unknown session purpose %q", sess.Purpose)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[sess.TenantID]; !ok {
		return Session{}, fmt.Errorf("%w: organization %s", ErrNotFound, sess.TenantID)
	}
	s.sessions[sess.TokenHash] = sess
	return sess, nil
}

func (s *MemStore) ResolveSession(tokenHash string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[tokenHash]
	if !ok {
		return Session{}, fmt.Errorf("%w: session", ErrNotFound)
	}
	return sess, nil
}

func (s *MemStore) RevokeSession(tokenHash string, atMillis int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[tokenHash]
	if !ok {
		return fmt.Errorf("%w: session", ErrNotFound)
	}
	if sess.RevokedAt == 0 {
		sess.RevokedAt = atMillis
		s.sessions[tokenHash] = sess
	}
	return nil
}

func (s *MemStore) ListSessionsFor(userID, tenantID string) ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Session{}
	for _, sess := range s.sessions {
		if sess.UserID == userID && sess.TenantID == tenantID {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func trimmed(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Device authorization (task 13.1)
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *MemStore) CreateDeviceAuthorization(d DeviceAuthorization) (DeviceAuthorization, error) {
	d, err := validateDeviceAuthorization(d)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.devices == nil {
		s.devices = map[string]DeviceAuthorization{}
		s.byUserCode = map[string]string{}
		s.byDeviceCode = map[string]string{}
	}
	if _, ok := s.devices[d.DeviceID]; ok {
		return DeviceAuthorization{}, fmt.Errorf("%w: device authorization", ErrExists)
	}
	// 🔴 Both hashes are unique, and a collision is an ERROR rather than an overwrite. Two live
	// authorizations sharing a user code would make "which one did they approve" unanswerable, and the
	// PGStore enforces the same thing with a unique index — so the two stores refuse the same writes.
	if _, ok := s.byUserCode[d.UserCodeHash]; ok {
		return DeviceAuthorization{}, fmt.Errorf("%w: user code", ErrExists)
	}
	if _, ok := s.byDeviceCode[d.DeviceCodeHash]; ok {
		return DeviceAuthorization{}, fmt.Errorf("%w: device code", ErrExists)
	}
	s.devices[d.DeviceID] = d
	s.byUserCode[d.UserCodeHash] = d.DeviceID
	s.byDeviceCode[d.DeviceCodeHash] = d.DeviceID
	return d, nil
}

func (s *MemStore) FindDeviceByUserCode(userCodeHash string) (DeviceAuthorization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byUserCode[userCodeHash]
	if !ok {
		return DeviceAuthorization{}, fmt.Errorf("%w: device authorization", ErrNotFound)
	}
	return s.devices[id], nil
}

func (s *MemStore) DecideDevice(deviceID string, status DeviceAuthStatus, approvedBy, tenantID, credentialID string, at time.Time) (DeviceAuthorization, error) {
	if status != DeviceApproved && status != DeviceDenied {
		return DeviceAuthorization{}, fmt.Errorf("%w: a decision is approved or denied", ErrEmptyField)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[deviceID]
	if !ok {
		return DeviceAuthorization{}, fmt.Errorf("%w: device authorization", ErrNotFound)
	}
	// Single-use, at the store. A second decision on the same record — a double-clicked Approve, two
	// tabs, a retry after a timeout — is refused rather than allowed to issue a second credential.
	if !d.Pending(at) {
		return DeviceAuthorization{}, ErrDeviceCode
	}
	d.Status = status
	d.ApprovedBy = approvedBy
	d.TenantID = tenantID
	d.CredentialID = credentialID
	t := at.UTC()
	d.DecidedAt = &t
	s.devices[deviceID] = d
	return d, nil
}

func (s *MemStore) CollectDevice(deviceCodeHash string, at time.Time) (DeviceAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byDeviceCode[deviceCodeHash]
	if !ok {
		// An unknown device code is ErrDeviceCode, not ErrNotFound: the caller must not be able to tell
		// "no such code" from "denied" from "expired".
		return DeviceAuthorization{}, ErrDeviceCode
	}
	d := s.devices[id]
	if d.Status == DevicePending && at.Before(d.ExpiresAt) {
		// The one answer that is NOT terminal, and the only one the CLI keeps polling on.
		return d, nil
	}
	if !d.Collectable(at) {
		return DeviceAuthorization{}, ErrDeviceCode
	}
	t := at.UTC()
	d.CollectedAt = &t
	s.devices[id] = d
	return d, nil
}
