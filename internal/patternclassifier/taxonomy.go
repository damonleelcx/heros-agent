package patternclassifier

import "sort"

// TaxonomyVersion pins the vocabulary a label was drawn from. Every emitted label carries it, so a
// label stays interpretable if the taxonomy is later extended (PRD Q5). Extending the taxonomy is a
// MINOR bump; renaming or removing a pattern is a MAJOR bump and invalidates old labels.
const TaxonomyVersion = "1.0.0"

// Pattern is one member of the FIXED 20-pattern taxonomy — a closed vocabulary. No layer, rule or
// LLM, may emit a label outside it (FR: taxonomy closure). The wire value is the snake_case name;
// it is part of the IR contract, so these strings are not renameable without a taxonomy MAJOR bump.
type Pattern string

// The 20 patterns. Grouped as in PRD §8.3; the group is data (Group()), not encoded in the name.
const (
	// Control-flow (7).
	PromptChaining       Pattern = "prompt_chaining"
	Routing              Pattern = "routing"
	Parallelization      Pattern = "parallelization"
	Reflection           Pattern = "reflection"
	Planning             Pattern = "planning"
	Prioritization       Pattern = "prioritization"
	ExplorationDiscovery Pattern = "exploration_discovery"

	// Capability (4).
	ToolUse             Pattern = "tool_use"
	RetrievalRAG        Pattern = "retrieval_rag"
	MemoryManagement    Pattern = "memory_management"
	ReasoningTechniques Pattern = "reasoning_techniques"

	// Coordination (2).
	MultiAgentCollaboration Pattern = "multi_agent_collaboration"
	InterAgentCommunication Pattern = "inter_agent_communication"

	// Governance (7).
	GoalSettingMonitoring     Pattern = "goal_setting_monitoring"
	ExceptionHandlingRecovery Pattern = "exception_handling_recovery"
	HumanInTheLoop            Pattern = "human_in_the_loop"
	EvaluationMonitoring      Pattern = "evaluation_monitoring"
	GuardrailsSafety          Pattern = "guardrails_safety"
	ResourceAwareOptimization Pattern = "resource_aware_optimization"
	LearningAdaptation        Pattern = "learning_adaptation"
)

// Group is one of the four taxonomy groups. The group carries meaning for overlap precedence:
// a control-flow pattern OWNS a subgraph, a capability pattern CO-EXISTS on a node inside it
// (Decision 4 / PRD Q3) — see precedence.go.
type Group string

const (
	GroupControlFlow  Group = "control_flow"
	GroupCapability   Group = "capability"
	GroupCoordination Group = "coordination"
	GroupGovernance   Group = "governance"
)

// Detection says whether a pattern's presence can be established from static structure at all.
//
// Structural: topology + node metadata determine it; a rule detector ships in P3.5.
// Behavioral: its DEFINITION is a runtime fact (it iterates, it votes, it pauses for a human).
// Structure may at most show a candidate, so such a label is capped at BehavioralCandidateCap and
// is never asserted as confirmed. Confirmation is P5 (dynamic tracing).
type Detection string

const (
	DetectionStructural Detection = "structural"
	DetectionBehavioral Detection = "behavioral"
)

// PatternInfo is the taxonomy row for one pattern.
type PatternInfo struct {
	Pattern Pattern `json:"pattern"`
	// Ordinal is the pattern's CANONICAL number, 1–20. It is part of the contract, not a display
	// detail: people refer to these patterns by number ("implement Pattern 13"), and a number that
	// means Retrieval/RAG in one document and Inter-Agent Communication in another is worse than no
	// number at all. Pinning it here — and asserting the whole 1..20 sequence in a test — makes one
	// numbering authoritative and lets every doc and screen agree with it.
	//
	// Note that the ordinal is NOT the group order. The canonical sequence interleaves groups
	// (Tool Use is #5, between Reflection and Planning), so sorting by ordinal and grouping by
	// Group are two different, both-valid views of the same closed set.
	Ordinal   int       `json:"ordinal"`
	Group     Group     `json:"group"`
	Detection Detection `json:"detection"`
	// Title is the human-readable name used by the UI and by diagnostics. Display only.
	Title string `json:"title"`
	// P5Signal names the runtime evidence that would CONFIRM this pattern. Empty when structure
	// already determines it. It is documentation of the honesty boundary, not an executable field.
	P5Signal string `json:"p5_signal,omitempty"`
}

