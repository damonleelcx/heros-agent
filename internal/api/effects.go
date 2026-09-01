package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/heros/internal/edit"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/task"
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

const proposeSystem = `You rewrite ONE span of a developer's source code to fix a specific weakness.

Rules:
- Return the replacement for the given span ONLY. Not the whole file, not an explanation.
- Preserve the EXACT leading whitespace of the first line. Indentation is block structure.
- Change as little as possible. A smaller diff is a better one.
- If the span cannot be improved without seeing more code, say so with "can_change": false.
- Reply with a JSON object only: {"can_change": boolean, "replacement": string, "rationale": string}`

type proposeWire struct {
	CanChange   bool   `json:"can_change"`
	Replacement string `json:"replacement"`
	Rationale   string `json:"rationale"`
}

// propose asks the model to rewrite one span, then validates the result against the file on disk.
//
// 🔴 The model's output is never trusted as an edit. It is a candidate replacement string; `Validate`
// decides whether it can be applied, and refuses ambiguity, re-indentation, and no-ops. The model
// suggests; this package decides.
func (s *Server) propose(ctx context.Context, sub *subjectState, axis, instruction string) (*pending, error) {
	ev := sub.Index.ForAxis(axis)
	if !ev.Found || len(ev.Spans) == 0 {
		return nil, fmt.Errorf("there is no %s code in this repository to change — %s", axis, ev.Note)
	}
	// The highest-ranked span: evidence nearest a call site, which is where the live path is.
	target := ev.Spans[0]

	temp := 0.0
	resp, err := s.Provider.Complete(ctx, provider.Request{
		Model: s.Model, MaxTokens: 600, Reasoning: provider.NoReasoning, Temperature: &temp,
		JSONObject: true,
		Messages: []provider.Message{
			{Role: "system", Content: proposeSystem},
			{Role: "user", Content: fmt.Sprintf(
				"Axis: %s\nFile: %s\nWhat to change: %s\n\nSpan to rewrite:\n%s\n\n"+
					"Reply as JSON: {\"can_change\":boolean,\"replacement\":string,\"rationale\":string}",
				axis, target.Ref(), instruction, target.Text)},
		},
	})
	if err != nil {
		return nil, err
	}
	if resp.Truncated() {
		return nil, fmt.Errorf("the proposed change was cut off; the span at %s is too large to rewrite "+
			"in one step", target.Ref())
	}
	var w proposeWire
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &w); err != nil {
		return nil, fmt.Errorf("the model did not return a usable change: %w", err)
	}
	if !w.CanChange || strings.TrimSpace(w.Replacement) == "" {
		reason := w.Rationale
		if reason == "" {
			reason = "the model could not improve this span from what it was shown"
		}
		return nil, fmt.Errorf("no change proposed for %s at %s: %s", axis, target.Ref(), reason)
	}

	p := edit.Proposal{
		Path: target.Path, Line: target.Line, Axis: axis,
		Before: target.Text, After: strings.TrimRight(w.Replacement, "\n"),
		Rationale: w.Rationale,
	}
	// 🔴 Validated against the file on disk BEFORE a person is shown a diff. Showing somebody a change
	// that cannot be applied wastes their decision, and they will not find out until they approve it.
	if err := p.Validate(sub.Source.Root); err != nil {
		return nil, fmt.Errorf("the proposed change cannot be applied safely: %w", err)
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
func (s *Server) handleDecide(w http.ResponseWriter, req decideReq) {
	if req.GoalID != "" && req.TaskID != "" {
		s.decideTask(w, req)
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

	branch := "heros/" + strings.ReplaceAll(p.Proposal.Axis, " ", "-") + "-" + p.ID[len(p.ID)-6:]
	if out, err := git(p.Root, "checkout", "-b", branch); err != nil {
		writeJSON(w, 200, decideResp{Message: fmt.Sprintf("could not create a branch: %v: %s", err, out)})
		return
	}
	if err := p.Proposal.Apply(p.Root); err != nil {
		// Leave the branch: a half-applied state a person can inspect beats a tidy one that hides what
		// happened. The message says where they are.
		writeJSON(w, 200, decideResp{
			Message: fmt.Sprintf("the change was not applied: %v. You are on branch %s; "+
				"`git checkout -` returns you.", err, branch)})
		return
	}
	if out, err := git(p.Root, "add", p.Proposal.Path); err != nil {
		writeJSON(w, 200, decideResp{Message: fmt.Sprintf("git add failed: %v: %s", err, out)})
		return
	}
	msg := fmt.Sprintf("%s: %s\n\nProposed by heros against %s.\nIdempotency key: %s\n",
		p.Proposal.Axis, firstSentence(p.Proposal.Rationale), p.Revision[:min(8, len(p.Revision))], p.Key)
	if out, err := git(p.Root, "commit", "-m", msg); err != nil {
		writeJSON(w, 200, decideResp{Message: fmt.Sprintf("git commit failed: %v: %s", err, out)})
		return
	}
	sha, _ := git(p.Root, "rev-parse", "--short", "HEAD")

	writeJSON(w, 200, decideResp{
		Applied: true, Branch: branch, Commit: strings.TrimSpace(sha),
		Push: fmt.Sprintf("git push -u origin %s", branch),
		Message: fmt.Sprintf("Committed to %s. Nothing has been pushed — run the command above when "+
			"you are ready, and open the pull request yourself.", branch),
	})
}

// decideTask answers a parked durable-goal task and, on approval, wakes the run.
func (s *Server) decideTask(w http.ResponseWriter, req decideReq) {
	if err := s.Store.Decide(goal.ID(req.GoalID), task.ID(req.TaskID), req.Approve,
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
	s.Sup.Start(context.Background(), goal.ID(req.GoalID))
	writeJSON(w, http.StatusOK, decideResp{
		Applied: true,
		Message: fmt.Sprintf("Approved. %s is running again.", req.TaskID)})
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
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
