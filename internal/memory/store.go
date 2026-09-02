package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// store.go holds the two implementations of Store: one in memory, one on Postgres.
//
// 🔴 Both exist for the same reason the goal store has two: tests and local runs use one, production
// uses the other, and two implementations of one interface diverge silently unless something asserts
// they behave the same. The divergence always surfaces in the implementation nobody exercised.

var ErrNotFound = errors.New("memory: not found")

// ── in memory ────────────────────────────────────────────────────────────────────────────────────

// Mem is an in-process Store. Correct under concurrency; not durable across a restart.
type Mem struct {
	mu        sync.Mutex
	episodes  map[string][]Episode
	summaries map[string][]Summary
	knowledge map[string][]Knowledge // keyed tenant\x00subject
	prefs     map[string]map[string]Preference
	nextSum   int64
	// turns holds conversation transcripts, keyed tenant\x00conversation. turnOrder records the write
	// order across all conversations so LatestConversation can answer without consulting timestamps.
	turns     map[string][]Turn
	turnOrder []string
	// tenantOf records which tenant a goal's history belongs to, first writer wins.
	//
	// 🔴 Only the SCOPED view reads or writes this. Postgres answers the same question from `goals`,
	// which is the authority; this store has no goals to ask, so it remembers instead. See scoped.go.
	tenantOf map[string]string
}

// NewMem builds an empty in-process store.
func NewMem() *Mem {
	return &Mem{
		episodes: map[string][]Episode{}, summaries: map[string][]Summary{},
		knowledge: map[string][]Knowledge{}, prefs: map[string]map[string]Preference{},
		turns:    map[string][]Turn{},
		tenantOf: map[string]string{},
	}
}

func subjectKey(tenant, subject string) string { return tenant + "\x00" + subject }

func (m *Mem) AppendEpisode(e Episode) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 🔴 The sequence is assigned HERE, under the lock, not by the caller. Two workers writing episodes
	// for one goal would otherwise choose the same number, and the order of a run is the one thing an
	// episodic record is for.
	e.Seq = int64(len(m.episodes[e.GoalID])) + 1
	m.episodes[e.GoalID] = append(m.episodes[e.GoalID], e)
	return e.Seq, nil
}

func (m *Mem) Episodes(goalID string) ([]Episode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Episode(nil), m.episodes[goalID]...), nil
}

func (m *Mem) SaveSummary(s Summary) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSum++
	s.ID = m.nextSum
	m.summaries[s.GoalID] = append(m.summaries[s.GoalID], s)
	for i := range m.episodes[s.GoalID] {
		if e := &m.episodes[s.GoalID][i]; e.Seq >= s.FromSeq && e.Seq <= s.ToSeq {
			e.SummarisedBy = s.ID
		}
	}
	return s.ID, nil
}

func (m *Mem) Summaries(goalID string) ([]Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Summary(nil), m.summaries[goalID]...), nil
}

