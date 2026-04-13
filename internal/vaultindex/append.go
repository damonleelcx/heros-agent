package vaultindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/heros-foreal/agentd/internal/config"
)

// AppendNoteToVault appends a Markdown block when a matching vault has vault_append_enabled.
// Intended for episodic role=note (e.g. heros_memory_save). Returns nil when no vault matches.
func AppendNoteToVault(cfg config.Config, tenantID, sessionID, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil
	}
	tid := strings.TrimSpace(tenantID)
	for _, v := range cfg.KnowledgeVaults {
		if !v.VaultAppendEnabled {
			continue
		}
		if strings.TrimSpace(v.TenantID) != tid {
			continue
		}
		return appendNoteToOneVault(v, tid, sessionID, note)
	}
	return nil
}

func appendNoteToOneVault(v config.KnowledgeVault, tenantID, sessionID, note string) error {
	root := strings.TrimSpace(v.Path)
	if root == "" {
		return fmt.Errorf("vault_append_enabled: empty path")
	}
	absVault, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if st, err := os.Stat(absVault); err != nil || !st.IsDir() {
		return fmt.Errorf("vault_append_enabled: not a directory %q", absVault)
	}
	sub := strings.TrimSpace(v.AgentNotesSubdir)
	if sub == "" {
		sub = "Agent/heros-notes"
	}
	mode := strings.ToLower(strings.TrimSpace(v.AgentNotesMode))
	if mode == "" {
		mode = "daily"
	}
	base := filepath.Join(absVault, filepath.FromSlash(sub))
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	var name string
	switch mode {
	case "session":
		name = sanitizeFilePart(sessionID) + ".md"
	default:
		name = time.Now().UTC().Format("2006-01-02") + ".md"
	}
	dest := filepath.Join(base, name)
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absVault, absDest)
	if err != nil || strings.HasPrefix(filepath.Clean(rel), "..") {
		return fmt.Errorf("agent notes path escapes vault root")
	}
	block := fmt.Sprintf("\n\n## heros %s (session=%s tenant=%q)\n\n%s\n",
		time.Now().UTC().Format(time.RFC3339), sessionID, tenantID, note)
	f, err := os.OpenFile(absDest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return err
	}
	return nil
}

func sanitizeFilePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "session"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
