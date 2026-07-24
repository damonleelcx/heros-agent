package adminops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// impersonation.go is support impersonation (FR13, design Decision 5): reason-required,
// time-bounded, read-scoped by default, fully audited, with write elevation behind a second
// confirmation.
//
// # Why impersonation is a feature rather than a workaround
//
// Support often has to see what a tenant sees. Doing that by copying a credential or querying tenant
// data ad hoc is an unaudited privacy breach that leaves no trace of who looked at what. Making it a
// first-class capability is the privacy-respecting alternative: the tenant's data is reachable only
// within a stated reason, inside a bounded window, read-only by default, and every action is on the
// record as IMPERSONATION — acting admin plus impersonated tenant — never as the tenant.
//
// # Why "see" and "act as" are separated
//
// A read-scoped session cannot write. Elevation is a distinct capability (Support does not hold it),
// requires the operator to type the tenant identifier, and is itself audited. That separation is the
// point: an operator who can see a tenant's data has not thereby acquired the ability to change it.

// ImpersonationScope is what an impersonation session may do.
type ImpersonationScope string

const (
	// ScopeRead is the default: reads only.
	ScopeRead ImpersonationScope = "read"
	// ScopeElevatedWrite permits writes, after an explicit, typed-confirmation elevation.
	ScopeElevatedWrite ImpersonationScope = "elevated_write"
)

// DefaultImpersonationTTL is how long a session lasts when the caller does not say.
const DefaultImpersonationTTL = 30 * time.Minute

// MaxImpersonationTTL bounds any session. "Time-bounded" that a caller can set to a week is not
// time-bounded, so the ceiling lives in code rather than in a runbook.
const MaxImpersonationTTL = 2 * time.Hour

var (
	// ErrImpersonationNoReason is FR13's first denial: no reason, no session.
	ErrImpersonationNoReason = errors.New("adminops: impersonation requires a recorded reason — no session was started")
	// ErrImpersonationNotFound means the session id names nothing.
	ErrImpersonationNotFound = errors.New("adminops: no such impersonation session")
	// ErrImpersonationExpired means the session's time bound elapsed. It ends automatically; this is
	// what a request arriving afterwards sees.
	ErrImpersonationExpired = errors.New("adminops: the impersonation session has expired")
	// ErrImpersonationReadScoped means a write was attempted in read scope.
	ErrImpersonationReadScoped = errors.New("adminops: this impersonation session is read-scoped — a write requires explicit elevation and a second confirmation")
	// ErrNotYourSession means an admin tried to operate another admin's impersonation session.
	ErrNotYourSession = errors.New("adminops: an impersonation session can only be elevated or ended by the admin who started it")
)

// ImpersonationSession is one bounded window onto a tenant — the design's `impersonation_session`.
type ImpersonationSession struct {
	ID           string             `json:"id"`
	ActorAdminID string             `json:"actor_admin_id"`
	TenantID     string             `json:"tenant_id"`
	Reason       string             `json:"reason"`
	Scope        ImpersonationScope `json:"scope"`
	StartedAt    time.Time          `json:"started_at"`
	ExpiresAt    time.Time          `json:"expires_at"`
	EndedAt      *time.Time         `json:"ended_at,omitempty"`
}

// Live reports whether the session is still usable at now.
func (s ImpersonationSession) Live(now time.Time) bool {
	return s.EndedAt == nil && now.Before(s.ExpiresAt)
}

// RemainingSeconds is how long the session has left — the number the persistent banner counts down
// (FR25). Zero once it has ended or expired.
func (s ImpersonationSession) RemainingSeconds(now time.Time) int {
	if !s.Live(now) {
		return 0
	}
	return int(s.ExpiresAt.Sub(now).Seconds())
}

// BannerText is the operator-facing banner copy, built here rather than in the client so the console
// and the audit trail cannot describe the same session differently. English, pinned wording.
func (s ImpersonationSession) BannerText(now time.Time) string {
	mins := s.RemainingSeconds(now) / 60
	scope := "read-only"
	if s.Scope == ScopeElevatedWrite {
		scope = "WRITE ENABLED"
	}
	return fmt.Sprintf("Impersonating %s, %s, expires in %d min — every action is logged",
		s.TenantID, scope, mins)
}

