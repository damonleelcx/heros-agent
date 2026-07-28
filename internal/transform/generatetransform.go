package transform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// GenerateTransform is P5's source-transformation entrypoint (task 2.4, ADR-001 Decision 8). It turns a
// coherent (possibly adapter-augmented) Variant Spec into a deterministic, AST-level codemod:
//
//   - the per-node DIMENSION overrides are rewritten by the existing engine (Generate) — byte-splice
//     value replacement, minimality-gated;
//   - each INSERTED ADAPTER is materialised as a generated, inspectable Go source file (EmitAdapter) —
//     the ADR-001 realisation of "adapter auto-insertion is a generated code change".
//
// The two are merged into ONE Patch with one deterministic diff and one content hash, so the same
// {config_hash, source_revision} against the same source yields a byte-identical diff (task 2.4). It is
// a PURE function of (resolved spec, inserted adapters, bytes at root); it writes nothing — application
// to an isolated worktree + the build-preserving gate are the worktree layer's job (tasks 2.5, 2.6),
// deliberately separate so validation and generation stay upstream of any side effect.
//
// resolved may be nil when the spec carries no dimension overrides (a pure re-arrangement): the adapter
// files are still generated. spec.InsertedAdapters carries the adapters GateReorder recorded.
func GenerateTransform(resolved *variantspec.Resolved, spec *variantspec.VariantSpec, root string) (*Patch, error) {
	if spec == nil {
		return nil, fmt.Errorf("transform: GenerateTransform requires a variant spec")
	}

	// 🔴 P15 task 4.2 — the wiring gate, before any file is read or any adapter is emitted. The spec's
	// own Order/Edges are the statement of the desired graph here (Generate sees only the resolved
	// projection), and an inserted adapter is collapsed out first: an adapter IS materialized, in this
	// very function, so it is not the un-materializable rewire this refusal is about.
	//
	// No diff is produced on this path — the refusal returns before base is built — which is task 4.3's
	// "no diff is emitted for a refused spec", structurally rather than by convention.
	plan, err := checkWiring(resolved, spec)
	if err != nil {
		return nil, err
	}

	// 1. Dimension overrides (reuses the whole existing, tested value-rewrite path).
	//
	// Generate is also where a materializable wiring transposition is emitted (15c task 15.2), so it is
	// called when there is a PLAN even if there is no override — a pure reorder is a real change with a
	// real diff, and routing it through the same function keeps one emission path rather than two.
	var base *Patch
	if resolved != nil && (len(resolved.Overrides) > 0 || plan != nil) {
		p, err := Generate(resolved, root)
		if err != nil {
			return nil, err
		}
		base = p
	} else {
		base = &Patch{Files: map[string][]byte{}}
		if resolved != nil {
			base.ConfigHash, base.SourceRevision = resolved.ConfigHash, resolved.SourceRevision
		} else {
			base.SourceRevision = spec.SourceRevision
		}
	}

	// 2. Inserted adapters → generated source files. Sorted by adapter node id so emission — and the
	// resulting diff — is deterministic regardless of the slice's order.
	language := ""
	if resolved != nil {
		language = resolved.Language
	}
	adapters := append([]variantspec.InsertedAdapter(nil), spec.InsertedAdapters...)
	sort.Slice(adapters, func(i, j int) bool { return adapters[i].AdapterNodeID < adapters[j].AdapterNodeID })

	var adapterDiffs []byte
	for _, a := range adapters {
		if language == "" {
			// Without a resolved spec we cannot know the target language; adapters are a source change
			// and a source change needs a language. Fail loudly rather than emit into the void.
			return nil, fmt.Errorf("transform: cannot emit adapter %q without a resolved spec to supply the workflow language", a.AdapterNodeID)
		}
		gf, err := EmitAdapter(a, language)
		if err != nil {
			return nil, err
		}
		if _, exists := base.Files[gf.Path]; exists {
			return nil, fmt.Errorf("transform: adapter %q would overwrite an existing generated file %s", a.AdapterNodeID, gf.Path)
		}
		base.Files[gf.Path] = gf.Content
		adapterDiffs = append(adapterDiffs, newFileDiff(gf.Path, gf.Content)...)
		base.Touched = append(base.Touched, TouchedDimension{
			NodeID: a.AdapterNodeID, Dim: "adapter:" + a.CatalogKind, File: gf.Path, Line: 1})
	}

	// 3. Bound apply mode (P10 §7): for every node the spec marks `bound`, emit the binding document
	// and the generated accessor into the SAME patch as the call-site rewrite, so one revert restores
	// all three parts (task 7.5). A bound node with no resolved values is rejected here (task 7.3),
	// before the transform is proposed or run — the same footing as a failed build.
	var boundDiffs []byte
	if resolved != nil {
		boundFiles, err := GenerateBoundArtifacts(resolved)
		if err != nil {
			return nil, err
		}
		for _, path := range sortedFileKeys(boundFiles) {
			if _, exists := base.Files[path]; exists {
				return nil, fmt.Errorf("transform: bound artifact would overwrite an existing generated file %s", path)
			}
			base.Files[path] = boundFiles[path]
			boundDiffs = append(boundDiffs, newFileDiff(path, boundFiles[path])...)
			base.Touched = append(base.Touched, TouchedDimension{NodeID: "", Dim: "bound-artifact", File: path, Line: 1})
		}
	}

	// 4. Combine diffs: existing-file edits first (already in Patch.Diff, path-sorted), then adapter
	// new-file diffs (adapter-id-sorted), then bound artifacts (path-sorted). Recompute the content
	// hash over the whole.
	base.Diff = append(base.Diff, adapterDiffs...)
	base.Diff = append(base.Diff, boundDiffs...)
	sum := sha256.Sum256(base.Diff)
	base.DiffHash = hex.EncodeToString(sum[:])
	return base, nil
}

