package edit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const agent = `import openai

SYSTEM_PROMPT = "You are terse."

def answer(history):
    return openai.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "system", "content": SYSTEM_PROMPT}] + history,
    )
`

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAnUnambiguousEditAppliesAndIsVerified.
func TestAnUnambiguousEditAppliesAndIsVerified(t *testing.T) {
	root := tree(t, map[string]string{"agent/bot.py": agent})
	p := Proposal{
		Path: "agent/bot.py", Line: 7, Axis: "model",
		Before: `        model="gpt-4o",`,
		After:  `        model="gpt-4o-mini",`,
	}
	if err := p.Validate(root); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := p.Apply(root); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := read(t, root, "agent/bot.py")
	if !strings.Contains(got, `model="gpt-4o-mini"`) {
		t.Fatal("the change is not in the file")
	}
	if strings.Contains(got, `model="gpt-4o",`) {
		t.Fatal("the old text is still there")
	}
	// Everything else is untouched.
	if !strings.Contains(got, `SYSTEM_PROMPT = "You are terse."`) {
		t.Fatal("an unrelated line was changed")
	}
}

// TestAnAmbiguousEditIsRefused.
//
// 🔴 The refusal is the product. Two identical lines and a replacement is a guess about which one the
// person meant — and the wrong guess is a silent behaviour change in a file they will not re-read.
func TestAnAmbiguousEditIsRefused(t *testing.T) {
	root := tree(t, map[string]string{"a.py": "x = 1\ny = 2\nx = 1\n"})
	p := Proposal{Path: "a.py", Line: 1, Before: "x = 1", After: "x = 9"}
	err := p.Validate(root)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("an ambiguous edit was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "2 occurrences") {
		t.Errorf("the refusal does not say how ambiguous it is: %v", err)
	}
	// And nothing was written.
	if read(t, root, "a.py") != "x = 1\ny = 2\nx = 1\n" {
		t.Fatal("a refused edit modified the file")
	}
}

