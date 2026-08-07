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

import (
	"net/url"
	"path"
	"strings"
)

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

	// ── eval summary + per-node attribution ──────────────────────────────────────
	//
	// 🔴 THIS IS A DELIBERATE WIDENING OF THE BOUNDARY. Read this before adding to it.
	//
	// The eval board and the scorecard render evidence that QUALIFIES a claim: how many cases a score is
	// over, whether the gate passed, and which node the cost came from. The platform was sent the claim
	// (a score, a total) and none of the evidence, so those surfaces could only be mounted by GUESSING —
	// a `gate_pass` boolean the platform invented, a scorecard with per-node columns and no per-node
	// data. Refusing to guess is why they were mounted nil. This is the other way to fix it: send the
	// evidence, on purpose, named.
	//
	// Every field below is a COUNT, a VERDICT, or a QUANTITY ALREADY PERMITTED IN AGGREGATE:
	//
	//   - case_count is how many cases ran. Not the cases. The eval set itself never crosses, and there
	//     is no field here it could occupy.
	//   - gate_outcome is the verdict the CLI already printed to the developer's own terminal, and
	//     gate_failures names the METRICS that failed — metric names are already permitted under
	//     `scores.metric`. The thresholds are the customer's policy and do NOT cross.
	//   - single_seed is the provisional flag. It travels WITH the score for the reason ci_low/ci_high
	//     do (design Decision 8): a number whose caveat was left behind reads as a stronger claim than
	//     it is, and the platform would render a one-seed run identically to a fifty-seed one.
	//   - metrics.per_node is cost/latency/tokens attributed to a node id. Both halves are already on
	//     this list — the quantities under `metrics.*`, the ids under `ir_structure.node_ids`. What is
	//     new is the JOIN, which is exactly what "which node is expensive" needs and is the scorecard's
	//     entire purpose. It was computed, carried in RunRecord, and dropped at BuildPayload with the
	//     note "aggregate-derivable"; that was wrong in one direction that matters — an aggregate does
	//     not tell you WHICH node.
	//
	// Still not permitted, and still not expressible: prompt text, case inputs or outputs, expected
	// answers, judge prompts, gate THRESHOLDS, blob contents. A case count says how much evidence there
	// was; it does not carry any of it.
	{"eval.case_count", "eval", "How many eval cases the score is computed over — the board's denominator. A count, never the cases."},
	{"eval.seed_count", "eval", "How many seeds ran. The seed list itself already crosses under run_metadata.seed; this is its length, so the board can say n= without the reader counting."},
	{"eval.gate_outcome", "eval", "The gate verdict the CLI already printed locally: pass | fail | not-configured. A verdict, not the policy behind it."},
	{"eval.gate_failures", "eval", "Which METRICS failed the gate (names only, already permitted under scores.metric). The thresholds are the customer's policy and do not cross."},
	{"eval.single_seed", "eval", "Whether this was a single-seed run — the provisional caveat, travelling with the number it qualifies (design Decision 8)."},
	{"metrics.per_node", "metrics", "Cost/latency/tokens attributed to a node id. Both halves already cross; this is the JOIN, and it is what the scorecard exists to show. No content."},
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

// PlatformPaths are every path this package is allowed to address under PlatformBaseURL. It is a
// declared list, not a pattern, so a reviewer reads the complete set of things the CLI can reach.
//
// 🔴 Each entry must be a path some transport actually uses. The previous version of IsLinkTarget
// compared the WHOLE URL against PlatformBaseURL+LinkPath, which made `push-source`, the workflow-IR
// upload, platform-side discovery and `report-verdict` refuse their own requests — every one of them
// addresses a path that was never in the comparison. The test that was supposed to catch it enumerated
// the paths the PIN allowed rather than the paths the PRODUCT uses, so both agreed with each other and
// neither agreed with the shipped commands. TestEveryPlatformPathIsALinkTarget now drives this list
// from the transports themselves.
var PlatformPaths = []string{
	"/api/v1/whoami",     // login token validation
	LinkPath,             // run linking
	WorkflowIRPath,       // opt-in workflow structure
	WorkflowSourcePath,   // source snapshots (PUT) and their retraction (DELETE)
	SourceDiscoveryPath,  // platform-side discovery over a pushed snapshot
	VerdictPath,          // CI-measured verification verdicts
	TransformReceiptPath, // P29 · what a locally-generated transform did (counts, never a diff)
	// 🔴 P29 · every entry above is now EXACT. Two of them used to be trailing-slash PREFIX entries
	// (`/api/v1/workflows/`, `/api/v1/proposals/`), which meant this list permitted anything below those
	// heads — a much wider pin than the comment above claims, and the same shape as the ingress defect:
	// a prefix is a standing permission for routes that do not exist yet.
	// ⚠️ The three below were MISSING, and the omission was invisible for the same reason the comment
	// above describes: `TestEveryPlatformPathIsALinkTarget` drives this list from a hand-maintained list
	// of transport calls, and P27 added two transports without adding two cases. The pin is checked on
	// the BASE for these calls rather than on the full URL, so nothing refused them — the list simply
	// stopped being "every path this package is allowed to address" and nobody could tell.
	//
	// `internal/api`'s ingress fence now derives the set from the transport SOURCE, so a fourth transport
	// added without a case here fails a test instead of quietly widening this comment's claim.
	"/api/v1/device/authorize",     // P27 · the terminal asks for a code, holding no credential
	"/api/v1/device/token",         // P27 · the terminal polls for the approval
	"/api/v1/auth/password/signin", // P28 · `heros login` with an email and a password
}

// IsLinkTarget reports whether rawURL is on the one permitted linking origin and addresses a declared
// platform path. The ORIGIN is the pin — scheme, host and port must equal PlatformBaseURL's, so a wrong
// scheme, a different host, a suffix attack, a subdomain or a stray port is refused. Credentials in the
// URL, a query string and a fragment are refused too: nothing on this boundary needs them, and each is a
// way to carry bytes into a destination that is supposed to be fully readable from the constant above.
func IsLinkTarget(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	base, err := url.Parse(PlatformBaseURL)
	if err != nil {
		return false
	}
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return false
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	p := path.Clean("/" + strings.TrimPrefix(u.EscapedPath(), "/"))
	if p == "/" {
		// The base URL itself — what Validate re-checks before building the whoami request.
		return true
	}
	for _, allowed := range PlatformPaths {
		if strings.HasSuffix(allowed, "/") {
			// A prefix entry: the id and revision segments below it are caller-supplied.
			if strings.HasPrefix(p+"/", allowed) {
				return true
			}
			continue
		}
		if p == allowed {
			return true
		}
	}
	return false
}
