package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveUserOpenAIAPIKey merges openai_api_key into the per-user config file:
//
//	os.UserConfigDir()/heros/config.json
//
// On Windows that is typically %APPDATA%\heros\config.json.
// Existing keys in the file are preserved (JSON object merge at top level).
func SaveUserOpenAIAPIKey(apiKey string) (savedPath string, err error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("empty openai api key")
	}
	cd, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cd, "heros")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	savedPath = filepath.Join(dir, "config.json")

	var m map[string]any
	b, err := os.ReadFile(savedPath)
	if err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return savedPath, fmt.Errorf("parse %s: %w", savedPath, err)
		}
	} else if !os.IsNotExist(err) {
		return savedPath, err
	}
	if m == nil {
		m = map[string]any{}
	}
	m["openai_api_key"] = apiKey

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return savedPath, err
	}
	if err := os.WriteFile(savedPath, out, 0o600); err != nil {
		return savedPath, err
	}
	return savedPath, nil
}
