package sourceingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// service.go is the CONNECTION LIFECYCLE: connect, list, read the ledger, and revoke.
//
// # Why the cascade lives here and not in the store
//
// Revoking a connection has to delete the grant, the credential, AND every tree derived from it (D3).
// Those live in three places — a table, the secret store, and the blob-backed snapshot store — and no
// one of them can perform the whole operation. Design D3 is explicit about what happens when the
// second half is missing:
//
//	"Deleting a grant row is one line; enumerating the artifacts derived from it is not, and nothing
//	fails when the second half is missing — the system keeps answering, correctly, from data it is no
//	longer authorized to hold. That failure is invisible from the inside and indefensible from the
//	outside."
//
// So the cascade is one function with one caller, and the ORDER is chosen so that every partial
// failure leaves the system in the safe direction:
//
//  1. **Snapshots first.** They are the customer's source. A snapshot deleted while the grant survives
//     is a re-clone away; a grant deleted while snapshots survive is data we cannot find again,
//     because the grant row is the only thing that says which snapshots came from it.
//  2. **Credential second.** Once the trees are gone, the credential is the remaining capability.
//  3. **Grant row last.** It is the index. Removing it first would orphan both of the above.
//
// A failure at any step returns an error AND is logged with the code, so a half-completed revocation
// is a page rather than a silence. It is also RETRYABLE: every step is idempotent, so a customer or an
// operator re-issuing the revoke completes it.

// Service is the connection lifecycle.
type Service struct {
	conns   ConnectionStore
	snaps   SnapshotStore
	secrets providergateway.ForgeSecrets
	log     *slog.Logger
	nowMS   func() int64
	newID   func(prefix string) string
}

// ServiceConfig wires a Service.
type ServiceConfig struct {
	Connections ConnectionStore
	Snapshots   SnapshotStore
	Secrets     providergateway.ForgeSecrets
	Logger      *slog.Logger
	NowMS       func() int64
	IDFor       func(prefix string) string
}

