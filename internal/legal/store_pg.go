package legal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// store_pg.go is the Postgres store and the HTTP manifest source.
//
// # Why the SQL lives here and nowhere else
//
// Four statements, one file. The idempotency guarantee, the tenant scoping and the append-only rule are
// all properties of these statements, and spreading them across call sites is how one of them ends up
// written a fifth way that forgets the `tenant_id` predicate.

// PGStore is the Postgres implementation of Store.
type PGStore struct {
	db *sql.DB
}

// NewPGStore wraps a database handle.
func NewPGStore(db *sql.DB) *PGStore { return &PGStore{db: db} }

// Insert writes an acceptance idempotently.
//
// 🔴 `ON CONFLICT DO NOTHING` plus a follow-up read is the whole idempotency implementation, and the
// unique constraint is what makes it correct. There is deliberately no "check then insert": that is a
// race with a customer's double-click, and it loses.
//
// A conflict is a SUCCESS with `created=false`. The customer accepted; they accepted once. The caller
// returns 200 rather than 201 and the outcome on screen is identical, which is what a re-submitted form
// should do.
func (s *PGStore) Insert(ctx context.Context, a Acceptance) (Acceptance, bool, error) {
	const insert = `
INSERT INTO legal_acceptance
    (id, tenant_id, principal_id, document_kind, document_version, content_hash, accepted_at, method)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id, principal_id, document_kind, document_version) DO NOTHING`

	res, err := s.db.ExecContext(ctx, insert,
		a.ID, a.TenantID, a.PrincipalID, string(a.DocumentKind), a.DocumentVersion,
		a.ContentHash, a.AcceptedAt, string(a.Method))
	if err != nil {
		return Acceptance{}, false, fmt.Errorf("legal: insert: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Acceptance{}, false, fmt.Errorf("legal: insert: %w", err)
	}

	// Read back regardless of which branch was taken. On a fresh insert this proves the row COMMITTED —
	// which is what "persist-then-acknowledge" means operationally, rather than trusting an affected-row
	// count. On a conflict it returns the row that already existed, so the caller can render what was
	// actually agreed rather than what was just submitted.
	stored, err := s.get(ctx, a.TenantID, a.PrincipalID, a.DocumentKind, a.DocumentVersion)
	if err != nil {
		return Acceptance{}, false, err
	}
	return stored, affected == 1, nil
}

func (s *PGStore) get(ctx context.Context, tenantID, principalID string, kind Kind, version string) (Acceptance, error) {
	const q = `
SELECT id, tenant_id, principal_id, document_kind, document_version, content_hash,
       accepted_at, method, COALESCE(superseded_by::text, ''), subject_erased_at
  FROM legal_acceptance
 WHERE tenant_id = $1 AND principal_id = $2 AND document_kind = $3 AND document_version = $4`
	row := s.db.QueryRowContext(ctx, q, tenantID, principalID, string(kind), version)
	return scanAcceptance(row)
}

// ListForPrincipal returns one principal's acceptances within one tenant.
//
// 🔴 `tenant_id` is in the WHERE clause and is not optional. There is no variant of this query that
// omits it, which is what makes "no cross-tenant read exists on this path at all" a statement about the
// code rather than about intentions.
func (s *PGStore) ListForPrincipal(ctx context.Context, tenantID, principalID string) ([]Acceptance, error) {
	const q = `
SELECT id, tenant_id, principal_id, document_kind, document_version, content_hash,
       accepted_at, method, COALESCE(superseded_by::text, ''), subject_erased_at
  FROM legal_acceptance
 WHERE tenant_id = $1 AND principal_id = $2
 ORDER BY accepted_at DESC, id`
	rows, err := s.db.QueryContext(ctx, q, tenantID, principalID)
	if err != nil {
		return nil, fmt.Errorf("legal: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Acceptance
	for rows.Next() {
		a, err := scanAcceptance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkSuperseded sets superseded_by on prior unsuperseded acceptances of a kind (task 9.6).
//
// It runs across tenants deliberately and is the ONE operation here that does: publishing a material
// version affects every customer, and asking the caller to enumerate tenants would be both slower and a
// place to miss one.
//
// `newAcceptanceID` is optional. When empty the column is set to the row's own id — a non-null marker
// meaning "superseded by a later publication" without inventing a foreign key to a row that does not
// exist. The alternative, a nullable text column, would lose the referential guarantee for every case
// where a real successor DOES exist.
func (s *PGStore) MarkSuperseded(ctx context.Context, kind Kind, newVersion, newAcceptanceID string) (int, error) {
	const q = `
UPDATE legal_acceptance
   SET superseded_by = COALESCE(NULLIF($3, '')::uuid, id)
 WHERE document_kind = $1
   AND document_version <> $2
   AND superseded_by IS NULL`
	res, err := s.db.ExecContext(ctx, q, string(kind), newVersion, newAcceptanceID)
	if err != nil {
		return 0, fmt.Errorf("legal: supersede: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// DeleteOlderThan removes acceptances past the retention window.
//
// 🔴 The append-only trigger refuses DELETE. That is intentional and it means this statement FAILS unless
// the session has first disabled the trigger — which requires table ownership. The retention job is the
// only caller that gets that privilege, and the failure of any other caller is loud rather than silent.
//
// `session_replication_role = replica` is the mechanism: it suppresses user triggers for this session
// only, and is reset immediately. It is scoped to a transaction so a crash mid-run cannot leave a
// session with triggers off.
func (s *PGStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("legal: retention: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return 0, fmt.Errorf("legal: retention: cannot suspend the append-only guard (this requires table ownership): %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM legal_acceptance WHERE accepted_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("legal: retention: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("legal: retention: %w", err)
	}
	return int(n), nil
}

// EraseSubject tombstones a principal's rows (task 9.8).
//
// 🔴 It sets `subject_erased_at` and NOTHING else. The append-only trigger permits exactly that, so this
// statement is the only shape of erasure the database will accept — an implementation that also tried to
// blank the principal id would be refused by the trigger, which is the guarantee working rather than the
// guarantee being tested.
func (s *PGStore) EraseSubject(ctx context.Context, tenantID, principalID string, at time.Time) (int, error) {
	const q = `
UPDATE legal_acceptance
   SET subject_erased_at = $3
 WHERE tenant_id = $1 AND principal_id = $2 AND subject_erased_at IS NULL`
	res, err := s.db.ExecContext(ctx, q, tenantID, principalID, at)
	if err != nil {
		return 0, fmt.Errorf("legal: erase: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAcceptance(row scanner) (Acceptance, error) {
	var a Acceptance
	var kind, method string
	var erased sql.NullTime
	if err := row.Scan(&a.ID, &a.TenantID, &a.PrincipalID, &kind, &a.DocumentVersion,
		&a.ContentHash, &a.AcceptedAt, &method, &a.SupersededBy, &erased); err != nil {
		return Acceptance{}, err
	}
	a.DocumentKind = Kind(kind)
	a.Method = Method(method)
	if erased.Valid {
		t := erased.Time
		a.SubjectErasedAt = &t
	}
	return a, nil
}

// ── The manifest source ───────────────────────────────────────────────────────

// HTTPManifestSource reads `/legal/manifest.json` from the console this platform is deployed alongside.
//
// # Why over HTTP rather than from a shared file
//
// The documents live in the CONSOLE image (ADR-011) and the platform is a different container. Reading
// the console's own published manifest means the platform validates against exactly what a reader was
// shown — including after a console-only deploy, which is how a copy-fix ships.
//
// # Why a short timeout and no retry
//
// This sits on the commitment path. A slow manifest read must become "the acceptance was not recorded"
// quickly, not a hung request that the customer resolves by clicking again — which is the double-submit
// the unique constraint then has to absorb. Failing fast is kinder than failing slowly here.
type HTTPManifestSource struct {
	BaseURL string
	Client  *http.Client
}

// NewHTTPManifestSource builds a source with a bounded client.
func NewHTTPManifestSource(baseURL string) *HTTPManifestSource {
	return &HTTPManifestSource{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Manifest fetches and parses the published manifest.
func (h *HTTPManifestSource) Manifest(ctx context.Context) (Manifest, error) {
	if h.BaseURL == "" {
		return Manifest{}, errors.New("no console base URL configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/legal/manifest.json", nil)
	if err != nil {
		return Manifest{}, err
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("the console answered %d for the legal manifest", res.StatusCode)
	}
	// Bounded read: the manifest is a few kilobytes, and an unbounded read from a misbehaving upstream
	// on the commitment path is a memory exhaustion nobody would attribute to consent.
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("the legal manifest did not parse: %w", err)
	}
	if len(m.Kinds) == 0 {
		// An empty manifest is not a valid answer to "what is published". Treating it as one would make
		// every hash comparison fail with "unknown version", which reads as an attack rather than as an
		// upstream problem.
		return Manifest{}, errors.New("the legal manifest lists no documents")
	}
	return m, nil
}

// StaticManifestSource serves a fixed manifest. It exists for tests and for an air-gapped deployment
// that pins the manifest at build time rather than reading it over a network that does not exist.
type StaticManifestSource struct{ M Manifest }

// Manifest returns the fixed manifest.
func (s StaticManifestSource) Manifest(context.Context) (Manifest, error) { return s.M, nil }
