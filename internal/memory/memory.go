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

// ── conversation ─────────────────────────────────────────────────────────────────────────────────

// TurnRole is who spoke. A closed set of two: there is no third party to a conversation, and a role
// the renderer does not recognise would be drawn as whichever side the default happens to be.
type TurnRole string

const (
	TurnUser  TurnRole = "user"
	TurnAgent TurnRole = "agent"
)

// Valid reports membership. Checked at write, so the database CHECK and the Go type agree by
// construction rather than by both being remembered.
func (r TurnRole) Valid() bool { return r == TurnUser || r == TurnAgent }

// Decider says HOW an agent turn was produced.
//
// # 🔴 Why this is recorded rather than inferred
//
// Understanding is a model call now, and a model call can fail — rate limited, timed out, or answering
// with JSON that will not parse. The product requirement is that the console keeps working by falling
// back to the deterministic keyword router, which means two turns that read identically in the
// transcript may have come from two completely different mechanisms.
//
// Without this, "why did it answer that?" is unanswerable, and an evaluation that cannot exclude
// degraded turns is measuring a population it did not intend to measure.
type Decider string

const (
	// DecidedByModel — the agent loop ran and chose this reply.
	DecidedByModel Decider = "model"
	// DecidedByFallback — the model was unavailable, so the deterministic router decided. The reply is
	// today's keyword behaviour, and the person is told the surface is degraded.
	DecidedByFallback Decider = "keyword-fallback"
	// DecidedByFloor — the deterministic safety floor answered before any model was consulted: an
	// unbounded request, or a topic that belongs to another surface. 🔴 These never reach the model by
	// design; see internal/converse.
	DecidedByFloor Decider = "floor"
)

// Turn is one utterance in one conversation.
type Turn struct {
	Tenant         string
	ConversationID string
	// Seq orders turns within a conversation. Assigned by the store, never by the caller: two tabs
	// posting at once would otherwise choose the same number.
	Seq  int64
	Role TurnRole
	Body string
	// Kind mirrors the API response shape ("say", "answer", "goal", "confirm", …) so a replayed
	// transcript renders as what the person originally saw rather than as flat text that drops the
	// cards. Empty on a user turn, which has no shape but its words.
	Kind string
	// Capability is the named intent this turn resolved to, empty when the agent simply talked.
	Capability string
	Decided    Decider
	// CostMicroCents is what this turn spent. 🔴 Micro-cents, matching provider.MicroCentsPerCent: a
	// turn costs a small fraction of a cent, and a ledger in whole cents records every one as zero.
	CostMicroCents int64
	At             time.Time
}

// ConversationSummary is one thread as a list entry: enough to draw a row in the console's session
// rail, and nothing more.
//
// # 🔴 Why there is no conversations table and no stored title
//
// `conversation_turns` already holds every fact this struct carries. A title column would be a second
// place the name of a thread lives, written once at creation and never corrected when the first
// sentence turns out not to be what the conversation was about — and a separate table would need an
// owner row created before the first turn, which is a write that can fail and leave a thread nobody
// can list. Deriving both from the turns means a conversation exists exactly when somebody has said
// something in it, which is the only definition that cannot drift.
//
// The price is a GROUP BY per listing instead of a point read. Paid knowingly: the rail lists one
// organization's threads, and a tenant with enough conversations for that to matter has a product
// problem this struct would not have solved.
type ConversationSummary struct {
	ID string
	// Title is the FIRST thing the person said, truncated. Empty when a thread somehow holds only agent
	// turns — rendered by the console as "untitled", never as a blank row.
	Title string
	// Turns is how many utterances the thread holds, both roles counted.
	Turns int
	// LastAt is the most recent turn's timestamp — what the list is ordered by.
	LastAt time.Time
}

// TitleLimit is how much of the opening sentence becomes a thread's title.
//
// Truncated in the STORE rather than in the browser so both legs agree on one answer and the console
// cannot be the thing that decides it. A rail row is one line wide; sending a 4KB opening paragraph so
// that CSS can hide all but forty characters of it is a listing that gets slower the more somebody
// types.
const TitleLimit = 80

