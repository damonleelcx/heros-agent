package sandbox

import (
	"context"
	"fmt"
	"sync"
)

// CONCURRENCY — the sandbox's OWN limit, and why the spec's is not enough (P34 tasks 4.4, 8.3, 9.12)
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
// P34 lets a spec declare that members of a group may run concurrently. Concurrency multiplies a run's
// PEAK resource use by the group's width — one isolate's bounds are per-isolate, so four overlapping
// isolates is four times the address space, four times the PIDs, four times the output buffer — which
// makes the width a blast-radius statement rather than a scheduling hint.
//
// # Two gates, and the reason there must be two
//
// `variantspec.checkConcurrencyLimit` refuses a group wider than the envelope's limit at RESOLVE. That
// gate is early and legible, and it is the one an author sees. It is also the one that is BYPASSED by
// every path that reaches an executor without resolving a spec: a `Resolved` assembled by hand, a spec
// resolved under an older binary, a caller that built a Spec directly.
//
// 🔴 So the sandbox enforces its own, and it does NOT trust the number it is handed. `Limit` is the
// MINIMUM of what the spec declared and what the sandbox was configured to allow, so a Spec claiming a
// width of 100 is admitted 4 at a time on a sandbox configured for 4. Task 9.12 asserts both gates,
// because a limit with one entrance is a limit with a way around it.
//
// # Why a per-GROUP gate and not a global one
//
// A global semaphore would make two unrelated runs contend, so a tenant with a wide group would slow
// every other tenant's narrow ones — and the number an operator set as a blast-radius bound would turn
// into a throughput knob that behaves differently depending on who else is running. The gate is keyed by
// (run, group), which is the scope the envelope's limit is a statement about.

// ConcurrencyKey identifies the group a Spec belongs to. The zero value means "not part of a group",
// and such a Spec takes no slot at all — a node that is not declared concurrent with anything is not
// competing with anything.
type ConcurrencyKey struct {
	RunID string
	Group string
}

// InGroup reports whether this key names a group.
func (k ConcurrencyKey) InGroup() bool { return k.RunID != "" && k.Group != "" }

// SandboxConcurrencyCeiling is the most isolates this sandbox will run at once for ONE group,
// regardless of what any spec declares.
//
// 🔴 It is a constant here rather than a parameter, and that is the point of task 4.4's word
// "independently": a ceiling supplied by the same caller that supplies the width is not an independent
// gate, it is the same gate written twice. An operator raising it is a deployment change to this
// process, which is the level a resource bound belongs at.
//
// The value is a policy choice. Eight overlapping isolates at DefaultBounds is 32 GiB of address space
// and 1024 PIDs — already a materially larger footprint than one — and a width a caller has to argue
// past is the point of having a ceiling.
const SandboxConcurrencyCeiling = 8

// ConcurrencyHealth is what a readable health endpoint publishes about overlapping isolates
// (task 8.3). Counts only: no node id, no tenant, no repository — a readiness probe is public by
// necessity, so everything it says is said to everybody.
type ConcurrencyHealth struct {
	// Active is how many isolates are executing right now, across every group.
	Active int `json:"active"`
	// Peak is the high-water mark of Active since this process started. 🔴 This is the number task 8.3
	// asks for: "peak resource use per run" is not measurable from a current gauge, because the moment
	// that matters has already passed by the time anybody looks.
	Peak int `json:"peak"`
	// PeakGroupWidth is the widest a SINGLE group ever got. Reported beside Peak because they answer
	// different questions: Peak says how loaded the box got, this says whether any one configuration is
	// wide enough to be the reason.
	PeakGroupWidth int `json:"peak_group_width"`
	// Ceiling is the refusal point, reported so "active: 6" means something without reading the source.
	Ceiling int `json:"ceiling"`
	// Capped is how many admissions were narrowed because a spec asked for more than the ceiling allows.
	//
	// 🔴 A NON-ZERO value here is the signal that the resolve-time gate was bypassed — a spec that had
	// been resolved would have been refused before it got here. It is published rather than logged
	// because "the early gate is not running" is exactly the class of fact that is invisible in every
	// aggregate: nothing errors, nothing retries, the work simply runs narrower than it asked to.
	Capped int `json:"capped"`
}

