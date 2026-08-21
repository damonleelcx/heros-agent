package conversation

import "fmt"

// effects.go is the D7 table: which message kinds cause an effect, and which typed artifact each one
// requires before it may be emitted (task 1.4).
//
// # Why this is a TABLE and not three checks scattered across three constructors
//
// So a reviewer can CHECK THE LIST rather than reconstruct it. The security property this file carries
// is "a fully compromised model can still only produce text, and text is not a ledger row", and a
// reviewer asked to confirm that has to answer one question: *for every kind that can cause an effect,
// what artifact does the platform require, and can a model mint it?* If the answer lives in three
// constructors, confirming it means reading three constructors and trusting that there is no fourth.
// Here it is one table, and `TestEveryEffectBearingKindRequiresAnArtifact` fails if a kind gains an
// effect without gaining a required artifact.
//
// # 🔴 Why this is the defence that does not depend on detection working
//
// Injection detection is a classifier and classifiers have a false-negative rate. If detection were the
// only defence, the system would be secure at exactly the rate the classifier is accurate. Under this
// table it does not matter whether the classifier noticed: a model can emit text that looks exactly
// like a proposal, including a well-formed `proposal_id`, and the emitter still refuses it because the
// id does not RESOLVE. Resolving is a database read the model has no path to influence.
//
// This is why §6.3's fence runs with injection detection deliberately disabled. A fence that only
// passes with the classifier on is testing the classifier, not this.

// Artifact names the typed thing the platform must resolve before an effect-bearing kind is sayable.
//
// It is a name rather than an interface because the three resolutions have nothing in common at the
// type level — one reads the verification ledger, one reads the delivery record, one reads the approval
// gate's entitlement decision — and an interface unifying them would be an abstraction invented to make
// a table look tidy. The table's job is to say WHAT is required; `Emitter` wires each name to the
// resolver that answers it.
type Artifact string

const (
	// ArtifactProposalID — a `proposal_id` that exists in the verification ledger. 🚫 A model cannot
	// mint one: the id is assigned when a proposal is written by the platform's own generation pass,
	// and resolving it is a read of that row.
	ArtifactProposalID Artifact = "proposal_id"
	// ArtifactDeliveryRecord — a delivery record that exists. A pull-request URL is carried ONLY when
	// the delivery record carries it, so the URL in the message is the URL the forge returned rather
	// than one composed from a repository name.
	ArtifactDeliveryRecord Artifact = "delivery_record"
	// ArtifactEntitlementDecision — the approval gate's own answer about whether this tenant's plan and
	// automation level permit the action. Required so an `approval_request` arrives already
	// un-approvable when it must (FR9), rather than rendering a control that fails on click.
	ArtifactEntitlementDecision Artifact = "entitlement_decision"
)

// effectArtifacts is the table. Keyed by kind; a kind absent from it causes no effect.
//
// 🔴 THREE ENTRIES, and the three are exactly PRD NFR-S2's list. Adding a fourth effect-bearing kind
// means adding a row here AND an artifact resolver on Emitter — the fence in effects_test.go fails
// otherwise, which is the point: the failure mode being prevented is a kind that acquires an effect
// while nobody notices it needs an artifact.
var effectArtifacts = map[Kind]Artifact{
	KindProposal:        ArtifactProposalID,
	KindApprovalRequest: ArtifactEntitlementDecision,
	KindResult:          ArtifactDeliveryRecord,
}

// EffectBearing reports whether this kind can cause an effect and therefore requires an artifact.
func EffectBearing(k Kind) bool {
	_, ok := effectArtifacts[k]
	return ok
}

// EffectArtifact returns the artifact this kind requires, and whether it requires one at all.
func EffectArtifact(k Kind) (Artifact, bool) {
	a, ok := effectArtifacts[k]
	return a, ok
}

// EffectBearingKinds returns the effect-bearing kinds in vocabulary order. Ordered rather than map-
// ordered so a test failure and a doc generated from it read the same way twice.
func EffectBearingKinds() []Kind {
	out := make([]Kind, 0, len(effectArtifacts))
	for _, k := range kinds {
		if EffectBearing(k) {
			out = append(out, k)
		}
	}
	return out
}

// ArtifactResolver answers, for one artifact kind, whether a specific reference resolves.
//
// # Why it returns an error rather than a bool
//
// Two failures are possible and they are not the same: the reference does not exist (refuse the
// message — this is the security case), and the store could not be reached (refuse the message AND say
// so differently, because an operator's next action is to fix a database, not to investigate an
// injection). A bool collapses them, and the collapse is dangerous in the safe-looking direction: a
// store outage would read as "no proposal has ever resolved", which is silently correct behaviour with
// an entirely wrong explanation.
type ArtifactResolver interface {
	// Resolve reports whether ref names a real artifact of this kind, owned by this tenant.
	//
	// 🔴 tenantID is a parameter rather than ambient because a resolver that ignored it would let a
	// well-formed id belonging to ANOTHER tenant satisfy the check — a cross-tenant read reached
	// through a message payload, which is the worst shape this table could fail in.
	Resolve(tenantID string, ref string) (bool, error)
}

// ErrArtifactMissing is returned by the emitter when an effect-bearing message names a reference that
// does not resolve. 🔴 Its text names the KIND and the ARTIFACT and never the reference itself: the
// reference came from somewhere, and echoing an unresolvable one into a log is how repository content
// reaches an operator's terminal.
type ErrArtifactMissing struct {
	Kind     Kind
	Artifact Artifact
}

func (e ErrArtifactMissing) Error() string {
	return fmt.Sprintf("conversation: a %s requires a %s that resolves; this one does not", e.Kind, e.Artifact)
}

// ErrNoResolver is returned when an effect-bearing kind is emitted on an emitter that was given no
// resolver for its artifact.
//
// 🔴 It FAILS rather than skipping the check. An emitter with a missing resolver that let the message
// through would turn a deployment wiring mistake into a silently disabled security control — the
// "green fence over nothing" shape. A deployment that cannot resolve proposals must not be able to emit
// proposals.
type ErrNoResolver struct {
	Kind     Kind
	Artifact Artifact
}

func (e ErrNoResolver) Error() string {
	return fmt.Sprintf("conversation: this deployment can emit no %s — nothing resolves a %s here", e.Kind, e.Artifact)
}
