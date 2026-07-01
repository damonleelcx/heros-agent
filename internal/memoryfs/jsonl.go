package memoryfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/agentlayout"
)

type SessionIndexEntry struct {
	SessionID     string `json:"session_id"`
	TenantID      string `json:"tenant_id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	TurnsPath     string `json:"turns_path"`
	MetaPath      string `json:"meta_path"`
	LinksPath     string `json:"links_path"`
	SchemaVersion int    `json:"schema_version"`
}

type EntityIndexEntry struct {
	EntityID      string `json:"entity_id"`
	TenantID      string `json:"tenant_id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	UpdatedAt     string `json:"updated_at"`
	SchemaVersion int    `json:"schema_version"`
}

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
		"ts":             time.Now().UTC().Format(time.RFC3339Nano),
		"schema_version": 1,
		"provenance": map[string]any{
			"source":  "heros-cli",
			"tenant":  tenantID,
			"session": sessionID,
		},
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
	if err := upsertSessionIndex(dataDir, tenantID, sessionID); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(dataDir, dir)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func AppendLink(dataDir, tenantID, sessionID, linkID, source, target, rel, provenance string, confidence float64) error {
	dir := agentlayout.SessionDir(dataDir, tenantID, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "links.json")
	rec := map[string]any{
		"id": linkID, "source": source, "target": target, "rel": rel,
		"confidence": confidence, "created_at": time.Now().UTC().Format(time.RFC3339Nano),
		"schema_version": 1, "provenance": provenance,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	if err != nil {
		return err
	}
	return upsertSessionIndex(dataDir, tenantID, sessionID)
}

func upsertSessionIndex(dataDir, tenantID, sessionID string) error {
	idxPath := agentlayout.SessionsIndexPath(dataDir, tenantID)
	if err := os.MkdirAll(filepath.Dir(idxPath), 0o755); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entry := SessionIndexEntry{
		SessionID:     sessionID,
		TenantID:      tenantID,
		CreatedAt:     now,
		UpdatedAt:     now,
		TurnsPath:     agentlayout.TurnsPath(dataDir, tenantID, sessionID),
		MetaPath:      agentlayout.SessionMetaPath(dataDir, tenantID, sessionID),
		LinksPath:     agentlayout.SessionLinksPath(dataDir, tenantID, sessionID),
		SchemaVersion: 1,
	}
	return upsertIndexEntry(idxPath, sessionID, entry)
}

func upsertIndexEntry(path, key string, entry any) error {
	rows := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		_ = json.Unmarshal(b, &rows)
	}
	enc, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	rows[key] = enc
	out, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func WriteEntityIndex(dataDir, tenantID string, entry EntityIndexEntry) error {
	idxPath := agentlayout.EntitiesIndexPath(dataDir, tenantID)
	if err := os.MkdirAll(filepath.Dir(idxPath), 0o755); err != nil {
		return err
	}
	return upsertIndexEntry(idxPath, entry.EntityID, entry)
}

func LoadSessionIndex(dataDir, tenantID string) (map[string]SessionIndexEntry, error) {
	idxPath := agentlayout.SessionsIndexPath(dataDir, tenantID)
	b, err := os.ReadFile(idxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]SessionIndexEntry{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]SessionIndexEntry{}, nil
	}
	var out map[string]SessionIndexEntry
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func RebuildSessionIndexes(dataDir string) error {
	memoryRoot := agentlayout.MemoryRoot(dataDir)
	entries, err := os.ReadDir(memoryRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tenantID := e.Name()
		indexes := map[string]SessionIndexEntry{}
		sessionsRoot := filepath.Join(memoryRoot, tenantID, "sessions")
		sessions, err := os.ReadDir(sessionsRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, s := range sessions {
			if !s.IsDir() {
				continue
			}
			sid := s.Name()
			now := time.Now().UTC().Format(time.RFC3339Nano)
			indexes[sid] = SessionIndexEntry{
				SessionID:     sid,
				TenantID:      tenantID,
				CreatedAt:     now,
				UpdatedAt:     now,
				TurnsPath:     agentlayout.TurnsPath(dataDir, tenantID, sid),
				MetaPath:      agentlayout.SessionMetaPath(dataDir, tenantID, sid),
				LinksPath:     agentlayout.SessionLinksPath(dataDir, tenantID, sid),
				SchemaVersion: 1,
			}
		}
		idxPath := agentlayout.SessionsIndexPath(dataDir, tenantID)
		if err := os.MkdirAll(filepath.Dir(idxPath), 0o755); err != nil {
			return err
		}
		out, err := json.MarshalIndent(indexes, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(idxPath, out, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func RebuildEntityIndexes(dataDir string, rows []EntityIndexEntry) error {
	buckets := map[string]map[string]EntityIndexEntry{}
	for _, row := range rows {
		tenantID := strings.TrimSpace(row.TenantID)
		if tenantID == "" {
			tenantID = "_global"
		}
		if buckets[tenantID] == nil {
			buckets[tenantID] = map[string]EntityIndexEntry{}
		}
		if row.SchemaVersion <= 0 {
			row.SchemaVersion = 1
		}
		if strings.TrimSpace(row.UpdatedAt) == "" {
			row.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		row.TenantID = tenantID
		buckets[tenantID][row.EntityID] = row
	}
	for tenantID, index := range buckets {
		idxPath := agentlayout.EntitiesIndexPath(dataDir, tenantID)
		if err := os.MkdirAll(filepath.Dir(idxPath), 0o755); err != nil {
			return err
		}
		out, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(idxPath, out, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type LinkRecord struct {
	ID            string  `json:"id"`
	Source        string  `json:"source"`
	Target        string  `json:"target"`
	Rel           string  `json:"rel"`
	Confidence    float64 `json:"confidence"`
	CreatedAt     string  `json:"created_at"`
	SchemaVersion int     `json:"schema_version"`
	Provenance    string  `json:"provenance"`
}

func ListLinks(dataDir, tenantID, sessionID string) ([]LinkRecord, error) {
	p := filepath.Join(agentlayout.SessionDir(dataDir, tenantID, sessionID), "links.json")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	out := make([]LinkRecord, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec LinkRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse links.json: %w", err)
		}
		if rec.ID == "" || rec.Source == "" || rec.Target == "" || rec.Rel == "" {
			return nil, errors.New("invalid link record")
		}
		out = append(out, rec)
	}
	return out, nil
}

// UpsertSessionAgentMemory overwrites session-scoped agent memory notes.
func UpsertSessionAgentMemory(dataDir, tenantID, sessionID, body string) error {
	dir := agentlayout.SessionDir(dataDir, tenantID, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := agentlayout.SessionAgentMemoryPath(dataDir, tenantID, sessionID)
	return os.WriteFile(p, []byte(strings.TrimSpace(body)), 0o644)
}

// ReadSessionAgentMemory returns session-scoped agent memory notes.
func ReadSessionAgentMemory(dataDir, tenantID, sessionID string) (string, error) {
	p := agentlayout.SessionAgentMemoryPath(dataDir, tenantID, sessionID)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
