package discovery

import (
	"fmt"
	"regexp"
	"strings"
)

// Span is a run of the customer's own code, with where it came from.
//
// 🔴 Every span carries file and line, and nothing constructs one without them. A finding a reader
// cannot navigate to is a finding they cannot check, and a claim about somebody's code that they cannot
// check is a claim they have to take on faith.
type Span struct {
	Path string
	Line int // 1-indexed, the first line of Text
	Text string
	// Why names the pattern that matched, so a reader can tell "this was found because it assigns
	// temperature" from "this was found because the word memory appears in it".
	Why string
}

// Ref renders a span the way an editor and a code host both accept.
func (s Span) Ref() string { return fmt.Sprintf("%s:%d", s.Path, s.Line) }

// Evidence is what discovery found for one axis.
type Evidence struct {
	Axis  string
	Spans []Span
	// Found is false when nothing matched. 🔴 Distinct from an empty Spans slice by intent rather than by
	// accident: absence is a FINDING here, and the whole difference between "your agent has no memory
	// strategy" and "I could not find the file" lives in whether a caller can tell them apart.
	Found bool
	// Note explains an absence, and is what a report renders when there is nothing to show.
	Note string
}

// pattern is one axis signal.
type pattern struct {
	// re matches a line worth capturing.
	re *regexp.Regexp
	// why is the human name of the signal.
	why string
	// before and after are lines of surrounding context to include, because a matched line is rarely
	// self-explanatory: `temperature=0.7` means nothing without the call it sits in.
	before, after int
	// hints are lowercase literals, at least one of which MUST appear in a line for `re` to match.
	//
	// 🔴 They are a performance gate, and a wrong hint SILENTLY DROPS FINDINGS — the worst failure this
	// package has, because the axis then reports absence and absence is a finding here. So they are not
	// trusted: `TestTheLiteralGateChangesNothing` runs every pattern with and without the gate over a
	// real corpus and asserts the two produce identical evidence. A hint that is too narrow turns that
	// test red rather than quietly shrinking a report.
	hints []string
}

