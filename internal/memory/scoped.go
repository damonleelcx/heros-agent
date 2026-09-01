package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// scoped.go binds each Store implementation to one tenant.
//
// # 🔴 Where the tenant comes from, in each implementation
//
// Episodes and summaries are keyed by goal id, and a goal belongs to a tenant — but this package holds
// no goals, so the two implementations answer "whose goal is this?" from different places.
//
// Postgres asks `goals`, which is the authority, with the same clause `store/scoped_pg.go` already uses:
// `goal_id IN (SELECT id FROM goals WHERE tenant = $n)`. 🚫 Deliberately NOT a denormalised `tenant`
// column on the episode tables. That would be faster and symmetric, and it would put tenancy in a second
// place that can disagree with `goals.tenant` — a split source of truth, which is the thing this
// codebase argues against everywhere else. Deriving it cannot drift, because there is nothing to drift
// from.
//
// The in-memory store has no goals table to ask, so it records the association on the first write and
// refuses any later write for the same goal under a different tenant. That is weaker than asking an
// authority, and it is the honest limit of an implementation that exists for tests and local runs;
// Postgres is what production uses, and the conformance suite asserts both refuse the same crossings.
//
// A row belonging to another tenant is INVISIBLE on read, not forbidden — returning "forbidden" would
// confirm the goal id is real, turning a guessable identifier into an enumeration of everybody's runs.
// On write it is REFUSED, because silently accepting an episode for somebody else's goal is how one
// customer's run ends up narrated into another's timeline.

// ── in memory ────────────────────────────────────────────────────────────────────────────────────

// For returns a tenant-scoped view of the in-process store.
func (m *Mem) For(tenant string) Store { return &memScoped{m: m, tenant: tenant} }

type memScoped struct {
	m      *Mem
	tenant string
}

// claim records that a goal belongs to this tenant, or reports that it already belongs to another.
//
// First writer wins. There is no goals table here to consult, so the first tenant to write history for a
// goal id defines whose it is — which is correct for every sequence a test or a local run can produce,
// and is why production uses the other implementation.
func (s *memScoped) claim(goalID string) bool {
	if s.tenant == "" {
		return false
	}
	m := s.m
	owner, seen := m.tenantOf[goalID]
	if seen {
		return owner == s.tenant
	}
	m.tenantOf[goalID] = s.tenant
	return true
}

func (s *memScoped) owns(goalID string) bool {
	return s.tenant != "" && s.m.tenantOf[goalID] == s.tenant
}

func (s *memScoped) AppendEpisode(e Episode) (int64, error) {
	s.m.mu.Lock()
	ok := s.claim(e.GoalID)
	s.m.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("%w: goal %q", ErrNotFound, e.GoalID)
	}
	return s.m.AppendEpisode(e)
}

func (s *memScoped) Episodes(goalID string) ([]Episode, error) {
	s.m.mu.Lock()
	ok := s.owns(goalID)
	s.m.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return s.m.Episodes(goalID)
}

func (s *memScoped) SaveSummary(sum Summary) (int64, error) {
	s.m.mu.Lock()
	ok := s.claim(sum.GoalID)
	s.m.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("%w: goal %q", ErrNotFound, sum.GoalID)
	}
	return s.m.SaveSummary(sum)
}

func (s *memScoped) Summaries(goalID string) ([]Summary, error) {
	s.m.mu.Lock()
	ok := s.owns(goalID)
	s.m.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return s.m.Summaries(goalID)
}

// 🔴 The tenant is IMPOSED on the way in, not read from the object. A caller that sets it themselves is
// a caller who can set it to somebody else.
func (s *memScoped) PromoteKnowledge(k Knowledge) error {
	k.Tenant = s.tenant
	return s.m.PromoteKnowledge(k)
}

func (s *memScoped) KnowledgeFor(_ string, subject string) ([]Knowledge, error) {
	return s.m.KnowledgeFor(s.tenant, subject)
}

func (s *memScoped) SetPreference(p Preference) error {
	p.Tenant = s.tenant
	return s.m.SetPreference(p)
}

func (s *memScoped) Preferences(string) ([]Preference, error) { return s.m.Preferences(s.tenant) }

// ── postgres ─────────────────────────────────────────────────────────────────────────────────────

// For returns a tenant-scoped view of the Postgres store.
func (p *PG) For(tenant string) Store { return &pgScoped{p: p, tenant: tenant} }

type pgScoped struct {
	p      *PG
	tenant string
}

// ownedClause restricts a goal id to the bound tenant. The same shape `store/scoped_pg.go` uses, so the
// two packages are checkable by reading rather than by tracing which caller validated what.
const ownedClause = ` AND goal_id IN (SELECT id FROM goals WHERE tenant = $%d)`

