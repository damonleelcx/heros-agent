package herosagent

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// 🔴 THE NO-KEY FENCE (tasks 3.5, 10.17).
//
// D5 is that the credential is BOUND, never entered — and the MECHANISM is the absence of a field, not
// a rule about what to put in one. A rule is obeyed until somebody adds `APIKey string` in a hurry; an
// absence fails to compile the moment something tries to use it.
//
// So this fence is AUTO-DISCOVERING. It walks every exported type in this package by reflection and
// asks whether any field could carry a key value. A whitelist would pass a field added tomorrow, which
// is precisely the case that matters — nobody adds a key field on the day the fence is written.

// keyShapedField matches a field name a key would plausibly live in. Name COMPONENTS, not substrings:
// the same lesson migration 0046 learned when a substring match flagged `tokens_in`, a COUNT.
var keyShapedField = regexp.MustCompile(`(?i)(^|_)(api_?key|apikey|secret|password|passwd|bearer|token|credential_value)(_|$)`)

// typesUnderTest is every exported struct this package defines. Listed by VALUE rather than by name so
// the compiler keeps it honest: a type that is renamed or removed fails to build here.
func typesUnderTest() []any {
	return []any{
		Definition{}, Node{}, Version{}, PublishResult{}, Availability{}, RunnerHosts{},
		canonicalDefinition{}, canonicalNode{}, canonicalGraph{}, legacyDefinition{},
		extendedDefinition{}, ListEdit{}, NodeEdit{}, TopologyEdit{}, NodeRun{},
	}
}

// keyShapedOffenders walks every exported struct reachable from `types` and returns the fields a key
// could live in, plus how many fields it inspected.
//
// 🔴 EXTRACTED so the fence and its DRILL run the SAME code (P36 task 3.3). A drill that re-implemented
// the walk would prove that the drill's walk fires, which is not the question — the question is whether
// THE FENCE fires on a shape it has never seen.
func keyShapedOffenders(types []any) (offenders []string, scanned int) {
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			scanned++
			name := path + "." + f.Name
			// `CredentialRef` and `CriticCredentialRef` are provider NAMES and are the point of the
			// design. They are exempt BY NAME, and the exemption is narrow: `credential_value` is not.
			if f.Name == "CredentialRef" || f.Name == "CriticCredentialRef" {
				continue
			}
			if keyShapedField.MatchString(f.Name) || keyShapedField.MatchString(f.Tag.Get("json")) {
				offenders = append(offenders, name+" ("+f.Type.String()+")")
			}
			walk(f.Type, name)
		}
	}
	for _, v := range types {
		rt := reflect.TypeOf(v)
		walk(rt, rt.Name())
	}
	return offenders, scanned
}

func TestNoTypeInThisPackageHasAFieldThatCouldCarryAKey(t *testing.T) {
	offenders, scanned := keyShapedOffenders(typesUnderTest())

	if len(offenders) > 0 {
		t.Errorf("these fields could carry a provider key:\n  %s\n\n"+
			"  D5: the credential is BOUND, never entered. A masked console input storing an encrypted "+
			"value \"puts plaintext keys in request bodies and audit trails and duplicates a secret store "+
			"the deployment already runs\" — and level 1 on the ladder is not tradeable against the "+
			"convenience of a text field.", strings.Join(offenders, "\n  "))
	}
	// 🔴 ANTI-VACUITY. A reflection walk that reached nothing would report clean forever.
	//
	// 🔴 P36 raises the floor from 20 to 60. The package grew a Node, a NodeEdit, a TopologyEdit, two
	// canonical projections and two wire projections, and a floor set for the old shape would be met by
	// a walk that reached only the old types — which is exactly the vacuous pass the whole file is about.
	if scanned < 60 {
		t.Errorf("the walk inspected only %d field(s) — it is not reaching the types, so its clean "+
			"report above means nothing", scanned)
	}
	// 🔴 P36 task 3.3 — AND IT MUST REACH THE NEW SHAPE BY NAME. A count can be met by the old types
	// alone; this asserts the fields P36 added were actually walked.
	for _, want := range []string{"Node", "NodeEdit", "TopologyEdit", "canonicalNode"} {
		if !reachedType(want) {
			t.Errorf("the walk never reached %s. A fence enumerating the old shape passes VACUOUSLY on "+
				"the new one, and a vacuous pass looks exactly like a real one.", want)
		}
	}
	// And the pattern must be able to FIRE, or its silence says nothing.
	if !keyShapedField.MatchString("APIKey") || !keyShapedField.MatchString("access_token") {
		t.Error("the key-shaped-field pattern does not match `APIKey` or `access_token` — the fence " +
			"reports clean because it cannot see anything, not because there is nothing to see")
	}
	// 🚫 And it must NOT fire on a token COUNT, or somebody switches it off.
	if keyShapedField.MatchString("TokensIn") || keyShapedField.MatchString("tokens_out") {
		t.Error("the pattern fires on a token COUNT — a fence that flags the fields it exists to permit " +
			"is a fence that gets deleted")
	}
}

// reachedType reports whether a type of this NAME is in typesUnderTest, directly or as a field.
func reachedType(name string) bool {
	found := false
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array ||
			rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		if rt.Name() == name {
			found = true
		}
		for i := 0; i < rt.NumField(); i++ {
			walk(rt.Field(i).Type)
		}
	}
	for _, v := range typesUnderTest() {
		walk(reflect.TypeOf(v))
	}
	return found
}

