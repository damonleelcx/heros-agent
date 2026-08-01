// Package runlink is the P11 egress boundary — the only path by which anything from a customer's
// environment reaches the platform. Everything in this package exists to make one guarantee true by
// CONSTRUCTION rather than by review: the linked payload is built field by field from an explicit
// allowlist, and a field added to an internal run representation is ABSENT from a transmitted payload
// by default (PRD FR11, design Decision 3).
//
// The asymmetry is the whole argument:
//
//	Denylist (serialize + strip) — a new field is SENT. Silent. Discovered externally (by a customer).
//	Allowlist (construct)        — a new field is ABSENT. Visible as a missing feature. Discovered here.
//
// Only one of those directions is acceptable for a boundary carrying customer source, so this package
// never serializes a rich run object. It reads named fields off a source and writes them into a fresh
// payload struct whose JSON is the exact bytes on the wire (see payload.go, BuildPayload).
package runlink

import "strings"

// PlatformBaseURL is the ONE endpoint run linking is allowed to transmit to.
//
// 🔴 Run linking works with https://heros-agent.space and nothing else. This is a hard pin, not a
// default: the CLI refuses to transmit a payload to any other origin (see client.go, assertLinkTarget).
// A customer-review promise — "you can read exactly where this goes" — is only checkable if the
// destination is fixed and named, so it is a constant in the security-critical package, not a flag with
// a default that an environment variable can quietly move.
const PlatformBaseURL = "https://heros-agent.space"

// LinkPath is the authenticated ingest path linked runs are POSTed to, under PlatformBaseURL.
const LinkPath = "/api/v1/run-links"

// ContractVersion is the run-linking payload contract version. It is transmitted in the payload and
// echoed by the platform; a mismatch is loud (PRD FR6), never a silent reinterpretation. The moment a
// customer's pipeline parses the payload it is a public contract, so it is versioned explicitly.
const ContractVersion = "p11.link.v1"

// AllowlistField is one permitted field, named and categorized so the allowlist is a readable
// security-review artifact (task 1.1) rather than a set of struct tags a reviewer must reverse-engineer.
type AllowlistField struct {
	// Name is the wire key (the JSON path root) this field appears under in a linked payload.
	Name string
	// Category groups fields for the review doc: metrics, ir_structure, provenance, scores, run_metadata.
	Category string
	// Why is the one-line justification a reviewer reads: what the platform does with it, and why it is
	// structure/metric rather than content.
	Why string
}

// Allowlist is the complete, ratified set of fields permitted to cross the boundary (PRD FR12). It is
// the SINGLE SOURCE OF TRUTH: payload.BuildPayload writes exactly these keys, the contract doc
// docs/decisions/p11-contracts.md renders from this list, and the egress test asserts a transmitted
// payload carries no key outside it. Change the boundary here, in one place, or not at all.
//
// Never permitted, and deliberately NOT expressible as a field here: prompt text, source code, file
// contents, generated diffs, environment-variable values, provider credentials. Those are content; the
// dashboard's job is comparison, which structure and metrics satisfy (design Decision 4).
var Allowlist = []AllowlistField{
	// ── metrics ────────────────────────────────────────────────────────────────
	{"metrics.cost", "metrics", "Per-node/aggregate provider spend in the customer's own unit — the input SUM is derived from. No markup; the platform neither resells nor reprices these tokens."},
	{"metrics.latency", "metrics", "Per-node/aggregate wall time — the console renders it for comparison."},
	{"metrics.tokens", "metrics", "Prompt/completion token counts — quantities, not the tokens themselves."},
	// ── ir_structure (shape, never content) ──────────────────────────────────────
	{"ir_structure.node_ids", "ir_structure", "Node identifiers — the dashboard needs to know a node EXISTS, not what its prompt said."},
	{"ir_structure.edges", "ir_structure", "Edges between nodes — the workflow's shape, so the console can draw the graph."},
	{"ir_structure.model_refs", "ir_structure", "Model references (e.g. provider/model) — which model a node used, not what it was asked."},
	{"ir_structure.pattern_labels", "ir_structure", "P3.5 pattern labels — the classifier's shape tags, no source text."},
	// ── provenance ───────────────────────────────────────────────────────────────
	{"config_hash", "provenance", "The variant's config hash — a determinism anchor, not the config's contents."},
	{"source_revision", "provenance", "The repo revision the run was computed at — a commit id, not the code."},
	// ── scores + intervals ───────────────────────────────────────────────────────
	{"scores.metric", "scores", "The metric name a score is for (e.g. \"quality\") — a label, not content."},
	{"scores.value", "scores", "Eval score computed by the P4 harness — a number, not the eval-set data behind it."},
	{"scores.ci_low", "scores", "Lower confidence bound — statistical honesty travels with the score (design Decision 8)."},
	{"scores.ci_high", "scores", "Upper confidence bound — so the console shows the interval, never a bare point estimate."},
	// ── run metadata ─────────────────────────────────────────────────────────────
	{"run_metadata.run_id", "run_metadata", "Run identity — the idempotency key; also what the returned console URL resolves to."},
	{"run_metadata.workflow_id", "run_metadata", "Workflow identity — which workflow this run belongs to."},
	{"run_metadata.timestamp", "run_metadata", "When the run happened — for the period a linked event lands in."},
	{"run_metadata.seed", "run_metadata", "The eval seed(s) — a reproducibility number, not run content."},
	{"run_metadata.tool_version", "run_metadata", "The CLI version that produced the run — for the support window (PRD NFR9)."},
	// ── coverage denominator (task 1.7) ──────────────────────────────────────────
	// The ONLY field here that is not about the run being linked: it is the count of runs the CLI
	// observed, so link coverage has a denominator that means something (PRD FR17, open-question Q5). It
	// is a single non-negative integer — a count, never a list of the runs it counts, which would be a
	// second egress surface needing its own scrutiny.
	{"runs_reported", "run_metadata", "How many runs the CLI observed this session — the coverage denominator; a count, never the runs."},
}

// allowlistKeys is the flattened set of permitted wire keys, computed once from Allowlist.
var allowlistKeys = func() map[string]bool {
	m := make(map[string]bool, len(Allowlist))
	for _, f := range Allowlist {
		m[f.Name] = true
	}
	return m
}()

// Permitted reports whether a dotted wire key (e.g. "metrics.cost", "run_metadata.seed") is on the
// allowlist. The egress test walks a transmitted payload and asserts every leaf key is Permitted.
func Permitted(key string) bool { return allowlistKeys[key] }

// AllowlistKeys returns the permitted wire keys, sorted-insertion order preserved via Allowlist. The
// test and the contract-doc generator share this so neither can drift from the payload builder.
func AllowlistKeys() []string {
	out := make([]string, 0, len(Allowlist))
	for _, f := range Allowlist {
		out = append(out, f.Name)
	}
	return out
}

// CategoryOf returns a field's category, or "" if the key is not on the allowlist.
func CategoryOf(key string) string {
	for _, f := range Allowlist {
		if f.Name == key {
			return f.Category
		}
	}
	return ""
}

// IsLinkTarget reports whether rawURL is the one permitted linking origin. A trailing slash or the
// exact ingest path is tolerated; any other scheme, host, or port is refused.
func IsLinkTarget(rawURL string) bool {
	u := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	return u == PlatformBaseURL || u == PlatformBaseURL+LinkPath
}
