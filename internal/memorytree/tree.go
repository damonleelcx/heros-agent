package memorytree

import (
	"os"
	"path/filepath"

	"github.com/heros-foreal/agentd/internal/agentlayout"
)

// ListSessions returns session folder names under memory/<tenant>/sessions/.
func ListSessions(dataDir, tenantID string) ([]string, error) {
	t := agentlayout.SanitizeSlug(tenantID)
	if t == "" {
		t = "_global"
	}
	root := filepath.Join(agentlayout.MemoryRoot(dataDir), t, "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
