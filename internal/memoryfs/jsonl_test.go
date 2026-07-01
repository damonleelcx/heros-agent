package memoryfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/heros-foreal/agentd/internal/db"
)

func TestRebuildSessionIndexes(t *testing.T) {
	dir := t.TempDir()
	if _, err := AppendTurn(dir, "tenant-a", "session-1", "turn-1", "note", "hello", 0.4); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	idx, err := LoadSessionIndex(dir, "tenant-a")
	if err != nil {
		t.Fatalf("load session index: %v", err)
	}
	if len(idx) != 1 {
		t.Fatalf("expected 1 session index entry, got %d", len(idx))
	}
	if _, ok := idx["session-1"]; !ok {
		t.Fatalf("missing session-1 entry")
	}
}

func TestWriteAndRebuildEntityIndex(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEntityIndex(dir, "tenant-a", EntityIndexEntry{
		EntityID:      "ent-1",
		TenantID:      "tenant-a",
		Name:          "Entity",
		Kind:          "note",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		SchemaVersion: 1,
	}); err != nil {
		t.Fatalf("write entity index: %v", err)
	}
	p := filepath.Join(dir, "memory", "tenant-a", "indexes", "entities.index.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var m map[string]EntityIndexEntry
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if _, ok := m["ent-1"]; !ok {
		t.Fatalf("missing ent-1 in index")
	}
}

func TestRebuildEntityIndexesFromGraph(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "test.db")
	database, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO graph_entities (id, name, kind, props_json) VALUES (?, ?, ?, ?)`, "ent-2", "Entity 2", "note", `{"x":1}`); err != nil {
		t.Fatalf("insert graph entity: %v", err)
	}
	rows, err := database.Query(`SELECT id, '' AS tenant_id, name, kind, COALESCE(props_json, ''), COALESCE(created_at, datetime('now')) FROM graph_entities`)
	if err != nil {
		t.Fatalf("query graph entities: %v", err)
	}
	var entities []EntityIndexEntry
	for rows.Next() {
		var e EntityIndexEntry
		var props string
		if err := rows.Scan(&e.EntityID, &e.TenantID, &e.Name, &e.Kind, &props, &e.UpdatedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		entities = append(entities, e)
	}
	rows.Close()
	if err := RebuildEntityIndexes(dir, entities); err != nil {
		t.Fatalf("rebuild entity indexes: %v", err)
	}
	p := filepath.Join(dir, "memory", "_global", "indexes", "entities.index.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read rebuilt index: %v", err)
	}
	var m map[string]EntityIndexEntry
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal rebuilt index: %v", err)
	}
	if _, ok := m["ent-2"]; !ok {
		t.Fatalf("missing ent-2 in rebuilt index")
	}
}