// TestIndentationChangesAreRefused.
//
// 🔴 In Python indentation IS the block structure. A replacement one level out moves the statement into
// a different scope while remaining valid, parseable code — the diff looks fine and the behaviour
// changes. This is the most likely way a plausible edit silently breaks an agent.
func TestIndentationChangesAreRefused(t *testing.T) {
	root := tree(t, map[string]string{"agent/bot.py": agent})
	p := Proposal{
		Path: "agent/bot.py", Line: 7,
		Before: `        model="gpt-4o",`,
		After:  `    model="gpt-4o",  # dedented out of the call`,
	}
	err := p.Validate(root)
	if !errors.Is(err, ErrIndentation) {
		t.Fatalf("a re-indenting edit was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "8 space(s)") {
		t.Errorf("the refusal does not say what the indentation was: %v", err)
	}
}

// TestTheFileMovingUnderneathAnApprovalIsRefused.
//
// An approval is consent to a specific change to specific text, not a standing licence to edit that
// file. Between proposing and applying, a person may take days.
func TestTheFileMovingUnderneathAnApprovalIsRefused(t *testing.T) {
	root := tree(t, map[string]string{"agent/bot.py": agent})
	p := Proposal{
		Path: "agent/bot.py", Line: 7,
		Before: `        model="gpt-4o",`,
		After:  `        model="gpt-4o-mini",`,
	}
	if err := p.Validate(root); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Somebody else edits the file while the approval sits.
	changed := strings.Replace(agent, `model="gpt-4o",`, `model="claude-sonnet-4",`, 1)
	if err := os.WriteFile(filepath.Join(root, "agent", "bot.py"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(root); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an edit was applied to a file that had moved underneath it: %v", err)
	}
	if !strings.Contains(read(t, root, "agent/bot.py"), `claude-sonnet-4`) {
		t.Fatal("the other person's edit was overwritten")
	}
}

// TestAPathCannotLeaveTheRepository.
func TestAPathCannotLeaveTheRepository(t *testing.T) {
	outside := tree(t, map[string]string{"secret.env": "KEY=real\n"})
	root := tree(t, map[string]string{"agent/bot.py": agent})

	for _, path := range []string{"../secret.env", "agent/../../secret.env"} {
		p := Proposal{Path: path, Before: "KEY=real", After: "KEY=stolen"}
		if err := p.Validate(root); err == nil {
			t.Errorf("%q was accepted", path)
		}
	}
	// A symlink pointing out of the tree is the same attack wearing a valid-looking path.
	link := filepath.Join(root, "escape.env")
	if err := os.Symlink(filepath.Join(outside, "secret.env"), link); err == nil {
		p := Proposal{Path: "escape.env", Before: "KEY=real", After: "KEY=stolen"}
		if err := p.Validate(root); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("a symlink out of the repository was accepted: %v", err)
		}
		if !strings.Contains(read(t, outside, "secret.env"), "KEY=real") {
			t.Fatal("a file outside the repository was modified")
		}
	}
}

// TestANoOpEditIsRefused. Applying it would produce an empty diff and a commit that says nothing.
func TestANoOpEditIsRefused(t *testing.T) {
	root := tree(t, map[string]string{"a.py": "x = 1\n"})
	p := Proposal{Path: "a.py", Before: "x = 1", After: "x = 1"}
	if err := p.Validate(root); !errors.Is(err, ErrNoChange) {
		t.Fatalf("a no-op edit was accepted: %v", err)
	}
}

// TestAnIncompleteProposalIsRefused.
func TestAnIncompleteProposalIsRefused(t *testing.T) {
	root := tree(t, map[string]string{"a.py": "x = 1\n"})
	for name, p := range map[string]Proposal{
		"no path":   {Before: "x = 1", After: "x = 2"},
		"no before": {Path: "a.py", After: "x = 2"},
		"no after":  {Path: "a.py", Before: "x = 1"},
	} {
		if err := p.Validate(root); !errors.Is(err, ErrEmpty) && !errors.Is(err, ErrNoChange) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
}

// TestFilePermissionsSurviveARewrite. A rewrite that silently widens a file's mode is a change nobody
// asked for.
func TestFilePermissionsSurviveARewrite(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "run.sh")
	if err := os.WriteFile(full, []byte("MODEL=gpt-4o\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	p := Proposal{Path: "run.sh", Before: "MODEL=gpt-4o", After: "MODEL=gpt-4o-mini"}
	if err := p.Apply(root); err != nil {
		t.Fatalf("apply: %v", err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("mode changed from 0750 to %04o", got)
	}
}

// TestTheIdempotencyKeyIsContentDerived. A retried application must produce the same key, and two
// different changes must not collide.
func TestTheIdempotencyKeyIsContentDerived(t *testing.T) {
	a := Proposal{Path: "a.py", Before: "x = 1", After: "x = 2"}
	b := Proposal{Path: "a.py", Before: "x = 1", After: "x = 3"}
	c := Proposal{Path: "b.py", Before: "x = 1", After: "x = 2"}

	if a.IdempotencyKey("rev1") != a.IdempotencyKey("rev1") {
		t.Fatal("the same proposal produced two keys; a retry would apply it twice")
	}
	if a.IdempotencyKey("rev1") == b.IdempotencyKey("rev1") {
		t.Error("two different replacements share a key")
	}
	if a.IdempotencyKey("rev1") == c.IdempotencyKey("rev1") {
		t.Error("the same change in two files shares a key")
	}
	if a.IdempotencyKey("rev1") == a.IdempotencyKey("rev2") {
		t.Error("the same change at two revisions shares a key; they are different changes")
	}
}

// TestTheDiffShowsExactlyWhatChanges. It is what a person approves, so it must not omit or add a line.
func TestTheDiffShowsExactlyWhatChanges(t *testing.T) {
	p := Proposal{
		Path: "agent/bot.py", Line: 7,
		Before: "a\nb", After: "c",
	}
	d := p.Diff()
	for _, want := range []string{"--- a/agent/bot.py", "+++ b/agent/bot.py", "@@ -7,2 +7,1 @@", "-a", "-b", "+c"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff is missing %q:\n%s", want, d)
		}
	}
}

// TestAReplacementContainingTheOriginalVerifies is a regression fence.
//
// 🔴 The verifier first asked "does the file still contain the old text?", which is false for a correct
// edit whenever the replacement contains the original: `MODEL=gpt-4o` becoming `MODEL=gpt-4o-mini` still
// contains `MODEL=gpt-4o`. A correct write reported itself unverified, and the operator would have been
// told their repository was in an unknown state.
//
// It is the fourth substring trap in this codebase — "remember"/"member" in the router,
// "paragraph"/"graph" in a synthesis test, an axis name inside another word in validate, and this one.
func TestAReplacementContainingTheOriginalVerifies(t *testing.T) {
	cases := map[string][2]string{
		"suffix added": {`MODEL=gpt-4o`, `MODEL=gpt-4o-mini`},
		"prefix added": {`model=x`, `default_model=x`},
		"wrapped":      {`temperature=0`, `temperature=0  # was temperature=0`},
	}
	for name, pair := range cases {
		root := tree(t, map[string]string{"a.env": pair[0] + "\n"})
		p := Proposal{Path: "a.env", Line: 1, Before: pair[0], After: pair[1]}
		if err := p.Apply(root); err != nil {
			t.Errorf("%s: a correct edit reported itself unverified: %v", name, err)
			continue
		}
		if got := read(t, root, "a.env"); got != pair[1]+"\n" {
			t.Errorf("%s: file is %q, want %q", name, got, pair[1]+"\n")
		}
	}
}

// TestVerificationCatchesAWrongWrite. The re-read must actually be able to fail, or it is decoration.
func TestVerificationCatchesAWrongWrite(t *testing.T) {
	root := tree(t, map[string]string{"a.py": "x = 1\n"})
	p := Proposal{Path: "a.py", Before: "x = 1", After: "x = 2"}
	if err := p.Apply(root); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Simulate a racing writer clobbering the file, then re-verify by applying the inverse.
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte("something else entirely\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	back := Proposal{Path: "a.py", Before: "x = 2", After: "x = 1"}
	if err := back.Apply(root); !errors.Is(err, ErrNotFound) {
		t.Fatalf("applying to a clobbered file was not refused: %v", err)
	}
}
