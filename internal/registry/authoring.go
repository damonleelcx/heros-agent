package registry

import (
	"context"
	"fmt"
	"time"
)

// Prompt authoring read models (P10 tasks 2.4, 2.5).
//
// # Why these live here and add no table
//
// The timeline and the diff are the "missing half" of prompt versioning: a person needs to see a
// name's history and what changed between two versions before adopting one (proposal §"There is also
// no history"). Both are **read models over rows that already exist** — prompt_entry, one row per
// published version, keyed by content and stamped with created_at. The standing constraint is explicit
// that P10 adds no registry table (tasks.md), so nothing here writes; every function is a pure read.
//
// They live on registry.Store because that is the one place that already knows how a prompt version is
// shaped — its envelope, its slot set, its body blob. A second reader that re-decoded envelopes would
// be a second definition of "what a prompt version is," and the two would drift the first time the
// envelope grows a field. (Lineage stays a name-grouped read model, never a stored derived_from — see
// openspec/changes/p10-prompt-model-studio/decisions.md D-1.3.)

// StudioRender renders a prompt version against supplied bindings and returns the exact string a run
// would send (P10 tasks 4.6, 6.1). It calls the SAME Template.Render the runtime uses — not a second
// renderer — so the previewed string is byte-identical to what a run sends, structurally rather than
// by testing for it. A missing binding for a declared slot, or a binding for an undeclared slot, is a
// loud error naming the offending slot (Render's own contract); nothing partially-rendered is returned.
func (s *Store) StudioRender(ctx context.Context, versionID string, bindings map[string]string) (string, error) {
	entry, err := s.ResolvePrompt(ctx, versionID)
	if err != nil {
		return "", err
	}
	return entry.Template.Render(bindings)
}

// PromptTimelineEntry is one version in a prompt name's history (task 2.4). Ordered oldest-first by
// the timeline, each entry carries what a reader needs to pick one: its content-addressed id, the
// declared slot set (what alters where the prompt can be applied), and when it was published.
type PromptTimelineEntry struct {
	VersionID string    `json:"version_id"`
	Name      string    `json:"name"`
	Slots     []string  `json:"slots"`
	CreatedAt time.Time `json:"created_at"`
}

