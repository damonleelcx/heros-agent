package cliagent

import (
	"strings"
	"testing"
)

func TestFormatPendingProposalsForUser(t *testing.T) {
	list := []map[string]any{
		{"id": "abc", "title": "T1", "layer": "prompt_engineering"},
		{"id": "def", "title": "T2", "layer": "tooling"},
	}
	s := FormatPendingProposalsForUser(list)
	for _, sub := range []string{"1.", "T1", "abc", "2.", "T2", "def", "prompt_engineering"} {
		if !strings.Contains(s, sub) {
			t.Fatalf("missing %q in:\n%s", sub, s)
		}
	}
}

func TestFormatPendingProposalsForUser_empty(t *testing.T) {
	s := FormatPendingProposalsForUser(nil)
	if !strings.Contains(s, "no pending") {
		t.Fatalf("got %q", s)
	}
}
