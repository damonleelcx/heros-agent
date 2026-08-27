package herosagent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/registry"
)

// axiseditor.go is D12: NO AXIS IS A TEXT BOX, and every param validates AT SAVE (§6b).
//
// # Why validation is at save and not at run
//
// D11's argument in miniature, and it is the same one three times over: a malformed strategy discovered
// when a run reaches it is discovered by the wrong person at the wrong time. The operator who typed it
// has moved on; the person who meets the refusal did not make the choice and cannot tell whether it is
// a bug or a configuration.
//
// # Why a free-text field is refused for a closed vocabulary
//
// "A free-text field for a value with a closed vocabulary eventually holds a value nothing can
// interpret, and the closed sets exist precisely so a stored `config_hash` still means something months
// later."
//
// So every function here takes the CANDIDATE and the VOCABULARY it must belong to, and refuses by name.

// MaxTurnsCeiling is the hard ceiling on a multi-turn harness (task 6b.9).
//
// 🔴 It is a CONSTANT rather than configuration. A ceiling an operator can raise is not a ceiling: the
// cost of a runaway loop is paid by whoever is billed for the analysis, and sixteen turns is already
// far more than any calibration fixture needs.
const MaxTurnsCeiling = 16

// SkillEntry is a registered skill as the editor needs it (task 6b.4).
type SkillEntry struct {
	VersionID string
	Name      string
	// ImplHandle is what actually runs. A skill with none is not selectable — it would bind a
	// capability with no implementation behind it.
	ImplHandle string
	// Schema is the skill's JSON Schema, as registered.
	Schema json.RawMessage
	// SchemaCompiles reports whether the registry compiled it. 🚫 A skill whose schema does not compile
	// is NOT selectable: it would bind a contract nothing can validate against.
	SchemaCompiles bool
	// CompileError is why it did not, so the refusal names something actionable.
	CompileError string
}

// ToolEntry is an indexed tool as the editor needs it (task 6b.5).
type ToolEntry struct {
	Name string
	// TenantScope is `_global` or a tenant id. 🔴 CARRIED AND ALWAYS DISPLAYED: `_global` and a
	// tenant-scoped tool of the same NAME are DIFFERENT BINDINGS, and an editor that showed only the
	// name would let an operator bind one believing they had bound the other.
	TenantScope string
	Description string
	RiskTier    string
	Approved    bool
	// DeclaresNetwork reports whether this tool declares outbound network access.
	DeclaresNetwork bool
}

// Identity is the pair that makes a tool unique. Two tools of one name in two scopes are two tools.
func (t ToolEntry) Identity() string { return t.TenantScope + "/" + t.Name }

// ── Prompt (6b.1) ───────────────────────────────────────────────────────────────────────────────

// PromptTemplate is a parsed template: the body plus the slots DERIVED from it.
type PromptTemplate struct {
	Body string
	// Slots are parsed out of the body, never typed by an operator. A typed slot list is a list that
	// eventually disagrees with the template it describes.
	Slots []string
}

// ParsePrompt derives a template's slots from its body.
//
// 🔴 DERIVED, not declared (task 6b.1). The slots ARE what the body references; asking an operator to
// list them creates two sources for one fact, and the failure is a bound slot the template never uses —
// which renders as a prompt that silently ignores an input somebody configured.
func ParsePrompt(body string) PromptTemplate {
	out := PromptTemplate{Body: body, Slots: []string{}}
	seen := map[string]bool{}
	for i := 0; i+1 < len(body); i++ {
		if body[i] != '{' || body[i+1] != '{' {
			continue
		}
		end := strings.Index(body[i:], "}}")
		if end < 0 {
			break
		}
		name := strings.TrimSpace(body[i+2 : i+end])
		if name != "" && !seen[name] {
			seen[name] = true
			out.Slots = append(out.Slots, name)
		}
		i += end
	}
	sort.Strings(out.Slots)
	return out
}

