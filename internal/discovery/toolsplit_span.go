package discovery

import (
	"strings"
)

// The syntactic tool split — the thing P14 pruning was ACTUALLY blocked on
// ───────────────────────────────────────────────────────────────────────
//
// `spanRewriteTools` refused for years with a sentence that named the wrong owner: "no pruner for this
// language has landed yet". It read like a rewriter gap and it was not one. Deleting a written element
// from a written list is the easiest edit the transform engine performs — no construction, no per-SDK
// knowledge, byte-safe. What was missing was the answer to *which written element is which tool*, and
// that answer lives here, in the frontend that is looking at the declaration.
//
// 🔴 So the frontend RECORDS it. The pruner may never infer it — not by position, not by text
// similarity, not by matching the selection's names against element text. Every one of those is a guess,
// and a prune that deletes the wrong element produces a diff that parses (D-14.5 part 3).
//
// 🚫 And "cannot locate" is recorded EXPLICITLY, as a tool with no location, rather than by omitting the
// tool. "This node offers tools we cannot address" and "this node offers no tools" are different facts,
// and only the first one may make a prune refuse — omission would make a prune report "no such tool"
// about a set that is plainly right there in the source.

// classifySyntacticToolsSkills splits a syntactic call site's declared entries into provider-native
// TOOLS and registered platform SKILLS, with each tool's declaration location, from the tools argument's
// span.
//
// It mirrors the Go frontend's classifyToolsSkills — same fail-closed default (an entry that is not
// provably a platform skill is a TOOL), same nil-when-empty result — because the two must produce the
// same IR shape for the same source. What differs is only how the elements are found: go/ast positions
// there, the shared list splitter here.
func classifySyntacticToolsSkills(s DetectedCallSite, unitSrc []byte, language string) ([]IRTool, []string) {
	loc := s.ArgMap.Tools
	if loc == nil || loc.Form != LocParamName {
		// The row names no tools argument for this entrypoint. Nothing is recorded, which is correct:
		// there is no tool set here to be unable to address.
		return nil, nil
	}
	v, written := s.Keywords[loc.Name]
	if !written {
		return nil, nil
	}
	if v.Value.Start < 0 || v.Value.End > len(unitSrc) || v.Value.Start > v.Value.End {
		return nil, nil
	}
	text := string(unitSrc[v.Value.Start:v.Value.End])

	if !IsWrittenList(language, text) {
		// 🔴 A tool set assembled at run time (`tools=build_tools(ctx)`), or a variable. RECORDED, as one
		// entry with no location, so a prune against it refuses by name instead of deleting nothing.
		name := strings.TrimSpace(text)
		if name == "" {
			return nil, nil
		}
		return []IRTool{{Name: name}}, nil
	}

	elems, err := SplitWrittenList(language, text)
	if err != nil {
		// The list is written but this engine cannot prove its boundaries — a spread, a comment, a
		// multi-line string. Recording HALF a list is the shape that deletes the wrong element, so the
		// whole list is recorded as one unlocatable entry and every prune over it refuses.
		return []IRTool{{Name: strings.TrimSpace(text)}}, nil
	}

	var tools []IRTool
	var skills []string
	for i, e := range elems {
		if name, isSkill := platformSkillName(e.Text); isSkill {
			skills = append(skills, name)
			continue
		}
		tools = append(tools, IRTool{
			Name: e.Text,
			DeclaredAt: &IRToolLocation{
				// Line is the node's own file, 1-based, counted from the unit source so it addresses the
				// same line the IR's node span does.
				Line:  lineOfOffset(unitSrc, v.Value.Start+e.Start),
				Index: i,
			},
		})
	}
	return tools, skills
}

// lineOfOffset counts 1-based lines up to an offset.
func lineOfOffset(src []byte, off int) int {
	if off > len(src) {
		off = len(src)
	}
	line := 1
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return line
}
