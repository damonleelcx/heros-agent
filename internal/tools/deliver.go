package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/edit"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// deliver.go puts an approved change into the customer's repository — on a branch, committed, and NOT
// pushed. See boundary.go: pushing needs a credential with write access to somebody's repository, held
// by this process, used while they are not present.

// Delivery is what the delivery step produced.
type Delivery struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Path   string `json:"path"`
	// Push is the exact command the person runs. Handed back rather than executed.
	Push string `json:"push"`
}

// CommitChange applies a proposal on a new branch and commits it.
//
// 🔴 ONE implementation, shared by the in-turn Tier-C path and the improvement run's delivery task.
// They are the same act with the same blast radius; two copies means the safety rules are enforced twice
// and will eventually be enforced differently.
func CommitChange(root string, p edit.Proposal, branch, message string) (Delivery, error) {
	var d Delivery
	if out, err := gitIn(root, "checkout", "-b", branch); err != nil {
		return d, fmt.Errorf("could not create branch %s: %v: %s", branch, err, strings.TrimSpace(out))
	}
	// Apply AFTER the branch exists, so a failure leaves the change on a branch a person can inspect
	// rather than in the middle of their working branch.
	if err := p.Apply(root); err != nil {
		return d, fmt.Errorf("the change was not applied: %w (you are on branch %s; "+
			"`git checkout -` returns you)", err, branch)
	}
	if out, err := gitIn(root, "add", p.Path); err != nil {
		return d, fmt.Errorf("git add failed: %v: %s", err, strings.TrimSpace(out))
	}
	if out, err := gitIn(root, "commit", "-m", message); err != nil {
		return d, fmt.Errorf("git commit failed: %v: %s", err, strings.TrimSpace(out))
	}
	sha, _ := gitIn(root, "rev-parse", "--short", "HEAD")
	return Delivery{
		Branch: branch, Commit: strings.TrimSpace(sha), Path: p.Path,
		Push: fmt.Sprintf("git push -u origin %s", branch),
	}, nil
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// 🚫 No credential prompts. A git subprocess that can ask for a password blocks forever inside a
	// worker, holding its lease, with nobody at a terminal to answer.
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// OpenPullRequest is the improvement run's delivery task.
//
// 🔴 Named for what a person asked for and honest about what it does: it prepares the branch and hands
// back the push command. The name stays because it is the sentence the user typed; the RESULT says
// plainly that nothing was pushed.
type OpenPullRequest struct {
	Root string
}

func (o OpenPullRequest) Spec() toolcontract.Spec {
	return toolcontract.Spec{
		Kind:          planner.KindOpenPR,
		Permissions:   []toolcontract.Permission{toolcontract.WriteSource},
		Timeout:       60 * time.Second,
		RetrySafe:     false,
		EffectBearing: true,
	}
}

func (o OpenPullRequest) Execute(_ context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	if o.Root == "" {
		return toolcontract.Result{}, fmt.Errorf("no repository is loaded, so there is nowhere to deliver")
	}
	verdict, err := verdictFromInputs(c.Inputs)
	if err != nil {
		return toolcontract.Result{}, err
	}
	// 🔴 A verdict that did not pass must never reach here — the DAG blocks it, because verify fails the
	// task. Checked anyway: this is the last step before somebody's repository changes, and a gate that
	// trusts an upstream gate is one gate.
	if !verdict.Passed {
		return toolcontract.Result{}, fmt.Errorf(
			"this change did not pass verification and must not be delivered")
	}
	p := verdict.Proposal

	// The branch name is derived from the change, so a retried delivery targets the SAME branch rather
	// than accumulating one per attempt.
	branch := fmt.Sprintf("heros/%s-%s", p.Axis, hashOf(p.Before + p.After)[:6])
	msg := fmt.Sprintf("%s: %s\n\nProposed by heros.\nIdempotency key: %s\n",
		p.Axis, firstSentenceOf(p.Rationale), c.IdempotencyKey)

	d, err := CommitChange(o.Root, p, branch, msg)
	if err != nil {
		return toolcontract.Result{}, err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return toolcontract.Result{}, err
	}
	return toolcontract.Result{Output: b}, nil
}

func verdictFromInputs(inputs map[string][]byte) (Verdict, error) {
	for _, raw := range inputs {
		var v Verdict
		if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
			continue
		}
		if v.Proposal.Path != "" {
			return v, nil
		}
	}
	return Verdict{}, fmt.Errorf("no verified change was produced for delivery")
}

func firstSentenceOf(s string) string {
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

// deliveryVerifier confirms the commit exists and contains the change.
//
// 🔴 Required by the contract for an effect-bearing tool, and it goes and looks. `git commit` returning
// zero says the command ran; it does not say the file in that commit contains the replacement.
type deliveryVerifier struct{ Root string }

// NewDeliveryVerifier builds the verifier for a repository.
func NewDeliveryVerifier(root string) toolcontract.Verifier { return deliveryVerifier{Root: root} }

func (v deliveryVerifier) Verify(_ context.Context, _ toolcontract.Call, r toolcontract.Result) (bool, string, error) {
	var d Delivery
	if err := json.Unmarshal(r.Output, &d); err != nil {
		return false, "the delivery step reported nothing readable", nil
	}
	if d.Commit == "" || d.Branch == "" {
		return false, "the delivery reported no commit", nil
	}
	// The commit must exist and touch the file the change was in.
	out, err := gitIn(v.Root, "show", "--name-only", "--format=", d.Commit)
	if err != nil {
		// Inconclusive rather than absent: "I could not check" and "it is not there" lead to different
		// next actions, and only one of them is safe to retry.
		return false, "", fmt.Errorf("could not read commit %s: %v", d.Commit, err)
	}
	if !strings.Contains(out, d.Path) {
		return false, fmt.Sprintf("commit %s does not touch %s", d.Commit, d.Path), nil
	}
	head, err := gitIn(v.Root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return false, "", fmt.Errorf("could not read the current branch: %v", err)
	}
	if strings.TrimSpace(head) != d.Branch {
		return false, fmt.Sprintf("the repository is on %s, not the branch the change was committed to (%s)",
			strings.TrimSpace(head), d.Branch), nil
	}
	return true, "", nil
}
