package sourceingest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
)

// memstore.go is the in-process ConnectionStore and snapshot store.
//
// # Why it exists beside the Postgres one
//
// Two reasons, and the second is the one that matters. The first is ordinary: tests need a store, and
// a test that needs a database is a test that does not run on a laptop.
//
// The second is the one this file is written carefully for: it is the SAME contract, so the fences
// that assert the revocation cascade, the retention sweep and the ceilings can run against it in unit
// time AND against real Postgres in the pgproof suite. A memory store that implemented a subtly
// different contract would make the fast fences prove something about a store no customer has.
//
// 🔴 It implements the cascade honestly. `DeleteByConnection` really removes the bytes, so
// `TestRevokeDeletesDerivedTrees` asserting "the tree is absent" is asserting something rather than
// asserting that a map was not consulted.

type memSnapshot struct {
	data         []byte
	connectionID string
	expiresAtMS  int64
}

// MemStore is an in-process ConnectionStore, BundleStore and SnapshotStore.
//
// One type implementing all three because they share a cascade: revoking a connection deletes
// snapshots, and a test that wired three separate fakes would be testing that its own fakes agree.
type MemStore struct {
	mu      sync.Mutex
	conns   map[string]Connection    // connection_id → connection
	records map[string][]CloneRecord // connection_id → ledger, oldest first
	snaps   map[string]memSnapshot   // ref key → snapshot
	pairs   map[string]Pairing       // pairing_id → pairing
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{
		conns:   map[string]Connection{},
		records: map[string][]CloneRecord{},
		snaps:   map[string]memSnapshot{},
		pairs:   map[string]Pairing{},
	}
}

func refKey(r Ref) string { return r.TenantID + "\x00" + r.WorkflowID + "\x00" + r.SourceRevision }

// ── ConnectionStore ─────────────────────────────────────────────────────────────────────────────

// Create records a connection, refusing a second one for the same workflow.
func (m *MemStore) Create(_ context.Context, c Connection) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.conns {
		if existing.TenantID == c.TenantID && existing.WorkflowID == c.WorkflowID {
			return fmt.Errorf("%w: %s already reads %s", ErrConnectionExists, c.WorkflowID, existing.Repository)
		}
	}
	m.conns[c.ConnectionID] = c
	return nil
}

// ForWorkflow returns a workflow's connection.
func (m *MemStore) ForWorkflow(_ context.Context, tenantID, workflowID string) (Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		if c.TenantID == tenantID && c.WorkflowID == workflowID {
			return c, nil
		}
	}
	return Connection{}, ErrNoConnection
}

// List returns a tenant's connections, oldest first.
func (m *MemStore) List(_ context.Context, tenantID string) ([]Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Connection{}
	for _, c := range m.conns {
		if c.TenantID == tenantID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAtMS != out[j].CreatedAtMS {
			return out[i].CreatedAtMS < out[j].CreatedAtMS
		}
		return out[i].ConnectionID < out[j].ConnectionID
	})
	return out, nil
}

// Revoke deletes the grant row and its ledger.
//
// The ledger goes with it, and that is a deliberate answer to a real tension. FR9's record exists so
// the customer can audit a standing capability; keeping it after the grant is gone would mean the
// platform retains a list of a customer's repositories and read times after they asked us to stop
// holding anything. The capability is gone, so the evidence about it goes too — and what remains is
// the aggregate per-forge metric, which names no repository.
func (m *MemStore) Revoke(_ context.Context, tenantID, connectionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[connectionID]
	if !ok || c.TenantID != tenantID {
		return ErrNoConnection
	}
	delete(m.conns, connectionID)
	delete(m.records, connectionID)
	return nil
}

// AppendRecord appends to the ledger. Append-only: there is no update and no delete-one.
func (m *MemStore) AppendRecord(_ context.Context, r CloneRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[r.ConnectionID] = append(m.records[r.ConnectionID], r)
	return nil
}

// Records returns a connection's ledger, newest first.
func (m *MemStore) Records(_ context.Context, tenantID, connectionID string, limit int) ([]CloneRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[connectionID]
	if !ok || c.TenantID != tenantID {
		return nil, ErrNoConnection
	}
	all := m.records[connectionID]
	out := make([]CloneRecord, 0, len(all))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out, nil
}

// ── BundleStore ─────────────────────────────────────────────────────────────────────────────────

// Put records a PUSHED snapshot: no connection, no expiry (PRD §14 A4).
func (m *MemStore) Put(_ context.Context, ref Ref, data []byte) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("sourceingest: refusing an empty bundle for %s", ref)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snaps[refKey(ref)] = memSnapshot{data: append([]byte(nil), data...)}
	return nil
}

