package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadAuto_explicit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"listen_addr":"127.0.0.1:19999","data_dir":"`+filepath.ToSlash(dir)+`/d"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, src, err := LoadAuto(p)
	if err != nil {
		t.Fatal(err)
	}
	if src != p {
		t.Fatalf("source: got %q want %q", src, p)
	}
	if c.ListenAddr != "127.0.0.1:19999" {
		t.Fatalf("listen: %q", c.ListenAddr)
	}
}

func TestLoadAuto_defaults(t *testing.T) {
	dir := t.TempDir()
	// Isolate from developer machine %APPDATA%/heros/config.json (API key file).
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(dir, "Roaming"))
	} else {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	}
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	c, src, err := LoadAuto("")
	if err != nil {
		t.Fatal(err)
	}
	if src != "" {
		t.Fatalf("expected empty source for defaults, got %q", src)
	}
	if c.ListenAddr == "" {
		t.Fatal("empty listen")
	}
}
