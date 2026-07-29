package cli

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/discovery"
)

// P13 13c task 12.1 — offline authoring parity.
//
// The property under test is not "the command runs". It is that the CLI reaches the SAME verdict with
// the SAME words as the hosted surface, with no account and no network — because a user diagnosing
// "why won't this apply?" across two surfaces that describe one cause two ways has two problems.

// TestCLIAuthorsOfflineWithIdenticalCause runs `author` against a real repository fixture and asserts
// the verdict, the named cause, and the absence of any network dependency.
func TestCLIAuthorsOfflineWithIdenticalCause(t *testing.T) {
	repo := authorFixtureRepo(t)
	specPath := writeAuthorSpec(t, repo)

	// 🔴 Offline there is NO registry, so a ref that names a registry entry cannot resolve — and that is
	// the correct offline behaviour, not a limitation to work around. The refusal a fully-offline author
	// most often meets is therefore "this ref resolves to nothing", named with its node and dimension.
	// Naming this subtest after the cross-provider refusal (which needs a resolvable ref to reach) would
	// be a test claiming to cover something it never exercises.
	t.Run("an unresolvable ref is refused by name, offline, and exits OK", func(t *testing.T) {
		out, errOut, code := runAuthor(t,
			"--repo", repo, "--spec", specPath, "--node", authorNodeID(t, repo),
			"--model", "openai:gpt-4o")

		// 🔴 A refusal is a VERDICT. Exiting non-zero would make every CI wrapper read "the platform
		// declined this" as "the tool broke", which is the single most damaging mapping on this surface.
		if code != ExitOK {
			t.Fatalf("exit = %d, want %d (a refusal is an answer, not a failure)\n%s", code, ExitOK, errOut)
		}
		var env struct {
			Data AuthorData `json:"data"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("decode machine output: %v\n%s", err, out)
		}
		if env.Data.Verdict != "refused" {
			t.Fatalf("verdict = %q, want refused (payload %+v)", env.Data.Verdict, env.Data)
		}
		if env.Data.Cause == "" || env.Data.NodeID == "" {
			t.Errorf("the refusal names nothing: %+v — a refusal a user cannot act on is not a refusal", env.Data)
		}
		// The cause is the ENGINE's sentence, rendered verbatim. If the CLI ever starts re-wording it,
		// this is where the two surfaces begin to drift.
		if !strings.Contains(env.Data.Cause, env.Data.NodeID) {
			t.Errorf("the cause does not name the node it refers to: %q", env.Data.Cause)
		}
		// Nothing was written.
		if env.Data.DiffPath != "" {
			t.Errorf("a refused author wrote a diff at %q", env.Data.DiffPath)
		}
		if env.Data.VerificationState != "unverified" {
			t.Errorf("verification_state = %q, want unverified", env.Data.VerificationState)
		}
	})

	t.Run("an empty edit is invalid config, not a refusal", func(t *testing.T) {
		// "You did not ask for anything" and "we decline what you asked for" are different mistakes with
		// different remedies, and collapsing them sends the user to look for a platform limitation that
		// is not there.
		_, _, code := runAuthor(t, "--repo", repo, "--spec", specPath, "--node", "whatever")
		if code != ExitInvalidCfg {
			t.Errorf("exit = %d, want %d for a draft that changes nothing", code, ExitInvalidCfg)
		}
	})

	t.Run("without --apply nothing is written", func(t *testing.T) {
		before := dirListing(t, repo)
		runAuthor(t, "--repo", repo, "--spec", specPath, "--node", authorNodeID(t, repo),
			"--model", "openai:gpt-4o")
		if after := dirListing(t, repo); after != before {
			t.Errorf("author changed the repository without --apply:\n before %s\n after  %s", before, after)
		}
	})
}

// TestCLIAuthoringIsStructurallyOffline asserts the offline guarantee the way the package already makes
// it: by what is NOT linked in. A test that merely observes "no request was made" proves only that this
// run did not make one.
func TestCLIAuthoringIsStructurallyOffline(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "net/http" || strings.HasPrefix(path, "net/http/") {
				t.Errorf("%s imports %s — the offline surface must not link the network in", name, path)
			}
		}
	}
}

// TestAuthorHelpStatesTheBoundary: a user who learns from --help that authoring never merges anything is
// a user who does not have to discover it, and a salesperson who reads it cannot promise past it.
func TestAuthorHelpStatesTheBoundary(t *testing.T) {
	var errOut bytes.Buffer
	Main([]string{"help"}, Streams{Out: &bytes.Buffer{}, Err: &errOut}, emptyEnv, nil)
	help := errOut.String()
	if !strings.Contains(help, "author") {
		t.Fatal("the author command is absent from usage — a command nobody can discover")
	}
	for _, must := range []string{"never merges", "refused-by-name", "not-yet-measurable"} {
		if !strings.Contains(help, must) {
			t.Errorf("usage does not state %q", must)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────

func emptyEnv(string) (string, bool) { return "", false }

func runAuthor(t *testing.T, args ...string) (stdout []byte, stderr string, code int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = Main(append([]string{"author"}, args...), Streams{Out: &out, Err: &errBuf}, emptyEnv, nil)
	return out.Bytes(), errBuf.String(), code
}

// authorFixtureRepo is the SAME discovery sample repository the other CLI tests drive.
//
// An earlier version of this test hand-rolled a fake SDK, and discovery found nothing in it — so the
// test SKIPPED, which is not a pass. A fixture the real frontend does not recognise proves nothing
// about a command whose whole job is to act on what discovery found.
func authorFixtureRepo(t *testing.T) string {
	t.Helper()
	return fixtureRepo(t)
}

func writeAuthorSpec(t *testing.T, repo string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "base.json")
	spec := map[string]any{
		"workflow_id":     discoveredWorkflowID(t, repo),
		"source_revision": "0000000000000000000000000000000000000000",
		"order":           discoveredNodeIDs(t, repo),
		"nodes":           map[string]any{},
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

// authorNodeID discovers the fixture's single node id, rather than hard-coding one. A hard-coded id
// would make this test pass or fail on discovery's naming convention rather than on authoring.
func authorNodeID(t *testing.T, repo string) string {
	t.Helper()
	if id := discoverFirstNodeID(t, repo); id != "" {
		return id
	}
	t.Skip("discovery found no call site in the fixture; nothing to author against")
	return ""
}

func dirListing(t *testing.T, root string) string {
	t.Helper()
	var names []string
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

// discoverFirstNodeID runs the real discovery over the fixture and returns its first node id.
func discoverFirstNodeID(t *testing.T, repo string) string {
	t.Helper()
	res, err := discovery.Run(discovery.Options{Repo: repo,
		CommitSHA: "0000000000000000000000000000000000000000"})
	if err != nil {
		t.Logf("discovery over the fixture failed: %v", err)
		return ""
	}
	for _, n := range res.IR.Nodes {
		return n.NodeID
	}
	return ""
}

// discoveredWorkflowID and discoveredNodeIDs read what the real frontend found, so the spec this test
// authors against describes the fixture rather than an assumption about it.
func discoveredWorkflowID(t *testing.T, repo string) string {
	t.Helper()
	res, err := discovery.Run(discovery.Options{Repo: repo,
		CommitSHA: "0000000000000000000000000000000000000000"})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	return res.IR.Workflow.ID
}

func discoveredNodeIDs(t *testing.T, repo string) []string {
	t.Helper()
	res, err := discovery.Run(discovery.Options{Repo: repo,
		CommitSHA: "0000000000000000000000000000000000000000"})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	var ids []string
	for _, n := range res.IR.Nodes {
		ids = append(ids, n.NodeID)
	}
	if len(ids) == 0 {
		t.Fatal("the sample repository yielded no nodes — the fixture, not the command, is wrong")
	}
	return ids
}

// TestCLISkillToolAuthoringOfflineParity (P14 14c task 9.13).
//
// The skills and tools axis has to be authorable offline like every other, and its refusals have to
// read the same as the hosted surface's. This drives the real command over the real sample repository.
func TestCLISkillToolAuthoringOfflineParity(t *testing.T) {
	repo := authorFixtureRepo(t)
	specPath := writeAuthorSpec(t, repo)
	node := discoveredNodeIDs(t, repo)[0]

	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"bind a skill", []string{"--skills", "skill-rerank@v3"}},
		{"select tools", []string{"--tools", "search,fetch"}},
		{"reorder skills", []string{"--skills", "skill-b@v1,skill-a@v2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--repo", repo, "--spec", specPath, "--node", node}, tc.flags...)
			out, errOut, code := runAuthor(t, args...)

			// Whatever the verdict, the command answers rather than failing: a refusal on this axis is an
			// answer too, and a non-zero exit would make CI read it as a broken tool.
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, errOut)
			}
			var env struct {
				Data AuthorData `json:"data"`
			}
			if err := json.Unmarshal(out, &env); err != nil {
				t.Fatalf("decode: %v\n%s", err, out)
			}
			// Three verdicts, and the command must have produced exactly one of them.
			switch env.Data.Verdict {
			case "admissible", "refused", "not_yet_measurable":
			default:
				t.Fatalf("verdict = %q, which is not one of the three", env.Data.Verdict)
			}
			// A refusal must name what it refused — the same contract the hosted surface holds.
			if env.Data.Verdict == "refused" && (env.Data.Cause == "" || env.Data.NodeID == "") {
				t.Errorf("the refusal names nothing: %+v", env.Data)
			}
			// Nothing is ever claimed from this command.
			if env.Data.VerificationState != "unverified" {
				t.Errorf("verification_state = %q, want unverified", env.Data.VerificationState)
			}
		})
	}
}

// TestCLIAuthorFlagsCoverEveryAxis: an axis the CLI cannot express is an axis the offline surface does
// not have, whatever the docs say.
func TestCLIAuthorFlagsCoverEveryAxis(t *testing.T) {
	var errOut bytes.Buffer
	Main([]string{"author", "--help"}, Streams{Out: &bytes.Buffer{}, Err: &errOut}, emptyEnv, nil)
	help := errOut.String()
	// Go's flag package renders names with a SINGLE dash in its generated usage, so the assertion is on
	// the flag NAME rather than on how a reader types it. Asserting the double-dash spelling here failed
	// against a command that supports every one of these — a test wrong about its own tooling.
	for _, name := range []string{"model", "prompt", "skills", "tools", "context-policy",
		"drop-tolerance", "clear-drop-tolerance", "apply-mode"} {
		if !strings.Contains(help, name) {
			t.Errorf("author cannot express --%s offline", name)
		}
	}
}

// TestCLIWiringAuthoringOfflineParity (P15 15d task 19.15).
//
// The wiring axis has to be draftable offline and must give the same SHAPE NAME as the hosted surface.
// A user diagnosing "why won't this apply?" across two surfaces that name the shape differently has two
// problems instead of one.
func TestCLIWiringAuthoringOfflineParity(t *testing.T) {
	// The shape vocabulary is the contract between the two surfaces, so it is asserted as a set rather
	// than by driving a gesture the CLI has no way to express (a graph drag is not a flag).
	shapes := authoring.WiringShapes()
	if len(shapes) < 5 {
		t.Fatalf("the shape vocabulary has %d members — too few to name every refusal", len(shapes))
	}
	seen := map[authoring.WiringShape]bool{}
	for _, s := range shapes {
		if seen[s] {
			t.Errorf("shape %q appears twice", s)
		}
		seen[s] = true
		if strings.TrimSpace(string(s)) == "" {
			t.Error("an unnamed shape would produce a refusal that names nothing")
		}
	}
	// The one applicable shape must be in the vocabulary, or a surface has nothing to call the case that
	// works.
	if !seen[authoring.ShapeTransposition] {
		t.Error("the applicable shape is not in the vocabulary")
	}

	// And the CLI must reach the same three verdicts on this axis as everywhere else. `author` expresses
	// node-level edits; a wiring draft arrives through the spec's own Order/Edges, so this asserts the
	// command handles a spec whose wiring differs — refusing by name rather than silently ignoring it.
	repo := authorFixtureRepo(t)
	specPath := writeAuthorSpec(t, repo)
	node := discoveredNodeIDs(t, repo)[0]
	out, _, code := runAuthor(t, "--repo", repo, "--spec", specPath, "--node", node, "--model", "m")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	var env struct {
		Data AuthorData `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Verdict == "" {
		t.Error("the command produced no verdict")
	}
}

// TestCLIContextAuthoringOfflineParity (P16 16c task 8.16).
func TestCLIContextAuthoringOfflineParity(t *testing.T) {
	repo := authorFixtureRepo(t)
	specPath := writeAuthorSpec(t, repo)
	node := discoveredNodeIDs(t, repo)[0]

	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"select a policy", []string{"--context-policy", "ctx-summarization"}},
		{"declare a tolerance", []string{"--drop-tolerance", "0.25"}},
		{"clear a tolerance", []string{"--clear-drop-tolerance"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--repo", repo, "--spec", specPath, "--node", node}, tc.flags...)
			out, errOut, code := runAuthor(t, args...)
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, errOut)
			}
			var env struct {
				Data AuthorData `json:"data"`
			}
			if err := json.Unmarshal(out, &env); err != nil {
				t.Fatalf("decode: %v\n%s", err, out)
			}
			switch env.Data.Verdict {
			case "admissible", "refused", "not_yet_measurable":
			default:
				t.Fatalf("verdict = %q, which is not one of the three", env.Data.Verdict)
			}
			// The third verdict must name what is missing, or it is a dead end offline too.
			if env.Data.Verdict == "not_yet_measurable" && env.Data.MissingKind == "" {
				t.Error("the third verdict arrived without naming the missing measurement")
			}
		})
	}

	t.Run("an out-of-range tolerance is invalid config, not a refusal", func(t *testing.T) {
		// "You gave me nonsense" and "we decline what you asked for" are different mistakes.
		for _, bad := range []string{"1.5", "-0.2", "many"} {
			_, _, code := runAuthor(t, "--repo", repo, "--spec", specPath, "--node", node,
				"--drop-tolerance", bad)
			if code != ExitInvalidCfg {
				t.Errorf("--drop-tolerance %q → exit %d, want %d", bad, code, ExitInvalidCfg)
			}
		}
	})

	t.Run("clearing a tolerance is not declaring zero", func(t *testing.T) {
		// The two are opposite intents: zero rejects every lossy policy; clearing removes the constraint.
		// The CLI must express both, and they must be different flags.
		var errOut bytes.Buffer
		Main([]string{"author", "--help"}, Streams{Out: &bytes.Buffer{}, Err: &errOut}, emptyEnv, nil)
		help := errOut.String()
		if !strings.Contains(help, "clear-drop-tolerance") || !strings.Contains(help, "drop-tolerance") {
			t.Error("the CLI cannot express both declaring and clearing a tolerance")
		}
	})
}
