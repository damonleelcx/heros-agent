package telemetry

import (
	"context"
	"fmt"
	"strings"
)

// RunContext carries the seven tags through a run so the emitter populates every event from context
// instead of trusting each call site to remember them (Decision 3). This is the mechanism behind
// "tags complete by construction": an evaluator or an instrument that is handed a RunContext CANNOT
// emit an under-tagged event, because the tags are not its to supply — they are read from here.
//
// timestamp is deliberately NOT a field: it is stamped at emission (each event has its own), so a
// RunContext is reusable across a whole run and every node in it.
//
// The split between run-scoped and node-scoped tags is real: VariantID/RunID/ConfigHash/Seed/CaseID
// identify the run (and the case it is processing), while NodeID/AttemptGroup identify one node
// invocation within it. WithNode derives the node-scoped context the gateway observer reads.
type RunContext struct {
	// Run-scoped tags.
	VariantID  string
	RunID      string
	ConfigHash string
	Seed       int64
	CaseID     string

	// Node-scoped tags. Empty on a run-scoped context; set by WithNode.
	NodeID       string
	AttemptGroup int
}

// WithNode returns a copy scoped to one node invocation — the form the gateway observer and the node
// span read. AttemptGroup is the P2 retry unit; two retries of one logical call share it (idempotency,
// Decision 8), a loop's next iteration gets a new one.
func (rc RunContext) WithNode(nodeID string, attemptGroup int) RunContext {
	rc.NodeID = nodeID
	rc.AttemptGroup = attemptGroup
	return rc
}

// Complete reports whether every tag needed to emit a fully-tagged event is present. It is the
// same rule the emission boundary enforces, checked early so the instrument can refuse to attach an
// under-tagged context rather than emit events the collector will only reject later.
func (rc RunContext) Complete() error {
	var missing []string
	req := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	req(AttrVariantID, rc.VariantID)
	req(AttrRunID, rc.RunID)
	req(AttrConfigHash, rc.ConfigHash)
	req(AttrCaseID, rc.CaseID)
	req(AttrNodeID, rc.NodeID)
	if rc.Seed < 0 {
		missing = append(missing, AttrSeed+" (must be >= 0)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("telemetry: RunContext is missing tags: %s", strings.Join(missing, ", "))
	}
	return nil
}

// IdempotencyKey is the {run_id, node_id, attempt_group} identity a retried invocation is measured
// once against (Decision 8). It matches executor.IdempotencyKey byte-for-byte on purpose — the
// telemetry that de-duplicates a retry keys on the SAME string the gateway de-dupes the charge on, so
// "measured once" and "charged once" cannot drift apart.
func (rc RunContext) IdempotencyKey() string {
	return fmt.Sprintf("%s:%s:%d", rc.RunID, rc.NodeID, rc.AttemptGroup)
}

// runContextKey is unexported so the only way to put a RunContext in a context is NewContext — no
// other package can plant a different-typed value under a colliding key.
type runContextKey struct{}

// NewContext attaches a RunContext to ctx. The run driver / executor calls this before a provider
// call; the workflow author never does (Decision 1: zero user code).
func NewContext(ctx context.Context, rc RunContext) context.Context {
	return context.WithValue(ctx, runContextKey{}, rc)
}

// FromContext extracts the RunContext, reporting whether one was attached. The instrument uses ok to
// decide whether it can emit a fully-tagged event or must record a tagging gap rather than a silent
// under-tagged one.
func FromContext(ctx context.Context) (RunContext, bool) {
	rc, ok := ctx.Value(runContextKey{}).(RunContext)
	return rc, ok
}
