package sourceingest

import (
	"context"
	"errors"
)

// moderouter.go is the ONE place the three modes are chosen between, and it is itself a `Source`.
//
// # Why a router and not a flag on the call
//
// Design D1: *"Every consumer receives a source snapshot and cannot tell how it was obtained."* The
// choice is per WORKFLOW — a tenant can connect one repository and keep pushing bundles for another —
// so it cannot be a single constructor's decision, and it must not be a parameter on `Materialize`,
// because a parameter is a branch every caller then has to have an opinion about.
//
// A router resolves it from data the caller never sees: does this workflow have a connection. One
// implementation is chosen, and the pipeline below is unchanged in either case.
//
// # 🚫 The one thing this must never do
//
// **Fall back.** A connected workflow whose clone fails does NOT get its old pushed bundle served
// instead. That is design D4 and it is the failure this file is most able to cause: the fallback is a
// two-line change here and it would look like robustness. It is not — a clone fails most often while
// nobody is watching, and yesterday's tree under today's question produces a finding about source that
// no longer exists, with nothing about the finding saying so.
//
// The router therefore branches on a fact established BEFORE the read is attempted (is there a
// connection), never on the read's outcome. There is no `if err != nil { try the other one }` in this
// file, and a fence asserts it stays that way.

// ModeRouter picks the implementation for a workflow and delegates.
type ModeRouter struct {
	conns  ConnectionStore
	bundle Source
	git    Source
}

// NewModeRouter returns the Source the pipeline is wired with.
func NewModeRouter(conns ConnectionStore, bundle, git Source) *ModeRouter {
	return &ModeRouter{conns: conns, bundle: bundle, git: git}
}

// Materialize resolves the mode and delegates.
func (m *ModeRouter) Materialize(ctx context.Context, ref Ref) (Materialized, error) {
	if err := ref.Validate(); err != nil {
		return Materialized{}, err
	}
	if m.conns == nil || m.git == nil {
		return m.bundle.Materialize(ctx, ref)
	}
	_, err := m.conns.ForWorkflow(ctx, ref.TenantID, ref.WorkflowID)
	switch {
	case errors.Is(err, ErrNoConnection):
		// No connection: this workflow is Mode 1. The default, and never a lesser tier.
		return m.bundle.Materialize(ctx, ref)
	case err != nil:
		// 🔴 A store that could not be read is NOT "no connection". Flattening it into the bundle path
		// would silently serve a pushed snapshot to a workflow that is supposed to be reading a
		// connected repository — which is the fallback this file exists to refuse, arriving through
		// the error path rather than through a `catch`.
		return Materialized{}, err
	default:
		// Connected: the clone path, and its failures are its own. No fallback (D4).
		return m.git.Materialize(ctx, ref)
	}
}

// ModeOf reports which mode a workflow reads through, for the console's `mode` column.
//
// Returns the mode as the vocabulary the console switches on. A store failure returns the error rather
// than a mode: "we could not tell" and "it is a bundle" are different facts, and only one of them is
// safe to render.
func (m *ModeRouter) ModeOf(ctx context.Context, tenantID, workflowID string) (Mode, error) {
	if m.conns == nil || m.git == nil {
		return ModeBundle, nil
	}
	_, err := m.conns.ForWorkflow(ctx, tenantID, workflowID)
	switch {
	case errors.Is(err, ErrNoConnection):
		return ModeBundle, nil
	case err != nil:
		return "", err
	default:
		return ModeConnected, nil
	}
}

// Mode is how a workflow's source arrives — the vocabulary the console switches on.
//
// 🔴 A NAMED type, not a bare string, and the reason is concrete rather than stylistic: the console's
// type generator keys a TypeScript union to the Go type of its sample, so registering this vocabulary
// with a plain `string` sample retyped EVERY plain string field in every view as this union. A named
// type is what makes the generated contract mean what it says.
type Mode string

const (
	// ModeBundle — source arrives as a customer-pushed bundle. The DEFAULT, and never a lesser tier:
	// no feature is gated on any other mode (FR12).
	ModeBundle Mode = "bundle"
	// ModeConnected — source is read from a connected repository.
	ModeConnected Mode = "connected"
	// ModeLocal — source is read in place on the customer's machine and never transmitted.
	ModeLocal Mode = "local"
)

// Valid reports membership.
func (m Mode) Valid() bool { return m == ModeBundle || m == ModeConnected || m == ModeLocal }

// String makes Mode printable.
func (m Mode) String() string { return string(m) }

// Modes returns the three modes. The console renders one label per member, and a fence asserts the two
// lists have the same length so a fourth mode cannot arrive as a blank cell.
func Modes() []Mode { return []Mode{ModeBundle, ModeConnected, ModeLocal} }
