package memoryfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/heros-foreal/agentd/internal/agentlayout"
)

// AppendTurn appends one JSONL record and ensures meta.json exists. Returns session-relative path from dataDir (memory/...).
func AppendTurn(dataDir, tenantID, sessionID, turnID, role, content string, importance float64) (sessionRel string, err error) {
	dir := agentlayout.SessionDir(dataDir, tenantID, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	metaPath := filepath.Join(dir, "meta.json")
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		m := map[string]any{
			"tenant_id":  tenantID,
			"session_id": sessionID,
			"created_at": time.Now().UTC().Format(time.RFC3339),
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(metaPath, b, 0o644); err != nil {
			return "", err
		}
	}
	rec := map[string]any{
		"id": turnID, "role": role, "content": content, "importance": importance,
		"ts": time.Now().UTC().Format(time.RFC3339Nano),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	jl := agentlayout.TurnsPath(dataDir, tenantID, sessionID)
	f, err := os.OpenFile(jl, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(dataDir, dir)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
