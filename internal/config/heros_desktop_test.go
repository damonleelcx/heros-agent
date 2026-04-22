package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveHerosDesktopToConfigFile_merge(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	_ = os.WriteFile(p, []byte(`{"listen_addr":"127.0.0.1:1","data_dir":"/x","heros_desktop":{"accent":"#aabbcc"}}
`), 0o600)
	if err := SaveHerosDesktopToConfigFile(p, HerosDesktopPrefs{Appearance: "light"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:1" {
		t.Fatalf("listen_addr got %q", c.ListenAddr)
	}
	if c.HerosDesktop.Appearance != "light" {
		t.Fatalf("appearance got %q", c.HerosDesktop.Appearance)
	}
	if c.HerosDesktop.Accent != "#aabbcc" {
		t.Fatalf("accent should be preserved, got %q", c.HerosDesktop.Accent)
	}
}

func TestSaveHerosDesktopToConfigFile_setAccent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := SaveHerosDesktopToConfigFile(p, HerosDesktopPrefs{Appearance: "dark", Accent: "#112233"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.HerosDesktop.Appearance != "dark" || c.HerosDesktop.Accent != "#112233" {
		t.Fatalf("got %#v", c.HerosDesktop)
	}
}

func TestResolveHerosConfigWritePath(t *testing.T) {
	if p, err := ResolveHerosConfigWritePath("/a/b.json"); err != nil || p != "/a/b.json" {
		t.Fatalf("got %q %v", p, err)
	}
}
