package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := asString(args["action"])
	if action == "" {
		action = "run"
	}
	return toolcontract.Ok("fuzzy-match", action, args, map[string]any{"result": "ok", "timestamp": time.Now().UTC().Format(time.RFC3339)}), nil
}

func asString(v any) string { s, _ := v.(string); return s }
