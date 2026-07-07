package syncsnapshot

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
)

func RecordLedger(db *sql.DB, direction, source string, rep ImportReport) error {
	conflicts, _ := json.Marshal(rep.Conflicts)
	_, err := db.Exec(`INSERT INTO sync_ledger (direction, source, version, applied, conflict_count, conflicts_json) VALUES (?, ?, ?, ?, ?, ?)`,
		direction, source, 1, rep.Applied, len(rep.Conflicts), string(conflicts))
	return err
}

func RecordLedgerSnapshot(db *sql.DB, direction, source string, rep ImportReport, snap Snapshot) error {
	payload, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	conflicts, _ := json.Marshal(rep.Conflicts)
	_, err = db.Exec(`INSERT INTO sync_ledger (direction, source, version, applied, conflict_count, conflicts_json, snapshot_json, snapshot_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		direction, source, 1, rep.Applied, len(rep.Conflicts), string(conflicts), string(payload), hex.EncodeToString(sum[:]))
	return err
}

func LatestSnapshot(db *sql.DB) (*Snapshot, int64, error) {
	row := db.QueryRow(`SELECT id, snapshot_json FROM sync_ledger WHERE snapshot_json IS NOT NULL AND snapshot_json <> '' AND direction <> 'rollback' ORDER BY id DESC LIMIT 1`)
	var id int64
	var raw string
	if err := row.Scan(&id, &raw); err != nil {
		return nil, 0, err
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, 0, err
	}
	return &snap, id, nil
}

type LedgerEntry struct {
	ID             int64    `json:"id"`
	Direction      string   `json:"direction"`
	Source         string   `json:"source"`
	Version        int      `json:"version"`
	Applied        int      `json:"applied"`
	ConflictCount  int      `json:"conflict_count"`
	Conflicts      []string `json:"conflicts,omitempty"`
	CreatedAt      string   `json:"created_at"`
	HasSnapshot    bool     `json:"has_snapshot"`
	SnapshotSHA256 string   `json:"snapshot_sha256,omitempty"`
}

func ListLedger(db *sql.DB, limit int) ([]LedgerEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(`SELECT id, direction, source, version, applied, conflict_count, conflicts_json, created_at, CASE WHEN snapshot_json IS NOT NULL AND snapshot_json <> '' THEN 1 ELSE 0 END, COALESCE(snapshot_sha256, '')
		FROM sync_ledger ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		var conflicts string
		var hasSnapshot int
		if err := rows.Scan(&e.ID, &e.Direction, &e.Source, &e.Version, &e.Applied, &e.ConflictCount, &conflicts, &e.CreatedAt, &hasSnapshot, &e.SnapshotSHA256); err != nil {
			return nil, err
		}
		if conflicts != "" && conflicts != "null" {
			_ = json.Unmarshal([]byte(conflicts), &e.Conflicts)
		}
		e.HasSnapshot = hasSnapshot == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func SnapshotSeen(db *sql.DB, snap Snapshot) (bool, string, error) {
	payload, err := json.Marshal(snap)
	if err != nil {
		return false, "", err
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	var dummy int
	if err := db.QueryRow(`SELECT 1 FROM sync_ledger WHERE snapshot_sha256 = ? LIMIT 1`, hash).Scan(&dummy); err != nil {
		if err == sql.ErrNoRows {
			return false, hash, nil
		}
		return false, "", err
	}
	return true, hash, nil
}
