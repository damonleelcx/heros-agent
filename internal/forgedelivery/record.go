package forgedelivery

import (
	"context"
	"errors"
	"time"
)

// Entry is one row of the append-only delivery record (the `delivery` table). A delivery and every
// subsequent state change is a new Entry; nothing is ever mutated (design Decision 4).
type Entry struct {
	Seq            int64     `json:"seq"`
	DeliveryID     string    `json:"delivery_id"`
	TenantID       string    `json:"tenant_id"`
	ConfigHash     string    `json:"config_hash"`
	SourceRevision string    `json:"source_revision"`
	Target         string    `json:"target"`
	ForgeRef       string    `json:"forge_ref"`
	Mode           Mode      `json:"mode"`
	State          State     `json:"state"`
	Actor          string    `json:"actor"`
	Reason         string    `json:"reason,omitempty"`
	MergeCommit    string    `json:"merge_commit,omitempty"`
	At             time.Time `json:"at"`
}

// DeliveryHead is the current head of one logical delivery: its latest state and the pull request it
// concerns. It is what supersession and the console list read, without replaying full history.
type DeliveryHead struct {
	DeliveryID     string `json:"delivery_id"`
	TenantID       string `json:"tenant_id"`
	ConfigHash     string `json:"config_hash"`
	SourceRevision string `json:"source_revision"`
	Target         string `json:"target"`
	ForgeRef       string `json:"forge_ref"`
	Mode           Mode   `json:"mode"`
	State          State  `json:"state"`
	Reason         string `json:"reason,omitempty"`
	MergeCommit    string `json:"merge_commit,omitempty"`
}

// Open reports whether this delivery still has an open pull request awaiting a decision.
func (h DeliveryHead) Open() bool { return h.State == StateOpened || h.State == StateUpdated }

// ErrOpenConflict is returned by Recorder.Append when an 'opened' entry collides with an existing
// 'opened' row for the same delivery id — the partial unique index firing. It is not a failure: it is
// the concurrency signal that another delivery won the open race, so this one must take the update path.
// The core relies on this rather than a check-then-act, because the race is exactly what a check-then-
// act leaves open (design Decision 5 / task 7.1).
var ErrOpenConflict = errors.New("forgedelivery: an open pull request already exists for this delivery")

// Recorder is the append-only delivery record. Every method that changes state APPENDS; there is
// deliberately no update or delete method, so "the record is append-only" is a property of the
// interface rather than a rule a caller must remember (task 4.1). The concrete store lives in
// internal/deliveryrecord (SQLite dev + the Postgres `delivery` table).
type Recorder interface {
	// Append writes one entry. Appending an 'opened' entry whose delivery already has an open pull
	// request returns ErrOpenConflict. Any other constraint violation is returned as-is.
	Append(ctx context.Context, e Entry) error
	// OpenForTarget returns the heads of deliveries with an OPEN pull request for a (tenant, target).
	// Supersession reads it to close the others; the console reads it to list them.
	OpenForTarget(ctx context.Context, tenantID, target string) ([]DeliveryHead, error)
	// Head returns the current head of one delivery, or ok=false if it has no entries.
	Head(ctx context.Context, deliveryID string) (DeliveryHead, bool, error)
	// History returns every entry for one delivery in append order, so a delivery's full lifecycle is
	// reconstructable (task 4.6).
	History(ctx context.Context, deliveryID string) ([]Entry, error)
	// ListForTenant returns the head of every delivery for a tenant, newest first, for the console.
	ListForTenant(ctx context.Context, tenantID string) ([]DeliveryHead, error)
}