// 🔴 P36 TASK 3.3 — THE DRILL. Add a key-shaped field to the NEW struct and require the fence to fail.
//
// This is the assertion the design asks for by name: "The reflective credential fence can pass
// vacuously on the new shape. Adding a key-shaped field to the new struct must make it fail, and that
// must be asserted, not assumed."
//
// `nodeWithAKey` mirrors `Node` field for field and adds one. It runs through
// `keyShapedOffenders` — the SAME walk the fence above uses, not a copy — so a pass here is a pass of
// the real mechanism.
type nodeWithAKey struct {
	NodeID              string   `json:"node_id"`
	PromptRef           string   `json:"prompt_ref"`
	ModelRef            string   `json:"model_ref"`
	CredentialRef       string   `json:"credential_ref"`
	SkillRefs           []string `json:"skill_refs,omitempty"`
	ToolNames           []string `json:"tool_names,omitempty"`
	ContextRef          string   `json:"context_ref"`
	MemoryRef           string   `json:"memory_ref,omitempty"`
	HarnessRef          string   `json:"harness_ref"`
	LoopRef             string   `json:"loop_ref,omitempty"`
	CriticModelRef      string   `json:"critic_model_ref,omitempty"`
	CriticCredentialRef string   `json:"critic_credential_ref,omitempty"`
	// The one addition. This is what somebody adds in a hurry when a vendor needs a per-node key.
	APIKey string `json:"api_key"`
}

// definitionWithAKeyedNode is the containing shape, so the drill exercises the walk's RECURSION into
// the node list rather than only its handling of a top-level struct. A fence that inspected Definition
// and never descended would pass on this.
type definitionWithAKeyedNode struct {
	Nodes []nodeWithAKey `json:"nodes"`
}

func TestTheCredentialFenceFiresOnAKeyAddedToTheNewShape(t *testing.T) {
	for _, c := range []struct {
		name string
		typ  any
		want string
	}{
		{"a key on the node itself", nodeWithAKey{}, "nodeWithAKey.APIKey"},
		{"a key on a node inside a definition", definitionWithAKeyedNode{},
			"definitionWithAKeyedNode.Nodes.APIKey"},
	} {
		offenders, scanned := keyShapedOffenders([]any{c.typ})
		if scanned == 0 {
			t.Fatalf("%s: the walk inspected nothing", c.name)
		}
		var hit bool
		for _, o := range offenders {
			if strings.HasPrefix(o, c.want) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("%s: the fence did NOT fire on %s. It enumerates the old shape and passes "+
				"VACUOUSLY on the new one — which looks exactly like a real pass, and the field it "+
				"missed is a provider key in a database column.\n  offenders: %v",
				c.name, c.want, offenders)
		}
	}
}

// 🔴 The SERIALISED definition carries no key either. A field could be exempt by name and still hold
// one; this checks what actually crosses a wire or lands in a column.
func TestASerialisedDefinitionCarriesNoKeyShapedKey(t *testing.T) {
	d := goodDefinition()
	d.Nodes[0].CriticModelRef = "claude-sonnet-5"
	d.Nodes[0].CriticCredentialRef = "anthropic"
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for k := range doc {
		if k == "credential_ref" || k == "critic_credential_ref" {
			continue
		}
		if keyShapedField.MatchString(k) {
			t.Errorf("the serialised definition carries key-shaped field %q: %s", k, b)
		}
	}
}

// 🔴 A RESOLVED credential is never returned, stored or echoed. The publisher fetches one to prove the
// reference resolves and drops it on the same line; this asserts nothing that came back survives into
// the stored version.
func TestAResolvedCredentialNeverReachesTheStoredVersion(t *testing.T) {
	const marker = "sk-ant-THIS-MUST-NOT-BE-STORED"
	sec := leakySecrets{value: marker}
	p, store := testPublisher(t, registered("claude-opus-5"), sec)
	ctx := context.Background()

	res, err := p.Publish(ctx, goodDefinition())
	if err != nil {
		t.Fatal(err)
	}
	v, ok, _ := store.Get(ctx, res.ConfigHash)
	if !ok {
		t.Fatal("nothing was stored")
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), marker) {
		t.Errorf("the resolved credential VALUE reached the stored version: %s", b)
	}
	if strings.Contains(v.CredentialRef, marker) {
		t.Error("the credential value was stored in the reference field")
	}
}

// leakySecrets returns a key value, so the test above can look for it everywhere afterwards.
type leakySecrets struct{ value string }

func (l leakySecrets) Credential(context.Context, string) (providergateway.Credential, error) {
	return providergateway.Credential{APIKey: l.value}, nil
}
func (l leakySecrets) Describe() providergateway.SourceInfo {
	return providergateway.SourceInfo{Kind: "static"}
}

// 🔴 An unresolved credential's ERROR names the provider and nothing the source said about the secret.
// An error message is a log line, and a log line is the second-most-common place a key ends up.
func TestTheUnresolvedCredentialErrorLeaksNothing(t *testing.T) {
	p, _ := testPublisher(t, registered("claude-opus-5"), fakeSecrets{known: map[string]bool{}})
	_, err := p.Publish(context.Background(), goodDefinition())
	if err == nil {
		t.Fatal("an unresolvable credential published")
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("the refusal does not name the provider, so an operator cannot act on it: %v", err)
	}
	// The fake's own error text must not be carried through — a real source's error can quote a secret
	// id, an ARN, or worse.
	if strings.Contains(err.Error(), "no such secret") {
		t.Errorf("the secrets source's own error text was carried into the refusal: %v", err)
	}
}
