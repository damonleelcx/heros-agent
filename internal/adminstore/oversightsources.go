package adminstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
)

// oversightsources.go implements the three read seams that had no implementation anywhere in the tree:
// `ReleaseSource`, `AxisAdoptionSource` and `SubjectContentStore`.
//
// # The honest framing, because it is the whole point of these three
//
// None of them fabricates. Where a store genuinely holds nothing, the surface renders EMPTY — which is
// a different and much better answer than "this deployment does not carry the surface". "No releases
// have been recorded" is a fact an operator can act on; "the surface is absent" tells them the console
// is broken. That distinction is the reason these exist.
//
// Each one names its real source in `Describe()`, so an operator can tell a real record from a fixture
// without reading this file.

// ── Releases ────────────────────────────────────────────────────────────────────────────────────

// Releases is the Postgres-backed release record (migration 0037).
type Releases struct{ db *sql.DB }

// NewReleases wraps a live platform pool.
func NewReleases(db *sql.DB) (*Releases, error) {
	if db == nil {
		return nil, errors.New("adminstore: the release record needs the platform database")
	}
	return &Releases{db: db}, nil
}

// Releases implements adminops.ReleaseSource.
//
// The interface returns no error, so a failed READ cannot be distinguished from an empty record by its
// signature. It is therefore LOGGED and returns nil — and nil is what the read model renders as "no
// releases recorded", not as a verified-empty channel. A silent wrong answer here would tell an
// operator a channel publishes nothing when in fact the database was unreachable.
func (r *Releases) Releases() []adminops.ReleaseRecord {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.version, r.channel, r.published_at, r.signing_key_id,
		       a.platform, a.name, a.digest, a.verified, a.published
		FROM release_record r
		LEFT JOIN release_artefact a ON a.version = r.version AND a.channel = r.channel
		ORDER BY r.published_at DESC, r.version, r.channel, a.platform, a.name`)
	if err != nil {
		log.Printf("adminstore: the release record could not be read (%v) — the releases page will render "+
			"empty, which is NOT the same as 'this channel published nothing'", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	type key struct{ version, channel string }
	order := []key{}
	byKey := map[key]*adminops.ReleaseRecord{}
	for rows.Next() {
		var (
			version, channel, signingKey string
			publishedAt                  time.Time
			platform, name, digest       sql.NullString
			verified, published          sql.NullBool
		)
		if err := rows.Scan(&version, &channel, &publishedAt, &signingKey,
			&platform, &name, &digest, &verified, &published); err != nil {
			log.Printf("adminstore: scanning a release row failed (%v)", err)
			return nil
		}
		_ = digest // recorded in the table for auditability; the read model shows verification, not digests.
		k := key{version, channel}
		rec, ok := byKey[k]
		if !ok {
			rec = &adminops.ReleaseRecord{
				Version: version, Channel: channel,
				PublishedAt:  publishedAt.UTC().Format(time.RFC3339),
				SigningKeyID: signingKey,
				Artefacts:    []adminops.ArtefactRecord{},
			}
			byKey[k] = rec
			order = append(order, k)
		}
		// The LEFT JOIN yields one NULL artefact row for a release that has none. A release with no
		// artefacts is a real state (recorded, nothing published yet) and must not become a phantom row.
		if !platform.Valid || !name.Valid {
			continue
		}
		rec.Artefacts = append(rec.Artefacts, adminops.ArtefactRecord{
			Platform: platform.String, Name: name.String,
			// A NULL `published` means the row predates the column's default; treat it as published, which
			// is what a recorded artefact row means.
			Published: !published.Valid || published.Bool,
			// 🔴 The three-way map. NULL is `not_yet_verified`, NOT `failed` — see the migration header:
			// rendering "not checked" as "failed" sends an operator hunting a compromise that never
			// happened.
			Verification: verifyState(verified),
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("adminstore: reading the release record failed (%v)", err)
		return nil
	}
	out := make([]adminops.ReleaseRecord, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// verifyState maps the nullable column onto the read model's three states.
//
// NULL is "not yet verified" and FALSE is "failed", and they must never collapse: one is work not done,
// the other is a finding.
func verifyState(v sql.NullBool) adminops.VerifyState {
	switch {
	case !v.Valid:
		return adminops.VerifyNotYet
	case v.Bool:
		return adminops.VerifyVerified
	default:
		return adminops.VerifyFailed
	}
}

// Describe implements adminops.ReleaseSource.
func (r *Releases) Describe() string { return "postgres release_record + release_artefact" }

// ── Axis adoption ───────────────────────────────────────────────────────────────────────────────

// AxisAdoption reads per-axis fleet adoption and refusals from the `proposal` table.
//
// # Why `proposal` and not `coverage_item`
//
// `coverage_item` looks like the obvious source and is not: it is EVAL coverage — which cases an eval
// set exercises — keyed by `eval_set_hash`. Per-axis fleet adoption is a different question with no
// shared key. `proposal` is the right table: it carries `tenant_id`, `node_id` and, on a refusal,
// `refusal_dimension` — and a DIMENSION IS AN AXIS. That column is the only axis attribution the schema
// records, which decides exactly how much this can honestly report (below).
type AxisAdoption struct{ db *sql.DB }

// NewAxisAdoption wraps a live platform pool.
func NewAxisAdoption(db *sql.DB) (*AxisAdoption, error) {
	if db == nil {
		return nil, errors.New("adminstore: axis adoption needs the platform database")
	}
	return &AxisAdoption{db: db}, nil
}

// Adoption returns the tenants and nodes carrying a proposal attributed to this axis.
//
// 🔴 What this can and cannot see, stated because the number is small for a REASON and an operator
// reading zero deserves to know which reason. `proposal` attributes a row to an axis only through
// `refusal_dimension`, which is populated on REFUSALS. An accepted proposal carries no dimension column,
// so it cannot be attributed to an axis from this table at all. Adoption therefore counts what is
// attributable and no more — a zero means "nothing on this axis is attributable in the proposal
// record", never "no tenant uses this axis". Making the accepted path attributable is a schema change,
// not something this reader can infer.
func (a *AxisAdoption) Adoption(axis string) (tenants, nodes int) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT tenant_id), COUNT(DISTINCT node_id)
		FROM proposal WHERE refusal_dimension = $1`, axis).Scan(&tenants, &nodes)
	if err != nil {
		log.Printf("adminstore: axis adoption for %q could not be read (%v) — reporting zero, which the "+
			"page must not read as 'nobody adopted it'", axis, err)
		return 0, 0
	}
	return tenants, nodes
}