// Open returns the snapshot bytes, or ErrNoSource.
func (m *MemStore) Open(_ context.Context, ref Ref) (io.ReadCloser, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snaps[refKey(ref)]
	if !ok {
		return nil, ErrNoSource
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

// Delete removes a snapshot.
func (m *MemStore) Delete(_ context.Context, ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.snaps, refKey(ref))
	return nil
}

// ── SnapshotStore ───────────────────────────────────────────────────────────────────────────────

// PutDerived records a CLONED snapshot with its connection and expiry.
func (m *MemStore) PutDerived(_ context.Context, ref Ref, data []byte, connectionID string, expiresAtMS int64) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if connectionID == "" {
		// A derived snapshot with no connection is a snapshot the cascade cannot find. Refused at the
		// write, where the remedy is obvious, rather than discovered at the revocation that misses it.
		return fmt.Errorf("sourceingest: a derived snapshot must name the connection it came from")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snaps[refKey(ref)] = memSnapshot{data: append([]byte(nil), data...), connectionID: connectionID, expiresAtMS: expiresAtMS}
	return nil
}

// LiveSnapshot reports an unexpired snapshot derived from connectionID.
func (m *MemStore) LiveSnapshot(_ context.Context, ref Ref, connectionID string, nowMS int64) (bool, error) {
	if err := ref.Validate(); err != nil {
		return false, err
	}
	if connectionID == "" {
		return false, fmt.Errorf("sourceingest: LiveSnapshot needs the connection asking")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snaps[refKey(ref)]
	if !ok {
		return false, nil
	}
	// 🔴 A snapshot this connection did not produce is not this connection's to reuse — see the
	// interface's comment. A pushed bundle has an empty connectionID and never matches.
	if s.connectionID != connectionID {
		return false, nil
	}
	// expiresAtMS == 0 means "no expiry" — the pushed-bundle rule, unreachable for a derived row
	// because PutDerived refuses one.
	return s.expiresAtMS == 0 || s.expiresAtMS > nowMS, nil
}

// DeleteByConnection is the cascade.
func (m *MemStore) DeleteByConnection(_ context.Context, tenantID, connectionID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, s := range m.snaps {
		if s.connectionID != connectionID {
			continue
		}
		// The tenant is checked from the ref key's first component, so a connection id guessed from
		// another tenant cannot delete this one's rows.
		if !bytes.HasPrefix([]byte(k), []byte(tenantID+"\x00")) {
			continue
		}
		delete(m.snaps, k)
		n++
	}
	return n, nil
}

// DeleteExpired removes snapshots past their expiry.
func (m *MemStore) DeleteExpired(_ context.Context, nowMS int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, s := range m.snaps {
		if s.expiresAtMS != 0 && s.expiresAtMS <= nowMS {
			delete(m.snaps, k)
			n++
		}
	}
	return n, nil
}

// SnapshotCount reports how many snapshots are held. For fences that must assert absence.
func (m *MemStore) SnapshotCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.snaps)
}

// ── PairingStore ────────────────────────────────────────────────────────────────────────────────

// CreatePairing records a pending pairing, refusing a duplicate code.
func (m *MemStore) CreatePairing(_ context.Context, p Pairing) error {
	if err := p.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pairs == nil {
		m.pairs = map[string]Pairing{}
	}
	for _, existing := range m.pairs {
		if existing.UserCode == p.UserCode {
			// The unique index's behaviour, in memory. Not a formality: the claim resolves by code
			// alone, so a code that meant two things would resolve to whichever row came back first.
			return fmt.Errorf("sourceingest: that pairing code is already in use")
		}
	}
	m.pairs[p.PairingID] = p
	return nil
}

// ClaimPairing moves a pending pairing to paired. Atomic under the store's own lock, so two agents
// racing on one code produce one success and one ErrNoPairing.
func (m *MemStore) ClaimPairing(_ context.Context, userCode, machineName, revision string, atMS int64) (Pairing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.pairs {
		if p.UserCode != userCode {
			continue
		}
		if p.State != PairingPending {
			// Already claimed. Reported as no-such-pairing rather than as "already claimed", for the
			// reason ErrNoPairing gives: distinguishing them lets somebody probe which codes exist.
			return Pairing{}, ErrNoPairing
		}
		if p.StateAt(atMS) == PairingExpired {
			return Pairing{}, ErrPairingExpired
		}
		p.State = PairingPaired
		p.MachineName = machineName
		p.Revision = revision
		p.ClaimedAtMS = atMS
		m.pairs[id] = p
		return p, nil
	}
	return Pairing{}, ErrNoPairing
}

// PairingByID returns one pairing within a tenant, with its expiry applied.
func (m *MemStore) PairingByID(_ context.Context, tenantID, pairingID string, nowMS int64) (Pairing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pairs[pairingID]
	if !ok || p.TenantID != tenantID {
		return Pairing{}, ErrNoPairing
	}
	p.State = p.StateAt(nowMS)
	return p, nil
}

// PairingsForTenant returns a tenant's pairings, newest first.
func (m *MemStore) PairingsForTenant(_ context.Context, tenantID string, nowMS int64) ([]Pairing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Pairing{}
	for _, p := range m.pairs {
		if p.TenantID != tenantID {
			continue
		}
		p.State = p.StateAt(nowMS)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAtMS != out[j].CreatedAtMS {
			return out[i].CreatedAtMS > out[j].CreatedAtMS
		}
		return out[i].PairingID < out[j].PairingID
	})
	return out, nil
}
