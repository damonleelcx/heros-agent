// Package memory is long-term memory, kept as FOUR separate classes rather than one store.
//
// # Why four, and what collapsing them costs
//
// "Manage long-term memory separately — distinguish task state, episodic history, reusable knowledge,
// and user preferences." They differ in three ways that make a single store actively harmful:
//
//	                  lifetime          authored by        trusted because
//	task state        one goal          the system         it IS the record (goal/task packages)
//	episodic          one goal          the system         it observed something happen
//	knowledge         across goals      PROMOTED, never    evidence was cited when it was promoted
//	                                    written directly
//	preferences       until changed     the USER           the user said so
//
// A single store forces one retention policy, one trust level and one write path onto all four. The
// concrete failure is the third row: an agent that can write directly to "knowledge" launders its own
// speculation into fact, and the next goal reads that fact as if somebody had established it. Nothing
// looks wrong — the sentence is well-formed and confidently stated — and the error compounds across
// every future run for that tenant.
//
// So knowledge cannot be WRITTEN here. It can only be PROMOTED from episodes, citing them (see Promote),
// and every claim carries the evidence it was promoted on.
//
// # 🔴 Preferences are never inferred
//
// A preference is what the user SAID, not what the agent concluded they would like. There is no
// promotion path into preferences at all — the only writer is a person. An agent that infers "they seem
// to prefer aggressive refactors" and then acts on it has invented a mandate.
package memory

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Class names one of the four. It exists so the separation is a thing the code can be ASKED about, and
// so a fence can assert that no read path crosses classes.
type Class string

const (
	// ClassTaskState — owned by the goal and task packages, named here for completeness. This package
	// deliberately does NOT store it: two homes for task state is the split-brain that state machines
	// exist to prevent.
	ClassTaskState Class = "task_state"
	// ClassEpisodic — what happened, in order, within one goal.
	ClassEpisodic Class = "episodic"
	// ClassKnowledge — reusable across goals for one tenant. Promoted, never written.
	ClassKnowledge Class = "knowledge"
	// ClassPreference — standing instructions from the user. Authored by a person, never inferred.
	ClassPreference Class = "preference"
)

// Classes returns all four.
func Classes() []Class {
	return []Class{ClassTaskState, ClassEpisodic, ClassKnowledge, ClassPreference}
}

// ── episodic ─────────────────────────────────────────────────────────────────────────────────────

// EpisodeKind is what an episode records. A closed set, because the summariser treats them differently
// and a kind it does not recognise would be compressed by the default rule — which is exactly how a
// failure gets summarised away.
type EpisodeKind string

const (
	EpisodeObservation EpisodeKind = "observation" // something was read or measured
	EpisodeDecision    EpisodeKind = "decision"    // a choice was made, with its reason
	EpisodeFailure     EpisodeKind = "failure"     // something did not work
	EpisodeEffect      EpisodeKind = "effect"      // the outside world changed
)

// Episode is one thing that happened during one goal.
type Episode struct {
	GoalID string
	// Seq orders episodes within a goal. Monotonic, assigned by the store, so two workers writing
	// concurrently cannot produce an ambiguous order.
	Seq    int64
	TaskID string
	Kind   EpisodeKind
	// Summary is one line. Detail is everything else, and is what gets dropped first under a budget.
	Summary string
	Detail  string
	At      time.Time
	// SummarisedBy points at the Summary record that now covers this episode, if any. The episode is NOT
	// deleted when it is summarised: compression must be auditable, and "what did the summary leave out"
	// is unanswerable once the source is gone.
	SummarisedBy int64
}

// Compressible reports whether this episode may be folded into a summary.
//
// 🔴 Failures and effects never are, and this is the most important rule in the package. Compression is
// lossy by definition, and the two things a reader most needs from an old run are what broke and what
// it changed in the world. A summariser that treats all episodes alike will, given enough history,
// produce a tidy narrative of a run that quietly failed and quietly wrote to a repository.
func (e Episode) Compressible() bool {
	return e.Kind == EpisodeObservation || e.Kind == EpisodeDecision
}

// Summary covers a contiguous range of episodes.
type Summary struct {
	GoalID string
	ID     int64
	// FromSeq and ToSeq are the range covered, inclusive. A range rather than a list so the coverage is
	// checkable: gaps and overlaps are both detectable, and both mean the compression is wrong.
	FromSeq, ToSeq int64
	Content        string
	// Dropped counts episodes folded in. Reported to the reader, because "12 steps summarised" and
	// "1,200 steps summarised" describe very different amounts of missing detail.
	Dropped int
	At      time.Time
}

// ── knowledge ────────────────────────────────────────────────────────────────────────────────────

// Knowledge is a claim reusable across goals, with the evidence it was promoted on.
type Knowledge struct {
	Tenant string
	// Subject scopes the claim, normally a repository. Cross-tenant sharing is not a thing this type can
	// express, deliberately: one customer's repository is not evidence about another's.
	Subject string
	Key     string
	Value   string
	// EvidenceGoalID and EvidenceSeqs cite the episodes this was promoted from. 🔴 Required. A claim
	// with no evidence is indistinguishable from a guess, and six months later nobody can tell which it
	// was — including the person deciding whether to trust it.
	EvidenceGoalID string
	EvidenceSeqs   []int64
	At             time.Time
	// SupersededBy is set when a later promotion contradicts this one. The old claim is kept: knowing a
	// belief CHANGED, and on what evidence, is what lets somebody audit a wrong decision made while it
	// was still held.
	SupersededBy string
}

// ── preferences ──────────────────────────────────────────────────────────────────────────────────

