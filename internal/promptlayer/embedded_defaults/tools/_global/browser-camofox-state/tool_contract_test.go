package tooldef

import "testing"

func TestExecuteContract(t *testing.T) {
	res, err := Execute(map[string]any{
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
	if res == nil {
		t.Fatal("response is nil")
	}
	if _, ok := res["tool_id"].(string); !ok {
		t.Fatalf("missing tool_id string: %#v", res["tool_id"])
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
		t.Fatalf("missing audit object: %#v", res["audit"])
	}
	for _, key := range []string{"timestamp", "session_id", "workdir", "action"} {
		v, ok := audit[key].(string)
		if !ok || v == "" {
			t.Fatalf("invalid audit.%s: %#v", key, audit[key])
		}
	}
	if status == "error" {
		code, ok := res["error_code"].(string)
		if !ok {
			t.Fatalf("missing error_code: %#v", res["error_code"])
		}
		allowed := map[string]bool{
			"INVALID_ACTION":    true,
			"VALIDATION_ERROR":  true,
			"PERMISSION_DENIED": true,
			"POLICY_BLOCKED":    true,
			"IO_ERROR":          true,
			"NOT_FOUND":         true,
			"COMMAND_FAILED":    true,
			"NETWORK_ERROR":     true,
			"UNKNOWN_ERROR":     true,
		}
		if !allowed[code] {
			t.Fatalf("error_code not whitelisted: %s", code)
		}
	}
}