// ValidateBindings refuses a bound slot the template does not declare, BY NAME (task 6b.1).
//
// The failure this prevents is quiet: a binding for a slot the template never references is an input an
// operator configured and the prompt ignores, and nothing at run time reports it — the prompt renders
// perfectly well without it.
func ValidateBindings(t PromptTemplate, bound []string) error {
	declared := map[string]bool{}
	for _, s := range t.Slots {
		declared[s] = true
	}
	var missing []string
	for _, b := range bound {
		if !declared[b] {
			missing = append(missing, b)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("%w: the prompt binds slot(s) %s, which the template does not reference. The "+
		"template declares %v. A binding the template ignores is an input somebody configured and the "+
		"prompt silently drops",
		ErrInvalidDefinition, strings.Join(missing, ", "), t.Slots)
}

// ── Skills (6b.4) ───────────────────────────────────────────────────────────────────────────────

// SelectableSkills filters a registry snapshot to what may actually be bound.
//
// 🚫 A skill whose schema does not compile is NOT selectable, and 🚫 a schema carrying a REMOTE `$ref`
// is REJECTED rather than fetched: "the registry is already hermetic and the console must not become
// the hole in it." Fetching one would make a save depend on a third party's availability and would let
// a skill's contract change without a version changing.
func SelectableSkills(all []SkillEntry) (selectable []SkillEntry, refused map[string]string) {
	refused = map[string]string{}
	for _, s := range all {
		switch {
		case s.ImplHandle == "":
			refused[s.VersionID] = "this skill has no impl_handle, so binding it would bind a capability " +
				"with no implementation behind it"
		case !s.SchemaCompiles:
			refused[s.VersionID] = "this skill's schema does not compile" + detail(s.CompileError) +
				", so nothing could validate a call against it"
		case hasRemoteRef(s.Schema):
			refused[s.VersionID] = "this skill's schema carries a REMOTE $ref. It is rejected rather " +
				"than fetched: the registry is hermetic, and resolving a remote reference here would " +
				"make a save depend on a third party and let a contract change without a version changing"
		default:
			selectable = append(selectable, s)
		}
	}
	sort.SliceStable(selectable, func(i, j int) bool { return selectable[i].VersionID < selectable[j].VersionID })
	return selectable, refused
}

func detail(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

// hasRemoteRef reports whether a schema references anything off this machine.
//
// It walks the decoded document rather than matching the raw bytes, because `"$ref"` appears inside
// string values legitimately and a substring match would refuse a schema that merely mentions it — a
// fence that trips on prose is a fence somebody deletes.
func hasRemoteRef(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		// An undecodable schema is somebody else's refusal (SchemaCompiles is false); it is not a
		// remote-ref finding, and claiming it were would name the wrong cause.
		return false
	}
	return walkForRemoteRef(doc)
}

func walkForRemoteRef(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "$ref" {
				if s, ok := child.(string); ok && isRemoteRef(s) {
					return true
				}
			}
			if walkForRemoteRef(child) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if walkForRemoteRef(child) {
				return true
			}
		}
	}
	return false
}

// isRemoteRef reports whether a `$ref` leaves this document. A local pointer (`#/$defs/x`) is fine.
func isRemoteRef(ref string) bool {
	r := strings.TrimSpace(ref)
	return r != "" && !strings.HasPrefix(r, "#")
}

// ── Tools (6b.5, 6b.6) ──────────────────────────────────────────────────────────────────────────

// BindableTools filters the tool index to what may be bound, with a reason for everything refused.
//
// 🚫 TWO REFUSALS, and the second is not overridable from the console (task 6b.6):
//
//   - An UNAPPROVED tool is not bindable. Approval is the review; binding around it is skipping it.
//   - A tool declaring OUTBOUND NETWORK ACCESS is not bindable AT ALL. HEROS reads a pinned snapshot,
//     and a tool reaching the network from inside the analysis loop would be an egress surface created
//     by a dropdown — which is exactly what the two-lane egress rule exists to prevent.
func BindableTools(all []ToolEntry) (bindable []ToolEntry, refused map[string]string) {
	refused = map[string]string{}
	for _, t := range all {
		switch {
		case t.DeclaresNetwork:
			refused[t.Identity()] = "this tool declares outbound network access and is NOT bindable. " +
				"HEROS reads a pinned snapshot; a tool reaching the network from inside the analysis loop " +
				"would be an egress surface created by a dropdown. This refusal is not overridable here."
		case !t.Approved:
			refused[t.Identity()] = "this tool is not approved. Approval is the review, and binding " +
				"around it is skipping it."
		default:
			bindable = append(bindable, t)
		}
	}
	sort.SliceStable(bindable, func(i, j int) bool { return bindable[i].Identity() < bindable[j].Identity() })
	return bindable, refused
}

