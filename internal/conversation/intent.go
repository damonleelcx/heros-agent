package conversation

import "sort"

// intent.go is D9: **the intent set IS the set of working surfaces**, and a fence asserts it.
//
// # The drift this prevents, and why it is invisible without a fence
//
// Without it the two sets drift in exactly one direction: a surface ships, nobody adds its intent, and
// the conversation quietly cannot reach it. Nothing fails. The user asks about the new surface and gets
// a REFUSAL — well-formed, polite, and indistinguishable from the surface not existing. This is the
// shape P26 found after fourteen phases of operator-console drift, where the drift happened with
// nothing going red the entire time.
//
// So the failure is moved into the build: an intent with no surface and a surface with no intent both
// fail `TestIntentSetEqualsTheWorkingSurfaceSet` and its console-side counterpart, named, before a
// customer ever meets the refusal.
//
// # 🔴 What the fence cannot catch, stated so nobody trusts it for more than it does
//
// That the intent ROUTES WELL. Set equality is structural; recall is statistical. §3's per-intent recall
// is what covers that, and neither fence substitutes for the other — a build in which every intent has
// a surface and `coverage` is routed correctly one time in three is a green build over a broken product.
//
// # Why two backings and not one
//
// Twelve intents name a console ROUTE that exists today. Two — `assess` and `improve` — name a
// CAPABILITY (P33's assessment, P35's improvement run) that is mounted or not per deployment. Both are
// working surfaces in FR24's sense; they differ in what "exists" means, and collapsing them would force
// either a fake route for a capability or an exemption in the fence. An exemption in a fence is how a
// fence stops being one.

// Intent is a member of the closed intent set (PRD FR24).
type Intent string

const (
	IntentGraph         Intent = "graph"
	IntentRunHistory    Intent = "run_history"
	IntentCompare       Intent = "compare"
	IntentPreviewChange Intent = "preview_change"
	IntentDeliver       Intent = "deliver"
	IntentPromptModel   Intent = "prompt_model"
	IntentAuthor        Intent = "author"
	IntentGraphOrder    Intent = "graph_order"
	IntentContext       Intent = "context"
	IntentMemory        Intent = "memory"
	IntentHarness       Intent = "harness"
	IntentCoverage      Intent = "coverage"
	IntentAssess        Intent = "assess"
	IntentImprove       Intent = "improve"
)

// Backing says what an intent resolves to.
type Backing string

const (
	// BackedByRoute — the intent names a console route that exists in the route table today.
	BackedByRoute Backing = "route"
	// BackedByCapability — the intent names a platform capability that a deployment mounts or does
	// not. An unmounted capability answers with the `not_mounted` failure class, never a 404.
	BackedByCapability Backing = "capability"
)

// IntentSpec is one intent, everything about it in one row.
type IntentSpec struct {
	Intent Intent
	// Backing distinguishes the two kinds of "working surface".
	Backing Backing
	// Surface is the console route (BackedByRoute) or the capability name (BackedByCapability).
	Surface string
	// Capability is the P33/P35 capability this intent reaches, empty for route-backed intents.
	// Task 3.1's "mapping to P33/P35 capabilities", written as a field so it can be read rather than
	// inferred from a phase number in a comment.
	Capability string
	// Question is the sentence a person actually types, verbatim from PRD §6.7. It is DATA rather than
	// documentation: it is what the refusal's "here is what this surface can do" list renders, so the
	// list a user sees and the table a fence checks cannot drift apart.
	Question string
}

// intents is the closed set. 🔴 Fourteen, in PRD §6.7's order.
var intents = []IntentSpec{
	{IntentGraph, BackedByRoute, "/app/workflows", "", "what does my agent actually do, step by step?"},
	{IntentRunHistory, BackedByRoute, "/app/runs", "", "what happened in that run?"},
	{IntentCompare, BackedByRoute, "/app/variants", "", "is this version better than the last one?"},
	{IntentPreviewChange, BackedByRoute, "/app/transforms", "", "what exactly would you write into my source?"},
	{IntentDeliver, BackedByRoute, "/app/delivery", "", "how does an approved change reach my repository?"},
	{IntentPromptModel, BackedByRoute, "/app/studio", "", "change the instruction / change the model"},
	{IntentAuthor, BackedByRoute, "/app/authoring", "", "change something on an axis and show me the diff"},
	{IntentGraphOrder, BackedByRoute, "/app/wiring", "", "should these nodes run in this order?"},
	{IntentContext, BackedByRoute, "/app/context", "", "what conversation history does this node get?"},
	{IntentMemory, BackedByRoute, "/app/memory", "", "what does this node remember between calls?"},
	{IntentHarness, BackedByRoute, "/app/harness", "", "how many turns does it take, and in what loop?"},
	{IntentCoverage, BackedByRoute, "/app/coverage", "", "what did you measure, and what did you not?"},
	{IntentAssess, BackedByCapability, "surface-assessment", "surface-assessment",
		"look at my repository and tell me what is weak"},
	{IntentImprove, BackedByCapability, "autonomous-improvement-run", "autonomous-improvement-run",
		"fix it, and open a pull request"},
}