// ImpersonationService starts, elevates and ends impersonation sessions.
type ImpersonationService struct {
	exec *Executor
	now  func() time.Time

	mu       sync.RWMutex
	sessions map[string]ImpersonationSession
}

// NewImpersonationService wires the service and installs itself as the command path's write guard, so
// a write attempted under a read-scoped session is refused wherever it is attempted rather than only
// on the paths somebody remembered to check.
func NewImpersonationService(exec *Executor) (*ImpersonationService, error) {
	if exec == nil {
		return nil, errors.New("adminops: impersonation needs the command path")
	}
	s := &ImpersonationService{exec: exec, now: exec.Now, sessions: map[string]ImpersonationSession{}}
	exec.SetImpersonationGuard(s)
	return s, nil
}

// Start opens a read-scoped, time-bounded impersonation session.
func (s *ImpersonationService) Start(ctx context.Context, tenantID, reason string, ttl time.Duration, confirm Confirmation) (ImpersonationSession, Receipt, error) {
	if strings.TrimSpace(reason) == "" {
		// Checked BEFORE the command path so the denial is specific: "impersonation needs a reason" is
		// more actionable than the generic reason-required error, and FR13 names this case explicitly.
		return ImpersonationSession{}, Receipt{}, ErrImpersonationNoReason
	}
	if ttl <= 0 {
		ttl = DefaultImpersonationTTL
	}
	if ttl > MaxImpersonationTTL {
		return ImpersonationSession{}, Receipt{}, fmt.Errorf(
			"adminops: an impersonation session may not exceed %s (asked for %s)", MaxImpersonationTTL, ttl)
	}
	id, err := newImpersonationID()
	if err != nil {
		return ImpersonationSession{}, Receipt{}, err
	}
	var started ImpersonationSession
	receipt, err := s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapImpersonateRead,
		Action:     adminaudit.ActionImpersonationStart,
		Target:     TenantTarget(tenantID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{tenantID, string(ScopeRead), ttl.String()},
		Evidence: map[string]string{
			"impersonation_id": id, "tenant_id": tenantID, "scope": string(ScopeRead),
			"ttl_seconds": fmt.Sprint(int(ttl.Seconds())),
		},
	}, func(ctx context.Context) (map[string]string, error) {
		actor, _ := adminSessionFrom(ctx)
		now := s.now()
		sess := ImpersonationSession{
			ID: id, ActorAdminID: actor, TenantID: tenantID, Reason: reason, Scope: ScopeRead,
			StartedAt: now, ExpiresAt: now.Add(ttl),
		}
		s.mu.Lock()
		s.sessions[id] = sess
		s.mu.Unlock()
		started = sess
		return map[string]string{
			"impersonation_id": id, "scope": string(ScopeRead),
			"expires_at": sess.ExpiresAt.UTC().Format(time.RFC3339),
		}, nil
	})
	if err != nil {
		return ImpersonationSession{}, receipt, err
	}
	return started, receipt, nil
}

// Elevate raises a read-scoped session to write scope. Distinct capability, typed second confirmation,
// audited.
func (s *ImpersonationService) Elevate(ctx context.Context, impID, reason string, confirm Confirmation) (ImpersonationSession, Receipt, error) {
	sess, err := s.Session(impID)
	if err != nil {
		return ImpersonationSession{}, Receipt{}, err
	}
	actor, _ := adminSessionFrom(ctx)
	if actor != "" && actor != sess.ActorAdminID {
		return ImpersonationSession{}, Receipt{}, fmt.Errorf("%w (%s started it)", ErrNotYourSession, sess.ActorAdminID)
	}
	var elevated ImpersonationSession
	receipt, err := s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapImpersonateElevate,
		Action:     adminaudit.ActionImpersonationElevate,
		Target:     TenantTarget(sess.TenantID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{impID, string(ScopeElevatedWrite)},
		Evidence: map[string]string{
			"impersonation_id": impID, "tenant_id": sess.TenantID, "scope": string(ScopeElevatedWrite),
		},
	}, func(context.Context) (map[string]string, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		cur, ok := s.sessions[impID]
		if !ok {
			return nil, ErrImpersonationNotFound
		}
		if !cur.Live(s.now()) {
			return nil, ErrImpersonationExpired
		}
		cur.Scope = ScopeElevatedWrite
		s.sessions[impID] = cur
		elevated = cur
		return map[string]string{"impersonation_id": impID, "scope": string(ScopeElevatedWrite)}, nil
	})
	if err != nil {
		return ImpersonationSession{}, receipt, err
	}
	return elevated, receipt, nil
}