// axisPatterns is the extraction table.
//
// # 🔴 Why this is a TABLE rather than a function per axis
//
// So a reviewer can read the whole of what the system looks for in one place, and so adding an axis
// signal is a row rather than a code path. The alternative — nine functions, each grown by whoever last
// touched that axis — is how the `tools` axis ends up looking for three things and `memory` for eleven,
// with nobody able to say why.
//
// # 🚫 What these patterns are NOT
//
// They are not a parser. They find CANDIDATE evidence for a model to read, which is why a false positive
// is cheap (the model discards it) and a false negative is expensive (the axis reports absence). The
// patterns are therefore deliberately generous, and the honest consequence is stated in every report:
// this is what was found, not everything that exists.
var axisPatterns = map[string][]pattern{
	"model": {
		{regexp.MustCompile(`(?i)\bmodel\s*[=:]\s*["'\x60][\w.:/-]+`), "a model is named", 1, 1, []string{"model"}},
		{regexp.MustCompile(`(?i)\b(temperature|top_p|top_k|max_tokens|max_output_tokens)\s*[=:]`), "a sampling parameter is set", 2, 2, []string{"temperature", "top_p", "top_k", "max_tokens", "max_output_tokens"}},
		{regexp.MustCompile(`(?i)(gpt-[\w.]+|claude-[\w.-]+|deepseek-[\w.-]+|gemini-[\w.-]+|llama-?[\w.]*|mistral-[\w.]+|o[1-4]-\w+)`), "a model name appears", 1, 1, []string{"gpt-", "claude-", "deepseek-", "gemini-", "llama", "mistral-", "o1-", "o2-", "o3-", "o4-"}},
	},
	"prompt": {
		{regexp.MustCompile(`(?i)\b(system_prompt|SYSTEM_PROMPT|system_message|instructions)\s*[=:]`), "a system prompt is defined", 1, 4, []string{"system_prompt", "system_message", "instructions"}},
		{regexp.MustCompile(`(?i)["']role["']\s*:\s*["']system["']`), "a system role message", 1, 3, []string{"system"}},
		{regexp.MustCompile(`(?i)\.(prompt|from_template|ChatPromptTemplate|PromptTemplate)\b`), "a prompt template", 1, 2, []string{"prompt", "from_template", "chatprompttemplate", "prompttemplate"}},
	},
	"skills": {
		{regexp.MustCompile(`(?i)\b(skill|Skill)s?\s*[=:\[]`), "a skill binding", 1, 2, []string{"skill"}},
		{regexp.MustCompile(`(?i)\b(load_skills|register_skill|SkillSet|capabilities)\b`), "a skill registry", 1, 2, []string{"load_skills", "register_skill", "skillset", "capabilities"}},
	},
	"tools": {
		{regexp.MustCompile(`(?i)\btools\s*[=:]\s*[\[\{]`), "a tool list is passed", 1, 4, []string{"tools"}},
		{regexp.MustCompile(`(?i)@(tool|function_tool|openai_function)\b`), "a tool is declared", 0, 3, []string{"@tool", "@function_tool", "@openai_function"}},
		{regexp.MustCompile(`(?i)\b(tool_choice|function_call|tool_calls|bind_tools)\b`), "tool calling is configured", 1, 2, []string{"tool_choice", "function_call", "tool_calls", "bind_tools"}},
	},
	"context": {
		{regexp.MustCompile(`(?i)\bmessages\s*[=+]|\bmessages\.append|\bmessages\s*\+=`), "the message list is built", 2, 3, []string{"messages"}},
		{regexp.MustCompile(`(?i)\b(history|conversation|transcript)\b.*\[|\[.*\b(history|conversation)\b`), "history is spliced into a call", 1, 2, []string{"history", "conversation", "transcript"}},
		{regexp.MustCompile(`(?i)\b(truncat|summari[sz]|window|trim_messages|token_limit)\w*`), "history is trimmed or summarised", 1, 2, []string{"truncat", "summari", "window", "trim_messages", "token_limit"}},
	},
	"memory": {
		{regexp.MustCompile(`(?i)\b(ConversationBufferMemory|ConversationSummaryMemory|\bMemory\b|memory\s*[=:])`), "a memory object", 1, 3, []string{"memory"}},
		{regexp.MustCompile(`(?i)\b(save_context|load_memory|persist|checkpointer|thread_id|session_id)\b`), "state is persisted between calls", 1, 2, []string{"save_context", "load_memory", "persist", "checkpointer", "thread_id", "session_id"}},
		{regexp.MustCompile(`(?i)\b(vectorstore|embedding|faiss|chroma|pinecone|weaviate|qdrant)\b`), "a retrieval store", 1, 2, []string{"vectorstore", "embedding", "faiss", "chroma", "pinecone", "weaviate", "qdrant"}},
	},
	"harness": {
		{regexp.MustCompile(`(?i)\b(timeout|max_retries|retry|backoff|rate_limit|semaphore)\s*[=:(]`), "a limit or retry policy", 1, 2, []string{"timeout", "max_retries", "retry", "backoff", "rate_limit", "semaphore"}},
		{regexp.MustCompile(`(?i)\b(try:|except |catch\s*\(|recover\(\))`), "error handling around a call", 1, 3, []string{"try:", "except ", "catch", "recover()"}},
		{regexp.MustCompile(`(?i)\b(budget|max_cost|spend|token_budget|max_tokens_total)\b`), "a spend ceiling", 1, 2, []string{"budget", "max_cost", "spend", "token_budget", "max_tokens_total"}},
	},
	"loop": {
		{regexp.MustCompile(`(?i)\b(max_turns|max_iterations|max_steps|max_loops)\b`), "an iteration ceiling", 1, 2, []string{"max_turns", "max_iterations", "max_steps", "max_loops"}},
		{regexp.MustCompile(`^\s*(while|for)\s+.*(True|true|;;)\s*:?\s*\{?\s*$`), "an unbounded loop", 0, 5, []string{"while", "for"}},
		{regexp.MustCompile(`(?i)\b(AgentExecutor|ReAct|reflect|critique|self_critique)\b`), "an agent control loop", 1, 3, []string{"agentexecutor", "react", "reflect", "critique", "self_critique"}},
	},
	"graph": {
		{regexp.MustCompile(`(?i)\b(add_edge|add_node|add_conditional_edges|StateGraph|Workflow|DAG)\b`), "a graph is constructed", 1, 3, []string{"add_edge", "add_node", "add_conditional_edges", "stategraph", "workflow", "dag"}},
		{regexp.MustCompile(`(?i)\b(handoff|delegate|route_to|next_agent|supervisor)\b`), "control passes between agents", 1, 2, []string{"handoff", "delegate", "route_to", "next_agent", "supervisor"}},
	},
}

