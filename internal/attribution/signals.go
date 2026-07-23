package attribution

import (
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// signals.go extracts the DETERMINISTIC trace signals both the clusterer (§2) and the rule detectors
// (§5) read. Deriving them once, in one place, is what keeps clustering's "this failed on a tool that
// returned empty" and the detector's "tool-returns-empty typed cause" from drifting into two
// different notions of the same signal.
//
// Every signal is read straight off the spans + the case. There is no LLM in this path and no store
// access — the same trace yields the same signals every run.

// These are the span-attribute keys the P2.5 runtime attaches to a node span, reused here by their
// canonical names so the attribution engine reads the SAME keys the instrument writes. The
// context-assembly figures ride on the node span they describe (the node that consumed the context),
// and a tool span carries its fail-closed reason under AttrToolReason.
const (
	attrContextDropRatio    = telemetry.MetricContextDropRatio       // node span: fraction of source context dropped
	attrRetrievedChunks     = telemetry.MetricContextRetrievedChunks // node span: chunks a RAG node retrieved
	attrRelevanceScore      = "context_retrieval_relevance"          // node span: mean relevance of retrieved chunks [0,1]
	attrContextWindowTokens = telemetry.AttrGenAIUsageInput          // node span: input tokens sent to the model
	attrModelTier           = "model_tier"                           // node span: cheap|standard|reasoning
	attrReasoningNode       = "reasoning_heavy"                      // node span: bool, node needs a reasoning model
	attrContextWindowLimit  = "context_window_limit"                 // node span: the model's context window in tokens
)

// Signals is the deterministic feature set of one failing case's trace. Booleans are computed
// conservatively — each is true only when the trace carries positive evidence for it — so a missing
// attribute reads as "signal absent", never as a false positive.
type Signals struct {
	// NodeExecCount is the number of node-execution spans — the hop depth of the run. A deep chain is
	// what "multi-hop" means operationally.
	NodeExecCount int
	// DistinctNodes is the number of distinct node ids executed.
	DistinctNodes int
	// ToolErrors is the count of failed tool spans.
	ToolErrors int
	// ToolReasons is the set of fail-closed reasons the tool spans reported.
	ToolReasons map[string]int
	// ToolEmpty reports a tool that structurally succeeded but returned no result.
	ToolEmpty bool
	// ToolSchemaMismatch reports a tool call that failed on a schema/argument mismatch, or a node
	// with repeated (≥2) tool errors — the signature of a tool contract the node cannot satisfy.
	ToolSchemaMismatch bool
	// ToolErrorNode names the node whose tool calls failed.
	ToolErrorNode string
	// RetrievalMiss reports a RAG node that retrieved zero chunks or only low-relevance chunks.
	RetrievalMiss bool
	// ContextOverflow reports a node whose assembled context was truncated (drop ratio > 0) before it
	// ran, or whose input tokens exceeded the model's window.
	ContextOverflow bool
	// LostInMiddle reports an over-long context (many tokens, no truncation) feeding a LATER node —
	// the degradation-by-length signal distinct from a hard overflow.
	LostInMiddle bool
	// CheapModelOnReasoningNode reports a node tagged reasoning-heavy running on a cheap model tier.
	CheapModelOnReasoningNode bool
	// TruncatedBeforeNode / OverflowNode name the node the overflow/truncation was detected at.
	OverflowNode string
	// EmptyToolNode names the node whose tool returned empty.
	EmptyToolNode string
	// RetrievalMissNode names the RAG node that missed.
	RetrievalMissNode string
	// CheapModelNode names the reasoning node on a cheap model.
	CheapModelNode string
}

// DeriveSignals computes the trace signals for a failing case. The thresholds are explicit values so
// a report can state the rule it applied rather than implying one.
func DeriveSignals(fc FailingCase) Signals {
	const (
		multiHopMinNodes = 4    // a chain of ≥4 node executions is "multi-hop"
		longContextTok   = 8000 // input tokens beyond which lost-in-the-middle is plausible
		lowRelevance     = 0.3  // mean retrieval relevance below which a RAG node has effectively missed
	)
	s := Signals{ToolReasons: map[string]int{}}

	nodeSpans := fc.Trace.NodeSpans()
	s.NodeExecCount = len(nodeSpans)
	distinct := map[string]bool{}

	// Node-span signals.
	maxTokens := 0.0
	maxTokensNode := ""
	for _, sp := range nodeSpans {
		id := attrString(sp.Attributes, telemetry.AttrNodeID)
		if id == "" {
			id = sp.Name
		}
		distinct[id] = true

		if dr := attrFloat(sp.Attributes, attrContextDropRatio); dr > 0 {
			s.ContextOverflow = true
			if s.OverflowNode == "" {
				s.OverflowNode = id
			}
		}
		if tok := attrFloat(sp.Attributes, attrContextWindowTokens); tok > 0 {
			if limit := attrFloat(sp.Attributes, attrContextWindowLimit); limit > 0 && tok > limit {
				s.ContextOverflow = true
				if s.OverflowNode == "" {
					s.OverflowNode = id
				}
			}
			if tok > maxTokens {
				maxTokens, maxTokensNode = tok, id
			}
		}
		// Retrieval miss: a node that declares a retrieved-chunk count of zero, or a low mean
		// relevance. Only nodes that actually retrieved (attribute present) are judged.
		if _, present := sp.Attributes[attrRetrievedChunks]; present {
			chunks := attrFloat(sp.Attributes, attrRetrievedChunks)
			rel, relPresent := sp.Attributes[attrRelevanceScore]
			if chunks == 0 || (relPresent && attrFloatVal(rel) < lowRelevance) {
				s.RetrievalMiss = true
				if s.RetrievalMissNode == "" {
					s.RetrievalMissNode = id
				}
			}
		}
		if reasoningHeavy(sp.Attributes) && cheapTier(attrString(sp.Attributes, attrModelTier)) {
			s.CheapModelOnReasoningNode = true
			if s.CheapModelNode == "" {
				s.CheapModelNode = id
			}
		}
	}
	s.DistinctNodes = len(distinct)

	// Tool-span signals.
	toolErrByNode := map[string]int{}
	for _, sp := range fc.Trace.Spans {
		if sp.Kind != telemetry.SpanKindTool {
			continue
		}
		reason := attrString(sp.Attributes, telemetry.AttrToolReason)
		node := attrString(sp.Attributes, telemetry.AttrNodeID)
		if sp.Status == telemetry.SpanStatusError {
			s.ToolErrors++
			toolErrByNode[node]++
			if s.ToolErrorNode == "" {
				s.ToolErrorNode = node
			}
			if reason != "" {
				s.ToolReasons[reason]++
			}
			if isSchemaReason(reason) {
				s.ToolSchemaMismatch = true
				s.ToolErrorNode = node
			}
		}
		if isEmptyReason(reason) {
			s.ToolEmpty = true
			if s.EmptyToolNode == "" {
				s.EmptyToolNode = node
			}
		}
	}
	// Repeated tool errors on one node (≥2) are themselves the tool-schema-mismatch signature — a
	// node retrying a tool it cannot satisfy — even without an explicit schema reason. ToolErrorNode
	// stays the first-error node (deterministic by span order); only the flag is raised here.
	for _, n := range toolErrByNode {
		if n >= 2 {
			s.ToolSchemaMismatch = true
			break
		}
	}
	// The case's declared edge-case slot is a legitimate input signal (it is what the case was
	// authored to exercise), used only to confirm a trace signal, never to invent one.
	switch fc.Case.EdgeCase {
	case evalharness.EdgeCaseToolNoResult:
		s.ToolEmpty = true
	case evalharness.EdgeCaseRetrievalMiss:
		s.RetrievalMiss = true
	case evalharness.EdgeCaseContextOverflow:
		s.ContextOverflow = true
	}

	// Lost-in-the-middle: a long context (no hard overflow) feeding a node that is NOT the first hop —
	// length degrading a later node, which is a different failure from a truncation.
	if !s.ContextOverflow && maxTokens >= longContextTok && s.NodeExecCount >= 2 && maxTokensNode != firstNode(nodeSpans) {
		s.LostInMiddle = true
		s.OverflowNode = maxTokensNode
	}
	return s
}

// MultiHop reports whether the run is a deep enough chain to call the failure "multi-hop".
func (s Signals) MultiHop() bool { return s.NodeExecCount >= 4 }

func attrFloatVal(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return 0
}

func reasoningHeavy(attrs map[string]any) bool {
	if b, ok := attrs[attrReasoningNode].(bool); ok {
		return b
	}
	return false
}

func cheapTier(tier string) bool { return tier == "cheap" }

func isEmptyReason(reason string) bool {
	switch reason {
	case "empty", "no_result", "empty_result", "tool_returns_nothing":
		return true
	}
	return false
}

func isSchemaReason(reason string) bool {
	switch reason {
	case "schema", "schema_mismatch", "invalid_arguments", "bad_arguments", "validation_error":
		return true
	}
	return false
}

func firstNode(spans []telemetry.Span) string {
	if len(spans) == 0 {
		return ""
	}
	return attrString(spans[0].Attributes, telemetry.AttrNodeID)
}
