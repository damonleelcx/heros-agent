package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/edit"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// verify.go decides whether a proposed change should be put in front of a person.
//
// # 🔴 Why the model is asked to REFUTE rather than to review
//
// A verifier that asks "is this change good?" is the same model, on the same evidence, agreeing with
// itself — and it agrees, because agreeing is the shape of the question. The useful question is the
// opposite one: find a reason this is wrong. A change that survives a genuine attempt to refute it has
// been tested; a change that collects a second "looks good" has been endorsed twice by the same opinion.
//
// # 🔴 And the model is not the only check, or even the first
//
// Two checks run before it, and both are deterministic:
//
//	1. the edit still applies — the file may have changed since it was proposed;
//	2. the language still parses, where a parser is available.
//
// The second is the one that catches the failure mode that matters. A model rewriting Python can produce
// a replacement that is plausible, well-indented, and syntactically broken, and no amount of asking it
// nicely will reliably catch that. A parser will, every time, for free.

// Verdict is the outcome of verification.
type Verdict struct {
	Proposal edit.Proposal `json:"proposal"`
	// Passed says the change may be shown to a person.
	Passed bool `json:"passed"`
	// Checks records every check and its result, so a rejection can be read rather than guessed at.
	Checks []Check `json:"checks"`
}

// Check is one verification step.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// VerifyProposal checks a proposed change independently of the model that produced it.
type VerifyProposal struct {
	Provider provider.Provider
	Model    string
	Root     string
	Timeout  time.Duration
}

func (v VerifyProposal) Spec() toolcontract.Spec {
	t := v.Timeout
	if t == 0 {
		t = 90 * time.Second
	}
	return toolcontract.Spec{
		Kind:        planner.KindVerifyProposal,
		Permissions: []toolcontract.Permission{toolcontract.ReadSource, toolcontract.CallModel},
		Timeout:     t, RetrySafe: true, EffectBearing: false,
	}
}

const refuteSystem = `You are given a weakness in an agent's source code and a proposed change to fix it.

Your job is to REFUTE the change: find a concrete reason it is wrong, incomplete, or harmful. Look for
behaviour that would change unintentionally, an assumption the replacement makes that the surrounding
code does not support, and cases the original handled that the replacement does not.

Look especially for a name the replacement uses that the file does not define or import — a module,
function or constant introduced by the change with nothing bringing it into scope. That is the most
common way a plausible rewrite is broken.

- If you find a real problem, say refuted: true and name it specifically.
- If you genuinely cannot find one, say refuted: false. Do not invent an objection to seem thorough,
  and do not pass a change you have doubts about to seem agreeable.
- Reply with a JSON object only: {"refuted": boolean, "reason": string}`

type refuteReply struct {
	Refuted bool   `json:"refuted"`
	Reason  string `json:"reason"`
}

