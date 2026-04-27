package cliagent

import (
	"context"
	"strings"
	"testing"
)

func TestShouldInjectMemoryOnDemand(t *testing.T) {
	s := &Session{}
	if !s.shouldInjectMemory("what do you remember about this?") {
		t.Fatal("expected memory injection for memory intent")
	}
	if !s.shouldInjectMemory("continue from earlier context") {
		t.Fatal("expected memory injection for recall cue")
	}
	if s.shouldInjectMemory("hello there") {
		t.Fatal("did not expect memory injection for greeting")
	}
}

func TestModelMessagesForStepIsolation(t *testing.T) {
	s := &Session{ContextIsolation: true, ContextIsolationWindow: 3}
	s.Messages = []map[string]any{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "u1"},
		{"role": "assistant", "content": "a1"},
		{"role": "user", "content": "u2"},
		{"role": "assistant", "content": "a2"},
	}
	got := s.modelMessagesForStep()
	if len(got) != 4 {
		t.Fatalf("len=%d want 4", len(got))
	}
	if ArgString(got[0], "role") != "system" || ArgString(got[0], "content") != "sys" {
		t.Fatalf("first message should preserve system context")
	}
	if ArgString(got[1], "content") != "a1" || ArgString(got[3], "content") != "a2" {
		t.Fatalf("unexpected window selection: %#v", got)
	}
}

func TestCompressConversationContext(t *testing.T) {
	s := &Session{
		ContextCompression:            true,
		ContextCompressionMaxMessages: 6,
		ContextCompressionKeepRecent:  3,
	}
	s.Messages = []map[string]any{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "u1"},
		{"role": "assistant", "content": "a1"},
		{"role": "user", "content": "u2"},
		{"role": "assistant", "content": "a2"},
		{"role": "user", "content": "u3"},
		{"role": "assistant", "content": "a3"},
	}
	s.compressConversationContext()
	if len(s.Messages) != 5 {
		t.Fatalf("len=%d want 5", len(s.Messages))
	}
	if ArgString(s.Messages[1], "role") != "system" {
		t.Fatalf("expected synthetic system summary at index 1")
	}
	if !strings.HasPrefix(strings.TrimSpace(ArgString(s.Messages[1], "content")), "[compressed-context]") {
		t.Fatalf("expected compressed context note")
	}
	if ArgString(s.Messages[2], "content") != "a2" || ArgString(s.Messages[4], "content") != "a3" {
		t.Fatalf("tail messages should be preserved")
	}
}

func TestBuildAutoMemoryInjectionOnDemandDisabledForGenericTurn(t *testing.T) {
	s := &Session{
		AutoInjectMemory:        true,
		OnDemandMemoryInjection: true,
		AutoInjectTopK:          2,
		Agentd:                  &AgentdClient{},
	}
	out := s.buildAutoMemoryInjection(context.Background(), "hello")
	if out != "" {
		t.Fatalf("expected no injection, got %q", out)
	}
}
