package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/heros/internal/edit"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/tools"
)

// effects.go serves the Tier-C intents: author, prompt, model, deliver.
//
// # 🔴 Why these run in-turn rather than as durable goals
//
// The tiering says so, and the tiering is right: a bounded change is one model call and a diff. Putting
// a queue, a lease and a checkpoint behind it would add every failure mode of a distributed run to an
// operation that finishes in two seconds, and would make the person wait for a poll to see their own
// diff.
//
// What they DO get is the part that matters: an idempotency key, a human gate, and a verified write.

// pending is a proposal waiting for a person.
type pending struct {
	ID        string
	Proposal  edit.Proposal
	Key       string
	Root      string
	Revision  string
	CreatedAt time.Time
	// Decided guards against a double-approval racing itself. 🔴 An approval is consent to apply a
	// change ONCE; two clicks must not produce two commits.
	Decided bool
}

// approvals holds proposals between proposing and deciding.
//
// In memory on purpose, and stated as a limitation rather than hidden: a restart loses undecided
// proposals, which is correct behaviour for consent — an approval screen a person never answered should
// not survive to be answered by accident later.
type approvals struct {
	mu sync.Mutex
	by map[string]*pending
}

// NewApprovals builds an empty approval store.
func NewApprovals() *approvals { return &approvals{by: map[string]*pending{}} }

func (a *approvals) put(p *pending) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.by[p.ID] = p
}

// take marks a proposal decided and returns it, refusing a second decision on the same one.
func (a *approvals) take(id string) (*pending, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.by[id]
	if !ok {
		return nil, fmt.Errorf("that change is no longer pending; it may have been decided already, or " +
			"the server restarted, which discards undecided proposals")
	}
	if p.Decided {
		return nil, fmt.Errorf("that change has already been decided")
	}
	p.Decided = true
	return p, nil
}

// ── proposing ────────────────────────────────────────────────────────────────────────────────────

// propose delegates to the shared core, so the in-turn path and the improvement run enforce ONE set of
// safety rules. What differs between them is only how the result is delivered.
func (s *Server) propose(ctx context.Context, sub *subjectState, axis, instruction string) (*pending, error) {
	p, _, err := tools.ProposeSpanChange(ctx, s.Provider, s.Model, sub.Index, sub.Source.Root,
		axis, instruction)
	if err != nil {
		return nil, err
	}
	return &pending{
		ID:       fmt.Sprintf("chg-%d", time.Now().UnixNano()),
		Proposal: p, Key: p.IdempotencyKey(sub.Source.Revision),
		Root: sub.Source.Root, Revision: sub.Source.Revision, CreatedAt: time.Now().UTC(),
	}, nil
}

