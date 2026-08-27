package herosagent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// p36_compat_test.go is P36 §1 — the compatibility fence for the whole phase, and it exists BEFORE any
// P36 code does.
//
// # What it is protecting
//
// Every pinned inference is keyed by `(workflow_id, source_revision, agent_config_hash)`. A definition
// whose SHAPE changed hashes differently, so every pin filed under the old hash becomes unreachable
// from any definition anybody can author. Nothing errors. The console keeps rendering results computed
// by a configuration that no longer exists, and the bill arrives weeks later when somebody asks a
// question that re-runs every assessment.
//
// So D4's requirement is bytes: a definition with exactly one node, no ordering, no edges, no graph
// declaration and no loop ref must marshal to the bytes the pre-P36 shape produced, and hash to the
// same `config_hash`.
//
// # RECORDING (done once, in the pre-P36 tree — do NOT re-record from a tree that has P36 in it)
//
//	GOWORK=off P36_RECORD_PRE=1 go test ./internal/herosagent/ -run TestPreP36
//
// Re-recording destroys the only evidence this file carries: a fixture reconstructed after the change
// asserts that the new code is a function of its input, which was never in doubt.
const p36PreFixture = "testdata/p36-pre-confighash.json"

// p36Recorded is one definition as the pre-P36 tree serialised and hashed it.
//
// Three artefacts and not one, because they fail differently and say different things:
//
//	Wire       what `json.Marshal(Definition)` produced — what LANDS IN `spec_json`. A store row
//	           written by the old binary must still decode, so this is the readback fence.
//	Canonical  what `canonical()` produced — what is HASHED. Beside the hash on purpose: a hash
//	           mismatch says a configuration moved, the bytes say which key did it.
//	ConfigHash the identity every pin is filed under.
type p36Recorded struct {
	Name       string `json:"name"`
	Why        string `json:"why"`
	Wire       string `json:"wire_json"`
	Canonical  string `json:"canonical_json"`
	ConfigHash string `json:"config_hash"`
}

// p36RecordingDefinitions are the definitions the fixture covers. They span exactly what P36 threatens:
// the bare single-node definition (the default, D2), one carrying every optional axis, and one carrying
// the critic pair — the existing precedent for a second model, which PRD §14 Q1 asks about.
func p36RecordingDefinitions(t *testing.T) []p36Recorded {
	t.Helper()
	return []p36Recorded{
		p36Record(t, "bare-single-node",
			"§1.1 — the default definition: one node, no ordering, no edges, no graph declaration, no "+
				"loop ref. Its bytes are the ones P36 must not move. This is the shape D2 keeps as the "+
				"default, so it is also the shape the majority of stored pins were produced under.",
			Definition{
				PromptRef:     "prompt-v1",
				ModelRef:      "claude-opus-5",
				CredentialRef: "anthropic",
				ContextRef:    "ctx-v1",
				HarnessRef:    "harness-single-shot-v1",
			}),

		p36Record(t, "single-node-every-axis",
			"§1.1 — every authorable axis bound, still one node. Catches a compatibility encoding that "+
				"happens to reproduce the empty case by omitting everything: skill order is "+
				"identity-bearing and tool order is not, and both of those normalisations have to survive "+
				"the shape change unchanged.",
			Definition{
				PromptRef:     "prompt-v2",
				ModelRef:      "claude-opus-5",
				CredentialRef: "anthropic",
				SkillRefs:     []string{"skill-b-v1", "skill-a-v1"},
				ToolNames:     []string{"_global/read", "acme/search"},
				ContextRef:    "ctx-v2",
				MemoryRef:     "mem-scratchpad-v1",
				HarnessRef:    "harness-reflexion-v1",
				SetVersions:   map[string]string{"memory": "v3", "harness": "v2"},
			}),

		p36Record(t, "single-node-with-critic",
			"§1.1 — the critic pair, which is the EXISTING precedent for a second model on one "+
				"definition (PRD §14 Q1). Whatever P36 decides about per-node credentials, these two "+
				"fields already exist and already hash, so a definition carrying them must keep its "+
				"identity across the change.",
			Definition{
				PromptRef:           "prompt-v1",
				ModelRef:            "claude-opus-5",
				CredentialRef:       "anthropic",
				ContextRef:          "ctx-v1",
				HarnessRef:          "harness-critic-loop-v1",
				CriticModelRef:      "claude-sonnet-5",
				CriticCredentialRef: "openai",
			}),
	}
}

