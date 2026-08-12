package runlink

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// optinfields_test.go is P29 §2.2 — the fence over what the two OPT-IN payloads may carry.
//
// # Why a structural fence and not a review
//
// The run-link allowlist is protected by `TestContentIsNotExpressibleOnTheAllowlist`, which greps field
// NAMES for content-shaped words. That catches `prompt_text` and it would not have caught a field called
// `detail`, `note`, `reason` or `summary` — and the transform engine's own refusals carry exactly such a
// field, `RewriteError.Detail`, whose sentences name arguments, symbols, policies and registry rows
// lifted out of the customer's source. Copying it onto the wire is a one-word edit that reads as an
// improvement ("say WHY it refused"), and no name-based check would object.
//
// So this fence asks a different question, structurally, about the Go types themselves: **can any field
// added by this change hold a string that did not come from a closed set the platform itself defines?**
// A `string` field is admissible only when this test can name the closed set it draws from and check that
// every member is accounted for. Everything else must be an identifier the platform issued, or a number.

// closedSets maps a JSON path on the opt-in payloads to the closed set its values are drawn from.
//
// A `string` field with no entry here FAILS. That is the point: adding one is a decision somebody makes,
// in this file, with the set written down — not a default they inherit by picking `string` as a type.
func closedSets() map[string][]string {
	return map[string][]string{
		// Verdict + outcome statuses: two values each, defined in this package.
		"nodes.axis_verdicts.status": VerdictStatuses(),
		"node_outcomes.outcome":      OutcomeStatuses(),
		// Cause classes: the three transform.CauseClass values. Spelled here rather than imported so that
		// `internal/runlink` stays free of a dependency on the transform engine — the egress package must
		// not import the thing whose output it constrains. A drift between the two is caught by
		// TestCauseIdentifiersMatchTheEngine in internal/cli, which CAN see both.
		"nodes.axis_verdicts.cause": {
			"not-expressible-at-a-call-site",
			"call-site-cannot-carry-it",
			"no-materializer-for-this-language",
		},
		"node_outcomes.cause": {
			"not-expressible-at-a-call-site",
			"call-site-cannot-carry-it",
			"no-materializer-for-this-language",
		},
		// P30 · who wrote an edge, and why the agent declined a subject. Both defined in THIS package
		// (EdgeAuthors, AbstentionReasons) rather than imported from internal/herosagent, for the reason
		// the cause classes above are spelled out: the egress package must not import the thing whose
		// output it constrains. TestAbstentionCausesMatchTheAgent catches the drift.
		"edges.author":      EdgeAuthors(),
		"abstentions.cause": AbstentionCauses(),
	}
}

// identifierFields are the string fields that carry an identifier rather than a closed-set member: a
// hash, a revision, a version, a node id, an axis name, a symbol, a path.
//
// 🔴 `nodes.symbol` and `nodes.file` are repository-originating and were ratified in P11 with that
// argument made explicitly — a path and a line range say WHERE a call is, not what it says. They are
// listed to keep this test honest about what it does and does not prove: it proves nothing NEW can
// carry free text, not that nothing ever could.
func identifierFields() map[string]bool {
	return map[string]bool{
		"contract_version": true, "workflow_id": true, "source_revision": true, "ir_version": true,
		"coverage_version": true, "config_hash": true, "tool_version": true, "status": true,
		"nodes.node_id": true, "nodes.symbol": true, "nodes.file": true, "nodes.provider": true,
		"nodes.model_id": true, "nodes.context_policy": true, "nodes.language": true,
		"nodes.axis_verdicts.axis": true,
		"edges.from":               true, "edges.to": true, "edges.kind": true,
		"node_outcomes.node_id": true,
		// P30 · `agent_config_hash` is a content hash of a configuration the PLATFORM published, so it
		// originates here rather than in the customer's repository. `abstentions.subject` is a node id or
		// an ordered pair of them written `a→b` — the same identifiers `edges.from` and `edges.to`
		// already carry, and no more expressive than they are.
		"agent_config_hash": true, "abstentions.subject": true,
	}
}

// 🔴 THE FENCE. Every string field on either opt-in payload is a closed-set member or a declared
// identifier; everything else is a number.
//
// Verified red before it was verified green, twice:
//
//   - adding `Detail string \`json:"detail"\“ to WireAxisVerdict — the realistic mistake, since the
//     engine has exactly that field and copying it reads as helpfulness:
//     `nodes.axis_verdicts.detail is a string with no closed set and is not a declared identifier`
//   - adding `Diff string \`json:"diff"\“ to TransformReceipt: same failure, plus the diff-shaped-name
//     check below.
func TestEveryOptInFieldIsAnIdentifierOrAClosedSetMember(t *testing.T) {
	sets, ids := closedSets(), identifierFields()
	for _, root := range []struct {
		what string
		typ  reflect.Type
	}{
		{"the opt-in structure payload", reflect.TypeOf(WorkflowIRPayload{})},
		{"the transform receipt", reflect.TypeOf(TransformReceipt{})},
	} {
		walkWireFields(t, root.typ, "", func(path string, ft reflect.Type) {
			switch ft.Kind() {
			case reflect.Int, reflect.Int64, reflect.Float64, reflect.Bool:
				return // a count, a quantity or a flag: nothing repository-originating fits in one
			case reflect.String:
			default:
				t.Errorf("%s: %s has kind %s — the opt-in payloads carry identifiers, closed-set members "+
					"and counts, and a new kind needs a decision recorded here", root.what, path, ft.Kind())
				return
			}
			if ids[path] {
				return
			}
			if members, ok := sets[path]; ok {
				if len(members) == 0 {
					t.Errorf("%s: %s declares a closed set with no members", root.what, path)
				}
				return
			}
			t.Errorf("%s: %s is a string with no closed set and is not a declared identifier.\n"+
				"  Every string on this boundary must be one or the other. If it is meant to carry a "+
				"sentence, it must not exist: the console renders its own copy from its own catalogue, and "+
				"a sentence built on the customer's machine names their arguments, symbols and policies.\n"+
				"  If it really is an identifier, add it to identifierFields() — which is a decision a "+
				"reviewer reads, not a default.", root.what, path)
		})
	}
}