// AppendEpisode assigns the next sequence and stores the episode, for a goal this tenant owns.
//
// 🔴 The ownership test is part of the INSERT, not a lookup before it. A read-then-write would let a goal
// change hands between the two — and more practically, it would be a second statement somebody could
// return early before. Zero rows inserted is the refusal, and it reads back as ErrNoRows.
func (s *pgScoped) AppendEpisode(e Episode) (int64, error) {
	if s.tenant == "" {
		return 0, fmt.Errorf("memory: refusing to append an episode with no tenant")
	}
	tx, err := s.p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("memory: append episode: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The same advisory lock the unscoped path takes: the sequence is a per-goal counter and two workers
	// writing at once would otherwise choose the same number.
	if _, err := tx.ExecContext(context.Background(),
		`SELECT pg_advisory_xact_lock(hashtext($1))`, e.GoalID); err != nil {
		return 0, fmt.Errorf("memory: locking goal %q: %w", e.GoalID, err)
	}
	var seq int64
	err = tx.QueryRowContext(context.Background(), `
		INSERT INTO episodes (goal_id, seq, task_id, kind, summary, detail, at)
		SELECT $1, COALESCE((SELECT MAX(seq) FROM episodes WHERE goal_id = $1), 0) + 1,
		       $2, $3, $4, $5, $6
		WHERE EXISTS (SELECT 1 FROM goals WHERE id = $1 AND tenant = $7)
		RETURNING seq`,
		e.GoalID, e.TaskID, string(e.Kind), e.Summary, e.Detail, nz(e.At), s.tenant).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: goal %q", ErrNotFound, e.GoalID)
	}
	if err != nil {
		return 0, fmt.Errorf("memory: append episode: %w", err)
	}
	return seq, tx.Commit()
}

func (s *pgScoped) Episodes(goalID string) ([]Episode, error) {
	rows, err := s.p.db.QueryContext(context.Background(), `
		SELECT goal_id, seq, task_id, kind, summary, detail, at, COALESCE(summarised_by, 0)
		FROM episodes WHERE goal_id = $1`+fmt.Sprintf(ownedClause, 2)+` ORDER BY seq`,
		goalID, s.tenant)
	if err != nil {
		return nil, fmt.Errorf("memory: episodes: %w", err)
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		var kind string
		if err := rows.Scan(&e.GoalID, &e.Seq, &e.TaskID, &kind, &e.Summary, &e.Detail,
			&e.At, &e.SummarisedBy); err != nil {
			return nil, err
		}
		e.Kind = EpisodeKind(kind)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *pgScoped) SaveSummary(sum Summary) (int64, error) {
	if s.tenant == "" {
		return 0, fmt.Errorf("memory: refusing to save a summary with no tenant")
	}
	tx, err := s.p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("memory: save summary: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(context.Background(), `
		INSERT INTO episode_summaries (goal_id, from_seq, to_seq, content, dropped, at)
		SELECT $1, $2, $3, $4, $5, $6
		WHERE EXISTS (SELECT 1 FROM goals WHERE id = $1 AND tenant = $7)
		RETURNING id`,
		sum.GoalID, sum.FromSeq, sum.ToSeq, sum.Content, sum.Dropped, nz(sum.At), s.tenant).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: goal %q", ErrNotFound, sum.GoalID)
	}
	if err != nil {
		return 0, fmt.Errorf("memory: save summary: %w", err)
	}
	// Marking the covered episodes and writing the summary are one transaction, as in the unscoped path:
	// a summary claiming coverage it did not mark is a state nobody can reason about afterwards.
	if _, err := tx.ExecContext(context.Background(), `
		UPDATE episodes SET summarised_by = $1
		WHERE goal_id = $2 AND seq BETWEEN $3 AND $4`, id, sum.GoalID, sum.FromSeq, sum.ToSeq); err != nil {
		return 0, fmt.Errorf("memory: marking episodes: %w", err)
	}
	return id, tx.Commit()
}

func (s *pgScoped) Summaries(goalID string) ([]Summary, error) {
	rows, err := s.p.db.QueryContext(context.Background(), `
		SELECT id, goal_id, from_seq, to_seq, content, dropped, at
		FROM episode_summaries WHERE goal_id = $1`+fmt.Sprintf(ownedClause, 2)+` ORDER BY id`,
		goalID, s.tenant)
	if err != nil {
		return nil, fmt.Errorf("memory: summaries: %w", err)
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var sum Summary
		if err := rows.Scan(&sum.ID, &sum.GoalID, &sum.FromSeq, &sum.ToSeq, &sum.Content,
			&sum.Dropped, &sum.At); err != nil {
			return nil, err
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

func (s *pgScoped) PromoteKnowledge(k Knowledge) error {
	k.Tenant = s.tenant
	return s.p.PromoteKnowledge(k)
}

func (s *pgScoped) KnowledgeFor(_ string, subject string) ([]Knowledge, error) {
	return s.p.KnowledgeFor(s.tenant, subject)
}

func (s *pgScoped) SetPreference(p Preference) error {
	p.Tenant = s.tenant
	return s.p.SetPreference(p)
}

func (s *pgScoped) Preferences(string) ([]Preference, error) { return s.p.Preferences(s.tenant) }