// concurrencyGate admits at most N isolates per group.
type concurrencyGate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active map[ConcurrencyKey]int
	// total, peak and peakWidth are the health gauge. Maintained under the same lock as `active`, so a
	// reader cannot observe a peak lower than a count it can already see.
	total     int
	peak      int
	peakWidth int
	capped    int
}

func newConcurrencyGate() *concurrencyGate {
	g := &concurrencyGate{active: map[ConcurrencyKey]int{}}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// effectiveLimit is the number actually enforced: the minimum of what the caller declared and what this
// sandbox allows, with a caller that declared nothing getting the sandbox's own ceiling.
//
// 🔴 It returns whether it NARROWED, so the caller can count it. See ConcurrencyHealth.Capped: a
// narrowing means a spec reached execution without passing the resolve-time gate, and that is a fact
// about the deployment rather than about the spec.
func effectiveLimit(declared int) (limit int, narrowed bool) {
	if declared <= 0 {
		return SandboxConcurrencyCeiling, false
	}
	if declared > SandboxConcurrencyCeiling {
		return SandboxConcurrencyCeiling, true
	}
	return declared, false
}

// acquire blocks until the group has room, or the context is done.
//
// 🚫 It BLOCKS rather than refusing. A refusal here would turn a resource bound into a correctness
// failure: the caller asked for work that is entirely legitimate and merely has to wait its turn, and a
// node that failed because its three siblings were still running would be a flake with a plausible
// error message. The context is the deadline — `ResourceBounds.Wallclock` already bounds how long any
// of this may take, so waiting cannot be unbounded.
func (g *concurrencyGate) acquire(ctx context.Context, key ConcurrencyKey, declared int) (release func(), err error) {
	if !key.InGroup() {
		return func() {}, nil
	}
	limit, narrowed := effectiveLimit(declared)

	// A context cancelled while parked has to wake the waiter. sync.Cond has no context form, so a
	// watchdog broadcasts on cancellation; it exits as soon as the wait is over.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			g.mu.Lock()
			g.cond.Broadcast()
			g.mu.Unlock()
		case <-done:
		}
	}()

	g.mu.Lock()
	if narrowed {
		g.capped++
	}
	for g.active[key] >= limit {
		if ctx.Err() != nil {
			g.mu.Unlock()
			return nil, fmt.Errorf("%w: waiting for a slot in concurrent group %q (limit %d): %v",
				ErrIsolateUnavailable, key.Group, limit, ctx.Err())
		}
		g.cond.Wait()
	}
	g.active[key]++
	g.total++
	if g.active[key] > g.peakWidth {
		g.peakWidth = g.active[key]
	}
	if g.total > g.peak {
		g.peak = g.total
	}
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.active[key]--
			if g.active[key] <= 0 {
				delete(g.active, key) // a finished group must not leak a map entry per run
			}
			g.total--
			g.cond.Broadcast()
			g.mu.Unlock()
		})
	}, nil
}

func (g *concurrencyGate) health() ConcurrencyHealth {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ConcurrencyHealth{
		Active: g.total, Peak: g.peak, PeakGroupWidth: g.peakWidth,
		Ceiling: SandboxConcurrencyCeiling, Capped: g.capped,
	}
}

// Concurrency snapshots the gate for a health endpoint. Takes the lock for the length of a struct copy
// and never blocks on I/O — a readiness probe behind the same exhaustible resource as the isolates
// would be measuring its own starvation.
func (s *Sandbox) Concurrency() ConcurrencyHealth { return s.concurrency.health() }
