package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadToolRegistrySyncEnvOverrides(t *testing.T) {
	t.Setenv("HEROS_TOOL_REGISTRY_SYNC_DISK_TO_DB", "approved_only")
	t.Setenv("HEROS_TOOL_REGISTRY_SYNC_CONFLICT", "db")
	t.Setenv("HEROS_TOOL_REGISTRY_SYNC_PUSH_TO_DISK", "approved_only")

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.ToolRegistrySync.DiskToDB != "approved_only" {
		t.Fatalf("DiskToDB: got %q", c.ToolRegistrySync.DiskToDB)
	}
	if c.ToolRegistrySync.Conflict != "db" {
		t.Fatalf("Conflict: got %q", c.ToolRegistrySync.Conflict)
	}
	if c.ToolRegistrySync.PushToDisk != "approved_only" {
		t.Fatalf("PushToDisk: got %q", c.ToolRegistrySync.PushToDisk)
	}
}

func TestLoadJSONThenEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(p, []byte(`{"tool_registry_sync":{"disk_to_db":"all","conflict":"yaml","push_to_disk":"all"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEROS_TOOL_REGISTRY_SYNC_DISK_TO_DB", "approved_only")

	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ToolRegistrySync.DiskToDB != "approved_only" {
		t.Fatalf("env should override JSON, got %q", c.ToolRegistrySync.DiskToDB)
	}
	if c.ToolRegistrySync.Conflict != "yaml" {
		t.Fatalf("Conflict: got %q", c.ToolRegistrySync.Conflict)
	}
}
