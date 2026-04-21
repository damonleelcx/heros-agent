package cliagent

import "testing"

func TestMemoryGroundingRequired(t *testing.T) {
	t.Setenv("HEROS_NO_TOOL_FORCE", "")
	if !MemoryGroundingRequired("any memory you have now?") {
		t.Fatal("expected true")
	}
	if MemoryGroundingRequired("hello") {
		t.Fatal("expected false")
	}
	if MemoryGroundingRequired("/pending") {
		t.Fatal("slash")
	}
}

func TestWorkspaceGroundingRequired(t *testing.T) {
	t.Setenv("HEROS_NO_TOOL_FORCE", "")
	wd := `C:\repo\myapp`
	cases := []struct {
		line string
		want bool
	}{
		{"tell me about this project now", true},
		{"ok tell me about this project now", true},
		{"what does this codebase do", true},
		{"don't you know what you need to start?", true},
		{"thanks", false},
		{"/help", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := WorkspaceGroundingRequired(tc.line, wd); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.line, got, tc.want)
		}
	}
}

func TestFileActionGroundingRequired(t *testing.T) {
	t.Setenv("HEROS_NO_TOOL_FORCE", "")
	cases := []struct {
		line string
		want bool
	}{
		{"help me add a test file for canvas testing", true},
		{"create src/tests/canvas.test.ts", true},
		{"delete that file", true},
		{"show the file path", true},
		{"what is this project about", false},
		{"/pending", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := FileActionGroundingRequired(tc.line); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.line, got, tc.want)
		}
	}
}

func TestLongHorizonHarnessRequired(t *testing.T) {
	t.Setenv("HEROS_NO_TOOL_FORCE", "")
	cases := []struct {
		line string
		want bool
	}{
		{"I want to build a rocket emulator", true},
		{"design a production-ready agent harness", true},
		{"let's implement a multi-step orchestration engine", true},
		{"template for distributed system agent architecture", true},
		{"add more backend functionalities and connect to frontend", true},
		{"connect backend to frontend and add CRUD api", true},
		{"add some tests and run them to make sure they pass", false},
		{"show the file path", false},
		{"/harness build x", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := LongHorizonHarnessRequired(tc.line); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.line, got, tc.want)
		}
	}
}
