package vaultindex

import (
	"strings"
	"testing"
)

func TestChunkMarkdownHeadings(t *testing.T) {
	src := `---
title: X
---
Preamble here.

## One
Alpha

## Two
Beta **bold**
`
	ch := ChunkMarkdown(src)
	if len(ch) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(ch))
	}
	if !strings.Contains(ch[0].Text, "Preamble") {
		t.Fatalf("expected preamble in first chunk: %q", ch[0].Text)
	}
	found := false
	for _, c := range ch {
		if strings.Contains(c.Text, "Alpha") && c.Heading == "One" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing heading One / Alpha: %#v", ch)
	}
}

func TestFileKeyTenantScoped(t *testing.T) {
	a := FileKey("", `C:\V`, "a.md")
	b := FileKey("t1", `C:\V`, "a.md")
	if a == b {
		t.Fatal("expected different file keys for different tenants")
	}
}