// bits is a fixed bitset over the hint vocabulary.
//
// 🔴 Two words rather than one, because a 64-hint guard fired on its first run: the axis table declares
// more than 64 distinct hints. That panic was the design working — a silent overflow would give two
// hints the same bit and quietly run one pattern on another's lines. Widening is the right answer;
// trimming the signal vocabulary to fit a word size would be tuning the product to the implementation.
type bits [2]uint64

func (b *bits) set(i int)           { b[i/64] |= 1 << (i % 64) }
func (b bits) overlaps(o bits) bool { return b[0]&o[0] != 0 || b[1]&o[1] != 0 }

// maxHints is the ceiling `bits` can address. Exceeding it must force a decision, not degrade.
const maxHints = 128

// hintIDs assigns every distinct hint an id, so "which patterns could match this line" is a bitmask
// comparison rather than thirty substring scans.
var (
	hintIDs  = map[string]int{}
	hintList []string
	// axisMasks holds each pattern's hint bitmask, indexed exactly like axisPatterns.
	//
	// 🔴 Beside the table rather than a field inside `pattern`, because the table is written with
	// POSITIONAL literals — a derived field added mid-struct silently shifts every entry, and one added
	// at the end forces all thirty to spell out a value they do not set. Keeping computed data out of a
	// hand-written table is the cheaper half of that trade.
	axisMasks = map[string][]bits{}
	// hintScanner finds which hints a line contains, built once from the table.
	hintScanner *literalIndex
)

func init() {
	for _, pats := range axisPatterns {
		for _, p := range pats {
			if len(p.hints) == 0 {
				panic("discovery: pattern " + p.why + " has no hints, so the gate would drop it entirely")
			}
			for _, h := range p.hints {
				if _, seen := hintIDs[h]; !seen {
					if len(hintList) >= maxHints {
						panic("discovery: more than 128 distinct hints; widen `bits`")
					}
					hintIDs[h] = len(hintList)
					hintList = append(hintList, h)
				}
			}
		}
	}
	for axis, pats := range axisPatterns {
		masks := make([]bits, len(pats))
		for i, p := range pats {
			for _, h := range p.hints {
				masks[i].set(hintIDs[h])
			}
		}
		axisMasks[axis] = masks
	}
	hintScanner = newLiteralIndex(hintList)
}

// lineMask returns the set of hints present in an already-lowercased line.
func lineMask(lower string) bits {
	if !gateEnabled {
		return bits{}
	}
	return hintScanner.scan(lower)
}

// gateEnabled lets the fence run the same corpus with the gate off and compare. 🚫 Never changed
// outside a test: turning it off in production restores the 16-second scan.
var gateEnabled = true

// mayMatch reports whether any pattern could match, given the hints found in the line.
func mayMatch(m bits) bool { return !gateEnabled || m != bits{} }

// hintedBy reports whether a line's hint set overlaps the pattern at (axis, i).
func hintedBy(lineBits bits, axis string, i int) bool {
	if !gateEnabled {
		return true
	}
	return lineBits.overlaps(axisMasks[axis][i])
}

// toLower lowercases a line, returning the original when it is already lowercase.
//
// 🔴 Most source lines contain no uppercase letter, and `strings.ToLower` allocates a new string
// unconditionally — 1.1M allocations on a large repository, for a copy identical to the input. The scan
// that avoids it is cheaper than the allocation it avoids.
func toLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			return strings.ToLower(s)
		}
	}
	return s
}

// callSite matches a line that calls a model. Used to locate NODES, which is a different question from
// axis evidence: a node is a place the agent thinks, and the axes describe how it thinks there.
var callSite = regexp.MustCompile(`(?i)\b(` +
	`chat\.completions\.create|messages\.create|generate_content|` +
	`ChatOpenAI|ChatAnthropic|ChatDeepSeek|ChatGoogleGenerativeAI|` +
	`\.invoke\(|\.ainvoke\(|\.run\(|\.complete\(|\.chat\(|` +
	`openai\.|anthropic\.|deepseek\.|litellm\.` +
	`)`)

