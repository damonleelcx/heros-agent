// Package intent is the closed set of things a person can ask this platform to do.
//
// # Why the set is closed, and why that is enforced structurally
//
// An open text box implies infinity. A refusal therefore has to state the boundary, and the only way a
// stated boundary stays true is if the list a user reads and the table the code dispatches on are the
// same object. Two lists disagree within a quarter, and the copy is always the one that is wrong and
// always the one the user reads.
//
// # The drift this prevents runs in exactly one direction
//
// A surface ships, nobody adds its intent, and the agent quietly cannot reach it. Nothing fails. The
// user asks and gets a REFUSAL — well-formed, polite, and indistinguishable from the surface not
// existing. That is the shape the previous system found after fourteen phases, with nothing going red
// the entire time. So the failure is moved into the build: `intent_test.go` fails when an intent has no
// tier, no surface, or no question.
//
// # 🔴 What the fence cannot catch, stated so nobody trusts it for more than it does
//
// That an intent ROUTES WELL. Set membership is structural; routing accuracy is statistical. A build in
// which every intent is well-formed and `coverage` is recognised one time in three is a green build
// over a broken product. Held-out routing evaluation is a separate obligation (P13) and neither
// substitutes for the other.
package intent

import "sort"

// Intent is a member of the closed set.
type Intent string

// ── Tier A · durable goals ───────────────────────────────────────────────────────────────────────
//
// These four are the only intents that become long-running workflows. "Define the long-running goal" is
// a FILTER: putting a queue, a lease and a checkpoint behind "what happened in that run?" would be
// machinery in front of a database read.
const (
	// Assess — nine-axis report on a subject repository. Read-only, which is why it is the first Tier-A
	// goal to build: it exercises the whole durable stack without needing approval gates first.
	Assess Intent = "assess"
	// Improve — the sentence the whole platform was built around: fix it, and open a pull request.
	// Effect-bearing AND long-running, so it needs every gate the others need plus a spend ceiling.
	Improve Intent = "improve"
	// EvalSet — generate an evaluation set for a subject agent.
	//
	// 🔴 NEW. The previous system had four working generators and a route, but the route was a SUB-route
	// of a workflow, so it never entered the working-surface list and never got an intent. The capability
	// existed and was conversationally unreachable.
	EvalSet Intent = "evalset"
	// Compare — is this version better than the last one? Tier A rather than B because answering
	// honestly may require RUNNING an eval set, which costs money and time.
	Compare Intent = "compare"
)

// ── Tier B · queries over persisted state ────────────────────────────────────────────────────────
//
// Single-turn reads. These are the payoff of "persist all state": because Tier-A runs write everything
// down, answering is a SELECT rather than an agent run.
const (
	Graph         Intent = "graph"
	RunHistory    Intent = "run_history"
	Coverage      Intent = "coverage"
	Context       Intent = "context"
	Memory        Intent = "memory"
	Harness       Intent = "harness"
	Loop          Intent = "loop"
	GraphOrder    Intent = "graph_order"
	PreviewChange Intent = "preview_change"
	// Skills and Tools — 🔴 NEW. Both were configuration dimensions AND assessment axes in the previous
	// system, and neither had an intent. A person asking what tools this node can reach routed to
	// nothing and received a refusal that read exactly like the feature not existing. Two of the six
	// surfaces the product is *named after* were unreachable.
	Skills Intent = "skills"
	Tools  Intent = "tools"
)

// ── Tier C · bounded effects ─────────────────────────────────────────────────────────────────────
const (
	Author Intent = "author"
	// Prompt and Model — 🔴 SPLIT. The previous system had one `prompt_model` intent pointing at one
	// page. That is the same conflation that was already undone once, when `harness` was split into the
	// execution envelope and the iteration policy, because a merged intent sends a spend-ceiling
	// question to a page about reflection prompts. Changing which model a node calls and changing what
	// it is told are different acts with different blast radii, and a conversational surface is where a
	// reader is least equipped to notice they were sent to the wrong place.
	Prompt Intent = "prompt"
	Model  Intent = "model"
	// Deliver — put an approved change into the repository.
	//
	// 🔴 Its QUESTION was corrected, not its tier. It used to read "how does an approved change reach my
	// repository?", which is explanatory — a Tier-B question wearing a Tier-C label. An intent whose
	// wording asks for an explanation and whose tier says it writes to a repository will eventually be
	// implemented as whichever half the reader noticed, and the two are not close.
	Deliver Intent = "deliver"
)

