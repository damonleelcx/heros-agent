package herosagent

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// caps.go is task 9.2: the token ceiling, checked BEFORE the provider call.
//
// ── 🔴 P36: THE CEILING IS PER ASSESSMENT, NOT PER NODE. Adding a node does not raise the budget. ──
//
// Design D6 / decisions.md D-36.6, restated in the file it governs because a multi-node definition is
// the first thing that makes a per-node ceiling look reasonable — and it is the natural next edit.
//
// A per-node ceiling means a TOPOLOGY CHANGE SILENTLY CHANGES A BUDGET. That is the least visible way
// for a system to start spending more money: nobody edited a budget, the number that moved is one
// nobody was watching, and the change that moved it was about graph shape.
//
// 🔴 Nothing in this file is keyed by node, and that absence is the mechanism. `Cap` is per tenant (or
// fleet); `CapChecker.Check` takes a tenant id and nothing else. There is no node parameter to pass,
// so a caller who wanted a per-node ceiling would have to add one — which is a visible edit rather than
// a default that drifted.
//
// 🚫 The consequence is accepted rather than mitigated: a definition with many nodes may exhaust its
// ceiling MID-ASSESSMENT. That is correct. `Runner.inferGraph` re-checks this ceiling before EVERY
// node's provider call — not because the ceiling is per node, but because the earlier nodes' spend must
// be visible to the later nodes' check — and exhaustion degrades to `not_measured` with `budget
// exhausted`, the state P33 already defines, NAMING THE NODE it stopped at.
//
// # Why "before" is the whole task
//
// A cap enforced after a call is an accounting record, not a cap. The tokens are spent, the bill is
// incurred, and what the check produces is a slightly faster stop next time — which is exactly the
// behaviour of having no cap at all on the run that mattered. The per-run `Budget` already bounds ONE
// inference; this bounds what a tenant and the fleet may spend across them, which is the number that
// appears on an invoice.
//
// # Why the window is a parameter and not a calendar month
//
// A calendar month makes the cap reset at a moment nobody chose and makes "how much is left" depend on
// today's date — so the same configuration behaves differently on the 1st and the 28th, and a test
// written on one of those days passes for reasons it does not state. A rolling window of a stated
// length is the same rule every day, and `TestsMustNotHaveASecondClock`'s lesson applies: the clock is
// injected, so a test asserts on elapsed milliseconds rather than on when it happens to run.

// CapWindow is the rolling period a cap is measured over. Exported so an operator surface and the
// checker cannot disagree about what "spend so far" means.
const CapWindow = 30 * 24 * time.Hour

// Cap is one ceiling, as an operator set it.
type Cap struct {
	// TenantID is the tenant this applies to, or "" for the FLEET-WIDE ceiling.
	TenantID    string
	MaxTokens   int64
	Reason      string
	SetBy       string
	UpdatedAtMS int64
}

// FleetTenantID is the sentinel under which the fleet-wide cap is stored.
//
// A constant rather than a bare `""` at each call site, because an empty string is what a caller passes
// by accident and a named one is what they pass on purpose — and the two are indistinguishable in a
// query written at 6pm.
const FleetTenantID = ""

// CapStore reads and writes ceilings.
type CapStore interface {
	// Get returns one ceiling. ok=false is NO CAP, which means unbounded — a real and dangerous state
	// that the caller must not read as zero.
	Get(ctx context.Context, tenantID string) (Cap, bool, error)
	Set(ctx context.Context, c Cap) error
	// Delete removes a ceiling. Removing IS a delete rather than a write of zero, because `0` is
	// ambiguous between "spend nothing" and "no limit" and the schema refuses it.
	Delete(ctx context.Context, tenantID string) error
	List(ctx context.Context) ([]Cap, error)
}

// SpendReader reports tokens already spent over the cap window.
type SpendReader interface {
	// SpentSince returns tokens spent by one tenant since a timestamp, or by the WHOLE FLEET when
	// tenantID is FleetTenantID.
	SpentSince(ctx context.Context, tenantID string, sinceMS int64) (int64, error)
	// Record writes what an inference spent. Called after a run, so the next check sees it.
	Record(ctx context.Context, s Spend) error
}

