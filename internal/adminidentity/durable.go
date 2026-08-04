package adminidentity

import (
	"errors"
	"fmt"
	"strings"
)

// durable.go gives the two in-memory directories — `PrincipalStore` and `FactorStore` — an optional
// durable backing, so an operator survives a process restart.
//
// # Why this is a port on the existing store rather than a second implementation
//
// The obvious alternative is an interface with a memory and a Postgres implementation. It is the wrong
// shape here for a specific reason: `AuthenticatorConfig`, `SessionConfig` and `PlatformFactorsConfig`
// all take these CONCRETE types, and `FactorStore` is read from inside this package's verification path
// (`recordSignCount`). Making them interfaces would widen a security-critical seam across four call
// sites to buy an abstraction only one deployment shape uses. A write-through port keeps the read path,
// the locking and the validation exactly as they are, and adds one question: is anything listening.
//
// # Write-through, and the ORDER is the point
//
// The durable write happens FIRST, and a failure aborts the mutation with memory untouched. The other
// order — update memory, then persist, then log the persist error — is the one that produces an
// operator who is enrolled until the pod restarts, which is the failure mode this file exists to remove
// and would reintroduce in a form nobody can see. So: if it did not persist, it did not happen.
//
// Loading is the reverse and deliberately does NOT write back. `LoadPrincipals`/`LoadFactors` replay
// rows the store already holds durably; sending them back through `Put`/`Enroll` would re-persist every
// row on every boot and, worse, would make a read-only replay indistinguishable from a write.
//
// # Nil is a legitimate wiring, and it is the default
//
// A deployment with no platform database leaves the writer nil and gets exactly today's behaviour: an
// in-memory directory, which is correct for a test, a demo and a single-run proof binary. What it must
// never do is look durable. `adminlaunch` refuses a federated production deployment that has no writer,
// so "the operators are in memory" is a decision made once, at boot, by a caller that knows.

// PrincipalWriter persists admin-directory mutations.
//
// No context parameter, because `PrincipalStore`'s methods have none and threading one through would
// change four signatures to reach a call that is already bounded by the store's own lock. The
// implementation owns its timeout.
type PrincipalWriter interface {
	// PutPrincipal inserts or replaces one principal.
	PutPrincipal(p Principal) error
	// DisablePrincipal marks one principal disabled.
	DisablePrincipal(adminID string) error
}

// FactorWriter persists enrolment-directory mutations.
type FactorWriter interface {
	// EnrollFactor inserts or replaces one enrolled factor.
	EnrollFactor(f EnrolledFactor) error
	// RecordSignCount advances a WebAuthn credential's clone-detection counter.
	RecordSignCount(adminID string, credentialID []byte, count uint32) error
}

// SetWriter attaches a durable backing to the directory.
//
// Idempotent-by-refusal rather than by overwrite: swapping a live store's writer mid-process would
// leave whatever it already holds in the previous backing, and there is no correct place to notice.
func (s *PrincipalStore) SetWriter(w PrincipalWriter) error {
	if w == nil {
		return errors.New("adminidentity: SetWriter(nil) — leave the writer unset for an in-memory directory rather than clearing one")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer != nil {
		return errors.New("adminidentity: this principal directory already has a durable backing — a second one would leave the records written to the first invisible")
	}
	s.writer = w
	return nil
}

// Durable reports whether this directory survives a restart. Used by `adminlaunch` to refuse a
// federated production deployment that quietly has no backing.
func (s *PrincipalStore) Durable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writer != nil
}

// LoadPrincipals replays durably-held principals into the directory WITHOUT persisting them again.
//
// It is a package-level function rather than a method because it is a boot-time act by the assembler,
// not an operation the directory offers at runtime: nothing in a request path may repopulate the
// directory from anywhere.
func LoadPrincipals(s *PrincipalStore, rows []Principal) error {
	if s == nil {
		return errors.New("adminidentity: LoadPrincipals needs a directory")
	}
	for _, p := range rows {
		if strings.TrimSpace(p.AdminID) == "" || strings.TrimSpace(p.SSOSubject) == "" {
			return fmt.Errorf("adminidentity: durable principal row %q is missing an admin_id or an SSO subject", p.AdminID)
		}
		if p.Status == "" {
			return fmt.Errorf("adminidentity: durable principal %s has no status — an unset status is not treated as active", p.AdminID)
		}
		s.mu.Lock()
		if prior, ok := s.byID[p.AdminID]; ok && prior.SSOSubject != p.SSOSubject {
			delete(s.bySubject, prior.SSOSubject)
		}
		s.byID[p.AdminID] = p
		s.bySubject[p.SSOSubject] = p.AdminID
		s.mu.Unlock()
	}
	return nil
}

// SetWriter attaches a durable backing to the enrolment directory. Same refusal as the principal
// directory, for the same reason.
func (s *FactorStore) SetWriter(w FactorWriter) error {
	if w == nil {
		return errors.New("adminidentity: SetWriter(nil) — leave the writer unset for an in-memory enrolment directory rather than clearing one")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer != nil {
		return errors.New("adminidentity: this enrolment directory already has a durable backing — a second one would leave the factors written to the first invisible")
	}
	s.writer = w
	return nil
}

// Durable reports whether enrolments survive a restart.
func (s *FactorStore) Durable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writer != nil
}

// LoadFactors replays durably-held enrolments WITHOUT persisting them again.
func LoadFactors(s *FactorStore, rows []EnrolledFactor) error {
	if s == nil {
		return errors.New("adminidentity: LoadFactors needs a directory")
	}
	for _, f := range rows {
		if strings.TrimSpace(f.AdminID) == "" {
			return errors.New("adminidentity: a durable factor row is missing an admin_id")
		}
		switch f.Kind {
		case FactorWebAuthn:
			if len(f.CredentialID) == 0 || len(f.PublicKeySPKI) == 0 {
				return fmt.Errorf("adminidentity: durable WebAuthn factor for %s is missing its credential id or public key", f.AdminID)
			}
		case FactorTOTP:
			if strings.TrimSpace(f.SecretName) == "" {
				return fmt.Errorf("adminidentity: durable TOTP factor for %s is missing the logical name its seed is held under", f.AdminID)
			}
		default:
			return fmt.Errorf("adminidentity: durable factor for %s has unknown kind %q", f.AdminID, f.Kind)
		}
		s.mu.Lock()
		s.byAdmin[f.AdminID] = append(s.byAdmin[f.AdminID], f)
		s.mu.Unlock()
	}
	return nil
}
