// Package adminaudit is P8's append-only, hash-chained, tamper-evident record of everything the
// operator layer does — every admin action and every P6 autonomous merge (design Decision 6, FR15/FR16).
//
// # The one property everything else rests on
//
// The record of who did what across tenants is only trustworthy if it cannot be quietly rewritten by
// the operators it records — INCLUDING the most privileged one. So tamper-evidence is structural
// rather than procedural:
//
//	entry_hash = H(prev_hash ‖ canonical(payload))
//
// Altering or removing any entry changes that entry's hash, which no longer matches the next entry's
// prev_hash, and Verify reports the exact sequence number where the chain breaks. There is no policy
// to enforce and no role to exempt — the arithmetic does the work.
//
// # No mutate and no delete, by construction
//
// The Store interface has three methods: Append, Entries, Verify. There is no Update, no Delete, no
// "correct an entry" — and TestStoreExposesNoMutationPath asserts that by reflecting over the
// interface, so adding one is a failing test rather than a code review someone might wave through.
// Corrections, when they are needed, are NEW appended entries; the original is never touched. This is
// the same discipline P7 applies to a wrong charge (credit, never edit) and P6 to a bad merge (revert,
// never rewrite).
//
// # Fail closed
//
// An admin action that cannot be audited must not take effect (FR16). Append therefore returns
// ErrStoreUnavailable rather than degrading, and the command path (internal/adminops) commits the
// audit entry WRITE-AHEAD — before the effect — so no effect can escape the trail even under a crash.
package adminaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Action is one recorded operator or autonomous action. Central enum: an action name spelled two ways
// is an audit trail that cannot be queried (logging-conventions §3).
type Action string

const (
	// ── identity / access ──
	ActionRoleGrant  Action = "admin.role.grant"
	ActionRoleRevoke Action = "admin.role.revoke"
	// ActionAuthorizationDenied records a deny-by-default refusal. Denials are audited, not just
	// logged: "who tried to do what they could not" is the signal an insider-threat review reads.
	ActionAuthorizationDenied Action = "admin.authorization.denied"

	// ── tenant lifecycle ──
	ActionTenantSuspend    Action = "admin.tenant.suspend"
	ActionTenantReactivate Action = "admin.tenant.reactivate"
	ActionTenantSetQuota   Action = "admin.tenant.set_quota"

	// ── entitlements & billing ──
	ActionEntitlementOverride Action = "admin.entitlement.override"
	ActionBillingCredit       Action = "admin.billing.credit"
	ActionBillingRefund       Action = "admin.billing.refund"

	// ── model registry ──
	ActionRegistryAddModel     Action = "admin.registry.add_model"
	ActionRegistryDeprecate    Action = "admin.registry.deprecate_model"
	ActionRegistryRepointPrice Action = "admin.registry.repoint_price_ref"

	// ── jobs & fleet ──
	ActionJobRetry  Action = "admin.job.retry"
	ActionJobCancel Action = "admin.job.cancel"

	// ── autonomous fleet controls ──
	ActionKillSwitchArm    Action = "admin.killswitch.arm"
	ActionKillSwitchDisarm Action = "admin.killswitch.disarm"

	// ── impersonation ──
	ActionImpersonationStart   Action = "admin.impersonation.start"
	ActionImpersonationElevate Action = "admin.impersonation.elevate"
	ActionImpersonationEnd     Action = "admin.impersonation.end"
	// ActionImpersonatedAction records one action taken WHILE impersonating. It is logged as
	// impersonation — acting admin plus impersonated tenant — never as the tenant (FR13).
	ActionImpersonatedAction Action = "admin.impersonation.action"

	// ── cross-tenant reads ──
	// ActionCrossTenantView records an AUTHORIZED cross-tenant read. Logged even though it is allowed,
	// because any cross-tenant access is a privacy event and an auditor must see who looked at whom.
	ActionCrossTenantView Action = "admin.cross_tenant.view"

	// ── compliance ──
	ActionGDPRExecute Action = "admin.gdpr.execute"
	// ActionGDPRTombstone is the non-PII reference kept in the chain after an erasure, so no entry is
	// ever removed and the chain stays verifiable (FR17).
	ActionGDPRTombstone Action = "admin.gdpr.tombstone"

	// ── autonomous merges (P6) ──
	// ActionAutonomousMerge mirrors the P6 change ledger into the tamper-evident chain: every merge the
	// autonomous optimizer performs, with its motivating diagnosis, verified delta and merge commit.
	ActionAutonomousMerge Action = "p6.autonomous.merge"
)