// handleEffect serves author, prompt and model: propose a change and hand back a diff to approve.
func (s *Server) handleEffect(w http.ResponseWriter, spec intent.Spec, sub *subjectState, axis, text string) {
	if axis == "" {
		axis = spec.Axis
	}
	if axis == "" {
		writeJSON(w, 200, askResp{
			Kind: "refusal", Intent: spec.Intent.String(), Cause: "no_axis",
			NextAction: "name which axis to change: " + strings.Join(intent.Axes(), ", "),
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	p, err := s.propose(ctx, sub, axis, text)
	if err != nil {
		writeJSON(w, 200, askResp{
			Kind: "refusal", Intent: spec.Intent.String(), Cause: "cannot_change",
			NextAction: err.Error(),
		})
		return
	}
	s.Approvals.put(p)
	writeJSON(w, 200, askResp{
		Kind: "proposal", Intent: spec.Intent.String(), Tier: string(spec.Tier),
		Scope: axis, ChangeID: p.ID, Path: p.Proposal.Path, Ref: fmt.Sprintf("%s:%d", p.Proposal.Path, p.Proposal.Line),
		Diff: p.Proposal.Diff(), Text: p.Proposal.Rationale, IdempotencyKey: p.Key,
	})
}

// ── deciding ─────────────────────────────────────────────────────────────────────────────────────

type decideReq struct {
	// ChangeID names a Tier-C proposal held in memory.
	ChangeID string `json:"change_id"`
	// GoalID and TaskID name a durable-goal task parked awaiting approval. 🔴 Two shapes because they are
	// two different things: a Tier-C proposal is a diff this process is holding, and a parked task is a
	// row in the database that a worker will pick up again. Collapsing them would make one of the two
	// pretend to be the other.
	GoalID  string `json:"goal_id"`
	TaskID  string `json:"task_id"`
	Approve bool   `json:"approve"`
}

type decideResp struct {
	Applied bool   `json:"applied"`
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Push    string `json:"push,omitempty"`
	Message string `json:"message"`
}

// handleDecide applies or discards a pending change.
//
// # 🔴 What "deliver" does and, deliberately, does not do
//
// It writes the change on a NEW BRANCH and commits it. It does not push, and it does not open a pull
// request. Both would require a credential with write access to somebody's repository, held by this
// process, used while they are not present — the standing grant that repository connection is out of
// scope for in the first place.
//
// So the branch is prepared and the exact push command is handed back. The person runs it. That is one
// more step for them and one fewer credential for us, and it is the right trade for a system whose
// entire pitch is that it does not change your code without asking.
func (s *Server) handleDecide(w http.ResponseWriter, tenant string, req decideReq) {
	if req.GoalID != "" && req.TaskID != "" {
		s.decideTask(w, tenant, req)
		return
	}
	p, err := s.Approvals.take(req.ChangeID)
	if err != nil {
		writeJSON(w, 200, decideResp{Message: err.Error()})
		return
	}
	if !req.Approve {
		writeJSON(w, 200, decideResp{
			Applied: false,
			Message: "Nothing was written. The finding stays on the report.",
		})
		return
	}

	branch := fmt.Sprintf("heros/%s-%s", p.Proposal.Axis, p.ID[len(p.ID)-6:])
	msg := fmt.Sprintf("%s: %s\n\nProposed by heros against %s.\nIdempotency key: %s\n",
		p.Proposal.Axis, firstSentence(p.Proposal.Rationale),
		p.Revision[:min(8, len(p.Revision))], p.Key)

	// The SAME commit path the improvement run uses. Two implementations of "put a change in somebody's
	// repository" would be enforced twice and eventually differently.
	d, err := tools.CommitChange(p.Root, p.Proposal, branch, msg)
	if err != nil {
		writeJSON(w, http.StatusOK, decideResp{Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, decideResp{
		Applied: true, Branch: d.Branch, Commit: d.Commit, Push: d.Push,
		Message: fmt.Sprintf("Committed to %s. Nothing has been pushed — run the command above when "+
			"you are ready, and open the pull request yourself.", d.Branch),
	})
}

// decideTask answers a parked durable-goal task and, on approval, wakes the run.
func (s *Server) decideTask(w http.ResponseWriter, tenant string, req decideReq) {
	// 🔴 Scoped to the CALLER's tenant. Approval is the act of authorising a write to somebody's
	// repository, so reaching it across tenants would be the single worst thing in this system.
	if err := s.Root.For(tenant).Decide(goal.ID(req.GoalID), task.ID(req.TaskID), req.Approve,
		time.Now().UTC()); err != nil {
		writeJSON(w, http.StatusOK, decideResp{Message: err.Error()})
		return
	}
	if !req.Approve {
		writeJSON(w, http.StatusOK, decideResp{
			Applied: false,
			Message: "Declined. Nothing was written, and the run has stopped there."})
		return
	}
	// 🔴 The run is restarted explicitly. The worker that parked this task returned
	// DidBlockedOnApproval and stopped polling — deliberately, so a goal waiting on a person does not
	// look like a busy one. Something has to say the person answered, and that something is this.
	s.supFor(tenant).Start(context.Background(), goal.ID(req.GoalID))
	writeJSON(w, http.StatusOK, decideResp{
		Applied: true,
		Message: fmt.Sprintf("Approved. %s is running again.", req.TaskID)})
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 68 {
		s = s[:68]
	}
	if s == "" {
		s = "proposed change"
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
