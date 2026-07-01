package memorylayer

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PruneEpisodic removes old low-importance episodic rows by age and returns count.
func PruneEpisodic(db *sql.DB, sessionID string, olderThanDays int, minImportance float64) (int64, error) {
	if olderThanDays <= 0 {
		olderThanDays = 30
	}
	cutoff := time.Now().UTC().Add(-time.Duration(olderThanDays) * 24 * time.Hour).Format(time.RFC3339)
	res, err := db.Exec(`DELETE FROM episodic_memory WHERE session_id = ? AND importance <= ? AND created_at < ?`, sessionID, minImportance, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// QuarantineBrokenFile moves a corrupt memory file aside for inspection.
func QuarantineBrokenFile(dataDir, absPath string) (string, error) {
	absPath = filepath.Clean(absPath)
	if !strings.HasPrefix(absPath, filepath.Clean(dataDir)) {
		return "", os.ErrPermission
	}
	qdir := filepath.Join(dataDir, "memory", "_quarantine")
	if err := os.MkdirAll(qdir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(qdir, filepath.Base(absPath)+"."+time.Now().UTC().Format("20060102-150405"))
	if err := os.Rename(absPath, dst); err != nil {
		return "", err
	}
	return dst, nil
}