// End closes a session before its time bound.
func (s *ImpersonationService) End(ctx context.Context, impID, reason string) (Receipt, error) {
	sess, err := s.Session(impID)
	if err != nil {
		return Receipt{}, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "operator ended the impersonation session"
	}
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapImpersonateRead,
		Action:     adminaudit.ActionImpersonationEnd,
		Target:     TenantTarget(sess.TenantID),
		Reason:     reason,
		Confirm:    Confirm(),
		Params:     []string{impID},
		Evidence:   map[string]string{"impersonation_id": impID, "tenant_id": sess.TenantID},
	}, func(context.Context) (map[string]string, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		cur, ok := s.sessions[impID]
		if !ok {
			return nil, ErrImpersonationNotFound
		}
		if cur.EndedAt == nil {
			now := s.now()
			cur.EndedAt = &now
			s.sessions[impID] = cur
		}
		return map[string]string{"impersonation_id": impID, "ended": "true"}, nil
	})
}

// Session resolves one session, applying auto-expiry: a session past its bound is reported as ended
// rather than as live-but-stale, so "the session ends automatically" is a property of the read.
func (s *ImpersonationService) Session(impID string) (ImpersonationSession, error) {
	s.mu.RLock()
	sess, ok := s.sessions[impID]
	s.mu.RUnlock()
	if !ok {
		return ImpersonationSession{}, fmt.Errorf("%w: %s", ErrImpersonationNotFound, impID)
	}
	if sess.EndedAt == nil && !s.now().Before(sess.ExpiresAt) {
		return sess, fmt.Errorf("%w: %s", ErrImpersonationExpired, impID)
	}
	return sess, nil
}

// Active returns the caller's live sessions — what the console's persistent banner renders.
func (s *ImpersonationService) Active(ctx context.Context) ([]ImpersonationSession, error) {
	actor, ok := adminSessionFrom(ctx)
	if !ok {
		return nil, errors.New("adminops: no admin session on this request")
	}
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ImpersonationSession
	for _, sess := range s.sessions {
		if sess.ActorAdminID == actor && sess.Live(now) {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// AllActive returns every live session across admins — the "active impersonations" operational
// signal task 13.1 alerts on.
func (s *ImpersonationService) AllActive() []ImpersonationSession {
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ImpersonationSession
	for _, sess := range s.sessions {
		if sess.Live(now) {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RecordRead audits one READ taken while impersonating.
//
// Writes go through the command path, which audits them with the impersonation id automatically.
// Reads do not — they have no command — so FR13's "every action during the session is logged as
// impersonation" needs this explicit call on the read paths the impersonated console serves.
func (s *ImpersonationService) RecordRead(ctx context.Context, impID, whatWasRead string) error {
	sess, err := s.Session(impID)
	if err != nil {
		return err
	}
	actor, _ := adminSessionFrom(ctx)
	_, err = s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID:    actor,
		Target:          TenantTarget(sess.TenantID),
		Action:          adminaudit.ActionImpersonatedAction,
		Reason:          sess.Reason,
		Result:          "read",
		ImpersonationID: impID,
		Evidence:        map[string]string{"read": whatWasRead, "scope": string(sess.Scope), "tenant_id": sess.TenantID},
		CreatedAt:       s.now(),
	})
	if err != nil {
		// Fail closed: an impersonated read that cannot be logged is an unaudited look at tenant data,
		// which is the exact thing impersonation exists to prevent.
		return fmt.Errorf("adminops: impersonated read not permitted — it could not be audited: %w", err)
	}
	return nil
}

// AllowWrite implements the command path's ImpersonationGuard.
func (s *ImpersonationService) AllowWrite(impID string) (bool, string, error) {
	sess, err := s.Session(impID)
	if err != nil {
		return false, "impersonation session is not live", err
	}
	if sess.Scope != ScopeElevatedWrite {
		return false, "the session is read-scoped", nil
	}
	return true, "", nil
}

// newImpersonationID draws a random session id.
func newImpersonationID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("adminops: cannot draw an impersonation id: %w", err)
	}
	return "imp-" + hex.EncodeToString(buf), nil
}