// callSiteHints are the literals a call-site line must contain. Same reasoning as the axis hints, and
// the same fence: `TestTheCallSiteGateChangesNothing` compares gated and ungated over a real corpus,
// because a hint that is too narrow would silently shrink the call-site count — which feeds
// LooksLikeAnAgent, and would turn "987 call sites" into "this is not an agent".
var callSiteHints = []string{
	"chat.completions.create", "messages.create", "generate_content",
	"chatopenai", "chatanthropic", "chatdeepseek", "chatgooglegenerativeai",
	".invoke(", ".ainvoke(", ".run(", ".complete(", ".chat(",
	"openai.", "anthropic.", "deepseek.", "litellm.",
}

var callSiteScanner = newLiteralIndex(callSiteHints)

// mayBeCallSite reports whether a lowercased line could possibly be a call site.
func mayBeCallSite(lower string) bool {
	if !gateEnabled {
		return true
	}
	return callSiteScanner.scan(lower) != bits{}
}

// Node is one place the repository calls a model.
type Node struct {
	ID   string
	Span Span
	// Enclosing is the function or class the call sits in, empty when it could not be determined.
	Enclosing string
}

// enclosingDef finds the nearest preceding function or class definition.
var enclosingDef = regexp.MustCompile(
	`^\s*(?:async\s+)?(?:def|class|func|function|const|export\s+(?:async\s+)?function)\s+([\w.]+)`)

// Nodes finds every call site in the corpus.
func Nodes(c Corpus) []Node {
	var out []Node
	for _, f := range c.Files {
		for i, line := range f.Lines {
			// Gated the same way as the axis patterns: the `(?i)` alternation with word boundaries cost
			// 910ms over 1.1M lines, and almost no line contains any of these literals.
			if !mayBeCallSite(toLower(line)) {
				continue
			}
			if !callSite.MatchString(line) || isComment(line, f.Language) {
				continue
			}
			enclosing := ""
			for j := i; j >= 0 && j > i-80; j-- {
				if m := enclosingDef.FindStringSubmatch(f.Lines[j]); m != nil {
					enclosing = m[1]
					break
				}
			}
			id := enclosing
			if id == "" {
				id = fmt.Sprintf("%s:%d", f.Path, i+1)
			}
			out = append(out, Node{
				ID:        id,
				Enclosing: enclosing,
				Span:      Span{Path: f.Path, Line: i + 1, Text: strings.TrimSpace(line), Why: "calls a model"},
			})
		}
	}
	return out
}

// isComment skips commented-out code. A commented call site is not a call site, and reporting one sends
// a reader to a line that does nothing — which costs more trust than the finding was worth.
func isComment(line string, lang Language) bool {
	t := strings.TrimSpace(line)
	switch lang {
	case Python:
		return strings.HasPrefix(t, "#")
	case Go, TypeScript, JavaScript:
		return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*")
	}
	return false
}

// maxSpansPerAxis bounds how much of the customer's code goes into one prompt.
//
// 🔴 A bound rather than "everything that matched", because the excerpt becomes input tokens on a call
// with a MaxTokens ceiling — and a repository with four hundred `temperature=` lines would produce a
// prompt that is either truncated in the middle of a span or refused outright. Both fail; one fails
// silently.
const maxSpansPerAxis = 12

// ForAxis extracts evidence for one axis.
//
// 🔴 Convenience only. It builds a one-shot Index, which rescans the corpus for call sites — fine for a
// single axis, quadratic across nine. Anything asking about more than one axis must build an Index once
// and reuse it; the server does.
func ForAxis(c Corpus, axis string) Evidence { return NewIndex(c).ForAxis(axis) }

// Excerpt satisfies AxisSource for a bare Corpus. Same caveat as ForAxis: prefer Index.
func (c Corpus) Excerpt(axis string) (string, bool) { return NewIndex(c).Excerpt(axis) }

// LooksLikeAnAgent reports whether this repository calls a model. Same caveat as ForAxis: prefer Index.
func (c Corpus) LooksLikeAnAgent() (bool, string) { return NewIndex(c).LooksLikeAnAgent() }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
