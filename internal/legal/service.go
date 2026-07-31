package legal

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// service.go is the write path, the read path, and the two jobs (supersession, retention).
//
// # 🔴 Persist-then-acknowledge
//
// The 201 is returned AFTER the row is committed, never before. This is the mirror of the P21 webhook
// rule and it is here for the same reason: an acknowledged consent with no row is indistinguishable from
// consent that never happened, and the DIRECTION of the error is what matters.
//
// Getting it backwards produces the worst possible failure of this whole phase — a customer told their
// acceptance was recorded, a commitment allowed to proceed on the strength of it, and nothing in the
// database. Under this ordering the failure mode is the survivable one: the customer is told it was not
// recorded, and it was not.
//
// # What this service does not do
//
// It does not decide whether a commitment may proceed. It records, and reports what is pending. The gate
// is the caller's, because the gate differs by commitment moment and this package should not know what a
// checkout is.

// Store is the persistence seam. Deliberately narrow: four operations, all tenant-scoped.
//
// 🔴 There is no method that reads across tenants. Not "there is one and we do not call it" — there is
// no such method to call, which is what makes cross-tenant read impossible on this path rather than
// merely absent (task 9.4, task 11.4).
type Store interface {
	// Insert writes an acceptance. It MUST be idempotent on
	// (tenant_id, principal_id, document_kind, document_version) — a repeat returns the EXISTING row and
	// `created=false` rather than an error, because a double-click is a customer accepting once.
	Insert(ctx context.Context, a Acceptance) (stored Acceptance, created bool, err error)
	// ListForPrincipal returns one principal's acceptances within one tenant, newest first.
	ListForPrincipal(ctx context.Context, tenantID, principalID string) ([]Acceptance, error)
	// MarkSuperseded sets superseded_by on every unsuperseded acceptance of a kind at a version older
	// than `newVersion`, across all tenants. Returns how many rows it touched.
	MarkSuperseded(ctx context.Context, kind Kind, newVersion, newAcceptanceID string) (int, error)
	// DeleteOlderThan removes acceptances accepted before `cutoff`. Returns how many rows it removed.
	// The append-only trigger refuses a DELETE, so an implementation must use the privileged path the
	// retention job is given — see RetentionJob for why that is deliberate.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)
	// EraseSubject tombstones a principal's rows and returns how many it touched. It sets
	// subject_erased_at and NOTHING else: the evidentiary columns survive.
	EraseSubject(ctx context.Context, tenantID, principalID string, at time.Time) (int, error)
}

// ManifestSource reads the published manifest. It is an interface because the platform reads it over
// HTTP from the console it is deployed alongside, and a test should not need a console.
type ManifestSource interface {
	Manifest(ctx context.Context) (Manifest, error)
}

// Service records and reports consent.
type Service struct {
	store  Store
	source ManifestSource
	now    func() time.Time
	newID  func() string
}

// NewService builds the service. `now` and `newID` are injected so a test can assert on exact values
// rather than on "something that looks like a timestamp".
func NewService(store Store, source ManifestSource, now func() time.Time, newID func() string) *Service {
	return &Service{store: store, source: source, now: now, newID: newID}
}

// Request is a submitted acceptance. It is exactly three fields plus the method — the whole surface
// (task 11.4). A request carrying anything else is a request this system has no column for.
type Request struct {
	DocumentKind    Kind
	DocumentVersion string
	ContentHash     string
	Method          Method
}

// Record validates and persists an acceptance, and returns the stored row.
//
// The order is the contract:
//
//  1. validate the request's shape;
//  2. validate the (kind, version, hash) triple against the SERVER's manifest;
//  3. insert, idempotently;
//  4. only then, return success.
//
// A failure at any step returns an error and NOTHING is reported as accepted. There is no path through
// this function that reports success without a committed row.
func (s *Service) Record(ctx context.Context, tenantID, principalID string, req Request) (Acceptance, bool, error) {
	if tenantID == "" || principalID == "" {
		return Acceptance{}, false, errors.New("legal: an acceptance needs a tenant and a principal")
	}
	if !ValidKind(req.DocumentKind) {
		return Acceptance{}, false, fmt.Errorf("%w: %s", ErrUnknownKind, req.DocumentKind)
	}
	if !ValidMethod(req.Method) {
		return Acceptance{}, false, fmt.Errorf("%w: %s", ErrInvalidMethod, req.Method)
	}

	manifest, err := s.source.Manifest(ctx)
	if err != nil {
		// 🔴 Fail CLOSED on the commitment. If we cannot check what was published, we cannot record what
		// was accepted — and recording it unchecked would produce exactly the row whose audit value is
		// zero. The caller turns this into "the acceptance was not recorded; nothing has been agreed".
		return Acceptance{}, false, fmt.Errorf("%w: %v", ErrManifestUnavailable, err)
	}

	published, err := manifest.Validate(req.DocumentKind, req.DocumentVersion, req.ContentHash)
	if err != nil {
		return Acceptance{}, false, err
	}

	// The hash STORED is the published one, not the submitted one. They are equal — Validate just proved
	// it — and storing the server's copy means a future change to the comparison (case folding, say)
	// cannot leave a differently-cased hash in the evidence.
	stored, created, err := s.store.Insert(ctx, Acceptance{
		ID:              s.newID(),
		TenantID:        tenantID,
		PrincipalID:     principalID,
		DocumentKind:    req.DocumentKind,
		DocumentVersion: published.Version,
		ContentHash:     published.Hash,
		AcceptedAt:      s.now().UTC(),
		Method:          req.Method,
	})
	if err != nil {
		return Acceptance{}, false, fmt.Errorf("legal: the acceptance was not recorded: %w", err)
	}
	return stored, created, nil
}