// RefusedNodes returns the nodes the engine refused on this axis, with the typed cause.
//
// This half maps directly and completely: `refusal_dimension` is the axis and `refusal_reason` is the
// typed cause, both recorded at the moment of refusal.
func (a *AxisAdoption) RefusedNodes(axis string) []adminops.RefusedNode {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	rows, err := a.db.QueryContext(ctx, `
		SELECT tenant_id, node_id, COALESCE(refusal_reason, '')
		FROM proposal
		WHERE refusal_dimension = $1 AND refusal_reason IS NOT NULL AND refusal_reason <> ''
		ORDER BY tenant_id, node_id`, axis)
	if err != nil {
		log.Printf("adminstore: refused nodes for axis %q could not be read (%v)", axis, err)
		return nil
	}
	defer func() { _ = rows.Close() }()
	out := []adminops.RefusedNode{}
	for rows.Next() {
		var n adminops.RefusedNode
		if err := rows.Scan(&n.TenantID, &n.NodeID, &n.Cause); err != nil {
			log.Printf("adminstore: scanning a refused node failed (%v)", err)
			return nil
		}
		n.Axis = axis
		// Language is deliberately left empty rather than guessed. `proposal` does not record it, and a
		// wrong language on a refusal row sends somebody to fix the wrong rewriter.
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		log.Printf("adminstore: reading refused nodes for axis %q failed (%v)", axis, err)
		return nil
	}
	return out
}

// Describe implements adminops.AxisAdoptionSource.
func (a *AxisAdoption) Describe() string {
	return "postgres proposal.refusal_dimension (refusals are fully attributed; accepted proposals carry no axis column)"
}

// ── GDPR subject content ────────────────────────────────────────────────────────────────────────

