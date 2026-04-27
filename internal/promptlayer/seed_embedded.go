package promptlayer

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Include underscore-prefixed folders like skills/_global and tools/_global.
//
//go:embed all:embedded_defaults
var embeddedDefaults embed.FS

const embeddedRoot = "embedded_defaults"

// seedEmbeddedDefaults copies packaged defaults into dataDir only when each target file is missing.
func seedEmbeddedDefaults(dataDir string) error {
	return fs.WalkDir(embeddedDefaults, embeddedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, ok := strings.CutPrefix(path, embeddedRoot+"/")
		if !ok {
			return nil
		}
		dst := filepath.Join(dataDir, filepath.FromSlash(rel))
		if _, err := os.Stat(dst); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		b, err := embeddedDefaults.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}
