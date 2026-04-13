package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const userHerusDir = "heros"
const userHerusFile = "config.json"

// userHerusPath returns %APPDATA%/heros/config.json (Windows) or XDG config path.
func userHerusPath() (string, error) {
	cd, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cd, userHerusDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, userHerusFile), nil
}

func readUserHerusMap(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func writeUserHerusMap(path string, m map[string]any) error {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// GetCLIWorkdir returns saved workspace path from user config, or empty string.
func GetCLIWorkdir() (string, error) {
	path, err := userHerusPath()
	if err != nil {
		return "", err
	}
	m, err := readUserHerusMap(path)
	if err != nil {
		return "", err
	}
	v, _ := m["cli_workdir"].(string)
	return strings.TrimSpace(v), nil
}

// SaveCLIWorkdir persists the default heros_shell workspace (absolute path recommended).
func SaveCLIWorkdir(absWorkdir string) error {
	absWorkdir = strings.TrimSpace(absWorkdir)
	if absWorkdir == "" {
		return nil
	}
	path, err := userHerusPath()
	if err != nil {
		return err
	}
	m, err := readUserHerusMap(path)
	if err != nil {
		return err
	}
	m["cli_workdir"] = absWorkdir
	return writeUserHerusMap(path, m)
}
