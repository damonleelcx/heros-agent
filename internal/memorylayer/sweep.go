package memorylayer

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/memoryfs"
)

// SweepMemorySpace prunes stale episodic rows and quarantines malformed session artifacts.
func SweepMemorySpace(db *sql.DB, dataDir string) error {
	roots := []string{filepath.Join(dataDir, "memory")}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			base := filepath.Base(path)
			if base != "turns.jsonl" && base != "meta.json" && base != "links.json" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				q, qerr := QuarantineBrokenFile(dataDir, path)
				if qerr != nil {
					return qerr
				}
				log.Printf("memory sweep quarantined unreadable file %s -> %s", path, q)
				return nil
			}
			if len(strings.TrimSpace(string(b))) == 0 {
				_ = os.Remove(path)
				return nil
			}
			if base == "turns.jsonl" {
				for _, line := range strings.Split(string(b), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var tmp map[string]any
					if err := json.Unmarshal([]byte(line), &tmp); err != nil {
						q, qerr := QuarantineBrokenFile(dataDir, path)
						if qerr != nil {
							return qerr
						}
						log.Printf("memory sweep quarantined malformed jsonl %s -> %s", path, q)
						return nil
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := memoryfs.RebuildSessionIndexes(dataDir); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT id, '' AS tenant_id, name, kind, COALESCE(props_json, ''), COALESCE(created_at, datetime('now')) FROM graph_entities ORDER BY created_at ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var entities []memoryfs.EntityIndexEntry
	for rows.Next() {
		var e memoryfs.EntityIndexEntry
		var props, createdAt string
		if err := rows.Scan(&e.EntityID, &e.TenantID, &e.Name, &e.Kind, &props, &createdAt); err != nil {
			return err
		}
		e.UpdatedAt = createdAt
		e.SchemaVersion = 1
		entities = append(entities, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := memoryfs.RebuildEntityIndexes(dataDir, entities); err != nil {
		return err
	}
	return nil
}
