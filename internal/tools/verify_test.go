package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/heros/internal/edit"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

const pySource = `import openai

SYSTEM_PROMPT = "You are terse."


def answer(history):
    return openai.chat.completions.create(
        model="gpt-4o",
        temperature=0.7,
        messages=[{"role": "system", "content": SYSTEM_PROMPT}] + history,
    )
`

func pyRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent", "bot.py"), []byte(pySource), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func verdictFrom(t *testing.T, res toolcontract.Result) Verdict {
	t.Helper()
	var v Verdict
	if len(res.Output) == 0 {
		t.Fatal("no verdict was produced")
	}
	if err := json.Unmarshal(res.Output, &v); err != nil {
		t.Fatalf("undecodable verdict: %v", err)
	}
	return v
}

func runVerify(t *testing.T, root string, p *fakeProvider, prop edit.Proposal) (Verdict, error) {
	t.Helper()
	raw, _ := json.Marshal(prop)
	v := VerifyProposal{Provider: p, Model: "test", Root: root}
	res, err := v.Execute(context.Background(), toolcontract.Call{
		TaskID: "verify-model-0", Kind: "verify_proposal",
		Inputs: map[string][]byte{"propose-model-0": raw}, Attempt: 1,
	})
	if len(res.Output) == 0 {
		return Verdict{}, err
	}
	return verdictFrom(t, res), err
}

// TestSyntacticallyBrokenChangesAreCaughtByAParserNotAModel.
//
// 🔴 The check that matters most, and the reason a model is not the first line of defence. A rewrite can
// be plausible, well-indented, and syntactically broken; no amount of asking a model nicely catches that
// reliably. A parser catches it every time, for free — and here the model is configured to APPROVE, so
// the only thing that can reject this change is the parser.
func TestSyntacticallyBrokenChangesAreCaughtByAParserNotAModel(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable; the parser check could not run")
	}
	root := pyRepo(t)
	approving := &fakeProvider{reply: `{"refuted":false,"reason":""}`}

	broken := edit.Proposal{
		Path: "agent/bot.py", Line: 8, Axis: "model",
		Before:    `        model="gpt-4o",`,
		After:     `        model="gpt-4o-mini,`, // unterminated string
		Rationale: "cheaper model",
	}
	v, err := runVerify(t, root, approving, broken)
	if err == nil {
		t.Fatal("a syntactically broken change verified successfully")
	}
	if v.Passed {
		t.Fatal("the verdict passed on unparseable code")
	}
	var sawParse bool
	for _, c := range v.Checks {
		if c.Name == "still parses" {
			sawParse = true
			if c.Passed {
				t.Error("the parser check passed on unparseable code")
			}
			if c.Detail == "" {
				t.Error("the parser rejected the change without saying what was wrong")
			}
		}
	}
	if !sawParse {
		t.Fatal("no parser check ran")
	}
	// 🔴 And the customer's file is untouched: verification that mutates what it verifies is a change
	// that has already happened.
	body, _ := os.ReadFile(filepath.Join(root, "agent", "bot.py"))
	if string(body) != pySource {
		t.Fatal("verification modified the file it was checking")
	}
}

// TestAValidChangeSurvives.
func TestAValidChangeSurvives(t *testing.T) {
	root := pyRepo(t)
	approving := &fakeProvider{reply: `{"refuted":false,"reason":""}`}
	good := edit.Proposal{
		Path: "agent/bot.py", Line: 8, Axis: "model",
		Before:    `        model="gpt-4o",`,
		After:     `        model="gpt-4o-mini",`,
		Rationale: "cheaper model for a fixed-label task",
	}
	v, err := runVerify(t, root, approving, good)
	if err != nil {
		t.Fatalf("a valid change was rejected: %v", err)
	}
	if !v.Passed {
		t.Fatalf("verdict did not pass: %+v", v.Checks)
	}
	if len(v.Checks) != 4 {
		t.Errorf("%d checks ran, want 4 (applies, parses, names, refutation)", len(v.Checks))
	}
}

