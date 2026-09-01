// Package autonomy decides how much of a run may proceed without a person.
//
// # 🔴 Why a level per organization, and not trust earned from a track record
//
// The obvious "gradual" design lets autonomy widen on its own: after N proposals approved unedited, stop
// asking for that class of change. It was considered and rejected. A system that grants itself authority
// from its own record is exactly the thing that should need a human, and a run of easy approvals is not
// evidence about the hard change — the twenty diffs somebody waved through were the twenty that were
// obviously fine, which is why they were waved through.
//
// So autonomy is a setting: an owner says how much this organization wants, it is written down, it is
// revocable in one click, and it never moves unless somebody moves it. "Gradual" describes the operator
// turning a dial as their confidence grows, not the software deciding it has earned more room.
package autonomy

import (
	"fmt"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/task"
)

// Level is how much an organization has chosen to allow.
type Level string

const (
	// Supervised gates every effect. The default, and where every organization starts.
	Supervised Level = "supervised"
	// Assisted lets a run change files in its own workspace without asking, and still stops before
	// anything reaches the customer's world.
	Assisted Level = "assisted"
	// Autonomous gates nothing. The run is bounded by its ceilings and by nothing else.
	Autonomous Level = "autonomous"
)

// Levels is every level, least to most permissive. For rendering a menu and for the fence; it is not
// used to decide anything, because ordering is not the mechanism here either.
var Levels = []Level{Supervised, Assisted, Autonomous}

// Class is what kind of change an effect makes.
//
// 🔴 Two classes and not one, because the distinction is the whole point of an intermediate level. "It
// may edit files in the checkout it was given" and "it may push to our repository" are different
// permissions, and an organization that wants the first almost never wants the second on the same day.
type Class string

const (
	// Workspace changes something on this side of the boundary: files in the checkout, an artefact
	// written to our own root. Undone by discarding the workspace.
	Workspace Class = "workspace"
	// Publish changes something in the customer's world that we do not control and cannot take back — a
	// branch pushed to their remote, a pull request opened against their repository.
	Publish Class = "publish"
)

// classOf says which class a task kind belongs to.
//
// 🔴 A TABLE, and every effect-bearing kind must appear in it — asserted by
// TestEveryEffectBearingKindHasAClass, which fails the build if a kind is added without a decision. The
// alternative is a default, and a default here is a default in the PERMISSIVE direction for whichever
// kind somebody forgot: a new "push to production" landing in Workspace and going out unapproved under
// the level an organization chose for editing files.
var classOf = map[string]Class{
	"write_source":      Workspace,
	"publish_eval_set":  Workspace,
	"open_pull_request": Publish,
	"deliver_change":    Publish,
}

// ClassOf returns the class of a kind, and whether it is known.
//
// An unknown effect-bearing kind is reported as unknown rather than guessed at. The caller gates it.
func ClassOf(kind string) (Class, bool) {
	c, ok := classOf[kind]
	return c, ok
}

// mayProceed is the whole model: for each level, which classes go ahead without a person.
//
// Written out exhaustively, including the falses, for the same reason the capability table is: a `false`
// is the record that somebody considered the pair and decided against it, and an absent entry is the
// same decision with nobody's name on it.
var mayProceed = map[Level]map[Class]bool{
	Supervised: {Workspace: false, Publish: false},
	Assisted:   {Workspace: true, Publish: false},
	Autonomous: {Workspace: true, Publish: true},
}

// Valid reports whether a string is a level this build knows. Exact — no trimming, no case folding,
// for the reason learned next door: a validator more permissive than the thing it guards accepts values
// the system then cannot use.
func Valid(s string) bool {
	_, ok := mayProceed[Level(s)]
	return ok
}

// MayProceed reports whether a level lets a class of effect run without a person.
//
// 🔴 Fails closed on anything it does not recognise. An unknown level — a column value from a newer
// build, a typo written by hand — permits nothing, so the failure is "it keeps asking for approval",
// which somebody reports, rather than "it stopped asking", which nobody does.
func MayProceed(l Level, c Class) bool {
	byClass, known := mayProceed[l]
	if !known {
		return false
	}
	return byClass[c]
}

// Describe renders a level for a person choosing one.
func Describe(l Level) string {
	switch l {
	case Supervised:
		return "every change waits for a person, including edits inside the workspace"
	case Assisted:
		return "edits inside the workspace go ahead; anything reaching your repository waits"
	case Autonomous:
		return "nothing waits for a person — runs are bounded only by their ceilings"
	default:
		return "unknown, so nothing proceeds without a person"
	}
}

// Source answers what level an organization has chosen.
//
// An interface so this package does not depend on the identity store, and so the failure mode is
// explicit at the one place it matters — see Policy.
type Source interface {
	AutonomyFor(tenant string) (Level, error)
}

// Policy is the worker's approval policy, driven by the organization's setting.
//
// It satisfies worker.ApprovalPolicy without importing it: the worker declares the interface, this
// provides it, and neither package knows about the other.
type Policy struct{ Source Source }

// NeedsApproval decides whether this task waits for a person, and always says why.
//
// # 🔴 Every failure gates
//
// A lookup that errors, a level nobody recognises, an effect-bearing kind with no class — each one ends
// in "wait for a person". The permissive branch of this function is reached only when the organization's
// choice was read successfully AND it is a level this build knows AND the kind has a declared class. A
// policy that is wrong in the permissive direction is discovered by the customer.
//
// The second return value is populated in BOTH directions. When a task is gated it explains the wait;
// when a task proceeds it explains why nobody was asked — and that sentence is what the worker records,
// so "who approved this?" has an answer even when the answer is "nobody, and here is the setting that
// said so".
func (p Policy) NeedsApproval(g *goal.Goal, t *task.Task) (bool, string) {
	if !task.EffectBearingKinds[t.Kind] {
		return false, ""
	}
	class, known := ClassOf(t.Kind)
	if !known {
		return true, fmt.Sprintf("%q changes something outside the platform and this build has no "+
			"autonomy class for it, so it waits for a person", t.Kind)
	}
	if p.Source == nil {
		return true, fmt.Sprintf("%q waits for a person: no autonomy setting is wired", t.Kind)
	}
	level, err := p.Source.AutonomyFor(g.Tenant)
	if err != nil {
		return true, fmt.Sprintf("%q waits for a person: this organization's autonomy setting could "+
			"not be read (%v)", t.Kind, err)
	}
	if !MayProceed(level, class) {
		return true, fmt.Sprintf("%q is a %s change, and this organization is set to %s — %s",
			t.Kind, class, level, Describe(level))
	}
	return false, fmt.Sprintf("%q is a %s change and this organization is set to %s, so it proceeded "+
		"without asking anybody", t.Kind, class, level)
}