// sortedFileKeys returns a file map's paths sorted, so emission order (and the diff) is deterministic.
func sortedFileKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PreviewAdapterDiff renders the reviewable source diff a single inserted adapter would generate,
// without touching disk — this is what the editor previews next to an adapter-inserted state (task 3.3).
// It is the same deterministic emission GenerateTransform uses, so the preview and the committed diff
// match byte-for-byte.
func PreviewAdapterDiff(a variantspec.InsertedAdapter, language string) (path, diff string, err error) {
	gf, err := EmitAdapter(a, language)
	if err != nil {
		return "", "", err
	}
	return gf.Path, string(newFileDiff(gf.Path, gf.Content)), nil
}

// newFileDiff renders a deterministic unified diff for a wholly new file: `--- /dev/null` to `+++ b/path`
// with every line added. Hand-rolled for the same reason as unifiedDiff — the artifact is content-hashed
// and must not depend on the local git version (task 2.4).
func newFileDiff(path string, content []byte) []byte {
	lines := splitLines(content)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--- /dev/null\n")
	fmt.Fprintf(&buf, "+++ b/%s\n", path)
	fmt.Fprintf(&buf, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		fmt.Fprintf(&buf, "+%s\n", l)
	}
	return buf.Bytes()
}

// RejectedTransform is the typed outcome of the build-preserving gate (task 2.5, Decision 8): a codemod
// that fails to compile/build the target is rejected BEFORE it is proposed, so no broken diff ever
// reaches the user. It is distinct from a schema-coherence rejection (typedcontract.Verdict) — that one
// runs before any transform exists; this one runs after generation, on the built artifact.
//
// It carries the build error and, when the gate could attribute it, the node/dimension the failing edit
// belongs to, so the UI can point the user at the cause rather than the whole diff. The worktree layer
// (worktree.BuildRejection) produces the raw build failure; NewRejectedTransform lifts it into this
// typed shape without a package cycle (transform must not import worktree).
type RejectedTransform struct {
	BuildError string
	NodeID     string
	Dim        string
}

func (r *RejectedTransform) Error() string {
	if r.NodeID != "" {
		return fmt.Sprintf("transform rejected before proposal: node %q (%s) fails to build: %s", r.NodeID, r.Dim, r.BuildError)
	}
	return fmt.Sprintf("transform rejected before proposal: %s", r.BuildError)
}

// NewRejectedTransform builds the typed rejection from a build failure. buildErr is the compiler/build
// output; node/dim are the attribution the worktree build gate recovered (may be empty).
func NewRejectedTransform(buildErr, node, dim string) *RejectedTransform {
	return &RejectedTransform{BuildError: buildErr, NodeID: node, Dim: dim}
}
