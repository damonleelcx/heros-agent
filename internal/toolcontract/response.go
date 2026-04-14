package toolcontract

import "time"

func BuildAudit(action string, args map[string]any) map[string]any {
	return map[string]any{
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"session_id": asString(args["_session_id"]),
		"workdir":    asString(args["_workdir"]),
		"action":     action,
	}
}

func Ok(toolID, action string, args map[string]any, data map[string]any) map[string]any {
	res := map[string]any{
		"tool_id": toolID,
		"status":  "ok",
		"action":  action,
		"audit":   BuildAudit(action, args),
	}
	for k, v := range data {
		res[k] = v
	}
	return res
}

func Error(toolID string, code ErrorCode, message, action string, args map[string]any, extra ...map[string]any) map[string]any {
	res := map[string]any{
		"tool_id":    toolID,
		"status":     "error",
		"error_code": string(code),
		"error":      message,
		"action":     action,
		"audit":      BuildAudit(action, args),
	}
	if len(extra) > 0 {
		for k, v := range extra[0] {
			res[k] = v
		}
	}
	return res
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
