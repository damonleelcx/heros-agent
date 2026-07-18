package telemetry

import (
	"sort"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// cardinality.go is Decision 4 — the "metric cardinality" deep-dive the ownership matrix names as one
// of the two hardest parts of the project — expressed as code the TSDB write path enforces.
//
// # The rule
//
// Only LOW-to-moderate cardinality tags become TSDB series labels. Every other identifier is stripped
// from the label set and retained as a span attribute, a Postgres column, or a TSDB exemplar — present
// and queryable, just not a label. This is what keeps active series at ~3×10⁴ per optimization run
// instead of ~10⁸, and it is the single reason metrics live in a TSDB at all (P0 §8.2).
//
// # Why config_hash IS a label but case_id/run_id are NOT
//
// config_hash is low-cardinality PER RUN (the metric-event schema says so verbatim: "TAG 7/7 ...
// Low-cardinality per run => usable as a TSDB series label") and trend queries MUST filter by it (task
// 5.4). It correlates with variant_id — a snapshot has ~one config per variant — so including it does
// not multiply series. case_id (~200/run), run_id and invocation_id (~10⁶) are high-cardinality: as
// labels they would multiply series 200× and 10⁶×, which is the exact explosion this rule forbids.

// SeriesLabelTags is the CLOSED set of tags allowed to be TSDB series labels. Anything not in this set
// is not a label — the filter is an allowlist, not a denylist, so a new high-cardinality dimension
// added later is excluded by default rather than silently becoming a label and blowing up the series
// count. The four named in the requirement (variant_id, node_id, seed, metric_name) plus config_hash,
// which the schema itself blesses and trend queries filter on.
var SeriesLabelTags = []string{
	AttrVariantID,
	AttrNodeID,
	AttrSeed,
	"metric_name",
	AttrConfigHash,
}

// HighCardinalityTags are the identifiers explicitly forbidden as labels (task 3.2). Listed so a test
// can assert none of them ever appears in a projected label set, and so the intent is greppable.
var HighCardinalityTags = []string{
	AttrCaseID,
	AttrRunID,
	AttrInvocationID,
}

// IsSeriesLabel reports whether a tag/dimension may be a TSDB series label.
func IsSeriesLabel(tag string) bool {
	for _, t := range SeriesLabelTags {
		if t == tag {
			return true
		}
	}
	return false
}

// SeriesLabels projects a metric event to exactly the labels the TSDB is allowed to index it under.
// High-cardinality tags (case_id, run_id, invocation_id, blob refs) are DROPPED here — they do not
// appear in the returned map at all, so a TSDB store built on this projection cannot make them labels
// even by accident.
func SeriesLabels(ev metricevent.Event) map[string]string {
	return map[string]string{
		AttrVariantID:  ev.VariantID,
		AttrNodeID:     ev.NodeID,
		AttrSeed:       strconv.FormatInt(seedVal(ev), 10),
		"metric_name":  ev.MetricName,
		AttrConfigHash: ev.ConfigHash,
	}
}

// SeriesKey is the canonical series identity — the label set flattened to one deterministic string, so
// two events with identical labels map to ONE series regardless of case_id/run_id. This is the value a
// budget test counts distinct of.
func SeriesKey(ev metricevent.Event) string {
	labels := SeriesLabels(ev)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

// Exemplars are the high-cardinality identifiers a metric point CARRIES without indexing on: they link
// a series bucket back to a representative run/case/invocation for drill-down, exactly as OTel
// exemplars do. Retained (task 3.2) — the data is not lost, only kept off the label axis.
func Exemplars(ev metricevent.Event) map[string]string {
	ex := map[string]string{
		AttrRunID:  ev.RunID,
		AttrCaseID: ev.CaseID,
	}
	if inv, ok := invocationID(ev); ok {
		ex[AttrInvocationID] = inv
	}
	return ex
}

func seedVal(ev metricevent.Event) int64 {
	if ev.Seed == nil {
		return -1
	}
	return *ev.Seed
}
