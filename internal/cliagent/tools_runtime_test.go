package cliagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathUnderWorkspace(t *testing.T) {
	wd := t.TempDir()
	sub := filepath.Join(wd, "sub", "a.txt")
	if !pathUnderWorkspace(wd, sub) {
		t.Fatal("expected under workspace")
	}
	outside := filepath.Join(t.TempDir(), "x.txt")
	if pathUnderWorkspace(wd, outside) {
		t.Fatal("expected outside workspace")
	}
}

func TestExtAnsiStrip(t *testing.T) {
	in := "\x1b[31mhello\x1b[0m"
	args := map[string]any{"text": in}
	got := extAnsiStrip(args)
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestRunImportedCatalogTool_terminal(t *testing.T) {
	s := &Session{WorkDir: t.TempDir()}
	out, err := s.runImportedCatalogTool(context.Background(), "terminal-tool", map[string]any{
		"command": "echo ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestRunImportedCatalogTool_fileList(t *testing.T) {
	dir := t.TempDir()
	s := &Session{WorkDir: dir}
	out, err := s.runImportedCatalogTool(context.Background(), "file-operations", map[string]any{
		"action": "list",
		"path":   ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["entries"]; !ok {
		t.Fatalf("expected entries: %s", out)
	}
}