// Every field added by this change appears on its allowlist with a justification — the allowlist is the
// security-review artefact, and a field on the wire that is not on it is a field nobody reviewed.
func TestEveryOptInWireFieldIsOnItsAllowlist(t *testing.T) {
	cases := []struct {
		what      string
		typ       reflect.Type
		permitted func(string) bool
		// skip are the envelope keys that are not per-field claims about the customer.
		skip map[string]bool
	}{
		{"the opt-in structure payload", reflect.TypeOf(WorkflowIRPayload{}), WorkflowIRPermitted,
			map[string]bool{"contract_version": true}},
		{"the transform receipt", reflect.TypeOf(TransformReceipt{}), TransformReceiptPermitted,
			map[string]bool{"contract_version": true}},
	}
	for _, c := range cases {
		walkWireFields(t, c.typ, "", func(path string, _ reflect.Type) {
			if c.skip[path] || c.permitted(path) {
				return
			}
			t.Errorf("%s transmits %q, which is not on its allowlist.\n"+
				"  Add it with the one-line justification the file's convention requires — what the "+
				"platform does with it, and why it is structure rather than content.", c.what, path)
		})
	}
}

// The reverse: an allowlist row for a field nothing transmits is a claim about a boundary that does not
// exist, and it is how an allowlist stops describing the wire it claims to describe.
func TestNoOptInAllowlistRowIsStale(t *testing.T) {
	for _, c := range []struct {
		what string
		typ  reflect.Type
		list []AllowlistField
	}{
		{"the opt-in structure payload", reflect.TypeOf(WorkflowIRPayload{}), WorkflowIRAllowlist},
		{"the transform receipt", reflect.TypeOf(TransformReceipt{}), TransformReceiptAllowlist},
	} {
		onWire := map[string]bool{}
		walkWireFields(t, c.typ, "", func(path string, _ reflect.Type) { onWire[path] = true })
		for _, f := range c.list {
			if !onWire[f.Name] {
				t.Errorf("%s's allowlist ratifies %q, which nothing transmits", c.what, f.Name)
			}
		}
		if len(c.list) == 0 {
			t.Errorf("%s has an empty allowlist", c.what)
		}
	}
}

// A diff, a source line and an environment value have no field to occupy — asserted by NAME as well as
// by shape, because the two checks fail on different mistakes and neither subsumes the other.
func TestNoOptInFieldIsShapedLikeContent(t *testing.T) {
	forbidden := []string{
		"diff", "patch", "hunk", "prompt", "credential", "secret", "password", "token",
		"body", "content", "source_code", "env", "api_key", "detail", "reason", "message", "note",
	}
	for _, c := range []struct {
		what string
		typ  reflect.Type
	}{
		{"the opt-in structure payload", reflect.TypeOf(WorkflowIRPayload{})},
		{"the transform receipt", reflect.TypeOf(TransformReceipt{})},
	} {
		walkWireFields(t, c.typ, "", func(path string, _ reflect.Type) {
			leaf := path
			if i := strings.LastIndex(leaf, "."); i >= 0 {
				leaf = leaf[i+1:]
			}
			for _, bad := range forbidden {
				if leaf == bad || strings.HasPrefix(leaf, bad+"_") || strings.HasSuffix(leaf, "_"+bad) {
					t.Errorf("%s carries %q, whose name says content. Nothing that names what the "+
						"customer's code SAYS crosses this boundary — only where it is and what shape it "+
						"has.", c.what, path)
				}
			}
		})
	}
}

// A receipt carries three integers where a diff would go, and this asserts the three are the ONLY thing
// standing in for one — a fourth field of any string kind under the diffstat would be a place for a hunk.
func TestTheReceiptsDiffIsThreeIntegersAndNothingElse(t *testing.T) {
	r := TransformReceipt{
		ContractVersion: TransformReceiptContractVersion,
		FilesChanged:    3, LinesAdded: 12, LinesRemoved: 4,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"files_changed", "lines_added", "lines_removed"} {
		var n int
		if err := json.Unmarshal(m[k], &n); err != nil {
			t.Errorf("%s is not an integer on the wire: %v", k, err)
		}
	}
	if strings.Contains(string(b), "@@") || strings.Contains(string(b), "+++") {
		t.Errorf("a receipt rendered diff markers: %s", b)
	}
}

// walkWireFields visits every JSON leaf of a wire struct, dotted, descending into structs and slices.
func walkWireFields(t *testing.T, typ reflect.Type, prefix string, visit func(path string, ft reflect.Type)) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			t.Fatalf("%s.%s has no json tag — a wire struct's fields are named on the wire, on purpose",
				typ.Name(), f.Name)
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			walkWireFields(t, ft, path, visit)
			continue
		}
		visit(path, ft)
	}
}