// NewService builds the connection lifecycle.
func NewService(cfg ServiceConfig) (*Service, error) {
	switch {
	case cfg.Connections == nil:
		return nil, fmt.Errorf("sourceingest: connection service needs a connection store")
	case cfg.Snapshots == nil:
		return nil, fmt.Errorf("sourceingest: connection service needs a snapshot store to cascade into")
	case cfg.Secrets == nil:
		return nil, fmt.Errorf("sourceingest: connection service needs a forge-credential source")
	}
	s := &Service{
		conns:   cfg.Connections,
		snaps:   cfg.Snapshots,
		secrets: cfg.Secrets,
		log:     cfg.Logger,
		nowMS:   cfg.NowMS,
		newID:   cfg.IDFor,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.nowMS == nil {
		s.nowMS = func() int64 { return time.Now().UnixMilli() }
	}
	if s.newID == nil {
		s.newID = defaultID
	}
	return s, nil
}

// ConnectRequest is what the console submits after the customer authorized on the forge.
type ConnectRequest struct {
	// TenantID comes from the authenticated principal, never from a request field.
	TenantID   string
	WorkflowID string
	// Repository is the one the customer NAMED. The breadth check compares the forge's answer against
	// this, so it has to be the customer's input rather than anything derived from the grant.
	Repository string
	SubPath    string
	// CreatedBy is the person who authorized it.
	CreatedBy string
	// Authorization is what the forge returned, including what the grant actually covers.
	Authorization Authorization
	// ConsentShown asserts the disclosure was displayed (FR10). Refused when false — see Connect.
	ConsentShown bool
}

// Connect validates and records a connection.
//
// # 🔴 Why `ConsentShown` is checked HERE and not only in the browser
//
// FR10 says authorization *"cannot be completed without that disclosure having been displayed."* A
// check that lives only in the console is a check a second client does not have — and the console is
// not the only thing that can POST to this API. Enforcing it at the write is what makes the
// requirement true of the SYSTEM rather than of one page. It is not proof the human read it; nothing
// is. It is proof that no code path can create a grant without the screen that states what the grant
// permits, which is the property that can actually be enforced.
func (s *Service) Connect(ctx context.Context, req ConnectRequest) (Connection, error) {
	if !req.ConsentShown {
		return Connection{}, fmt.Errorf("sourceingest: refusing to create a connection before its disclosure was displayed")
	}
	if err := req.Authorization.Validate(req.Repository); err != nil {
		s.warn(ctx, eventname.IngestConnectionRefused, errorcode.RequestInvalid,
			"a repository connection was refused at connect", "forge", req.Authorization.Forge.String())
		return Connection{}, err
	}
	expected, err := ExpectedGrantKind(req.Authorization.Forge)
	if err != nil {
		return Connection{}, err
	}
	if req.Authorization.GrantKind != expected {
		// A GitHub `access_token` where an App installation was expected is a fine-grained PAT wearing
		// the App's label, and §14 A2 decided that difference deliberately. Refused rather than
		// coerced: silently rewriting the kind would make the stored row a claim nobody checked.
		return Connection{}, fmt.Errorf(
			"sourceingest: %s issues %q at repository scope and this authorization claims %q",
			req.Authorization.Forge, expected, req.Authorization.GrantKind)
	}

	conn := Connection{
		ConnectionID: s.newID("conn"),
		TenantID:     req.TenantID,
		WorkflowID:   req.WorkflowID,
		Forge:        req.Authorization.Forge,
		Repository:   req.Repository,
		SubPath:      req.SubPath,
		GrantKind:    req.Authorization.GrantKind,
		ExternalID:   req.Authorization.ExternalID,
		CreatedBy:    req.CreatedBy,
		CreatedAtMS:  s.nowMS(),
	}
	if err := conn.Validate(); err != nil {
		return Connection{}, err
	}

	// 🔴 The CREDENTIAL is stored BEFORE the grant row, and the order is the opposite of the
	// revocation's for the same reason: a grant row with no credential is a connection that fails at
	// first use with `credential rejected`, which sends the customer to rotate a token that was never
	// stored. A credential with no grant row is unreferenced material that the next revoke-by-ref or
	// a sweep removes, and that nothing will ever use, because every read starts from the row.
	ref := providergateway.ForgeRef{Forge: conn.Forge.String(), ConnectionID: conn.ConnectionID}
	if err := s.secrets.Store(ctx, ref, req.Authorization.Token); err != nil {
		return Connection{}, fmt.Errorf("sourceingest: store forge credential for %s: %w", conn.Repository, err)
	}
	if err := s.conns.Create(ctx, conn); err != nil {
		// Roll the credential back so the failure leaves nothing behind. Best effort AND logged: a
		// silent failure here is exactly the orphan the comment above says a sweep would catch, and
		// "a sweep would catch it" is not a reason to not say it happened.
		if rbErr := s.secrets.Revoke(ctx, ref); rbErr != nil {
			s.warn(ctx, eventname.IngestConnectionRevoked, errorcode.StoreWriteFailed,
				"a forge credential could not be rolled back after a failed connect", "connection_id", conn.ConnectionID)
		}
		return Connection{}, err
	}
	s.info(ctx, eventname.IngestConnectionCreated, "a repository connection was created",
		"forge", conn.Forge.String(), "grant_kind", conn.GrantKind.String())
	return conn, nil
}

// RevocationResult is what a revocation removed. Returned rather than logged only, because the
// console's confirmation states that derived trees are deleted (task 6.3) and a number it can show is
// the difference between a claim and a receipt.
type RevocationResult struct {
	ConnectionID string `json:"connection_id"`
	// SnapshotsDeleted is how many derived trees the cascade removed.
	SnapshotsDeleted int `json:"snapshots_deleted"`
}

// Revoke deletes the grant, the credential and every tree derived from the connection (FR8, D3).
//
// A subsequent read returns ErrNoSource, because GitSource resolves the connection first and finds
// none — see Materialize. That is asserted end-to-end by §7.3 rather than reasoned about here.
func (s *Service) Revoke(ctx context.Context, tenantID, connectionID string) (RevocationResult, error) {
	conn, err := s.byID(ctx, tenantID, connectionID)
	if err != nil {
		return RevocationResult{}, err
	}

	// 1) The derived trees. First, for the reason in this file's header.
	n, err := s.snaps.DeleteByConnection(ctx, tenantID, connectionID)
	if err != nil {
		s.warn(ctx, eventname.IngestConnectionRevoked, errorcode.StoreWriteFailed,
			"a revocation could not delete the trees derived from a connection — the grant was NOT removed, so the revocation can be retried",
			"connection_id", connectionID)
		return RevocationResult{}, fmt.Errorf("sourceingest: revoking %s: delete derived snapshots: %w", connectionID, err)
	}

	// 2) The credential.
	ref := providergateway.ForgeRef{Forge: conn.Forge.String(), ConnectionID: connectionID}
	if err := s.secrets.Revoke(ctx, ref); err != nil {
		s.warn(ctx, eventname.IngestConnectionRevoked, errorcode.StoreWriteFailed,
			"a revocation deleted the derived trees but could not delete the forge credential — the grant was NOT removed, so the revocation can be retried",
			"connection_id", connectionID)
		return RevocationResult{}, fmt.Errorf("sourceingest: revoking %s: delete credential: %w", connectionID, err)
	}

	// 3) The grant row. Last: it is the index for both of the above.
	if err := s.conns.Revoke(ctx, tenantID, connectionID); err != nil {
		s.warn(ctx, eventname.IngestConnectionRevoked, errorcode.StoreWriteFailed,
			"a revocation removed the trees and the credential but could not delete the grant row — the connection can no longer read anything, and the revocation can be retried to clear the row",
			"connection_id", connectionID)
		return RevocationResult{}, fmt.Errorf("sourceingest: revoking %s: delete grant: %w", connectionID, err)
	}

	s.info(ctx, eventname.IngestConnectionRevoked, "a repository connection was revoked and its derived trees deleted",
		"forge", conn.Forge.String(), "snapshots_deleted", n)
	return RevocationResult{ConnectionID: connectionID, SnapshotsDeleted: n}, nil
}

// Rotate replaces a connection's stored credential (task 3.2).
//
// A lifecycle OPERATION with a test, not a runbook step. Note what it deliberately does not do: it
// does not touch the grant row, and it does not delete derived snapshots. A rotation is the same
// customer, the same repository and the same authorization with new material — treating it as a
// re-connect would delete trees the customer never asked to lose.
func (s *Service) Rotate(ctx context.Context, tenantID, connectionID, token string) error {
	conn, err := s.byID(ctx, tenantID, connectionID)
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("sourceingest: refusing to rotate %s to an empty credential", connectionID)
	}
	ref := providergateway.ForgeRef{Forge: conn.Forge.String(), ConnectionID: connectionID}
	if err := s.secrets.Store(ctx, ref, token); err != nil {
		return fmt.Errorf("sourceingest: rotate credential for %s: %w", connectionID, err)
	}
	return nil
}

