package adminops

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// killswitch.go is the platform's brake on its most autonomous actor (FR12, design Decision 4):
// a GLOBAL and a PER-TENANT kill switch that halt further autonomous merges immediately, with no
// deploy, read fail-closed to halt.
//
// # Why "fail closed to halt" is the whole design
//
// The state is consulted by the P6 loop before every merge. If it cannot be read, the loop must NOT
// merge. That is the AI-safety default stated plainly: "can't tell if we're stopped" means stopped.
// It is expressed here as an ERROR return from every read path rather than a boolean, because a
// boolean forces the reader to invent an answer, and the convenient invention is "false" — which is
// exactly the wrong one.
//
// # Why global and per-tenant are the same mechanism with different friction
//
// One state store, one read, one enforcement point — so the two cannot drift. What differs is the
// FRICTION: arming global is a safe direction (halt) and takes one operator with a reason; DISARMING
// global resumes the whole fleet, which is the dangerous direction, and takes a second operator's
// approval when policy says so. The console renders the global control differently for the same
// reason (FR24): "halt this tenant" must never be mistaken for "halt the fleet".

// ScopeGlobal is the fleet-wide kill-switch scope.
const ScopeGlobal = TargetGlobal

// TenantScope renders a per-tenant kill-switch scope. It is deliberately the same string a tenant
// target uses, so an audit entry's target and a kill-switch scope are the same identifier.
func TenantScope(tenantID string) string { return TenantTarget(tenantID) }

// KillSwitchState is one scope's state — the design's `kill_switch_state` row.
type KillSwitchState struct {
	Scope  string `json:"scope"`
	Armed  bool   `json:"armed"`
	SetBy  string `json:"set_by,omitempty"`
	Reason string `json:"reason,omitempty"`
	SetAt  string `json:"set_at,omitempty"`
}

// KillSwitchStore holds kill-switch state.
//
// Every method can return an error, and an error means INDETERMINATE — never "not armed". A store
// implementation that swallows a read failure and returns a zero state defeats the entire mechanism.
type KillSwitchStore interface {
	// Get returns one scope's state. A scope that was never set is returned un-armed with no error;
	// "never armed" is a known answer, unlike "cannot read".
	Get(scope string) (KillSwitchState, error)
	// Put writes one scope's state.
	Put(state KillSwitchState) error
	// All returns every scope with a state, for the console's fleet view.
	All() ([]KillSwitchState, error)
	// Describe names the store for the readiness surface.
	Describe() string
}

// ErrKillStateIndeterminate is what a caller sees when the state cannot be read. Its only correct
// handling is to halt.
var ErrKillStateIndeterminate = errors.New("adminops: kill-switch state indeterminate — merges fail closed to halt")

// MemKillSwitchStore is the in-process state store. The durable shape is the `kill_switch_state`
// table; both answer the same question and both can be unreachable, which is why the interface has an
// error on every method rather than only on the durable one.
type MemKillSwitchStore struct {
	mu     sync.RWMutex
	states map[string]KillSwitchState
	// unreachable makes every read fail. It is a first-class operational state, not a test hook: the
	// fail-closed behaviour is only real if it can be exercised, and the staging drill (task 13.4)
	// uses the same switch.
	unreachable bool
}

// NewMemKillSwitchStore builds an empty store.
func NewMemKillSwitchStore() *MemKillSwitchStore {
	return &MemKillSwitchStore{states: map[string]KillSwitchState{}}
}

// SetUnreachable takes the store offline (or brings it back).
func (s *MemKillSwitchStore) SetUnreachable(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unreachable = down
}

// Get implements KillSwitchStore.
func (s *MemKillSwitchStore) Get(scope string) (KillSwitchState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.unreachable {
		return KillSwitchState{}, fmt.Errorf("%w: %s", ErrKillStateIndeterminate, scope)
	}
	st, ok := s.states[scope]
	if !ok {
		return KillSwitchState{Scope: scope}, nil
	}
	return st, nil
}

// Put implements KillSwitchStore.
func (s *MemKillSwitchStore) Put(state KillSwitchState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return fmt.Errorf("%w: %s", ErrKillStateIndeterminate, state.Scope)
	}
	s.states[state.Scope] = state
	return nil
}

