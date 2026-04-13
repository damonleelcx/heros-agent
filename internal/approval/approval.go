package approval

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/sqltime"
)

type Layer string

const (
	LayerPrompt  Layer = "prompt_engineering"
	LayerContext Layer = "context_engineering"
	LayerHarness Layer = "harness_engineering"
	LayerTooling Layer = "tooling"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusApproved   Status = "approved"
	StatusRejected   Status = "rejected"
	StatusSuperseded Status = "superseded"
)

type Proposal struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Layer       Layer      `json:"layer"`
	Title       string     `json:"title"`
	Rationale   string     `json:"rationale"`
	DiffText    string     `json:"diff_text"`
	Status      Status     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
	ContentHash string     `json:"content_hash,omitempty"`
	RollbackRef string     `json:"rollback_ref,omitempty"`
}

func hashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Submit queues a human-readable mutation for review (spine: proposal → queue → human).
func Submit(db *sql.DB, tenantID string, layer Layer, title, rationale, diff string) (*Proposal, error) {
	id := newID()
	h := hashContent(diff)
	_, err := db.Exec(`
		INSERT INTO proposals (id, tenant_id, layer, title, rationale, diff_text, status, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, tenantID, string(layer), title, rationale, diff, string(StatusPending), h)
	if err != nil {
		return nil, err
	}
	return Get(db, id)
}

func Get(db *sql.DB, id string) (*Proposal, error) {
	var p Proposal
	var created, reviewed, rollback sql.NullString
	err := db.QueryRow(`
		SELECT id, tenant_id, layer, title, rationale, diff_text, status, created_at, reviewed_at, content_hash, rollback_ref
		FROM proposals WHERE id = ?`, id).Scan(
		&p.ID, &p.TenantID, &p.Layer, &p.Title, &p.Rationale, &p.DiffText, &p.Status, &created, &reviewed, &p.ContentHash, &rollback)
	if err != nil {
		return nil, err
	}
	p.CreatedAt = sqltime.Parse(created.String)
	if reviewed.Valid {
		t := sqltime.Parse(reviewed.String)
		p.ReviewedAt = &t
	}
	if rollback.Valid {
		p.RollbackRef = rollback.String
	}
	return &p, nil
}

// CanAccess returns true if principal may touch this proposal.
func CanAccess(p *Proposal, tenantID string, admin bool) bool {
	if p == nil {
		return false
	}
	if admin {
		return true
	}
	if tenantID == "" {
		return p.TenantID == ""
	}
	return p.TenantID == tenantID
}

func ListPending(db *sql.DB, tenantID string, admin bool) ([]Proposal, error) {
	var rows *sql.Rows
	var err error
	if admin {
		rows, err = db.Query(`
			SELECT id, tenant_id, layer, title, rationale, diff_text, status, created_at, reviewed_at, content_hash, rollback_ref
			FROM proposals WHERE status = ? ORDER BY created_at ASC`, string(StatusPending))
	} else {
		rows, err = db.Query(`
			SELECT id, tenant_id, layer, title, rationale, diff_text, status, created_at, reviewed_at, content_hash, rollback_ref
			FROM proposals WHERE status = ? AND tenant_id = ? ORDER BY created_at ASC`, string(StatusPending), tenantID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProposalRows(rows)
}

func ListRecent(db *sql.DB, tenantID string, admin bool, limit int) ([]Proposal, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if admin {
		rows, err = db.Query(`
			SELECT id, tenant_id, layer, title, rationale, diff_text, status, created_at, reviewed_at, content_hash, rollback_ref
			FROM proposals ORDER BY created_at DESC LIMIT ?`, limit)
	} else {
		rows, err = db.Query(`
			SELECT id, tenant_id, layer, title, rationale, diff_text, status, created_at, reviewed_at, content_hash, rollback_ref
			FROM proposals WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ?`, tenantID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProposalRows(rows)
}

func scanProposalRows(rows *sql.Rows) ([]Proposal, error) {
	var out []Proposal
	for rows.Next() {
		var p Proposal
		var created, reviewed, rollback sql.NullString
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Layer, &p.Title, &p.Rationale, &p.DiffText, &p.Status, &created, &reviewed, &p.ContentHash, &rollback); err != nil {
			return nil, err
		}
		p.CreatedAt = sqltime.Parse(created.String)
		if reviewed.Valid {
			t := sqltime.Parse(reviewed.String)
			p.ReviewedAt = &t
		}
		if rollback.Valid {
			p.RollbackRef = rollback.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Reject marks a proposal rejected; no state change to live systems.
func Reject(db *sql.DB, id string) error {
	res, err := db.Exec(`UPDATE proposals SET status = ?, reviewed_at = datetime('now') WHERE id = ? AND status = ?`,
		string(StatusRejected), id, string(StatusPending))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("proposal not found or not pending")
	}
	return nil
}
