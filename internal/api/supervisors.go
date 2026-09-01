package api

import "sync"

// Per-organization supervisors.
//
// # 🔴 Why this exists — the defect it closes
//
// The server used to hold ONE supervisor, built at boot against one organization's slice of the store.
// That was correct while there was exactly one organization, created at boot from the environment.
//
// Self-serve sign-up ended that, and the two halves disagreed in a way nothing reported: `handleAsk`
// CREATES a goal in the caller's own scope (`s.Root.For(tenant)`), while the single supervisor DRIVES
// against the boot organization's scope. For anybody outside that organization the goal was written to
// one place and looked for in another — so the run simply never happened. The browser subscribes, the
// stream stays open and silent, and no error is produced anywhere, because nothing failed: a goal that
// does not exist in the scope being polled is indistinguishable from one with nothing ready to do.
//
// A supervisor is now built per organization, against that organization's scope, so the goal is driven
// where it was written.
//
// # Why a lazily-filled map rather than one built per request
//
// A supervisor is not stateless: it holds the subscriber channels and the replay history that let a
// browser reconnecting mid-run see what it missed. Building one per request would give each request its
// own empty history and its own set of subscribers, so an events stream would never receive anything
// published by the request that started the run.
//
// The map grows by one small struct per organization that has ever asked a question, and is not
// evicted. That is a deliberate non-decision rather than an oversight: eviction would have to know that
// no goal is still running and no browser is still subscribed, and getting that wrong drops a live
// run's events. At the scale where this matters the supervisors belong in a process of their own.
type supervisorSet struct {
	mu    sync.Mutex
	byOrg map[string]*Supervisor
}

// supFor returns this organization's supervisor, building it on first use.
func (s *Server) supFor(tenant string) *Supervisor {
	s.sups.mu.Lock()
	defer s.sups.mu.Unlock()
	if s.sups.byOrg == nil {
		s.sups.byOrg = map[string]*Supervisor{}
	}
	if sup, ok := s.sups.byOrg[tenant]; ok {
		return sup
	}
	sup := s.SupervisorFor(tenant)
	s.sups.byOrg[tenant] = sup
	return sup
}