// Spend is one inference's meter reading.
type Spend struct {
	TenantID    string
	InferenceID string
	TokensIn    int64
	TokensOut   int64
	// EstimatedCost is meaningful ONLY when Priced. A model with no published price produces real
	// tokens and no cost, and a zero here would report a spend nobody incurred.
	EstimatedCost float64
	Priced        bool
	CreatedAtMS   int64
}

// CapVerdict is what a check answered, with everything a refusal has to say.
type CapVerdict struct {
	// Allowed is false when a ceiling is reached. 🚫 There is no "allowed with a warning": a check that
	// let a call through while reporting a problem is a check nothing acts on.
	Allowed bool
	// Scope names WHICH ceiling stopped this — the tenant's or the fleet's. An operator who raises the
	// wrong one has not raised anything, and "cap reached" without a scope is a message that sends them
	// to guess.
	Scope string
	// Limit and Spent are the two numbers a decision is made from.
	Limit int64
	Spent int64
	// Reason is the operator's own note from the cap row, carried through so the person who set the
	// ceiling gets to explain it to the person who hits it.
	Reason string
}

// CapScopes. A closed pair: a refusal is the tenant's ceiling or the fleet's, never both and never
// unattributed.
const (
	CapScopeTenant = "tenant"
	CapScopeFleet  = "fleet"
)

// CapChecker answers "may this tenant spend anything right now".
type CapChecker struct {
	caps   CapStore
	spend  SpendReader
	nowMS  func() int64
	window time.Duration
}

// NewCapChecker wires a checker.
//
// 🔴 Both stores are required. A checker with a nil cap store would answer "allowed" for everything —
// a fence that is off is worse than absent, because the surface still reports that caps are enforced.
func NewCapChecker(caps CapStore, spend SpendReader, nowMS func() int64) (*CapChecker, error) {
	switch {
	case caps == nil:
		return nil, fmt.Errorf("%w: a cap store is required — a checker without one answers `allowed` "+
			"for every call while every surface reports that caps are enforced", ErrInvalidDefinition)
	case spend == nil:
		return nil, fmt.Errorf("%w: a spend reader is required — a ceiling with nothing to measure "+
			"against is a number nobody is under", ErrInvalidDefinition)
	case nowMS == nil:
		return nil, fmt.Errorf("%w: a clock is required", ErrInvalidDefinition)
	}
	return &CapChecker{caps: caps, spend: spend, nowMS: nowMS, window: CapWindow}, nil
}

// Check answers whether a tenant may spend, reading the TENANT ceiling and the FLEET ceiling.
//
// 🔴 Both, and the tenant's is checked first so its own reason reaches whoever hit it. A fleet ceiling
// reached by one noisy tenant stops every other tenant too — which is correct and is the reason the
// verdict names the scope: an operator raising the wrong one has changed nothing.
//
// # 🔴 `pendingTokens` — what the CALLER has already spent that the meter has not seen yet
//
// This parameter exists because P36's multi-node runner found the hole it closes, and the hole was
// invisible until a definition had more than one node.
//
// The meter is written ONCE per assessment, after it completes — deliberately, because `Spend` is keyed
// by inference and a half-finished assessment has no inference id. So inside one assessment, every
// node's cap check read the same stale total and every node passed. A four-node definition under a
// ten-token ceiling spent thirty-two: the check ran four times and learned nothing between them, which
// is precisely "a cap enforced once at the top is an accounting record for every node after the first"
// — the failure this file's header claims to prevent, reintroduced by a shape change.
//
// 🔴 It is a REQUIRED parameter rather than an optional variant, and that is the point: the compiler
// makes every caller answer "what have I spent that the meter cannot see". A `CheckWithPending`
// alongside `Check` would leave the old call sites answering `nothing` by omission — which is the
// answer that was already wrong.
//
// 🚫 It is NOT added to the meter. Nothing about this call records spend; it only refuses to ignore
// spend that has already happened.
func (c *CapChecker) Check(ctx context.Context, tenantID string, pendingTokens int64) (CapVerdict, error) {
	since := c.nowMS() - c.window.Milliseconds()

	for _, scope := range []struct {
		name string
		id   string
	}{
		{CapScopeTenant, tenantID},
		{CapScopeFleet, FleetTenantID},
	} {
		cap, ok, err := c.caps.Get(ctx, scope.id)
		if err != nil {
			return CapVerdict{}, err
		}
		if !ok {
			// 🔴 NO CAP MEANS UNBOUNDED, and this is where that dangerous default actually bites. It is
			// not softened here — inventing a ceiling at the check would be a limit nobody chose,
			// applied silently, and the first time it bit somebody it would look like a product limit.
			// `/readyz` and the spend console both report an unset fleet cap as a state.
			continue
		}
		spent, err := c.spend.SpentSince(ctx, scope.id, since)
		if err != nil {
			return CapVerdict{}, err
		}
		spent += pendingTokens
		if spent >= cap.MaxTokens {
			return CapVerdict{
				Allowed: false, Scope: scope.name,
				Limit: cap.MaxTokens, Spent: spent, Reason: cap.Reason,
			}, nil
		}
	}
	// 🔴 An ALLOWED verdict carries the tenant's ceiling and spend too, not just `true`. A surface
	// asking "how much headroom is there" would otherwise have to read the cap store itself, which is a
	// second reader of the same two numbers — and two readers of one fact eventually disagree about the
	// window they are measured over.
	return c.allowedVerdict(ctx, tenantID, pendingTokens, since)
}

