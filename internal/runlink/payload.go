package runlink

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// payload.go builds the transmitted payload by CONSTRUCTION from the allowlist (PRD FR11). It reads
// named fields off a RunRecord and writes them into a fresh Payload whose JSON is the exact bytes on the
// wire. It never serializes RunRecord directly — that distinction is what the FR11 test exercises.

// Payload is the exact wire shape of a linked run. Its JSON keys ARE the allowlist keys; there is no
// key here that is not in Allowlist, and the egress test proves it by walking a marshaled payload.
type Payload struct {
	ContractVersion string          `json:"contract_version"`
	Metrics         WireMetrics     `json:"metrics"`
	IRStructure     WireIRStructure `json:"ir_structure"`
	ConfigHash      string          `json:"config_hash"`
	SourceRevision  string          `json:"source_revision"`
	Scores          []WireScore     `json:"scores"`
	RunMetadata     WireRunMetadata `json:"run_metadata"`
	RunsReported    int             `json:"runs_reported"`
}

type WireMetrics struct {
	Cost    float64    `json:"cost"`
	Latency float64    `json:"latency"`
	Tokens  WireTokens `json:"tokens"`
}

type WireTokens struct {
	In  int64 `json:"in"`
	Out int64 `json:"out"`
}

type WireIRStructure struct {
	NodeIDs       []string          `json:"node_ids"`
	Edges         []Edge            `json:"edges"`
	ModelRefs     map[string]string `json:"model_refs,omitempty"`
	PatternLabels map[string]string `json:"pattern_labels,omitempty"`
}

type WireScore struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
}

type WireRunMetadata struct {
	RunID       string  `json:"run_id"`
	WorkflowID  string  `json:"workflow_id"`
	Timestamp   string  `json:"timestamp"`
	Seed        []int64 `json:"seed"`
	ToolVersion string  `json:"tool_version"`
}

// BuildPayload constructs the wire payload from a RunRecord, field by named field. This is the ONLY
// place a linked payload is created. Adding a field to RunRecord changes nothing here until this
// function is edited to copy it — which is the whole point (FR11): the boundary widens only by a
// deliberate edit in one reviewed place, never by a struct growing a field.
func BuildPayload(r RunRecord) Payload {
	p := Payload{
		ContractVersion: ContractVersion,
		Metrics: WireMetrics{
			Cost:    r.Metrics.CostUSD,
			Latency: r.Metrics.LatencyMS,
			Tokens:  WireTokens{In: r.Metrics.TokensIn, Out: r.Metrics.TokensOut},
		},
		IRStructure: WireIRStructure{
			NodeIDs:       append([]string(nil), r.IR.NodeIDs...),
			Edges:         append([]Edge(nil), r.IR.Edges...),
			ModelRefs:     copyStrMap(r.IR.ModelRefs),
			PatternLabels: copyStrMap(r.IR.PatternLabels),
		},
		ConfigHash:     r.ConfigHash,
		SourceRevision: r.SourceRevision,
		RunMetadata: WireRunMetadata{
			RunID:       r.RunID,
			WorkflowID:  r.WorkflowID,
			Timestamp:   r.Timestamp,
			Seed:        append([]int64(nil), r.Seeds...),
			ToolVersion: r.ToolVersion,
		},
		RunsReported: r.RunsReported,
	}
	for _, s := range r.Scores {
		p.Scores = append(p.Scores, WireScore{Metric: s.Metric, Value: s.Value, CILow: s.CILow, CIHigh: s.CIHigh})
	}
	// Deliberately NOT copied: per-node metrics carry no content, but they are aggregate-derivable and
	// omitting them keeps the wire surface minimal. Everything sent is above; nothing below.
	return p
}

func copyStrMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// PayloadKeys walks a marshaled payload and returns the set of allowlist-granularity keys it contains:
// for a leaf at path a/b/c… it records "a.b" (or "a" for a top-level scalar), dropping array indices
// and deeper map keys (which are data — node ids, metric names — under an allowlisted namespace).
//
// This is the machine that makes FR11 checkable: AssertAllowlisted feeds every key here through
// Permitted, and the egress test adds a field to the source and asserts it never appears.
func PayloadKeys(payloadJSON []byte) ([]string, error) {
	var tree any
	if err := json.Unmarshal(payloadJSON, &tree); err != nil {
		return nil, fmt.Errorf("payload keys: %w", err)
	}
	set := map[string]bool{}
	var walk func(prefix []string, v any)
	walk = func(prefix []string, v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				walk(append(prefix, k), child)
			}
		case []any:
			for _, child := range t {
				walk(prefix, child) // array index dropped — position is not a field name
			}
		default:
			set[allowlistKeyOf(prefix)] = true
		}
	}
	walk(nil, tree)
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// allowlistKeyOf reduces a leaf path to its allowlist-granularity key: the first up-to-two non-numeric
// segments. "metrics/cost" -> "metrics.cost"; "config_hash" -> "config_hash";
// "ir_structure/model_refs/n_abc" -> "ir_structure.model_refs"; "scores/value" (index dropped) -> "scores.value".
func allowlistKeyOf(path []string) string {
	seg := make([]string, 0, 2)
	for _, s := range path {
		if _, err := strconv.Atoi(s); err == nil {
			continue // numeric index — not a field name
		}
		seg = append(seg, s)
		if len(seg) == 2 {
			break
		}
	}
	return strings.Join(seg, ".")
}

// AssertAllowlisted returns the keys in payloadJSON that are NOT on the allowlist. An empty result means
// every transmitted key is permitted. "contract_version" is the envelope version and is always allowed.
func AssertAllowlisted(payloadJSON []byte) ([]string, error) {
	keys, err := PayloadKeys(payloadJSON)
	if err != nil {
		return nil, err
	}
	var offenders []string
	for _, k := range keys {
		if k == "contract_version" {
			continue
		}
		if Permitted(k) {
			continue
		}
		// A two-segment key whose first segment is itself a permitted top-level key (e.g. a scalar) is
		// covered by the single-key check; anything else is an offender.
		if Permitted(firstSegment(k)) {
			continue
		}
		offenders = append(offenders, k)
	}
	return offenders, nil
}

func firstSegment(k string) string {
	if i := strings.IndexByte(k, '.'); i >= 0 {
		return k[:i]
	}
	return k
}