// PromptNames returns the distinct prompt names beginning with prefix, sorted (the prompt browser's
// list — task 4.1). The prefix is the tenant scope (`t:<tenant>/`), applied server-side so a tenant
// only ever enumerates its own prompts. A read model over existing rows; no new table.
func (s *Store) PromptNames(ctx context.Context, prefix string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT name FROM prompt_entry WHERE name LIKE $1 ORDER BY name`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("registry: prompt names %q: %w", prefix, err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("registry: prompt names %q: scan: %w", prefix, err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registry: prompt names %q: %w", prefix, err)
	}
	return out, nil
}

// PromptTimeline returns every version of a prompt name, oldest first, each with its slot set and
// creation metadata (task 2.4).
//
// A name with no versions returns an empty, non-nil slice and a nil error — an empty timeline is a
// legitimate answer distinguishable from a failure to retrieve one (spec: "A name with no versions is
// an empty timeline, not an error"). The slot set is decoded from the stored envelope, not re-derived
// from the body, so it is exactly the interface the version pinned at publish.
func (s *Store) PromptTimeline(ctx context.Context, name string) ([]PromptTimelineEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT version_id, envelope, created_at FROM prompt_entry
		 WHERE name = $1 ORDER BY created_at, version_id`, name)
	if err != nil {
		return nil, fmt.Errorf("registry: prompt timeline %q: %w", name, err)
	}
	defer func() { _ = rows.Close() }()

	out := []PromptTimelineEntry{} // never nil: an empty timeline is a result, not a decode failure
	for rows.Next() {
		var (
			versionID string
			envelope  []byte
			createdAt time.Time
		)
		if err := rows.Scan(&versionID, &envelope, &createdAt); err != nil {
			return nil, fmt.Errorf("registry: prompt timeline %q: scan: %w", name, err)
		}
		var spec PromptSpec
		// decodeEnvelope re-verifies the content address on the way out, so a row a bypassing writer
		// corrupted is caught here rather than shown as history.
		if _, err := decodeEnvelope(KindPrompt, versionID, envelope, &spec); err != nil {
			return nil, err
		}
		slots := spec.Slots
		if slots == nil {
			slots = []string{}
		}
		out = append(out, PromptTimelineEntry{
			VersionID: versionID, Name: name, Slots: slots, CreatedAt: createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registry: prompt timeline %q: %w", name, err)
	}
	return out, nil
}

// PromptVersionDiff reports the difference between two prompt versions (task 2.5).
//
// The slot-set change is reported **separately from the body text** and explicitly — SlotsAdded and
// SlotsRemoved are not left to be inferred from BodyDiff. A slot change is what alters *where a prompt
// can be applied* and is nearly invisible inside a body diff (spec, design.md), so a consumer must be
// able to see it without reading the body diff at all.
type PromptVersionDiff struct {
	VersionA string `json:"version_a"`
	VersionB string `json:"version_b"`
	// BodyChanged is true iff the two bodies differ. Distinguishes a pure-wording edit from a no-op and
	// from a slot-only change.
	BodyChanged bool `json:"body_changed"`
	// BodyDiff is the line-level difference between the two bodies, deterministic (LCS over lines).
	BodyDiff []DiffLine `json:"body_diff"`
	// SlotsAdded are slots B declares that A did not; SlotsRemoved the reverse. Sorted. Reported even
	// when BodyDiff would imply them, because "identifying it does not require reading the body diff."
	SlotsAdded   []string `json:"slots_added"`
	SlotsRemoved []string `json:"slots_removed"`
}

// DiffOp is one of the three line operations in a body diff.
type DiffOp string

const (
	DiffContext DiffOp = "context" // unchanged, present in both
	DiffAdd     DiffOp = "add"     // present only in B
	DiffRemove  DiffOp = "remove"  // present only in A
)

// DiffLine is one line of a body diff with its operation.
type DiffLine struct {
	Op   DiffOp `json:"op"`
	Text string `json:"text"`
}

// DiffPromptVersions diffs two prompt versions (task 2.5). Either id may be any published version of
// any name — the diff is over content, so cross-name diffs are meaningful and not refused.
func (s *Store) DiffPromptVersions(ctx context.Context, versionA, versionB string) (*PromptVersionDiff, error) {
	bodyA, slotsA, err := s.promptBodyAndSlots(ctx, versionA)
	if err != nil {
		return nil, err
	}
	bodyB, slotsB, err := s.promptBodyAndSlots(ctx, versionB)
	if err != nil {
		return nil, err
	}

	added, removed := setDiff(slotsA, slotsB)
	diff := &PromptVersionDiff{
		VersionA: versionA, VersionB: versionB,
		BodyChanged:  bodyA != bodyB,
		BodyDiff:     lineDiff(bodyA, bodyB),
		SlotsAdded:   added,
		SlotsRemoved: removed,
	}
	return diff, nil
}

// promptBodyAndSlots returns a version's raw body and its declared slot set. The raw body is fetched
// from the blob store (the diff is over exactly the published bytes), and the slots come from the
// pinned envelope, not from re-parsing — the two agree because the trigger in 0002 enforces it.
func (s *Store) promptBodyAndSlots(ctx context.Context, versionID string) (string, []string, error) {
	entry, err := s.ResolvePrompt(ctx, versionID)
	if err != nil {
		return "", nil, err
	}
	body, err := s.blobs.Get(ctx, entry.Spec.BodyBlobHash)
	if err != nil {
		return "", nil, fmt.Errorf("registry: prompt %s body: %w", versionID, err)
	}
	return string(body), entry.Spec.Slots, nil
}
