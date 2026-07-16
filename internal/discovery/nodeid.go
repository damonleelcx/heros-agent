package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// NodeIdentity is the content-addressed tuple a node_id is derived from (design doc 06, PRD Q4).
// It deliberately EXCLUDES absolute line numbers and formatting so a benign line shift (a comment
// added above the call, gofmt) does not churn the id, while a per-selector occurrence index keeps
// two calls in one function distinct.
type NodeIdentity struct {
	ModulePkgPath      string // module path + package dir, e.g. "github.com/acme/app/internal/llm"
	EnclosingSymbolFQN string // "(*Service).Summarize" | "RunAgent.func0" for a closure
	Selector           string // the written call selector, e.g. "client.Messages.New" | "llm.Complete"
	OccurrenceIndex    int    // 0-based, among same-Selector calls in the same symbol, source order
}

// nodeIDDelim (U+22EE VERTICAL ELLIPSIS) cannot appear in a Go import path, symbol, or selector, so it
// is a safe field delimiter for the canonical string.
const nodeIDDelim = "⋮"

// NodeID returns the stable, deterministic node_id (design doc 06 / invariant I3).
func (id NodeIdentity) NodeID() string {
	canon := strings.Join([]string{
		id.ModulePkgPath,
		id.EnclosingSymbolFQN,
		id.Selector,
		strconv.Itoa(id.OccurrenceIndex),
	}, nodeIDDelim)
	sum := sha256.Sum256([]byte(canon))
	return "n_" + hex.EncodeToString(sum[:8]) // 16 hex chars; collision-safe at repo node counts (tens–hundreds)
}