// TestARefutedChangeFails, and the reason reaches the caller.
func TestARefutedChangeFails(t *testing.T) {
	root := pyRepo(t)
	refuting := &fakeProvider{
		reply: `{"refuted":true,"reason":"gpt-4o-mini cannot use the vision inputs this node receives"}`}
	prop := edit.Proposal{
		Path: "agent/bot.py", Line: 8, Axis: "model",
		Before: `        model="gpt-4o",`, After: `        model="gpt-4o-mini",`,
	}
	v, err := runVerify(t, root, refuting, prop)
	if err == nil {
		t.Fatal("a refuted change verified successfully")
	}
	if v.Passed {
		t.Fatal("the verdict passed on a refuted change")
	}
	if !strings.Contains(err.Error(), "vision inputs") {
		t.Errorf("the refutation's reason did not reach the caller: %v", err)
	}
}

// TestAnUnreadableRefutationFailsTheChange.
//
// 🔴 The alternative is to pass anything whose verification could not be read, which turns a broken
// verifier into an open gate — and the gate is the last thing before a person is asked to approve a
// write to their repository.
func TestAnUnreadableRefutationFailsTheChange(t *testing.T) {
	root := pyRepo(t)
	garbage := &fakeProvider{reply: `I think this change is fine, honestly`}
	prop := edit.Proposal{
		Path: "agent/bot.py", Line: 8, Axis: "model",
		Before: `        model="gpt-4o",`, After: `        model="gpt-4o-mini",`,
	}
	v, err := runVerify(t, root, garbage, prop)
	if err == nil {
		t.Fatal("an unreadable verification passed the change")
	}
	if v.Passed {
		t.Fatal("the verdict passed")
	}
}

// TestAChangeThatNoLongerAppliesIsRejectedBeforeAnythingElse.
//
// Between proposing and verifying, another task or another person may have changed the file. An edit
// that no longer applies must not reach a person's approval screen looking ready.
func TestAChangeThatNoLongerAppliesIsRejectedBeforeAnythingElse(t *testing.T) {
	root := pyRepo(t)
	approving := &fakeProvider{reply: `{"refuted":false,"reason":""}`}
	stale := edit.Proposal{
		Path: "agent/bot.py", Line: 8, Axis: "model",
		Before: `        model="claude-sonnet-4",`, // never was there
		After:  `        model="gpt-4o-mini",`,
	}
	v, err := runVerify(t, root, approving, stale)
	if err == nil {
		t.Fatal("a stale change verified successfully")
	}
	if len(v.Checks) != 1 || v.Checks[0].Name != "still applies" {
		t.Fatalf("checks after a stale edit: %+v — nothing should run past the first failure", v.Checks)
	}
	if approving.seen.Model != "" {
		t.Error("the model was consulted about a change that cannot be applied")
	}
}

// TestNoProposalIsAFailure. A verification with nothing to check must not report success.
func TestNoProposalIsAFailure(t *testing.T) {
	root := pyRepo(t)
	v := VerifyProposal{Provider: &fakeProvider{}, Model: "test", Root: root}
	for name, inputs := range map[string]map[string][]byte{
		"none":        {},
		"empty":       {"propose-model-0": {}},
		"unparseable": {"propose-model-0": []byte("not json")},
		"incomplete":  {"propose-model-0": []byte(`{"path":"a.py"}`)},
	} {
		if _, err := v.Execute(context.Background(), toolcontract.Call{
			TaskID: "verify-model-0", Inputs: inputs, Attempt: 1}); err == nil {
			t.Errorf("%s: verification with nothing to check reported success", name)
		}
	}
}

// TestTheVerifierAsksForRefutationNotApproval.
//
// 🔴 A verifier that asks "is this good?" is the same model, on the same evidence, agreeing with itself
// — and it agrees, because agreeing is the shape of the question.
func TestTheVerifierAsksForRefutationNotApproval(t *testing.T) {
	root := pyRepo(t)
	p := &fakeProvider{reply: `{"refuted":false,"reason":""}`}
	prop := edit.Proposal{
		Path: "agent/bot.py", Line: 8, Axis: "model",
		Before: `        model="gpt-4o",`, After: `        model="gpt-4o-mini",`,
	}
	if _, err := runVerify(t, root, p, prop); err != nil {
		t.Fatalf("verify: %v", err)
	}
	prompt := strings.ToLower(p.seen.Messages[0].Content)
	if !strings.Contains(prompt, "refute") {
		t.Errorf("the verifier does not ask for refutation:\n%s", prompt)
	}
	if strings.Contains(prompt, "is this change good") {
		t.Error("the verifier asks for approval, which the model will give")
	}
}

