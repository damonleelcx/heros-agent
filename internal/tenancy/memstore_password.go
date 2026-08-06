package tenancy

import (
	"fmt"
	"sort"
	"time"
)

// memstore_password.go is the in-memory half of the P28 storage.
//
// Every method here returns an error it cannot produce, for the reason the package doc gives: `PGStore` can
// fail and the two have to stay interchangeable. What it must NOT do is be more permissive than the durable
// store — the shared behavioural suite runs against both, so a rule the database enforces and this does not
// would be a rule that holds only in production, discovered by a customer.

func (s *MemStore) SetPassword(userID, encoded string, at time.Time) (UserPassword, error) {
	rec, err := validateUserPassword(UserPassword{UserID: userID, Encoded: encoded, UpdatedAt: at.UTC()})
	if err != nil {
		return UserPassword{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[rec.UserID]; !ok {
		return UserPassword{}, fmt.Errorf("%w: user %s", ErrNotFound, rec.UserID)
	}
	// 🔴 Setting a password CLEARS the lockout. A person who just proved control of their address must not
	// still be locked out by the failures that made them reset — that is the shape of bug where the fix
	// works and the user still cannot get in.
	rec.FailedAttempts, rec.WindowStartedAt, rec.LockedUntil = 0, nil, nil
	if s.passwords == nil {
		s.passwords = map[string]UserPassword{}
	}
	s.passwords[rec.UserID] = rec
	return rec, nil
}

func (s *MemStore) GetPassword(userID string) (UserPassword, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.passwords[userID]
	if !ok {
		return UserPassword{}, fmt.Errorf("%w: %s", ErrNoPassword, userID)
	}
	return rec, nil
}

func (s *MemStore) RecordPasswordFailure(userID string, at time.Time, pol LockoutPolicy) (UserPassword, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.passwords[userID]
	if !ok {
		return UserPassword{}, fmt.Errorf("%w: %s", ErrNoPassword, userID)
	}
	next := applyFailure(cur, at, pol)
	s.passwords[userID] = next
	return next, nil
}

func (s *MemStore) ClearPasswordFailures(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.passwords[userID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoPassword, userID)
	}
	cur.FailedAttempts, cur.WindowStartedAt, cur.LockedUntil = 0, nil, nil
	s.passwords[userID] = cur
	return nil
}

func (s *MemStore) FindUserByEmail(issuer, email string) (User, error) {
	e := NormalizeEmail(email)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Issuer == issuer && NormalizeEmail(u.Email) == e {
			return u, nil
		}
	}
	return User{}, fmt.Errorf("%w: no user at that address", ErrNotFound)
}

func (s *MemStore) MarkEmailVerified(userID string, at time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return User{}, fmt.Errorf("%w: user %s", ErrNotFound, userID)
	}
	// Idempotent, keeping the FIRST time: that is the one that is true, and an audit that reads this column
	// is asking when the address was proved, not when somebody last clicked a link.
	if u.EmailVerifiedAt == nil {
		t := at.UTC()
		u.EmailVerifiedAt = &t
		s.users[userID] = u
	}
	return u, nil
}

func (s *MemStore) MintIdentityToken(t IdentityToken) (IdentityToken, error) {
	t, err := validateIdentityToken(t)
	if err != nil {
		return IdentityToken{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[t.UserID]; !ok {
		return IdentityToken{}, fmt.Errorf("%w: user %s", ErrNotFound, t.UserID)
	}
	if s.tokens == nil {
		s.tokens = map[string]IdentityToken{}
	}
	if _, ok := s.tokens[t.TokenHash]; ok {
		return IdentityToken{}, fmt.Errorf("%w: identity token", ErrExists)
	}
	s.tokens[t.TokenHash] = t
	return t, nil
}

func (s *MemStore) ConsumeIdentityToken(tokenHash string, purpose TokenPurpose, at time.Time) (IdentityToken, error) {
	at = at.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[tokenHash]
	// One answer for unknown, wrong-purpose, spent and expired. The mutex makes the check-and-write atomic
	// here, exactly as the conditional UPDATE does in Postgres — the point being that no caller ever gets to
	// decide whether a token is still good.
	if !ok || t.Purpose != purpose || !t.Live(at) {
		return IdentityToken{}, ErrIdentityToken
	}
	t.ConsumedAt = &at
	s.tokens[tokenHash] = t
	return t, nil
}

func (s *MemStore) RevokeEverythingFor(userID string, at time.Time) (PersonRevocation, error) {
	ts := at.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return PersonRevocation{}, fmt.Errorf("%w: user %s", ErrNotFound, userID)
	}

	// Which organizations this person belongs to — the scope machine credentials are reported from. A
	// machine credential belongs to an ORGANIZATION, so "what did this reset leave running" is a question
	// about the organizations the person is in, not about the person.
	orgs := map[string]bool{}
	for key, m := range s.members {
		_ = key
		if m.UserID == userID {
			orgs[m.TenantID] = true
		}
	}

	var out PersonRevocation
	for hash, sess := range s.sessions {
		if sess.UserID == userID && sess.RevokedAt == 0 {
			sess.RevokedAt = ts.UnixMilli()
			s.sessions[hash] = sess
			out.SessionsRevoked++
		}
	}
	for id, c := range s.creds {
		switch {
		case c.UserID == userID && c.RevokedAt == nil:
			t := ts
			c.RevokedAt = &t
			s.creds[id] = c
			out.CredentialsRevoked++
		case c.UserID == "" && c.RevokedAt == nil && orgs[c.TenantID]:
			// 🔴 Reported, not touched. See PersonRevocation's comment for why hiding this is worse than
			// showing it.
			out.MachineCredentials = append(out.MachineCredentials, c)
		}
	}
	sort.Slice(out.MachineCredentials, func(i, j int) bool {
		return out.MachineCredentials[i].CredentialID < out.MachineCredentials[j].CredentialID
	})
	return out, nil
}
