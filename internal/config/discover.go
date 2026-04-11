package config

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadAuto loads configuration with sensible discovery when explicitPath is empty.
// Search order:
//  1. explicitPath (e.g. -config flag)
//  2. HEROS_CONFIG or HEROS_CONFIG_PATH
//  3. config.json walking upward from the current working directory
//  4. os.UserConfigDir()/heros/config.json (e.g. %APPDATA%\heros\config.json on Windows)
//  5. ~/.heros/config.json
//  6. ~/.heros-agent/config.json
//  7. Built-in defaults (same as Load(""))
//
// The returned source is the file path used, or empty if only defaults applied.
func LoadAuto(explicitPath string) (cfg Config, source string, err error) {
	if p := strings.TrimSpace(explicitPath); p != "" {
		cfg, err = Load(p)
		return cfg, p, err
	}
	for _, env := range []string{"HEROS_CONFIG", "HEROS_CONFIG_PATH"} {
		if p := strings.TrimSpace(os.Getenv(env)); p != "" {
			cfg, err = Load(p)
			return cfg, p, err
		}
	}
	cwd, errWd := os.Getwd()
	if errWd != nil {
		cwd = "."
	}
	for dir := cwd; dir != ""; {
		candidate := filepath.Join(dir, "config.json")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			cfg, err = Load(candidate)
			return cfg, candidate, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if cd, err := os.UserConfigDir(); err == nil {
		candidate := filepath.Join(cd, "heros", "config.json")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			cfg, err = Load(candidate)
			return cfg, candidate, err
		}
	}
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		filepath.Join(home, ".heros", "config.json"),
		filepath.Join(home, ".heros-agent", "config.json"),
	} {
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			cfg, err = Load(candidate)
			return cfg, candidate, err
		}
	}
	cfg, err = Load("")
	return cfg, "", err
}