// taxonomy is the single source of truth for the closed vocabulary. Keyed by pattern so that
// "is this pattern in the taxonomy" is a map lookup and cannot drift from the enum above.
//
// Rows are written in CANONICAL ORDINAL order (1–20), which is how the taxonomy is enumerated
// everywhere it is read by a human. That order interleaves the groups on purpose — it is the
// canonical sequence, not a grouping — so the Group column, not the row position, is what carries
// group membership.
var taxonomy = map[Pattern]PatternInfo{
	PromptChaining:            {PromptChaining, 1, GroupControlFlow, DetectionStructural, "Prompt Chaining", ""},
	Routing:                   {Routing, 2, GroupControlFlow, DetectionStructural, "Routing", "branch-hit distribution"},
	Parallelization:           {Parallelization, 3, GroupControlFlow, DetectionStructural, "Parallelization", "real branch independence"},
	Reflection:                {Reflection, 4, GroupControlFlow, DetectionBehavioral, "Reflection", "iteration count > 1"},
	ToolUse:                   {ToolUse, 5, GroupCapability, DetectionStructural, "Tool Use", "tool-call→observe→retry trajectory"},
	Planning:                  {Planning, 6, GroupControlFlow, DetectionBehavioral, "Planning", "planning node emits a consumed task list"},
	MultiAgentCollaboration:   {MultiAgentCollaboration, 7, GroupCoordination, DetectionStructural, "Multi-Agent Collaboration", "inter-agent dispatch at runtime"},
	MemoryManagement:          {MemoryManagement, 8, GroupCapability, DetectionBehavioral, "Memory Management", "memory read/write against a store between turns"},
	LearningAdaptation:        {LearningAdaptation, 9, GroupGovernance, DetectionBehavioral, "Learning & Adaptation", "policy/weights updated from feedback"},
	GoalSettingMonitoring:     {GoalSettingMonitoring, 10, GroupGovernance, DetectionBehavioral, "Goal Setting & Monitoring", "goal-check nodes firing at runtime"},
	ExceptionHandlingRecovery: {ExceptionHandlingRecovery, 11, GroupGovernance, DetectionBehavioral, "Exception Handling & Recovery", "error→recover trajectory"},
	HumanInTheLoop:            {HumanInTheLoop, 12, GroupGovernance, DetectionBehavioral, "Human-in-the-Loop", "human-approval pause in the trace"},
	RetrievalRAG:              {RetrievalRAG, 13, GroupCapability, DetectionStructural, "Retrieval / RAG", "retrieval hit at runtime"},
	InterAgentCommunication:   {InterAgentCommunication, 14, GroupCoordination, DetectionBehavioral, "Inter-Agent Communication", "validated message passing between agents"},
	ResourceAwareOptimization: {ResourceAwareOptimization, 15, GroupGovernance, DetectionStructural, "Resource-Aware Optimization", "tier-selection decisions at runtime"},
	ReasoningTechniques:       {ReasoningTechniques, 16, GroupCapability, DetectionBehavioral, "Reasoning Techniques", "sample-N-then-vote; multi-path reasoning"},
	EvaluationMonitoring:      {EvaluationMonitoring, 17, GroupGovernance, DetectionBehavioral, "Evaluation & Monitoring", "evaluator node scoring at runtime"},
	GuardrailsSafety:          {GuardrailsSafety, 18, GroupGovernance, DetectionBehavioral, "Guardrails & Safety", "guard node blocking/refusing at runtime"},
	Prioritization:            {Prioritization, 19, GroupControlFlow, DetectionBehavioral, "Prioritization", "ordering/scoring of tasks at runtime"},
	ExplorationDiscovery:      {ExplorationDiscovery, 20, GroupControlFlow, DetectionBehavioral, "Exploration & Discovery", "branching search trajectories"},
}

// TaxonomySize is the fixed cardinality of the vocabulary in this taxonomy version. It is asserted
// against len(taxonomy) in a test: a 21st pattern added without a TaxonomyVersion bump fails loudly
// rather than silently changing what a stored label means.
const TaxonomySize = 20

// InTaxonomy reports whether p is a member of the closed vocabulary. This is THE membership check;
// every write path funnels through it (Label.Validate) so an out-of-taxonomy label cannot reach the
// IR from any layer.
func InTaxonomy(p Pattern) bool {
	_, ok := taxonomy[p]
	return ok
}

// Info returns the taxonomy row for p. ok is false when p is not in the taxonomy.
func Info(p Pattern) (PatternInfo, bool) {
	i, ok := taxonomy[p]
	return i, ok
}

// Patterns returns the whole taxonomy in CANONICAL ORDINAL order (1–20).
//
// Stable order matters twice over. It is what the LLM fallback's enumerated prompt is built from, so
// the prompt — and therefore its derived prompt_version — does not change run to run. And it is what
// every human-readable enumeration renders, so the list on a screen matches the list in the PRD
// matches the list in the prompt. Ordinal order rather than lexical because the ordinal is the
// number people use to refer to a pattern.
func Patterns() []PatternInfo {
	out := make([]PatternInfo, 0, len(taxonomy))
	for _, i := range taxonomy {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Ordinal < out[b].Ordinal })
	return out
}

// ByOrdinal resolves the canonical pattern number (1–20) to its taxonomy row, so "Pattern 13" can be
// looked up rather than counted off a document by hand.
func ByOrdinal(n int) (PatternInfo, bool) {
	for _, i := range taxonomy {
		if i.Ordinal == n {
			return i, true
		}
	}
	return PatternInfo{}, false
}

// StructuralPatterns returns the patterns that ship a structural detector in P3.5, in stable order.
func StructuralPatterns() []Pattern {
	var out []Pattern
	for _, i := range Patterns() {
		if i.Detection == DetectionStructural {
			out = append(out, i.Pattern)
		}
	}
	return out
}

// IsBehavioral reports whether confirming p requires runtime evidence (P5). A behavioral pattern's
// confidence is capped at BehavioralCandidateCap by Label.Validate — enforced, not merely intended.
func IsBehavioral(p Pattern) bool {
	i, ok := taxonomy[p]
	return ok && i.Detection == DetectionBehavioral
}