// ActorSystem is the actor recorded for entries no human initiated — the P6 loop's own merges. It is a
// reserved identifier rather than an empty string so "the autonomous fleet did this" is a positive
// statement in the record rather than a missing field.
const ActorSystem = "system:p6-autonomous-loop"

var (
	// ErrStoreUnavailable means the audit store could not record an entry. Its only correct handling is
	// to abandon the action: an unauditable privileged action must not take effect (FR16).
	ErrStoreUnavailable = errors.New("adminaudit: audit store unavailable — the action must not proceed")
	// ErrNoActor / ErrNoAction / ErrNoTarget reject entries that could not be reconstructed later. An
	// audit row that cannot answer "who, what, to whom" is not a record, it is noise.
	ErrNoActor  = errors.New("adminaudit: an audit entry needs an actor")
	ErrNoAction = errors.New("adminaudit: an audit entry needs an action")
	ErrNoTarget = errors.New("adminaudit: an audit entry needs a target")
)

// Entry is one append-only, hash-chained record — the design's `audit_entry`.
//
// It holds identifiers, a reason, and DIGESTS of parameters — never raw parameter bodies. That keeps
// the chain small, keeps secrets and tenant content out of a store that is by definition never
// deleted, and makes the GDPR tombstone (FR17) possible: there is nothing PII-bearing inside an entry
// to have to erase.
type Entry struct {
	Seq       int    `json:"seq"`
	PrevHash  string `json:"prev_hash"`
	EntryHash string `json:"entry_hash"`
	// ActorAdminID is the acting admin principal, or ActorSystem for autonomous entries.
	ActorAdminID string `json:"actor_admin_id"`
	// Target is what the action acted on ("tenant:acme", "global", "job:run-17").
	Target string `json:"target"`
	Action Action `json:"action"`
	// Reason is the operator's recorded justification. Required for every state-changing action
	// (FR6); reads and denials carry a short machine reason instead.
	Reason string `json:"reason,omitempty"`
	// ParamsDigest is a hash of the action's parameters. A digest rather than the parameters so the
	// chain never becomes a place tenant content or a credential can land.
	ParamsDigest string `json:"params_digest,omitempty"`
	// Result records the outcome ("applied", "denied", "halted").
	Result string `json:"result"`
	// ImpersonationID ties an entry to the impersonation session it was taken under, so an
	// impersonated action reads as impersonation and never as the tenant (FR13).
	ImpersonationID string `json:"impersonation_id,omitempty"`
	// Evidence carries the non-PII references an entry needs to be reconstructable: a merge commit, a
	// diagnosis id, a verified delta, a GDPR verification ref. Map-shaped so a new evidence kind is a
	// key rather than a schema change — and every value is an identifier or a measured number.
	Evidence  map[string]string `json:"evidence,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// Store is the append-only audit log.
//
// Three methods, deliberately. There is no mutation or deletion path for ANY caller — no role, not
// Superadmin, not the package itself. TestStoreExposesNoMutationPath enforces the shape.
type Store interface {
	// Append durably records e — assigning Seq, PrevHash and EntryHash — and returns the committed
	// entry. It returns ErrStoreUnavailable when the record cannot be written, which the caller must
	// treat as "the action does not happen".
	Append(e Entry) (Entry, error)
	// Entries returns the whole chain in sequence order (a copy; callers may not mutate it).
	Entries() []Entry
	// Verify walks the chain and reports whether it is intact.
	Verify() Verification
}

// Verification is the result of an integrity walk.
type Verification struct {
	// Intact is true when every entry's hash matches its recomputed value and its predecessor's.
	Intact bool `json:"intact"`
	// BreakAt is the sequence number of the FIRST entry that does not verify. Zero when intact.
	BreakAt int `json:"break_at,omitempty"`
	// Detail is a short non-sensitive description of the break.
	Detail string `json:"detail,omitempty"`
	// Checked is how many entries were walked.
	Checked int `json:"checked"`
}

// GenesisHash is the prev_hash of the first entry. A named constant rather than an empty string so
// "the chain starts here" is distinguishable from "prev_hash was never populated".
const GenesisHash = "genesis"

// Canonical renders the hashed payload of an entry.
//
// Exported because the hash is a promise about the CONTENT of a record: an independent auditor — or a
// future store implementation in another language — must be able to recompute it. Field order is
// fixed and every value is length-prefixed, so no two different entries can serialize to the same
// bytes by concatenation accident.
func Canonical(e Entry) []byte {
	fields := []string{
		"v1",
		strconv.Itoa(e.Seq),
		e.ActorAdminID,
		e.Target,
		string(e.Action),
		e.Reason,
		e.ParamsDigest,
		e.Result,
		e.ImpersonationID,
		canonicalEvidence(e.Evidence),
		e.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	var b strings.Builder
	for _, f := range fields {
		b.WriteString(strconv.Itoa(len(f)))
		b.WriteByte(':')
		b.WriteString(f)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// canonicalEvidence renders the evidence map deterministically. Sorted keys, because Go's map order is
// randomized and a hash that depends on iteration order is a hash that fails verification at random.
func canonicalEvidence(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(';')
	}
	return b.String()
}

// HashEntry computes entry_hash = H(prev_hash ‖ canonical(payload)).
func HashEntry(prevHash string, e Entry) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte{0})
	h.Write(Canonical(e))
	return hex.EncodeToString(h.Sum(nil))
}

// Digest hashes an action's parameters into ParamsDigest. Callers pass whatever identifies the
// parameters; the digest is what lands in the chain, so a parameter value can never leak into a store
// that is by design never deleted.
func Digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// validate rejects an entry that could not be reconstructed later.
func validate(e Entry) error {
	if strings.TrimSpace(e.ActorAdminID) == "" {
		return ErrNoActor
	}
	if strings.TrimSpace(string(e.Action)) == "" {
		return ErrNoAction
	}
	if strings.TrimSpace(e.Target) == "" {
		return ErrNoTarget
	}
	return nil
}

// ── MemoryStore ─────────────────────────────────────────────────────────────────────────────────

// MemoryStore is the in-process append-only chain. It is the shape the durable `audit_entry` table
// implements (write-once rows plus a database trigger refusing UPDATE/DELETE); both answer the same
// question — is this chain intact — and both are exercised by the same Verify.
type MemoryStore struct {
	mu      sync.RWMutex
	entries []Entry
	now     func() time.Time
	// unavailable makes the store refuse writes. This is not a test hook bolted on: "the audit store
	// is down" is a first-class operational state the command path must handle by NOT acting (FR16),
	// and a state that cannot be entered is a state that was never tested.
	unavailable bool
}

// NewMemoryStore builds an empty chain. now may be nil (defaults to time.Now().UTC()).
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{now: now}
}

// SetUnavailable takes the store offline (or brings it back). An operator drill and the fail-closed
// test both use it; nothing in the request path calls it.
func (s *MemoryStore) SetUnavailable(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unavailable = down
}

// Append implements Store.
func (s *MemoryStore) Append(e Entry) (Entry, error) {
	if err := validate(e); err != nil {
		return Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable {
		return Entry{}, ErrStoreUnavailable
	}
	prev := GenesisHash
	if n := len(s.entries); n > 0 {
		prev = s.entries[n-1].EntryHash
	}
	e.Seq = len(s.entries) + 1
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.now()
	}
	e.CreatedAt = e.CreatedAt.UTC()
	e.PrevHash = prev
	e.EntryHash = HashEntry(prev, e)
	s.entries = append(s.entries, e)
	return e, nil
}

// Entries implements Store.
func (s *MemoryStore) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Verify implements Store.
func (s *MemoryStore) Verify() Verification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prev := GenesisHash
	for i, e := range s.entries {
		if e.Seq != i+1 {
			return Verification{Intact: false, BreakAt: e.Seq, Checked: i,
				Detail: fmt.Sprintf("sequence gap: entry at position %d claims seq %d", i+1, e.Seq)}
		}
		if e.PrevHash != prev {
			return Verification{Intact: false, BreakAt: e.Seq, Checked: i,
				Detail: "prev_hash does not match the preceding entry — an entry was altered or removed"}
		}
		if got := HashEntry(prev, e); got != e.EntryHash {
			return Verification{Intact: false, BreakAt: e.Seq, Checked: i,
				Detail: "entry_hash does not match the recomputed payload — this entry was altered"}
		}
		prev = e.EntryHash
	}
	return Verification{Intact: true, Checked: len(s.entries)}
}

// SimulateOutOfBandTamper alters a stored entry the way somebody with direct store access would —
// bypassing Append entirely, because there IS no in-band mutation path.
//
// It exists so the tamper-evidence drill in the spec ("an out-of-band attempt to alter a stored
// entry") can actually be run: an integrity check nobody has ever seen fail is an integrity check
// nobody knows works. It is not reachable from any request path, any role, or any HTTP surface.
func (s *MemoryStore) SimulateOutOfBandTamper(seq int, mutate func(*Entry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := seq - 1
	if idx < 0 || idx >= len(s.entries) {
		return fmt.Errorf("adminaudit: no entry at seq %d", seq)
	}
	mutate(&s.entries[idx])
	return nil
}

// SimulateOutOfBandDeletion removes a stored entry out of band, the other half of the drill.
func (s *MemoryStore) SimulateOutOfBandDeletion(seq int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := seq - 1
	if idx < 0 || idx >= len(s.entries) {
		return fmt.Errorf("adminaudit: no entry at seq %d", seq)
	}
	s.entries = append(s.entries[:idx], s.entries[idx+1:]...)
	return nil
}

// ── Query helpers ───────────────────────────────────────────────────────────────────────────────

// Filter selects entries from a chain. Used by the console's audit viewer and by the tests; it reads
// a snapshot, so it can never mutate the chain.
type Filter struct {
	Actor  string
	Target string
	Action Action
	Since  time.Time
}

// Match reports whether e satisfies f. A zero Filter matches everything.
func (f Filter) Match(e Entry) bool {
	if f.Actor != "" && e.ActorAdminID != f.Actor {
		return false
	}
	if f.Target != "" && e.Target != f.Target {
		return false
	}
	if f.Action != "" && e.Action != f.Action {
		return false
	}
	if !f.Since.IsZero() && e.CreatedAt.Before(f.Since) {
		return false
	}
	return true
}

// Select returns the entries of s matching f, newest last.
func Select(s Store, f Filter) []Entry {
	var out []Entry
	for _, e := range s.Entries() {
		if f.Match(e) {
			out = append(out, e)
		}
	}
	return out
}

// storeMethodNames is the complete method set of Store. Named here so the structural test compares
// against one list rather than a literal typed twice.
var storeMethodNames = []string{"Append", "Entries", "Verify"}

// StoreMethodNames reports the audit store's permitted method set. Exported so the "no mutation path"
// test lives with the other audit tests instead of reaching into package internals.
func StoreMethodNames() []string {
	out := make([]string, len(storeMethodNames))
	copy(out, storeMethodNames)
	return out
}

// StoreInterfaceType returns the reflected Store interface, so a test can assert its shape.
func StoreInterfaceType() reflect.Type { return reflect.TypeOf((*Store)(nil)).Elem() }