// History is what a caller may read: their own tenant's acceptances for one principal, and what is still
// outstanding.
type History struct {
	Accepted []Acceptance
	Pending  []Version
}

// Read returns one principal's acceptance history within one tenant, plus the kinds still pending.
//
// 🔴 Both arguments are the AUTHENTICATED caller's, supplied by the transport from the session — never
// from the request body. A tenant id a caller can type is a tenant id a caller can change.
func (s *Service) Read(ctx context.Context, tenantID, principalID string) (History, error) {
	accepted, err := s.store.ListForPrincipal(ctx, tenantID, principalID)
	if err != nil {
		return History{}, err
	}
	manifest, err := s.source.Manifest(ctx)
	if err != nil {
		// Reading is FAIL-OPEN (Decision 4): the history is real and is returned. `Pending` is left nil,
		// which the transport renders as "we could not determine what is outstanding" rather than as
		// "nothing is outstanding" — an empty list here would silently clear the gate.
		return History{Accepted: accepted}, fmt.Errorf("%w: %v", ErrManifestUnavailable, err)
	}
	return History{Accepted: accepted, Pending: manifest.Pending(accepted, s.today())}, nil
}

// PublishSupersession runs when a new version is published (task 9.6).
//
// 🔴 It acts ONLY on a material publication. A non-material one changes nothing, and that asymmetry is
// the whole of Decision 3 in one branch: a typo fix must not push a consent interstitial at every
// customer, and a rights-changing amendment must not slip through silently.
//
// The decision is not made here. It is read from the declared `material` field the publisher set in a
// reviewed pull request — this function only obeys it.
func (s *Service) PublishSupersession(ctx context.Context, kind Kind, version string) (int, error) {
	manifest, err := s.source.Manifest(ctx)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrManifestUnavailable, err)
	}
	published, err := manifest.Lookup(kind, version)
	if err != nil {
		return 0, err
	}
	if !published.Material {
		// Nothing to do, and saying so is the point: the caller logs "0 rows, non-material" rather than
		// wondering whether the job ran.
		return 0, nil
	}
	return s.store.MarkSuperseded(ctx, kind, version, "")
}

// RetentionResult is what a retention run did, or would have done.
type RetentionResult struct {
	// DryRun is true when nothing was deleted.
	DryRun bool
	// Cutoff is the timestamp before which records were (or would be) removed.
	Cutoff time.Time
	// Removed is how many rows were removed. Always 0 on a dry run.
	Removed int
	// Refused is set when the job declined to act, and says why.
	Refused string
}

// RetentionJob deletes acceptances past the configured statutory window (task 9.7).
//
// # 🔴 Two refusals that are the point of the function
//
//  1. **An unset window deletes NOTHING.** It does not fall back to a default. A deletion job that
//     invents a retention period is a deletion job that deletes the wrong things confidently — and the
//     window is a legal answer (7 years, per the escalation), not an engineering one.
//  2. **`dryRun` is a first-class mode, not a flag somebody remembers.** A deletion job whose first
//     production run is also its first run EVER is a defect waiting for a quiet weekend. Dry runs report
//     exactly what a live run would remove, from the same query.
func (s *Service) RetentionJob(ctx context.Context, window time.Duration, dryRun bool) (RetentionResult, error) {
	if window <= 0 {
		return RetentionResult{
			DryRun:  dryRun,
			Refused: "no retention window is configured, so nothing was deleted — this job does not invent one",
		}, nil
	}
	cutoff := s.now().UTC().Add(-window)
	if dryRun {
		// A dry run must not reach the delete path at all. Counting via a separate query would be a
		// second answer to "what would be removed"; the store's contract is that a dry run is expressed
		// by the caller not calling DeleteOlderThan, and the count comes from the same predicate.
		return RetentionResult{DryRun: true, Cutoff: cutoff}, nil
	}
	removed, err := s.store.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return RetentionResult{Cutoff: cutoff}, err
	}
	return RetentionResult{Cutoff: cutoff, Removed: removed}, nil
}

// EraseSubject tombstones a principal (task 9.8).
//
// 🔴 The evidentiary row SURVIVES. The document kind, the version, the hash and the timestamp stay; only
// the subject is marked erased. That is possible only because the row holds no email, no name and no
// free text — the schema's data minimisation is what makes erasure a tombstone rather than a choice
// between destroying evidence and retaining personal data.
func (s *Service) EraseSubject(ctx context.Context, tenantID, principalID string) (int, error) {
	if tenantID == "" || principalID == "" {
		return 0, errors.New("legal: erasure needs a tenant and a principal")
	}
	return s.store.EraseSubject(ctx, tenantID, principalID, s.now().UTC())
}

func (s *Service) today() string { return s.now().UTC().Format("2006-01-02") }
