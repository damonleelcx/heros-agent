package vaultindex

import (
	"testing"
)

func TestParseWikilinksSkipsEmbeds(t *testing.T) {
	s := "See [[Alpha]] and ![[Beta]] and [[Gamma|g]]."
	got := ParseWikilinks(s)
	if len(got) != 2 || got[0] != "Alpha" || got[1] != "Gamma|g" {
		t.Fatalf("unexpected %q", got)
	}
}

func TestLinkIndexResolve(t *testing.T) {
	ix := buildLinkIndex([]fileEntry{
		{rel: "dir/A.md"},
		{rel: "B.md"},
	})
	rel, sec, _, ok := ix.resolve("dir/A.md", "B")
	if !ok || rel != "B.md" || sec != "" {
		t.Fatalf("bare title: ok=%v rel=%q sec=%q", ok, rel, sec)
	}
	_, _, _, ok2 := ix.resolve("dir/A.md", "C#h1")
	if ok2 {
		t.Fatalf("expected unresolved")
	}
	rel3, sec3, _, ok3 := ix.resolve("dir/A.md", "../B.md")
	if !ok3 || rel3 != "B.md" {
		t.Fatalf("parent path: ok=%v rel=%q sec=%q", ok3, rel3, sec3)
	}
	_ = sec3
}
