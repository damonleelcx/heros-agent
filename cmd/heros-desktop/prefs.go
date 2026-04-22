package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/config"
)

// mergeHerosDesktopForUI merges the agent config "heros_desktop" object with legacy ~/.heros-desktop.json.
// config.json (when present) takes precedence for each key over the home dotfile.
func mergeHerosDesktopForUI(hd config.HerosDesktopPrefs, f desktopFilePrefs) desktopFilePrefs {
	out := desktopFilePrefs{Appearance: strings.TrimSpace(hd.Appearance), Accent: strings.TrimSpace(hd.Accent)}
	if out.Appearance == "" {
		out.Appearance = strings.TrimSpace(f.Appearance)
	}
	if out.Accent == "" {
		out.Accent = strings.TrimSpace(f.Accent)
	}
	return out
}

// desktopFilePrefs is stored at ~/.heros-desktop.json (see desktopPrefsPath; mirrored from config for compatibility).
type desktopFilePrefs struct {
	// Appearance is "light" or "dark" (empty means no saved preference).
	Appearance string `json:"appearance,omitempty"`
	// Accent is optional #RRGGBB / RRGGBB used when HEROS_DESKTOP_ACCENT is unset.
	Accent string `json:"accent,omitempty"`
}

const desktopPrefsFileName = ".heros-desktop.json"

func desktopPrefsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, desktopPrefsFileName), nil
}

// loadDesktopFilePrefs reads ~/.heros-desktop.json. Missing file is not an error.
func loadDesktopFilePrefs() desktopFilePrefs {
	path, err := desktopPrefsPath()
	if err != nil {
		return desktopFilePrefs{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return desktopFilePrefs{}
		}
		log.Printf("heros-desktop: read %s: %v", path, err)
		return desktopFilePrefs{}
	}
	var p desktopFilePrefs
	if err := json.Unmarshal(b, &p); err != nil {
		log.Printf("heros-desktop: parse %s: %v", path, err)
		return desktopFilePrefs{}
	}
	return p
}

// saveDesktopFilePrefs writes prefs atomically to ~/.heros-desktop.json.
func saveDesktopFilePrefs(p desktopFilePrefs) error {
	path, err := desktopPrefsPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
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

// normalizeAppearanceKey maps common aliases to "light", "dark", or "" if unknown/empty.
func normalizeAppearanceKey(s string) string {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "light", "day", "paper":
		return "light"
	case "dark", "night":
		return "dark"
	default:
		return ""
	}
}

// appearanceForThemeOpts returns "light" or "dark" for JSON persistence.
func appearanceForThemeOpts(light bool) string {
	if light {
		return "light"
	}
	return "dark"
}
