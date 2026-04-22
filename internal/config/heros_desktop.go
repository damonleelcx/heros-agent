package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// HerosDesktopPrefs is UI-only data for the heros-desktop (Fyne) app.
//
// Read/write boundary:
//   - agentd and the HTTP API do not use these fields; they are ignored for server behavior.
//   - heros-desktop is the primary writer; humans may hand-edit the JSON.
//   - heros and heros-cli ignore this object unless a future version reads it.
//   - ~/.heros-desktop.json (if present) is a secondary/mirror; prefer heros_desktop in config when both exist.
//
// JSON key at top level: "heros_desktop" (see Config.HerosDesktop).
// Field names: "appearance" and "accent" (same as ~/.heros-desktop.json for mirroring).
type HerosDesktopPrefs struct {
	// Appearance is "light" or "dark" (empty = do not set this key in merge when saving from some callers; UI always sets).
	Appearance string `json:"appearance,omitempty"`
	// Accent is a hex color e.g. #6b4eff, used when HEROS_DESKTOP_ACCENT is not set.
	Accent string `json:"accent,omitempty"`
}

var errEmptyConfigPath = errors.New("heros config path is empty")

// SaveHerosDesktopToConfigFile merges the heros_desktop key into a JSON file without dropping other top-level keys.
// If the file is missing, it is created. Parent directories are created for ~/.heros-agent/config.json, etc.
//
// Merge rules for heros_desktop:
//   - If p.Appearance is non-empty, it is written.
//   - If p.Accent is non-empty, it is written; if p.Accent is empty, the existing accent in the file is kept
//     (so theme-only toggles do not clear a stored accent, and session env accent is not written when empty here).
func SaveHerosDesktopToConfigFile(path string, p HerosDesktopPrefs) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errEmptyConfigPath
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	m := make(map[string]any)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		if m == nil {
			m = make(map[string]any)
		}
	}
	hd, _ := m["heros_desktop"].(map[string]any)
	if hd == nil {
		hd = make(map[string]any)
	}
	if a := strings.TrimSpace(p.Appearance); a != "" {
		hd["appearance"] = a
	}
	if a := strings.TrimSpace(p.Accent); a != "" {
		hd["accent"] = a
	}
	m["heros_desktop"] = hd
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			_ = os.Remove(tmp)
			return err2
		}
	}
	return nil
}

// ResolveHerosConfigWritePath picks where to persist heros_desktop:
//   - the config file that LoadAuto used (discovered), if non-empty;
//   - otherwise $HOME/.heros-agent/config.json (same default family as our data_dir docs).
func ResolveHerosConfigWritePath(discoveredConfigPath string) (string, error) {
	if p := strings.TrimSpace(discoveredConfigPath); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".heros-agent", "config.json"), nil
}
