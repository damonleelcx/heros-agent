package promptlayer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedEmbeddedDefaultsIncludesUnderscoreGlobal(t *testing.T) {
	tmp := t.TempDir()
	if err := seedEmbeddedDefaults(tmp); err != nil {
		t.Fatalf("seedEmbeddedDefaults: %v", err)
	}

	skillPath := filepath.Join(tmp, "skills", "_global", "long-running-work", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected embedded skill to be seeded at %s: %v", skillPath, err)
	}

	toolPath := filepath.Join(tmp, "tools", "_global", "registry", "tool.yaml")
	if _, err := os.Stat(toolPath); err != nil {
		t.Fatalf("expected embedded tool to be seeded at %s: %v", toolPath, err)
	}
}
