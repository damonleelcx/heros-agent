package proposal

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Resolver turns a candidate Variant Spec into a resolved config (config_hash + language + resolved
// overrides) the codemod can act on. Production wraps variantspec.Resolve(ctx, spec, ir, registries);
// it is an interface so the codemod/build tests can supply a resolved config without a live registry.
type Resolver interface {
	Resolve(spec *variantspec.VariantSpec) (*variantspec.Resolved, error)
}

// PromptRegistrar registers a rewritten prompt body and returns its registry version_id. A
// prompt-rewrite candidate carries a fresh body (§2); before it can be resolved, the body must become
// a registry entry so the candidate's PromptRef resolves. Nil disables prompt-rewrite compilation.
type PromptRegistrar interface {
	RegisterPrompt(ctx context.Context, body string) (ref string, err error)
}

// Compiled is a candidate that has been resolved to a config_hash, run through the codemod to a
// deterministic source diff, and gated on the build. Surfaceable is false exactly when the diff failed
// to build or the transform refused it — the one signal the ranker and verification gate read to
// enforce ADR-001's "nothing non-building surfaces".
type Compiled struct {
	Candidate   Candidate
	ConfigHash  string
	Patch       *transform.Patch
	DiffHash    string
	BuildStatus BuildStatus
	BuildLog    string
	// Refusal is set exactly when BuildStatus is BuildRefused: the transform declined this change and
	// said why (P14 task 8.2). Zero otherwise.
	Refusal ChangeRefusal
	// IR is the discovered IR the candidate was compiled against, carried so the presentation can tell a
	// PROVIDER TOOL from a PLATFORM SKILL using the split fields that recorded the difference
	// (decisions.md D-14.1). Optional: a nil IR degrades the change surface's NAMES, never its
	// categories, because the dimension already says which of the two a change is.
	IR *discovery.IR
}

// ChangeRefusal is a change the transform declined to make, named.
//
// It exists because "the diff looks complete" is the failure D-14.3 is written against. A refusal that
// travels as an error is a refusal a user never sees; a refusal that travels as a value appears on the
// surface saying which node, which dimension, and why.
type ChangeRefusal struct {
	NodeID string `json:"node_id"`
	// Dimension is the dimension that was refused ("skills", "tools", …), so the surface can say
	// "node X, dim skills" rather than "something went wrong".
	Dimension string `json:"dimension"`
	// Reason is the transform's own sentence, rendered verbatim. It is written to be read by the person
	// who has to decide what to do about it, so it is not re-worded here.
	Reason string `json:"reason"`
}

// Refused reports whether this candidate was declined by the transform.
func (r ChangeRefusal) Refused() bool { return r.Reason != "" }

// Surfaceable reports whether this compiled candidate may be ranked, verified, or presented. A
// build_failed or refused candidate is never surfaceable (§1b.2) — but a refused one is still SHOWN,
// in the withheld section, by name.
func (c Compiled) Surfaceable() bool { return c.BuildStatus == BuildBuilt }

// Compiler resolves, codemods, and build-gates candidates.
type Compiler struct {
	// Resolver maps a candidate spec to a resolved config. Required.
	Resolver Resolver
	// Root is the checked-out source tree the codemod rewrites (read-only — the build check operates on
	// an isolated copy).
	Root string
	// Build is the pre-surface build gate. Required.
	Build BuildChecker
	// Prompts registers rewritten prompt bodies. Required only when compiling prompt-rewrite candidates.
	Prompts PromptRegistrar
	// IR is the discovered IR the candidates were proposed against, carried onto each Compiled so the
	// change surface can name a provider TOOL and a platform SKILL from the split fields that recorded
	// the difference (P14 task 8.1). Optional.
	IR *discovery.IR
}

// Compile resolves the candidate, generates its deterministic source diff, and gates it on the build.
// A candidate whose diff fails to build is returned with BuildStatus == BuildFailed (Surfaceable ==
// false) rather than an error — a build failure is a verdict about the candidate, not a failure of the
// compiler. Determinism (§1b.3): the same candidate against the same Root yields a byte-identical
// Patch, because Resolve and transform.Generate are both pure functions of their inputs.
func (c Compiler) Compile(ctx context.Context, cand Candidate) (Compiled, error) {
	if c.Resolver == nil {
		return Compiled{}, fmt.Errorf("proposal: Compile requires a Resolver")
	}
	if c.Build == nil {
		return Compiled{}, fmt.Errorf("proposal: Compile requires a BuildChecker")
	}

	spec := cand.Spec
	// A prompt-rewrite candidate carries a fresh prompt body; register it so its PromptRef resolves.
	if cand.NewPromptBody != "" {
		if c.Prompts == nil {
			return Compiled{}, fmt.Errorf("proposal: compiling a prompt rewrite requires a PromptRegistrar")
		}
		ref, err := c.Prompts.RegisterPrompt(ctx, cand.NewPromptBody)
		if err != nil {
			return Compiled{}, fmt.Errorf("proposal: register rewritten prompt: %w", err)
		}
		spec = cloneSpec(spec)
		setPrompt(spec, cand.NodeID, ref)
	}

	resolved, err := c.Resolver.Resolve(spec)
	if err != nil {
		// 🔴 A resolve-time REFUSAL is a verdict about the candidate, not a failure of the compiler. A tool
		// selection over an undiscovered tool (D-14.2) and a spec that cannot be safely rewritten both land
		// here, and returning an error would abort the whole batch over one declined change.
		if ref, ok := refusalFrom(cand, err); ok {
			return Compiled{Candidate: cand, BuildStatus: BuildRefused, Refusal: ref, IR: c.IR}, nil
		}
		return Compiled{}, fmt.Errorf("proposal: resolve candidate: %w", err)
	}
	patch, err := transform.Generate(resolved, c.Root)
	if err != nil {
		if ref, ok := refusalFrom(cand, err); ok {
			return Compiled{Candidate: cand, ConfigHash: resolved.ConfigHash,
				BuildStatus: BuildRefused, Refusal: ref, IR: c.IR}, nil
		}
		return Compiled{}, fmt.Errorf("proposal: codemod: %w", err)
	}

	res, err := c.Build.Check(ctx, patch)
	if err != nil {
		return Compiled{}, fmt.Errorf("proposal: build check: %w", err)
	}
	status := BuildFailed
	if res.Builds {
		status = BuildBuilt
	}
	return Compiled{
		Candidate:   cand,
		ConfigHash:  resolved.ConfigHash,
		Patch:       patch,
		DiffHash:    patch.DiffHash,
		BuildStatus: status,
		BuildLog:    res.Log,
		IR:          c.IR,
	}, nil
}

// refusalFrom recognises a transform/resolve REFUSAL and turns it into a value the surface can render.
//
// It matches on the typed sentinels, never on message text: `ErrUnsafeRewrite` (the transform declined
// to write code it could not stand behind) and `ErrToolNotDiscovered` (a selection over a tool the node
// does not offer). Anything else is a real failure and keeps propagating — mis-classifying an
// infrastructure error as a refusal would report "we chose not to" about something that broke.
func refusalFrom(cand Candidate, err error) (ChangeRefusal, bool) {
	ref := ChangeRefusal{NodeID: cand.NodeID, Reason: err.Error()}
	if len(cand.Dimensions) > 0 {
		ref.Dimension = cand.Dimensions[0]
	}

	var re *transform.RewriteError
	if errors.As(err, &re) {
		if re.NodeID != "" {
			ref.NodeID = re.NodeID
		}
		if re.Dim != "" {
			ref.Dimension = re.Dim
		}
		ref.Reason = re.Detail
		return ref, true
	}
	var se *variantspec.SpecError
	if errors.As(err, &se) && errors.Is(err, variantspec.ErrToolNotDiscovered) {
		ref.NodeID, ref.Dimension, ref.Reason = se.NodeID, string(se.Dim), se.Error()
		return ref, true
	}
	if errors.Is(err, transform.ErrUnsafeRewrite) || errors.Is(err, variantspec.ErrUnsafeRewrite) {
		return ref, true
	}
	return ChangeRefusal{}, false
}