// Intents returns the closed set in PRD order. A copy.
func Intents() []IntentSpec { return append([]IntentSpec(nil), intents...) }

// Lookup returns one intent's spec.
func Lookup(i Intent) (IntentSpec, bool) {
	for _, s := range intents {
		if s.Intent == i {
			return s, true
		}
	}
	return IntentSpec{}, false
}

// Valid reports membership.
func (i Intent) Valid() bool { _, ok := Lookup(i); return ok }

// String makes Intent printable in an error.
func (i Intent) String() string { return string(i) }

// RouteBackedSurfaces returns the console routes the intent table names, sorted. This is one half of
// the set-equality fence; the other half is the console's own declared working-surface list.
func RouteBackedSurfaces() []string {
	var out []string
	for _, s := range intents {
		if s.Backing == BackedByRoute {
			out = append(out, s.Surface)
		}
	}
	sort.Strings(out)
	return out
}

// Capabilities returns the P33/P35 capability names the intent table reaches, sorted.
func Capabilities() []string {
	var out []string
	for _, s := range intents {
		if s.Backing == BackedByCapability {
			out = append(out, s.Capability)
		}
	}
	sort.Strings(out)
	return out
}

// CanDo is the finite list a refusal renders (FR14).
//
// 🔴 It is generated from the intent table rather than written as copy. An open text box implies
// infinity, so the refusal has to state the boundary — and a hand-written list of fourteen sentences
// beside a table of fourteen intents is two lists that will disagree within a quarter, with the copy
// being the one that is wrong and the one the user reads.
func CanDo() []string {
	out := make([]string, 0, len(intents))
	for _, s := range intents {
		out = append(out, s.Question)
	}
	return out
}

// ── Out of scope, by name (FR26) ─────────────────────────────────────────────────────────────────

// OutOfScopeTopic is a thing people will reasonably ask this surface to do that it will not do.
//
// # Why these are enumerated rather than left to the router's abstention
//
// An abstention says "I cannot route that". These say "that is done at /app/billing". The difference is
// the whole of FR26: account, billing and identity are SURFACES, they are not agent goals, and an agent
// that offers to change a plan or a password has crossed from answering about a system to administering
// an account. A person who asked has a next action either way, and naming the surface is the one that
// does not make them go and look for it.
type OutOfScopeTopic struct {
	// Topic is the noun a person used.
	Topic string
	// Surface is where it IS done.
	Surface string
	// Does is what that surface does, so the refusal reads as a redirection rather than a rejection.
	Does string
}

var outOfScope = []OutOfScopeTopic{
	{"plan", "/app/billing", "changing your plan and seeing what it includes"},
	{"payment", "/app/billing", "payment methods and invoices"},
	{"invoice", "/app/billing", "payment methods and invoices"},
	{"password", "/app/account", "your password and your sign-in details"},
	{"account", "/app/account", "your account details"},
	{"member", "/app/settings/members", "who is in this organization and what they may do"},
	{"invite", "/app/settings/members", "inviting people and managing their access"},
	{"access", "/app/settings/members", "who is in this organization and what they may do"},
	{"api key", "/app/settings/members", "credentials for this organization"},
	{"credential", "/app/settings/members", "credentials for this organization"},
	// P32 · repository intake. 🔴 OUT OF SCOPE ON PURPOSE, and this is the one entry here whose reason
	// is not "that is administration rather than analysis".
	//
	// Connecting a repository creates a STANDING READ GRANT — a credential the platform uses when the
	// customer is not present — and FR10 requires the disclosure be DISPLAYED before authorization can
	// complete. An agent that could act on "connect my repo" would be creating that grant from a
	// sentence, which is exactly the path around the consent screen the requirement exists to close.
	// So the agent names the surface and stops.
	//
	// Revoking is here too, and the asymmetry is deliberate: revoking is safe to want and destructive
	// to get wrong (it deletes the derived trees), and its confirmation states what will be deleted.
	// A conversational shortcut past that confirmation is the same class of mistake in the other
	// direction.
	{"connect a repository", "/app/connections", "connecting a repository, and revoking a connection"},
	{"repository access", "/app/connections", "what this platform may read from your repositories, and when it last did"},
	{"revoke", "/app/connections", "revoking a repository connection and deleting the trees derived from it"},
	{"pair", "/app/connections", "reading a repository in place on your own machine"},
}

// OutOfScope returns the enumerated redirections. A copy.
func OutOfScope() []OutOfScopeTopic { return append([]OutOfScopeTopic(nil), outOfScope...) }

// OutOfScopeSurfaces returns the distinct surfaces the redirections name, sorted. Used by the fence
// that asserts every console route is EITHER a working surface OR explicitly out of scope — so adding a
// route is a decision somebody makes rather than a default they inherit.
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
