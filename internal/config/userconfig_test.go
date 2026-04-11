package config

import (
	"encoding/json"
	"os"
	"runtime"
	"testing"
)

func TestSaveUserOpenAIAPIKey(t *testing.T) {
	tmp := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", tmp)
	case "darwin", "ios":
		t.Skip("UserConfigDir is fixed under Library/Application Support; set XDG unsupported")
	default:
		t.Setenv("XDG_CONFIG_HOME", tmp)
	}

	path, err := SaveUserOpenAIAPIKey("sk-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["openai_api_key"] != "sk-test-secret" {
		t.Fatalf("key: %v", m["openai_api_key"])
	}

	// merge: keep existing keys
	m["listen_addr"] = "127.0.0.1:19998"
	b2, _ := json.Marshal(m)
	_ = os.WriteFile(path, b2, 0o600)

	path2, err := SaveUserOpenAIAPIKey("sk-second")
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path {
		t.Fatalf("path mismatch")
	}
	b3, _ := os.ReadFile(path)
	var m2 map[string]any
	_ = json.Unmarshal(b3, &m2)
	if m2["listen_addr"] != "127.0.0.1:19998" {
		t.Fatalf("lost listen_addr: %v", m2["listen_addr"])
	}
	if m2["openai_api_key"] != "sk-second" {
		t.Fatal("key not updated")
	}
}