// Tier says what machinery an intent requires. It is the single discriminator: nothing else in the
// system decides durability, so a new intent cannot accidentally inherit a queue.
type Tier string

const (
	// TierGoal — a durable, long-running workflow. Planner/executor split, task DAG, checkpoints,
	// leases, ceilings, and (where effect-bearing) approval gates.
	TierGoal Tier = "goal"
	// TierQuery — a single-turn read of persisted state. No queue, no checkpoint, no lease.
	TierQuery Tier = "query"
	// TierEffect — bounded and effect-bearing. Idempotency key and an approval gate, but it completes
	// within one cycle and is never checkpointed across a wake-up.
	TierEffect Tier = "effect"
)

// Spec is one intent, everything about it in one row.
type Spec struct {
	Intent Intent
	Tier   Tier
	// Axis is the subject-agent axis this intent operates on, empty where the intent is about the
	// platform's own record (a run, a comparison, a delivery) rather than about an axis.
	Axis string
	// Question is the sentence a person actually types. It is DATA rather than documentation: it is
	// what a refusal renders as "here is what this surface can do", so the list a user reads and the
	// table a fence checks cannot drift apart.
	Question string
}

// specs is the closed set: nineteen, in tier order.
var specs = []Spec{
	// Tier A
	{Assess, TierGoal, "", "look at my repository and tell me what is weak"},
	{Improve, TierGoal, "", "fix it, and open a pull request"},
	{EvalSet, TierGoal, "", "build me an evaluation set for this agent"},
	{Compare, TierGoal, "", "is this version better than the last one?"},
	// Tier B
	{Graph, TierQuery, "graph", "what does my agent actually do, step by step?"},
	{RunHistory, TierQuery, "", "what happened in that run?"},
	{Coverage, TierQuery, "", "what did you measure, and what did you not?"},
	{Context, TierQuery, "context", "what conversation history does this node get?"},
	{Memory, TierQuery, "memory", "what does this node remember between calls?"},
	{Harness, TierQuery, "harness", "what is this node allowed to do — what can it reach, and what can it spend?"},
	{Loop, TierQuery, "loop", "how many turns does it take, and in what loop?"},
	{GraphOrder, TierQuery, "graph", "should these nodes run in this order?"},
	{PreviewChange, TierQuery, "", "what exactly would you write into my source?"},
	{Skills, TierQuery, "skills", "what skills are bound at this call site?"},
	{Tools, TierQuery, "tools", "what tools is this node offered, and which does it actually call?"},
	// Tier C
	{Author, TierEffect, "", "change something on an axis and show me the diff"},
	{Prompt, TierEffect, "prompt", "change what this node is told"},
	{Model, TierEffect, "model", "change which model this node calls"},
	{Deliver, TierEffect, "", "put an approved change into my repository"},
}

// Axes are the nine surfaces of a SUBJECT agent.
//
// 🔴 This is the noun dictionary. An axis is spelled here exactly as every other layer spells it,
// because a report that says `context` where an editor says `context assembly` makes the reader perform
// a translation the platform should have performed.
//
// 🚫 `graph` is a property BETWEEN nodes; the other eight are properties of ONE node. That asymmetry is
// permanent and is why graph can never become a per-node configuration dimension.
func Axes() []string {
	return []string{"model", "prompt", "skills", "context", "tools", "memory", "harness", "loop", "graph"}
}

// All returns the closed set in tier order. A copy, so no caller can widen it.
func All() []Spec { return append([]Spec(nil), specs...) }

