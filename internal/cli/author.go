package cli

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/authoringwire"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// author.go is the `author` command: make a change YOURSELF, offline, and be told what will happen
// before anything is written (P13 13c task 12.1).
//
// # Why the offline surface has to carry this at all
//
// The CLI is offline-first and free on every plan: discovery, apply and eval work with no account and
// no network. Authoring that existed only in the hosted console would make the cheapest, most obvious
// edits — pin the model this team already standardised on, bind the prompt version you know is right —
// the one thing you could not do without signing up. That is exactly backwards.
//
// # Why the answer must be the SAME answer, word for word
//
// This command runs the real resolver and probes the real codemod through `authoringwire`. It does not
// re-derive "can this be applied?" from the resolved config, and it carries no copy of the refusal text.
// A user diagnosing "why won't this apply?" across the console and the CLI, and getting two different
// sentences for one cause, has two problems instead of one — so the cause is rendered verbatim from the
// engine on both paths, and a test compares them rather than eyeballing them.
//
// # What it deliberately does not do
//
// It does not submit to a platform, does not record an audit row, and does not open a pull request. All
// three need an account, and this is the half that must work without one. `--apply` writes the diff to a
// file and stops — the destructive step stays in the developer's hands, exactly as `apply` already does.

// AuthorData is the machine payload for `author`.
//
// The three verdicts are ONE closed field rather than a boolean plus an optional reason. A consumer that
// receives two values can only render two states, and the third — "we have not measured this yet" — is
// the one that gets lost, collapsing into a refusal that blames the user for a gap that is ours.
type AuthorData struct {
	// Verdict is "admissible" | "refused" | "not_yet_measurable".
	Verdict string `json:"verdict"`
	// Cause, NodeID, Field and Shape are populated on a refusal, verbatim from the engine.
	Cause  string `json:"cause,omitempty"`
	NodeID string `json:"node_id,omitempty"`
	Field  string `json:"field,omitempty"`
	Shape  string `json:"shape,omitempty"`
	// MissingKind / MissingSubject are populated on not_yet_measurable and name what would resolve it.
	MissingKind    string `json:"missing_kind,omitempty"`
	MissingSubject string `json:"missing_subject,omitempty"`
	// ConfigHash is the hash the change WOULD have, on an admissible verdict.
	ConfigHash string   `json:"config_hash,omitempty"`
	Dimensions []string `json:"dimensions,omitempty"`
	Nodes      []string `json:"nodes,omitempty"`
	// VerificationState is always "unverified" from this command: nothing here runs the harness, so
	// nothing here may claim a result. Emitted rather than omitted, so a script cannot read its absence
	// as "fine".
	VerificationState string `json:"verification_state"`
	// DiffPath / DiffHash are set when --apply wrote a diff.
	DiffPath string `json:"diff_path,omitempty"`
	DiffHash string `json:"diff_hash,omitempty"`
	// Isolation records how the working copy was isolated, as `apply` reports it.
	Isolation string `json:"isolation,omitempty"`
}

