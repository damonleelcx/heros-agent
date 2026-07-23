package attribution

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// clustering.go is §2: embed failing inputs + traces and cluster them into NAMED categories with a
// size, a representative case, and their member case ids — so a fix targets a category ("fails when a
// tool returns empty") rather than forty one-off failures.
//
// Design Q2 offers two naming methods: an LLM-labeled cluster (itself calibrated) or a name derived
// from the dominant trace signature. This implementation takes the rule-derived route: the "embedding"
// is the deterministic failure signature computed from the trace + case (see DeriveSignals), and the
// cluster name is that signature rendered in words. That choice is deliberate — it makes clustering
// reproducible (the same failing set yields the same clusters every run) and free (no LLM in the
// path), and it keeps the cluster name faithful to the evidence the cluster was formed from. An
// LLM labeler can be layered on later behind the same []FailureCluster contract; the calibration
// discipline that would govern it lives in the diagnosis engine (§6).

// FailureSignature is the categorical key a failing case is clustered by. It is a closed vocabulary
// (not free text) so two clusters of the same kind always collapse into one, the way the closed
// edge-case taxonomy makes coverage aggregatable.
type FailureSignature string

const (
	SigToolReturnsEmpty  FailureSignature = "tool_returns_empty"
	SigRetrievalMiss     FailureSignature = "retrieval_miss"
	SigContextOverflow   FailureSignature = "context_overflow"
	SigLostInMiddle      FailureSignature = "lost_in_middle"
	SigModelMismatch     FailureSignature = "model_capability_mismatch"
	SigMultiHop          FailureSignature = "multi_hop"
	SigContractViolation FailureSignature = "contract_violation"
	SigUnclustered       FailureSignature = "unclustered"
)

// signatureLabels renders each signature as the human-readable category name the scorecard shows.
var signatureLabels = map[FailureSignature]string{
	SigToolReturnsEmpty:  "fails when a tool returns empty",
	SigRetrievalMiss:     "fails on a retrieval miss",
	SigContextOverflow:   "fails on context overflow / truncation",
	SigLostInMiddle:      "fails on over-long context (lost in the middle)",
	SigModelMismatch:     "fails on a cheap model at a reasoning-heavy node",
	SigMultiHop:          "fails on multi-hop reasoning",
	SigContractViolation: "fails on an output-contract violation",
	SigUnclustered:       "unclustered failures",
}

// FailureCluster is one named category of failures with its size, representative case, and members.
type FailureCluster struct {
	ClusterID   string `json:"cluster_id"`
	VariantID   string `json:"variant_id"`
	EvalSetHash string `json:"eval_set_hash"`

	Signature FailureSignature `json:"signature"`
	Label     string           `json:"label"`
	Size      int              `json:"size"`
	// RepresentativeCaseID is one member a human can open to understand the whole cluster — chosen
	// deterministically (the lexicographically smallest member) so it never moves between runs.
	RepresentativeCaseID string   `json:"representative_case_id"`
	MemberCaseIDs        []string `json:"member_case_ids"`
}

// Cluster groups the failing cases into named categories. It is deterministic and append-only-safe:
// the ClusterID is a content hash of {variant, eval set, signature}, so re-running over the same
// failing set produces the same cluster ids and a store can upsert-by-id without duplicating.
func Cluster(ir *discovery.IR, v Variant, cases []FailingCase) []FailureCluster {
	members := map[FailureSignature][]string{}
	for _, fc := range cases {
		sig := signatureOf(ir, fc)
		members[sig] = append(members[sig], fc.Case.CaseID)
	}

	sigs := make([]FailureSignature, 0, len(members))
	for sig := range members {
		sigs = append(sigs, sig)
	}
	// Order clusters by size (desc) then signature (asc) so "top failure clusters" is a stable slice.
	sort.Slice(sigs, func(i, j int) bool {
		if len(members[sigs[i]]) != len(members[sigs[j]]) {
			return len(members[sigs[i]]) > len(members[sigs[j]])
		}
		return sigs[i] < sigs[j]
	})

	out := make([]FailureCluster, 0, len(sigs))
	for _, sig := range sigs {
		ids := append([]string(nil), members[sig]...)
		sort.Strings(ids)
		out = append(out, FailureCluster{
			ClusterID:            clusterID(v, sig),
			VariantID:            v.VariantID,
			EvalSetHash:          v.EvalSetHash,
			Signature:            sig,
			Label:                signatureLabels[sig],
			Size:                 len(ids),
			RepresentativeCaseID: ids[0],
			MemberCaseIDs:        ids,
		})
	}
	return out
}

// signatureOf assigns a failing case to exactly one category, by a fixed precedence. The precedence
// is deliberate: a tool-returns-empty case that also happens to be a deep chain is a tool-empty
// failure, not a multi-hop one — the more specific, more actionable signal wins.
func signatureOf(ir *discovery.IR, fc FailingCase) FailureSignature {
	s := DeriveSignals(fc)
	switch {
	case s.ToolEmpty:
		return SigToolReturnsEmpty
	case s.RetrievalMiss:
		return SigRetrievalMiss
	case s.ContextOverflow:
		return SigContextOverflow
	case s.LostInMiddle:
		return SigLostInMiddle
	case s.CheapModelOnReasoningNode:
		return SigModelMismatch
	case s.MultiHop():
		return SigMultiHop
	}
	// No structural signal fired: fall back to whether a node's output violated its contract.
	if node, kind := FirstDivergence(ir, fc); node != "" && kind == DivergenceContract {
		return SigContractViolation
	}
	return SigUnclustered
}

func clusterID(v Variant, sig FailureSignature) string {
	h := sha256.New()
	h.Write([]byte("heros.p45.cluster.v1"))
	for _, p := range []string{v.VariantID, v.EvalSetHash, string(sig)} {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