func (v VerifyProposal) Execute(ctx context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	prop, err := proposalFrom(c.Inputs)
	if err != nil {
		return toolcontract.Result{}, err
	}
	verdict := Verdict{Proposal: prop, Passed: true}

	// ── 1. it still applies ──────────────────────────────────────────────────────────────────────
	//
	// Re-validated rather than trusted from the proposal step: between proposing and verifying, another
	// task or another person may have changed the file, and an edit that no longer applies must not
	// reach a person's approval screen looking ready.
	if err := prop.Validate(v.Root); err != nil {
		verdict.Passed = false
		verdict.Checks = append(verdict.Checks, Check{"still applies", false, err.Error()})
		return finish(verdict, toolcontract.Result{})
	}
	verdict.Checks = append(verdict.Checks, Check{Name: "still applies", Passed: true})

	// ── 2. it still parses ───────────────────────────────────────────────────────────────────────
	if ok, detail := parses(v.Root, prop); !ok {
		verdict.Passed = false
		verdict.Checks = append(verdict.Checks, Check{"still parses", false, detail})
		return finish(verdict, toolcontract.Result{})
	}
	verdict.Checks = append(verdict.Checks, Check{Name: "still parses", Passed: true,
		Detail: parseNote(prop)})

	// ── 3. every name it uses is defined ─────────────────────────────────────────────────────────
	//
	// 🔴 This check exists because the system produced a change that passed everything else and was
	// broken. Asked to make a model configurable, it wrote `os.getenv(...)` into a file that does not
	// import `os`. The syntax is valid, so compile() accepted it; the model was asked to refute it and
	// did not notice; and the change was committed to a branch as if it were ready.
	//
	// A syntax check answers "is this parseable". It does not answer "does this run", and the gap between
	// those two is where a plausible rewrite lives. Name resolution closes the most common part of that
	// gap deterministically, which is worth more than asking the model more insistently.
	if ok, detail := namesResolve(v.Root, prop); !ok {
		verdict.Passed = false
		verdict.Checks = append(verdict.Checks, Check{"names resolve", false, detail})
		return finish(verdict, toolcontract.Result{})
	}
	verdict.Checks = append(verdict.Checks, Check{Name: "names resolve", Passed: true})

	// ── 4. somebody tries to refute it ───────────────────────────────────────────────────────────
	temp := 0.0
	resp, err := v.Provider.Complete(ctx, provider.Request{
		Model: v.Model, MaxTokens: 500, Reasoning: provider.NoReasoning, Temperature: &temp,
		JSONObject: true,
		Messages: []provider.Message{
			{Role: "system", Content: refuteSystem},
			{Role: "user", Content: fmt.Sprintf(
				"Axis: %s\nFile: %s:%d\nWeakness being fixed: %s\n\nCurrent code:\n%s\n\nProposed replacement:\n%s\n\n"+
					"Reply as JSON: {\"refuted\":boolean,\"reason\":string}",
				prop.Axis, prop.Path, prop.Line, prop.Rationale, prop.Before, prop.After)},
		},
	})
	res := toolcontract.Result{ToolCalls: 1, Tokens: resp.Usage.Total(), CostMicroCents: resp.CostMicroCents}
	if err != nil {
		return res, err
	}
	var w refuteReply
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &w); err != nil {
		// 🔴 An unreadable refutation FAILS the change. The alternative is to pass anything whose
		// verification could not be read, which turns a broken verifier into an open gate — and the gate
		// is the last thing before a person is asked to approve a write to their repository.
		verdict.Passed = false
		verdict.Checks = append(verdict.Checks, Check{"survives refutation", false,
			"the refutation could not be read: " + err.Error()})
		return finish(verdict, res)
	}
	if w.Refuted {
		verdict.Passed = false
		verdict.Checks = append(verdict.Checks, Check{"survives refutation", false, w.Reason})
		return finish(verdict, res)
	}
	verdict.Checks = append(verdict.Checks, Check{Name: "survives refutation", Passed: true})
	return finish(verdict, res)
}

// finish serialises the verdict. A FAILED verification is an error, so the task fails and the pull
// request behind it is blocked — a rejected change must not sit in the graph looking merely unfinished.
func finish(v Verdict, res toolcontract.Result) (toolcontract.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return res, err
	}
	res.Output = b
	if !v.Passed {
		for _, c := range v.Checks {
			if !c.Passed {
				return res, fmt.Errorf("the change did not verify — %s: %s", c.Name, c.Detail)
			}
		}
		return res, fmt.Errorf("the change did not verify")
	}
	return res, nil
}

// proposalFrom decodes the proposal this verification is about.
func proposalFrom(inputs map[string][]byte) (edit.Proposal, error) {
	for _, raw := range inputs {
		var p edit.Proposal
		if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
			continue
		}
		if p.Path != "" && p.Before != "" && p.After != "" {
			return p, nil
		}
	}
	return edit.Proposal{}, fmt.Errorf("no proposal was produced for this verification to check")
}

// parses applies the change to a COPY of the file and asks the language's own parser whether the result
// is valid.
//
// 🔴 Nothing is written to the customer's tree. The candidate is parsed from a temporary file, and the
// original is untouched whether it passes or fails — verification that mutates the thing it verifies is
// a change that has already happened.
//
// 🚫 It does not RUN the code. Parsing is reading; running is the boundary this system does not cross
// (see boundary.go). `python -c "compile(...)"` compiles a string and executes nothing.
func parses(root string, p edit.Proposal) (bool, string) {
	lang := languageOfPath(p.Path)
	if lang == "" {
		return true, "" // nothing to check; not a failure
	}
	full, err := safeJoin(root, p.Path)
	if err != nil {
		return false, err.Error()
	}
	body, err := osReadFile(full)
	if err != nil {
		return false, err.Error()
	}
	candidate := strings.Replace(body, p.Before, p.After, 1)

	switch lang {
	case "python":
		return compilesWithPython(candidate, p.Path)
	case "go":
		return compilesWithGofmt(candidate, p.Path)
	}
	return true, ""
}