func (m *Mem) PromoteKnowledge(k Knowledge) error {
	if k.EvidenceGoalID == "" || len(k.EvidenceSeqs) == 0 {
		return fmt.Errorf("%w: %q", ErrNoEvidence, k.Key)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := subjectKey(k.Tenant, k.Subject)
	// A later claim on the same key supersedes the earlier one. The earlier is kept.
	for i := range m.knowledge[key] {
		if m.knowledge[key][i].Key == k.Key && m.knowledge[key][i].SupersededBy == "" {
			m.knowledge[key][i].SupersededBy = k.EvidenceGoalID
		}
	}
	m.knowledge[key] = append(m.knowledge[key], k)
	return nil
}

func (m *Mem) KnowledgeFor(tenant, subject string) ([]Knowledge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Knowledge
	for _, k := range m.knowledge[subjectKey(tenant, subject)] {
		if k.SupersededBy == "" {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *Mem) SetPreference(p Preference) error {
	if err := ValidatePreference(p); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prefs[p.Tenant] == nil {
		m.prefs[p.Tenant] = map[string]Preference{}
	}
	m.prefs[p.Tenant][p.Key] = p
	return nil
}

func (m *Mem) Preferences(tenant string) ([]Preference, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Preference
	for _, p := range m.prefs[tenant] {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

var _ Store = (*Mem)(nil)

// ── postgres ─────────────────────────────────────────────────────────────────────────────────────

// PG is the durable Store.
type PG struct{ db *sql.DB }

// NewPG wraps a pool.
func NewPG(db *sql.DB) *PG { return &PG{db: db} }

var _ Store = (*PG)(nil)

// AppendEpisode assigns the next sequence for this goal and writes the row.
//
// # 🔴 Why an advisory lock, and the bug that put it here
//
// The first version computed the number inside the INSERT — `SELECT COALESCE(MAX(seq),0)+1` — with a
// comment claiming "the table's own locking settles it". It does not. Under READ COMMITTED two
// transactions both read the same maximum, both insert it, and the primary key rejects the second: 16
// concurrent writers produced 6 distinct sequences and 10 errors.
//
// MAX() takes no lock that stops another transaction reading the same value. The lock has to be
// explicit, and it has to be on the GOAL — serialising all episode writes globally would make one busy
// run slow every other run in the system.
//
// `pg_advisory_xact_lock` is released when the transaction ends, including on rollback or a dropped
// connection, so a worker that dies mid-append cannot wedge a goal's episode log.
//
// 🚫 Not solved by retrying the unique violation. A retry loop converges, but it converts a designed
// ordering into a race that usually works — and "usually" is measured in whichever concurrency the test
// happened to use.
func (p *PG) AppendEpisode(e Episode) (int64, error) {
	tx, err := p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("memory: append episode: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// hashtext maps the goal id onto the advisory lock space. A collision between two different goals
	// costs a little contention and nothing else — the lock guards a sequence, not a correctness claim.
	if _, err := tx.ExecContext(context.Background(),
		`SELECT pg_advisory_xact_lock(hashtext($1))`, e.GoalID); err != nil {
		return 0, fmt.Errorf("memory: locking goal %q: %w", e.GoalID, err)
	}
	var seq int64
	if err := tx.QueryRowContext(context.Background(), `
		INSERT INTO episodes (goal_id, seq, task_id, kind, summary, detail, at)
		SELECT $1, COALESCE(MAX(seq), 0) + 1, $2, $3, $4, $5, $6 FROM episodes WHERE goal_id = $1
		RETURNING seq`,
		e.GoalID, e.TaskID, string(e.Kind), e.Summary, e.Detail, nz(e.At)).Scan(&seq); err != nil {
		return 0, fmt.Errorf("memory: append episode: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("memory: append episode: %w", err)
	}
	return seq, nil
}

func (p *PG) Episodes(goalID string) ([]Episode, error) {
	rows, err := p.db.QueryContext(context.Background(), `
		SELECT goal_id, seq, task_id, kind, summary, detail, at, COALESCE(summarised_by, 0)
		FROM episodes WHERE goal_id = $1 ORDER BY seq`, goalID)
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

func (p *PG) SaveSummary(s Summary) (int64, error) {
	tx, err := p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("memory: save summary: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	if err := tx.QueryRowContext(context.Background(), `
		INSERT INTO episode_summaries (goal_id, from_seq, to_seq, content, dropped, at)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		s.GoalID, s.FromSeq, s.ToSeq, s.Content, s.Dropped, nz(s.At)).Scan(&id); err != nil {
		return 0, fmt.Errorf("memory: save summary: %w", err)
	}
	// Marking the covered episodes and writing the summary are one transaction: a summary claiming
	// coverage it did not mark, or episodes marked by a summary that failed to write, are both states
	// nobody can reason about afterwards.
	if _, err := tx.ExecContext(context.Background(), `
		UPDATE episodes SET summarised_by = $1
		WHERE goal_id = $2 AND seq BETWEEN $3 AND $4`, id, s.GoalID, s.FromSeq, s.ToSeq); err != nil {
		return 0, fmt.Errorf("memory: marking episodes: %w", err)
	}
	return id, tx.Commit()
}

func (p *PG) Summaries(goalID string) ([]Summary, error) {
	rows, err := p.db.QueryContext(context.Background(), `
		SELECT goal_id, id, from_seq, to_seq, content, dropped, at
		FROM episode_summaries WHERE goal_id = $1 ORDER BY id`, goalID)
	if err != nil {
		return nil, fmt.Errorf("memory: summaries: %w", err)
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.GoalID, &s.ID, &s.FromSeq, &s.ToSeq, &s.Content, &s.Dropped, &s.At); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *PG) PromoteKnowledge(k Knowledge) error {
	if k.EvidenceGoalID == "" || len(k.EvidenceSeqs) == 0 {
		return fmt.Errorf("%w: %q", ErrNoEvidence, k.Key)
	}
	seqs, err := json.Marshal(k.EvidenceSeqs)
	if err != nil {
		return err
	}
	tx, err := p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("memory: promote: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(context.Background(), `
		UPDATE knowledge SET superseded_by = $4
		WHERE tenant = $1 AND subject = $2 AND key = $3 AND superseded_by IS NULL`,
		k.Tenant, k.Subject, k.Key, k.EvidenceGoalID); err != nil {
		return fmt.Errorf("memory: superseding: %w", err)
	}
	if _, err := tx.ExecContext(context.Background(), `
		INSERT INTO knowledge (tenant, subject, key, value, evidence_goal_id, evidence_seqs, at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		k.Tenant, k.Subject, k.Key, k.Value, k.EvidenceGoalID, seqs, nz(k.At)); err != nil {
		return fmt.Errorf("memory: promote: %w", err)
	}
	return tx.Commit()
}

func (p *PG) KnowledgeFor(tenant, subject string) ([]Knowledge, error) {
	rows, err := p.db.QueryContext(context.Background(), `
		SELECT tenant, subject, key, value, evidence_goal_id, evidence_seqs, at
		FROM knowledge WHERE tenant = $1 AND subject = $2 AND superseded_by IS NULL ORDER BY key`,
		tenant, subject)
	if err != nil {
		return nil, fmt.Errorf("memory: knowledge: %w", err)
	}
	defer rows.Close()
	var out []Knowledge
	for rows.Next() {
		var k Knowledge
		var seqs []byte
		if err := rows.Scan(&k.Tenant, &k.Subject, &k.Key, &k.Value,
			&k.EvidenceGoalID, &seqs, &k.At); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(seqs, &k.EvidenceSeqs)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (p *PG) SetPreference(pref Preference) error {
	if err := ValidatePreference(pref); err != nil {
		return err
	}
	_, err := p.db.ExecContext(context.Background(), `
		INSERT INTO preferences (tenant, key, value, authored_by, at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (tenant, key) DO UPDATE
		  SET value = EXCLUDED.value, authored_by = EXCLUDED.authored_by, at = EXCLUDED.at`,
		pref.Tenant, pref.Key, pref.Value, pref.AuthoredBy, nz(pref.At))
	if err != nil {
		return fmt.Errorf("memory: set preference: %w", err)
	}
	return nil
}

func (p *PG) Preferences(tenant string) ([]Preference, error) {
	rows, err := p.db.QueryContext(context.Background(), `
		SELECT tenant, key, value, authored_by, at FROM preferences WHERE tenant = $1 ORDER BY key`,
		tenant)
	if err != nil {
		return nil, fmt.Errorf("memory: preferences: %w", err)
	}
	defer rows.Close()
	var out []Preference
	for rows.Next() {
		var pr Preference
		if err := rows.Scan(&pr.Tenant, &pr.Key, &pr.Value, &pr.AuthoredBy, &pr.At); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ── conversation ─────────────────────────────────────────────────────────────────────────────────

// AppendTurn assigns the next sequence for this conversation and writes the row.
//
// 🔴 The same advisory-lock reasoning as AppendEpisode, for the same reason and with the same bug
// waiting behind the naive version: `COALESCE(MAX(seq),0)+1` inside the INSERT takes no lock that stops
// another transaction reading the same maximum. Two browser tabs on one conversation are exactly the
// concurrency that produces it. The lock is on the CONVERSATION, so one busy thread does not serialise
// every other thread in the deployment.
func (p *PG) AppendTurn(t Turn) (int64, error) {
	if err := ValidateTurn(t); err != nil {
		return 0, err
	}
	tx, err := p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("memory: append turn: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 🔴 The TWO-KEY form, locking on (tenant, conversation) as a pair rather than on a joined string.
	//
	// The obvious version — `hashtext(tenant || sep || conversation)` — has to choose a separator, and
	// the separator this package uses everywhere else in Go is "\x00" (see subjectKey). Postgres TEXT
	// cannot hold a NUL byte, so that version does not fail at a boundary or under load: it fails on
	// the FIRST call, with `invalid byte sequence for encoding "UTF8"`. Caught by the Postgres leg of
	// the conformance suite, which is the whole reason it exists.
	//
	// See workflow/CI/bugfix/20260901-heros-console-was-never-a-conversation.md
	// Fenced by TestTurnsAreOrderedByTheStore on the Postgres leg.
	//
	// Two keys also removes the question entirely: there is no separator to collide across, so a tenant
	// whose id ends where a conversation id begins cannot share a lock with a different pair.
	if _, err := tx.ExecContext(context.Background(),
		`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		t.Tenant, t.ConversationID); err != nil {
		return 0, fmt.Errorf("memory: locking conversation %q: %w", t.ConversationID, err)
	}
	var seq int64
	if err := tx.QueryRowContext(context.Background(), `
		INSERT INTO conversation_turns
		  (tenant, conversation_id, seq, role, body, kind, capability, decided_by, cost_micro_cents, at)
		SELECT $1, $2, COALESCE(MAX(seq), 0) + 1, $3, $4, $5, $6, $7, $8, $9
		  FROM conversation_turns WHERE tenant = $1 AND conversation_id = $2
		RETURNING seq`,
		t.Tenant, t.ConversationID, string(t.Role), t.Body, t.Kind, t.Capability,
		string(t.Decided), t.CostMicroCents, nz(t.At)).Scan(&seq); err != nil {
		return 0, fmt.Errorf("memory: append turn: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("memory: append turn: %w", err)
	}
	return seq, nil
}

const turnColumns = `SELECT tenant, conversation_id, seq, role, body, kind, capability,
	decided_by, cost_micro_cents, at FROM conversation_turns`

func scanTurns(rows *sql.Rows) ([]Turn, error) {
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var t Turn
		var role, decided string
		if err := rows.Scan(&t.Tenant, &t.ConversationID, &t.Seq, &role, &t.Body, &t.Kind,
			&t.Capability, &decided, &t.CostMicroCents, &t.At); err != nil {
			return nil, err
		}
		t.Role, t.Decided = TurnRole(role), Decider(decided)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *PG) Turns(tenant, conversationID string) ([]Turn, error) {
	rows, err := p.db.QueryContext(context.Background(),
		turnColumns+` WHERE tenant = $1 AND conversation_id = $2 ORDER BY seq`, tenant, conversationID)
	if err != nil {
		return nil, fmt.Errorf("memory: turns: %w", err)
	}
	return scanTurns(rows)
}

func (p *PG) LatestConversation(tenant string) (string, bool, error) {
	var id string
	// Tie-broken by conversation_id so two turns written in the same instant resolve the same way on
	// every call, rather than the answer flickering between two threads on consecutive refreshes.
	err := p.db.QueryRowContext(context.Background(), `
		SELECT conversation_id FROM conversation_turns WHERE tenant = $1
		ORDER BY at DESC, conversation_id DESC LIMIT 1`, tenant).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("memory: latest conversation: %w", err)
	}
	return id, true, nil
}

func (m *Mem) AppendTurn(t Turn) (int64, error) {
	if err := ValidateTurn(t); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 🔴 Assigned here, under the lock, for the reason AppendEpisode states: two writers choosing their
	// own numbers is how the order of a conversation stops being one.
	k := subjectKey(t.Tenant, t.ConversationID)
	t.Seq = int64(len(m.turns[k])) + 1
	t.At = nz(t.At)
	m.turns[k] = append(m.turns[k], t)
	m.turnOrder = append(m.turnOrder, k)
	return t.Seq, nil
}

func (m *Mem) Turns(tenant, conversationID string) ([]Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Turn(nil), m.turns[subjectKey(tenant, conversationID)]...), nil
}

// LatestConversation answers from write ORDER rather than from timestamps.
//
// 🔴 Deliberately different from the Postgres implementation, and the conformance suite is what keeps
// them honest. Turns written in one test run share a wall-clock instant at this resolution, so ordering
// by `At` here would return an arbitrary one of them — a difference between the two stores that would
// show up as a flaky test rather than as the bug it is.
func (m *Mem) LatestConversation(tenant string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := tenant + "\x00"
	for i := len(m.turnOrder) - 1; i >= 0; i-- {
		if k := m.turnOrder[i]; strings.HasPrefix(k, prefix) {
			return strings.TrimPrefix(k, prefix), true, nil
		}
	}
	return "", false, nil
}

func nz(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}