// subjectTables are the tables whose rows ARE the subject's content, keyed by the column that holds the
// subject reference.
//
// 🔴 The exclusions are the important part of this list, and each is a legal obligation rather than an
// oversight:
//
//   - `audit_entry` — append-only with a write-once trigger. The GDPR design's own rule is that erasure
//     appends a non-PII TOMBSTONE and never removes an audit row, because a chain with a hole is a chain
//     that cannot be verified. Deleting from it is not merely wrong, it is impossible by construction.
//   - `billing_event`, `billing_state`, `usage_record`, `account` — financial records under a statutory
//     retention period that outlives an erasure request. Erasing them would destroy the evidence for
//     invoices already issued.
//   - `legal_acceptance` — the record that the subject accepted terms, which is the lawful basis being
//     relied on. Erasing the consent record erases the proof of consent.
//
// What remains is the subject's CONTENT: their workflows, their runs, their proposals, their deliveries.
var subjectTables = []struct{ table, column string }{
	{"authored_change", "tenant_id"},
	{"delivery", "tenant_id"},
	{"delivery_route", "tenant_id"},
	{"impersonation_session", "tenant_id"},
	{"platform_workflow_graph", "tenant_id"},
	{"proposal", "tenant_id"},
	{"run_link", "tenant_id"},
	{"run_link_coverage", "tenant_id"},
	{"source_bundle", "tenant_id"},
	{"workflow_ir", "tenant_id"},
}

// SubjectContent erases a data subject's content across the platform's own stores.
type SubjectContent struct{ db *sql.DB }

// NewSubjectContent wraps a live platform pool.
func NewSubjectContent(db *sql.DB) (*SubjectContent, error) {
	if db == nil {
		return nil, errors.New("adminstore: subject erasure needs the platform database")
	}
	return &SubjectContent{db: db}, nil
}

// Tombstone removes every record belonging to subjectRef and reports how many it acted on.
//
// # One transaction, and why
//
// A half-completed erasure is the worst outcome available here: the operator is told it failed, the
// subject's data is partly gone, and nobody knows which part. All of it commits or none of it does, and
// the operator retries against a known state.
//
// # Idempotent by construction
//
// A re-run after a partial failure deletes whatever is left and returns that count, which is what the
// interface requires. It does not fail because some rows are already gone — "already erased" is success.
func (s *SubjectContent) Tombstone(ctx context.Context, subjectRef string) (int, error) {
	if strings.TrimSpace(subjectRef) == "" {
		return 0, errors.New("adminstore: an erasure needs the subject reference — an empty one would match every row")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("adminstore: erasure for %q: %w", subjectRef, err)
	}
	defer func() { _ = tx.Rollback() }()

	removed := 0
	for _, t := range subjectTables {
		// The table and column names come from the constant list above, never from input — this string is
		// assembled from code, and the subject reference is always a bound parameter.
		res, err := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, t.table, t.column), subjectRef)
		if err != nil {
			return 0, fmt.Errorf("adminstore: erasure for %q failed on %s (nothing was erased — the whole "+
				"operation is one transaction): %w", subjectRef, t.table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("adminstore: erasure for %q on %s: %w", subjectRef, t.table, err)
		}
		removed += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("adminstore: erasure for %q could not be committed (nothing was erased): %w", subjectRef, err)
	}
	return removed, nil
}

// Remaining reports how many records still hold the subject's content.
//
// This is what makes a completion VERIFIABLE rather than asserted: an auditor asks the store, not the
// console. It counts the same tables the erasure acts on, so a non-zero answer after a completed
// erasure is a real finding rather than a definitional mismatch.
func (s *SubjectContent) Remaining(ctx context.Context, subjectRef string) (int, error) {
	if strings.TrimSpace(subjectRef) == "" {
		return 0, errors.New("adminstore: counting remaining content needs the subject reference")
	}
	total := 0
	for _, t := range subjectTables {
		var n int
		err := s.db.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1`, t.table, t.column), subjectRef).Scan(&n)
		if err != nil {
			// Fail rather than under-report. A count that silently skipped a table would certify an
			// erasure as complete while the subject's content sat in the table nobody could read.
			return 0, fmt.Errorf("adminstore: counting remaining content for %q on %s: %w", subjectRef, t.table, err)
		}
		total += n
	}
	return total, nil
}

// Describe names the store for the completion record.
func (s *SubjectContent) Describe() string {
	names := make([]string, 0, len(subjectTables))
	for _, t := range subjectTables {
		names = append(names, t.table)
	}
	return "postgres subject content across " + strings.Join(names, ", ") +
		" (audit, billing and consent records are retained by obligation and are NOT erased)"
}
