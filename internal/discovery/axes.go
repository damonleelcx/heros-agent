package discovery

import (
	"fmt"
	"regexp"
	"sort"
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
		{regexp.MustCompile(`(?i)\bmodel\s*[=:]\s*["'\x60][\w.:/-]+`), "a model is named", 1, 1},
		{regexp.MustCompile(`(?i)\b(temperature|top_p|top_k|max_tokens|max_output_tokens)\s*[=:]`), "a sampling parameter is set", 2, 2},
		{regexp.MustCompile(`(?i)(gpt-[\w.]+|claude-[\w.-]+|deepseek-[\w.-]+|gemini-[\w.-]+|llama-?[\w.]*|mistral-[\w.]+|o[1-4]-\w+)`), "a model name appears", 1, 1},
	},
	"prompt": {
		{regexp.MustCompile(`(?i)\b(system_prompt|SYSTEM_PROMPT|system_message|instructions)\s*[=:]`), "a system prompt is defined", 1, 4},
		{regexp.MustCompile(`(?i)["']role["']\s*:\s*["']system["']`), "a system role message", 1, 3},
		{regexp.MustCompile(`(?i)\.(prompt|from_template|ChatPromptTemplate|PromptTemplate)\b`), "a prompt template", 1, 2},
	},
	"skills": {
		{regexp.MustCompile(`(?i)\b(skill|Skill)s?\s*[=:\[]`), "a skill binding", 1, 2},
		{regexp.MustCompile(`(?i)\b(load_skills|register_skill|SkillSet|capabilities)\b`), "a skill registry", 1, 2},
	},
	"tools": {
		{regexp.MustCompile(`(?i)\btools\s*[=:]\s*[\[\{]`), "a tool list is passed", 1, 4},
		{regexp.MustCompile(`(?i)@(tool|function_tool|openai_function)\b`), "a tool is declared", 0, 3},
		{regexp.MustCompile(`(?i)\b(tool_choice|function_call|tool_calls|bind_tools)\b`), "tool calling is configured", 1, 2},
	},
	"context": {
		{regexp.MustCompile(`(?i)\bmessages\s*[=+]|\bmessages\.append|\bmessages\s*\+=`), "the message list is built", 2, 3},
		{regexp.MustCompile(`(?i)\b(history|conversation|transcript)\b.*\[|\[.*\b(history|conversation)\b`), "history is spliced into a call", 1, 2},
		{regexp.MustCompile(`(?i)\b(truncat|summari[sz]|window|trim_messages|token_limit)\w*`), "history is trimmed or summarised", 1, 2},
	},
	"memory": {
		{regexp.MustCompile(`(?i)\b(ConversationBufferMemory|ConversationSummaryMemory|\bMemory\b|memory\s*[=:])`), "a memory object", 1, 3},
		{regexp.MustCompile(`(?i)\b(save_context|load_memory|persist|checkpointer|thread_id|session_id)\b`), "state is persisted between calls", 1, 2},
		{regexp.MustCompile(`(?i)\b(vectorstore|embedding|faiss|chroma|pinecone|weaviate|qdrant)\b`), "a retrieval store", 1, 2},
	},
	"harness": {
		{regexp.MustCompile(`(?i)\b(timeout|max_retries|retry|backoff|rate_limit|semaphore)\s*[=:(]`), "a limit or retry policy", 1, 2},
		{regexp.MustCompile(`(?i)\b(try:|except |catch\s*\(|recover\(\))`), "error handling around a call", 1, 3},
		{regexp.MustCompile(`(?i)\b(budget|max_cost|spend|token_budget|max_tokens_total)\b`), "a spend ceiling", 1, 2},
	},
	"loop": {
		{regexp.MustCompile(`(?i)\b(max_turns|max_iterations|max_steps|max_loops)\b`), "an iteration ceiling", 1, 2},
		{regexp.MustCompile(`^\s*(while|for)\s+.*(True|true|;;)\s*:?\s*\{?\s*$`), "an unbounded loop", 0, 5},
		{regexp.MustCompile(`(?i)\b(AgentExecutor|ReAct|reflect|critique|self_critique)\b`), "an agent control loop", 1, 3},
	},
	"graph": {
		{regexp.MustCompile(`(?i)\b(add_edge|add_node|add_conditional_edges|StateGraph|Workflow|DAG)\b`), "a graph is constructed", 1, 3},
		{regexp.MustCompile(`(?i)\b(handoff|delegate|route_to|next_agent|supervisor)\b`), "control passes between agents", 1, 2},
	},
}

// anySignal is the union of every axis pattern, used as a GATE.
//
// 🔴 Why a gate rather than just running the patterns
//
// The axis table holds about thirty case-insensitive regexes. Running all of them against every line of
// a large repository is ~30 million regex evaluations, and it took 17 seconds on a 2,541-file tree. The
// overwhelming majority of lines match nothing at all, so one combined pattern answers "is this line
// worth looking at?" in a single pass, and the thirty only run on the few percent that survive.
//
// It is built FROM the same table it gates, at init, so a pattern added to an axis is automatically in
// the gate. A hand-maintained gate would eventually omit a signal, and the symptom — one axis quietly
// finding less than it should — is invisible without a fixture that already knows the answer.
var anySignal = func() *regexp.Regexp {
	var alts []string
	for _, pats := range axisPatterns {
		for _, p := range pats {
			alts = append(alts, "(?:"+p.re.String()+")")
		}
	}
	sort.Strings(alts) // deterministic construction, so the compiled program is identical every run
	return regexp.MustCompile(strings.Join(alts, "|"))
}()

// callSite matches a line that calls a model. Used to locate NODES, which is a different question from
// axis evidence: a node is a place the agent thinks, and the axes describe how it thinks there.
var callSite = regexp.MustCompile(`(?i)\b(` +
	`chat\.completions\.create|messages\.create|generate_content|` +
	`ChatOpenAI|ChatAnthropic|ChatDeepSeek|ChatGoogleGenerativeAI|` +
	`\.invoke\(|\.ainvoke\(|\.run\(|\.complete\(|\.chat\(|` +
	`openai\.|anthropic\.|deepseek\.|litellm\.` +
	`)`)

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