// Author runs the authoring preflight offline and, with --apply, writes the resulting diff.
func Author(cfg Config, s Streams) error {
	specPath, err := cfg.Require("spec")
	if err != nil {
		return err
	}
	nodeID, err := cfg.Require("node")
	if err != nil {
		return err
	}
	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}

	base, err := loadSpec(specPath)
	if err != nil {
		return err
	}
	edit, err := editFromFlags(cfg)
	if err != nil {
		return err
	}
	if edit.Empty() {
		return invalidConfig("author: nothing to change — pass at least one of --model, --prompt, " +
			"--context-policy, --skills, --tools, --apply-mode, --drop-tolerance or --clear-drop-tolerance")
	}

	_, sha := repoIdentity(repo, cfg.Get("repo-url"), cfg.Get("commit"), s)
	if base.SourceRevision == "" {
		base.SourceRevision = sha
	}

	s.Narratef("author: discovering %s to resolve the change against its IR…", repo)
	res, err := discovery.Run(discovery.Options{Repo: repo, ConfigPath: cfg.Get("config"),
		WorkflowID: cfg.Get("workflow-id"), CommitSHA: sha})
	if err != nil {
		return operational("author: discovery failed", err)
	}
	ir := res.IR

	// The isolated copy is what the probe reads. Preflight must not read the caller's working tree: a
	// probe that saw uncommitted edits would answer about a configuration that is not the one being
	// authored.
	isolatedDir, isolation, cleanup, err := isolate(repo, base.SourceRevision, s)
	if err != nil {
		return operational("author: could not create an isolated working copy", err)
	}
	defer cleanup()

	draft := authoring.Draft{
		WorkflowID: base.WorkflowID,
		// Offline, the parent is the spec on disk. There is no head to race against and no account to
		// attribute to, so the concurrency check and the audit row are the hosted path's — ABSENT here
		// rather than skipped here, which is why Submitter is not used.
		ParentVariantID: firstNonEmptyStr(base.ParentVariantID, "local"),
		Edits:           map[string]authoring.Edit{nodeID: edit},
	}

	pre := authoring.Preflighter{
		Resolver:     &localResolver{ir: &ir},
		Materializer: authoringwire.Materializer{Root: isolatedDir},
	}
	verdict, err := pre.Preflight(context.Background(), draft, base)
	if err != nil {
		return operational("author: running the preflight", err)
	}

	data := AuthorData{
		Verdict: string(verdict.Verdict), Cause: verdict.Refusal.Cause, NodeID: verdict.Refusal.NodeID,
		Field: verdict.Refusal.Field, Shape: verdict.Refusal.Shape,
		MissingKind: verdict.Missing.Kind, MissingSubject: verdict.Missing.Subject,
		ConfigHash: verdict.ConfigHash, Dimensions: verdict.Dimensions, Nodes: verdict.Nodes,
		VerificationState: string(authoring.StateUnverified),
	}

	switch verdict.Verdict {
	case authoring.VerdictRefused:
		// 🔴 A refusal is a VERDICT, not a command failure. It exits OK with the answer on the machine
		// stream: the caller asked a question and got one, and a non-zero exit would make every CI
		// wrapper treat "the platform declined this" as "the tool broke".
		s.Narratef("author: declined — %s", verdict.Refusal.Cause)
		s.Narratef("author: nothing was written. There is no override for this, on any plan or role.")
		return s.EmitJSON("author", ExitOK, data, nil, nil)
	case authoring.VerdictNotYetMeasurable:
		s.Narratef("author: not yet measurable — %s has never been measured for %s",
			firstNonEmptyStr(verdict.Missing.Kind, "an input this change needs"),
			firstNonEmptyStr(verdict.Missing.Subject, verdict.Missing.NodeID))
		s.Narratef("author: that is a gap in our measurements, not a problem with your change. It is " +
			"neither a refusal nor an approval — run an evaluation on this node to collect it.")
		return s.EmitJSON("author", ExitOK, data, nil, nil)
	}

	s.Narratef("author: admissible — config_hash %s", verdict.ConfigHash)
	if cfg.Get("apply") != "true" {
		s.Narratef("author: pass --apply to write the diff. Nothing was written.")
		return s.EmitJSON("author", ExitOK, data, nil, nil)
	}

	spec, err := draft.Derive(base)
	if err != nil {
		return invalidConfig("author: deriving the authored spec: " + err.Error())
	}
	resolved, err := variantspec.Resolve(context.Background(), spec, &ir, noopRegistries{})
	if err != nil {
		return invalidConfig("author: cannot resolve the authored spec offline: " + err.Error())
	}
	patch, err := transform.Generate(resolved, isolatedDir)
	if err != nil {
		// Reaching here means preflight said admissible and the codemod then refused — the two have
		// drifted, which is exactly what `authoringwire` exists to make impossible. Reported as an
		// operational failure rather than a refusal, because a refusal at this point is a BUG.
		return operational("author: the codemod refused a change preflight admitted — "+
			"preflight and the transform have drifted", err)
	}
	out := cfg.Get("out")
	if out == "" {
		out = "authored.diff"
	}
	if err := os.WriteFile(out, patch.Diff, 0o600); err != nil {
		return operational("author: writing the diff", err)
	}
	data.DiffPath, data.DiffHash, data.Isolation = out, patch.DiffHash, isolation
	s.Narratef("author: reviewable diff → %s; your working tree is untouched (apply it with `git apply %s`)",
		out, out)
	s.Narratef("author: this change is UNVERIFIED — no quality or cost claim attaches to it until a " +
		"multi-seed evaluation has run.")
	return s.EmitJSON("author", ExitOK, data, nil, nil)
}

// editFromFlags builds the per-node edit. Every field is a pointer because "leave this alone" and "clear
// this" are different instructions, and a value type can express only one of them.
func editFromFlags(cfg Config) (authoring.Edit, error) {
	var e authoring.Edit
	if v := cfg.Get("model"); v != "" {
		e.ModelRef = &v
	}
	if v := cfg.Get("prompt"); v != "" {
		e.PromptRef = &v
	}
	if v := cfg.Get("context-policy"); v != "" {
		e.ContextPolicy = &v
	}
	if v := cfg.Get("skills"); v != "" {
		refs := splitList(v)
		e.SkillRefs = &refs
	}
	if v := cfg.Get("tools"); v != "" {
		keep := splitList(v)
		e.ToolSelection = &keep
	}
	if v := cfg.Get("apply-mode"); v != "" {
		mode := variantspec.ApplyMode(v)
		e.ApplyMode = &mode
	}
	// Declaring and CLEARING a tolerance are two different edits, and the second is not "declare 0" —
	// zero tolerance rejects every lossy policy, which is the opposite of removing the constraint.
	if cfg.Get("clear-drop-tolerance") == "true" {
		var none *float64
		e.DropTolerance = &none
	} else if v := cfg.Get("drop-tolerance"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			return e, invalidConfig("author: --drop-tolerance must be a fraction between 0 and 1")
		}
		p := &f
		e.DropTolerance = &p
	}
	return e, nil
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// localResolver resolves against the discovered IR with no network and no account. It is the same
// variantspec.Resolve every other path uses; only where the IR comes from differs.
type localResolver struct{ ir *discovery.IR }

func (l *localResolver) Resolve(spec *variantspec.VariantSpec) (*variantspec.Resolved, error) {
	return variantspec.Resolve(context.Background(), spec, l.ir, noopRegistries{})
}
