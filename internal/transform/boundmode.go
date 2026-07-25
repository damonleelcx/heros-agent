package transform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// boundmode.go generates the `bound` apply mode's two extra artifacts (P10 §7, ADR-004/ADR-009): the
// resolved binding DOCUMENT (the actual values, as data) and the binding ARTIFACT (the generated
// accessor the rewritten call site reads). Both ship in the SAME Patch as the call-site rewrite, so a
// single revert restores everything (task 7.5), and both are deterministic and dependency-free
// (task 7.4).
//
// The data/structure line (task 7.6, ADR-004 Decision 2) is enforced here mechanically: `literal`,
// `env`, model and prompt go into the DOCUMENT (runtime-changeable data); `expr` and `input` bindings
// do NOT — they name things in the user's lexical scope and stay at the rewritten call site.

// Artifact paths are fixed and deterministic so regeneration overwrites byte-identically.
const (
	bindingDocPath   = "agentcfg/bindings.json"
	agentcfgFilePath = "agentcfg/agentcfg.go"
	bindingSchema    = "heros.agentcfg/v1"
)

// BindingDocument is the resolved binding document (ADR-009). Its keys mirror the frozen JSON contract.
type BindingDocument struct {
	Schema     string                     `json:"schema"`
	ConfigHash string                     `json:"config_hash"`
	Nodes      map[string]BindingDocNode  `json:"nodes"`
}

// BindingDocNode holds one bound node's runtime-changeable data.
type BindingDocNode struct {
	ModelID         string            `json:"model_id"`
	Params          map[string]any    `json:"params"`
	PromptTemplate  string            `json:"prompt_template"`
	LiteralBindings map[string]string `json:"literal_bindings"`
	EnvBindings     map[string]string `json:"env_bindings"`
}

// ErrBoundWithoutValues is the hard gate of task 7.3: a bound node that would introduce an indirection
// with no resolved values to put behind it is rejected before it is proposed or run — on the same
// footing as a transform that fails to build, never surfaced as a warning.
type ErrBoundWithoutValues struct{ NodeID string }

func (e *ErrBoundWithoutValues) Error() string {
	return fmt.Sprintf("bound transform rejected: node %q introduces a binding indirection but resolves no values to put in the binding document", e.NodeID)
}

// GenerateBoundArtifacts builds the binding document and accessor for every node the spec marks
// `bound`. It returns the two generated files (path -> content) or the gate error. When no node is
// bound it returns an empty map and no error — inline stays the default and adds nothing.
//
// It is a PURE function of the resolved spec: the same resolved configuration regenerates byte-
// identical output (task 7.4), because the document is emitted through the RFC-8785 canonicalizer and
// the accessor iterates sorted keys.
func GenerateBoundArtifacts(resolved *variantspec.Resolved) (map[string][]byte, error) {
	if resolved == nil || len(resolved.ApplyModes) == 0 {
		return map[string][]byte{}, nil
	}
	// Index resolved nodes by id for model_id/params (the config_hash projection).
	byID := map[string]variantspec.ResolvedNode{}
	for _, n := range resolved.Config.Nodes {
		byID[n.NodeID] = n
	}

	doc := BindingDocument{Schema: bindingSchema, ConfigHash: resolved.ConfigHash, Nodes: map[string]BindingDocNode{}}
	var boundIDs []string
	for nodeID, mode := range resolved.ApplyModes {
		if mode.Mode() != variantspec.ApplyBound {
			continue
		}
		boundIDs = append(boundIDs, nodeID)
		node := byID[nodeID]
		docNode, err := bindingDocNodeFor(nodeID, node, resolved.Overrides[nodeID])
		if err != nil {
			return nil, err
		}
		doc.Nodes[nodeID] = docNode
	}
	if len(boundIDs) == 0 {
		return map[string][]byte{}, nil
	}
	sort.Strings(boundIDs)

	docBytes, err := canonicalDocument(doc)
	if err != nil {
		return nil, err
	}
	accessor := renderAgentcfg(boundIDs)

	return map[string][]byte{
		bindingDocPath:   docBytes,
		agentcfgFilePath: accessor,
	}, nil
}

