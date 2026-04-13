//go:build !windows

package installpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const markerBegin = "# >>> heros go bin (added by heros -add-path)"
const markerEnd = "# <<< heros go bin"

// AddUserPATH appends a block to ~/.zshrc (if SHELL is zsh) or ~/.profile so login shells include Go's bin dir.
func AddUserPATH(dir string) error {
	dir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target := filepath.Join(home, ".profile")
	if sh := strings.ToLower(os.Getenv("SHELL")); strings.Contains(sh, "zsh") {
		target = filepath.Join(home, ".zshrc")
	}
	return appendPathBlock(target, dir)
}

func appendPathBlock(target, dir string) error {
	b, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(b)
	if strings.Contains(content, markerBegin) {
		start := strings.Index(content, markerBegin)
		end := strings.Index(content, markerEnd)
		if end > start {
			end += len(markerEnd)
			content = strings.TrimSpace(content[:start]) + "\n\n" + strings.TrimSpace(content[end:])
		}
	}
	block := fmt.Sprintf("%s\nexport PATH=\"%s:$PATH\"\n%s\n", markerBegin, dir, markerEnd)
	out := strings.TrimSpace(content)
	if out != "" {
		out += "\n\n"
	}
	out += block + "\n"
	perm := os.FileMode(0o644)
	if fi, err := os.Stat(target); err == nil {
		perm = fi.Mode().Perm()
	}
	if err := os.WriteFile(target, []byte(out), perm); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}
