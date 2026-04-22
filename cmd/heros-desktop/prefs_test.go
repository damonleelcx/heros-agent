package main

import "testing"

func TestNormalizeAppearanceKey(t *testing.T) {
	if g, w := normalizeAppearanceKey("LIGHT"), "light"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g, w := normalizeAppearanceKey("paper"), "light"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g, w := normalizeAppearanceKey("night"), "dark"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g, w := normalizeAppearanceKey(""), ""; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g, w := normalizeAppearanceKey("unknown"), ""; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
}

func TestAppearanceForThemeOpts(t *testing.T) {
	if g, w := appearanceForThemeOpts(true), "light"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g, w := appearanceForThemeOpts(false), "dark"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
}