func p36Record(t *testing.T, name, why string, d Definition) p36Recorded {
	t.Helper()
	wire, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("%s: marshalling the definition: %v", name, err)
	}
	canon, err := json.Marshal(d.canonical())
	if err != nil {
		t.Fatalf("%s: marshalling the canonical projection: %v", name, err)
	}
	hash, err := d.ConfigHash()
	if err != nil {
		t.Fatalf("%s: hashing: %v", name, err)
	}
	return p36Recorded{Name: name, Why: why, Wire: string(wire), Canonical: string(canon), ConfigHash: hash}
}

// TestPreP36ConfigHashesAreReproducedExactly is the fence. In RECORD mode it writes the fixture; in
// normal mode it asserts this tree still reproduces every recorded byte.
func TestPreP36ConfigHashesAreReproducedExactly(t *testing.T) {
	got := p36RecordingDefinitions(t)

	if os.Getenv("P36_RECORD_PRE") == "1" {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p36PreFixture, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("RECORDED %d definition(s) into %s. This mode must run ONLY in a pre-P36 tree; "+
			"re-recording from a tree that contains P36 destroys the evidence.", len(got), p36PreFixture)
	}

	raw, err := os.ReadFile(p36PreFixture)
	if err != nil {
		t.Fatalf("the pre-P36 fixture is missing (%v). It is recorded once, in the pre-P36 tree, with "+
			"P36_RECORD_PRE=1 — not reconstructed from this one.", err)
	}
	var want []p36Recorded
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("the fixture is empty — a fence over nothing reports clean forever")
	}
	byName := map[string]p36Recorded{}
	for _, w := range want {
		byName[w.Name] = w
	}
	for _, g := range got {
		w, ok := byName[g.Name]
		if !ok {
			t.Errorf("%s: this tree produces a definition the fixture does not carry. Add it to the "+
				"fixture only by recording from a pre-P36 tree.", g.Name)
			continue
		}
		if g.Wire != w.Wire {
			t.Errorf("%s: the WIRE bytes moved.\n  was: %s\n  now: %s\n\n  %s\n\n"+
				"  A stored `spec_json` written by the previous binary no longer decodes to the same "+
				"definition, so a published version reads back as something else.", g.Name, w.Wire, g.Wire, w.Why)
		}
		if g.Canonical != w.Canonical {
			t.Errorf("%s: the CANONICAL (hashed) bytes moved.\n  was: %s\n  now: %s\n\n  %s",
				g.Name, w.Canonical, g.Canonical, w.Why)
		}
		if g.ConfigHash != w.ConfigHash {
			t.Errorf("%s: config_hash moved from %s to %s. Every inference pinned under the old hash is "+
				"now unreachable from any definition anybody can author — and nothing errors when that "+
				"happens.\n\n  %s", g.Name, w.ConfigHash, g.ConfigHash, w.Why)
		}
	}
	if len(got) != len(want) {
		var missing []string
		have := map[string]bool{}
		for _, g := range got {
			have[g.Name] = true
		}
		for _, w := range want {
			if !have[w.Name] {
				missing = append(missing, w.Name)
			}
		}
		if len(missing) > 0 {
			t.Errorf("the fixture carries %s, which this tree no longer produces. A shape that can no "+
				"longer be authored is exactly the case D4 requires to stay READABLE.",
				strings.Join(missing, ", "))
		}
	}
}
