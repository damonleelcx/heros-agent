package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/heros-foreal/agentd/internal/infra/natsbus"
)

// Run processes due rows in scheduled_jobs on a fixed tick (MVP scheduler).
func Run(ctx context.Context, db *sql.DB, bus *natsbus.Bus, every time.Duration) {
	if every < 10*time.Second {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick(ctx, db, bus)
		}
	}
}

func tick(ctx context.Context, db *sql.DB, bus *natsbus.Bus) {
	q := `
SELECT id, tenant_id, interval_sec, action, payload_json FROM scheduled_jobs
WHERE enabled = 1 AND (
  last_fired_at IS NULL OR
  datetime(last_fired_at, '+' || CAST(interval_sec AS TEXT) || ' seconds') <= datetime('now')
)`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		log.Printf("scheduler query: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, tenantID, action string
		var intervalSec int
		var payload sql.NullString
		if err := rows.Scan(&id, &tenantID, &intervalSec, &action, &payload); err != nil {
			log.Printf("scheduler scan: %v", err)
			return
		}
		var payloadBytes []byte
		if payload.Valid {
			payloadBytes = []byte(payload.String)
		}
		if bus != nil {
			_ = bus.PublishJobFired(id, tenantID, action, payloadBytes)
		}
		if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET last_fired_at = datetime('now') WHERE id = ?`, id); err != nil {
			log.Printf("scheduler update: %v", err)
		}
	}
}

// CreateJob adds a row; returns generated id.
func CreateJob(db *sql.DB, tenantID, name string, intervalSec int, action, payloadJSON string) (string, error) {
	id := uuid.NewString()
	_, err := db.Exec(`
INSERT INTO scheduled_jobs (id, tenant_id, name, interval_sec, action, payload_json, enabled, last_fired_at)
VALUES (?, ?, ?, ?, ?, ?, 1, NULL)`,
		id, tenantID, name, intervalSec, action, payloadJSON)
	return id, err
}

// ListJobs returns jobs for a tenant (admin: pass empty tenant + admin flag to list all — simplified: tenant filter only).
func ListJobs(db *sql.DB, tenantID string, admin bool) ([]map[string]any, error) {
	var rows *sql.Rows
	var err error
	if admin || tenantID == "" {
		rows, err = db.Query(`SELECT id, tenant_id, name, interval_sec, action, payload_json, enabled, last_fired_at FROM scheduled_jobs ORDER BY name`)
	} else {
		rows, err = db.Query(`SELECT id, tenant_id, name, interval_sec, action, payload_json, enabled, last_fired_at FROM scheduled_jobs WHERE tenant_id = ? ORDER BY name`, tenantID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, tid, name, action, payload, last sql.NullString
		var interval, enabled int
		if err := rows.Scan(&id, &tid, &name, &interval, &action, &payload, &enabled, &last); err != nil {
			return nil, err
		}
		m := map[string]any{
			"id": id.String, "tenant_id": tid.String, "name": name.String,
			"interval_sec": interval, "action": action.String, "enabled": enabled,
		}
		if payload.Valid {
			m["payload_json"] = json.RawMessage(payload.String)
		}
		if last.Valid {
			m["last_fired_at"] = last.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
