package syncsnapshot

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/db"
)

func TestImportConflictPolicies(t *testing.T) {
	database, err := db.Open(t.TempDir() + string(rune('\\')) + "sync.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if _, err := database.Exec(`INSERT INTO user_profiles (tenant_id, user_id, profile_json, updated_at) VALUES (?, ?, ?, ?)`, "tenant-a", "user-1", `{"name":"local"}`, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed local row: %v", err)
	}

	snap := Snapshot{
		Version: 1,
		UserProfiles: []map[string]any{
			{"tenant_id": "tenant-a", "user_id": "user-1", "profile_json": `{"name":"remote"}`, "updated_at": "2026-01-02T00:00:00Z"},
		},
	}

	if _, err := Import(database, snap, ImportPolicy{Conflict: "local_wins"}); err != nil {
		t.Fatalf("import local_wins: %v", err)
	}
	var got string
	if err := database.QueryRow(`SELECT profile_json FROM user_profiles WHERE tenant_id = ? AND user_id = ?`, "tenant-a", "user-1").Scan(&got); err != nil {
		t.Fatalf("read local_wins row: %v", err)
	}
	if got != `{"name":"local"}` {
		t.Fatalf("expected local row preserved, got %s", got)
	}

	if _, err := Import(database, snap, ImportPolicy{Conflict: "remote_wins"}); err != nil {
		t.Fatalf("import remote_wins: %v", err)
	}
	if err := database.QueryRow(`SELECT profile_json FROM user_profiles WHERE tenant_id = ? AND user_id = ?`, "tenant-a", "user-1").Scan(&got); err != nil {
		t.Fatalf("read remote_wins row: %v", err)
	}
	if got != `{"name":"remote"}` {
		t.Fatalf("expected remote row applied, got %s", got)
	}
}