// List returns a tenant's connections.
func (s *Service) List(ctx context.Context, tenantID string) ([]Connection, error) {
	return s.conns.List(ctx, tenantID)
}

// Records returns a connection's read ledger, newest first.
func (s *Service) Records(ctx context.Context, tenantID, connectionID string, limit int) ([]CloneRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if _, err := s.byID(ctx, tenantID, connectionID); err != nil {
		return nil, err
	}
	return s.conns.Records(ctx, tenantID, connectionID, limit)
}

// byID resolves a connection within a tenant.
//
// 🔴 Scoped by tenant in the LOOKUP, not filtered afterwards. A read that fetches by id and then
// compares the tenant is a read that returns another tenant's row to any code path that forgets the
// comparison; there is no such code path if the id alone never resolves.
func (s *Service) byID(ctx context.Context, tenantID, connectionID string) (Connection, error) {
	all, err := s.conns.List(ctx, tenantID)
	if err != nil {
		return Connection{}, err
	}
	for _, c := range all {
		if c.ConnectionID == connectionID {
			return c, nil
		}
	}
	return Connection{}, ErrNoConnection
}

// info and warn are the only two ways this package writes a log line.
//
// 🔴 Three properties are enforced here rather than at each call site, because "remember to include
// the trace id" is a rule that holds until somebody is in a hurry:
//
//   - the event NAME is a member of the central enum, and a non-member writes nothing rather than
//     inventing a name — an invented name is a free-text field on the far side of a boundary;
//   - every WARN carries an `error_code` from the central enum;
//   - every line carries `trace_id` when the context has one, so a customer quoting the id in their
//     response header lands an operator on this line.
func (s *Service) info(ctx context.Context, ev eventname.Name, msg string, kv ...any) {
	if !ev.Valid() {
		return
	}
	s.log.InfoContext(ctx, msg, append(logBase(ctx, ev, ""), kv...)...)
}

func (s *Service) warn(ctx context.Context, ev eventname.Name, code errorcode.Code, msg string, kv ...any) {
	if !ev.Valid() {
		return
	}
	s.log.WarnContext(ctx, msg, append(logBase(ctx, ev, code), kv...)...)
}

// logBase builds the attributes every line in this package carries.
func logBase(ctx context.Context, ev eventname.Name, code errorcode.Code) []any {
	out := []any{"event", ev.String()}
	if code != "" {
		out = append(out, "error_code", string(code))
	}
	if tid := telemetry.TraceIDFromContext(ctx); tid != "" {
		out = append(out, "trace_id", tid, "request_id", tid)
	}
	return out
}
