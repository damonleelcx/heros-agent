// Package metricevent is the emission-boundary guard for the metric-event contract (P0 task 2.2).
//
// It is the FIRST of two defense-in-depth layers that enforce the seven-tag contract (the second is
// the Postgres NOT NULL columns + FKs in db/migrations/postgres/0001_p0_lineage.up.sql). Every event
// MUST carry {variant_id, run_id, node_id, case_id, seed, timestamp, config_hash} non-null plus a
// typed payload {metric_name, value, unit}. An event missing any of these is REJECTED here, before it
// is persisted or exported to any store — it is never written.
//
// Backend rule this package embodies: FAIL LOUD, no silent fallback. A missing tag is returned as an
// error listing every problem; it is never defaulted to "" or 0 to make the write "succeed".
//
// Contract source of truth: schemas/metric-event.schema.json. This code intentionally re-encodes the
// required-and-non-null rule so the boundary does not depend on a JSON-Schema validator being wired in
// on every hot-path emit; the CI schema-validation job (task 4.2) checks the two stay in agreement.
package metricevent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the metric-event schema version this guard enforces.
const SchemaVersion = "1.0.0"

// Tags is the seven-tag set, in schema order. Exported so callers/tests share one source of truth.
var Tags = []string{
	"variant_id",
	"run_id",
	"node_id",
	"case_id",
	"seed",
	"timestamp",
	"config_hash",
}

// payloadFields are the typed-payload requirements beyond the seven tags.
var payloadFields = []string{"metric_name", "value", "unit"}

// RejectionError is returned when an event fails the boundary. It lists every problem at once
// (not just the first) so a producer sees the full gap, and it is explicit that the event was dropped.
type RejectionError struct {
	Problems []string
}

func (e *RejectionError) Error() string {
	return fmt.Sprintf("metric event REJECTED at emission boundary (not persisted): %s",
		strings.Join(e.Problems, "; "))
}

// Event is a typed metric event. Seed and Value are pointers so that a legitimate 0 is distinguishable
// from an absent field — the exact trap that makes "0 tokens" and "no tag" look identical otherwise.
type Event struct {
	SchemaVersion string   `json:"schema_version"`
	VariantID     string   `json:"variant_id"`
	RunID         string   `json:"run_id"`
	NodeID        string   `json:"node_id"`
	CaseID        string   `json:"case_id"`
	Seed          *int64   `json:"seed"`
	Timestamp     string   `json:"timestamp"`
	ConfigHash    string   `json:"config_hash"`
	MetricName    string   `json:"metric_name"`
	Value         *float64 `json:"value"`
	Unit          string   `json:"unit"`
	// Dimensions holds optional extensible dimensions (e.g. node_kind, invocation_id). Never required.
	Dimensions map[string]any `json:"-"`
}

// Validate applies the emission-boundary rule to a typed Event. Returns a *RejectionError listing
// every problem, or nil if the event may be persisted.
func (e Event) Validate() error {
	var p []string

	req := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			p = append(p, "missing tag "+name)
		}
	}
	req("variant_id", e.VariantID)
	req("run_id", e.RunID)
	req("node_id", e.NodeID)
	req("case_id", e.CaseID)
	req("timestamp", e.Timestamp)
	req("config_hash", e.ConfigHash)

	if e.Seed == nil {
		p = append(p, "missing tag seed")
	} else if *e.Seed < 0 {
		p = append(p, "invalid tag seed: must be >= 0")
	}

	// Payload.
	if strings.TrimSpace(e.MetricName) == "" {
		p = append(p, "missing payload metric_name")
	}
	if e.Value == nil {
		p = append(p, "missing payload value")
	}
	if strings.TrimSpace(e.Unit) == "" {
		// A value without a unit is an incomplete payload (schema FR10).
		p = append(p, "missing payload unit")
	}

	// Format checks that would otherwise pass NOT NULL but still be un-sliceable / un-reproducible.
	if e.ConfigHash != "" && !isSHA256Hex(e.ConfigHash) {
		p = append(p, "invalid tag config_hash: must be 64 lowercase hex chars")
	}
	if e.Timestamp != "" && !isRFC3339(e.Timestamp) {
		p = append(p, "invalid tag timestamp: must be RFC 3339")
	}

	if len(p) > 0 {
		sort.Strings(p)
		return &RejectionError{Problems: p}
	}
	return nil
}

// ValidateMap applies the same rule to an already-decoded event object (the form a generic exporter
// or a cross-language producer hands in). Any of the seven tags absent, null, or empty is rejected.
func ValidateMap(m map[string]any) error {
	if m == nil {
		return &RejectionError{Problems: []string{"event is nil"}}
	}
	var p []string

	for _, tag := range Tags {
		v, ok := m[tag]
		if !ok || v == nil {
			p = append(p, "missing tag "+tag)
			continue
		}
		if tag == "seed" {
			if !isNumber(v) {
				p = append(p, "invalid tag seed: must be a number")
			}
			continue
		}
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
			p = append(p, "missing tag "+tag)
		}
	}

	for _, f := range payloadFields {
		v, ok := m[f]
		if !ok || v == nil {
			p = append(p, "missing payload "+f)
			continue
		}
		if f != "value" {
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
				p = append(p, "missing payload "+f)
			}
		}
	}

	if len(p) > 0 {
		sort.Strings(p)
		return &RejectionError{Problems: p}
	}
	return nil
}

// AsRejection extracts a *RejectionError from err, if that is what it is.
func AsRejection(err error) (*RejectionError, bool) {
	var re *RejectionError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !isLowerHexDigit(c) {
			return false
		}
	}
	return true
}

func isLowerHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func isRFC3339(s string) bool {
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}