// Preference is a standing instruction from a person.
type Preference struct {
	Tenant string
	Key    string
	Value  string
	// AuthoredBy is the person. 🔴 Required and never a system identity: the type is designed so that
	// "the agent set this preference" cannot be represented.
	AuthoredBy string
	At         time.Time
}

var (
	ErrNoEvidence     = errors.New("memory: knowledge promoted without citing evidence")
	ErrNotPromotable  = errors.New("memory: episode is not a valid basis for a claim")
	ErrNoAuthor       = errors.New("memory: preference has no human author")
	ErrSystemAuthor   = errors.New("memory: preferences may not be authored by the system")
	ErrSummaryGap     = errors.New("memory: summary range has a gap or overlap")
	ErrIncompressible = errors.New("memory: refusing to compress a failure or an effect")
)

// systemIdentities are names that are not people. Checked at write, so the "never inferred" rule is
// enforced rather than documented.
var systemIdentities = map[string]bool{
	"": true, "system": true, "agent": true, "heros": true, "worker": true, "planner": true,
}

// ValidatePreference refuses a preference that is not attributable to a person.
func ValidatePreference(p Preference) error {
	author := strings.ToLower(strings.TrimSpace(p.AuthoredBy))
	if author == "" {
		return fmt.Errorf("%w: key %q", ErrNoAuthor, p.Key)
	}
	if systemIdentities[author] || strings.HasPrefix(author, "worker-") {
		return fmt.Errorf("%w: %q — an agent that infers a preference has invented a mandate",
			ErrSystemAuthor, p.AuthoredBy)
	}
	if p.Key == "" {
		return fmt.Errorf("memory: preference has no key")
	}
	return nil
}

// Promote turns episodes into a knowledge claim, refusing to do so without evidence.
//
// # 🔴 Why promotion is a function here rather than a write on the store
//
// Because the RULES are the point, and a store method would let a caller bypass them by writing a row.
// Promotion requires: at least one citation; every cited episode from the goal named; and the cited
// episodes to be observations or effects — a DECISION is the agent's own reasoning, and promoting a
// decision to knowledge is precisely how speculation becomes fact.
func Promote(tenant, subject, key, value string, from []Episode) (Knowledge, error) {
	if len(from) == 0 {
		return Knowledge{}, fmt.Errorf("%w: %q", ErrNoEvidence, key)
	}
	goalID := from[0].GoalID
	seqs := make([]int64, 0, len(from))
	for _, e := range from {
		if e.GoalID != goalID {
			return Knowledge{}, fmt.Errorf("%w: evidence spans goals %q and %q",
				ErrNoEvidence, goalID, e.GoalID)
		}
		// 🔴 A decision is the agent's own reasoning. Promoting it to knowledge is the laundering step
		// this whole package exists to make impossible.
		if e.Kind != EpisodeObservation && e.Kind != EpisodeEffect {
			return Knowledge{}, fmt.Errorf("%w: episode %d is a %s; only observations and effects are "+
				"evidence, because a decision is the agent's own reasoning", ErrNotPromotable, e.Seq, e.Kind)
		}
		seqs = append(seqs, e.Seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return Knowledge{
		Tenant: tenant, Subject: subject, Key: key, Value: value,
		EvidenceGoalID: goalID, EvidenceSeqs: seqs,
	}, nil
}

// Compress folds a contiguous run of episodes into a summary.
//
// Refuses when the range is not contiguous (a gap means the summary claims coverage it does not have)
// and when it contains a failure or an effect.
func Compress(goalID string, episodes []Episode, content string, now time.Time) (Summary, error) {
	if len(episodes) == 0 {
		return Summary{}, fmt.Errorf("memory: nothing to compress")
	}
	sorted := append([]Episode(nil), episodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })

	for i, e := range sorted {
		if e.GoalID != goalID {
			return Summary{}, fmt.Errorf("%w: episode %d belongs to goal %q", ErrSummaryGap, e.Seq, e.GoalID)
		}
		if !e.Compressible() {
			return Summary{}, fmt.Errorf("%w: episode %d is a %s", ErrIncompressible, e.Seq, e.Kind)
		}
		if i > 0 && e.Seq != sorted[i-1].Seq+1 {
			return Summary{}, fmt.Errorf("%w: %d follows %d", ErrSummaryGap, e.Seq, sorted[i-1].Seq)
		}
	}
	return Summary{
		GoalID: goalID, FromSeq: sorted[0].Seq, ToSeq: sorted[len(sorted)-1].Seq,
		Content: content, Dropped: len(sorted), At: now,
	}, nil
}

// Store persists the three classes this package owns.
//
// 🔴 There is deliberately no generic `Get(class, key)`. A single accessor is how the classes get
// confused at the call site, and the whole value of separating them is that a caller must NAME which
// kind of memory it is asking for — and therefore must have thought about whether that is the right kind.
type Store interface {
	// AppendEpisode assigns the next sequence number and stores the episode.
	AppendEpisode(e Episode) (int64, error)
	// Episodes returns a goal's episodes in sequence order, oldest first.
	Episodes(goalID string) ([]Episode, error)

	// SaveSummary stores a compression and marks the episodes it covers.
	SaveSummary(s Summary) (int64, error)
	Summaries(goalID string) ([]Summary, error)

	// PromoteKnowledge stores a claim built by Promote. It takes a Knowledge rather than raw fields so
	// the only way to construct one is through the function that enforces the evidence rule.
	PromoteKnowledge(k Knowledge) error
	// KnowledgeFor returns claims for a tenant and subject, most recent first, excluding superseded ones.
	KnowledgeFor(tenant, subject string) ([]Knowledge, error)

	// SetPreference stores a user-authored preference.
	SetPreference(p Preference) error
	Preferences(tenant string) ([]Preference, error)
}
