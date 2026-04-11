package promptlayer

import "testing"

func TestParseMutation(t *testing.T) {
	diff := `### SKILL:demo
line1
line2

### SYSTEM_PROMPT
You are helpful.
`
	m, err := ParseMutation(diff)
	if err != nil {
		t.Fatal(err)
	}
	if m.Skills["demo"] != "line1\nline2" {
		t.Fatalf("skill body: %q", m.Skills["demo"])
	}
	if !m.SystemPromptSet || m.SystemPrompt != "You are helpful." {
		t.Fatalf("system: %#v", m)
	}
}