// RequireBindableTools refuses a selection containing a tool that may not be bound (task 10.15).
//
// 🔴 The check is on the IDENTITY — scope AND name — because `_global/search` and `acme/search` are
// different bindings. A check on the name alone would let a tenant-scoped tool be approved into a
// global binding.
func RequireBindableTools(selected []string, all []ToolEntry) error {
	bindable, refused := BindableTools(all)
	ok := map[string]bool{}
	for _, t := range bindable {
		ok[t.Identity()] = true
	}
	var problems []string
	for _, s := range selected {
		if ok[s] {
			continue
		}
		if why, known := refused[s]; known {
			problems = append(problems, fmt.Sprintf("%s: %s", s, why))
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: no tool with that scope and name is indexed. A "+
			"tool is identified by BOTH — `_global/x` and `acme/x` are different bindings", s))
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%w: %s", ErrInvalidDefinition, strings.Join(problems, "; "))
}

// RequireSelectableSkills refuses a selection containing a skill that may not be bound.
func RequireSelectableSkills(selected []string, all []SkillEntry) error {
	selectable, refused := SelectableSkills(all)
	ok := map[string]bool{}
	for _, s := range selectable {
		ok[s.VersionID] = true
	}
	var problems []string
	for _, s := range selected {
		if ok[s] {
			continue
		}
		if why, known := refused[s]; known {
			problems = append(problems, fmt.Sprintf("%s: %s", s, why))
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: no registered skill has that version_id", s))
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%w: %s", ErrInvalidDefinition, strings.Join(problems, "; "))
}

// ── Harness params (6b.9) ───────────────────────────────────────────────────────────────────────

// HarnessParams are a harness strategy's parameters, validated at SAVE.
type HarnessParams struct {
	Strategy string
	// MaxTurns is REQUIRED for a multi-turn strategy. Zero on a multi-turn strategy is refused rather
	// than defaulted: a default here is an unbounded loop nobody chose.
	MaxTurns int
	// RetryBudget is how many times a failed turn may be retried.
	RetryBudget int
}

// ValidateHarnessParams enforces the ceiling and the retry rule (task 6b.9).
//
// 🔴 THE RETRY RULE IS THE SUBTLE HALF. `max_turns: 8` with `retry_budget: 3` is up to 24 provider
// calls, and each of the three numbers looks reasonable on its own. It is refused with the SAME
// reasoning as an out-of-ceiling `max_turns`, because it is the same failure reached by multiplication.
func ValidateHarnessParams(p HarnessParams) error {
	multiTurn := p.Strategy != "single-shot" && p.Strategy != ""
	switch {
	case !multiTurn:
		if p.MaxTurns > 1 {
			return fmt.Errorf("%w: %q is single-turn and max_turns is %d. A turn count on a strategy that "+
				"takes one turn is a number that does nothing, and a reader would take it for a bound",
				ErrInvalidDefinition, p.Strategy, p.MaxTurns)
		}
		return nil
	case p.MaxTurns <= 0:
		return fmt.Errorf("%w: %q is multi-turn and max_turns is unset. It is REQUIRED rather than "+
			"defaulted: a default here is an unbounded loop nobody chose, paid for by whoever is billed "+
			"for the analysis", ErrInvalidDefinition, p.Strategy)
	case p.MaxTurns > MaxTurnsCeiling:
		return fmt.Errorf("%w: max_turns is %d and the ceiling is %d. The ceiling is a constant rather "+
			"than configuration — a ceiling an operator can raise is not a ceiling",
			ErrInvalidDefinition, p.MaxTurns, MaxTurnsCeiling)
	case p.RetryBudget < 0:
		return fmt.Errorf("%w: retry_budget cannot be negative", ErrInvalidDefinition)
	}
	// The multiplication. Each number is reasonable alone.
	if worst := p.MaxTurns * (1 + p.RetryBudget); worst > MaxTurnsCeiling {
		return fmt.Errorf("%w: max_turns %d with retry_budget %d is up to %d provider calls, past the "+
			"ceiling of %d. Refused for the same reason an out-of-ceiling max_turns is: it is the same "+
			"unbounded loop reached by multiplication, and each of the two numbers looks reasonable alone",
			ErrInvalidDefinition, p.MaxTurns, p.RetryBudget, worst, MaxTurnsCeiling)
	}
	return nil
}

// ── Policy params (6b.7) ────────────────────────────────────────────────────────────────────────

// ValidatePolicyParams checks a context or memory policy's params against its declared ParamsSchema,
// naming the POLICY and the PARAMETER on failure (task 6b.7).
//
// 🔴 It refuses at SAVE and writes no version row. A malformed param discovered at run is discovered by
// somebody who did not choose it — and a version row written for a configuration that cannot execute is
// a `config_hash` pointing at something nothing can run.
func ValidatePolicyParams(policy string, params map[string]any, schema map[string]ParamSpec) error {
	var problems []string
	for name, spec := range schema {
		v, present := params[name]
		if !present {
			if spec.Required {
				problems = append(problems, fmt.Sprintf("%s.%s is required and unset", policy, name))
			}
			continue
		}
		if err := spec.check(policy, name, v); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for name := range params {
		if _, declared := schema[name]; !declared {
			// An UNDECLARED param is refused rather than ignored, for the reason an unknown axis is: a
			// silently dropped parameter is a setting the operator believes they made.
			problems = append(problems, fmt.Sprintf("%s.%s is not a parameter of this policy — a "+
				"parameter that is silently dropped is a setting somebody believes they made", policy, name))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%w: %s", ErrInvalidDefinition, strings.Join(problems, "; "))
}

// ParamSpec is one declared parameter, as a policy's ParamsSchema describes it.
type ParamSpec struct {
	Type     string // "int" | "float" | "string" | "bool"
	Required bool
	Min      *float64
	Max      *float64
	// Enum closes a string parameter's vocabulary.
	Enum []string
}

func (p ParamSpec) check(policy, name string, v any) error {
	label := policy + "." + name
	num := func() (float64, bool) {
		switch t := v.(type) {
		case float64:
			return t, true
		case int:
			return float64(t), true
		case int64:
			return float64(t), true
		}
		return 0, false
	}
	switch p.Type {
	case "int", "float":
		f, ok := num()
		if !ok {
			return fmt.Errorf("%s must be a number, got %T", label, v)
		}
		if p.Type == "int" && f != float64(int64(f)) {
			return fmt.Errorf("%s must be a whole number, got %v", label, f)
		}
		if p.Min != nil && f < *p.Min {
			return fmt.Errorf("%s is %v, below the minimum %v", label, f, *p.Min)
		}
		if p.Max != nil && f > *p.Max {
			return fmt.Errorf("%s is %v, above the maximum %v", label, f, *p.Max)
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s must be a string, got %T", label, v)
		}
		if len(p.Enum) > 0 {
			for _, e := range p.Enum {
				if s == e {
					return nil
				}
			}
			return fmt.Errorf("%s is %q, which is not one of %v", label, s, p.Enum)
		}
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s must be true or false, got %T", label, v)
		}
	}
	return nil
}

// ── The loop axis, edited per node (P36 task 3.2 / operator-agent-authoring spec) ────────────────
//
// # 🔴 Why the loop axis is not a text box either
//
// D12 unchanged, one axis later. A free-text strategy name is a value nothing can interpret the moment
// the vocabulary moves; a free-text stop condition is worse, because it reads as configuration and is
// inert. Both bind to the registry's closed sets.
//
// # Why this lives here rather than in publish.go
//
// The split is the one the whole file draws: these functions take the CANDIDATE and the VOCABULARY and
// need no database, so a console can run them on every keystroke. `Publisher.checkLoopAxis` is the
// second gate, for the request that does not come from the console, and it resolves the ref against the
// registry — a question only a store can answer.

// LoopParams are a loop strategy's parameters, validated at SAVE.
type LoopParams struct {
	Strategy string
	// MaxTurns is REQUIRED for a multi-turn strategy. Zero on a multi-turn strategy is refused rather
	// than defaulted: a default here is an unbounded loop nobody chose.
	MaxTurns int
	// StopCondition is why the loop stops. From the strategy's declared set, never free text.
	StopCondition string
	// EnvelopeTurnCeiling is the node's harness envelope ceiling, and 0 means the node binds no
	// envelope.
	//
	// 🔴 A pointer would be more honest about "absent" and is not needed here: `ValidateLoopParams`
	// treats 0 as "no ceiling declared", which is the same answer `checkTurnCeiling` gives for a nil
	// envelope — a node with no envelope is bounded by the platform ceiling alone, and refusing every
	// loop that had no envelope would make the loop axis unusable without also authoring a harness.
	EnvelopeTurnCeiling int
}

// ValidateLoopParams enforces the vocabulary, the platform ceiling and the node's envelope ceiling —
// at save, naming the axis and (via the caller's node label) the node.
//
// 🔴 IT NAMES BOTH NUMBERS when the envelope refuses (task 3.6). "Too many turns" leaves an operator
// unable to tell whether to lower their value or ask for a higher policy, and those are requests to two
// different people: the ceiling is IMPOSED by whoever owns the envelope, the turn count is CHOSEN by
// whoever authors the loop.
func ValidateLoopParams(p LoopParams, where string) error {
	known := false
	for _, n := range registry.LoopStrategyNames() {
		if n == p.Strategy {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("%w: %q is not a loop strategy this deployment knows%s. It is one of %s. A "+
			"free-text strategy is a value nothing can interpret the moment the vocabulary moves",
			ErrInvalidDefinition, p.Strategy, where, strings.Join(registry.LoopStrategyNames(), ", "))
	}
	multiTurn := p.Strategy != registry.StrategySingleShot
	switch {
	case !multiTurn:
		if p.MaxTurns > 1 {
			return fmt.Errorf("%w: %q is single-turn and max_turns is %d%s. A turn count on a strategy "+
				"that takes one turn is a number that does nothing, and a reader would take it for a bound",
				ErrInvalidDefinition, p.Strategy, p.MaxTurns, where)
		}
		return nil
	case p.MaxTurns <= 0:
		return fmt.Errorf("%w: %q is multi-turn and max_turns is unset%s. It is REQUIRED rather than "+
			"defaulted: a default here is an unbounded loop nobody chose, paid for by whoever is billed "+
			"for the analysis", ErrInvalidDefinition, p.Strategy, where)
	case p.MaxTurns > MaxTurnsCeiling:
		return fmt.Errorf("%w: max_turns is %d%s and the platform ceiling is %d. The ceiling is a "+
			"constant rather than configuration — a ceiling an operator can raise is not a ceiling",
			ErrInvalidDefinition, p.MaxTurns, where, MaxTurnsCeiling)
	case p.EnvelopeTurnCeiling > 0 && p.MaxTurns > p.EnvelopeTurnCeiling:
		return fmt.Errorf("%w: the loop asks for max_turns=%d%s and its harness envelope's turn_ceiling "+
			"is %d. The ceiling is imposed and the turn count is chosen, so either lower max_turns to %d "+
			"or less, or ask whoever owns the envelope to raise turn_ceiling",
			ErrInvalidDefinition, p.MaxTurns, where, p.EnvelopeTurnCeiling, p.EnvelopeTurnCeiling)
	}
	return nil
}

// NodeLabel is the " on node x" suffix an editor refusal carries, exported so a console and this
// package cannot spell it two ways.
//
// Empty for a single-node definition: the pre-P36 sentences stay exactly as they were, and a message
// that gained a node prefix nobody asked for would be a second thing to re-baseline in every test.
func NodeLabel(d Definition, nodeID string) string {
	if !d.MultiNode() {
		return ""
	}
	return " on node " + nodeID
}
