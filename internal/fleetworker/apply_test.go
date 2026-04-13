package fleetworker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPromptLayerDiff_skillOnly(t *testing.T) {
	dir := t.TempDir()
	diff := "### SKILL:demo-skill\n\nHello **fleet**.\n"
	paths, err := ApplyPromptLayerDiff(dir, "", diff, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "skills/_global/demo-skill/SKILL.md") {
		t.Fatalf("paths: %v", paths)
	}
	b, err := os.ReadFile(filepath.Join(dir, "skills", "_global", "demo-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Hello **fleet**") {
		t.Fatalf("content: %s", b)
	}
}