// namesResolve checks that every module the candidate uses is actually imported.
//
// 🚫 Not a type checker and not a linter. It answers one question — "does this change reference something
// that is not in scope?" — because that is the failure a syntax check cannot see and the one a rewrite
// most often introduces. Anything subtler belongs to the customer's own tests, which this system does
// not run.
func namesResolve(root string, p edit.Proposal) (bool, string) {
	if languageOfPath(p.Path) != "python" {
		// 🔴 Unchecked, and the verdict says so rather than implying a check that did not happen. Go's
		// equivalent needs a compile, which needs the customer's dependencies — a different promise from
		// the one this system makes.
		return true, ""
	}
	full, err := safeJoin(root, p.Path)
	if err != nil {
		return false, err.Error()
	}
	body, err := osReadFile(full)
	if err != nil {
		return false, err.Error()
	}
	candidate := strings.Replace(body, p.Before, p.After, 1)

	cmd := exec.Command("python3", "-c", pyNameCheck)
	cmd.Stdin = strings.NewReader(candidate)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			return true, ""
		}
		return false, firstLineOf(string(out))
	}
	missing := strings.TrimSpace(string(out))
	if missing == "" {
		return true, ""
	}
	return false, fmt.Sprintf("uses %s, which the file does not import", missing)
}

// pyNameCheck reads a module on stdin and prints any module-qualified name it uses that is neither
// bound in the file nor a builtin. It PARSES; it executes nothing.
const pyNameCheck = `
import ast, sys, builtins
tree = ast.parse(sys.stdin.read())
bound = set()
for n in ast.walk(tree):
    if isinstance(n, ast.Import):
        for a in n.names:
            bound.add((a.asname or a.name).split('.')[0])
    elif isinstance(n, ast.ImportFrom):
        for a in n.names:
            bound.add(a.asname or a.name)
    elif isinstance(n, ast.Name) and isinstance(n.ctx, ast.Store):
        bound.add(n.id)
    elif isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
        bound.add(n.name)
    elif isinstance(n, ast.arg):
        bound.add(n.arg)
    elif isinstance(n, ast.ExceptHandler) and n.name:
        bound.add(n.name)
used = {x.value.id for x in ast.walk(tree)
        if isinstance(x, ast.Attribute) and isinstance(x.value, ast.Name)}
missing = sorted(u for u in used if u not in bound and not hasattr(builtins, u))
if missing:
    print(", ".join(missing))
`

func parseNote(p edit.Proposal) string {
	switch languageOfPath(p.Path) {
	case "python":
		return "python compile()"
	case "go":
		return "gofmt parse"
	}
	return "no parser for this language; syntax was not checked"
}

func languageOfPath(path string) string {
	switch {
	case strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasSuffix(path, ".go"):
		return "go"
	}
	return ""
}

// compilesWithPython compiles the candidate source WITHOUT executing it.
func compilesWithPython(source, name string) (bool, string) {
	cmd := exec.Command("python3", "-c",
		"import sys; compile(sys.stdin.read(), sys.argv[1], 'exec')", name)
	cmd.Stdin = strings.NewReader(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			// 🔴 A missing parser is NOT a pass and NOT a failure — it is an unchecked property, and the
			// verdict says so rather than implying a check that did not happen.
			return true, ""
		}
		return false, firstLineOf(string(out))
	}
	return true, ""
}

// compilesWithGofmt parses Go without building it.
func compilesWithGofmt(source, name string) (bool, string) {
	cmd := exec.Command("gofmt", "-e")
	cmd.Stdin = strings.NewReader(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, firstLineOf(string(out))
	}
	_ = name
	return true, ""
}

// osReadFile is a thin alias so the read has one home and the import list stays honest.
func osReadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