var (
	ErrNoConversation = errors.New("memory: turn has no conversation")
	ErrBadTurnRole    = errors.New("memory: turn has an unknown role")

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

// ValidateTurn refuses a turn that cannot be ordered or attributed.
//
// 🔴 An empty conversation id is refused rather than defaulted to something like "default". A shared
// implicit conversation is one where every tenant's tabs append to the same thread, and the failure is
// silent: the transcript simply grows sentences nobody in this browser typed.
func ValidateTurn(t Turn) error {
	if strings.TrimSpace(t.ConversationID) == "" {
		return ErrNoConversation
	}
	if !t.Role.Valid() {
		return fmt.Errorf("%w: %q — a turn is spoken by %q or %q", ErrBadTurnRole, t.Role,
			TurnUser, TurnAgent)
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
// Root hands out tenant-scoped stores. It is the ONLY way to obtain a Store.
//
// # 🔴 Why this exists, given that nothing leaks today
//
// The four episodic methods below take a goal id and nothing else. A goal id is therefore sufficient to
// read — or write — any customer's history, and the only thing preventing it is that both call sites
// happen to reach them with an id already proven to belong to the caller. That is handler discipline,
// which is the exact property `store.Root` was introduced to stop relying on, in a package whose methods
// have the same shape and for the same reason.
//
// `For` closes over the tenant and every query it produces carries it. A handler is given a scoped store
// and never holds the root, so it cannot construct a query for another tenant: not because it is
// careful, but because it has nothing to be careless with.
//
// 🔴 The write path matters most here. `worker.record` is fire-and-forget (`_, _ =`), so a scoping
// mistake on the way IN would never surface as an error — it would surface as one customer's run
// narrated into another's timeline. Appending to a goal that is not yours is refused, not absorbed.
type Root interface {
	// For returns a store bound to one tenant. An empty tenant is refused rather than treated as "all",
	// because "all" is the value an unset variable has.
	For(tenant string) Store
}

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
	//
	// 🔴 On a scoped store the tenant argument is IGNORED in favour of the one the store is bound to.
	// The parameter is kept rather than removed so that call sites do not churn — the same choice
	// `store.LatestGoal(_ string)` made, and fenced the same way by a test that passes somebody else's
	// tenant and asserts it changes nothing.
	KnowledgeFor(tenant, subject string) ([]Knowledge, error)

	// SetPreference stores a user-authored preference.
	SetPreference(p Preference) error
	// Preferences returns a tenant's preferences. On a scoped store the argument is ignored, as with
	// KnowledgeFor.
	Preferences(tenant string) ([]Preference, error)

	// AppendTurn assigns the next sequence number within the conversation and stores the turn.
	AppendTurn(t Turn) (int64, error)
	// Turns returns one conversation's turns in sequence order, oldest first. On a scoped store the
	// tenant argument is ignored, as with KnowledgeFor and Preferences.
	//
	// 🔴 A conversation id does NOT imply a tenant the way a goal id does. Postgres could resolve one
	// through `goals`, but there is no conversation owner table to join and the in-memory store has
	// nothing to ask at all — so the tenant travels with the call and the scoped view overrides it.
	Turns(tenant, conversationID string) ([]Turn, error)
	// LatestConversation returns the id of the conversation this tenant spoke in most recently, so a
	// reconnecting browser can resume rather than silently start a second thread beside the first.
	//
	// 🔴 Its own method rather than "read all turns and sort": the alternative reads every turn this
	// tenant has ever spoken in order to discard all but one of them.
	LatestConversation(tenant string) (string, bool, error)
	// Conversations lists every thread this tenant has spoken in, most recently active first. On a
	// scoped store the tenant argument is ignored, as with Turns.
	//
	// 🔴 Ordered by (LastAt DESC, ID DESC) on BOTH implementations, and the tie-break is not decoration.
	// Turns written in one test run share a wall-clock instant at this resolution — the reason
	// Mem.LatestConversation answers from write order rather than from timestamps at all — so ordering
	// on LastAt alone lets the two legs disagree about threads that are genuinely simultaneous, and the
	// conformance suite would catch it as a flake rather than as a fact. The second key makes the answer
	// total. It is the same tie-break PG.LatestConversation already uses.
	Conversations(tenant string) ([]ConversationSummary, error)
}
