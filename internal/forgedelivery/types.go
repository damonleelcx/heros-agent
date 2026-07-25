// Package forgedelivery is P12: how a verified optimization reaches a customer's repository.
//
// # The load-bearing property (ADR-005)
//
// In the DEFAULT mode the platform holds NO forge credential. The customer's CI opens the pull request
// with the ephemeral, repository-scoped token it already holds, so the credential the P2 exit analysis
// called "the highest blast-radius action in the system" is never created on our side. A hosted Git App
// (Mode "app") is the opt-in upgrade for customers who cannot run delivery in CI; it holds standing
// write access, contained per-repository, least-privilege, and customer-revocable.
//
// # What this package does and does not do
//
// It DELIVERS — opens/updates pull requests and their branches, records each delivery, observes merges.
// It never MERGES below the Autonomous level, never writes anything but pull requests and their
// branches, and is never a way around the P5.5 gate: an unverified change is undeliverable from every
// entry point. Verification stays P5.5's, the autonomous loop and its halt stay P6's, entitlement stays
// P7's, and CI-mediated delivery runs inside P11's CI hook — this package consumes all of them.
package forgedelivery

import (
	"crypto/sha256"
	"encoding/hex"
)

// Mode is the credential path a delivery took. It is recorded on every delivery-record row so a later
// audit can answer which credential opened a given pull request (task 4.3 / ADR-005).
type Mode string

const (
	// ModeCI is the default: the customer's CI opens the pull request with its own ephemeral token. The
	// platform holds no forge credential in this mode.
	ModeCI Mode = "ci"
	// ModeApp is the opt-in hosted Git App: the platform opens the pull request with a per-repository,
	// least-privilege, customer-revocable installation token held in a secrets manager.
	ModeApp Mode = "app"
)

// Valid reports whether m is a known credential mode. An unknown mode denies rather than defaults —
// the direction a mistake must fail in when the value decides who holds a write credential.
func (m Mode) Valid() bool { return m == ModeCI || m == ModeApp }

// State is one entry in a delivery's lifecycle. The record is append-only, so a state change is a new
// row rather than a mutated field (ADR-005 blocker #2 / design Decision 4).
type State string

const (
	// StateOpened is the first row of a delivery: a pull request was opened. Exactly one 'opened' row
	// can exist per delivery id (a partial unique index enforces it), which is what makes idempotency
	// hold under concurrency.
	StateOpened State = "opened"
	// StateUpdated is a re-delivery of the same change to the same target: the existing pull request was
	// updated rather than a second one opened.
	StateUpdated State = "updated"
	// StateSuperseded is a delivery whose pull request was closed because a newer verified proposal
	// replaced it. The reason is stated on the row.
	StateSuperseded State = "superseded"
	// StateClosed is a pull request closed without merging — NOT a merge.
	StateClosed State = "closed"
	// StateMerged is recorded from an OBSERVATION of a merge into the target branch, never inferred from
	// a pull request closing. It is P7 gainshare's observable input.
	StateMerged State = "merged"
	// StateReverted is a merged change later reverted. It is a further state; the 'merged' row stays, so
	// a disputed billed period is answerable as a sequence.
	StateReverted State = "reverted"
)

// terminalStates are the states after which a delivery has no open pull request.
var validStates = map[State]bool{
	StateOpened: true, StateUpdated: true, StateSuperseded: true,
	StateClosed: true, StateMerged: true, StateReverted: true,
}

// Valid reports whether s is a known lifecycle state.
func (s State) Valid() bool { return validStates[s] }

// DeliveryID is the deterministic identity of a logical delivery: the same
// (config_hash, source_revision, target) always maps to the same id. It is the idempotency anchor —
// two concurrent deliveries of the same change to the same target compute the SAME id and contend for
// the same single 'opened' row (design Decision 5).
//
// A hash rather than a concatenation so the id is a fixed, index-friendly width and cannot be
// accidentally parsed back into its parts by a consumer who should treat it as opaque.
func DeliveryID(configHash, sourceRevision, target string) string {
	h := sha256.Sum256([]byte(configHash + "\x00" + sourceRevision + "\x00" + target))
	return hex.EncodeToString(h[:])[:32]
}