// All implements KillSwitchStore.
func (s *MemKillSwitchStore) All() ([]KillSwitchState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.unreachable {
		return nil, ErrKillStateIndeterminate
	}
	out := make([]KillSwitchState, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out, nil
}

// Describe implements KillSwitchStore.
func (s *MemKillSwitchStore) Describe() string { return "memory:kill_switch_state" }

// KillSwitchPolicy holds the settable policy defaults (task 7.1).
type KillSwitchPolicy struct {
	// GlobalDisarmRequiresTwoPerson makes resuming the whole fleet require a SECOND admin's approval
	// (design Open Q2). Arming is the safe direction and always takes one operator; disarming globally
	// is the one that can restart a runaway fleet.
	GlobalDisarmRequiresTwoPerson bool
	// ArmedAtStartup are scopes a deployment boots halted. A deployment mid-incident wants its
	// processes to come back stopped, not to resume merging because a pod restarted.
	ArmedAtStartup []string
}

// DefaultKillSwitchPolicy is the platform's default: two-person global disarm, nothing armed at boot.
func DefaultKillSwitchPolicy() KillSwitchPolicy {
	return KillSwitchPolicy{GlobalDisarmRequiresTwoPerson: true}
}

// ErrSecondApproverRequired is returned when a global disarm arrives without a second approver.
var ErrSecondApproverRequired = errors.New("adminops: disarming the global kill switch resumes every tenant's autonomous merges and requires a second approver")

// KillSwitchService is the operator's kill-switch surface, and the P6 loop's state reader.
type KillSwitchService struct {
	exec   *Executor
	store  KillSwitchStore
	policy KillSwitchPolicy
	now    func() time.Time
}

// NewKillSwitchService wires the service and applies the policy's startup arming.
func NewKillSwitchService(exec *Executor, store KillSwitchStore, policy KillSwitchPolicy) (*KillSwitchService, error) {
	if exec == nil || store == nil {
		return nil, errors.New("adminops: the kill switch needs the command path and a state store")
	}
	s := &KillSwitchService{exec: exec, store: store, policy: policy, now: exec.Now}
	for _, scope := range policy.ArmedAtStartup {
		if err := store.Put(KillSwitchState{
			Scope: scope, Armed: true, SetBy: "policy:armed_at_startup",
			Reason: "policy default: this deployment boots halted", SetAt: s.now().UTC().Format(time.RFC3339),
		}); err != nil {
			return nil, fmt.Errorf("adminops: applying the startup kill-switch policy: %w", err)
		}
	}
	return s, nil
}

// Policy reports the live policy defaults, so the console can render why a control is higher-friction
// rather than asserting it in copy that can drift.
func (s *KillSwitchService) Policy() KillSwitchPolicy { return s.policy }

// State returns one scope's state. Permission-gated on the kill-switch capability: who may see the
// brake is the same question as who may pull it.
func (s *KillSwitchService) State(ctx context.Context, scope string) (KillSwitchState, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapKillSwitch, scope); err != nil {
		return KillSwitchState{}, err
	}
	return s.store.Get(scope)
}

// States returns every scope with a state.
func (s *KillSwitchService) States(ctx context.Context) ([]KillSwitchState, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapKillSwitch, ScopeGlobal); err != nil {
		return nil, err
	}
	return s.store.All()
}

// Arm halts autonomous merges for a scope. Effective immediately, with no deploy: the P6 loop reads
// this state before every merge, so the next merge attempt anywhere sees it.
func (s *KillSwitchService) Arm(ctx context.Context, scope, reason string, confirm Confirmation) (Receipt, error) {
	if err := validScope(scope); err != nil {
		return Receipt{}, err
	}
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapKillSwitch,
		Action:     adminaudit.ActionKillSwitchArm,
		Target:     scope,
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{scope, "armed"},
		Evidence:   map[string]string{"scope": scope, "armed": "true", "store": s.store.Describe()},
	}, func(context.Context) (map[string]string, error) {
		st := KillSwitchState{
			Scope: scope, Armed: true, SetBy: actorOf(ctx), Reason: reason,
			SetAt: s.now().UTC().Format(time.RFC3339),
		}
		if err := s.store.Put(st); err != nil {
			return nil, err
		}
		return map[string]string{"scope": scope, "armed": "true", "effective": "immediately, no deploy"}, nil
	})
}

