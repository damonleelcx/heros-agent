package cli

import (
	"hash/fnv"
)

// reference_runtime.go is the offline, deterministic node runtime `eval` uses when no provider is
// configured. It is NOT a provider and does not pretend to be — Name() returns "reference", and `eval`
// labels the run accordingly so a reference run is never mistaken for a real provider evaluation. It
// exists for three reasons the product actually needs: the FREE/air-gapped tier must run `eval` with no
// keys and no network, the offline guarantee (NFR2) must be testable, and determinism (NFR5) must be
// provable — the same repo + revision + config yields the same measurements, so the same IR and
// config_hash. It touches no network and no clock; every number is a pure function of its inputs.
type ReferenceRuntime struct{}

func (ReferenceRuntime) Name() string { return "reference" }

// RunNode derives stable measurements from a hash of {node, model, case, seed}. The numbers are
// plausible (cents of cost, hundreds of ms) but their only real property is determinism and per-seed
// variation, which is what the multi-seed statistics need to have something to measure.
func (ReferenceRuntime) RunNode(nodeID, model, caseID string, seed int64) NodeMeasurement {
	h := fnv.New64a()
	h.Write([]byte(nodeID))
	h.Write([]byte{0})
	h.Write([]byte(model))
	h.Write([]byte{0})
	h.Write([]byte(caseID))
	var sb [8]byte
	for i := 0; i < 8; i++ {
		sb[i] = byte(seed >> (8 * i))
	}
	h.Write(sb[:])
	x := h.Sum64()

	// Decompose the hash into independent, bounded quantities.
	cost := 0.001 + float64(x&0xff)/255.0*0.02        // ~[0.001, 0.021] USD
	latency := 120 + float64((x>>8)&0x3ff)/1023.0*900 // ~[120, 1020] ms
	tokensIn := int64(180 + (x>>18)&0x7f)             // ~[180, 307]
	tokensOut := int64(60 + (x>>25)&0x3f)             // ~[60, 123]
	// Correct ~80% of the time, deterministically — so quality is neither a perfect 1.0 (no signal) nor
	// pinned, and a gate can plausibly pass or fail depending on its configured floor.
	correct := (x>>31)%100 < 80

	return NodeMeasurement{CostUSD: cost, LatencyMS: latency, TokensIn: tokensIn, TokensOut: tokensOut, Correct: correct}
}
