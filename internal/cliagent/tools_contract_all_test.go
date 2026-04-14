package cliagent

import "testing"

func TestAllExtensionToolsContract(t *testing.T) {
	for toolID, exec := range extensionToolModules {
		t.Run(toolID, func(t *testing.T) {
			res, err := exec(map[string]any{
				"action":      "contract_check",
				"_workdir":    ".",
				"_session_id": "contract-test",
				"policy": map[string]any{
					"allow_exec":      true,
					"allow_write":     true,
					"allow_admin":     true,
					"allow_sync":      true,
					"allow_dangerous": false,
				},
			})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			res = normalizeToolResponse(toolID, map[string]any{
				"action":      "contract_check",
				"_workdir":    ".",
				"_session_id": "contract-test",
			}, res)
			if got, _ := res["tool_id"].(string); got != toolID {
				t.Fatalf("tool_id mismatch: got=%q want=%q", got, toolID)
			}
			status, ok := res["status"].(string)
			if !ok || (status != "ok" && status != "error") {
				t.Fatalf("invalid status: %#v", res["status"])
			}
			action, ok := res["action"].(string)
			if !ok || action == "" {
				t.Fatalf("missing action: %#v", res["action"])
			}
			audit, ok := res["audit"].(map[string]any)
			if !ok {
				t.Fatalf("missing audit map: %#v", res["audit"])
			}
			for _, k := range ToolAuditRequiredKeys {
				v, ok := audit[k].(string)
				if !ok || v == "" {
					t.Fatalf("invalid audit.%s: %#v", k, audit[k])
				}
			}
			if status == ToolStatusError {
				code, ok := res["error_code"].(string)
				if !ok || !IsAllowedToolErrorCode(ParseErrorCode(code)) {
					t.Fatalf("error_code not allowed: %#v", res["error_code"])
				}
			}
		})
	}
}