// bindingDocNodeFor projects one bound node's runtime-changeable data into the document, and enforces
// the data/structure line (task 7.6): only literal and env bindings enter the document.
func bindingDocNodeFor(nodeID string, node variantspec.ResolvedNode, ov variantspec.ResolvedOverride) (BindingDocNode, error) {
	out := BindingDocNode{
		ModelID:         node.ModelRef,
		Params:          node.ProviderParams,
		LiteralBindings: map[string]string{},
		EnvBindings:     map[string]string{},
	}
	if out.Params == nil {
		out.Params = map[string]any{}
	}
	if ov.Prompt != nil {
		out.PromptTemplate = reconstructBody(ov.Prompt.Template)
	}
	for slot, b := range node.Bindings {
		switch b.Kind {
		case string(variantspec.BindLiteral):
			out.LiteralBindings[slot] = b.Value
		case string(variantspec.BindEnv):
			out.EnvBindings[slot] = b.Value
			// expr / input are call-site structure — deliberately NOT written to the document.
		}
	}

	// The hard gate (task 7.3): an indirection with nothing behind it is rejected. A bound node must
	// resolve at least a model or a prompt to be worth an indirection.
	if out.ModelID == "" && out.PromptTemplate == "" &&
		len(out.LiteralBindings) == 0 && len(out.EnvBindings) == 0 {
		return BindingDocNode{}, &ErrBoundWithoutValues{NodeID: nodeID}
	}
	return out, nil
}

// canonicalDocument renders the document as RFC-8785 canonical JSON so regeneration is byte-identical
// (task 7.4) and a hand-edit changes the bytes visibly. A trailing newline makes it a well-formed text
// file in a diff.
func canonicalDocument(doc BindingDocument) ([]byte, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("transform: marshal binding document: %w", err)
	}
	canon, err := confighash.CanonicalizeBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("transform: canonicalize binding document: %w", err)
	}
	return append(canon, '\n'), nil
}

// reconstructBody rebuilds a prompt's body from its parsed template — literal runs verbatim, slots as
// {{name}}. ParseTemplate is lossless for any body that parses, so this round-trips exactly.
func reconstructBody(t *registry.Template) string {
	var b strings.Builder
	for _, seg := range t.Segments() {
		if seg.Slot == "" {
			b.WriteString(seg.Literal)
		} else {
			b.WriteString("{{")
			b.WriteString(seg.Slot)
			b.WriteString("}}")
		}
	}
	return b.String()
}

// renderAgentcfg emits the generated Go accessor. Dependency-free (standard library only) and
// deterministic: node ids are sorted, so the same set regenerates byte-identically. Each bound node
// gets its OWN accessor function, so referencing a node the document does not contain is a
// compile error (ADR-009), not a runtime map miss.
func renderAgentcfg(boundIDs []string) []byte {
	var b strings.Builder
	b.WriteString("// Code generated by heros agentcfg (P10 bound apply mode). DO NOT EDIT.\n")
	b.WriteString("// This package is generated, dependency-free, and byte-identically regenerable.\n")
	b.WriteString("// It reads the resolved binding document that ships beside it and supplies values at\n")
	b.WriteString("// bound call sites. Editing bindings.json changes configuration with no new codemod.\n")
	b.WriteString("package agentcfg\n\n")
	b.WriteString("import (\n\t_ \"embed\"\n\t\"encoding/json\"\n)\n\n")
	b.WriteString("//go:embed bindings.json\n")
	b.WriteString("var rawDocument []byte\n\n")
	b.WriteString("type document struct {\n")
	b.WriteString("\tConfigHash string                 `json:\"config_hash\"`\n")
	b.WriteString("\tNodes      map[string]NodeConfig   `json:\"nodes\"`\n")
	b.WriteString("}\n\n")
	b.WriteString("// NodeConfig is one bound node's runtime-changeable configuration.\n")
	b.WriteString("type NodeConfig struct {\n")
	b.WriteString("\tModelID         string            `json:\"model_id\"`\n")
	b.WriteString("\tParams          map[string]any    `json:\"params\"`\n")
	b.WriteString("\tPromptTemplate  string            `json:\"prompt_template\"`\n")
	b.WriteString("\tLiteralBindings map[string]string `json:\"literal_bindings\"`\n")
	b.WriteString("\tEnvBindings     map[string]string `json:\"env_bindings\"`\n")
	b.WriteString("}\n\n")
	b.WriteString("var doc document\n\n")
	b.WriteString("func init() {\n")
	b.WriteString("\tif err := json.Unmarshal(rawDocument, &doc); err != nil {\n")
	b.WriteString("\t\tpanic(\"agentcfg: embedded binding document does not parse: \" + err.Error())\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
	b.WriteString("// ConfigHash is the config_hash of the resolved document actually in force.\n")
	b.WriteString("func ConfigHash() string { return doc.ConfigHash }\n\n")
	for _, id := range boundIDs {
		fn := accessorName(id)
		fmt.Fprintf(&b, "// %s returns the bound configuration for node %q.\n", fn, id)
		fmt.Fprintf(&b, "func %s() NodeConfig { return doc.Nodes[%q] }\n\n", fn, id)
	}
	return []byte(b.String())
}

// accessorName turns a node id into an exported Go identifier, deterministically.
func accessorName(nodeID string) string {
	var b strings.Builder
	b.WriteString("Node_")
	for _, r := range nodeID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