// Lookup returns one intent's spec.
func Lookup(i Intent) (Spec, bool) {
	for _, s := range specs {
		if s.Intent == i {
			return s, true
		}
	}
	return Spec{}, false
}

// Valid reports membership.
func (i Intent) Valid() bool { _, ok := Lookup(i); return ok }

// String makes Intent printable in an error.
func (i Intent) String() string { return string(i) }

// InTier returns the intents of one tier, in declaration order.
func InTier(t Tier) []Intent {
	var out []Intent
	for _, s := range specs {
		if s.Tier == t {
			out = append(out, s.Intent)
		}
	}
	return out
}

// Durable reports whether this intent becomes a long-running workflow. The ONE predicate the rest of
// the system asks; nothing else may decide durability for itself.
func (i Intent) Durable() bool {
	s, ok := Lookup(i)
	return ok && s.Tier == TierGoal
}

// EffectBearing reports whether serving this intent can change something outside the platform.
//
// 🔴 Tier A `improve` is effect-bearing too — it opens a pull request. Durability and effect are
// INDEPENDENT properties, and a system that treats "long-running" as a proxy for "safe" will eventually
// let a durable goal write to a repository without a gate.
func (i Intent) EffectBearing() bool {
	s, ok := Lookup(i)
	if !ok {
		return false
	}
	return s.Tier == TierEffect || s.Intent == Improve
}

// CanDo is the finite list a refusal renders.
//
// 🔴 Generated from the table rather than written as copy. A hand-written list of nineteen sentences
// beside a table of nineteen intents is two lists that will disagree.
func CanDo() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Question)
	}
	return out
}

// ── Out of scope, by name ────────────────────────────────────────────────────────────────────────

// OutOfScope is a thing people will reasonably ask this surface to do that it will not do.
//
// # Why these are enumerated rather than left to an abstention
//
// An abstention says "I cannot route that". These say "that is done at /app/billing". The difference is
// the whole point: account, billing and identity are SURFACES, not agent goals, and an agent that
// offers to change a plan or a password has crossed from answering about a system to administering an
// account. A person has a next action either way; naming the surface is the one that does not make them
// go looking for it.
type OutOfScopeTopic struct {
	Topic   string
	Surface string
	Does    string
}

var outOfScope = []OutOfScopeTopic{
	{"plan", "/app/billing", "changing your plan and seeing what it includes"},
	{"payment", "/app/billing", "payment methods and invoices"},
	{"invoice", "/app/billing", "payment methods and invoices"},
	{"password", "/app/account", "your password and your sign-in details"},
	{"account", "/app/account", "your account details"},
	{"member", "/app/settings/members", "who is in this organization and what they may do"},
	{"invite", "/app/settings/members", "inviting people and managing their access"},
	{"api key", "/app/settings/members", "credentials for this organization"},
	// 🔴 Repository connection is out of scope for a SECURITY reason, not because it is administration.
	//
	// Connecting a repository creates a STANDING READ GRANT — a credential the platform uses when the
	// customer is not present — and the disclosure must be DISPLAYED before authorization completes. An
	// agent that could act on "connect my repo" would create that grant from a sentence, which is
	// exactly the path around the consent screen the requirement exists to close.
	//
	// Revoking is here for the mirror reason: it is safe to want and destructive to get wrong, and its
	// confirmation states what will be deleted. A conversational shortcut past a destructive
	// confirmation is the same mistake pointed the other way.
	{"connect a repository", "/app/connections", "connecting a repository, and revoking a connection"},
	{"revoke", "/app/connections", "revoking a connection and deleting the trees derived from it"},
}

// OutOfScopeTopics returns the enumerated redirections. A copy.
func OutOfScopeTopics() []OutOfScopeTopic {
	return append([]OutOfScopeTopic(nil), outOfScope...)
}

// OutOfScopeSurfaces returns the distinct surfaces the redirections name, sorted.
func OutOfScopeSurfaces() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range outOfScope {
		if !seen[t.Surface] {
			seen[t.Surface] = true
			out = append(out, t.Surface)
		}
	}
	sort.Strings(out)
	return out
}