// allowedVerdict fills in the numbers behind an allowed answer. A read failure here does NOT turn an
// allowance into a refusal: the decision was already made from the same values, and failing now would
// deny a call for want of a display figure.
func (c *CapChecker) allowedVerdict(ctx context.Context, tenantID string, pending, since int64) (CapVerdict, error) {
	out := CapVerdict{Allowed: true, Scope: CapScopeTenant}
	cap, ok, err := c.caps.Get(ctx, tenantID)
	if err != nil || !ok {
		return CapVerdict{Allowed: true}, nil
	}
	spent, err := c.spend.SpentSince(ctx, tenantID, since)
	if err != nil {
		return CapVerdict{Allowed: true}, nil
	}
	out.Limit, out.Spent, out.Reason = cap.MaxTokens, spent+pending, cap.Reason
	return out, nil
}

// CapError renders a refusal as the sentence an operator and a customer both read.
func (v CapVerdict) CapError() error {
	if v.Allowed {
		return nil
	}
	detail := ""
	if v.Reason != "" {
		detail = " The ceiling was set with this note: " + v.Reason
	}
	return fmt.Errorf("%w: the %s token ceiling is reached — %d spent against a limit of %d over the "+
		"last %d days. No provider call was made.%s",
		ErrCapReached, v.Scope, v.Spent, v.Limit, int(CapWindow.Hours()/24), detail)
}

// ── in-memory stores ─────────────────────────────────────────────────────────────────────────────

// MemCapStore is the in-memory cap store, for tests and a deployment with no platform database.
type MemCapStore struct {
	mu sync.RWMutex
	m  map[string]Cap
}

// NewMemCapStore returns an empty in-memory cap store.
func NewMemCapStore() *MemCapStore { return &MemCapStore{m: map[string]Cap{}} }

func (s *MemCapStore) Get(_ context.Context, tenantID string) (Cap, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m[tenantID]
	return c, ok, nil
}

func (s *MemCapStore) Set(_ context.Context, c Cap) error {
	if c.MaxTokens <= 0 {
		return fmt.Errorf("%w: a ceiling of %d is not a ceiling. `0` is ambiguous between `spend "+
			"nothing` and `no limit`; removing a cap is a delete", ErrInvalidDefinition, c.MaxTokens)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[c.TenantID] = c
	return nil
}

func (s *MemCapStore) Delete(_ context.Context, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tenantID)
	return nil
}

func (s *MemCapStore) List(_ context.Context) ([]Cap, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Cap, 0, len(s.m))
	for _, c := range s.m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}

// MemSpendStore is the in-memory meter.
type MemSpendStore struct {
	mu sync.RWMutex
	m  []Spend
}

// NewMemSpendStore returns an empty in-memory meter.
func NewMemSpendStore() *MemSpendStore { return &MemSpendStore{} }

func (s *MemSpendStore) Record(_ context.Context, sp Spend) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = append(s.m, sp)
	return nil
}

// SpentSince sums tokens over the window. A FleetTenantID sums every tenant.
func (s *MemSpendStore) SpentSince(_ context.Context, tenantID string, sinceMS int64) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for _, sp := range s.m {
		if sp.CreatedAtMS < sinceMS {
			continue
		}
		if tenantID != FleetTenantID && sp.TenantID != tenantID {
			continue
		}
		total += sp.TokensIn + sp.TokensOut
	}
	return total, nil
}