// TestAChangeUsingAnUnimportedModuleIsRejected.
//
// 🔴 Regression fence for a change this system actually produced, verified, and committed. Asked to make
// a model configurable it wrote `os.getenv(...)` into a file that does not import `os`. The syntax is
// valid, so compile() accepted it. The model was asked to refute it and did not notice. The change
// reached a branch looking ready.
//
// A syntax check answers "is this parseable", not "does this run", and the gap between those is exactly
// where a plausible rewrite lives. The model is configured to APPROVE here, so only the deterministic
// name check can reject this.
func TestAChangeUsingAnUnimportedModuleIsRejected(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable; the name check could not run")
	}
	root := pyRepo(t)
	approving := &fakeProvider{reply: `{"refuted":false,"reason":""}`}

	broken := edit.Proposal{
		Path: "agent/bot.py", Line: 8, Axis: "model",
		Before:    `        model="gpt-4o",`,
		After:     `        model=os.getenv("OPENAI_MODEL", "gpt-4o"),`,
		Rationale: "make the model configurable",
	}
	v, err := runVerify(t, root, approving, broken)
	if err == nil {
		t.Fatal("a change using an unimported module verified successfully")
	}
	if v.Passed {
		t.Fatal("the verdict passed on code that raises NameError at runtime")
	}
	var saw bool
	for _, c := range v.Checks {
		if c.Name == "names resolve" {
			saw = true
			if c.Passed {
				t.Error("the name check passed on an unimported module")
			}
			if !strings.Contains(c.Detail, "os") {
				t.Errorf("the rejection does not name what is missing: %q", c.Detail)
			}
		}
	}
	if !saw {
		t.Fatal("no name-resolution check ran")
	}
}

// TestAChangeThatImportsWhatItUsesPasses. The check must not reject a correct fix.
func TestAChangeThatImportsWhatItUsesPasses(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent", "bot.py"),
		[]byte("import os\n"+pySource), 0o644); err != nil {
		t.Fatal(err)
	}
	approving := &fakeProvider{reply: `{"refuted":false,"reason":""}`}
	good := edit.Proposal{
		Path: "agent/bot.py", Line: 9, Axis: "model",
		Before: `        model="gpt-4o",`,
		After:  `        model=os.getenv("OPENAI_MODEL", "gpt-4o"),`,
	}
	v, err := runVerify(t, root, approving, good)
	if err != nil {
		t.Fatalf("a correct change was rejected: %v", err)
	}
	if !v.Passed {
		t.Fatalf("verdict did not pass: %+v", v.Checks)
	}
	if len(v.Checks) != 4 {
		t.Errorf("%d checks ran, want 4 (applies, parses, names, refutation)", len(v.Checks))
	}
}

// TestLocallyDefinedNamesAreNotReportedMissing. A check that flagged the file's own functions would
// reject every correct change, which is worse than not checking at all.
func TestLocallyDefinedNamesAreNotReportedMissing(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	src := `import openai


class Client:
    def send(self, m):
        return m


client = Client()


def answer(history):
    helper = client
    return helper.send(history)
`
	if err := os.WriteFile(filepath.Join(root, "bot.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	approving := &fakeProvider{reply: `{"refuted":false,"reason":""}`}
	p := edit.Proposal{
		Path: "bot.py", Line: 14, Axis: "model",
		Before: `    return helper.send(history)`, After: `    return helper.send(history[-6:])`,
	}
	if _, err := runVerify(t, root, approving, p); err != nil {
		t.Fatalf("a change using locally-defined names was rejected: %v", err)
	}
}
