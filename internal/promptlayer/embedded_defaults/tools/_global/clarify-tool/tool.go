package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "generate"
	}
	switch action {
	case "status":
		return toolcontract.Ok("clarify-tool", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "generate":
		prompt := strings.TrimSpace(asString(args["prompt"]))
		if prompt == "" {
			prompt = strings.TrimSpace(asString(args["request"]))
		}
		if prompt == "" {
			return toolcontract.Error("clarify-tool", toolcontract.ErrorCodeValidationError, "missing prompt/request", action, args), nil
		}
		required := asStringSlice(args["required_fields"])
		questions, reasons := generateQuestions(prompt, required)
		return toolcontract.Ok("clarify-tool", action, args, map[string]any{
			"needs_clarification": len(questions) > 0,
			"questions":           questions,
			"reasons":             reasons,
			"question_count":      len(questions),
			"timestamp":           time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("clarify-tool", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, _ := item.(string)
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func generateQuestions(prompt string, required []string) ([]string, []string) {
	p := strings.ToLower(prompt)
	questions := make([]string, 0, 3)
	reasons := make([]string, 0, 3)

	for _, field := range required {
		if len(questions) >= 3 {
			break
		}
		f := strings.ToLower(strings.TrimSpace(field))
		if f == "" {
			continue
		}
		if !strings.Contains(p, f) {
			questions = append(questions, "Can you specify the "+field+"?")
			reasons = append(reasons, "missing required field: "+field)
		}
	}

	ambiguousTerms := []string{"soon", "quickly", "best", "optimize", "improve", "fix it", "asap"}
	for _, t := range ambiguousTerms {
		if len(questions) >= 3 {
			break
		}
		if strings.Contains(p, t) {
			questions = append(questions, "What concrete success criteria should be used for \""+t+"\"?")
			reasons = append(reasons, "ambiguous term: "+t)
			break
		}
	}

	if len(questions) == 0 && len(prompt) < 12 {
		questions = append(questions, "Can you provide more detail about the desired outcome and constraints?")
		reasons = append(reasons, "request too short")
	}
	return questions, reasons
}