// Disarm resumes autonomous merges for a scope.
//
// secondApprover is the other admin who approved a GLOBAL disarm. It is ignored for a per-tenant
// scope, required for global when policy says so, and must be a DIFFERENT admin who also holds the
// kill-switch capability — a second confirmation from the same person is not a second person.
func (s *KillSwitchService) Disarm(ctx context.Context, scope, reason string, confirm Confirmation, secondApprover string) (Receipt, error) {
	if err := validScope(scope); err != nil {
		return Receipt{}, err
	}
	if scope == ScopeGlobal && s.policy.GlobalDisarmRequiresTwoPerson {
		if err := s.checkSecondApprover(ctx, secondApprover); err != nil {
			return Receipt{}, err
		}
	}
	evidence := map[string]string{"scope": scope, "armed": "false", "store": s.store.Describe()}
	if secondApprover != "" {
		evidence["second_approver"] = secondApprover
	}
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapKillSwitch,
		Action:     adminaudit.ActionKillSwitchDisarm,
		Target:     scope,
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{scope, "disarmed", secondApprover},
		Evidence:   evidence,
	}, func(context.Context) (map[string]string, error) {
		st := KillSwitchState{
			Scope: scope, Armed: false, SetBy: actorOf(ctx), Reason: reason,
			SetAt: s.now().UTC().Format(time.RFC3339),
		}
		if err := s.store.Put(st); err != nil {
			return nil, err
		}
		return map[string]string{"scope": scope, "armed": "false"}, nil
	})
}

// checkSecondApprover verifies the named approver is a different admin who also holds the capability.
func (s *KillSwitchService) checkSecondApprover(ctx context.Context, approver string) error {
	approver = strings.TrimSpace(approver)
	if approver == "" {
		return ErrSecondApproverRequired
	}
	actor := actorOf(ctx)
	if approver == actor {
		return fmt.Errorf("%w: %q approved their own disarm", ErrSecondApproverRequired, approver)
	}
	if d := s.exec.Gate().Authorize(approver, adminrbac.CapKillSwitch, ScopeGlobal); !d.Allowed {
		return fmt.Errorf("%w: %q does not hold the kill-switch capability", ErrSecondApproverRequired, approver)
	}
	return nil
}

// HaltsMerge implements KillStateReader — the question the P6 loop asks before every merge.
//
// Global is checked FIRST and a read failure at either level is INDETERMINATE. There is no branch
// here that turns an unreadable state into "go ahead".
func (s *KillSwitchService) HaltsMerge(tenantID string) (bool, string, error) {
	global, err := s.store.Get(ScopeGlobal)
	if err != nil {
		return true, "kill-switch state indeterminate", err
	}
	if global.Armed {
		reason := "global kill switch armed"
		if global.Reason != "" {
			reason += ": " + global.Reason
		}
		return true, reason, nil
	}
	if tenantID == "" {
		return false, "", nil
	}
	per, err := s.store.Get(TenantScope(tenantID))
	if err != nil {
		return true, "kill-switch state indeterminate", err
	}
	if per.Armed {
		reason := "tenant kill switch armed"
		if per.Reason != "" {
			reason += ": " + per.Reason
		}
		return true, reason, nil
	}
	return false, "", nil
}

// validScope rejects a scope that is neither global nor a tenant scope, so a typo halts nothing
// silently instead of creating a scope nothing reads.
func validScope(scope string) error {
	if scope == ScopeGlobal {
		return nil
	}
	if TenantOf(scope) != "" {
		return nil
	}
	return fmt.Errorf("adminops: %q is not a kill-switch scope (want %q or %q)", scope, ScopeGlobal, TenantScope("<id>"))
}

// actorOf reads the acting admin from the request context. It returns "" when there is no session,
// which cannot happen on a path that reached an effect — Execute has already required one.
func actorOf(ctx context.Context) string {
	if s, ok := adminSessionFrom(ctx); ok {
		return s
	}
	return ""
}
