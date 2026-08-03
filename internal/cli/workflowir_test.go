package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// workflowir_test.go is the egress test for the OPT-IN payload.
//
// It is written the way the run-link egress test is written, and for the same reason: the guarantee is
// "a field added to the IR is absent from the wire until somebody adds it on purpose", and the only way
// to keep that true is to fail the build when a transmitted payload grows a key nobody ratified.
//
// The IR fixture below is deliberately hostile. Every field this projection must refuse is populated
// with a string that would be unmistakable in a payload dump — so a leak is not a subtle diff, it is a
// test naming the exact secret it found on the wire.

func hostileIR() *discovery.IR {
	ir := &discovery.IR{IRVersion: "1.2.0"}
	ir.Workflow.ID = "openclaw/openclaw"
	ir.Workflow.Repo.CommitSHA = "1a51b0e58d674fdccd6704389f1116adfc901918"
	ir.Nodes = []discovery.IRNode{{
		NodeID:   "n_1",
		Kind:     "static_definition",
		CallSite: discovery.IRCallSite{File: "src/a.ts", Symbol: "streamAnthropic", LineStart: 10, LineEnd: 14, ASTPath: "SECRET_AST_PATH"},
		Model:    discovery.IRModel{Provider: "anthropic", ModelID: "claude-sonnet-4-5"},
		// The one that matters most.
		Prompt:          discovery.IRPrompt{Inline: "SECRET_PROMPT_TEXT you are a helpful assistant", Variables: []string{"SECRET_VARIABLE_NAME"}},
		ToolsSkills:     []string{"SECRET_TOOL_NAME", "SECRET_OTHER_TOOL"},
		ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages", Description: "SECRET_DESCRIPTION"},
		IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"SECRET_SCHEMA_KEY": "SECRET_SCHEMA_VALUE"},
			OutputSchema: map[string]any{"type": "object"},
		},
	}}
	ir.Edges = []discovery.IREdge{{FromNodeID: "n_1", ToNodeID: "n_2", Kind: "sequential"}}
	return ir
}

func TestTheOptInPayloadCarriesNoPromptTextOrAnyOtherContent(t *testing.T) {
	wire, err := json.Marshal(BuildWorkflowIR(hostileIR()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(wire)

	// Each of these was one field access away from the projection.
	for _, secret := range []string{
		"SECRET_PROMPT_TEXT",   // prompt.inline — the whole reason the boundary exists
		"SECRET_VARIABLE_NAME", // prompt variable names are the customer's identifiers
		"SECRET_TOOL_NAME",     // tool NAMES; a count crosses instead
		"SECRET_OTHER_TOOL",
		"SECRET_SCHEMA_KEY", // io_contract schemas carry literals lifted from source
		"SECRET_SCHEMA_VALUE",
		"SECRET_DESCRIPTION", // a generated sentence about the call site
		"SECRET_AST_PATH",
	} {
		if strings.Contains(body, secret) {
			t.Fatalf("the opt-in payload transmitted %q. This payload may carry SHAPE and never CONTENT; "+
				"remove the field from buildWorkflowIR, or ratify it in runlink.WorkflowIRAllowlist with a "+
				"justification a security reviewer will accept.\npayload: %s", secret, body)
		}
	}

	// And the count DID cross, so the refusal above is a projection rather than an omission.
	if !strings.Contains(body, `"tool_count":2`) {
		t.Fatalf("the tool COUNT should cross where the names do not: %s", body)
	}
}

func TestEveryKeyOnTheWireIsRatified(t *testing.T) {
	wire, _ := json.Marshal(BuildWorkflowIR(hostileIR()))
	var doc map[string]any
	if err := json.Unmarshal(wire, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// contract_version is the envelope, not a field about the customer.
	delete(doc, "contract_version")

	var walk func(prefix string, v any)
	var unratified []string
	walk = func(prefix string, v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				switch child.(type) {
				case map[string]any, []any:
					walk(key, child)
				default:
					if !runlink.WorkflowIRPermitted(key) {
						unratified = append(unratified, key)
					}
				}
			}
		case []any:
			for _, child := range t {
				walk(prefix, child)
			}
		}
	}
	walk("", doc)

	if len(unratified) > 0 {
		t.Fatalf("these keys crossed the boundary without being on runlink.WorkflowIRAllowlist: %v.\n"+
			"An unratified key is not a small thing here: the allowlist IS the security review, and a "+
			"field that reaches the wire without appearing in it was reviewed by nobody.", unratified)
	}
}

func TestAnIRForADifferentWorkflowOrRevisionIsRefused(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, ir *discovery.IR) string {
		b, _ := json.Marshal(ir)
		p := dir + "/" + name
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	good := hostileIR()
	path := write("ir.json", good)

	if _, err := LoadIRForLink(path, "openclaw/openclaw", good.Workflow.Repo.CommitSHA); err != nil {
		t.Fatalf("a matching IR must be accepted: %v", err)
	}

	// 🔴 The failure this guards against does not look like a failure. Both artifacts are valid; they
	// simply describe different things, and the console would render the mismatch as fact.
	if _, err := LoadIRForLink(path, "someone-else/repo", good.Workflow.Repo.CommitSHA); err == nil {
		t.Fatal("an IR describing a DIFFERENT workflow was accepted — it would attach one repository's " +
			"shape to another repository's measurements")
	}
	if _, err := LoadIRForLink(path, "openclaw/openclaw", "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("an IR discovered at a different revision was accepted — a graph drawn at one revision " +
			"and scored at another is a picture of neither")
	}
}
