package proposal

import (
	"context"
	"fmt"

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
// to build — the one signal the ranker and verification gate read to enforce ADR-001's "nothing
// non-building surfaces".
type Compiled struct {
	Candidate   Candidate
	ConfigHash  string
	Patch       *transform.Patch
	DiffHash    string
	BuildStatus BuildStatus
	BuildLog    string
}

// Surfaceable reports whether this compiled candidate may be ranked, verified, or presented. A
// build_failed candidate is never surfaceable (§1b.2).
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
		return Compiled{}, fmt.Errorf("proposal: resolve candidate: %w", err)
	}
	patch, err := transform.Generate(resolved, c.Root)
	if err != nil {
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
	}, nil
}
